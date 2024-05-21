package oauth2_idp

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	"clerk/api/shared/user_profile"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/oauth_idp_state"
	"clerk/pkg/ctx/requestdomain"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/oauth2idp"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	db    database.Database
	clock clockwork.Clock

	// services
	userProfileService *user_profile.Service
	clientDataService  *client_data.Service

	// repositories
	oauthApplicationTokensRepo *repository.OAuthApplicationTokens
	usersRepo                  *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                         deps.DB(),
		clock:                      deps.Clock(),
		userProfileService:         user_profile.NewService(deps.Clock()),
		clientDataService:          client_data.NewService(deps),
		oauthApplicationTokensRepo: repository.NewOAuthApplicationTokens(),
		usersRepo:                  repository.NewUsers(),
	}
}

func (s *Service) SetUserFromClient(ctx context.Context) (context.Context, apierror.Error) {
	env := environment.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	if client == nil {
		return ctx, nil
	}

	activeSessions, err := s.clientDataService.FindAllClientSessions(ctx, env.Instance.ID, client.ID, &client_data.SessionFilterParams{
		ActiveOnly: true,
	})
	if err != nil {
		return ctx, apierror.Unexpected(err)
	}
	if len(activeSessions) == 0 {
		return ctx, nil
	}

	var sess *model.Session
	if env.AuthConfig.SessionSettings.SingleSessionMode {
		sess = activeSessions[0].ToSessionModel()
	} else {
		// multi-session applications - determine last active session
		var lastTouchedAt *time.Time
		for _, cdsSession := range activeSessions {
			session := cdsSession.ToSessionModel()
			if !session.IsActive(s.clock) {
				continue
			}

			if lastTouchedAt == nil || session.TouchedAt.After(*lastTouchedAt) {
				lastTouchedAt = &session.TouchedAt
				sess = session
			}
		}
	}

	if sess == nil || !sess.IsActive(s.clock) {
		return ctx, nil
	}

	user, err := s.usersRepo.QueryByIDAndInstance(ctx, s.db, sess.UserID, env.Instance.ID)
	if err != nil {
		return ctx, apierror.Unexpected(err)
	} else if user == nil {
		return ctx, nil
	}

	ctx = oauth_idp_state.NewContext(ctx, &oauth_idp_state.OAuthIDPState{
		User:   user,
		Scopes: "",
	})

	return ctx, nil
}

func (s *Service) SetUserFromAccessToken(ctx context.Context, token string) (context.Context, apierror.Error) {
	domain := requestdomain.FromContext(ctx)
	oat, err := s.oauthApplicationTokensRepo.QueryByTokenAndTypeAndInstance(ctx, s.db, token, repository.OAuthIDPTypeAccessToken, domain.InstanceID)
	if err != nil {
		return ctx, apierror.Unexpected(err)
	}

	if oat == nil {
		return ctx, apierror.OAuthFetchUserInfo()
	}

	if oat.ExpiresAt.Time.Before(s.clock.Now()) {
		return ctx, apierror.OAuthFetchUserInfo()
	}

	user, err := s.usersRepo.QueryByID(ctx, s.db, oat.UserID.String)
	if err != nil {
		return ctx, apierror.Unexpected(err)
	}
	if user == nil {
		return ctx, apierror.OAuthFetchUserInfo()
	}

	return oauth_idp_state.NewContext(ctx, &oauth_idp_state.OAuthIDPState{
		User:   user,
		Scopes: oat.Scopes,
	}), nil
}

func (s *Service) UserInfo(ctx context.Context) (*serialize.OAuthUserInfoResponse, apierror.Error) {
	state := oauth_idp_state.FromContext(ctx)
	user := state.User

	if user.Banned {
		return nil, apierror.OAuthFetchUserInfoForbidden()
	}

	info := model.OAuthUserInfo{
		InstanceID: user.InstanceID,
		FamilyName: user.LastName.String,
		GivenName:  user.FirstName.String,
		Name:       user.FirstName.String + " " + user.LastName.String,
		Picture:    user.ProfileImagePublicURL.String,
		UserID:     user.ID,
	}

	email, err := s.userProfileService.GetPrimaryEmailAddress(ctx, s.db, user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if email != nil {
		info.Email = *email
	}
	emailVerified, err := s.userProfileService.HasVerifiedEmail(ctx, s.db, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	info.EmailVerified = emailVerified

	username, err := s.userProfileService.GetUsername(ctx, s.db, user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if username != nil {
		info.Username = *username
	}

	if strings.Contains(state.Scopes, oauth2idp.PublicMetadataScope) {
		info.PublicMetadata = json.RawMessage(user.PublicMetadata)
		info.UnsafeMetadata = json.RawMessage(user.UnsafeMetadata)
	}

	if strings.Contains(state.Scopes, oauth2idp.PrivateMetadataScope) {
		info.PrivateMetadata = json.RawMessage(user.PrivateMetadata)
	}

	return serialize.OAuthUserInfo(info), nil
}
