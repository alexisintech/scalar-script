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

func (s *Service) generateEmailData(ctx context.Context, exec database.Executor, instanceID string, params CreateEmailParams, identification *model.Identification) (*model.EmailData, apierror.Error) {
	env := environment.FromContext(ctx)

	if params.Subject != nil && params.Body != nil {
		return &model.EmailData{
			FromEmailName:    params.FromEmailName,
			Subject:          *params.Subject,
			Body:             *params.Body,
			Identification:   identification,
			DeliveredByClerk: true,
		}, nil
	}

	// TODO(templates) reinstate template_slug when available

	templateType := constants.TTEmail
	//slug := *params.TemplateSlug
	slug := ""

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

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)

	// TODO(templates) revisit params when custom templates become available
	emailData, err := templates.RenderEmail(ctx, commonEmailData, template, fromEmailName, identification, nil)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	emailData.CustomFlow = true

	return emailData, nil
}
