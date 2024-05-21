package verification

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/clients"
	"clerk/api/fapi/v1/cookies"
	"clerk/api/shared/strategies"
	"clerk/model"
	"clerk/pkg/cache"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/jwt"
	sentryclerk "clerk/pkg/sentry"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"
	"clerk/utils/param"

	"github.com/jonboulle/clockwork"
)

type HTTP struct {
	cache cache.Cache
	clock clockwork.Clock
	db    database.Database

	// services
	service       *Service
	clientService *clients.Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		cache:         deps.Cache(),
		clock:         deps.Clock(),
		db:            deps.DB(),
		clientService: clients.NewService(deps),
		service:       NewService(deps),
	}
}

const (
	VerifyTokenStatusVerified       = "verified"
	VerifyTokenStatusExpired        = "expired"
	VerifyTokenStatusFailed         = "failed"
	VerifyTokenStatusClientMismatch = "client_mismatch"
)

// GET /v1/verify
func (h *HTTP) VerifyToken(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	token := r.URL.Query().Get("token")

	var newSession *model.Session
	var newClient *model.Client
	var apiErr apierror.Error

	claims, err := strategies.ParseVerificationLinkToken(token, env.Instance.PublicKey, env.Instance.KeyAlgorithm, h.clock)
	if errors.Is(err, jwt.ErrTokenExpired) {
		apiErr = apierror.VerificationLinkTokenExpired()
		h.logIfError(ctx, apiErr)
	} else if err != nil {
		return nil, apierror.VerificationInvalidLinkToken()
	} else {
		userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
		newSession, newClient, apiErr = h.service.VerifyTokenClaims(ctx, claims, userSettings)
		h.logIfError(ctx, apiErr)

		if newClient != nil {
			_ = cookies.SetClientCookie(ctx, h.db, h.cache, w, newClient, env.Domain.AuthHost())
		}
	}

	redirectURL, err := buildVerifyTokenRedirectURL(claims.RedirectURL, newSession, apiErr)
	if err != nil {
		return nil, apierror.VerificationInvalidLinkToken()
	}

	finalRedirectURL := redirectURL.String()
	if newSession != nil {
		clientType := client_type.FromContext(ctx)
		clerkJSVersion := clerkjs_version.FromContext(ctx)

		handshakeRedirectURL, handshakeError := h.clientService.SetHandshakeTokenInResponse(ctx, w, newClient, finalRedirectURL, clientType, clerkJSVersion)
		if handshakeError != nil {
			return nil, handshakeError
		}
		finalRedirectURL = handshakeRedirectURL
	}

	http.Redirect(w, r, finalRedirectURL, http.StatusSeeOther)
	return nil, nil
}

func (h *HTTP) logIfError(ctx context.Context, apiErr apierror.Error) {
	if apiErr == nil {
		return
	}

	if logLine, ok := log.GetLogLine(ctx); ok {
		logLine.RequestError = apiErr
		log.CanonicalLineEntry(ctx, logLine)
	}
	sentryclerk.CaptureException(ctx, apiErr)
}

func getVerifyTokenRedirectStatus(err apierror.Error) string {
	if err == nil {
		return VerifyTokenStatusVerified
	}
	if err.ErrorCode() == apierror.VerificationLinkTokenExpiredCode {
		return VerifyTokenStatusExpired
	}
	if err.ErrorCode() == apierror.SignInEmailLinkNotSameClientCode || err.ErrorCode() == apierror.SignUpEmailLinkNotSameClientCode {
		return VerifyTokenStatusClientMismatch
	}
	return VerifyTokenStatusFailed
}

func buildVerifyTokenRedirectURL(baseURL string, newSession *model.Session, err apierror.Error) (*url.URL, error) {
	u, parseErr := url.Parse(baseURL)
	if parseErr != nil {
		return u, parseErr
	}
	q := u.Query()
	q.Add(param.ClerkStatus, getVerifyTokenRedirectStatus(err))

	if newSession != nil {
		q.Add("__clerk_created_session", newSession.ID)
	}
	u.RawQuery = q.Encode()
	return u, nil
}
