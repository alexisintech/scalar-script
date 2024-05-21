package bff

import (
	"clerk/api/dapi/serialize"
	"clerk/model"
)

type key struct {
	*serialize.InstanceKeyResponse
	LegacySecret string `json:"legacy_secret" logger:"redact"`
}

type APIKeysResponse struct {
	FapiURL           string `json:"fapi_url"`
	JwksURL           string `json:"jwks_url"`
	PublishableKey    string `json:"publishable_key"`
	PEMPublicKey      string `json:"pem_public_key"`
	SecretKeys        []key  `json:"secret_keys"`
	ShowLegacyAPIKeys bool   `json:"show_legacy_api_keys"`
}

func APIKeys(instance *model.Instance, domain *model.Domain, keys []*model.InstanceKey) *APIKeysResponse {
	response := &APIKeysResponse{
		FapiURL:           domain.FapiURL(),
		JwksURL:           domain.JwksURL(),
		PublishableKey:    instance.PublishableKey(domain),
		PEMPublicKey:      instance.PublicKey,
		ShowLegacyAPIKeys: !instance.UsesKimaKeys(),
		SecretKeys:        make([]key, 0, len(keys)),
	}

	for _, k := range keys {
		pKey := key{
			InstanceKeyResponse: serialize.InstanceKey(k, false),
			LegacySecret:        k.LegacyFormat(),
		}
		// InstanceKey always sets the secret to the legacy format which we don't want,
		// so we override it here so the returned payload includes both key formats.
		// This avoids an extra HTTP request that the frontend was typically making to get the legacy key.
		pKey.Secret = k.Secret
		response.SecretKeys = append(response.SecretKeys, pKey)
	}

	return response
}
