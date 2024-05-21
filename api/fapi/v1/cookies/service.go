package cookies

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"clerk/api/shared/client_data"
	"clerk/pkg/cookies"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requesting_session"
	"clerk/pkg/jwt"
	"clerk/pkg/psl"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"

	"clerk/model"
)

type Service struct {
	db    database.Database
	clock clockwork.Clock

	clientDataService *client_data.Service
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                deps.DB(),
		clock:             deps.Clock(),
		clientDataService: client_data.NewService(deps),
	}
}

// Returns the two client related cookies. The auth cookie and the
// client_uat cookie for the given client.
func (s *Service) CreateCookiesForClient(ctx context.Context, cookieOven *cookies.Oven, client *model.Client) (cookies.Jar, error) {
	jar := []*http.Cookie{}
	env := environment.FromContext(ctx)

	// Add the client cookie
	clientCookie := ForClient(client, clientCookieDomain(env.Domain))
	clientCookie.Path = clientCookiePath(env.Domain)
	jar = append(jar, clientCookie)

	// Add the client_uat cookie
	clientUats, err := s.CreateClientUatCookies(ctx, cookieOven, client, env.Domain.ClientUatDomain())
	if err != nil {
		return nil, err
	}

	jar = append(jar, clientUats...)

	return jar, nil
}

func clientCookiePath(dmn *model.Domain) string {
	defaultPath := "/"
	if dmn.ProxyURL.Valid {
		u, err := url.Parse(dmn.ProxyURL.String)
		if err != nil {
			return defaultPath
		}
		return u.Path
	}
	return defaultPath
}

func clientCookieDomain(dmn *model.Domain) string {
	if dmn.ProxyURL.Valid {
		// Leave the domain blank so that the cookie is not accessible
		// from subdomains.
		// In most browsers there is a difference between a cookie set from
		// foo.com without a domain, and a cookie set with the foo.com domain.
		// In the former case, the cookie will only be sent for requests to
		// foo.com, also known as a host-only cookie. In the latter case, all
		// subdomains are also included (for example, docs.foo.com).
		return ""
	}
	return dmn.AuthHost()
}

// CreateClientUatCookies returns a *http.Cookie that can be used to retrieve
// the short-lived session cookie. We call this cookie client_uat.
// A cookie will still be returned for nil clients, or clients without
// an active session and its value will represent a signed out state.
func (s *Service) CreateClientUatCookies(ctx context.Context, cookieOven *cookies.Oven, client *model.Client, domain string) (cookies.Jar, error) {
	env := environment.FromContext(ctx)

	if client == nil {
		cookie := NewClientUatSignedOut(domain, determineSameSite(env.Instance.ID))
		return cookieOven.ClientUat(cookie), nil
	}

	ok, err := s.hasActiveSession(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("cannot determine active session for client %s: %w", client.ID, err)
	}
	if !ok {
		cookie := NewClientUatSignedOut(domain, determineSameSite(env.Instance.ID))
		return cookieOven.ClientUat(cookie), nil
	}

	clientUpdatedAt := strconv.FormatInt(client.UpdatedAt.Unix(), 10)
	cookie := NewClientUatSignedIn(clientUpdatedAt, domain, determineSameSite(env.Instance.ID))

	return cookieOven.ClientUat(cookie), nil
}

// Check that the client has an active session.
func (s *Service) hasActiveSession(ctx context.Context, client *model.Client) (bool, error) {
	// Important Note: the following logic depends on mutations of the model.Session as stored in the context
	// since it will not reload the session from the database.
	if session := requesting_session.FromContext(ctx); session != nil && session.IsActive(s.clock) {
		return true, nil
	}
	session, err := s.clientDataService.QueryLatestTouchedActiveSessionByClient(ctx, client.InstanceID, client.ID)
	if err != nil {
		return false, err
	}
	return session != nil, nil
}

// ClientUatDomain determines the proper domain to set the client_uat cookie on. Sets it to localhost if the targetURL is localhost, otherwise sets it to eTLD+1 of the instance domain.
func (s *Service) ClientUatDomain(env *model.Env, targetURL *url.URL) (string, error) {
	clientUatDomain := env.Domain.ClientUatDomain()
	if env.Instance.IsDevelopment() {
		hostname := targetURL.Hostname()
		hostnameParts := strings.Split(hostname, ".")

		if hostname == "" {
			return "", fmt.Errorf("cannot determine client_uat domain from empty hostname: %s", hostname)
		}

		if netIP := net.ParseIP(hostname); netIP != nil {
			// psl.Domain() fails to parse redirect URLs with IPs
			clientUatDomain = hostname
		} else if len(hostnameParts) == 1 {
			// psl.Domain() assumes localhost, or any other single-part hostnames, are a suffix, so errors
			clientUatDomain = hostname
		} else {
			redirectURLETLDPlusOne, err := psl.Domain(hostname)
			if err != nil {
				return "", err
			}
			clientUatDomain = redirectURLETLDPlusOne
		}
	}

	return clientUatDomain, nil
}

type handshakeClaims struct {
	Handshake []string `json:"handshake"`
}

// Create an encoded client handshake cookie, containing an encoded new session token
func (s *Service) CreateEncodedHandshakeCookie(ctx context.Context, extraCookies []*http.Cookie) (*http.Cookie, error) {
	env := environment.FromContext(ctx)

	jar := []*http.Cookie{}

	// Ensure the __clerk_handshake cookie gets removed once the host application passes along the cookies
	if env.Instance.IsProduction() {
		jar = append(jar, NewHandshakeClear(ctx))
	}

	if extraCookies != nil {
		jar = append(jar, extraCookies...)
	}

	setCookieDirectives := []string{}

	for _, cookie := range jar {
		// Safari does not respect set-cookie headers with Secure on localhost, and development is generally done over HTTP, so we ensure we aren't trying to set any secure cookies
		// related ref: https://bugs.webkit.org/show_bug.cgi?id=232088
		if env.Instance.IsDevelopment() {
			cookie.Secure = false
		}
		setCookieDirectives = append(setCookieDirectives, cookie.String())
	}

	tokenPayload := &handshakeClaims{
		Handshake: setCookieDirectives,
	}

	jwtCat := jwt.ClerkSessionTokenCategory
	if env.AuthConfig.SessionSettings.UseIgnoreJWTCat {
		jwtCat = jwt.ClerkIgnoreTokenCategory
	}

	token, err := jwt.GenerateToken(
		env.Instance.PrivateKey,
		tokenPayload,
		env.Instance.KeyAlgorithm,
		jwt.WithKID(env.Instance.ID),
		jwt.WithCategory(jwtCat),
	)
	if err != nil {
		return nil, err
	}

	return NewHandshake(ctx, token), nil
}

// Returns a cookie with default values for most attributes.
func newDefault(name, value, domain string, sameSiteMode http.SameSite) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Domain:   domain,
		HttpOnly: true,
		MaxAge:   tenYears,
		Path:     "/",
		SameSite: sameSiteMode,
		Secure:   domain != localhost,
	}
}

// In development, sometimes the host might be in some kind of
// host:port form. We assume the domain is localhost for these
// cases.
func sanitizeDomain(host string) string {
	if strings.Split(host, ":")[0] == localhost {
		return localhost
	}
	return host
}
