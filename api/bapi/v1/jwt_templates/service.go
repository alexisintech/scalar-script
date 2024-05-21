package jwt_templates

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/instances"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/jwt"
	"clerk/pkg/set"
	cstrings "clerk/pkg/strings"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/types"
)

var (
	disallowedClaims         = []string{"iat", "nbf", "exp", "iss", "sub", "azp"}
	allowedSigningAlgorithms = []string{"RS256", "RS384", "RS512", "HS256", "HS384", "HS512", "ES256", "ES384", "ES512"}
	allowedNameCharacters    = regexp.MustCompile("^[a-zA-Z0-9-_]+$").MatchString
)

const claimsMaxSizeInBytes = 2048
const reservedNamePrefix = "integration_"

type Service struct {
	db        database.Database
	clock     clockwork.Clock
	gueClient *gue.Client
	validator *validator.Validate

	instanceService       *instances.Service
	jwtTemplateRepo       *repository.JWTTemplate
	subscriptionPlansRepo *repository.SubscriptionPlans
}

func NewService(db database.Database, gueClient *gue.Client, clock clockwork.Clock) *Service {
	return &Service{
		db:                    db,
		clock:                 clock,
		gueClient:             gueClient,
		validator:             validator.New(),
		jwtTemplateRepo:       repository.NewJWTTemplate(),
		subscriptionPlansRepo: repository.NewSubscriptionPlans(),
		// services
		instanceService: instances.NewService(db, gueClient),
	}
}

type CreateUpdateParams struct {
	Name             string          `json:"name" form:"name" validate:"required"`
	Claims           json.RawMessage `json:"claims" form:"claims" validate:"required"`
	Lifetime         *int            `json:"lifetime" form:"lifetime" validate:"omitempty,min=30,max=315360000"`
	AllowedClockSkew *int            `json:"allowed_clock_skew" form:"allowed_clock_skew" validate:"omitempty,min=0,max=300"`
	CustomSigningKey bool            `json:"custom_signing_key" form:"custom_signing_key"`
	SigningKey       *string         `json:"signing_key" form:"signing_key" validate:"omitempty,required_if=CustomSigningKey true"`
	SigningAlgorithm *string         `json:"signing_algorithm" form:"signing_algorithm" validate:"omitempty,required_if=CustomSigningKey true"`
}

func (params CreateUpdateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}

	// ensure name contains only allowed characters
	if !allowedNameCharacters(params.Name) {
		return apierror.FormInvalidParameterValue(param.JWTTemplateName.Name, params.Name)
	}

	if strings.HasPrefix(params.Name, reservedNamePrefix) {
		errMsg := fmt.Sprintf("You can't use the reserved name prefix: '%s'", reservedNamePrefix)
		return apierror.FormInvalidParameterFormat(param.JWTTemplateName.Name, errMsg)
	}

	if len(params.Claims) > claimsMaxSizeInBytes {
		return apierror.FormParameterSizeTooLarge(param.JWTTemplateClaims.Name, claimsMaxSizeInBytes)
	}

	var claimsObj any
	if err := json.Unmarshal(params.Claims, &claimsObj); err != nil {
		return apierror.Unexpected(err)
	}

	claims, isJSONObject := claimsObj.(map[string]any)
	if !isJSONObject {
		return apierror.FormInvalidParameterFormat(param.JWTTemplateClaims.Name, "Claims need to be a valid JSON object.")
	}

	// ensure no reserved claims are present in the template
	for _, claimKey := range disallowedClaims {
		_, ok := claims[claimKey]
		if ok {
			return apierror.JWTTemplateReservedClaim(param.JWTTemplateClaims.Name, claimKey)
		}
	}

	// ensure that someone can't use 'clerk' as audience value
	aud, ok := claims["aud"]
	if !ok {
		return nil
	}

	audience, ok := aud.(string)
	if !ok {
		return nil
	}

	if strings.ToLower(audience) == "clerk" {
		return apierror.FormInvalidParameterValue("aud", audience)
	}

	// Validate that provided singing algorithm is one of the supported
	if params.CustomSigningKey && !cstrings.ArrayContains(allowedSigningAlgorithms, *params.SigningAlgorithm) {
		return apierror.FormInvalidParameterValue(
			param.JWTTemplateSigningAlgorithm.Name, *params.SigningAlgorithm)
	}

	return nil
}

// ReadAllPaginated calls ReadAll and includes the total count of results
// in the response.
// It does not actually perform pagination.
func (s *Service) ReadAllPaginated(ctx context.Context) (*serialize.PaginatedResponse, apierror.Error) {
	list, apiErr := s.ReadAll(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	totalCount := len(list)
	data := make([]any, totalCount)
	for i, template := range list {
		data[i] = template
	}
	return serialize.Paginated(data, int64(totalCount)), nil
}

func (s *Service) ReadAll(ctx context.Context) ([]*serialize.JWTTemplateResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	templates, err := s.jwtTemplateRepo.FindAllByInstance(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	results := make([]*serialize.JWTTemplateResponse, 0)
	for _, tmpl := range templates {
		if tmpl.SessionTokenTemplate() {
			continue
		}
		results = append(results, serialize.JWTTemplate(tmpl))
	}

	return results, nil
}

func (s *Service) Read(ctx context.Context, templateID string) (*serialize.JWTTemplateResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	tmpl, err := s.jwtTemplateRepo.QueryByIDAndInstance(ctx, s.db, templateID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if tmpl == nil {
		return nil, apierror.JWTTemplateNotFound("id", templateID)
	}

	return serialize.JWTTemplate(tmpl), nil
}

func (s *Service) Create(ctx context.Context, params CreateUpdateParams) (*serialize.JWTTemplateResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	instance := env.Instance

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	if !env.Instance.HasAccessToAllFeatures() {
		apierr := s.ensureCustomJWTIsAllowed(ctx, env.Subscription, params)
		if apierr != nil {
			return nil, apierr
		}
	}

	existingJWTTmpl, err := s.jwtTemplateRepo.QueryByNameAndInstance(ctx, s.db, params.Name, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if existingJWTTmpl != nil {
		return nil, apierror.FormIdentifierExists("name")
	}

	tmpl := &model.JWTTemplate{JWTTemplate: &sqbmodel.JWTTemplate{
		InstanceID:       instance.ID,
		Name:             params.Name,
		Claims:           types.JSON(params.Claims),
		SigningAlgorithm: instance.KeyAlgorithm,
		SigningKey:       null.NewString("", false),
	}}

	if params.Lifetime != nil {
		tmpl.Lifetime = *params.Lifetime
	}

	if params.AllowedClockSkew != nil {
		tmpl.ClockSkew = *params.AllowedClockSkew
	}

	if params.CustomSigningKey {
		if params.SigningAlgorithm != nil {
			tmpl.SigningAlgorithm = *params.SigningAlgorithm
		}
		tmpl.SigningKey = null.StringFromPtr(params.SigningKey)

		err := s.mintTestToken(tmpl, instance)
		if err != nil {
			return nil, err
		}
	}

	txerr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err = s.jwtTemplateRepo.Insert(ctx, tx, tmpl)
		if err != nil {
			return true, err
		}

		if tmpl.SessionTokenTemplate() {
			err = s.instanceService.UpdateSessionTokenTemplateID(ctx, tx, instance, &tmpl.ID)
			if err != nil {
				return true, err
			}
		}

		return false, nil
	})
	if txerr != nil {
		return nil, apierror.Unexpected(txerr)
	}

	return serialize.JWTTemplate(tmpl), nil
}

func (s *Service) Update(ctx context.Context, templateID string, params CreateUpdateParams) (*serialize.JWTTemplateResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	if !env.Instance.HasAccessToAllFeatures() {
		apierr := s.ensureCustomJWTIsAllowed(ctx, env.Subscription, params)
		if apierr != nil {
			return nil, apierr
		}
	}

	tmpl, err := s.jwtTemplateRepo.QueryByIDAndInstance(ctx, s.db, templateID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if tmpl == nil {
		return nil, apierror.JWTTemplateNotFound("id", templateID)
	}

	// verify that the provided name is not already used by another template
	existingJWTTmpl, err := s.jwtTemplateRepo.QueryByNameAndInstance(ctx, s.db, params.Name, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if existingJWTTmpl != nil && existingJWTTmpl.ID != tmpl.ID {
		return nil, apierror.FormIdentifierExists("name")
	}

	tmpl.Name = params.Name
	tmpl.Claims = types.JSON(params.Claims)

	if params.Lifetime != nil {
		tmpl.Lifetime = *params.Lifetime
	}

	if params.AllowedClockSkew != nil {
		tmpl.ClockSkew = *params.AllowedClockSkew
	}

	if params.CustomSigningKey {
		if params.SigningKey != nil {
			tmpl.SigningKey = null.StringFromPtr(params.SigningKey)
		}
		if params.SigningAlgorithm != nil {
			tmpl.SigningAlgorithm = *params.SigningAlgorithm
		}

		err := s.mintTestToken(tmpl, env.Instance)
		if err != nil {
			return nil, err
		}
	} else {
		tmpl.SigningKey = null.NewString("", false)
	}

	if !tmpl.SigningKey.Valid {
		tmpl.SigningAlgorithm = env.Instance.KeyAlgorithm
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		err = s.jwtTemplateRepo.Update(ctx, txEmitter, tmpl)
		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.JWTTemplate(tmpl), nil
}

func (s *Service) Delete(ctx context.Context, templateID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	tmpl, err := s.jwtTemplateRepo.QueryByIDAndInstance(ctx, s.db, templateID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if tmpl == nil {
		return nil, apierror.JWTTemplateNotFound("id", templateID)
	}

	if env.Instance.SessionTokenTemplateID.String == tmpl.ID {
		return nil, apierror.SessionTokenTemplateNotDeletable()
	}

	err = s.jwtTemplateRepo.DeleteByIDAndInstance(ctx, s.db, templateID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.DeletedObject(tmpl.ID, serialize.JWTTemplateObjectName), nil
}

// attempt to mint a dummy token as a means to check if tmpl is a well-formed
// template
func (s *Service) mintTestToken(jwtTemplate *model.JWTTemplate, instance *model.Instance) apierror.Error {
	pkey := instance.PrivateKey
	if jwtTemplate.SigningKey.Valid {
		pkey = jwtTemplate.SigningKey.String
	}

	dummyClaims := map[string]any{"foo": "bar"}
	_, err := jwt.GenerateToken(pkey, dummyClaims, jwtTemplate.SigningAlgorithm, jwt.WithCategory(jwt.ClerkJWTTemplateCategory))
	if err != nil {
		return apierror.FormInvalidParameterFormat(param.JWTTemplateSigningKey.Name)
	}

	return nil
}

// ensure custom JWT tokens are supported in the current billing plan
func (s *Service) ensureCustomJWTIsAllowed(
	ctx context.Context, subscription *model.Subscription, params CreateUpdateParams,
) apierror.Error {
	features := set.New[string]()
	if params.Name == constants.SessionTokenJWTTemplateName {
		features.Insert(billing.Features.CustomSessionToken)
	} else {
		features.Insert(billing.Features.CustomJWTTemplate)
	}

	plans, err := s.subscriptionPlansRepo.FindAllBySubscription(ctx, s.db, subscription.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	unsupportedFeatures := billing.ValidateSupportedFeatures(features, subscription, plans...)
	if len(unsupportedFeatures) > 0 {
		return apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
	}

	return nil
}
