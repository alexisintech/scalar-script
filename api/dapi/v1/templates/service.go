package templates

import (
	"context"
	"time"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/template"
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

func (s *Service) List(ctx context.Context, instanceID string, params *template.ListParams) ([]*sdk.Template, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	return sdkutils.WithRetry(func() ([]*sdk.Template, apierror.Error) {
		response, err := sdkClient.List(ctx, params)
		return response.Templates, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

func (s *Service) Read(ctx context.Context, instanceID string, params *template.GetParams) (*sdk.Template, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	return sdkutils.WithRetry(func() (*sdk.Template, apierror.Error) {
		response, err := sdkClient.Get(ctx, params)
		return response, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

func (s *Service) Upsert(ctx context.Context, instanceID string, params *template.UpdateParams) (*sdk.Template, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Update(ctx, params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) Revert(ctx context.Context, instanceID string, params *template.RevertParams) (*sdk.Template, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Revert(ctx, params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) Preview(ctx context.Context, instanceID string, params *template.PreviewParams) (*sdk.TemplatePreview, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Preview(ctx, params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) Delete(ctx context.Context, instanceID string, params *template.DeleteParams) (*sdk.DeletedResource, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Delete(ctx, params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) ToggleDelivery(ctx context.Context, instanceID string, params *template.ToggleDeliveryParams) (*sdk.Template, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.ToggleDelivery(ctx, params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) newSDKClientForInstance(ctx context.Context, instanceID string) (*template.Client, apierror.Error) {
	config, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	return template.NewClient(config), nil
}
