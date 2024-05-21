package router

import (
	"database/sql"
	"net/http"

	"clerk/api/dapi/v1/account_portal"
	"clerk/api/dapi/v1/allowlists"
	"clerk/api/dapi/v1/analytics"
	"clerk/api/dapi/v1/applications"
	"clerk/api/dapi/v1/bff"
	"clerk/api/dapi/v1/billing"
	"clerk/api/dapi/v1/blocklists"
	"clerk/api/dapi/v1/clients"
	"clerk/api/dapi/v1/display_config"
	"clerk/api/dapi/v1/domains"
	"clerk/api/dapi/v1/environment"
	"clerk/api/dapi/v1/events"
	"clerk/api/dapi/v1/feature_flags"
	"clerk/api/dapi/v1/instance_keys"
	"clerk/api/dapi/v1/instances"
	"clerk/api/dapi/v1/integrations"
	"clerk/api/dapi/v1/jwt_services"
	"clerk/api/dapi/v1/jwt_templates"
	"clerk/api/dapi/v1/organization_permissions"
	"clerk/api/dapi/v1/organization_roles"
	"clerk/api/dapi/v1/organizations"
	"clerk/api/dapi/v1/organizationsettings"
	"clerk/api/dapi/v1/pricing"
	"clerk/api/dapi/v1/redirect_urls"
	"clerk/api/dapi/v1/saml_connections"
	"clerk/api/dapi/v1/subscriptions"
	"clerk/api/dapi/v1/system_config"
	"clerk/api/dapi/v1/templates"
	"clerk/api/dapi/v1/user_settings"
	"clerk/api/dapi/v1/users"
	"clerk/api/dapi/v1/webhooks"
	"clerk/api/middleware"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/externalapis/clerkimages"
	"clerk/pkg/externalapis/svix"
	"clerk/pkg/handlers"
	sdkutils "clerk/pkg/sdk"
	"clerk/pkg/vercel"
	"clerk/utils/clerk"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	sdkhttp "github.com/clerk/clerk-sdk-go/v2/http"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	sentry "github.com/getsentry/sentry-go/http"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	chitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-chi/chi.v5"
)

// Router -
type Router struct {
	authorizedParties []string

	jwksClient *jwks.Client

	// handlers
	common *handlers.Common

	deps clerk.Deps

	// services
	accountPortal        *account_portal.HTTP
	allowlists           *allowlists.HTTP
	analytics            *analytics.HTTP
	apps                 *applications.HTTP
	billing              *billing.HTTP
	bff                  *bff.HTTP
	blocklists           *blocklists.HTTP
	clients              *clients.HTTP
	displayConfig        *display_config.HTTP
	environment          *environment.HTTP
	events               *events.HTTP
	featureFlags         *feature_flags.HTTP
	instances            *instances.HTTP
	integrations         *integrations.HTTP
	jwtTemplates         *jwt_templates.HTTP
	keys                 *instance_keys.HTTP
	samlConnections      *saml_connections.HTTP
	subscriptions        *subscriptions.HTTP
	systemConfig         *system_config.HTTP
	organizations        *organizations.HTTP
	organizationPerms    *organization_permissions.HTTP
	organizationRoles    *organization_roles.HTTP
	organizationSettings *organizationsettings.HTTP
	pricing              *pricing.HTTP
	domains              *domains.HTTP
	redirectURLs         *redirect_urls.HTTP
	templates            *templates.HTTP
	users                *users.HTTP
	userSettings         *user_settings.HTTP
	jwtServices          *jwt_services.HTTP
	webhooks             *webhooks.HTTP
}

// NewRouter initializes a new dashboard router.
func NewRouter(
	deps clerk.Deps,
	sdkConfigConstructor sdkutils.ConfigConstructor,
	dapiSDKClientConfig *sdk.ClientConfig,
	svixClient *svix.Client,
	clerkImagesClient *clerkimages.Client,
	vercelClient *vercel.Client,
	common *handlers.Common,
	billingConnector clerkbilling.Connector,
	paymentProvider clerkbilling.PaymentProvider,
	authorizedParties []string,
) *Router {
	jwksClient := jwks.NewClient(dapiSDKClientConfig)
	return &Router{
		deps:                 deps,
		authorizedParties:    authorizedParties,
		jwksClient:           jwksClient,
		common:               common,
		accountPortal:        account_portal.NewHTTP(deps, sdkConfigConstructor),
		allowlists:           allowlists.NewHTTP(deps.DB(), sdkConfigConstructor),
		analytics:            analytics.NewHTTP(deps.Clock(), deps.DB()),
		apps:                 applications.NewHTTP(deps, svixClient, clerkImagesClient, paymentProvider),
		billing:              billing.NewHTTP(deps.DB(), deps.GueClient(), billingConnector),
		bff:                  bff.NewHTTP(deps.DB(), deps.Clock(), sdkConfigConstructor),
		blocklists:           blocklists.NewHTTP(deps.DB(), sdkConfigConstructor),
		clients:              clients.NewHTTP(deps, jwksClient),
		displayConfig:        display_config.NewHTTP(deps.DB(), deps.GueClient(), clerkImagesClient),
		domains:              domains.NewHTTP(deps, sdkConfigConstructor),
		environment:          environment.NewHTTP(deps.DB()),
		events:               events.NewHTTP(deps, paymentProvider),
		featureFlags:         feature_flags.NewHTTP(deps),
		instances:            instances.NewHTTP(deps, svixClient, clerkImagesClient, sdkConfigConstructor),
		integrations:         integrations.NewHTTP(deps, vercelClient, jwksClient),
		jwtTemplates:         jwt_templates.NewHTTP(deps.DB(), sdkConfigConstructor),
		keys:                 instance_keys.NewHTTP(deps.DB()),
		samlConnections:      saml_connections.NewHTTP(deps.DB(), sdkConfigConstructor),
		subscriptions:        subscriptions.NewHTTP(deps, paymentProvider),
		systemConfig:         system_config.NewHTTP(deps.DB()),
		organizations:        organizations.NewHTTP(deps, sdkConfigConstructor, paymentProvider),
		organizationPerms:    organization_permissions.NewHTTP(deps),
		organizationRoles:    organization_roles.NewHTTP(deps),
		organizationSettings: organizationsettings.NewHTTP(deps.DB(), sdkConfigConstructor),
		pricing:              pricing.NewHTTP(deps, paymentProvider),
		redirectURLs:         redirect_urls.NewHTTP(deps.DB(), sdkConfigConstructor),
		templates:            templates.NewHTTP(deps.DB(), sdkConfigConstructor),
		users:                users.NewHTTP(deps, dapiSDKClientConfig, sdkConfigConstructor),
		userSettings:         user_settings.NewHTTP(deps.DB(), deps.GueClient(), sdkConfigConstructor),
		jwtServices:          jwt_services.NewHTTP(deps.DB()),
		webhooks:             webhooks.NewHTTP(deps.DB(), svixClient),
	}
}

// BuildRoutes builds a router for the dashboard
func (router Router) BuildRoutes() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Recover)

	if cenv.IsEnabled(cenv.ClerkDatadogTracer) {
		r.Use(chitrace.Middleware())
	}

	// report panics to Sentry and re-panic. Also, populate a Sentry Hub
	// into the context.
	//
	// NOTE: This must come after middleware.Recover and before
	// middleware.SetTraceID
	r.Use(sentry.New(sentry.Options{Repanic: true}).Handle)

	r.Use(middleware.SetTraceID)
	r.Use(clerkhttp.Middleware(middleware.SetMaintenanceAndRecoveryMode))
	r.Use(middleware.Log(func() sql.DBStats {
		return router.deps.DB().Conn().Stats()
	}))

	r.Use(middleware.StripV1)
	r.Use(chimw.StripSlashes)
	r.Use(middleware.SetResponseTypeToJSON)

	r.Use(clerkhttp.Middleware(checkRequestAllowedDuringMaintenance))

	r.Method(http.MethodGet, "/health", router.common.Health())
	r.Method(http.MethodHead, "/health", router.common.Health())

	// incoming webhooks
	r.Route("/webhooks", func(r chi.Router) {
		r.Method(http.MethodPost, "/stripe", clerkhttp.Handler(router.pricing.StripeWebhook))
		r.Method(http.MethodPost, "/clerk", clerkhttp.Handler(router.events.ClerkWebhook))
	})

	r.Method(http.MethodGet, "/billing/connect_oauth_callback", clerkhttp.Handler(router.billing.ConnectCallback))

	r.Route("/", func(r chi.Router) {
		r.Use(corsHandler())

		// Integration routes
		r.Group(func(r chi.Router) {
			r.Route("/integrations", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Use(clerkhttp.Middleware(router.clients.RequireClient))
					r.Method(http.MethodPost, "/", clerkhttp.Handler(router.integrations.UpsertVercel))
				})

				r.Route("/{integrationID}", func(r chi.Router) {
					// Unauthenticated routes
					r.Group(func(r chi.Router) {
						r.Use(clerkhttp.Middleware(router.clients.RequireClient))
						r.Use(clerkhttp.Middleware(router.integrations.CheckIntegrationOwner))
						r.Method(http.MethodGet, "/userinfo", clerkhttp.Handler(router.integrations.GetUserInfo))
					})

					// Authenticated routes
					r.Group(func(r chi.Router) {
						r.Use(sdkhttp.RequireHeaderAuthorization(sdkhttp.JWKSClient(router.jwksClient), sdkhttp.AuthorizedPartyMatches(router.authorizedParties...)))
						r.Use(clerkhttp.Middleware(restrictUpdatesOnImpersonationSessions))
						r.Use(clerkhttp.Middleware(injectJSONValidator))
						r.Use(clerkhttp.Middleware(router.integrations.CheckIntegrationOwner))

						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.integrations.ReadVercel))
						r.Route("/objects", func(r chi.Router) {
							r.Method(http.MethodGet, "/", clerkhttp.Handler(router.integrations.GetObjects))
							r.Method(http.MethodGet, "/{objectID}", clerkhttp.Handler(router.integrations.GetObject))
						})
						r.Method(http.MethodPost, "/link", clerkhttp.Handler(router.integrations.Link))
					})
				})
			})
		})

		// Authenticated routes
		r.Group(func(r chi.Router) {
			r.Use(sdkhttp.RequireHeaderAuthorization(sdkhttp.JWKSClient(router.jwksClient), sdkhttp.AuthorizedPartyMatches(router.authorizedParties...)))
			r.Use(clerkhttp.Middleware(addLoggedInUserToLog))
			r.Use(clerkhttp.Middleware(restrictUpdatesOnImpersonationSessions))
			r.Use(clerkhttp.Middleware(injectJSONValidator))

			r.Method(http.MethodPut, "/preferences", clerkhttp.Handler(router.users.SetPreferences))
			r.Method(http.MethodGet, "/instance_keys", clerkhttp.Handler(router.keys.ListAll))

			r.Route("/organizations/{organizationID}", func(r chi.Router) {
				r.Use(clerkhttp.Middleware(router.organizations.CheckOrganizationAdmin))
				r.Method(http.MethodPost, "/checkout/{planID}/session", clerkhttp.Handler(router.pricing.CheckoutOrganizationSessionRedirect))
				r.Method(http.MethodPost, "/refresh_payment_status", clerkhttp.Handler(router.organizations.RefreshPaymentStatus))
				r.Method(http.MethodPost, "/customer_portal_session", clerkhttp.Handler(router.pricing.OrganizationCustomerPortalRedirect))
				r.Method(http.MethodGet, "/subscription", clerkhttp.Handler(router.organizations.Subscription))
				r.Method(http.MethodGet, "/subscription_plans", clerkhttp.Handler(router.organizations.ListPlans))
				r.Method(http.MethodGet, "/current_subscription", clerkhttp.Handler(router.organizations.CurrentSubscription))

				r.Group(func(r chi.Router) {
					r.Use(clerkhttp.Middleware(router.subscriptions.NewPricingCheckoutEnabled))

					r.Method(http.MethodPost, "/checkout_subscription", clerkhttp.Handler(router.subscriptions.OrganizationCheckout))
					r.Method(http.MethodPost, "/complete_subscription", clerkhttp.Handler(router.subscriptions.OrganizationComplete))
				})
			})

			r.Route("/applications", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.apps.List))
				r.Method(http.MethodPost, "/", clerkhttp.Handler(router.apps.Create))
				r.Route("/{applicationID}", func(r chi.Router) {
					r.Use(clerkhttp.Middleware(router.apps.EnsureApplicationNotPendingDeletion))
					r.Use(clerkhttp.Middleware(router.apps.CheckApplicationOwner))
					r.Method(http.MethodGet, "/", clerkhttp.Handler(router.apps.Read))
					r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.apps.Update))
					r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.apps.Delete))

					r.Group(func(r chi.Router) {
						r.Use(clerkhttp.Middleware(router.apps.CheckAdminIfOrganizationActive))
						r.Method(http.MethodPost, "/transfer_to_organization", clerkhttp.Handler(router.apps.TransferToOrganization))
						r.Method(http.MethodPost, "/transfer_to_user", clerkhttp.Handler(router.apps.TransferToUser))
					})

					r.Method(http.MethodGet, "/instances", clerkhttp.Handler(router.instances.List))
					r.Method(http.MethodPost, "/production_instance", clerkhttp.Handler(router.instances.CreateProduction))
					r.Method(http.MethodPost, "/validate_cloning", clerkhttp.Handler(router.instances.ValidateCloning))
					r.Method(http.MethodGet, "/subscription_plans", clerkhttp.Handler(router.apps.ListPlans))
					r.Method(http.MethodGet, "/current_subscription", clerkhttp.Handler(router.apps.CurrentSubscription))
					r.Method(http.MethodPost, "/logo", clerkhttp.Handler(router.apps.UpdateLogo))
					r.Method(http.MethodDelete, "/logo", clerkhttp.Handler(router.apps.DeleteLogo))
					r.Method(http.MethodPost, "/favicon", clerkhttp.Handler(router.apps.UpdateFavicon))

					r.Route("/products/{productID}", func(r chi.Router) {
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.apps.SubscribeToProduct))
						r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.apps.UnsubscribeFromProduct))
					})

					r.Group(func(r chi.Router) {
						r.Use(clerkhttp.Middleware(router.apps.CheckAdminIfOrganizationActive))
						r.Method(http.MethodPost, "/checkout/{planID}/validate", clerkhttp.Handler(router.pricing.OldCheckoutValidate))
						r.Method(http.MethodPost, "/checkout/validate", clerkhttp.Handler(router.pricing.CheckoutValidate))
						r.Method(http.MethodPost, "/checkout/suggest_addons", clerkhttp.Handler(router.pricing.CheckoutSuggestAddons))
						r.Method(http.MethodPost, "/checkout/{planID}/session", clerkhttp.Handler(router.pricing.OldCheckoutSessionRedirect))
						r.Method(http.MethodPost, "/checkout/session", clerkhttp.Handler(router.pricing.CheckoutSessionRedirect))
						r.Method(http.MethodPost, "/customer_portal_session", clerkhttp.Handler(router.pricing.ApplicationCustomerPortalRedirect))
						r.Method(http.MethodPost, "/refresh_payment_status", clerkhttp.Handler(router.apps.RefreshPaymentStatus))

						r.Group(func(r chi.Router) {
							r.Use(clerkhttp.Middleware(router.subscriptions.NewPricingCheckoutEnabled))

							r.Method(http.MethodPost, "/checkout_subscription", clerkhttp.Handler(router.subscriptions.ApplicationCheckout))
							r.Method(http.MethodPost, "/complete_subscription", clerkhttp.Handler(router.subscriptions.ApplicationComplete))
						})
					})
				})
			})

			r.Route("/instances", func(r chi.Router) {
				r.Route("/{instanceID}", func(r chi.Router) {
					r.Use(clerkhttp.Middleware(router.instances.EnsureApplicationNotPendingDeletion))
					r.Use(clerkhttp.Middleware(router.instances.CheckInstanceOwner))
					r.Use(clerkhttp.Middleware(router.environment.LoadEnvFromInstance))
					r.Use(router.pricing.RefreshGracePeriodFeaturesAfterUpdate)

					r.Route("/bff", func(r chi.Router) {
						r.Method(http.MethodGet, "/api_keys", clerkhttp.Handler(router.bff.APIKeys))
						r.Method(http.MethodGet, "/users", clerkhttp.Handler(router.bff.Users))
					})

					r.Method(http.MethodGet, "/", clerkhttp.Handler(router.instances.Read))
					r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.instances.Delete))
					r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.instances.UpdateSettings))
					r.Method(http.MethodPatch, "/communication", clerkhttp.Handler(router.instances.UpdateCommunication))
					r.Method(http.MethodPost, "/change_domain", clerkhttp.Handler(router.instances.UpdateHomeURL))
					r.Method(http.MethodPatch, "/patch_me_password", clerkhttp.Handler(router.instances.UpdatePatchMePassword))
					r.Method(http.MethodPut, "/api_versions", clerkhttp.Handler(router.instances.UpdateAPIVersion))
					r.Method(http.MethodGet, "/api_versions", clerkhttp.Handler(router.instances.GetAvailableAPIVersions))
					r.Method(http.MethodGet, "/deploy_status", clerkhttp.Handler(router.instances.DeployStatus))

					r.Route("/billing", func(r chi.Router) {
						r.Use(clerkhttp.Middleware(ensureStaffMode(router.deps.Clock(), router.deps.DB())))
						r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.billing.Config))
						r.Method(http.MethodPost, "/connect", clerkhttp.Handler(router.billing.Connect))

						r.Route("/plans", func(r chi.Router) {
							r.Use(clerkhttp.Middleware(router.billing.EnsureInstanceHasConnectedBillingAccount))

							r.Method(http.MethodPost, "/", clerkhttp.Handler(router.billing.CreatePlan))
							r.Method(http.MethodGet, "/", clerkhttp.Handler(router.billing.GetPlans))

							r.Route("/{planID}", func(r chi.Router) {
								r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.billing.UpdatePlan))
								r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.billing.DeletePlan))
							})
						})
					})

					r.Route("/status", func(r chi.Router) {
						r.Method(http.MethodPost, "/mail/retry", clerkhttp.Handler(router.instances.RetryMail))
						r.Method(http.MethodPost, "/ssl/retry", clerkhttp.Handler(router.instances.RetrySSL))
					})

					r.Route("/organization_settings", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizationSettings.Read))
						r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.organizationSettings.Update))
					})

					r.Route("/user_settings", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.userSettings.FindAll))
						r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.userSettings.UpdateUserSettings))
						r.Method(http.MethodPatch, "/sessions", clerkhttp.Handler(router.userSettings.UpdateUserSettingsSessions))
						r.Method(http.MethodPatch, "/social/{providerID}", clerkhttp.Handler(router.userSettings.UpdateUserSettingsSocial))
						r.Method(http.MethodPatch, "/restrictions", clerkhttp.Handler(router.userSettings.UpdateRestrictions))

						// TODO(haris: 10/06/2022): Temporally endpoint to migrate an instance to PSU mode. Should be removed after
						r.Method(http.MethodPatch, "/psu", clerkhttp.Handler(router.userSettings.SwitchToPSU))
					})

					r.Route("/account_portal", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.accountPortal.Read))
						r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.accountPortal.Update))
					})

					r.Route("/display_config", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.displayConfig.Read))
						r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.displayConfig.Update))
					})

					r.Route("/jwt_services", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.jwtServices.Read))
						r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.jwtServices.Update))
					})

					r.Route("/theme", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.displayConfig.GetTheme))
						r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.displayConfig.UpdateTheme))
					})

					r.Route("/image_settings", func(r chi.Router) {
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.displayConfig.UpdateImageSettings))
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.displayConfig.GetImageSettings))
					})

					r.Route("/allowlist_identifiers", func(r chi.Router) {
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.allowlists.Create))
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.allowlists.ListAll))
						r.Method(http.MethodDelete, "/{identifierID}", clerkhttp.Handler(router.allowlists.Delete))
					})

					r.Route("/blocklist_identifiers", func(r chi.Router) {
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.blocklists.Create))
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.blocklists.ListAll))
						r.Method(http.MethodDelete, "/{identifierID}", clerkhttp.Handler(router.blocklists.Delete))
					})

					r.Route("/instance_keys", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.keys.List))
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.keys.Create))
						r.Route("/{instanceKeyID}", func(r chi.Router) {
							r.Method(http.MethodGet, "/", clerkhttp.Handler(router.keys.Read))
							r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.keys.Delete))
						})
					})

					r.Route("/integrations", func(r chi.Router) {
						r.Method(http.MethodGet, "/{integrationType}", clerkhttp.Handler(router.integrations.ReadByType))
						r.Method(http.MethodPut, "/{integrationType}", clerkhttp.Handler(router.integrations.UpsertByType))
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

					r.Route("/organizations", func(r chi.Router) {
						r.Use(clerkhttp.Middleware(router.organizations.CheckOrganizationsEnabled))
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizations.List))
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.organizations.Create))
						r.Method(http.MethodGet, "/{organizationIDorSlug}", clerkhttp.Handler(router.organizations.Read))
						r.Method(http.MethodPatch, "/{organizationID}", clerkhttp.Handler(router.organizations.Update))
						r.Method(http.MethodDelete, "/{organizationID}", clerkhttp.Handler(router.organizations.Delete))
						r.Method(http.MethodPatch, "/{organizationID}/metadata", clerkhttp.Handler(router.organizations.UpdateMetadata))
						r.Method(http.MethodPost, "/{organizationID}/logo", clerkhttp.Handler(router.organizations.UpdateLogo))
						r.Method(http.MethodDelete, "/{organizationID}/logo", clerkhttp.Handler(router.organizations.DeleteLogo))
						r.Method(http.MethodPost, "/{organizationID}/update_logo", clerkhttp.Handler(router.organizations.UpdateLogo))

						r.Route("/{organizationID}/memberships", func(r chi.Router) {
							r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizations.ListMemberships))
							r.Method(http.MethodPost, "/", clerkhttp.Handler(router.organizations.CreateMembership))
							r.Method(http.MethodPatch, "/{userID}", clerkhttp.Handler(router.organizations.UpdateMembership))
							r.Method(http.MethodDelete, "/{userID}", clerkhttp.Handler(router.organizations.DeleteMemebership))
						})
					})

					r.Route("/organization_roles", func(r chi.Router) {
						r.Use(clerkhttp.Middleware(router.organizations.CheckOrganizationsEnabled))
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizationRoles.List))
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.organizationRoles.Create))
						r.Method(http.MethodGet, "/{orgRoleID}", clerkhttp.Handler(router.organizationRoles.Read))
						r.Method(http.MethodPatch, "/{orgRoleID}", clerkhttp.Handler(router.organizationRoles.Update))
						r.Method(http.MethodDelete, "/{orgRoleID}", clerkhttp.Handler(router.organizationRoles.Delete))

						r.Route("/{orgRoleID}/permissions/{orgPermissionID}", func(r chi.Router) {
							r.Method(http.MethodPost, "/", clerkhttp.Handler(router.organizationRoles.AssignPermission))
							r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.organizationRoles.RemovePermission))
						})
					})

					r.Route("/organization_permissions", func(r chi.Router) {
						r.Use(clerkhttp.Middleware(router.organizations.CheckOrganizationsEnabled))
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizationPerms.List))
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.organizationPerms.Create))

						r.Route("/{orgPermissionID}", func(r chi.Router) {
							r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizationPerms.Read))
							r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.organizationPerms.Update))
							r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.organizationPerms.Delete))
						})
					})

					r.Route("/redirect_urls", func(r chi.Router) {
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.redirectURLs.Create))
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.redirectURLs.List))
						r.Route("/{urlID}", func(r chi.Router) {
							r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.redirectURLs.Delete))
						})
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

					r.Route("/saml_connections", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.samlConnections.List))
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.samlConnections.Create))

						r.Route("/{samlConnectionID}", func(r chi.Router) {
							r.Method(http.MethodGet, "/", clerkhttp.Handler(router.samlConnections.Read))
							r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.samlConnections.Update))
							r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.samlConnections.Delete))
						})
					})

					r.Route("/users", func(r chi.Router) {
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.users.Create))
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.List))
						r.Method(http.MethodGet, "/count", clerkhttp.Handler(router.users.Count))

						r.Route("/{userID}", func(r chi.Router) {
							r.Use(clerkhttp.Middleware(router.users.CheckUserInInstance))
							r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.Read))
							r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.users.Update))
							r.Method(http.MethodPost, "/impersonate", clerkhttp.Handler(router.users.Impersonate))

							r.Group(func(r chi.Router) {
								r.Method(http.MethodPost, "/ban", clerkhttp.Handler(router.users.Ban))
								r.Method(http.MethodPost, "/unban", clerkhttp.Handler(router.users.Unban))
							})

							r.Method(http.MethodPost, "/unlock", clerkhttp.Handler(router.users.Unlock))

							r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.users.Delete))
							r.Method(http.MethodGet, "/organization_memberships", clerkhttp.Handler(router.users.ListOrganizationMemberships))
						})
					})

					r.Route("/webhooks", func(r chi.Router) {
						r.Method(http.MethodPost, "/svix", clerkhttp.Handler(router.webhooks.CreateSvix))
						r.Method(http.MethodGet, "/svix", clerkhttp.Handler(router.webhooks.GetSvixStatus))
						r.Method(http.MethodDelete, "/svix", clerkhttp.Handler(router.webhooks.DeleteSvix))
					})

					r.Route("/analytics", func(r chi.Router) {
						r.Method(http.MethodGet, "/user_activity/{kind}", clerkhttp.Handler(router.analytics.UserActivity))
						r.Method(http.MethodGet, "/monthly_metrics", clerkhttp.Handler(router.analytics.MonthlyMetrics))
						r.Method(http.MethodGet, "/latest_activity", clerkhttp.Handler(router.analytics.LatestActivity))
					})

					r.Route("/feature_flags", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.featureFlags.Read))
					})

					r.Route("/domains", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.domains.List))
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.domains.Create))
						r.Route("/{domainID}", func(r chi.Router) {
							r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.domains.Update))
							r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.domains.Delete))
							r.Method(http.MethodPost, "/verify_proxy", clerkhttp.Handler(router.domains.VerifyProxy))

							r.Route("/status", func(r chi.Router) {
								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.domains.Status))
								r.Method(http.MethodPost, "/dns/retry", clerkhttp.Handler(router.domains.RetryDNS))
								r.Method(http.MethodPost, "/mail/retry", clerkhttp.Handler(router.domains.RetryMail))
								r.Method(http.MethodPost, "/ssl/retry", clerkhttp.Handler(router.domains.RetrySSL))
							})
						})
					})
				})
			})

			r.Route("/domains", func(r chi.Router) {
				r.Method(http.MethodGet, "/{name}/exists", clerkhttp.Handler(router.domains.Exists))
			})

			r.Route("/system_config", func(r chi.Router) {
				r.Method(http.MethodGet, "/", clerkhttp.Handler(router.systemConfig.Read))
			})
		})
	})

	return r
}
