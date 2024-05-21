package serialize

import (
	"clerk/model"
)

type LinkedIdentificationResponse struct {
	IdentType string `json:"type"`
	IdentID   string `json:"id"`
}

func linkedIdentification(i *model.Identification) LinkedIdentificationResponse {
	return LinkedIdentificationResponse{IdentType: i.Type, IdentID: i.ID}
}
