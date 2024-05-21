package serialize

import (
	"encoding/json"

	"clerk/model"
	"clerk/pkg/time"
)

type JWTServiceResponse struct {
	Object        string          `json:"object"`
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	AllowedClaims []string        `json:"allowed_claims"`
	Configuration json.RawMessage `json:"configuration" logger:"omit"`
	CreatedAt     int64           `json:"created_at"`
	UpdatedAt     int64           `json:"updated_at"`
}

func JWTService(jwtSers []*model.JWTService, obfuscateSecrets bool) map[model.JWTServiceType]*JWTServiceResponse {
	respDict := make(map[model.JWTServiceType]*JWTServiceResponse)

	jwtSvcMap := model.JWTServiceClass.ToMap(jwtSers)
	for k, v := range jwtSvcMap {
		respDict[k] = jwtResponse(v, obfuscateSecrets)
	}

	if _, hasFirebase := jwtSvcMap[model.JWTServiceTypeFirebase]; !hasFirebase {
		respDict[model.JWTServiceTypeFirebase] = nil
	}

	return respDict
}

var (
	jwtServicesSecretKeyProperties = []string{"api_secret"}
)

func jwtResponse(jwtSer *model.JWTService, obfuscateSecrets bool) *JWTServiceResponse {
	configuration := jwtSer.Configuration
	if obfuscateSecrets {
		configuration = obfuscateSecretsFromJSON(configuration, jwtServicesSecretKeyProperties)
	}

	return &JWTServiceResponse{
		Object:        "jwt_service",
		ID:            jwtSer.ID,
		Type:          jwtSer.Type,
		AllowedClaims: jwtSer.AllowedClaims,
		Configuration: json.RawMessage(configuration),
		CreatedAt:     time.UnixMilli(jwtSer.CreatedAt),
		UpdatedAt:     time.UnixMilli(jwtSer.UpdatedAt),
	}
}
