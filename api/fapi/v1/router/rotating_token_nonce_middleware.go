package router

import (
	"context"
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/ctxkeys"
	"clerk/utils/param"
)

// The rotating_token_nonce is used in native application oauth flows to allow
// the native client to update its JWT once despite changes in its
// rotating_token.
func setRotatingTokenNonce(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	// After we get the nonce, remove it from the map so it doesn't interfere with param checking
	rotatingTokenNonce := r.FormValue(param.RotatingTokenNonce.Name)

	q := r.URL.Query()
	q.Del(param.RotatingTokenNonce.Name)
	r.URL.RawQuery = q.Encode()

	delete(r.Form, param.RotatingTokenNonce.Name)
	delete(r.PostForm, param.RotatingTokenNonce.Name)

	newCtx := context.WithValue(r.Context(), ctxkeys.RotatingTokenNonce, rotatingTokenNonce)
	return r.WithContext(newCtx), nil
}
