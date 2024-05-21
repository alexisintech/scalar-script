package clients

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/ctx/environment"

	"github.com/go-playground/validator/v10"
)

type VerifyParams struct {
	Token string `json:"token" form:"token" validate:"required"`
}

func (p VerifyParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

// Verify verifies the given token and returns the active session.
func (s *Service) Verify(ctx context.Context, params VerifyParams) (*serialize.ClientResponseServerAPI, apierror.Error) {
	env := environment.FromContext(ctx)

	if err := params.validate(s.validator); err != nil {
		return nil, err
	}

	client, err := s.cookieService.VerifyCookie(ctx, env.Instance, params.Token)
	if err != nil {
		return nil, err
	}

	clientWithSessions, convErr := s.ConvertClientForBAPI(ctx, client)
	if err != nil {
		return nil, apierror.Unexpected(convErr)
	}

	return serialize.ClientToServerAPI(s.clock, clientWithSessions), nil
}
