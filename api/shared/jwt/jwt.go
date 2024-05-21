package jwt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"clerk/api/shared/jwt_template"
	"clerk/api/shared/token"
	"clerk/model"
	"clerk/pkg/jwt"
	"clerk/pkg/jwt_services"
	"clerk/pkg/jwt_services/vendors"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

// RegisterServiceVendors enables our currently supported JWT service vendors.
func RegisterServiceVendors(clock clockwork.Clock) {
	jwt_services.RegisterVendors(
		vendors.NewFirebase(clock),
	)
}

var (
	ErrUserNotFound        = errors.New("user not found")
	ErrJWTTemplateNotFound = errors.New("jwt template not found")
)

type Service struct {
	clock clockwork.Clock

	// repositories
	jwtTemplatesRepo   *repository.JWTTemplate
	orgMembershipsRepo *repository.OrganizationMembership
	userRepo           *repository.Users
}

func NewService(clock clockwork.Clock) *Service {
	return &Service{
		clock:              clock,
		jwtTemplatesRepo:   repository.NewJWTTemplate(),
		orgMembershipsRepo: repository.NewOrganizationMembership(),
		userRepo:           repository.NewUsers(),
	}
}

type CreateFromTemplateParams struct {
	Env          *model.Env
	UserID       string
	ActiveOrgID  *string
	TemplateName string
	Origin       string
	Actor        json.RawMessage
}

// CreateFromTemplate mints a token for the provided userID and based on the jwt template with the provided templateName
func (s Service) CreateFromTemplate(ctx context.Context, exec database.Executor, params CreateFromTemplateParams) (string, error) {
	userSettings := usersettings.NewUserSettings(params.Env.AuthConfig.UserSettings)

	user, err := s.userRepo.QueryByIDAndInstance(ctx, exec, params.UserID, params.Env.Instance.ID)
	if err != nil {
		return "", fmt.Errorf("shared/CreateFromTemplate: querying user with id %s: %w", params.UserID, err)
	}
	if user == nil {
		return "", fmt.Errorf("shared/CreateFromTemplate: user not found with id %s: %w", params.UserID, ErrUserNotFound)
	}

	jwtTemplate, err := s.jwtTemplatesRepo.QueryByNameAndInstance(ctx, exec, params.TemplateName, params.Env.Instance.ID)
	if err != nil {
		return "", fmt.Errorf("shared/CreateFromTemplate: querying jwt_template with name %s: %w", params.TemplateName, err)
	}
	if jwtTemplate == nil {
		return "", fmt.Errorf("shared/CreateFromTemplate: jwt_template not found with name %s: %w", params.TemplateName, ErrJWTTemplateNotFound)
	}

	tmpldata := jwt_template.Data{
		UserSettings:   userSettings,
		JWTTmpl:        jwtTemplate,
		User:           user,
		OrgMemberships: make(model.OrganizationMembershipsWithRole, 0),
		Issuer:         params.Env.Domain.FapiURL(),
		Origin:         params.Origin,
		SessionActor:   params.Actor,
	}

	tmpldata.OrgMemberships, err = s.orgMembershipsRepo.FindAllByUserWithRole(ctx, exec, params.UserID)
	if err != nil {
		return "", fmt.Errorf("shared/CreateFromTemplate: find org memberships for user id %s: %w", params.UserID, err)
	}

	if params.ActiveOrgID != nil {
		tmpldata.ActiveOrgMembership, err = s.orgMembershipsRepo.QueryByOrganizationAndUser(ctx, exec, *params.ActiveOrgID, user.ID)
		if err != nil {
			return "", fmt.Errorf("shared/CreateFromTemplate: find active org membership for (%s, %s): %w",
				*params.ActiveOrgID, user.ID, err)
		}
	}

	tmpl, err := jwt_template.New(exec, s.clock, tmpldata)
	if err != nil {
		return "", fmt.Errorf("shared/CreateFromTemplate: jwt_template constructor: %w", err)
	}

	claims, err := tmpl.Execute(ctx)
	if err != nil {
		return "", fmt.Errorf("shared/CreateFromTemplate: executing jwt_template: %w", err)
	}

	privateKey := params.Env.Instance.PrivateKey
	if jwtTemplate.SigningKey.Valid {
		privateKey = jwtTemplate.SigningKey.String
	}

	generateTokenOptions := []jwt.GenerateTokenOption{jwt.WithCategory(token.GetTemplateCategory(params.Env.AuthConfig))}
	// Include the KID claim only if the instance's key will be used
	if !jwtTemplate.SigningKey.Valid {
		generateTokenOptions = append(generateTokenOptions, jwt.WithKID(params.Env.Instance.ID))
	}

	token, err := jwt.GenerateToken(privateKey, claims, jwtTemplate.SigningAlgorithm, generateTokenOptions...)
	if err != nil {
		return "", fmt.Errorf("shared/CreateFromTemplate: generating token %w", err)
	}

	return token, nil
}
