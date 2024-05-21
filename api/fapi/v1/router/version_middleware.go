package router

import (
	"net/http"

	"clerk/api/apierror"
	event_service "clerk/api/shared/events"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/iossdk_version"
	"clerk/pkg/ctx/maintenance"
	event_types "clerk/pkg/events"
	"clerk/pkg/versions"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"
	"clerk/utils/param"

	"github.com/volatiletech/null/v8"
)

const (
	ClerkJSVersionHeader  = "x-clerk-js-version"
	ClerkIOSVersionHeader = "x-ios-sdk-version"
)

const (
	noVersion = "no_version"
)

// logClerkJSVersion checks whether the incoming request contains the ClerkJSVersion.
// If it does, it adds it to the canonical log line so that it can be logged along with the request.
// It also removes it from the request, since we're using strict parameters.
func logClerkJSVersion(db database.Database) func(http.ResponseWriter, *http.Request) (*http.Request, apierror.Error) {
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		ctx := r.Context()

		version := readVersion(r)
		r = r.WithContext(clerkjs_version.NewContext(ctx, version))
		log.AddToLogLine(ctx, log.ClerkJSVersion, version)

		env := environment.FromContext(ctx)
		instance := env.Instance
		if version == noVersion || maintenance.FromContext(ctx) {
			return r, nil
		}

		var shouldUpdate bool
		if !instance.MinClerkjsVersion.Valid || versions.IsBefore(version, instance.MinClerkjsVersion.String, true) {
			shouldUpdate = true
			instance.MinClerkjsVersion = null.StringFrom(version)
		}
		if !instance.MaxClerkjsVersion.Valid || versions.IsBefore(instance.MaxClerkjsVersion.String, version, true) {
			shouldUpdate = true
			instance.MaxClerkjsVersion = null.StringFrom(version)
		}
		if shouldUpdate {
			err := repository.NewInstances().UpdateClerkJSVersions(ctx, db, instance)
			if err != nil {
				return r, apierror.Unexpected(err)
			}
		}
		return r, nil
	}
}

func readVersion(r *http.Request) string {
	// check if version is included as a header
	version := r.Header.Get(ClerkJSVersionHeader)
	if versions.IsValid(version) {
		return version
	}

	version = r.URL.Query().Get(param.ClerkJSVersion)
	if version != "" {
		// we need to remove it from the form since we're using strict parameters in our endpoints
		delete(r.Form, param.ClerkJSVersion)

		if versions.IsValid(version) {
			return version
		}
	}

	return noVersion
}

func readIOSSDKVersion(r *http.Request) string {
	// check if version is included as a header
	version := r.Header.Get(ClerkIOSVersionHeader)
	if versions.IsValid(version) {
		return version
	}
	return noVersion
}

// logClerkJSVersion checks whether the incoming request contains the ClerkIOSSDKVersion header.
// If it does, it adds it to the canonical log line so that it can be logged along with the request.
// It also sends a telemetry event to be for downstream analysis.
// Middleware must be run _after_ SetEnvFromRequest
// Test coverage is in /tests/fapi/iossdk_version_test.go

func logClerkIOSSDKVersion(deps clerk.Deps) func(http.ResponseWriter, *http.Request) (*http.Request, apierror.Error) {
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		ctx := r.Context()
		version := readIOSSDKVersion(r)
		if version == noVersion || maintenance.FromContext(ctx) {
			return r, nil
		}
		r = r.WithContext(iossdk_version.NewContext(ctx, version))
		log.AddToLogLine(ctx, log.ClerkIOSSDKVersion, version)

		env := environment.FromContext(ctx)
		instance := env.Instance
		payload := event_types.IOSActivityRegisteredPayload{
			IOSSDKVersion: version,
		}
		eventService := event_service.NewService(deps)
		err := eventService.IOSActivityRegistered(ctx, deps.DB(), instance, payload)
		if err != nil {
			log.Error(ctx, err.Error())
			return r, nil
		}
		return r, nil
	}
}
