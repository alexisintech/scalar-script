package cookies

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"clerk/pkg/cache"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/ctx/requestingdevbrowser"
	clerkmaintenance "clerk/pkg/maintenance"

	"github.com/volatiletech/null/v8"

	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/ctx/devbrowser"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/versions"
	"clerk/repository"
	"clerk/utils/database"
)

const (
	tenYears  = 60 * 60 * 24 * 365 * 10
	localhost = "localhost"
)

// ForClient returns the proper auth http.Cookie for the given
// client and domain.
// A blank cookie will be returned for a nil client, which can be
// used to reset the cookie value, without deleting it.
func ForClient(client *model.Client, domain string) *http.Cookie {
	domain = sanitizeDomain(domain)

	if client == nil {
		return newBlank(constants.ClientCookie, domain, determineSameSite(""))
	}
	return newDefault(constants.ClientCookie, client.CookieValue.String, domain, determineSameSite(client.InstanceID))
}

// SetClientCookie sets the main authentication cookie that contains the long-lived
// token. This cookie lives in FAPI (e.g. clerk.example.com)
//
// For more info see https://docs.google.com/document/d/1Kv4fQFfoXb7NzO3a287hYZboTl3fQjQ3LLegLTJEwNE/edit?usp=sharing
func SetClientCookie(ctx context.Context, db database.Database, cache cache.Cache, w http.ResponseWriter, client *model.Client, domain string) error {
	env := environment.FromContext(ctx)
	clientType := client_type.FromContext(ctx)

	cookie := ForClient(client, domain)

	if env.Instance.IsProduction() || clientType.IsNative() {
		http.SetCookie(w, cookie)
		return nil
	}

	if _, err := updateDevBrowser(ctx, db, cache, &client.ID); err != nil {
		return err
	}

	devBrowserRequestContext := devbrowser.FromContext(ctx)
	if devBrowserRequestContext.IsThirdParty() || env.Instance.UsesURLBasedSessionSyncingMode(env.AuthConfig) {
		setThirdPartyHeaders(ctx, w, cookie.Value)
	}

	if !env.Instance.UsesURLBasedSessionSyncingMode(env.AuthConfig) {
		resetURLBasedSessionSyncing(w, cookie.Name, cookie.Value, cookie.Domain)
	}

	return nil
}

// UnsetClientCookie unsets the client cookie
func UnsetClientCookie(ctx context.Context, db database.Database, cache cache.Cache, w http.ResponseWriter, domain string) error {
	env := environment.FromContext(ctx)
	clientType := client_type.FromContext(ctx)

	if env.Instance.IsProduction() || clientType.IsNative() {
		SetBlank(w, constants.ClientCookie, domain, env.Instance.ID)
		return nil
	}

	devBrowser, err := updateDevBrowser(ctx, db, cache, nil)
	if err != nil {
		return err
	}

	devBrowserRequestContext := devbrowser.FromContext(ctx)
	// In development, we always want to keep in-sync the DevBrowser between
	// localhost and Accounts. Therefore we don't actually delete the cookie
	// (despite the current function's name) but instead update it with the most
	// current DevBrowser.
	if devBrowserRequestContext.IsThirdParty() || env.Instance.UsesURLBasedSessionSyncingMode(env.AuthConfig) {
		// This token includes the env.DevBrowser but no client ID
		setThirdPartyHeaders(ctx, w, devBrowser.Token)
	}

	if !env.Instance.UsesURLBasedSessionSyncingMode(env.AuthConfig) {
		resetURLBasedSessionSyncing(w, constants.ClientCookie, devBrowser.Token, domain)
	}

	return nil
}

func UnsetClientHandshakeCookie(ctx context.Context, w http.ResponseWriter, domain string) {
	env := environment.FromContext(ctx)

	// In development, URL-based session syncing is used, and so there is no handshake cookie
	if !env.Instance.IsProduction() {
		return
	}

	handshakeCookie := newBlank(constants.ClientHandshakeCookie, sanitizeDomain(domain), determineSameSite(env.Instance.ID))
	handshakeCookie.HttpOnly = false

	http.SetCookie(w, handshakeCookie)
}

func updateDevBrowser(ctx context.Context, db database.Executor, cache cache.Cache, clientID *string) (*model.DevBrowser, error) {
	devBrowser := requestingdevbrowser.FromContext(ctx)
	if devBrowser == nil {
		return devBrowser, errors.New("expected a devbrowser but it was not found")
	}
	devBrowserRepo := repository.NewDevBrowser()
	devBrowser.ClientID = null.StringFromPtr(clientID)

	var err error
	if maintenance.FromContext(ctx) {
		err = cache.Set(ctx, clerkmaintenance.DevBrowserKey(devBrowser.ID, devBrowser.InstanceID), devBrowser, time.Hour)
	} else {
		err = devBrowserRepo.UpdateClientID(ctx, db, devBrowser)
	}
	return devBrowser, err
}

func setThirdPartyHeaders(ctx context.Context, w http.ResponseWriter, value string) {
	clerkJSVersion := clerkjs_version.FromContext(ctx)
	if versions.IsBefore(clerkJSVersion, "5.0.0", true) {
		w.Header().Set("Access-Control-Expose-Headers", constants.LegacyDevBrowserHeaders)
		w.Header().Set(constants.LegacyDevBrowserHeaders, value)
	} else {
		w.Header().Set("Access-Control-Expose-Headers", constants.DevBrowserHeader)
		w.Header().Set(constants.DevBrowserHeader, value)
	}
}

// unsetPrevious unsets a cookie that was previously set so it can be set again
// It's used in development where cookie headers are sent with every response
// from the middleware, but mutations that occur after the middleware require
// the value to be changed.
func unsetPrevious(w http.ResponseWriter, name string) {
	j := 0
	for _, cookie := range w.Header()["Set-Cookie"] {
		if !strings.HasPrefix(cookie, name+"=") {
			w.Header()["Set-Cookie"][j] = cookie
			j++
		}
	}
	w.Header()["Set-Cookie"] = w.Header()["Set-Cookie"][:j]
}

// Set sets the cookie
func Set(w http.ResponseWriter, name string, value string, domain string, sameSiteNone bool) {
	sameSiteMode := http.SameSiteLaxMode
	if sameSiteNone {
		sameSiteMode = http.SameSiteNoneMode
	}

	http.SetCookie(w, newDefault(name, value, sanitizeDomain(domain), sameSiteMode))
}

// SetBlank sets a "blank" cookie with the given name and domain
// attributes.
func SetBlank(w http.ResponseWriter, name string, domain string, instanceID string) {
	http.SetCookie(w, newBlank(name, sanitizeDomain(domain), determineSameSite(instanceID)))
}

// Returns a "blank" cookie with empty value and an invalid MaxAge.
func newBlank(name, domain string, sameSite http.SameSite) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Domain:   domain,
		HttpOnly: true,
		MaxAge:   -1,
		Path:     "/",
		SameSite: sameSite,
		Secure:   domain != localhost,
	}
}

// even in third-party devbrowser contexts (e.g. localhost<->FAPI), we
// set the cookie so that Accounts stays in sync when it queries FAPI.
func resetURLBasedSessionSyncing(w http.ResponseWriter, name, value, domain string) {
	unsetPrevious(w, name)
	Set(w, name, value, domain, true)
}

func determineSameSite(instanceID string) http.SameSite {
	// Two customers, sportbookr.com and saasmonk.io built an iframe-based widget that gets embedded
	// in their customer's websites. That iframe can't use FAPI unless the __client cookie is set with
	// SameSite=None.
	//
	// TODO: Determine how to make this option available to everyone, or discuss SameSite=None by default
	// since it may not confer any security benefit given our other protections on FAPI.
	//
	// More info at:
	// - https://clerkinc.slack.com/archives/C031BD2QJDD/p1652475753158809
	// - https://discord.com/channels/856971667393609759/1106124471334617178
	if cenv.ResourceHasAccess(cenv.ClerkExperimentalSameSiteNoneInstanceIds, instanceID) {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}
