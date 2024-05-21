package serialize

type ConnectResponse struct {
	RedirectURL string `json:"redirect_url"`
}

func BillingConnect(redirectURL string) *ConnectResponse {
	return &ConnectResponse{
		RedirectURL: redirectURL,
	}
}
