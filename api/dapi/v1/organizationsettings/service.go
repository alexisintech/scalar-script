package organizationsettings

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	sdkutils "clerk/pkg/sdk"
	"clerk/repository"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/instancesettings"
)

type Service struct {
	db                   database.Database
	sdkConfigConstructor sdkutils.ConfigConstructor
	authConfigRepo       *repository.AuthConfig
}

func NewService(db database.Database, sdkConfigConstructor sdkutils.ConfigConstructor) *Service {
	return &Service{
		db:                   db,
		sdkConfigConstructor: sdkConfigConstructor,
		authConfigRepo:       repository.NewAuthConfig(),
	}
}

func (s *Service) Read(ctx context.Context, instanceID string) (*serialize.OrganizationSettingsResponse, apierror.Error) {
	authConfig, err := s.authConfigRepo.FindByInstanceActiveAuthConfigID(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.OrganizationSettings(authConfig.OrganizationSettings), nil
}

// Update will configure and save organization related settings based on
// the params.
func (s *Service) Update(ctx context.Context, instanceID string, params instancesettings.UpdateOrganizationSettingsParams) (*sdk.OrganizationSettings, apierror.Error) {
	config, apiErr := sdkutils.NewConfigForInstance(ctx, s.sdkConfigConstructor, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, sdkErr := instancesettings.NewClient(config).UpdateOrganizationSettings(ctx, &params)
	if sdkErr != nil {
		return nil, sdkutils.ToAPIError(sdkErr)
	}
	return response, nil
}
