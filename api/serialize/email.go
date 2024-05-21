package serialize

import (
	"encoding/json"

	"clerk/model"

	"github.com/volatiletech/null/v8"
)

type EmailResponse struct {
	ID               string          `json:"id"`
	Object           string          `json:"object"`
	Slug             null.String     `json:"slug"`
	FromEmailName    string          `json:"from_email_name"`
	ReplyToEmailName null.String     `json:"reply_to_email_name"`
	ToEmailAddress   string          `json:"to_email_address,omitempty"`
	EmailAddressID   null.String     `json:"email_address_id,omitempty"`
	UserID           null.String     `json:"user_id,omitempty"`
	Subject          string          `json:"subject,omitempty"`
	Body             string          `json:"body,omitempty" logger:"omit"`
	BodyPlain        null.String     `json:"body_plain,omitempty" logger:"omit"`
	Status           string          `json:"status,omitempty"`
	Data             json.RawMessage `json:"data" logger:"omit"`
	DeliveredByClerk bool            `json:"delivered_by_clerk"`
}

func Email(email *model.Email) *EmailResponse {
	return &EmailResponse{
		Object:           "email",
		ID:               email.ID,
		Slug:             email.Slug,
		FromEmailName:    email.FromEmailName,
		ReplyToEmailName: email.ReplyToEmailName,
		ToEmailAddress:   email.ToEmailAddress,
		EmailAddressID:   email.EmailAddressID,
		UserID:           email.UserID,
		Subject:          email.Subject,
		Body:             email.Body,
		BodyPlain:        email.BodyPlain,
		Status:           email.Status,
		Data:             json.RawMessage(email.Data),
		DeliveredByClerk: email.DeliveredByClerk,
	}
}
