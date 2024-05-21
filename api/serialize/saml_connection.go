package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

const SAMLConnectionObjectName = "saml_connection"

type SAMLConnectionResponse struct {
	Object             string                    `json:"object"`
	ID                 string                    `json:"id"`
	Name               string                    `json:"name"`
	Domain             string                    `json:"domain"`
	IdpEntityID        *string                   `json:"idp_entity_id"`
	IdpSsoURL          *string                   `json:"idp_sso_url"`
	IdpCertificate     *string                   `json:"idp_certificate"`
	IdpMetadataURL     *string                   `json:"idp_metadata_url"`
	IdpMetadata        *string                   `json:"idp_metadata"`
	AcsURL             string                    `json:"acs_url"`
	SPEntityID         string                    `json:"sp_entity_id"`
	SPMetadataURL      string                    `json:"sp_metadata_url"`
	AttributeMapping   *attributeMappingResponse `json:"attribute_mapping"`
	Active             bool                      `json:"active"`
	Provider           string                    `json:"provider"`
	UserCount          int64                     `json:"user_count"`
	SyncUserAttributes bool                      `json:"sync_user_attributes"`
	AllowSubdomains    bool                      `json:"allow_subdomains"`
	AllowIdpInitiated  bool                      `json:"allow_idp_initiated"`
	CreatedAt          int64                     `json:"created_at"`
	UpdatedAt          int64                     `json:"updated_at"`
}

type attributeMappingResponse struct {
	UserID       string `json:"user_id"`
	EmailAddress string `json:"email_address"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
}

func SAMLConnection(samlConnection *model.SAMLConnection, domain *model.Domain, userCount int64) *SAMLConnectionResponse {
	return &SAMLConnectionResponse{
		Object:             SAMLConnectionObjectName,
		ID:                 samlConnection.ID,
		Name:               samlConnection.Name,
		Domain:             samlConnection.Domain,
		IdpEntityID:        samlConnection.IdpEntityID.Ptr(),
		IdpSsoURL:          samlConnection.IdpSsoURL.Ptr(),
		IdpCertificate:     samlConnection.IdpCertificate.Ptr(),
		IdpMetadataURL:     samlConnection.IdpMetadataURL.Ptr(),
		IdpMetadata:        samlConnection.IdpMetadata.Ptr(),
		AcsURL:             samlConnection.AcsURL(domain),
		SPEntityID:         samlConnection.SPEntityID(domain),
		SPMetadataURL:      samlConnection.SPMetadataURL(domain),
		AttributeMapping:   attributeMapping(samlConnection),
		Active:             samlConnection.Active,
		Provider:           samlConnection.Provider,
		UserCount:          userCount,
		SyncUserAttributes: samlConnection.SyncUserAttributes,
		AllowSubdomains:    samlConnection.AllowSubdomains,
		AllowIdpInitiated:  samlConnection.AllowIdpInitiated,
		CreatedAt:          time.UnixMilli(samlConnection.CreatedAt),
		UpdatedAt:          time.UnixMilli(samlConnection.UpdatedAt),
	}
}

func attributeMapping(samlConnection *model.SAMLConnection) *attributeMappingResponse {
	return &attributeMappingResponse{
		UserID:       samlConnection.AttributeMapping.UserID,
		EmailAddress: samlConnection.AttributeMapping.EmailAddress,
		FirstName:    samlConnection.AttributeMapping.FirstName,
		LastName:     samlConnection.AttributeMapping.LastName,
	}
}
