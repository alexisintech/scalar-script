package jwt_templates

import (
	"context"
	"time"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwttemplate"
)

type Service struct {
	db           database.Database
	newSDKConfig sdkutils.ConfigConstructor
}

func NewService(db database.Database, newSDKConfig sdkutils.ConfigConstructor) *Service {
	return &Service{
		db:           db,
		newSDKConfig: newSDKConfig,
	}
}

func (s *Service) ReadAll(ctx context.Context, instanceID string) ([]*sdk.JWTTemplate, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	return sdkutils.WithRetry(func() ([]*sdk.JWTTemplate, apierror.Error) {
		response, err := sdkClient.List(ctx, &jwttemplate.ListParams{})
		if err != nil {
			return nil, sdkutils.ToAPIError(err)
		}
		return response.JWTTemplates, nil
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

func (s *Service) Create(ctx context.Context, instanceID string, params *jwttemplate.CreateParams) (*sdk.JWTTemplate, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Create(ctx, params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) Read(ctx context.Context, instanceID, templateID string) (*sdk.JWTTemplate, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Get(ctx, templateID)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) Update(ctx context.Context, instanceID, templateID string, params *jwttemplate.UpdateParams) (*sdk.JWTTemplate, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Update(ctx, templateID, params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) Delete(ctx context.Context, instanceID, templateID string) (*sdk.DeletedResource, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Delete(ctx, templateID)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) newSDKClientForInstance(ctx context.Context, instanceID string) (*jwttemplate.Client, apierror.Error) {
	sdkConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	return jwttemplate.NewClient(sdkConfig), nil
}
