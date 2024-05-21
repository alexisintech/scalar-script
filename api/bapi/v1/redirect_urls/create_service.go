package redirect_urls

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/ctx/environment"

	"github.com/go-playground/validator/v10"
)

type CreateParams struct {
	URL string `json:"url" form:"url" validate:"required,url"`
}

func (params CreateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.RedirectURLResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	redirectURL := &model.RedirectURL{RedirectURL: &sqbmodel.RedirectURL{
		InstanceID: env.Instance.ID,
		URL:        params.URL,
	}}

	if err := s.redirectUrlsRepo.Insert(ctx, s.db, redirectURL); err != nil {
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueRedirectURL) {
			return nil, apierror.FormAlreadyExists("url")
		}
		return nil, apierror.Unexpected(err)
	}

	return serialize.RedirectURL(redirectURL), nil
}
