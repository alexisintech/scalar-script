package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"clerk/api/fapi/v1/router"
	"clerk/api/shared/jwt"
	"clerk/api/shared/sso"
	"clerk/pkg/apiversioning"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/externalapis/turnstile"
	"clerk/pkg/handlers"
	"clerk/pkg/pubsub"
	"clerk/pkg/sentry"
	"clerk/pkg/storage/google"
	"clerk/utils/clerk"
	"clerk/utils/log"

	"cloud.google.com/go/profiler"
	"github.com/volatiletech/sqlboiler/v4/boil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func main() {
	missingEnvVars := cenv.MissingEnvironmentVariables()
	if len(missingEnvVars) > 0 {
		panic(fmt.Sprintf("Missing Environment Variables: %+v", missingEnvVars))
	}
	// FAPI only required env variables
	if err := cenv.Require(
		cenv.BillingStripeSecretKey,
		cenv.CloudflareTurnstileSecretKeyInvisible,
		cenv.CloudflareTurnstileSiteKeyInvisible,
		cenv.CloudflareTurnstileSecretKeyManaged,
		cenv.CloudflareTurnstileSiteKeyManaged,
		cenv.FetchDevSessionFromFEClerkJSVersion,
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

	// Start the HTTP server.
	commonHandlers := handlers.NewCommon(deps.DB())

	captchaClientPool, err := turnstile.NewClientPool(turnstile.WithKeys(
		cenv.Get(cenv.CloudflareTurnstileSecretKeyInvisible),
		cenv.Get(cenv.CloudflareTurnstileSecretKeyManaged),
	))
	if err != nil {
		panic(err)
	}

	paymentProvider := clerkbilling.NewStripePaymentProvider(deps.GueClient())

	// Initialize billing connector for Stripe
	stripeConnectorConfig := clerkbilling.StripeConnectorConfig{SecretKey: cenv.Get(cenv.BillingStripeSecretKey)}
	billingConnector, err := clerkbilling.NewStripeConnector(stripeConnectorConfig)
	if err != nil {
		panic(err)
	}

	r := router.New(deps, captchaClientPool, commonHandlers, billingConnector, paymentProvider)

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
