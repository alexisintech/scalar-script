package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"clerk/pkg/clerkhttp"
	"clerk/pkg/ctx/clerk_testing_token"
)

func Test_testingTokenParam(t *testing.T) {
	t.Parallel()

	var (
		parsedToken      string
		processedRequest *http.Request
	)

	// parseForm middleware is crucial since it calls r.ParseForm and it is
	// actually used on every one of our endpoints. So this chain mirrors the
	// real-world scenario.
	parseFormMiddleware := clerkhttp.Middleware(parseForm)
	testingTokenMiddleware := clerkhttp.Middleware(testingToken)
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		parsedToken = clerk_testing_token.FromContext(r.Context())
		processedRequest = r
	})
	req := httptest.NewRequest(http.MethodGet, "https://clerk.example.com?__clerk_testing_token=foo", nil)
	parseFormMiddleware(testingTokenMiddleware(handler)).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "foo", parsedToken)
	assert.Empty(t, processedRequest.Form.Get("__clerk_testing_token"))
}
