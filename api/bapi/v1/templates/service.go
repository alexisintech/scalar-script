package templates

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"

	"clerk/api/apierror"
	"clerk/api/serialize"
	shtemplates "clerk/api/shared/templates"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/templates"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/validate"
)

// TODO(templates) Consider previewing metadata variables as {{metadata}} (escaped)

var publicMetadataRegexp = regexp.MustCompile(`public_metadata(\.\w*)*`)

// Service contains the business logic of all operations specific to templates in the server API.
type Service struct {
	db        database.Database
	validator *validator.Validate

	// services
	templateSvc *shtemplates.Service

	// repositories
	domainRepo            *repository.Domain
	subscriptionPlansRepo *repository.SubscriptionPlans
	templateRepo          *repository.Templates
}

func NewService(clock clockwork.Clock, db database.Database) *Service {
	return &Service{
		db:                    db,
		validator:             validator.New(),
		templateSvc:           shtemplates.NewService(clock),
		domainRepo:            repository.NewDomain(),
		subscriptionPlansRepo: repository.NewSubscriptionPlans(),
		templateRepo:          repository.NewTemplates(),
	}
}

// ReadAllPaginated calls ReadAll and includes the total_count along
// with the results.
func (s *Service) ReadAllPaginated(ctx context.Context, templateType string) (*serialize.PaginatedResponse, apierror.Error) {
	list, apiErr := s.ReadAll(ctx, templateType)
	if apiErr != nil {
		return nil, apiErr
	}
	totalCount := len(list)
	data := make([]any, totalCount)
	for i, template := range list {
		data[i] = template
	}
	return serialize.Paginated(data, int64(totalCount)), nil
}

// ReadAll returns all templates for the given instance
// Compact payload is returned
func (s *Service) ReadAll(ctx context.Context, templateType string) ([]*serialize.TemplateResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := validateTemplateType(templateType); apiErr != nil {
		return nil, apiErr
	}

	excludedSlugs := make([]string, 0)
	if !env.AuthConfig.IsOrganizationsEnabled() {
		excludedSlugs = append(excludedSlugs, constants.OrganizationInvitationSlug)
		excludedSlugs = append(excludedSlugs, constants.OrganizationInvitationAcceptedSlug)
	}
	if !env.AuthConfig.IsOrganizationDomainsEnabled() {
		excludedSlugs = append(excludedSlugs, constants.AffiliationCodeSlug)
		excludedSlugs = append(excludedSlugs, constants.OrganizationJoinedSlug)
		excludedSlugs = append(excludedSlugs, constants.OrganizationMembershipRequestedSlug)
	}

	if !cenv.ResourceHasAccess(cenv.FlagAllowPasskeysInstanceIDs, env.Instance.ID) {
		excludedSlugs = append(excludedSlugs, constants.PasskeyAddedSlug, constants.PasskeyRemovedSlug)
	}

	allTemplates, err := s.templateRepo.FindAllByTemplateType(ctx, s.db, env.Instance.ID, templateType, excludedSlugs...)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]*serialize.TemplateResponse, 0)
	for _, template := range allTemplates {
		templateInfo := templates.GetTemplate(template)
		if templateInfo != nil && !templateInfo.Visible() {
			continue
		}
		responses = append(responses, serialize.Template(template))
	}

	return responses, nil
}

// Read returns the model.Template with the given slug
// Extended payload is returned
func (s *Service) Read(ctx context.Context, templateType, slug string) (*serialize.TemplateResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := validateTemplateType(templateType); apiErr != nil {
		return nil, apiErr
	}

	template, err := s.templateRepo.QueryCurrentByTemplateTypeAndSlug(ctx, s.db, env.Instance.ID, templateType, slug)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if template == nil {
		return nil, apierror.TemplateNotFound(slug)
	}

	return serialize.Template(template), nil
}

type UpsertParams struct {
	Name             string  `json:"name" form:"name" validate:"required_if=TemplateType email,required_if=TemplateType sms"`
	Subject          string  `json:"subject" form:"subject" validate:"required_if=TemplateType email"`
	Body             string  `json:"body" form:"body" validate:"required_if=TemplateType email,required_if=TemplateType sms"`
	Markup           *string `json:"markup" form:"markup"`
	TemplateType     string  `json:"-" form:"-" validate:"oneof=email sms"`
	Slug             string  `json:"-" form:"-"`
	FromEmailName    *string `json:"from_email_name" form:"from_email_name"`
	ReplyToEmailName *string `json:"reply_to_email_name" form:"reply_to_email_name"`
	DeliveredByClerk *bool   `json:"delivered_by_clerk" form:"delivered_by_clerk"`
}

type ToggleDeliveryParams struct {
	TemplateType     string `json:"-" form:"-" validate:"oneof=email sms"`
	Slug             string `json:"-" form:"-"`
	DeliveredByClerk bool   `json:"delivered_by_clerk" form:"delivered_by_clerk"`
}

func (p ToggleDeliveryParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}

	return nil
}

func (p ToggleDeliveryParams) toUpsertParams(template *model.Template) UpsertParams {
	return UpsertParams{
		Name:             template.Name,
		Subject:          template.Subject.String,
		Body:             template.Body,
		Markup:           &template.Markup,
		TemplateType:     template.TemplateType,
		Slug:             template.Slug,
		FromEmailName:    template.FromEmailName.Ptr(),
		ReplyToEmailName: template.ReplyToEmailName.Ptr(),
		DeliveredByClerk: &p.DeliveredByClerk,
	}
}

func (p UpsertParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}

	if p.FromEmailName != nil && *p.FromEmailName != "" {
		if err := validate.EmailName(*p.FromEmailName, "from_email_name"); err != nil {
			return err
		}
	}

	if p.ReplyToEmailName != nil && *p.ReplyToEmailName != "" {
		if err := validate.EmailName(*p.ReplyToEmailName, "reply_to_email_name"); err != nil {
			return err
		}
	}

	return nil
}

// Upsert user template
func (s *Service) Upsert(ctx context.Context, params UpsertParams) (*serialize.TemplateResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	apiErr := s.ensureTemplateFeatureAvailable(ctx, env, params.TemplateType)
	if apiErr != nil {
		return nil, apiErr
	}

	currentTemplate, err := s.templateRepo.QueryCurrentByTemplateTypeAndSlug(ctx, s.db, env.Instance.ID, params.TemplateType, params.Slug)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if apiErr = validateRequiredVariables(params, currentTemplate); apiErr != nil {
		return nil, apiErr
	}

	if !cenv.GetBool(cenv.FlagAllowCustomTemplateCreation) && currentTemplate == nil {
		return nil, apierror.CustomTemplatesNotAvailable()
	}

	template := newTemplateFromParams(params, currentTemplate, env.Instance.ID)

	// Check if the template renders
	preview, previewErr := s.previewTemplate(ctx, env, template)
	if previewErr != nil {
		return nil, previewErr
	}

	// If SMS template, check that preview is within character limits
	if template.IsSMS() {
		encoding, _, segmentCount := templates.AnalyzeSMS(preview.Body)

		if encoding == constants.SMSEncodingGSM7 && segmentCount > 1 {
			return nil, apierror.SMSMaxLengthExceeded(encoding)
		} else if encoding == constants.SMSEncodingUCS2 && segmentCount > 2 {
			return nil, apierror.SMSMaxLengthExceeded(encoding)
		}
	}

	err = s.templateRepo.Upsert(ctx, s.db, template, true)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Template(template), nil
}

type PreviewParams struct {
	Subject          string  `json:"subject" form:"subject" validate:"required_if=TemplateType email"`
	Body             string  `json:"body" form:"body" validate:"required_if=TemplateType email,required_if=TemplateType sms"`
	TemplateType     string  `json:"-" form:"-" validate:"oneof=email sms"`
	Slug             string  `json:"-" form:"-"`
	FromEmailName    *string `json:"from_email_name" form:"from_email_name"`
	ReplyToEmailName *string `json:"reply_to_email_name" form:"reply_to_email_name"`
}

func (p PreviewParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}

	return nil
}

func (p PreviewParams) toUpsertParams() UpsertParams {
	return UpsertParams{
		Subject:          p.Subject,
		Body:             p.Body,
		TemplateType:     p.TemplateType,
		Slug:             p.Slug,
		FromEmailName:    p.FromEmailName,
		ReplyToEmailName: p.ReplyToEmailName,
	}
}

// Preview a template with sample data
// TODO support user-provided variables
func (s *Service) Preview(ctx context.Context, params PreviewParams) (*serialize.TemplatePreviewResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	template := newTemplateFromParams(params.toUpsertParams(), nil, env.Instance.ID)

	replaceMetadataVariablesForPreview(template)

	result, previewErr := s.previewTemplate(ctx, env, template)
	if previewErr != nil {
		return nil, previewErr
	}

	return result, nil
}

// Delete deletes a custom template
func (s *Service) Delete(ctx context.Context, templateType, slug string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := validateTemplateType(templateType); apiErr != nil {
		return nil, apiErr
	}

	apiErr := s.ensureTemplateFeatureAvailable(ctx, env, templateType)
	if apiErr != nil {
		return nil, apiErr
	}

	template, err := s.templateRepo.QueryCurrentByTemplateTypeAndSlug(ctx, s.db, env.Instance.ID, templateType, slug)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if template == nil {
		return nil, apierror.TemplateNotFound(slug)
	}

	if !template.CanDelete() {
		return nil, apierror.TemplateDeletionRestricted(slug)
	}

	err = s.templateRepo.DeleteByID(ctx, s.db, template.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.TemplateDeletedObject(slug, serialize.TemplateObjectName), nil
}

// Revert deletes the user template that overrides a system template & returns the original
func (s *Service) Revert(ctx context.Context, templateType, slug string) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := validateTemplateType(templateType); apiErr != nil {
		return nil, apiErr
	}

	template, err := s.templateRepo.QueryCurrentByTemplateTypeAndSlug(ctx, s.db, env.Instance.ID, templateType, slug)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if template == nil {
		return nil, apierror.TemplateNotFound(slug)
	}

	if !template.CanRevert() {
		return nil, apierror.TemplateRevertRestricted(slug)
	}

	// Reversible template exists, delete it
	err = s.templateRepo.DeleteByID(ctx, s.db, template.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	parentTemplate, err := s.templateRepo.FindByID(ctx, s.db, template.ParentID.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Template(parentTemplate), nil
}

// ToggleDelivery toggles the delivered_by_clerk flag for the template based on the provided param
// Does not check for feature availability, it's available in any plan.
func (s *Service) ToggleDelivery(ctx context.Context, params ToggleDeliveryParams) (*serialize.TemplateResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	currentTemplate, err := s.templateRepo.QueryCurrentByTemplateTypeAndSlug(ctx, s.db, env.Instance.ID, params.TemplateType, params.Slug)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Delivery can be toggled only for an existing template
	if currentTemplate == nil {
		return nil, apierror.ResourceNotFound()
	}

	// essentially upsert with the same values as the existing template, but merge delivered_by_clerk from params

	template := newTemplateFromParams(params.toUpsertParams(currentTemplate), currentTemplate, env.Instance.ID)

	err = s.templateRepo.Upsert(ctx, s.db, template, true)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Template(template), nil
}

// Check for business logic errors
// * If required_variables have been specified on the preexisting template, ensure the new body contains them
func validateRequiredVariables(params UpsertParams, currentTemplate *model.Template) apierror.Error {
	if currentTemplate == nil {
		return nil
	}

	var apiErrs apierror.Error

	requiredVariables := templates.GetRequiredVariables(currentTemplate)

	for _, requiredVariable := range requiredVariables {
		if !strings.Contains(params.Body, requiredVariable) {
			apiErrs = apierror.Combine(apiErrs, apierror.RequiredVariableMissing(requiredVariable))
		}
	}

	return apiErrs
}

func (s *Service) ensureTemplateFeatureAvailable(
	ctx context.Context,
	env *model.Env,
	templateType string,
) apierror.Error {
	// For development instances, the feature is on so that users can try it
	if env.Instance.IsDevelopment() {
		return nil
	}

	plans, err := s.subscriptionPlansRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	features, err := billing.TemplateFeatures(templateType)
	if err != nil {
		return apierror.Unexpected(err)
	}
	unsupportedFeatures := billing.ValidateSupportedFeatures(features, env.Subscription, plans...)
	if len(unsupportedFeatures) > 0 {
		return apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
	}

	return nil
}

func (s *Service) previewTemplate(ctx context.Context, env *model.Env, template *model.Template) (*serialize.TemplatePreviewResponse, apierror.Error) {
	switch constants.TemplateType(template.TemplateType) {
	case constants.TTEmail:
		result, err := s.previewEmail(ctx, env, template)
		if err != nil {
			return nil, apierror.InvalidTemplateBody()
		}
		return result, nil
	case constants.TTSMS:
		result, err := s.previewSMS(ctx, env, template)
		if err != nil {
			return nil, apierror.InvalidTemplateBody()
		}
		return result, nil
	default:
		return nil, apierror.TemplateTypeUnsupported(template.TemplateType)
	}
}

func (s *Service) previewEmail(ctx context.Context, env *model.Env, template *model.Template) (*serialize.TemplatePreviewResponse, error) {
	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return nil, err
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)

	renderer, ok := templates.GetTemplate(template).(templates.EmailRenderer)
	if !ok {
		renderer = templates.CustomEmail{}
	}
	emailData, err := templates.RenderEmail(ctx, renderer.PreviewData(commonEmailData), template, fromEmailName, nil, nil)
	if err != nil {
		return nil, err
	}

	fromEmailAddress, err := s.getEmailAddress(ctx, env.Instance, fromEmailName)
	if err != nil {
		return nil, err
	}

	var replyToEmailAddress *string
	if template.ReplyToEmailName.Valid {
		replyToEmailAddressStr, err := s.getEmailAddress(ctx, env.Instance, template.ReplyToEmailName.String)
		if err != nil {
			return nil, err
		}
		replyToEmailAddress = &replyToEmailAddressStr
	}

	templatePreviewResponse := &serialize.TemplatePreviewResponse{
		Subject:             emailData.Subject,
		Body:                emailData.Body,
		FromEmailAddress:    &fromEmailAddress,
		ReplyToEmailAddress: replyToEmailAddress,
	}

	return templatePreviewResponse, nil
}

func (s *Service) previewSMS(ctx context.Context, env *model.Env, template *model.Template) (*serialize.TemplatePreviewResponse, error) {
	commonSMSData, err := s.templateSvc.GetCommonSMSData(ctx, env)
	if err != nil {
		return nil, err
	}

	renderer, ok := templates.GetTemplate(template).(templates.SMSRenderer)
	if !ok {
		renderer = templates.CustomSMS{}
	}
	smsData, err := templates.RenderSMS(renderer.PreviewData(commonSMSData), template, nil, nil)
	if err != nil {
		return nil, err
	}

	return &serialize.TemplatePreviewResponse{
		Body: smsData.Message,
	}, nil
}

func validateTemplateType(templateType string) apierror.Error {
	switch templateType {
	case string(constants.TTEmail), string(constants.TTSMS):
		return nil
	default:
		return apierror.TemplateTypeUnsupported(templateType)
	}
}

func newTemplateFromParams(params UpsertParams, currentTemplate *model.Template, instanceID string) *model.Template {
	tmpl := &model.Template{Template: &sqbmodel.Template{
		InstanceID:   instanceID,
		ResourceType: string(constants.RTUser),
		Slug:         params.Slug,
		TemplateType: params.TemplateType,
		Body:         params.Body,
		Name:         params.Name,
	}}

	if params.FromEmailName != nil {
		fromEmailName := strings.TrimSpace(*params.FromEmailName)

		if fromEmailName != "" {
			tmpl.FromEmailName = null.StringFrom(fromEmailName)
		}
	}

	if params.ReplyToEmailName != nil {
		replyToEmailName := strings.TrimSpace(*params.ReplyToEmailName)

		if replyToEmailName != "" {
			tmpl.ReplyToEmailName = null.StringFrom(replyToEmailName)
		}
	}

	if currentTemplate == nil {
		tmpl.DeliveredByClerk = true
	} else {
		tmpl.DeliveredByClerk = currentTemplate.DeliveredByClerk

		if currentTemplate.IsUserTemplate() {
			tmpl.ID = currentTemplate.ID
			tmpl.ParentID = currentTemplate.ParentID
		} else {
			tmpl.ParentID = null.StringFrom(currentTemplate.ID)

			// If not provided, inherit from parent
			if params.FromEmailName == nil {
				tmpl.FromEmailName = currentTemplate.FromEmailName
			}
			if params.ReplyToEmailName == nil {
				tmpl.ReplyToEmailName = currentTemplate.ReplyToEmailName
			}
		}

		if instanceID == currentTemplate.InstanceID {
			tmpl.ResourceType = currentTemplate.ResourceType
		}
	}

	if params.TemplateType == string(constants.TTEmail) {
		tmpl.Subject = null.StringFrom(params.Subject)

		if params.Markup != nil {
			tmpl.Markup = *params.Markup
		}
	}

	// Override delivered_by_clerk from params if provided
	if params.DeliveredByClerk != nil {
		tmpl.DeliveredByClerk = *params.DeliveredByClerk
	}

	return tmpl
}

// replaceMetadataVariablesForPreview replaces metadata variables for preview with a fallback variable
// since we can't really predict their structure nor their semantics
// public_metadata         => public_metadata_fallback
// public_metadata.foo     => public_metadata_fallback
// public_metadata.foo.bar => public_metadata_fallback
// etc
func replaceMetadataVariablesForPreview(template *model.Template) {
	if template.Subject.Valid {
		template.Subject = null.StringFrom(publicMetadataRegexp.ReplaceAllString(template.Subject.String, "public_metadata_fallback"))
	}

	template.Markup = publicMetadataRegexp.ReplaceAllString(template.Markup, "public_metadata_fallback")
	template.Body = publicMetadataRegexp.ReplaceAllString(template.Body, "public_metadata_fallback")
}

func (s *Service) getEmailAddress(ctx context.Context, instance *model.Instance, emailName string) (string, error) {
	domain, err := s.domainRepo.FindByIDAndInstanceID(ctx, s.db, instance.ActiveDomainID, instance.ID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s@%s", emailName, domain.FromEmailDomainName()), nil
}
