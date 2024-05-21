package internalapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	HTTPClient *http.Client
	baseURL    string
}

// NewClient creates a client that can be used to invoke proxy
// related endpoints. Accepts an optional http.Client.
// If no http.Client is provided, a default client will be used instead.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	c := &Client{
		baseURL:    baseURL,
		HTTPClient: httpClient,
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 1 * time.Second}
	}
	return c
}

type WhatIsMyIP struct {
	IP     string `json:"ip"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// WhatIsMyIP invokes the /v1/internal/whatismyip endpoint.
func (c *Client) WhatIsMyIP(ctx context.Context, authorization string) (*WhatIsMyIP, error) {
	endpoint, err := url.JoinPath(c.baseURL, "/internal/whatismyip")
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("authorization", authorization)

	response, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	var decoded WhatIsMyIP
	err = json.NewDecoder(response.Body).Decode(&decoded)
	if err != nil {
		return nil, err
	}

	return &decoded, nil
}
