package router

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/ctx/devbrowser"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctxkeys"

	"github.com/stretchr/testify/assert"
)

func TestSetDevBrowserRequestContexts(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		clientType    client_type.ClientType
		instanceType  constants.EnvironmentType
		currentDomain string
		origin        string
		expected      devbrowser.DevBrowser
	}{
		{
			name:         "Production instance",
			clientType:   client_type.Browser,
			instanceType: constants.ETProduction,
			expected:     "",
		},
		{
			name:         "Native client",
			clientType:   client_type.Native,
			instanceType: constants.ETDevelopment,
			expected:     "",
		},
		{
			name:          "Accessing FAPI with no Origin",
			clientType:    client_type.Browser,
			instanceType:  constants.ETDevelopment,
			currentDomain: "foo.bar-13.dev.lclclerk.com",
			origin:        "",
			expected:      devbrowser.FirstParty,
		},
		{
			name:          "Accessing FAPI from localhost (cross-site)",
			clientType:    client_type.Browser,
			instanceType:  constants.ETDevelopment,
			currentDomain: "foo.bar-13.dev.lclclerk.com",
			origin:        "http://localhost:3000",
			expected:      devbrowser.ThirdParty,
		},
		{
			name:          "Accessing FAPI from Accounts (same-site)",
			clientType:    client_type.Browser,
			instanceType:  constants.ETDevelopment,
			currentDomain: "foo.bar-13.dev.lclclerk.com",
			origin:        "https://accounts.foo.bar-13.dev.lclclerk.com",
			expected:      devbrowser.FirstParty,
		},
		{
			name:          "Accessing FAPI from Publishable key Accounts (same-site)",
			clientType:    client_type.Browser,
			instanceType:  constants.ETDevelopment,
			currentDomain: "foo.bar-13.dev.lclclerk.com",
			origin:        "https://foo-bar-13.accounts.lclclerk.com",
			expected:      devbrowser.FirstParty,
		},
	}

	for _, testCase := range testCases {
		tc := testCase
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			middleware := clerkhttp.Middleware(setDevBrowserRequestContext)

			var actualDevBrowser devbrowser.DevBrowser
			handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				actualDevBrowser = devbrowser.FromContext(r.Context())
			})
			env := &model.Env{
				Domain:   &model.Domain{Domain: &sqbmodel.Domain{Name: tc.currentDomain}},
				Instance: &model.Instance{Instance: &sqbmodel.Instance{EnvironmentType: string(tc.instanceType)}},
			}

			ctx := client_type.NewContext(context.Background(), tc.clientType)
			ctx = environment.NewContext(ctx, env)

			req := httptest.NewRequest(http.MethodGet, "https://testing.com", nil)
			req.Header.Set("Origin", tc.origin)
			middleware(handler).ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
			assert.Equal(t, tc.expected, actualDevBrowser)
		})
	}
}

func Test_validateRequestOrigin(t *testing.T) {
	t.Parallel()

	middleware := clerkhttp.Middleware(validateRequestOrigin)
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {})

	// populate necessary deps in request's context
	env := &model.Env{
		Domain:   &model.Domain{Domain: &sqbmodel.Domain{Name: "foo.com"}},
		Instance: &model.Instance{Instance: &sqbmodel.Instance{EnvironmentType: string(constants.ETProduction)}},
	}

	testcases := []struct {
		origin               string
		endpoint             string
		authorizationHeader  string
		expectedResponseCode int
	}{
		{"https://foo.com", "", "", http.StatusOK},
		{"https://www.foo.com", "", "", http.StatusOK},
		{"https://bar.foo.com", "", "", http.StatusOK},
		{"https://yo.bar.foo.com", "", "", http.StatusOK},

		// Ignored endpoints
		{"https://external-origin.com", "/v1/client/link", "", http.StatusOK},
		{"https://external-origin.com", "/v1/client/sync", "", http.StatusOK},
		{"https://external-origin.com", "/v1/dev_browser/init", "", http.StatusOK},
		{"https://external-origin.com", "/v1/tickets/accept", "", http.StatusOK},
		{"https://external-origin.com", "/v1/oauth_callback", "", http.StatusOK},
		{"https://external-origin.com", "/v1/verify", "", http.StatusOK},
		{"https://external-origin.com", "/v1/health", "", http.StatusOK},
		{"https://external-origin.com", "/.well-known/", "", http.StatusOK},
		{"https://external-origin.com", "/", "", http.StatusOK},
		{"https://external-origin.com", "/v1/saml/acs/samlc_abcd", "", http.StatusOK},

		{"foo.com", "/v1/client", "", http.StatusBadRequest},
		{"foo.com", "", "", http.StatusBadRequest},
		{"foo", "", "", http.StatusBadRequest},
		{"https://foo", "", "", http.StatusBadRequest},
		{"http://foo.com", "", "", http.StatusBadRequest},
		{"http://bar.foo.com", "", "", http.StatusBadRequest},
		{"www.foo.com", "", "", http.StatusBadRequest},
		{"https://evilfoo.com", "", "", http.StatusBadRequest},
		{"", "", "", http.StatusBadRequest},
		{"   ", "", "", http.StatusBadRequest},
		{"https://", "", "", http.StatusBadRequest},
		{"https://   ", "", "", http.StatusBadRequest},

		// setting both the Origin and Authorization headers
		{"https://foo.com", "", "https://app.foo.com", http.StatusBadRequest},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(fmt.Sprintf("origin=%s | authorization=%s -> %s", tc.origin, tc.authorizationHeader, tc.endpoint), func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			ctx = context.WithValue(ctx, ctxkeys.CSRFPresentAndValid, true)
			ctx = environment.NewContext(ctx, env)

			target := "https://clerk.foo.com" + tc.endpoint

			req := httptest.NewRequest(http.MethodGet, target, nil)
			req.Header.Set("Origin", tc.origin)

			if tc.authorizationHeader != "" {
				req.Header.Set("Authorization", tc.authorizationHeader)
			}

			recorder := httptest.NewRecorder()
			middleware(handler).ServeHTTP(recorder, req.WithContext(ctx))

			assert.Equal(t, tc.expectedResponseCode, recorder.Code)
		})
	}
}
