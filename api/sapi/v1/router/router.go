package router

import (
	"database/sql"
	"net/http"

	"clerk/api/middleware"
	"clerk/api/sapi/v1/applications"
	"clerk/api/sapi/v1/domains"
	"clerk/api/sapi/v1/emaildomains"
	"clerk/api/sapi/v1/environment"
	"clerk/api/sapi/v1/instances"
	"clerk/api/sapi/v1/pricing"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/handlers"
	"clerk/utils/clerk"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	sdkhttp "github.com/clerk/clerk-sdk-go/v2/http"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	sentry "github.com/getsentry/sentry-go/http"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	chitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-chi/chi.v5"
)

type Router struct {
	authorizedParties []string
	common            *handlers.Common
	deps              clerk.Deps
	jwksClient        *jwks.Client
	sdkClientConfig   *sdk.ClientConfig

	applications *applications.HTTP
	domains      *domains.HTTP
	emailQuality *emaildomains.HTTP
	environment  *environment.HTTP
	instances    *instances.HTTP
	pricing      *pricing.HTTP
}

// NewRouter initializes a new support router.
func NewRouter(
	deps clerk.Deps,
	paymentProvider billing.PaymentProvider,
	sdkClientConfig *sdk.ClientConfig,
	common *handlers.Common,
	authorizedParties []string,
) *Router {
	return &Router{
		authorizedParties: authorizedParties,
		common:            common,
		deps:              deps,
		jwksClient:        jwks.NewClient(sdkClientConfig),
		sdkClientConfig:   sdkClientConfig,

		applications: applications.NewHTTP(deps.DB()),
		domains:      domains.NewHTTP(deps),
		emailQuality: emaildomains.NewHTTP(deps),
		environment:  environment.NewHTTP(deps.DB()),
		instances:    instances.NewHTTP(deps.DB(), deps.GueClient()),
		pricing:      pricing.NewHTTP(deps.Clock(), deps.DB(), paymentProvider),
	}
}

// BuildRoutes builds a router for the dashboard
func (router Router) BuildRoutes() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Recover)

	if cenv.IsEnabled(cenv.ClerkDatadogTracer) {
		r.Use(chitrace.Middleware())
	}

	r.Use(sentry.New(sentry.Options{Repanic: true}).Handle)

	r.Use(middleware.SetTraceID)
	r.Use(middleware.SetResponseTypeToJSON)
	r.Use(middleware.Log(func() sql.DBStats {
		return router.deps.DB().Conn().Stats()
	}))

	// StripV1 is intentionally mounted after our observability middleware, so that the
	// true path that the request is being routed to is logged.
	r.Use(middleware.StripV1)

	r.Use(chimw.StripSlashes)

	r.Method(http.MethodGet, "/health", router.common.Health())
	r.Method(http.MethodHead, "/health", router.common.Health())

	r.Route("/", func(r chi.Router) {
		r.Use(corsHandler(router.authorizedParties))
		r.Use(sdkhttp.RequireHeaderAuthorization(sdkhttp.JWKSClient(router.jwksClient), sdkhttp.AuthorizedPartyMatches(router.authorizedParties...)))

		r.Route("/applications", func(r chi.Router) {
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.applications.GetApplications))
			r.Method(http.MethodGet, "/{applicationID}", clerkhttp.Handler(router.applications.Read))
			r.Method(http.MethodPatch, "/{applicationID}", clerkhttp.Handler(router.applications.Update))
		})

		r.Route("/email_quality", func(r chi.Router) {
			r.Method(http.MethodPost, "/check", clerkhttp.Handler(router.emailQuality.CheckQuality))
			r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.emailQuality.UpdateQuality))
		})

		r.Route("/email_domains/{emailDomain}", func(r chi.Router) {
			r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.emailQuality.Update))
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.emailQuality.Read))
		})

		r.Route("/instances", func(r chi.Router) {
			r.Route("/{instanceID}", func(r chi.Router) {
				r.Use(clerkhttp.Middleware(router.environment.LoadToContext))
				r.Use(clerkhttp.Middleware(notDeleted))
				r.Use(clerkhttp.Middleware(checkUpdateOnSystemApplication))
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.instances.Read))

				r.Method(http.MethodPatch, "/user_limits", clerkhttp.Handler(router.instances.UpdateUserLimits))
				r.Method(http.MethodPatch, "/organization_settings", clerkhttp.Handler(router.instances.UpdateOrganizationSettings))
				r.Method(http.MethodPatch, "/sms_settings", clerkhttp.Handler(router.instances.UpdateSMSSettings))
				r.Method(http.MethodPost, "/purge_cache", clerkhttp.Handler(router.instances.PurgeCache))

				r.Method(http.MethodGet, "/domains", clerkhttp.Handler(router.domains.List))
			})
		})

		r.Route("/pricing", func(r chi.Router) {
			r.Route("/enterprise_plans", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.pricing.ListEnterprisePlans))
				r.Method(http.MethodPost, "/", clerkhttp.Handler(router.pricing.CreateEnterprisePlan))

				r.Route("/{planID}", func(r chi.Router) {
					r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.pricing.AssignToApplications))
				})
			})

			r.Route("/trials", func(r chi.Router) {
				r.Method(http.MethodPatch, "/{applicationID}", clerkhttp.Handler(router.pricing.SetTrialForApplication))
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.pricing.ListApplicationsWithTrials))
			})
		})
	})

	return r
}
