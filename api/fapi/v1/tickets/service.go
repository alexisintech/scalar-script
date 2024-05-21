package tickets

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/samlaccount"
	"clerk/api/shared/client_data"
	"clerk/api/shared/organizations"
	"clerk/model"
	"clerk/pkg/cache"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/jwt"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/ticket"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	cache cache.Cache
	clock clockwork.Clock
	db    database.Database

	// services
	clientDataService   *client_data.Service
	organizationService *organizations.Service
	samlAccountService  *samlaccount.Service

	// repositories
	actorTokensRepo     *repository.ActorToken
	devBrowserRepo      *repository.DevBrowser
	identificationsRepo *repository.Identification
	invitationsRepo     *repository.Invitations
	orgInvitationsRepo  *repository.OrganizationInvitation
	orgRepo             *repository.Organization
	samlAccountRepo     *repository.SAMLAccount
	samlConnectionRepo  *repository.SAMLConnection
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		cache:               deps.Cache(),
		clock:               deps.Clock(),
		db:                  deps.DB(),
		clientDataService:   client_data.NewService(deps),
		organizationService: organizations.NewService(deps),
		samlAccountService:  samlaccount.NewService(deps),
		actorTokensRepo:     repository.NewActorToken(),
		devBrowserRepo:      repository.NewDevBrowser(),
		identificationsRepo: repository.NewIdentification(),
		invitationsRepo:     repository.NewInvitations(),
		orgInvitationsRepo:  repository.NewOrganizationInvitation(),
		samlAccountRepo:     repository.NewSAMLAccount(),
		samlConnectionRepo:  repository.NewSAMLConnection(),
	}
}

// BuildTicketRedirectURL will parse the ticket and if possible, it will
// return a redirect URL according to what the ticket requires.
func (s Service) BuildTicketRedirectURL(ctx context.Context, ticketToken, afterImpersonateRedirectURL string) (*url.URL, apierror.Error) {
	env := environment.FromContext(ctx)

	accountsURL := env.Domain.AccountsURL()
	claims, claimsErr := ticket.Parse(ticketToken, env.Instance, s.clock)
	// we ignore expired ticket error because it will be handled in the url we will
	// redirect the user
	if claimsErr != nil && !errors.Is(claimsErr, jwt.ErrTokenExpired) {
		return nil, apierror.TicketInvalid()
	}

	hasTicketExpired := errors.Is(claimsErr, jwt.ErrTokenExpired)

	var redirectURL *url.URL
	var apiErr apierror.Error
	switch claims.SourceType {
	case constants.OSTActorToken:
		redirectURL, apiErr = s.handleActorToken(ctx, env, ticketToken, afterImpersonateRedirectURL, claims, accountsURL)
		if apiErr != nil {
			return nil, apiErr
		}
	case constants.OSTInvitation:
		redirectURL, apiErr = s.handleInstanceInvitation(ctx, env, ticketToken, claims, hasTicketExpired, accountsURL)
		if apiErr != nil {
			return nil, apiErr
		}
	case constants.OSTOrganizationInvitation:
		redirectURL, apiErr = s.handleOrganizationInvitation(ctx, env, ticketToken, claims, accountsURL)
		if apiErr != nil {
			return nil, apiErr
		}
	case constants.OSTSAMLIdpInitiated:
		redirectURL, apiErr = s.handleSAMLIdPInitiated(ctx, env, ticketToken, claims, accountsURL)
		if apiErr != nil {
			return nil, apiErr
		}
	default:
		if claims.SourceType != "" {
			sentryclerk.CaptureException(ctx,
				fmt.Errorf("tickets/accept: invalid source type %s found in claims %+v", claims.SourceType, claims),
			)
		}
		return nil, apierror.TicketInvalid()
	}

	return redirectURL, nil
}

func (s *Service) handleInstanceInvitation(
	ctx context.Context,
	env *model.Env,
	ticket string,
	claims ticket.Claims,
	hasTicketExpired bool,
	accountsURL string,
) (*url.URL, apierror.Error) {
	var existingIdentification *model.Identification
	invitation, err := s.invitationsRepo.QueryByIDAndInstance(ctx, s.db, claims.SourceID, claims.InstanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if invitation != nil {
		existingIdentification, err = s.identificationsRepo.QueryClaimedVerifiedOrReservedByInstanceAndIdentifierAndTypePrioritizingVerified(ctx, s.db,
			claims.InstanceID, invitation.EmailAddress, constants.ITEmailAddress)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	userExists := existingIdentification != nil

	var linkURL string
	var statusForCustomFlow string
	if claims.RedirectURL != nil {
		linkURL = *claims.RedirectURL
		if userExists {
			statusForCustomFlow = "sign_in"
		} else {
			statusForCustomFlow = "sign_up"
		}
	} else {
		if userExists {
			// use the default sign in
			linkURL = env.DisplayConfig.Paths.SignInURL(env.Instance.Origin(env.Domain, nil), accountsURL)
		} else {
			// use the default sign up
			linkURL = env.DisplayConfig.Paths.SignUpURL(env.Instance.Origin(env.Domain, nil), accountsURL)
		}
	}

	redirectURL, err := url.Parse(linkURL)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if !hasTicketExpired {
		// As mentioned above, if there is an error parsing the token, then
		// we want to avoid returning an error but instead postpone the error for
		// later in the invitations flow. In order to do that, we would need to
		// include the token when redirecting in case of an error.
		// Also, if there was no error (so we managed to decode the token) and
		// the token is not expired, we want to include the token in the
		// redirection (a.k.a. happy path).
		q := redirectURL.Query()
		q.Add(param.ClerkTicket, ticket)

		if claims.RedirectURL != nil {
			q.Add(param.ClerkStatus, statusForCustomFlow)
		}

		redirectURL.RawQuery = q.Encode()
	}
	return redirectURL, nil
}

func (s *Service) handleOrganizationInvitation(
	ctx context.Context,
	env *model.Env,
	ticket string,
	claims ticket.Claims,
	accountsURL string,
) (*url.URL, apierror.Error) {
	if claims.OrganizationID != nil {
		exists, err := s.orgRepo.ExistsByID(ctx, s.db, *claims.OrganizationID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		if !exists {
			return nil, apierror.OrganizationInvitationToDeletedOrganization()
		}
	}

	// Retrieve invitation
	invitation, err := s.orgInvitationsRepo.QueryByID(ctx, s.db, claims.SourceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if invitation == nil {
		return nil, apierror.OrganizationInvitationNotFound(claims.SourceID)
	}

	// Check if user already exists
	identification, err := s.identificationsRepo.QueryClaimedVerifiedByInstanceAndIdentifierAndType(ctx, s.db, env.Instance.ID, invitation.EmailAddress, constants.ITEmailAddress)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	userExists := identification != nil

	userIsLoggedIn, err := s.belongsToRequestingUser(ctx, env, identification)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var redirectURL *url.URL
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// if invited user is the same that's making the request, add user to organization
		if userIsLoggedIn && invitation.IsPending() {
			_, err := s.organizationService.AcceptInvitation(ctx, tx, organizations.AcceptInvitationParams{
				InvitationID: invitation.ID,
				UserID:       identification.UserID.String,
				Instance:     env.Instance,
				Subscription: env.Subscription,
			})
			if err != nil {
				return true, err
			}
		}

		var linkURL string
		var statusForCustomFlow string
		if claims.RedirectURL != nil {
			// We have a custom flow, use the given redirect url
			linkURL = *claims.RedirectURL
			if userIsLoggedIn {
				statusForCustomFlow = "complete"
			} else if userExists {
				statusForCustomFlow = "sign_in"
			} else {
				statusForCustomFlow = "sign_up"
			}
		} else {
			var devBrowser *model.DevBrowser
			if invitation.DevBrowserID.Valid {
				// There is a dev browser, so this means that the organization
				// invitation was created via FAPI.
				// We can use the home origin of that dev browser to find out the
				// link url.
				devBrowser, err = s.devBrowserRepo.QueryByIDAndInstance(ctx, tx, invitation.DevBrowserID.String, invitation.InstanceID)
				if err != nil {
					return true, err
				}
			}

			origin := env.Instance.Origin(env.Domain, devBrowser)

			if userIsLoggedIn {
				// use the default home
				linkURL = env.DisplayConfig.Paths.HomeURL(origin, accountsURL)
			} else if userExists {
				// use the default sign in
				linkURL = env.DisplayConfig.Paths.SignInURL(origin, accountsURL)
			} else {
				// use the default sign up
				linkURL = env.DisplayConfig.Paths.SignUpURL(origin, accountsURL)
			}
		}

		redirectURL, err = url.Parse(linkURL)
		if err != nil {
			return true, err
		}

		q := redirectURL.Query()
		q.Add(param.ClerkTicket, ticket)
		if claims.RedirectURL != nil {
			q.Add(param.ClerkStatus, statusForCustomFlow)
		}
		redirectURL.RawQuery = q.Encode()
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return redirectURL, nil
}

func (s *Service) handleActorToken(
	ctx context.Context,
	env *model.Env,
	ticket string,
	afterImpersonateRedirectURL string,
	claims ticket.Claims,
	accountsURL string,
) (*url.URL, apierror.Error) {
	// Fetch the Actor token & parse out the ActorID
	actorToken, err := s.actorTokensRepo.QueryByIDAndInstance(ctx, s.db, claims.SourceID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	var actorID string
	if actorToken != nil {
		// ignore error, it will be caught and handled properly later in the process
		actorID, _ = actorToken.ActorID()
	}

	// Terminate the Active Sessions (unless the Actor has active sessions)
	requestingClient, _ := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	err = s.terminateActiveSessions(ctx, requestingClient, actorID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Construct the redirect URL & return it
	signInURL := env.DisplayConfig.Paths.SignInURL(env.Instance.Origin(env.Domain, nil), accountsURL)
	redirectURL, err := url.Parse(signInURL)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	query := redirectURL.Query()
	query.Add(param.ClerkTicket, ticket)
	if afterImpersonateRedirectURL != "" {
		query.Add(param.ClerkRedirectURL, afterImpersonateRedirectURL)
	}
	redirectURL.RawQuery = query.Encode()
	return redirectURL, nil
}

func (s *Service) handleSAMLIdPInitiated(ctx context.Context, env *model.Env, ticket string, claims ticket.Claims, accountsURL string) (*url.URL, apierror.Error) {
	// Make sure an active saml connection exists
	samlConnection, err := s.samlConnectionRepo.QueryActiveByIDAndInstanceID(ctx, s.db, claims.SourceID, claims.InstanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if samlConnection == nil {
		return nil, apierror.SAMLConnectionActiveNotFound(claims.SourceID)
	}

	// Check if user already exists
	samlAccount, err := s.samlAccountService.QueryForUser(ctx, s.db, samlConnection, claims.SAMLUser)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Check if an email address with the same identifier already exists, in order to proceed with account linking
	emailIdentification, err := s.samlAccountService.QueryEmailIdentificationForAccountLinking(ctx, s.db, samlConnection, claims.SAMLUser)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	userExists := samlAccount != nil || emailIdentification != nil

	var samlIdentification *model.Identification
	if samlAccount != nil {
		samlIdentification, err = s.identificationsRepo.FindByID(ctx, s.db, samlAccount.IdentificationID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	userIsLoggedIn, err := s.belongsToRequestingUser(ctx, env, samlIdentification)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	redirectToHome := userIsLoggedIn && env.AuthConfig.SessionSettings.SingleSessionMode

	if redirectToHome {
		// NOTE: this is security-critical, to mitigate replay attacks. Consume token in order not be able to reuse it
		cacheKey := claims.SAMLIdpInitiatedCacheKey()
		if err := s.cache.Set(ctx, cacheKey, claims.ID, time.Second*time.Duration(constants.ExpiryTimeSAMLIdpInitiated)); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	origin := env.Instance.Origin(env.Domain, nil)

	var linkURL string
	if redirectToHome {
		// use the default home
		linkURL = env.DisplayConfig.Paths.HomeURL(origin, accountsURL)
	} else if userExists {
		// use the default sign in
		linkURL = env.DisplayConfig.Paths.SignInURL(origin, accountsURL)
	} else {
		// use the default sign up
		linkURL = env.DisplayConfig.Paths.SignUpURL(origin, accountsURL)
	}

	redirectURL, err := url.Parse(linkURL)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if !redirectToHome {
		q := redirectURL.Query()
		q.Add(param.ClerkTicket, ticket)
		redirectURL.RawQuery = q.Encode()
	}

	return redirectURL, nil
}

// belongsToRequestingUser checks whether the given identification belongs to the
// same user that's making the request.
func (s *Service) belongsToRequestingUser(ctx context.Context, env *model.Env, identification *model.Identification) (bool, error) {
	if identification == nil {
		return false, nil
	}

	requestingClient, _ := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	if requestingClient == nil {
		return false, nil
	}

	// Fetch all the Sessions that belong to the Client
	sessions, err := s.clientDataService.FindAllClientSessions(ctx, env.Instance.ID, requestingClient.ID, nil)
	if err != nil {
		return false, err
	}

	// Ensure at least one belongs to the user
	for _, session := range sessions {
		if session.UserID == identification.UserID.String {
			return true, nil
		}
	}
	return false, nil
}

// terminateActiveSessions Removes all existing active sessions for a client.
// In the event the actorID that is passed in has an active session, we do nothing.
func (s *Service) terminateActiveSessions(ctx context.Context, client *model.Client, actorID string) error {
	if client == nil {
		return nil
	}

	// Find all Active Sessions belonging to this client
	activeSessions, err := s.clientDataService.FindAllClientSessions(ctx, client.InstanceID, client.ID, &client_data.SessionFilterParams{
		ActiveOnly: true,
	})
	if err != nil {
		return err
	}

	// Check if actor has any active sessions on the same client. If they have, then we
	// shouldn't terminate it.
	// This ensures proper handling of Clerk dashboard case.
	for _, session := range activeSessions {
		if session.UserID == actorID {
			return nil
		}
	}

	// Remove all existing active sessions for the client
	// TODO @BrandonRomano: Refactor this in the future to use a shared session service
	for _, session := range activeSessions {
		session.Status = constants.SESSRemoved
		err := s.clientDataService.UpdateSessionStatus(ctx, session)
		if err != nil {
			return err
		}
	}
	return nil
}
