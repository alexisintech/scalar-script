package allowlists

import (
	"context"
	"time"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/allowlistidentifier"
)

type Service struct {
	db                   database.Database
	sdkConfigConstructor sdkutils.ConfigConstructor
}

func NewService(db database.Database, sdkConfigConstructor sdkutils.ConfigConstructor) *Service {
	return &Service{
		db:                   db,
		sdkConfigConstructor: sdkConfigConstructor,
	}
}

func (s *Service) Create(ctx context.Context, instanceID string, params *allowlistidentifier.CreateParams) (*sdk.AllowlistIdentifier, apierror.Error) {
	sdkClientConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.sdkConfigConstructor, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	res, err := allowlistidentifier.NewClient(sdkClientConfig).Create(ctx, params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return res, nil
}

func (s *Service) Delete(ctx context.Context, instanceID, identifierID string) (*sdk.DeletedResource, apierror.Error) {
	sdkClientConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.sdkConfigConstructor, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := allowlistidentifier.NewClient(sdkClientConfig).Delete(ctx, identifierID)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return response, nil
}

func (s *Service) ListAll(ctx context.Context, instanceID string) (*sdk.AllowlistIdentifierList, apierror.Error) {
	sdkClientConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.sdkConfigConstructor, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	allowlistIdentifierClient := allowlistidentifier.NewClient(sdkClientConfig)
	return sdkutils.WithRetry(func() (*sdk.AllowlistIdentifierList, apierror.Error) {
		response, err := allowlistIdentifierClient.List(ctx, &allowlistidentifier.ListParams{})
		return response, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}
