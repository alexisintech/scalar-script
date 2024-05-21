package cookies

import (
	"context"
	"net/http"
	"time"

	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	oven "clerk/pkg/cookies"
	"clerk/pkg/ctx/cookiessuffix"
	"clerk/pkg/ctx/environment"
)

func NewOvenWithDefaultRecipes(ctx context.Context) *oven.Oven {
	return NewOven(ctx, nil)
}

func NewOven(ctx context.Context, defaultRecipes []oven.Recipe) *oven.Oven {
	if !cenv.IsEnabled(cenv.FlagUseSuffixedCookies) {
		return &oven.Oven{
			Recipes: []oven.Recipe{&oven.BaseRecipe{}},
		}
	}

	if cookiessuffix.FromContext(ctx) {
		return &oven.Oven{
			Recipes: []oven.Recipe{oven.NewSuffixedRecipe(ctx)},
		}
	}

	if defaultRecipes == nil {
		defaultRecipes = []oven.Recipe{&oven.BaseRecipe{}, oven.NewSuffixedRecipe(ctx)}
	}

	return &oven.Oven{Recipes: defaultRecipes}
}

const (
	// this value indicates that the user is signed out
	clientUatValueSignedOut = "0"
)

func NewClientUatSignedIn(value, domain string, sameSite http.SameSite) *http.Cookie {
	return newClientUat(domain, value, sameSite)
}

func NewClientUatSignedOut(domain string, sameSite http.SameSite) *http.Cookie {
	return newClientUat(domain, clientUatValueSignedOut, sameSite)
}

func NewClientUatClear() *http.Cookie {
	return &http.Cookie{
		Name:    constants.ClientUatCookie,
		Path:    "/",
		Expires: time.Unix(0, 0),
	}
}

func newClientUat(domain, value string, sameSite http.SameSite) *http.Cookie {
	return &http.Cookie{
		Name:     constants.ClientUatCookie,
		Value:    value,
		Domain:   domain,
		Path:     "/",
		MaxAge:   tenYears,
		Secure:   true,
		SameSite: sameSite,
		HttpOnly: false,
	}
}

func NewDevBrowser(value string) *http.Cookie {
	return &http.Cookie{
		Name:    constants.DevBrowserCookie,
		Value:   value,
		Path:    "/",
		Expires: time.Now().UTC().AddDate(1, 0, 0),
	}
}

const (
	// this value indicates that the user is signed out
	sessionValueSignedOut = ""
)

func NewSessionSignedIn(ctx context.Context, value string) *http.Cookie {
	env := environment.FromContext(ctx)
	secure := env.Instance.IsProduction()
	expires := time.Now().AddDate(1, 0, 0)

	return newSession(value, secure, expires)
}

func NewSessionClear(ctx context.Context) *http.Cookie {
	env := environment.FromContext(ctx)
	secure := env.Instance.IsProduction()
	expires := time.Unix(0, 0)

	return newSession(sessionValueSignedOut, secure, expires)
}

func newSession(value string, secure bool, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     constants.SessionTokenJWTTemplateName,
		Value:    value,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	}
}

func NewHandshake(ctx context.Context, value string) *http.Cookie {
	env := environment.FromContext(ctx)
	// We're adding a max-age here as the handshake payload transports a session JWT, which is only valid for one minute.
	// By setting a max-age we ensure the handshake payload will not be parsed after the session JWT has expired.
	// We add 10 seconds of buffer to account for any latency in the handshake flow.
	maxAge := constants.ExpiryTimeSessionJWT + 10

	return &http.Cookie{
		Name:   constants.ClientHandshakeCookie,
		Value:  value,
		Path:   "/",
		Domain: env.Domain.Name,
		MaxAge: maxAge,
		Secure: true,
	}
}

func NewHandshakeClear(ctx context.Context) *http.Cookie {
	env := environment.FromContext(ctx)

	return &http.Cookie{
		Name:    constants.ClientHandshakeCookie,
		Path:    "/",
		Expires: time.Unix(0, 0),
		Domain:  env.Domain.Name,
	}
}
