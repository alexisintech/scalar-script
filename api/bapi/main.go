package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"clerk/api/bapi/v1/externalapp"
	"clerk/api/bapi/v1/internalapi"
	"clerk/api/bapi/v1/router"
	"clerk/api/shared/jwt"
	"clerk/api/shared/sso"
	"clerk/pkg/apiversioning"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/externalapis/svix"
	"clerk/pkg/handlers"
	"clerk/pkg/pubsub"
	"clerk/pkg/sentry"
	"clerk/pkg/storage/google"
	"clerk/utils/clerk"
	"clerk/utils/log"

	"cloud.google.com/go/profiler"
	"github.com/stripe/stripe-go/v72"
	"github.com/volatiletech/sqlboiler/v4/boil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func main() {
	missingEnvVars := cenv.MissingEnvironmentVariables()
	if len(missingEnvVars) > 0 {
		panic(fmt.Sprintf("Missing Environment Variables: %+v", missingEnvVars))
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

	if cenv.IsEnabled(cenv.ClerkDebugMode) {
		boil.DebugMode = true
		boil.DebugWriter = logger.Writer()
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

	// Initialize Stripe - This must come BEFORE the Gue worker initialization
	stripe.Key = cenv.Get(cenv.StripeSecretKey)
	paymentProvider := billing.NewStripePaymentProvider(deps.GueClient())

	// Initialize the Stripe billing connector
	stripeConnectorConfig := billing.StripeConnectorConfig{SecretKey: cenv.Get(cenv.BillingStripeSecretKey)}
	billingConnector, err := billing.NewStripeConnector(stripeConnectorConfig)
	if err != nil {
		panic(err)
	}

	commonHandlers := handlers.NewCommon(deps.DB())
	svixClient := svix.NewClient(&svix.ClientOptions{
		APIToken: cenv.Get(cenv.SvixAPIToken),
	})

	// Client for external app requests, like proxy config health check
	externalAppClient := externalapp.NewClient(&http.Client{Timeout: 1 * time.Second})

	// Client for making requests to BAPI internal endpoints
	internalClient := internalapi.NewClient(cenv.Get(cenv.ClerkServerAPI), nil)

	r := router.New(
		deps,
		commonHandlers,
		svixClient,
		billingConnector,
		paymentProvider,
		externalAppClient,
		internalClient,
	)

	// Start the HTTP server.
	port := cenv.Get(cenv.Port)
	ctxTimeout := cenv.GetInt(cenv.ContextTimeoutSeconds)
	writeTimeout := ctxTimeout + 2 // write timeout should be longer than context timeout

	server := http.Server{
		Addr:         ":" + port,
		Handler:      http.TimeoutHandler(r.BuildRoutes(), time.Duration(ctxTimeout)*time.Second, ""),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: time.Duration(writeTimeout) * time.Second,
	}

	logger.Info("Up on port %s (pid=%d)", port, os.Getpid())
	logger.Fatal(server.ListenAndServe())
}
