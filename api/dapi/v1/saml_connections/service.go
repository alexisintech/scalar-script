package saml_connections

import (
	"context"
	"time"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/samlconnection"
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

type ListParams struct {
	pagination pagination.Params
	query      *string
	orderBy    *string
}

func (s *Service) List(ctx context.Context, instanceID string, params ListParams) (*sdk.SAMLConnectionList, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	sdkParams := &samlconnection.ListParams{
		Query:   params.query,
		OrderBy: params.orderBy,
	}
	sdkParams.Limit = sdk.Int64(int64(params.pagination.Limit))
	sdkParams.Offset = sdk.Int64(int64(params.pagination.Offset))
	return sdkutils.WithRetry(func() (*sdk.SAMLConnectionList, apierror.Error) {
		response, err := sdkClient.List(ctx, sdkParams)
		return response, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

func (s *Service) Read(ctx context.Context, instanceID, samlConnectionID string) (*sdk.SAMLConnection, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	return sdkutils.WithRetry(func() (*sdk.SAMLConnection, apierror.Error) {
		response, err := sdkClient.Get(ctx, samlConnectionID)
		return response, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

func (s *Service) Create(ctx context.Context, instanceID string, params *samlconnection.CreateParams) (*sdk.SAMLConnection, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Create(ctx, params)
	return response, sdkutils.ToAPIError(err)
}

func (s *Service) Update(ctx context.Context, instanceID, samlConnectionID string, params *samlconnection.UpdateParams) (*sdk.SAMLConnection, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Update(ctx, samlConnectionID, params)
	return response, sdkutils.ToAPIError(err)
}

func (s *Service) Delete(ctx context.Context, instanceID, samlConnectionID string) (*sdk.DeletedResource, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Delete(ctx, samlConnectionID)
	return response, sdkutils.ToAPIError(err)
}

func (s *Service) newSDKClientForInstance(ctx context.Context, instanceID string) (*samlconnection.Client, apierror.Error) {
	sdkConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	return samlconnection.NewClient(sdkConfig), nil
}
