package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/ctx/clerk_testing_token"
	"clerk/utils/log"
	"clerk/utils/param"
)

// Testing tokens are only consumed by our Cloudflare WAF, which comes before we
// process the request here. And because we do strict validation of parameters
// in our handlers, we have to drop the __clerk_testing_token param so that
// downstream handlers do not throw an error because of it.
func testingToken(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()

	token := r.URL.Query().Get(param.ClerkTestingToken)
	if token != "" {
		delete(r.Form, param.ClerkTestingToken)

		r = r.WithContext(clerk_testing_token.NewContext(ctx, token))
		log.AddToLogLine(ctx, log.ClerkTestingToken, token)
	}

	return r, nil
}
