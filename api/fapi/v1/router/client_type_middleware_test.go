package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"clerk/pkg/clerkhttp"
	"clerk/pkg/ctx/client_type"

	"github.com/stretchr/testify/assert"
)

func TestSetClientTypeFromQueryParams(t *testing.T) {
	t.Parallel()

	middleware := clerkhttp.Middleware(setClientType)

	var actualClientType client_type.ClientType
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		actualClientType = client_type.FromContext(r.Context())
	})

	// request that looks like a non-browser one (no origin/referer/cookie)
	req := httptest.NewRequest(http.MethodGet, "http://testing", nil)
	middleware(handler).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, client_type.Native, actualClientType)

	// browser-looking request
	req = httptest.NewRequest(http.MethodGet, "http://testing", nil)
	req.Header.Set("Origin", "http://testing")

	middleware(handler).ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, client_type.Browser, actualClientType)

	// request that looks like originating from a browser, but contains the
	// native param
	req = httptest.NewRequest(http.MethodGet, "http://testing", nil)
	query := req.URL.Query()
	query.Add("_is_native", "1")
	req.Header.Set("Origin", "http://testing")
	req.URL.RawQuery = query.Encode()

	middleware(handler).ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, client_type.Native, actualClientType)
}
