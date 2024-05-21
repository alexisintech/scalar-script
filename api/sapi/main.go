package main

import (
	"net/http"
	"os"
	"strings"
	"time"

	"clerk/api/sapi/v1/router"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/handlers"
	"clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/utils/clerk"
	"clerk/utils/log"

	"cloud.google.com/go/profiler"
	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/stripe/stripe-go/v72"
	"github.com/volatiletech/sqlboiler/v4/boil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func main() {
	// SAPI only required env variables
	if err := cenv.Require(
		cenv.ClerkDatabaseURL,
		cenv.ClerkRedisURL,
		cenv.CloudflarePurgeCacheAPIKey,
		cenv.ClerkEnv,
		cenv.ClerkServiceIdentifier,
		cenv.ClerkSupportAZP,
		cenv.ClerkSupportAPIKey,
		cenv.ClerkServerAPI,
		cenv.ClerkGodPlanID,
		cenv.StripeSecretKey,
	); err != nil {
		panic(err)
	}

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

	deps := clerk.NewDeps(logger)

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

	commonHandlers := handlers.NewCommon(deps.DB())
	authorizedParties := set.New(strings.Split(cenv.Get(cenv.ClerkSupportAZP), ",")...).Array()

	// Initialize Stripe
	stripe.Key = cenv.Get(cenv.StripeSecretKey)
	paymentProvider := billing.NewStripePaymentProvider(deps.GueClient())

	sdkClientConfig := &sdk.ClientConfig{
		BackendConfig: sdk.BackendConfig{
			URL: sdk.String(cenv.Get(cenv.ClerkServerAPI)),
			Key: sdk.String(cenv.Get(cenv.ClerkSupportAPIKey)),
			CustomRequestHeaders: &sdk.CustomRequestHeaders{
				Application: "support.clerk.app",
			},
		},
	}

	r := router.NewRouter(
		deps,
		paymentProvider,
		sdkClientConfig,
		commonHandlers,
		authorizedParties,
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
