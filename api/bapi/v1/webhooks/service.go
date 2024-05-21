package webhooks

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/webhooks"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/externalapis/svix"
	"clerk/utils/database"
)

type Service struct {
	db             database.Database
	webhookService *webhooks.Service
}

func NewService(db database.Database, svixClient *svix.Client) *Service {
	return &Service{
		db:             db,
		webhookService: webhooks.NewService(svixClient),
	}
}

func (s *Service) CreateSvix(ctx context.Context) (*serialize.SvixURLResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	var svixURLResponse *serialize.SvixURLResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		svixURLResponse, err = s.webhookService.CreateSvix(ctx, tx, env.Instance)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}
	return svixURLResponse, nil
}

func (s *Service) DeleteSvix(ctx context.Context) apierror.Error {
	env := environment.FromContext(ctx)

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.webhookService.DeleteSvix(ctx, tx, env.Instance)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return apiErr
		}
		return apierror.Unexpected(txErr)
	}
	return nil
}

func (s *Service) CreateSvixURL(ctx context.Context) (*serialize.SvixURLResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	return s.webhookService.CreateSvixURL(env.Instance)
}
