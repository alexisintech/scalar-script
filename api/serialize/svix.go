package serialize

type SvixStatusResponse struct {
	Enabled bool   `json:"enabled"`
	SvixURL string `json:"svix_url"`
}

func SvixStatus(enabled bool, url string) *SvixStatusResponse {
	return &SvixStatusResponse{
		Enabled: enabled,
		SvixURL: url,
	}
}

type SvixURLResponse struct {
	SvixURL string `json:"svix_url"`
}

func SvixURL(url string) *SvixURLResponse {
	return &SvixURLResponse{
		SvixURL: url,
	}
}
