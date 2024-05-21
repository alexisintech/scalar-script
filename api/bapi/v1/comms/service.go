package comms

import (
	"context"

	"clerk/pkg/cenv"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/emails"
	"clerk/api/shared/sms"
	shtemplates "clerk/api/shared/templates"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"
)

// Service contains the business logic for communications in the server API.
type Service struct {
	db        database.Database
	validator *validator.Validate

	// services
	emailService *emails.Service
	smsService   *sms.Service
	templateSvc  *shtemplates.Service

	// repositories
	identificationRepo *repository.Identification
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                 deps.DB(),
		validator:          validator.New(),
		emailService:       emails.NewService(deps),
		smsService:         sms.NewService(deps),
		templateSvc:        shtemplates.NewService(deps.Clock()),
		identificationRepo: repository.NewIdentification(),
	}
}

// TODO(templates) reinstate template_slug when available
type CreateSMSParams struct {
	PhoneNumberID string  `json:"phone_number_id" form:"phone_number_id" validate:"required"`
	Message       *string `json:"message" form:"message" validate:"required"`
	//TemplateSlug  *string `json:"template_slug" form:"template_slug"`
}

func (p CreateSMSParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}

	return nil
}

// CreateSMS creates and sends a new SMS message. This endpoint is now deprecated
// Only old instances that have used this endpoint in the past will retain access to it.
func (s *Service) CreateSMS(ctx context.Context, params CreateSMSParams) (*serialize.SMSMessageResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	// Restrict access to this endpoint to only instances that have used it in the past,
	// if the flag is enabled.
	if cenv.Get(cenv.FlagAllowCreateSMSInstanceIDs) != "" {
		if !cenv.ResourceHasAccess(cenv.FlagAllowCreateSMSInstanceIDs, env.Instance.ID) {
			return nil, apierror.BAPIEndpointDeprecated("The SMS Creation endpoint is no longer available to new instances. Instances that have used this endpoint from 13-11-2023 to 20-11-2023 will still retain access, until the endpoint is eventually removed.")
		}
	}

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	identification, err := s.identificationRepo.QueryByIDAndInstance(ctx, s.db, params.PhoneNumberID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if identification == nil || identification.PhoneNumber() == nil {
		return nil, apierror.IdentificationNotFound(params.PhoneNumberID)
	}

	if !identification.IsVerified() {
		return nil, apierror.IdentificationNotFound(params.PhoneNumberID)
	}

	smsData, genErr := s.generateSMSData(ctx, s.db, env.Instance.ID, params, identification)
	if genErr != nil {
		return nil, genErr
	}

	var smsMessage *model.SMSMessage
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		smsMessage, err = s.smsService.Send(ctx, tx, smsData, env)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.SMSMessage(smsMessage), nil
}

// TODO(templates) reinstate template_slug when available
type CreateEmailParams struct {
	EmailAddressID string  `json:"email_address_id" form:"email_address_id" validate:"required"`
	FromEmailName  string  `json:"from_email_name" form:"from_email_name" validate:"required"`
	Subject        *string `json:"subject" form:"subject" validate:"required"`
	Body           *string `json:"body" form:"body" validate:"required"`
	//TemplateSlug   *string `json:"template_slug" form:"template_slug"`
}

func (p CreateEmailParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}

	testEmailAddress := p.FromEmailName + "@example.com"

	if err := validator.Var(testEmailAddress, "required,email"); err != nil {
		return apierror.FormInvalidEmailLocalPart("from_email_name")
	}

	return nil
}

// CreateEmail creates and sends a new email
func (s *Service) CreateEmail(ctx context.Context, params CreateEmailParams) (*serialize.EmailResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if cenv.Get(cenv.FlagAllowCreateEmailApplicationIDs) != "" {
		if !cenv.ResourceHasAccess(cenv.FlagAllowCreateEmailApplicationIDs, env.Instance.ApplicationID) {
			return nil, apierror.BAPIEndpointDeprecated("The Email Creation endpoint is no longer available to new instances. Instances that have used this endpoint from 01-12-2023 to 17-01-2024 will still retain access, until the endpoint is eventually removed.")
		}
	}

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	identification, err := s.identificationRepo.QueryByIDAndInstance(ctx, s.db, params.EmailAddressID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if identification == nil || identification.EmailAddress() == nil {
		return nil, apierror.IdentificationNotFound(params.EmailAddressID)
	}

	if !identification.IsVerified() {
		return nil, apierror.IdentificationNotFound(params.EmailAddressID)
	}

	emailData, genErr := s.generateEmailData(ctx, s.db, env.Instance.ID, params, identification)
	if genErr != nil {
		return nil, genErr
	}

	var email *model.Email
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		email, err = s.emailService.Send(ctx, tx, emailData, env)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.Email(email), nil
}
