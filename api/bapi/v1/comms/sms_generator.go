package comms

import (
	"context"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/templates"
	"clerk/utils/database"
)

func (s *Service) generateSMSData(ctx context.Context, exec database.Executor, instanceID string, params CreateSMSParams, identification *model.Identification) (*model.SMSMessageData, apierror.Error) {
	env := environment.FromContext(ctx)

	if params.Message != nil {
		return &model.SMSMessageData{
			Message:          *params.Message,
			Identification:   identification,
			DeliveredByClerk: true,
		}, nil
	}

	templateType := constants.TTSMS
	//slug := *params.TemplateSlug
	slug := ""

	// TODO(templates) reinstate template_slug when available

	template, err := s.templateSvc.GetTemplate(ctx, exec, instanceID, templateType, slug)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Allow only custom templates to be sent programmatically
	if !template.IsCustomTemplate() {
		return nil, apierror.CustomTemplateRequired(slug)
	}

	// TODO populate data
	// Available variables TBD
	// Using common data for now

	commonSMSData, err := s.templateSvc.GetCommonSMSData(ctx, env)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	smsData, err := templates.RenderSMS(commonSMSData, template, identification, nil)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return smsData, nil
}
