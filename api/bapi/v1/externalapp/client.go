package externalapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client is used to invoke proxy related endpoints.
type Client struct {
	httpClient *http.Client
}

// NewClient creates a client that can be used to invoke external endpoints
// to customer's applications. Accepts an optional http.Client.
// If no http.Client is provided, a default client will be used instead.
func NewClient(httpClient *http.Client) *Client {
	c := &Client{
		httpClient: httpClient,
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{
			Timeout: 1 * time.Second,
		}
	}
	return c
}

type ProxyHealthParams struct {
	// The URL of the proxy that needs to be validated. A proxy server is
	// expected to be up and running on this URL and forward requests to FAPI.
	ProxyURL string
	// Controls the domain_id query string parameter.
	DomainID string
	// Sets the X-Forwarded-For request header.
	XForwardedFor string
}

type ProxyHealthResponse struct {
	Status        string `json:"status"`
	Message       string `json:"message,omitempty"`
	XForwardedFor string `json:"x_forwarded_for,omitempty"`
	// Raw, unprocessed response body
	Raw        []byte `json:"-"`
	statusCode int    `json:"-"`
}

// Success returns true if the response status is 200, false otherwise.
func (res *ProxyHealthResponse) Success() bool {
	return res != nil && res.statusCode == http.StatusOK
}

// GetProxyHealth performs a health check for proxy configurations.
// It triggers a request to the ProxyURL and expects to successfully
// reach the /v1/proxy-health endpoint through the reverse proxy.
func (c *Client) GetProxyHealth(ctx context.Context, params *ProxyHealthParams) (*ProxyHealthResponse, error) {
	endpoint, err := buildProxyHealthURL(params.ProxyURL, params.DomainID)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-forwarded-for", params.XForwardedFor)

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	res := ProxyHealthResponse{
		statusCode: response.StatusCode,
		Raw:        raw,
	}
	// If the proxy is not configured correctly, it might not reach
	// our FAPI at all. The response might not be valid JSON, but that's
	// ok. We have already stored the raw response, so let's return
	// without an error.
	_ = json.Unmarshal(raw, &res)
	return &res, nil
}

// Append the FAPI proxy health endpoint to the base proxy URL and
// pass domain_id and instance_id as query parameters.
func buildProxyHealthURL(base string, domainID string) (*url.URL, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	u = u.JoinPath("v1/proxy-health")
	q := u.Query()
	q.Add("domain_id", domainID)
	u.RawQuery = q.Encode()
	return u, nil
}
