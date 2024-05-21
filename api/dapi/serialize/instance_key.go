package serialize

import (
	"clerk/model"
	clerkstrings "clerk/pkg/strings"
	"clerk/pkg/time"
)

type InstanceKeyResponse struct {
	ID         string `json:"id"`
	Object     string `json:"object"`
	Name       string `json:"name"`
	Secret     string `json:"secret" logger:"redact"`
	InstanceID string `json:"instance_id"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type instanceKeysResponse struct {
	InstanceID      string `json:"instance_id"`
	EnvironmentType string `json:"environment_type"`
	FAPIKey         string `json:"fapi_key"`
	BAPIKey         string `json:"bapi_key" logger:"redact"`

	PublishableKey string `json:"publishable_key"`
	SecretKey      string `json:"secret_key"`

	JWTPublicKey    string `json:"jwt_public_key"`
	JWTPublicKeyPEM string `json:"jwt_public_key_pem"`
}

type InstanceKeysResponse struct {
	ApplicationID   string `json:"application_id"`
	ApplicationName string `json:"application_name"`

	// TODO(2023-01-23, agis): remove this when we always show the new API keys
	// to everyone, no matter what
	ShowNewAPIKeys bool `json:"show_new_api_keys"`

	Instances []instanceKeysResponse `json:"instances"`
}

func InstanceKey(key *model.InstanceKey, obfuscate bool) *InstanceKeyResponse {
	instanceKeyResponse := &InstanceKeyResponse{
		ID:         key.ID,
		Object:     "instance_key",
		Name:       key.Name,
		InstanceID: key.InstanceID,
		CreatedAt:  time.UnixMilli(key.CreatedAt),
		UpdatedAt:  time.UnixMilli(key.UpdatedAt),
	}

	secret := key.LegacyFormat()

	if obfuscate {
		instanceKeyResponse.Secret = clerkstrings.Obfuscate(secret)
	} else {
		instanceKeyResponse.Secret = secret
	}

	return instanceKeyResponse
}

func SecretKey(key *model.InstanceKey, obfuscate bool) *InstanceKeyResponse {
	response := &InstanceKeyResponse{
		ID:         key.ID,
		Object:     "secret_key",
		Name:       key.Name,
		Secret:     key.Secret,
		InstanceID: key.InstanceID,
		CreatedAt:  time.UnixMilli(key.CreatedAt),
		UpdatedAt:  time.UnixMilli(key.UpdatedAt),
	}
	if obfuscate {
		response.Secret = clerkstrings.Obfuscate(key.Secret)
	}
	return response
}

func InstanceKeyPublic(instance *model.Instance) *InstanceKeyResponse {
	return &InstanceKeyResponse{
		Object:     "public_key",
		Secret:     instance.PublicKeyForEdge(),
		InstanceID: instance.ID,
	}
}

func InstanceKeyPublicPEM(instance *model.Instance) *InstanceKeyResponse {
	return &InstanceKeyResponse{
		Object:     "public_key_pem",
		Secret:     instance.PublicKey,
		InstanceID: instance.ID,
	}
}

func InstanceFAPIKeyV2(instance *model.Instance, domain *model.Domain) *InstanceKeyResponse {
	return &InstanceKeyResponse{
		Object:     "fapi_key",
		InstanceID: instance.ID,
		Secret:     instance.PublishableKey(domain),
	}
}

// Assumes apps have their Instances relation loaded, and those in turn
// have their Domains and InstanceKeys relations loaded.
func InstanceKeys(apps []*model.Application) []InstanceKeysResponse {
	resp := make([]InstanceKeysResponse, len(apps))

	for i, app := range apps {
		elem := InstanceKeysResponse{
			ApplicationID:   app.ID,
			ApplicationName: app.Name,
			ShowNewAPIKeys:  false,
			Instances:       make([]instanceKeysResponse, len(app.R.Instances)),
		}

		for j, instance := range app.R.Instances {
			ins := model.Instance{Instance: instance}
			dmn := model.Domain{Domain: instance.R.Domains[0]}
			key := model.InstanceKey{InstanceKey: instance.R.InstanceKeys[0]}

			elem.Instances[j] = instanceKeysResponse{
				InstanceID:      instance.ID,
				EnvironmentType: instance.EnvironmentType,
				FAPIKey:         dmn.FapiHost(),
				BAPIKey:         key.LegacyFormat(),
				JWTPublicKey:    ins.PublicKeyForEdge(),
				JWTPublicKeyPEM: ins.PublicKey,
				PublishableKey:  ins.PublishableKey(&dmn),
				SecretKey:       key.Secret,
			}

			if !elem.ShowNewAPIKeys {
				elem.ShowNewAPIKeys = ins.UsesKimaKeys()
			}
		}

		resp[i] = elem
	}

	return resp
}
