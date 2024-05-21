package serialize

import (
	"encoding/json"

	"github.com/volatiletech/null/v8"

	"clerk/model"
	"clerk/pkg/strings"
)

type SMSMessageResponse struct {
	Object           string          `json:"object"`
	ID               string          `json:"id"`
	Slug             null.String     `json:"slug"`
	FromPhoneNumber  string          `json:"from_phone_number"`
	ToPhoneNumber    string          `json:"to_phone_number"`
	PhoneNumberID    *string         `json:"phone_number_id"`
	UserID           null.String     `json:"user_id,omitempty"`
	Message          string          `json:"message" logger:"omit"`
	Status           string          `json:"status"`
	Data             json.RawMessage `json:"data" logger:"omit"`
	DeliveredByClerk bool            `json:"delivered_by_clerk"`
}

func SMSMessage(sms *model.SMSMessage) *SMSMessageResponse {
	return &SMSMessageResponse{
		Object:           "sms_message",
		ID:               sms.ID,
		Slug:             sms.Slug,
		FromPhoneNumber:  sms.FromPhoneNumber,
		ToPhoneNumber:    sms.ToPhoneNumber,
		PhoneNumberID:    strings.ToStringPtr(&sms.PhoneNumberID),
		UserID:           sms.UserID,
		Message:          sms.Message,
		Status:           sms.Status,
		Data:             json.RawMessage(sms.Data),
		DeliveredByClerk: sms.DeliveredByClerk,
	}
}
