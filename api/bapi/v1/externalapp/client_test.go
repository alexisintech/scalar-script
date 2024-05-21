package externalapp_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"clerk/api/bapi/v1/externalapp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetProxyHealth_PathAndQueryString(t *testing.T) {
	t.Parallel()
	rt := &recordingRoundTripper{}
	client := externalapp.NewClient(&http.Client{
		Transport: rt,
	})
	ctx := context.Background()
	proxyHost := "https://proxy.com"
	proxyPath := "proxy"
	proxyURL, err := url.JoinPath(proxyHost, proxyPath)
	require.NoError(t, err)
	rt.Clear()

	// Trigger a request with a domain_id. It will be included
	// in the query string params.
	domainID := "dmn_123"
	_, err = client.GetProxyHealth(ctx, &externalapp.ProxyHealthParams{
		ProxyURL: proxyURL,
		DomainID: domainID,
	})
	require.NoError(t, err)
	assert.Equal(t, domainID, *rt.DomainID)
	assert.Equal(t, "/"+proxyPath+"/v1/proxy-health", rt.URL.Path)
	assert.Equal(t, proxyHost, "https://"+rt.URL.Hostname())
}

func TestGetProxyHealth_SuccessfulResponse(t *testing.T) {
	t.Parallel()
	status := "healthy"
	xForwardedFor := "1.1.1.1"
	successResponse := fmt.Sprintf(
		`{"status":"%s","x_forwarded_for":"%s"}`,
		status,
		xForwardedFor,
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, successResponse)
	}))
	defer ts.Close()

	client := externalapp.NewClient(ts.Client())
	res, err := client.GetProxyHealth(context.Background(), &externalapp.ProxyHealthParams{
		ProxyURL: ts.URL,
	})
	require.NoError(t, err)
	assert.Equal(t, successResponse, string(res.Raw))
	assert.Equal(t, status, res.Status)
	assert.Equal(t, xForwardedFor, res.XForwardedFor)
	assert.True(t, res.Success())
}

func TestGetProxyHealth_UnsuccessfulResponse(t *testing.T) {
	t.Parallel()
	status := "healthy"
	message := "grandmaster flash and the furious five"
	errorResponse := fmt.Sprintf(
		`{"status":"%s","message":"%s"}`,
		status,
		message,
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, errorResponse)
	}))
	defer ts.Close()

	client := externalapp.NewClient(ts.Client())
	res, err := client.GetProxyHealth(context.Background(), &externalapp.ProxyHealthParams{
		ProxyURL: ts.URL,
	})
	require.NoError(t, err)
	assert.Equal(t, errorResponse, string(res.Raw))
	assert.Equal(t, status, res.Status)
	assert.Equal(t, message, res.Message)
	assert.False(t, res.Success())
}

// Can be used as an HTTP transport RoundTripper which
// stores the request path, along with the domain_id
// and instance_id parameters from the query string.
// Warning: This is not thread-safe. It doesn't
// need to be for now.
type recordingRoundTripper struct {
	URL        *url.URL
	DomainID   *string
	InstanceID string
}

func (rt *recordingRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.URL = r.URL
	q := r.URL.Query()
	if q.Has("domain_id") {
		domainID := q.Get("domain_id")
		rt.DomainID = &domainID
	}
	rt.InstanceID = q.Get("instance_id")

	return &http.Response{
		Body: io.NopCloser(bytes.NewReader([]byte("{}"))),
	}, nil
}

// Clear resets the roundtripper fields.
func (rt *recordingRoundTripper) Clear() {
	rt.URL = nil
	rt.DomainID = nil
	rt.InstanceID = ""
}
