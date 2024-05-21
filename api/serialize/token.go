package serialize

type TokenResponse struct {
	Object string `json:"object"`
	JWT    string `json:"jwt" logger:"redact"`
}

func Token(jwt string) *TokenResponse {
	return &TokenResponse{
		Object: "token",
		JWT:    jwt,
	}
}
