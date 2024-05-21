package router

import (
	"database/sql"
	"net/http"

	"clerk/api/apierror"
	"clerk/api/bapi/v1/actor_tokens"
	"clerk/api/bapi/v1/allowlist"
	"clerk/api/bapi/v1/authconfig"
	"clerk/api/bapi/v1/billing"
	"clerk/api/bapi/v1/blocklist"
	"clerk/api/bapi/v1/clients"
	"clerk/api/bapi/v1/comms"
	"clerk/api/bapi/v1/domains"
	"clerk/api/bapi/v1/edge_events"
	"clerk/api/bapi/v1/email_addresses"
	"clerk/api/bapi/v1/engineering"
	"clerk/api/bapi/v1/environment"
	"clerk/api/bapi/v1/externalapp"
	"clerk/api/bapi/v1/features"
	"clerk/api/bapi/v1/instance_organization_permissions"
	"clerk/api/bapi/v1/instance_organization_roles"
	"clerk/api/bapi/v1/instances"
	"clerk/api/bapi/v1/internalapi"
	"clerk/api/bapi/v1/interstitial"
	"clerk/api/bapi/v1/invitations"
	"clerk/api/bapi/v1/jwks"
	"clerk/api/bapi/v1/jwt_templates"
	"clerk/api/bapi/v1/messaging"
	"clerk/api/bapi/v1/oauth_applications"
	"clerk/api/bapi/v1/organization_invitations"
	"clerk/api/bapi/v1/organization_memberships"
	"clerk/api/bapi/v1/organizations"
	"clerk/api/bapi/v1/phone_numbers"
	"clerk/api/bapi/v1/proxy_checks"
	"clerk/api/bapi/v1/redirect_urls"
	"clerk/api/bapi/v1/saml_connections"
	"clerk/api/bapi/v1/scheduler"
	"clerk/api/bapi/v1/sessions"
	"clerk/api/bapi/v1/sign_in_tokens"
	"clerk/api/bapi/v1/sign_ups"
	"clerk/api/bapi/v1/smscountrytiers"
	supportOps "clerk/api/bapi/v1/support_ops"
	"clerk/api/bapi/v1/templates"
	"clerk/api/bapi/v1/testing_tokens"
	"clerk/api/bapi/v1/tokens"
	"clerk/api/bapi/v1/users"
	"clerk/api/bapi/v1/webhooks"
	"clerk/api/middleware"
	apiVersioningMiddleware "clerk/pkg/apiversioning/middleware"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/externalapis/svix"
	"clerk/pkg/handlers"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/utils/clerk"

	sentry "github.com/getsentry/sentry-go/http"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	chitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-chi/chi.v5"
)

// Router is responsible for request routing in server API
type Router struct {
	deps clerk.Deps

	// handlers
	common *handlers.Common

	// services
	allowlist         *allowlist.HTTP
	authConfig        *authconfig.HTTP
	billing           *billing.HTTP
	blocklist         *blocklist.HTTP
	scheduler         *scheduler.HTTP
	clients           *clients.HTTP
	comms             *comms.HTTP
	domains           *domains.HTTP
	emailAddresses    *email_addresses.HTTP
	engineering       *engineering.HTTP
	environment       *environment.HTTP
	features          *features.HTTP
	actorTokens       *actor_tokens.HTTP
	interstitial      *interstitial.HTTP
	instances         *instances.HTTP
	instanceOrgPerm   *instance_organization_permissions.HTTP
	instanceOrgRoles  *instance_organization_roles.HTTP
	invitations       *invitations.HTTP
	jwks              *jwks.HTTP
	jwtTemplates      *jwt_templates.HTTP
	messaging         *messaging.HTTP
	orgInvitations    *organization_invitations.HTTP
	orgMemberships    *organization_memberships.HTTP
	organizations     *organizations.HTTP
	supportOps        *supportOps.HTTP
	phoneNumbers      *phone_numbers.HTTP
	proxyChecks       *proxy_checks.HTTP
	redirectURLs      *redirect_urls.HTTP
	samlConnections   *saml_connections.HTTP
	sessions          *sessions.HTTP
	signInTokens      *sign_in_tokens.HTTP
	signUps           *sign_ups.HTTP
	templates         *templates.HTTP
	testingTokens     *testing_tokens.HTTP
	tokens            *tokens.HTTP
	users             *users.HTTP
	webhooks          *webhooks.HTTP
	oauthApplications *oauth_applications.HTTP
	edgeEventsService *edge_events.HTTP
	smsCountryTiers   *smscountrytiers.HTTP
}

// New builds a new router
func New(
	deps clerk.Deps,
	common *handlers.Common,
	svixClient *svix.Client,
	billingConnector clerkbilling.Connector,
	paymentProvider clerkbilling.PaymentProvider,
	externalAppClient *externalapp.Client,
	internalClient *internalapi.Client,
) *Router {
	return &Router{
		deps:       deps,
		common:     common,
		allowlist:  allowlist.NewHTTP(deps),
		authConfig: authconfig.NewHTTP(deps.DB(), deps.GueClient()),
		billing:    billing.NewHTTP(deps, billingConnector),
		blocklist:  blocklist.NewHTTP(deps.DB()),
		scheduler: scheduler.NewHTTP(
			deps,
			paymentProvider,
			deps.DNSResolver(),
		),
		clients:           clients.NewHTTP(deps),
		comms:             comms.NewHTTP(deps),
		domains:           domains.NewHTTP(deps, externalAppClient, internalClient),
		engineering:       engineering.NewHTTP(deps.Cache()),
		environment:       environment.NewHTTP(deps.DB()),
		emailAddresses:    email_addresses.NewHTTP(deps),
		features:          features.NewHTTP(deps.DB()),
		actorTokens:       actor_tokens.NewHTTP(deps),
		instances:         instances.NewHTTP(deps, externalAppClient, internalClient),
		instanceOrgPerm:   instance_organization_permissions.NewHTTP(deps),
		instanceOrgRoles:  instance_organization_roles.NewHTTP(deps),
		interstitial:      interstitial.NewHTTP(),
		invitations:       invitations.NewHTTP(deps),
		jwks:              jwks.NewHTTP(),
		jwtTemplates:      jwt_templates.NewHTTP(deps.DB(), deps.GueClient(), deps.Clock()),
		messaging:         messaging.NewHTTP(deps),
		orgInvitations:    organization_invitations.NewHTTP(deps),
		orgMemberships:    organization_memberships.NewHTTP(deps),
		organizations:     organizations.NewHTTP(deps),
		phoneNumbers:      phone_numbers.NewHTTP(deps),
		supportOps:        supportOps.NewHTTP(deps),
		proxyChecks:       proxy_checks.NewHTTP(deps.Clock(), deps.DB(), deps.GueClient(), externalAppClient, internalClient),
		redirectURLs:      redirect_urls.NewHTTP(deps.DB(), deps.Clock()),
		samlConnections:   saml_connections.NewHTTP(deps),
		sessions:          sessions.NewHTTP(deps),
		signInTokens:      sign_in_tokens.NewHTTP(deps.Clock(), deps.DB()),
		signUps:           sign_ups.NewHTTP(deps),
		templates:         templates.NewHTTP(deps.Clock(), deps.DB()),
		testingTokens:     testing_tokens.NewHTTP(deps.Clock()),
		tokens:            tokens.NewHTTP(deps),
		users:             users.NewHTTP(deps),
		webhooks:          webhooks.NewHTTP(deps.DB(), svixClient),
		oauthApplications: oauth_applications.NewHTTP(deps),
		edgeEventsService: edge_events.NewHTTP(deps),
		smsCountryTiers:   smscountrytiers.NewHTTP(deps),
	}
}

// BuildRoutes returns a mux with all routes for the server API
func (router *Router) BuildRoutes() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Recover)

	if cenv.IsEnabled(cenv.ClerkDatadogTracer) {
		r.Use(chitrace.Middleware(chitrace.WithServiceName(cenv.Get(cenv.ClerkServiceIdentifier))))
	}

	// report panics to Sentry and re-panic. Also, populate a Sentry Hub
	// into the context.
	//
	// NOTE: This must come after middleware.Recover and before
	// middleware.SetTraceID
	r.Use(sentry.New(sentry.Options{Repanic: true}).Handle)

	r.Use(middleware.SetTraceID)
	r.Use(clerkhttp.Middleware(middleware.SetMaintenanceAndRecoveryMode))
	r.Use(middleware.SetResponseTypeToJSON)
	r.Use(clerkhttp.Middleware(parseForm))
	r.Use(clerkhttp.Middleware(validateCharSet))
	r.Use(middleware.Log(func() sql.DBStats {
		return router.deps.DB().Conn().Stats()
	}))
	r.Use(chimw.StripSlashes)
	r.Use(clerkhttp.Middleware(checkRequestAllowedDuringMaintenance))

	// Public routes
	r.Method(http.MethodGet, "/v1/health", router.common.Health())
	r.Method(http.MethodHead, "/v1/health", router.common.Health())

	r.Route("/v1/public", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(clerkhttp.Middleware(infiniteRedirectLoop()))

			r.Method(http.MethodGet, "/interstitial", clerkhttp.Handler(router.interstitial.RenderPublic))
		})

		r.Method(http.MethodPost, "/demo_instance", clerkhttp.Handler(router.instances.CreateDemoInstance))
	})

	// incoming webhooks / events
	r.Route("/v1/events", func(r chi.Router) {
		r.Method(http.MethodPost, "/twilio_sms_status", clerkhttp.Handler(router.messaging.TwilioSMSStatusCallback))
		r.Method(http.MethodPost, "/stripe", clerkhttp.Handler(router.billing.StripeWebhook))
	})

	r.Route("/v1/internal", func(r chi.Router) {
		r.Route("/edge-events", func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.edgeEventsService.ValidateGoogleJWT))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.edgeEventsService.HandlePubsubPush))
		})

		// This endpoint is unauthenticated and meant to be consumed by our marketing site.
		// It is also cached in Cloudflare using a cache rule.
		r.Method(http.MethodGet, "/pricing/sms_country_tiers", clerkhttp.Handler(router.smsCountryTiers.GetCountryTiers))

		r.Route("/support-ops", func(r chi.Router) {
			r.Route("/customer-data", func(r chi.Router) {
				r.Use(clerkhttp.Middleware(router.supportOps.EnsureValidToken(cenv.ClerkTokenForSupportOps)))
				r.Method(http.MethodGet, "/{userID}", clerkhttp.Handler(router.supportOps.CustomerData))
			})

			r.Route("/plain", func(r chi.Router) {
				r.Use(clerkhttp.Middleware(router.supportOps.EnsureValidToken(cenv.ClerkTokenForPlain)))
				r.Method(http.MethodPost, "/customercards", clerkhttp.Handler(router.supportOps.CustomerCards))
			})
		})

		r.Group(func(r chi.Router) { // scheduler
			r.Use(clerkhttp.Middleware(router.scheduler.CheckSchedulerToken))

			r.Method(http.MethodPost, "/cleanup/dead_sessions_job", clerkhttp.Handler(router.scheduler.DeadSessionsJob))
			r.Method(http.MethodPost, "/cleanup/orphan_applications", clerkhttp.Handler(router.scheduler.OrphanApplications))
			r.Method(http.MethodPost, "/cleanup/orphan_organizations", clerkhttp.Handler(router.scheduler.OrphanOrganizations))
			r.Method(http.MethodPost, "/cleanup/expired_oauth_tokens", clerkhttp.Handler(router.scheduler.ExpiredOAuthTokens))
			r.Method(http.MethodPost, "/stripe/usage_report_jobs", clerkhttp.Handler(router.scheduler.StripeUsageReportJobs))
			r.Method(http.MethodPost, "/stripe/sync_plans", clerkhttp.Handler(router.scheduler.SyncStripePlans))
			r.Method(http.MethodPost, "/stripe/refresh_cache_responses", clerkhttp.Handler(router.scheduler.StripeRefreshCacheResponses))
			r.Method(http.MethodPost, "/cloudflare/monitor_custom_hostname", clerkhttp.Handler(router.scheduler.MonitorCustomHostname))
			r.Method(http.MethodPost, "/dns/enqueue_checks", clerkhttp.Handler(router.scheduler.DNSChecks))
			r.Method(http.MethodPost, "/email_domain_reports/populate_disposable", clerkhttp.Handler(router.scheduler.PopulateDisposableEmailDomains))
			r.Method(http.MethodPost, "/email_domain_reports/populate_common", clerkhttp.Handler(router.scheduler.PopulateCommonEmailDomains))
			r.Method(http.MethodPost, "/hype_stats", clerkhttp.Handler(router.scheduler.CreateHypeStats))
			r.Method(http.MethodPost, "/webauthn/refresh_authenticator_data", clerkhttp.Handler(router.scheduler.RefreshWebAuthnAuthenticatorData))

			r.Route("/engineering-ops", func(r chi.Router) {
				r.Method(http.MethodPost, "/github/generate_pr_review_report", clerkhttp.Handler(router.scheduler.GeneratePRReviewReport))

				r.Route("/cache/{key}", func(r chi.Router) {
					r.Method(http.MethodPost, "/", clerkhttp.Handler(router.engineering.Set))
					r.Method(http.MethodGet, "/", clerkhttp.Handler(router.engineering.Get))
					r.Method(http.MethodGet, "/exists", clerkhttp.Handler(router.engineering.Exists))
				})
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.environment.SetEnvironmentFromHeader))
			r.Use(clerkhttp.Middleware(middleware.EnsureEnvNotPendingDeletion))
			r.Use(clerkhttp.Middleware(logClerkSDKVersion))
			r.Use(clerkhttp.Middleware(apiVersioningMiddleware.SetAPIVersionFromHeader))

			r.Method(http.MethodGet, "/interstitial", clerkhttp.Handler(router.interstitial.RenderPrivate))
			r.Method(http.MethodGet, "/whatismyip", clerkhttp.Handler(router.common.WhatIsMyIP))
		})

		r.Method(http.MethodGet, "/proxy_image_url", clerkhttp.Handler(router.users.ProxyImageURL))
	})

	r.Route("/v1", func(r chi.Router) {
		r.Use(clerkhttp.Middleware(router.environment.SetEnvironmentFromHeader))
		r.Use(clerkhttp.Middleware(middleware.EnsureEnvNotPendingDeletion))
		r.Use(clerkhttp.Middleware(logClerkSDKVersion))
		r.Use(clerkhttp.Middleware(apiVersioningMiddleware.SetAPIVersionFromHeader))

		r.Method(http.MethodGet, "/jwks", clerkhttp.Handler(router.jwks.Read))

		r.Route("/clients", func(r chi.Router) {
			r.Method(http.MethodPost, "/verify", clerkhttp.Handler(router.clients.Verify))
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.clients.ReadAll))
			r.Route("/{clientID}", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Method(http.MethodGet, "/", clerkhttp.Handler(router.clients.Read))
				})
			})
		})

		r.Route("/sessions", func(r chi.Router) {
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.sessions.ReadAll))
			r.Route("/{sessionID}", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Method(http.MethodGet, "/", clerkhttp.Handler(router.sessions.Read))
					r.Method(http.MethodPost, "/revoke", clerkhttp.Handler(router.sessions.Revoke))
					r.Method(http.MethodPost, "/verify", clerkhttp.Handler(router.sessions.Verify))

					r.Route("/tokens", func(r chi.Router) {
						r.Method(http.MethodPost, "/{templateName}", clerkhttp.Handler(router.sessions.CreateTokenFromTemplate))
					})
				})
			})
		})

		r.Route("/emails", func(r chi.Router) {
			r.Use(clerkhttp.Middleware(middleware.EnabledInUserSettings(names.EmailAddress)))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.comms.CreateEmail))
		})

		r.Route("/sms_messages", func(r chi.Router) {
			r.Use(clerkhttp.Middleware(middleware.EnabledInUserSettings(names.PhoneNumber)))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.comms.CreateSMS))
		})

		r.Route("/templates/{template_type}", func(r chi.Router) {
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.templates.List))

			r.Route("/{slug}", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.templates.Read))
				r.Method(http.MethodPut, "/", clerkhttp.Handler(router.templates.Upsert))
				r.Method(http.MethodPost, "/revert", clerkhttp.Handler(router.templates.Revert))
				r.Method(http.MethodPost, "/preview", clerkhttp.Handler(router.templates.Preview))
				r.Method(http.MethodPost, "/toggle_delivery", clerkhttp.Handler(router.templates.ToggleDelivery))
				r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.templates.Delete))
			})
		})

		r.Route("/testing_tokens", func(r chi.Router) {
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.testingTokens.Create))
		})

		r.Route("/email_addresses", func(r chi.Router) {
			r.Use(clerkhttp.Middleware(middleware.EnabledInUserSettings(names.EmailAddress)))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.emailAddresses.Create))

			r.Route("/{emailAddressID}", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.emailAddresses.Read))
				r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.emailAddresses.Update))
				r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.emailAddresses.Delete))
			})
		})

		r.Route("/phone_numbers", func(r chi.Router) {
			r.Use(clerkhttp.Middleware(middleware.EnabledInUserSettings(names.PhoneNumber)))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.phoneNumbers.Create))

			r.Route("/{phoneNumberID}", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.phoneNumbers.Read))
				r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.phoneNumbers.Update))
				r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.phoneNumbers.Delete))
			})
		})

		r.Route("/users", func(r chi.Router) {
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.List))
			r.Method(http.MethodGet, "/count", clerkhttp.Handler(router.users.Count))

			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.users.Create))

			r.Route("/{userID}", func(r chi.Router) {
				r.Use(clerkhttp.Middleware(router.users.CheckUserInInstance))
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.Read))
				r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.users.Update))
				r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.users.Delete))

				r.Group(func(r chi.Router) {
					r.Use(clerkhttp.Middleware(router.features.CheckSupportedByPlan(clerkbilling.Features.BanUser)))
					r.Method(http.MethodPost, "/ban", clerkhttp.Handler(router.users.Ban))
					r.Method(http.MethodPost, "/unban", clerkhttp.Handler(router.users.Unban))
				})

				r.Method(http.MethodPost, "/lock", clerkhttp.Handler(router.users.Lock))
				r.Method(http.MethodPost, "/unlock", clerkhttp.Handler(router.users.Unlock))

				r.Method(http.MethodPatch, "/metadata", clerkhttp.Handler(router.users.UpdateMetadata))

				r.Method(http.MethodPost, "/profile_image", clerkhttp.Handler(router.users.UpdateProfileImage))
				r.Method(http.MethodDelete, "/profile_image", clerkhttp.Handler(router.users.DeleteProfileImage))

				r.Method(http.MethodGet, "/oauth_access_tokens/{provider}", clerkhttp.Handler(router.users.ListOAuthAccessTokens))

				r.Method(http.MethodPost, "/verify_password", clerkhttp.Handler(router.users.VerifyPassword))
				r.Method(http.MethodPost, "/verify_totp", clerkhttp.Handler(router.users.VerifyTOTP))

				r.Method(http.MethodDelete, "/mfa", clerkhttp.Handler(router.users.DisableMFA))

				r.Group(func(r chi.Router) {
					r.Use(clerkhttp.Middleware(router.organizations.CheckOrganizationsEnabled))

					r.Method(http.MethodGet, "/organization_memberships", clerkhttp.Handler(router.users.ListOrganizationMemberships))
				})
			})
		})

		r.Route("/webhooks", func(r chi.Router) {
			r.Method(http.MethodPost, "/svix", clerkhttp.Handler(router.webhooks.CreateSvix))
			r.Method(http.MethodDelete, "/svix", clerkhttp.Handler(router.webhooks.DeleteSvix))
			r.Method(http.MethodPost, "/svix_url", clerkhttp.Handler(router.webhooks.CreateSvixURL))
		})

		r.Route("/allowlist_identifiers", func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.features.CheckSupportedByPlan(clerkbilling.Features.Allowlist)))

			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.allowlist.ReadAll))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.allowlist.Create))
			r.Method(http.MethodDelete, "/{identifierID}", clerkhttp.Handler(router.allowlist.Delete))
		})

		r.Route("/blocklist_identifiers", func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.features.CheckSupportedByPlan(clerkbilling.Features.Blocklist)))

			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.blocklist.ReadAll))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.blocklist.Create))
			r.Method(http.MethodDelete, "/{identifierID}", clerkhttp.Handler(router.blocklist.Delete))
		})

		r.Route("/invitations", func(r chi.Router) {
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.invitations.Create))
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.invitations.ReadAll))
			r.Route("/{invitationID}", func(r chi.Router) {
				r.Method(http.MethodPost, "/revoke", clerkhttp.Handler(router.invitations.Revoke))
			})
		})

		r.Route("/organizations", func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.organizations.CheckOrganizationsEnabled))
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizations.List))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.organizations.Create))

			r.Route("/{organizationID}", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizations.Read))
				r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.organizations.Delete))
				r.Method(http.MethodPut, "/logo", clerkhttp.Handler(router.organizations.UpdateLogo))
				r.Method(http.MethodDelete, "/logo", clerkhttp.Handler(router.organizations.DeleteLogo))

				r.Group(func(r chi.Router) {
					r.Use(clerkhttp.Middleware(router.organizations.EnsureOrganizationExists))
					r.Use(clerkhttp.Middleware(router.organizations.EmitActiveOrganizationEventIfNeeded))

					r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.organizations.Update))
					r.Method(http.MethodPatch, "/metadata", clerkhttp.Handler(router.organizations.UpdateMetadata))

					r.Route("/invitations", func(r chi.Router) {
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.orgInvitations.Create))
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.orgInvitations.List))
						r.Method(http.MethodPost, "/bulk", clerkhttp.Handler(router.orgInvitations.CreateBulk))

						r.Group(func(r chi.Router) {
							r.Use(middleware.Deprecated)
							r.Method(http.MethodGet, "/pending", clerkhttp.Handler(router.orgInvitations.ListPending))
						})

						r.Route("/{invitationID}", func(r chi.Router) {
							r.Method(http.MethodGet, "/", clerkhttp.Handler(router.orgInvitations.Read))
							r.Method(http.MethodPost, "/revoke", clerkhttp.Handler(router.orgInvitations.Revoke))
						})
					})

					r.Route("/memberships", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.orgMemberships.List))
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.orgMemberships.Create))

						r.Route("/{userID}", func(r chi.Router) {
							r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.orgMemberships.Update))
							r.Method(http.MethodPatch, "/metadata", clerkhttp.Handler(router.orgMemberships.UpdateMetadata))
							r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.orgMemberships.Delete))
						})
					})
				})
			})
		})

		r.Route("/actor_tokens", func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.features.CheckSupportedByPlan(clerkbilling.Features.Impersonation)))

			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.actorTokens.Create))

			r.Route("/{actorTokenID}", func(r chi.Router) {
				r.Method(http.MethodPost, "/revoke", clerkhttp.Handler(router.actorTokens.Revoke))
			})
		})

		r.Route("/sign_in_tokens", func(r chi.Router) {
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.signInTokens.Create))

			r.Route("/{signInTokenID}", func(r chi.Router) {
				r.Method(http.MethodPost, "/revoke", clerkhttp.Handler(router.signInTokens.Revoke))
			})
		})

		r.Route("/sign_ups", func(r chi.Router) {
			r.Route("/{signUpID}", func(r chi.Router) {
				r.Use(clerkhttp.Middleware(router.signUps.CheckSignUpInInstance))
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.signUps.Read))
				r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.signUps.Update))
			})
		})

		r.Route("/jwt_templates", func(r chi.Router) {
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.jwtTemplates.ReadAll))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.jwtTemplates.Create))

			r.Route("/{templateID}", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.jwtTemplates.Read))
				r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.jwtTemplates.Update))
				r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.jwtTemplates.Delete))
			})
		})

		r.Route("/redirect_urls", func(r chi.Router) {
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.redirectURLs.ReadAll))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.redirectURLs.Create))

			r.Route("/{redirectURLID}", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.redirectURLs.Read))
				r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.redirectURLs.Delete))
			})
		})

		r.Route("/tokens", func(r chi.Router) {
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.tokens.CreateFromTemplate))
		})

		r.Route("/beta_features", func(r chi.Router) {
			r.Route("/instance_settings", func(r chi.Router) {
				r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.authConfig.Update))
			})
			r.Route("/domain", func(r chi.Router) {
				r.Method(http.MethodPut, "/", clerkhttp.Handler(router.instances.UpdateHomeURL))
			})
		})

		r.Route("/instance", func(r chi.Router) {
			r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.instances.Update))
			r.Method(http.MethodPatch, "/restrictions", clerkhttp.Handler(router.instances.UpdateRestrictions))
			r.Method(http.MethodPatch, "/organization_settings", clerkhttp.Handler(router.instances.UpdateOrganizationSettings))
			r.Method(http.MethodPost, "/change_domain", clerkhttp.Handler(router.instances.UpdateHomeURL))
		})

		r.Route("/domains", func(r chi.Router) {
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.domains.Create))
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.domains.List))

			r.Route("/{domainID}", func(r chi.Router) {
				r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.domains.Update))
				r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.domains.Delete))
			})
		})

		r.Route("/oauth_applications", func(r chi.Router) {
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.oauthApplications.List))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.oauthApplications.Create))

			r.Route("/{oauthApplicationID}", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.oauthApplications.Read))
				r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.oauthApplications.Update))
				r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.oauthApplications.Delete))
				r.Method(http.MethodPost, "/rotate_secret", clerkhttp.Handler(router.oauthApplications.RotateSecret))
			})
		})

		r.Route("/proxy_checks", func(r chi.Router) {
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.proxyChecks.Create))
		})

		r.Route("/saml_connections", func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.features.CheckSupportedByPlan(clerkbilling.Features.SAML)))
			r.Use(clerkhttp.Middleware(middleware.EnabledInUserSettings(names.EmailAddress)))

			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.samlConnections.List))
			r.Method(http.MethodPost, "/", clerkhttp.Handler(router.samlConnections.Create))

			r.Route("/{samlConnectionID}", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.samlConnections.Read))
				r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.samlConnections.Update))
				r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.samlConnections.Delete))
			})
		})

		r.Route("/organization_roles", func(r chi.Router) {
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.instanceOrgRoles.List))
		})

		r.Route("/organization_permissions", func(r chi.Router) {
			r.Method(http.MethodGet, "/", clerkhttp.Handler(router.instanceOrgPerm.List))
		})
	})
	return r
}

func parseForm(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	if err := r.ParseForm(); err != nil {
		return nil, apierror.MalformedRequestParameters(err)
	}

	return r, nil
}
