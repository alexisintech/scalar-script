package serialize

import (
	"encoding/json"

	"clerk/model"
	"clerk/pkg/time"
)

type IntegrationResponse struct {
	Object     string      `json:"object"`
	ID         string      `json:"id"`
	InstanceID string      `json:"instance_id"`
	ClientID   *string     `json:"client_id"`
	UserID     *string     `json:"user_id"`
	Type       string      `json:"type"`
	Metadata   interface{} `json:"metadata" logger:"omit"`
	CreatedAt  int64       `json:"created_at"`
	UpdatedAt  int64       `json:"updated_at"`
}

var (
	integrationSecretKeyProperties = []string{"api_secret"}
)

func Integration(integration *model.Integration, obfuscateSecrets bool) *IntegrationResponse {
	metadata := integration.Metadata
	if obfuscateSecrets {
		metadata = obfuscateSecretsFromJSON(metadata, integrationSecretKeyProperties)
	}

	return &IntegrationResponse{
		Object:     "integration",
		ID:         integration.ID,
		InstanceID: integration.InstanceID,
		ClientID:   integration.ClientID.Ptr(),
		UserID:     integration.UserID.Ptr(),
		Type:       integration.Type,
		Metadata:   metadata,
		CreatedAt:  time.UnixMilli(integration.CreatedAt),
		UpdatedAt:  time.UnixMilli(integration.UpdatedAt),
	}
}

func obfuscateSecretsFromJSON(jsonWithSecrets []byte, propertiesWithSecrets []string) []byte {
	var jsonMap map[string]interface{}
	err := json.Unmarshal(jsonWithSecrets, &jsonMap)
	if err != nil {
		return jsonWithSecrets
	}
	for _, propertyToObfuscate := range propertiesWithSecrets {
		if _, propertyExists := jsonMap[propertyToObfuscate]; propertyExists {
			jsonMap[propertyToObfuscate] = "••••••••"
		}
	}
	// we can safely ignore the error here
	jsonWithoutSecrets, _ := json.Marshal(jsonMap) // nolint:errchkjson
	return jsonWithoutSecrets
}
