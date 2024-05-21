package router

import (
	"database/sql"
	"net/http"

	"clerk/api/fapi/v1/account_portal"
	"clerk/api/fapi/v1/billing"
	"clerk/api/fapi/v1/certs"
	"clerk/api/fapi/v1/clients"
	"clerk/api/fapi/v1/cookies"
	"clerk/api/fapi/v1/debugging"
	"clerk/api/fapi/v1/dev_browser"
	"clerk/api/fapi/v1/domain"
	"clerk/api/fapi/v1/environment"
	"clerk/api/fapi/v1/jwks"
	"clerk/api/fapi/v1/oauth"
	"clerk/api/fapi/v1/oauth2_idp"
	"clerk/api/fapi/v1/organization_domains"
	"clerk/api/fapi/v1/organization_invitations"
	"clerk/api/fapi/v1/organization_membership_requests"
	"clerk/api/fapi/v1/organization_memberships"
	"clerk/api/fapi/v1/organizations"
	"clerk/api/fapi/v1/passkeys"
	"clerk/api/fapi/v1/root"
	"clerk/api/fapi/v1/saml"
	"clerk/api/fapi/v1/sessions"
	"clerk/api/fapi/v1/sign_in"
	"clerk/api/fapi/v1/sign_up"
	"clerk/api/fapi/v1/tickets"
	"clerk/api/fapi/v1/tokens"
	"clerk/api/fapi/v1/users"
	"clerk/api/fapi/v1/verification"
	"clerk/api/fapi/v1/well_known"
	"clerk/api/middleware"
	"clerk/model"
	apiVersioningMiddleware "clerk/pkg/apiversioning/middleware"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/externalapis/turnstile"
	"clerk/pkg/handlers"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/utils/clerk"

	sentry "github.com/getsentry/sentry-go/http"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/hostrouter"
	chitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-chi/chi.v5"
)

// Router is responsible for request routing in client API
type Router struct {
	deps clerk.Deps

	// handlers
	common *handlers.Common

	// services
	accountPortal           *account_portal.HTTP
	billing                 *billing.HTTP
	certs                   *certs.HTTP
	clients                 *clients.HTTP
	cookies                 *cookies.CookieSetter
	debugging               *debugging.HTTP
	devBrowser              *dev_browser.HTTP
	domains                 *domain.HTTP
	env                     *environment.HTTP
	jwks                    *jwks.HTTP
	oauth                   *oauth.OAuth
	oauth2IDP               *oauth2_idp.HTTP
	organizations           *organizations.HTTP
	organizationDomains     *organization_domains.HTTP
	organizationInvitations *organization_invitations.HTTP
	organizationMemberships *organization_memberships.HTTP
	orgMembershipRequests   *organization_membership_requests.HTTP
	passkeys                *passkeys.HTTP
	saml                    *saml.HTTP
	sessions                *sessions.HTTP
	signIn                  *sign_in.HTTP
	signUp                  *sign_up.HTTP
	tickets                 *tickets.HTTP
	tokens                  *tokens.HTTP
	users                   *users.HTTP
	verification            *verification.HTTP
	wellknown               *well_known.HTTP
}

// New builds a new router
func New(
	deps clerk.Deps,
	captchaClientPool *turnstile.ClientPool,
	common *handlers.Common,
	billingConnector clerkbilling.Connector,
	paymentProvider clerkbilling.PaymentProvider,
) *Router {
	return &Router{
		deps:                    deps,
		accountPortal:           account_portal.NewHTTP(deps),
		billing:                 billing.NewHTTP(deps, billingConnector, paymentProvider),
		certs:                   certs.NewHTTP(deps.DB()),
		common:                  common,
		clients:                 clients.NewHTTP(deps),
		cookies:                 cookies.NewCookieSetter(deps),
		debugging:               debugging.NewHTTP(),
		devBrowser:              dev_browser.NewHTTP(deps),
		domains:                 domain.NewHTTP(deps.DB()),
		env:                     environment.NewHTTP(deps.DB()),
		jwks:                    jwks.NewHTTP(),
		oauth:                   oauth.New(deps),
		oauth2IDP:               oauth2_idp.NewHTTP(deps),
		organizations:           organizations.NewHTTP(deps),
		organizationDomains:     organization_domains.NewHTTP(deps),
		organizationInvitations: organization_invitations.NewHTTP(deps),
		organizationMemberships: organization_memberships.NewHTTP(deps),
		orgMembershipRequests:   organization_membership_requests.NewHTTP(deps),
		passkeys:                passkeys.NewHTTP(deps),
		saml:                    saml.NewHTTP(deps),
		sessions:                sessions.NewHTTP(deps),
		signIn:                  sign_in.NewHTTP(deps),
		signUp:                  sign_up.NewHTTP(deps, captchaClientPool),
		tickets:                 tickets.NewHTTP(deps),
		tokens:                  tokens.NewHTTP(deps),
		users:                   users.NewHTTP(deps),
		verification:            verification.NewHTTP(deps),
		wellknown:               well_known.NewHTTP(deps.DB()),
	}
}

// BuildRoutes returns a mux with all routes for the client API
func (router *Router) BuildRoutes() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Recover)
	r.Use(clerkhttp.Middleware(robotsNoIndexMiddleware))

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
	r.Use(clerkhttp.Middleware(withSessionActivity))
	r.Use(chimw.StripSlashes)

	r.Method(http.MethodGet, "/", clerkhttp.Handler(root.Root))
	r.Method(http.MethodGet, "/v1/health", router.common.Health())
	r.Method(http.MethodHead, "/v1/health", router.common.Health())
	r.Method(http.MethodGet, "/v1/proxy-health", clerkhttp.Handler(router.common.ProxyHealth))

	hr := hostrouter.New()
	hr.Map("*", router.v1Router())
	hr.Map(model.DomainClass.DevelopmentSharedAuthHost(), router.sharedDevV1Router())
	hr.Map(model.DomainClass.StagingSharedAuthHost(), router.sharedDevV1Router())
	r.Mount("/", hr)
	return r
}

func (router *Router) v1Router() chi.Router {
	r := chi.NewRouter()

	r.Use(clerkhttp.Middleware(router.domains.SetDomainFromHost))

	// Certificate management routes
	r.Group(func(r chi.Router) {
		r.Method(http.MethodGet, "/v1/certificate-health", clerkhttp.Handler(router.certs.Health))
	})

	// .well-known
	r.Group(func(r chi.Router) {
		r.Use(clerkhttp.Middleware(router.env.SetEnvFromDomain))
		r.Use(clerkhttp.Middleware(middleware.EnsureEnvNotPendingDeletion))
		r.Use(clerkhttp.Middleware(apiVersioningMiddleware.SetAPIVersionFromHeader))
		r.Use(clerkhttp.Middleware(setRequestInfo))

		r.Method(http.MethodGet, "/.well-known/jwks.json", clerkhttp.Handler(router.jwks.Read))
		r.Method(http.MethodGet, "/.well-known/openid-configuration", clerkhttp.Handler(router.wellknown.OpenIDConfiguration))
		r.Method(http.MethodGet, "/.well-known/apple-app-site-association", clerkhttp.Handler(router.wellknown.AppleAppSiteAssociation))
		r.Method(http.MethodGet, "/.well-known/assetlinks.json", clerkhttp.Handler(router.wellknown.AssetLinks))
	})

	// OAuth2 Identify Provider
	r.Group(func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.env.SetEnvFromDomain))
			r.Use(clerkhttp.Middleware(middleware.EnsureEnvNotPendingDeletion))
			r.Use(clerkhttp.Middleware(blockDuringMaintenance))
			r.Use(clerkhttp.Middleware(router.domains.EnsurePrimaryDomain))
			r.Use(clerkhttp.Middleware(apiVersioningMiddleware.SetAPIVersionFromHeader))
			r.Use(clerkhttp.Middleware(setClientType))
			r.Use(clerkhttp.Middleware(parseAuthToken(router.deps.Clock())))
			r.Use(clerkhttp.Middleware(setDevBrowser(router.deps)))
			r.Use(clerkhttp.Middleware(setRotatingTokenNonce))
			r.Use(clerkhttp.Middleware(router.clients.SetRequestingClient))
			r.Use(clerkhttp.Middleware(router.oauth2IDP.SetUserFromClient))

			r.Method(http.MethodGet, "/oauth/authorize", clerkhttp.Handler(router.oauth2IDP.Authorize))
		})

		r.Group(func(r chi.Router) {
			r.Use(clerkhttp.Middleware(blockDuringMaintenance))
			r.Method(http.MethodPost, "/oauth/token", clerkhttp.Handler(router.oauth2IDP.Token))
		})

		r.Group(func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.oauth2IDP.SetUserFromAccessToken))
			r.Method(http.MethodGet, "/oauth/userinfo", clerkhttp.Handler(router.oauth2IDP.UserInfo))
		})
	})

	r.Route("/v1", func(r chi.Router) {
		r.Method(http.MethodGet, "/clear-site-data", clerkhttp.Handler(router.debugging.ClearSiteData))
		r.Method(http.MethodPost, "/oauth_callback", clerkhttp.Handler(router.oauth.ConvertToGET))

		r.Group(func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.env.SetEnvFromDomain))
			r.Method(http.MethodGet, "/billing_change_plan_callback", clerkhttp.Handler(router.billing.ChangePlanCallback))
		})

		r.Group(func(r chi.Router) {
			r.Use(clerkhttp.Middleware(router.env.SetEnvFromDomain))
			r.Use(clerkhttp.Middleware(middleware.EnsureEnvNotPendingDeletion))
			r.Use(clerkhttp.Middleware(apiVersioningMiddleware.SetAPIVersionFromHeader))
			r.Use(clerkhttp.Middleware(setRequestInfo))
			r.Use(clerkhttp.Middleware(csrfCheck(router.deps.Clock())))
			r.Use(clerkhttp.Middleware(setClientType))
			r.Use(clerkhttp.Middleware(validateRequestOrigin))
			r.Use(clerkhttp.Middleware(httpMethodPolyfill))
			r.Use(clerkhttp.Middleware(checkRequestAllowedDuringMaintenance))
			r.Use(clerkhttp.Middleware(logClerkJSVersion(router.deps.DB())))
			r.Use(clerkhttp.Middleware(logClerkIOSSDKVersion(router.deps)))
			r.Use(clerkhttp.Middleware(testingToken))
			r.Use(clerkhttp.Middleware(router.cookies.SetAuthCookieFromURLQuery))
			r.Use(clerkhttp.Middleware(setDevBrowserRequestContext))
			r.Use(clerkhttp.Middleware(parseAuthToken(router.deps.Clock())))
			r.Use(clerkhttp.Middleware(setRotatingTokenNonce))
			r.Use(clerkhttp.Middleware(setDevBrowser(router.deps)))
			r.Use(clerkhttp.Middleware(router.clients.SetRequestingClient))
			r.Use(clerkhttp.Middleware(setPrimedEdgeClientID(router.deps.Clock())))

			r.Group(func(r chi.Router) {
				r.Use(clerkhttp.Middleware(setCookiesSuffix))

				r.Method(http.MethodGet, "/client/sync", clerkhttp.Handler(router.clients.Sync))
				r.Method(http.MethodGet, "/client/link", clerkhttp.Handler(router.clients.Link))
				r.Method(http.MethodGet, "/client/handshake", clerkhttp.Handler(router.clients.Handshake))
			})

			r.Group(func(r chi.Router) {
				r.Use(corsHandler)
				r.Use(clerkhttp.Middleware(corsFirefoxFix))
				r.Use(clerkhttp.Middleware(parseTrackingData))

				r.Group(func(r chi.Router) {
					r.Use(router.cookies.WithClientUatResponseWriter())

					r.Method(http.MethodGet, "/", clerkhttp.Handler(root.Root))

					r.Route("/environment", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.env.Read))
						r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.env.Update))
					})

					r.Method(http.MethodGet, "/account_portal", clerkhttp.Handler(router.accountPortal.Read))

					r.Method(http.MethodGet, "/oauth_callback", clerkhttp.Handler(router.oauth.Callback))

					r.Group(func(r chi.Router) {
						r.Use(clerkhttp.Middleware(fetchDevSessionIfNecessary(router.deps)))
						r.Method(http.MethodGet, "/verify", clerkhttp.Handler(router.verification.VerifyToken))
					})

					r.Route("/saml", func(r chi.Router) {
						r.Use(clerkhttp.Middleware(router.domains.EnsurePrimaryDomain))
						r.Method(http.MethodGet, "/metadata/{samlConnectionID}.xml", clerkhttp.Handler(router.saml.Metadata))
						r.Method(http.MethodPost, "/acs/{samlConnectionID}", clerkhttp.Handler(router.saml.AssertionConsumerService))
					})

					r.Route("/tickets", func(r chi.Router) {
						r.Use(clerkhttp.Middleware(fetchDevSessionIfNecessary(router.deps)))
						r.Method(http.MethodGet, "/accept", clerkhttp.Handler(router.tickets.Accept))
					})

					r.Route("/dev_browser", func(r chi.Router) {
						// Used for cookie sync mode
						r.Method(http.MethodGet, "/init", clerkhttp.Handler(router.devBrowser.Init))
						r.Method(http.MethodPost, "/set_first_party_cookie", clerkhttp.Handler(router.devBrowser.SetCookie))

						// Used for URL-based session syncing mode in dev
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.devBrowser.CreateDevBrowser))
					})

					r.Route("/client", func(r chi.Router) {
						r.Method(http.MethodGet, "/", clerkhttp.Handler(router.clients.Read))
						r.Method(http.MethodPut, "/", clerkhttp.Handler(router.clients.Create))
						r.Method(http.MethodPost, "/", clerkhttp.Handler(router.clients.Create))
						r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.clients.Delete))

						r.Route("/sessions", func(r chi.Router) {
							r.Route("/{sessionID}", func(r chi.Router) {
								r.Group(func(r chi.Router) {
									r.Use(clerkhttp.Middleware(router.clients.VerifyRequestingClient))
									r.Use(clerkhttp.Middleware(router.sessions.SetRequestingSession))

									r.Method(http.MethodGet, "/", clerkhttp.Handler(router.sessions.Read))
									r.Method(http.MethodPost, "/touch", clerkhttp.Handler(router.sessions.Touch))
									r.Method(http.MethodPost, "/end", clerkhttp.Handler(router.sessions.End))
									r.Method(http.MethodPost, "/remove", clerkhttp.Handler(router.sessions.Remove))

									r.Route("/tokens", func(r chi.Router) {
										r.Method(http.MethodPost, "/", clerkhttp.Handler(router.tokens.CreateSessionToken))
										r.Method(http.MethodPost, "/{templateName}", clerkhttp.Handler(router.tokens.CreateFromTemplate))
									})
								})
							})
						})

						r.Route("/sign_ins", func(r chi.Router) {
							r.Use(clerkhttp.Middleware(router.domains.EnsurePrimaryDomain))
							r.Use(clerkhttp.Middleware(validateUserSettings))
							r.Method(http.MethodPost, "/", clerkhttp.Handler(router.signIn.Create))

							r.Route("/{signInID}", func(r chi.Router) {
								r.Use(clerkhttp.Middleware(router.clients.VerifyRequestingClient))
								r.Use(clerkhttp.Middleware(router.clients.UpdateClientCookieIfNeeded))
								r.Use(clerkhttp.Middleware(router.signIn.SetSignInFromPath))
								r.Use(clerkhttp.Middleware(router.signIn.EnsureUserNotLocked))

								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.signIn.Read))

								r.Group(func(r chi.Router) {
									// Mutation endpoints
									// Only the latest client sign in is allowed to be mutated
									r.Use(clerkhttp.Middleware(router.signIn.EnsureLatestClientSignIn))
									r.Method(http.MethodPost, "/reset_password", clerkhttp.Handler(router.signIn.ResetPassword))
									r.Method(http.MethodPost, "/prepare_first_factor", clerkhttp.Handler(router.signIn.PrepareFirstFactor))
									r.Method(http.MethodPost, "/attempt_first_factor", clerkhttp.Handler(router.signIn.AttemptFirstFactor))
									r.Method(http.MethodPost, "/prepare_second_factor", clerkhttp.Handler(router.signIn.PrepareSecondFactor))
									r.Method(http.MethodPost, "/attempt_second_factor", clerkhttp.Handler(router.signIn.AttemptSecondFactor))
								})
							})
						})

						r.Route("/sign_ups", func(r chi.Router) {
							r.Use(clerkhttp.Middleware(router.domains.EnsurePrimaryDomain))
							r.Use(clerkhttp.Middleware(validateUserSettings))
							r.Method(http.MethodPost, "/", clerkhttp.Handler(router.signUp.Create))

							r.Route("/{signUpID}", func(r chi.Router) {
								r.Use(clerkhttp.Middleware(router.clients.VerifyRequestingClient))
								r.Use(clerkhttp.Middleware(router.clients.UpdateClientCookieIfNeeded))
								r.Use(clerkhttp.Middleware(router.signUp.SetSignUpFromPath))

								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.signUp.Read))

								r.Group(func(r chi.Router) {
									r.Use(clerkhttp.Middleware(router.signUp.EnsureLatestSignUp))

									r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.signUp.Update))
									r.Method(http.MethodPost, "/prepare_verification", clerkhttp.Handler(router.signUp.PrepareVerification))
									r.Method(http.MethodPost, "/attempt_verification", clerkhttp.Handler(router.signUp.AttemptVerification))
								})
							})
						})
					})

					// restricted endpoints
					r.Group(func(r chi.Router) {
						r.Use(clerkhttp.Middleware(router.clients.VerifyRequestingClient))
						r.Use(clerkhttp.Middleware(router.users.SetRequestingUser))

						// /v1/me
						r.Route("/me", func(r chi.Router) {
							r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.Read))
							r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.users.Update))
							r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.users.Delete))

							r.Method(http.MethodPost, "/profile_image", clerkhttp.Handler(router.users.UpdateProfileImage))
							r.Method(http.MethodDelete, "/profile_image", clerkhttp.Handler(router.users.DeleteProfileImage))

							r.Group(func(r chi.Router) {
								r.Use(clerkhttp.Middleware(middleware.EnabledInUserSettings(names.Password)))
								r.Method(http.MethodPost, "/change_password", clerkhttp.Handler(router.users.ChangePassword))
								r.Method(http.MethodPost, "/remove_password", clerkhttp.Handler(router.users.DeletePassword))
							})

							r.Route("/sessions", func(r chi.Router) {
								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.sessions.ListUserSessions))
								r.Method(http.MethodGet, "/active", clerkhttp.Handler(router.sessions.ListUserActiveSessions))

								r.Route("/{sessionID}", func(r chi.Router) {
									r.Method(http.MethodPost, "/revoke", clerkhttp.Handler(router.sessions.Revoke))
								})
							})

							r.Route("/billing", func(r chi.Router) {
								r.Use(clerkhttp.Middleware(router.billing.EnsureBillingAccountConnected))
								r.Method(http.MethodGet, "/available_plans", clerkhttp.Handler(router.billing.GetAvailablePlansForUser))
								r.Method(http.MethodPost, "/start_portal_session", clerkhttp.Handler(router.billing.StartPortalSessionForUser))
								r.Method(http.MethodGet, "/current", clerkhttp.Handler(router.billing.GetCurrentForUser))
								r.Method(http.MethodPost, "/change_plan", clerkhttp.Handler(router.billing.ChangePlanForUser))
							})

							r.Route("/email_addresses", func(r chi.Router) {
								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.ListUserEmailAddresses))
								r.Method(http.MethodPost, "/", clerkhttp.Handler(router.users.CreateEmailAddress))
								r.Route("/{emailID}", func(r chi.Router) {
									r.Group(func(r chi.Router) {
										r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.ReadEmailAddress))
										r.Method(http.MethodPost, "/prepare_verification", clerkhttp.Handler(router.users.PrepareEmailAddressVerification))
										r.Method(http.MethodPost, "/attempt_verification", clerkhttp.Handler(router.users.AttemptEmailAddressVerification))
										r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.users.DeleteEmailAddress))
									})
								})
							})

							r.Route("/phone_numbers", func(r chi.Router) {
								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.ListPhoneNumbers))
								r.Method(http.MethodPost, "/", clerkhttp.Handler(router.users.CreatePhoneNumber))
								r.Route("/{phoneNumberID}", func(r chi.Router) {
									r.Group(func(r chi.Router) {
										r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.ReadPhoneNumber))
										r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.users.UpdatePhoneNumber))
										r.Method(http.MethodPost, "/prepare_verification", clerkhttp.Handler(router.users.PreparePhoneNumberVerification))
										r.Method(http.MethodPost, "/attempt_verification", clerkhttp.Handler(router.users.AttemptPhoneNumberVerification))
										r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.users.DeletePhoneNumber))
									})
								})
							})

							r.Route("/web3_wallets", func(r chi.Router) {
								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.ListWeb3Wallets))
								r.Method(http.MethodPost, "/", clerkhttp.Handler(router.users.CreateWeb3Wallet))
								r.Route("/{web3WalletID}", func(r chi.Router) {
									r.Group(func(r chi.Router) {
										r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.ReadWeb3Wallet))
										r.Method(http.MethodPost, "/prepare_verification", clerkhttp.Handler(router.users.PrepareWeb3WalletVerification))
										r.Method(http.MethodPost, "/attempt_verification", clerkhttp.Handler(router.users.AttemptWeb3WalletVerification))
										r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.users.DeleteWeb3Wallet))
									})
								})
							})

							r.Route("/passkeys", func(r chi.Router) {
								r.Use(middleware.EnsureInstanceHasAccess(cenv.FlagAllowPasskeysInstanceIDs))
								r.Method(http.MethodPost, "/", clerkhttp.Handler(router.passkeys.CreatePasskey))
								r.Route("/{passkeyIdentID}", func(r chi.Router) {
									r.Method(http.MethodPost, "/attempt_verification", clerkhttp.Handler(router.passkeys.AttemptPasskeyVerification))
									r.Method(http.MethodGet, "/", clerkhttp.Handler(router.passkeys.ReadPasskey))
									r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.passkeys.UpdatePasskey))
									r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.passkeys.DeletePasskey))
								})
							})

							r.Route("/external_accounts", func(r chi.Router) {
								r.Method(http.MethodPost, "/", clerkhttp.Handler(router.users.ConnectOAuthAccount))

								r.Route("/{externalAccountID}", func(r chi.Router) {
									r.Method(http.MethodPatch, "/reauthorize", clerkhttp.Handler(router.users.ReauthorizeOAuthAccount))
									r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.users.DisconnectOAuthAccount))
								})
							})

							r.Route("/totp", func(r chi.Router) {
								r.Method(http.MethodPost, "/", clerkhttp.Handler(router.users.CreateTOTP))
								r.Method(http.MethodPost, "/attempt_verification", clerkhttp.Handler(router.users.AttemptTOTPVerification))
								r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.users.DeleteTOTP))
							})

							r.Route("/backup_codes", func(r chi.Router) {
								r.Method(http.MethodPost, "/", clerkhttp.Handler(router.users.CreateBackupCodes))
							})

							r.Route("/tokens", func(r chi.Router) {
								r.Method(http.MethodPost, "/", clerkhttp.Handler(router.tokens.CreateForJWTService))
							})

							r.Route("/organization_invitations", func(r chi.Router) {
								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.ListOrganizationInvitations))
								r.Method(http.MethodPost, "/{invitationID}/accept", clerkhttp.Handler(router.users.AcceptOrganizationInvitation))
							})

							r.Route("/organization_suggestions", func(r chi.Router) {
								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.ListOrganizationSuggestions))
								r.Method(http.MethodPost, "/{suggestionID}/accept", clerkhttp.Handler(router.users.AcceptOrganizationSuggestion))
							})

							r.Route("/organization_memberships", func(r chi.Router) {
								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.users.ListOrganizationMemberships))
								r.Method(http.MethodDelete, "/{organizationID}", clerkhttp.Handler(router.users.DeleteOrganizationMembership))
							})
						})

						r.Route("/organizations", func(r chi.Router) {
							r.Use(clerkhttp.Middleware(router.organizations.CheckOrganizationsEnabled))
							r.Method(http.MethodPost, "/", clerkhttp.Handler(router.organizations.Create))

							r.Route("/{organizationID}", func(r chi.Router) {
								r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizations.Read))
								r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.organizations.Delete))
								r.Method(http.MethodPut, "/logo", clerkhttp.Handler(router.organizations.UpdateLogo))
								r.Method(http.MethodDelete, "/logo", clerkhttp.Handler(router.organizations.DeleteLogo))

								r.Group(func(r chi.Router) {
									r.Use(clerkhttp.Middleware(router.organizations.EnsureOrganizationExists))
									r.Use(clerkhttp.Middleware(router.organizations.EmitActiveOrganizationEventIfNeeded))

									r.Route("/billing", func(r chi.Router) {
										r.Use(clerkhttp.Middleware(router.billing.EnsureBillingAccountConnected))
										r.Method(http.MethodGet, "/available_plans", clerkhttp.Handler(router.billing.GetAvailablePlansForOrganization))
										r.Method(http.MethodPost, "/start_portal_session", clerkhttp.Handler(router.billing.StartPortalSessionForOrganization))
										r.Method(http.MethodGet, "/current", clerkhttp.Handler(router.billing.GetCurrentForOrganization))
										r.Method(http.MethodPost, "/change_plan", clerkhttp.Handler(router.billing.ChangePlanForOrganization))
									})

									r.Method(http.MethodPatch, "/", clerkhttp.Handler(router.organizations.Update))

									r.Route("/invitations", func(r chi.Router) {
										r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizationInvitations.List))
										r.Method(http.MethodPost, "/", clerkhttp.Handler(router.organizationInvitations.Create))
										r.Method(http.MethodPost, "/bulk", clerkhttp.Handler(router.organizationInvitations.CreateBulk))

										r.Group(func(r chi.Router) {
											r.Use(middleware.Deprecated)
											r.Method(http.MethodGet, "/pending", clerkhttp.Handler(router.organizationInvitations.ListPending))
										})

										r.Route("/{invitationID}", func(r chi.Router) {
											r.Method(http.MethodPost, "/revoke", clerkhttp.Handler(router.organizationInvitations.Revoke))
										})
									})

									r.Route("/memberships", func(r chi.Router) {
										r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizationMemberships.List))
										r.Method(http.MethodPost, "/", clerkhttp.Handler(router.organizationMemberships.Create))
										r.Method(http.MethodPatch, "/{userID}", clerkhttp.Handler(router.organizationMemberships.Update))
										r.Method(http.MethodDelete, "/{userID}", clerkhttp.Handler(router.organizationMemberships.Delete))
									})

									r.Route("/membership_requests", func(r chi.Router) {
										r.Use(clerkhttp.Middleware(router.organizations.EnsureMembersManagePermission))
										r.Method(http.MethodGet, "/", clerkhttp.Handler(router.orgMembershipRequests.List))
										r.Method(http.MethodPost, "/{requestID}/accept", clerkhttp.Handler(router.orgMembershipRequests.Accept))
										r.Method(http.MethodPost, "/{requestID}/reject", clerkhttp.Handler(router.orgMembershipRequests.Reject))
									})

									r.Route("/domains", func(r chi.Router) {
										r.Use(clerkhttp.Middleware(router.organizationDomains.EnsureDomainsEnabled))
										r.Method(http.MethodPost, "/", clerkhttp.Handler(router.organizationDomains.Create))
										r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizationDomains.List))

										r.Route("/{domainID}", func(r chi.Router) {
											r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizationDomains.Read))
											r.Method(http.MethodPost, "/prepare_affiliation_verification", clerkhttp.Handler(router.organizationDomains.PrepareAffiliationVerification))
											r.Method(http.MethodPost, "/attempt_affiliation_verification", clerkhttp.Handler(router.organizationDomains.AttemptAffiliationVerification))
											r.Method(http.MethodPost, "/update_enrollment_mode", clerkhttp.Handler(router.organizationDomains.UpdateEnrollmentMode))
											r.Method(http.MethodDelete, "/", clerkhttp.Handler(router.organizationDomains.Delete))
										})
									})

									r.Route("/roles", func(r chi.Router) {
										r.Method(http.MethodGet, "/", clerkhttp.Handler(router.organizations.ListOrganizationRoles))
									})
								})
							})
						})
					})
				})
			})
		})
	})
	return r
}

func (router *Router) sharedDevV1Router() chi.Router {
	r := chi.NewRouter()
	r.Use(clerkhttp.Middleware(router.oauth.SetEnvironmentFromStateParam))
	r.Use(clerkhttp.Middleware(middleware.EnsureEnvNotPendingDeletion))
	r.Use(clerkhttp.Middleware(apiVersioningMiddleware.SetAPIVersionFromHeader))
	r.Use(clerkhttp.Middleware(setRequestInfo))
	r.Method(http.MethodPost, "/v1/oauth_callback", clerkhttp.Handler(router.oauth.ConvertToGET))
	r.Method(http.MethodGet, "/v1/oauth_callback", clerkhttp.Handler(router.oauth.Callback))

	return r
}
