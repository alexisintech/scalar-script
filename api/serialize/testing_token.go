package serialize

const TestingTokenObjectName = "testing_token"

type TestingTokenResponse struct {
	Object    string `json:"object"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

func TestingToken(token string, expiresAt int64) *TestingTokenResponse {
	return &TestingTokenResponse{
		Object:    TestingTokenObjectName,
		Token:     token,
		ExpiresAt: expiresAt,
	}
}
