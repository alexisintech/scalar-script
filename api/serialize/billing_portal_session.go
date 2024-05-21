package serialize

const BillingPortalSessionObjectName = "portal_session"

type BillingPortalSessionResponse struct {
	Object      string
	RedirectURL string `json:"redirect_url"`
}

func NewBillingPortalSession(redirectURL string) *BillingPortalSessionResponse {
	return &BillingPortalSessionResponse{
		Object:      BillingPortalSessionObjectName,
		RedirectURL: redirectURL,
	}
}
