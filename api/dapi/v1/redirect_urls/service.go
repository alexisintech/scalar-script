package redirect_urls

import (
	"context"
	"time"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/redirecturl"
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

func (s *Service) Create(ctx context.Context, instanceID string, params redirecturl.CreateParams) (*sdk.RedirectURL, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	redirectURLResponse, err := sdkClient.Create(ctx, &params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return redirectURLResponse, nil
}

func (s *Service) List(ctx context.Context, instanceID string) ([]*sdk.RedirectURL, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	return sdkutils.WithRetry(func() ([]*sdk.RedirectURL, apierror.Error) {
		list, err := sdkClient.List(ctx, &redirecturl.ListParams{})
		return list.RedirectURLs, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

func (s *Service) Delete(ctx context.Context, instanceID, urlID string) (*sdk.DeletedResource, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	deletedResponse, err := sdkClient.Delete(ctx, urlID)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return deletedResponse, nil
}

func (s *Service) newSDKClientForInstance(ctx context.Context, instanceID string) (*redirecturl.Client, apierror.Error) {
	config, apiErr := sdkutils.NewConfigForInstance(ctx, s.sdkConfigConstructor, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	return redirecturl.NewClient(config), nil
}
