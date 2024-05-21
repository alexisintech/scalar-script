package serialize

import (
	"clerk/model"
	"clerk/pkg/templates"
	"clerk/pkg/time"
)

const TemplateObjectName = "template"

type TemplateResponse struct {
	Object             string   `json:"object"`
	Slug               string   `json:"slug"`
	ResourceType       string   `json:"resource_type"`
	TemplateType       string   `json:"template_type"`
	Name               string   `json:"name"`
	Position           int      `json:"position"`
	CanRevert          bool     `json:"can_revert"`
	CanDelete          bool     `json:"can_delete"`
	FromEmailName      *string  `json:"from_email_name,omitempty"`
	ReplyToEmailName   *string  `json:"reply_to_email_name,omitempty"`
	DeliveredByClerk   bool     `json:"delivered_by_clerk"`
	Subject            *string  `json:"subject"`
	Markup             string   `json:"markup"`
	Body               string   `json:"body"`
	AvailableVariables []string `json:"available_variables"`
	RequiredVariables  []string `json:"required_variables"`
	CreatedAt          int64    `json:"created_at"`
	UpdatedAt          int64    `json:"updated_at"`
}

type TemplatePreviewResponse struct {
	Subject             string  `json:"subject,omitempty"`
	Body                string  `json:"body"`
	FromEmailAddress    *string `json:"from_email_address,omitempty"`
	ReplyToEmailAddress *string `json:"reply_to_email_address,omitempty"`
}

func Template(template *model.Template) *TemplateResponse {
	return &TemplateResponse{
		Object:             TemplateObjectName,
		Slug:               template.Slug,
		ResourceType:       template.ResourceType,
		TemplateType:       template.TemplateType,
		Name:               template.Name,
		Position:           0,
		CanRevert:          template.CanRevert(),
		CanDelete:          template.CanDelete(),
		FromEmailName:      template.FromEmailName.Ptr(),
		ReplyToEmailName:   template.ReplyToEmailName.Ptr(),
		DeliveredByClerk:   template.DeliveredByClerk,
		Subject:            template.Subject.Ptr(),
		Markup:             template.Markup,
		Body:               template.Body,
		AvailableVariables: templates.GetAvailableVariables(template),
		RequiredVariables:  templates.GetRequiredVariables(template),
		CreatedAt:          time.UnixMilli(template.CreatedAt),
		UpdatedAt:          time.UnixMilli(template.UpdatedAt),
	}
}

func TemplateDeletedObject(slug, object string) *DeletedObjectResponse {
	return deletedObject("", slug, object)
}
