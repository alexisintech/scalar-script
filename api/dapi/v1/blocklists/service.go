package blocklists

import (
	"context"
	"time"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/blocklistidentifier"
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

func (s *Service) Create(ctx context.Context, instanceID string, params blocklistidentifier.CreateParams) (*sdk.BlocklistIdentifier, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	blocklistIdentifierResponse, err := sdkClient.Create(ctx, &params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return blocklistIdentifierResponse, nil
}

func (s *Service) Delete(ctx context.Context, instanceID, identifierID string) (*sdk.DeletedResource, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := sdkClient.Delete(ctx, identifierID)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) ListAll(ctx context.Context, instanceID string) (*sdk.BlocklistIdentifierList, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	return sdkutils.WithRetry(func() (*sdk.BlocklistIdentifierList, apierror.Error) {
		response, err := sdkClient.List(ctx, &blocklistidentifier.ListParams{})
		return response, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

func (s *Service) newSDKClientForInstance(ctx context.Context, instanceID string) (*blocklistidentifier.Client, apierror.Error) {
	sdkConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	return blocklistidentifier.NewClient(sdkConfig), nil
}
