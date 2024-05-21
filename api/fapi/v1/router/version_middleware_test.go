package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clerk/utils/param"

	"github.com/stretchr/testify/assert"
)

func TestReadVersionFromQueryParams(t *testing.T) {
	t.Parallel()
	expectedVersion := "v1.0.1"

	req := httptest.NewRequest(http.MethodGet, "http://testing", nil)
	query := req.URL.Query()
	query.Add(param.ClerkJSVersion, expectedVersion)
	req.URL.RawQuery = query.Encode()

	actualVersion := readVersion(req)
	assert.Equal(t, expectedVersion, actualVersion)
}

func TestLogClerkJSVersionFromHeader(t *testing.T) {
	t.Parallel()
	expectedVersion := "v1.0.1"

	req := httptest.NewRequest(http.MethodGet, "http://testing", nil)
	req.Header.Set(strings.ToUpper(ClerkJSVersionHeader), expectedVersion)

	actualVersion := readVersion(req)
	assert.Equal(t, expectedVersion, actualVersion)
}

func TestLogClerkJSVersionUsesNoVersionIfNoVersionIsIncluded(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "http://testing", nil)
	actualVersion := readVersion(req)
	assert.Equal(t, noVersion, actualVersion)
}

func TestLogClerkJSVersion_FallbackToNoVersionForInvalid(t *testing.T) {
	t.Parallel()
	invalidVersion := "4.43.ß"

	// Invalid in query param
	req := httptest.NewRequest(http.MethodGet, "http://testing", nil)
	query := req.URL.Query()
	query.Add(param.ClerkJSVersion, invalidVersion)
	req.URL.RawQuery = query.Encode()

	actualVersion := readVersion(req)
	assert.Equal(t, noVersion, actualVersion)

	// Invalid in header
	req = httptest.NewRequest(http.MethodGet, "http://testing", nil)
	req.Header.Set(strings.ToUpper(ClerkJSVersionHeader), invalidVersion)

	actualVersion = readVersion(req)
	assert.Equal(t, noVersion, actualVersion)
}
