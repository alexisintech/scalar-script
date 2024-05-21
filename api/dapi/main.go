package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"clerk/api/dapi/v1/router"
	"clerk/api/shared/jwt"
	"clerk/api/shared/sso"
	"clerk/pkg/apiversioning"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/externalapis/clerkimages"
	"clerk/pkg/externalapis/svix"
	"clerk/pkg/handlers"
	"clerk/pkg/pubsub"
	sdkutils "clerk/pkg/sdk"
	"clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/pkg/storage/google"
	"clerk/pkg/vercel"
	"clerk/utils/clerk"
	"clerk/utils/log"

	"cloud.google.com/go/profiler"
	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/stripe/stripe-go/v72"
	"github.com/volatiletech/sqlboiler/v4/boil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func main() {
	missingEnvVars := cenv.MissingEnvironmentVariables()
	if len(missingEnvVars) > 0 {
		panic(fmt.Sprintf("Missing Environment Variables: %+v", missingEnvVars))
	}

	// DAPI only required env variables
	if err := cenv.Require(
		cenv.DNSEntryCacheExpiryInSeconds,
		cenv.BillingStripeSecretKey,
		cenv.BillingStripeClientID,
		cenv.BillingOAuthConnectCallbackURL,
	); err != nil {
		panic(err)
	}

	apiversioning.RegisterAllVersions()

	sso.RegisterOAuthProviders()
	sso.RegisterWeb3Providers()
	sso.RegisterSAMLProviders()

	logger := log.New()
	defer logger.Flush()

	err := sentry.Init(
		cenv.Get(cenv.SentryURL),
		cenv.Get(cenv.ClerkEnv),
		cenv.Get(cenv.SentryIgnoredStatusCodes),
	)
	if err != nil {
		logger.Error("failed starting Sentry: %s", err)
	} else {
		defer sentry.Flush()
	}

	if cenv.IsEnabled(cenv.GoogleCloudProfiler) {
		err = profiler.Start(profiler.Config{Service: cenv.Get(cenv.ClerkServiceIdentifier)})
		if err != nil {
			logger.Error("profiler: start: %s", err)
		}
	}

	if cenv.IsEnabled(cenv.ClerkDebugMode) {
		boil.DebugMode = true
		boil.DebugWriter = logger.Writer()
	}

	if cenv.IsEnabled(cenv.ClerkDatadogTracer) {
		//GitCommitSHA is not a required env var, but if it's available use it
		tracerOpts := []tracer.StartOption{
			tracer.WithEnv(cenv.Get(cenv.ClerkEnv)),
			tracer.WithService(cenv.Get(cenv.ClerkServiceIdentifier)),
		}
		if cenv.IsSet(cenv.GitCommitSHA) {
			tracerOpts = append(tracerOpts, tracer.WithServiceVersion(cenv.Get(cenv.GitCommitSHA)))
		}
		tracer.Start(tracerOpts...)
		defer tracer.Stop()
	}

	storageClient, err := google.NewClient(context.Background(), cenv.Get(cenv.GoogleStorageBucket))
	if err != nil {
		panic(err)
	}

	pubsubEventsTopic := pubsub.EventsTopic()
	deps := clerk.NewDeps(logger, clerk.WithStorageClient(storageClient), clerk.WithPubsubEventTopic(pubsubEventsTopic))

	defer func() {
		err := deps.SegmentClient().Close()
		if err != nil {
			logger.Error("error closing Segment Client: %s", err)
		}

		err = deps.StatsdClient().Close()
		if err != nil {
			logger.Error("error closing StatsD client: %s", err)
		}
	}()

	jwt.RegisterServiceVendors(deps.Clock())

	// Initialize Stripe
	stripe.Key = cenv.Get(cenv.StripeSecretKey)
	paymentProvider := billing.NewStripePaymentProvider(deps.GueClient())

	// Initialize billing connector for Stripe
	stripeConnectorConfig := billing.StripeConnectorConfig{SecretKey: cenv.Get(cenv.BillingStripeSecretKey)}
	billingConnector, err := billing.NewStripeConnector(stripeConnectorConfig)
	if err != nil {
		panic(err)
	}

	svixClient := svix.NewClient(&svix.ClientOptions{
		APIToken: cenv.Get(cenv.SvixAPIToken),
	})

	clerkImagesClient := clerkimages.NewClient(
		cenv.Get(cenv.ClerkImageServiceAPIKey),
		cenv.Get(cenv.ClerkImageServiceURL),
	)

	vercelClient := vercel.NewClient(deps.DB(), deps.Clock(), nil)

	commonHandlers := handlers.NewCommon(deps.DB())

	// NOTE: Currently our Go SDK does not support a dynamic matcher against AZP making it hard to support
	// Vercel Preview deployments.
	//
	// As a workaround until Go SDK V2 is released, we temporarily disabled the AZP check for staging
	// Dashboard. For more information please refer to the following slack conversation.
	//
	// https://clerkinc.slack.com/archives/C06FGDX7MRD/p1706523543427249?thread_ts=1706470714.542809&cid=C06FGDX7MRD
	authorizedParties := []string{}
	if cenv.IsProduction() || cenv.IsDevelopment() {
		authorizedParties = set.New(strings.Split(cenv.Get(cenv.ClerkDashboardAZP), ",")...).Array()
	}

	// Configuration for SDK clients per customer instance. Clerk impersonates a Clerk customer.
	sdkConfigConstructor := sdkutils.NewConfigConstructor(cenv.Get(cenv.ClerkServerAPI), cenv.IsEnabled(cenv.ClerkDatadogTracer))

	// Configuration for SDK clients for the Clerk instance. Clerk is also a Clerk customer.
	dapiSDKClientConfig := &sdk.ClientConfig{}
	dapiSDKClientConfig.Key = sdk.String(cenv.Get(cenv.ClerkDashboardAPIKey))
	dapiSDKClientConfig.URL = sdk.String(cenv.Get(cenv.ClerkServerAPI))

	router := router.NewRouter(
		deps,
		sdkConfigConstructor,
		dapiSDKClientConfig,
		svixClient,
		clerkImagesClient,
		vercelClient,
		commonHandlers,
		billingConnector,
		paymentProvider,
		authorizedParties,
	)

	// Start the HTTP server.
	port := cenv.Get(cenv.Port)
	ctxTimeout := cenv.GetInt(cenv.ContextTimeoutSeconds)
	writeTimeout := ctxTimeout + 2 // write timeout should be longer than context timeout

	server := http.Server{
		Addr:         ":" + port,
		Handler:      http.TimeoutHandler(router.BuildRoutes(), time.Duration(ctxTimeout)*time.Second, ""),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: time.Duration(writeTimeout) * time.Second,
	}

	logger.Info("Up on port %s (pid=%d)", port, os.Getpid())
	logger.Fatal(server.ListenAndServe())
}
