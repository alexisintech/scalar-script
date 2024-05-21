package serialize

import "clerk/pkg/apiversioning"

type APIVersionResponse struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func APIVersion(v apiversioning.APIVersion) *APIVersionResponse {
	return &APIVersionResponse{
		Name:   v.GetName(),
		Status: v.GetStatus().String(),
	}
}
