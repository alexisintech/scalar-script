package oauth_applications

import (
	"context"
	"net/url"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/hash"
	"clerk/pkg/oauth2idp"
	"clerk/pkg/rand"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"
)

const (
	paramCallbackURL = "callback_url"
)

type Service struct {
	db        database.Database
	validator *validator.Validate

	// repositories
	oauthApplicationsRepo *repository.OAuthApplications
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                    deps.DB(),
		validator:             validator.New(),
		oauthApplicationsRepo: repository.NewOAuthApplications(),
	}
}

type CreateParams struct {
	Name        string `json:"name" form:"name" validate:"required,max=256"`
	CallbackURL string `json:"callback_url" form:"callback_url" validate:"required,max=1024"`
	Public      *bool  `json:"public" form:"public"`
	Scopes      string `json:"scopes" form:"scopes" validate:"max=1024"`
}

func (p CreateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}

	url, err := url.ParseRequestURI(p.CallbackURL)
	if err != nil || url.Scheme == "" || url.Host == "" {
		return apierror.FormInvalidTypeParameter(paramCallbackURL, "valid url")
	}

	if _, err := oauth2idp.ParseScopes(p.Scopes, oauth2idp.FormattedAvailableScopes()); err != nil {
		return apierror.FormInvalidParameterValueWithAllowed("scopes", p.Scopes, oauth2idp.AvailableScopes.Array())
	}

	return nil
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.OAuthApplicationResponse, apierror.Error) {
	if err := params.validate(s.validator); err != nil {
		return nil, err
	}

	clientID, err := rand.AlphanumExtended(16)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	clientSecret, err := rand.AlphanumExtended(32)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	clientSecretHash, err := hash.GenerateBcryptHash(clientSecret)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	scopes, _ := oauth2idp.ParseScopes(params.Scopes, oauth2idp.FormattedAvailableScopes())

	env := environment.FromContext(ctx)
	oauthApplication := &model.OAuthApplication{
		OauthApplication: &sqbmodel.OauthApplication{
			InstanceID:       env.Instance.ID,
			Name:             params.Name,
			ClientID:         clientID,
			ClientSecretHash: clientSecretHash,
			CallbackURL:      params.CallbackURL,
			Scopes:           scopes,
			Public:           params.Public != nil && *params.Public,
		},
		ClientSecret: clientSecret,
	}

	err = s.oauthApplicationsRepo.Insert(ctx, s.db, oauthApplication)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.OAuthApplication(oauthApplication, env.Domain), nil
}

func (s *Service) Read(ctx context.Context, oauthApplicationID string) (*serialize.OAuthApplicationResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	oa, err := s.oauthApplicationsRepo.QueryByIDAndInstance(ctx, s.db, oauthApplicationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if oa == nil {
		return nil, apierror.ResourceNotFound()
	}
	return serialize.OAuthApplication(oa, env.Domain), nil
}

type UpdateParams struct {
	Name        string `json:"name" form:"name" validate:"max=256"`
	CallbackURL string `json:"callback_url" form:"callback_url" validate:"max=1024"`
	Scopes      string `json:"scopes" form:"scopes" validate:"max=1024"`
}

func (p UpdateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}

	if p.CallbackURL != "" {
		url, err := url.ParseRequestURI(p.CallbackURL)
		if err != nil || url.Scheme == "" || url.Host == "" {
			return apierror.FormInvalidTypeParameter(paramCallbackURL, "valid url")
		}
	}

	if _, err := oauth2idp.ParseScopes(p.Scopes, oauth2idp.FormattedAvailableScopes()); err != nil {
		return apierror.FormInvalidParameterValueWithAllowed("scopes", p.Scopes, oauth2idp.AvailableScopes.Array())
	}

	return nil
}

func (s *Service) Update(ctx context.Context, oauthApplicationID string, params UpdateParams) (*serialize.OAuthApplicationResponse, apierror.Error) {
	if err := params.validate(s.validator); err != nil {
		return nil, err
	}

	env := environment.FromContext(ctx)
	oa, err := s.oauthApplicationsRepo.QueryByIDAndInstance(ctx, s.db, oauthApplicationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if oa == nil {
		return nil, apierror.ResourceNotFound()
	}

	updatedColumns := []string{}

	if params.Name != "" {
		oa.Name = params.Name
		updatedColumns = append(updatedColumns, sqbmodel.OauthApplicationColumns.Name)
	}
	if params.CallbackURL != "" {
		oa.CallbackURL = params.CallbackURL
		updatedColumns = append(updatedColumns, sqbmodel.OauthApplicationColumns.CallbackURL)
	}
	if params.Scopes != "" {
		oa.Scopes, _ = oauth2idp.ParseScopes(params.Scopes, oauth2idp.FormattedAvailableScopes())
		updatedColumns = append(updatedColumns, sqbmodel.OauthApplicationColumns.Scopes)
	}

	err = s.oauthApplicationsRepo.Update(
		ctx,
		s.db,
		oa,
		updatedColumns...,
	)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.OAuthApplication(oa, env.Domain), nil
}

func (s *Service) Delete(ctx context.Context, oauthApplicationID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	rowsAff, err := s.oauthApplicationsRepo.DeleteByIDAndInstance(ctx, s.db, oauthApplicationID, env.Instance.ID)

	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if rowsAff == 0 {
		return nil, apierror.ResourceNotFound()
	}

	return serialize.DeletedObject(oauthApplicationID, serialize.ObjectOAuthApplication), nil
}

func (s *Service) List(ctx context.Context, paginationParams pagination.Params) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	oauthApplications, err := s.oauthApplicationsRepo.FindAllByInstance(ctx, s.db, env.Instance.ID, paginationParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalCount, err := s.oauthApplicationsRepo.CountByInstance(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]interface{}, len(oauthApplications))
	for i, oa := range oauthApplications {
		responses[i] = serialize.OAuthApplication(oa, env.Domain)
	}

	return serialize.Paginated(responses, totalCount), nil
}

func (s *Service) RotateSecret(ctx context.Context, oauthApplicationID string) (*serialize.OAuthApplicationResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	oa, err := s.oauthApplicationsRepo.QueryByIDAndInstance(ctx, s.db, oauthApplicationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if oa == nil {
		return nil, apierror.ResourceNotFound()
	}

	clientSecret, err := rand.AlphanumExtended(32)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	clientSecretHash, err := hash.GenerateBcryptHash(clientSecret)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	oa.ClientSecretHash = clientSecretHash
	oa.ClientSecret = clientSecret

	err = s.oauthApplicationsRepo.Update(ctx, s.db, oa, sqbmodel.OauthApplicationColumns.ClientSecretHash)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.OAuthApplication(oa, env.Domain), nil
}
