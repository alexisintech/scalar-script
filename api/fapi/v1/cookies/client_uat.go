package cookies

import (
	"net/http"

	"clerk/model"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctxkeys"
)

// WithClientUatResponseWriter is an HTTP middleware that overrides the default
// ResponseWriter with clientUatResponseWriter.
func (s *CookieSetter) WithClientUatResponseWriter() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw := clientUatResponseWriter{
				ResponseWriter: w,
				cookieService:  NewService(s.deps),
				req:            r,
				written:        false,
			}

			next.ServeHTTP(&rw, r)
		})
	}
}

// clientUatResponseWriter is a http.ResponseWriter that writes the
// __client_uat cookie once, before w.WriteHeader() is called. This cookie
// facilitates for server-rendered apps.
//
// Doing this before the call to w.WriteHeader() ensures that the value that
// will be written will be up-to-date, since it will be queried from the
// database _only after_ all other handler code has executed (i.e. on the
// response's way out).
type clientUatResponseWriter struct {
	http.ResponseWriter

	cookieService *Service
	req           *http.Request

	// we only want to write the cookie once
	written bool
}

// WriteHeader writes the cookie and then writes the actual header. The cookie
// has to be written before any calls to WriteHeader() or Write(), otherwise it
// will be a noop.
//
// See https://pkg.go.dev/net/http#ResponseWriter
func (rw *clientUatResponseWriter) WriteHeader(statusCode int) {
	if !rw.written {
		rw.setCookie()
		rw.written = true
	}

	rw.ResponseWriter.WriteHeader(statusCode)
}

// setCookie writes the cookie on the app's eTLD+1 domain.
//
// See https://docs.google.com/document/d/1PGAykkmPjx5Mtdi6j-yHc5Qy-uasjtcXnfGKDy3cHIE/edit#
func (rw *clientUatResponseWriter) setCookie() {
	ctx := rw.req.Context()
	clientType := client_type.FromContext(ctx)

	if clientType.IsNative() {
		return
	}

	env := environment.FromContext(ctx)
	client, _ := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	cookieOven := NewOvenWithDefaultRecipes(ctx)
	clientUats, err := rw.cookieService.CreateClientUatCookies(ctx, cookieOven, client, env.Domain.ClientUatDomain())
	if err != nil {
		return
	}

	for _, c := range clientUats {
		http.SetCookie(rw, c)
	}
}
