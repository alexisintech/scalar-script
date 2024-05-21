package router

import (
	"net/http"

	"clerk/api/apierror"
	shorigin "clerk/api/shared/origin"
	"clerk/api/shared/user_profile"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/clerkvalidator"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/ctx/validator"
	"clerk/pkg/emailaddress"
	clerkjson "clerk/pkg/json"
	sdkutils "clerk/pkg/sdk"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/log"

	"github.com/go-chi/cors"
	"github.com/jonboulle/clockwork"
)

func corsHandler() func(http.Handler) http.Handler {
	c := cors.New(cors.Options{
		AllowOriginFunc: func(r *http.Request, origin string) bool {
			return shorigin.ValidateDashboardOrigin(origin)
		},
		AllowCredentials: false,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Accept", "Content-Type"},
		MaxAge:           300,
	})
	return c.Handler
}

func injectJSONValidator(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	v := clerkvalidator.New()
	v.RegisterCustomTypeFunc(clerkjson.StringValuer, clerkjson.String{})

	ctx := validator.NewContext(r.Context(), v)
	return r.WithContext(ctx), nil
}

func restrictUpdatesOnImpersonationSessions(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	if !clerkhttp.IsMutationMethod(r.Method) {
		// non-mutation methods are allowed
		return r, nil
	}
	if sdkutils.ActorHasLimitedAccess(r.Context()) {
		return r, apierror.DashboardMutationsDuringImpersonationForbidden()
	}
	return r, nil
}

func addLoggedInUserToLog(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	claims, hasClaims := sdkutils.GetActiveSession(ctx)

	if hasClaims {
		log.AddToLogLine(ctx, log.UserID, claims.Subject)
	}
	return r, nil
}

func checkRequestAllowedDuringMaintenance(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	if maintenance.FromContext(r.Context()) && clerkhttp.IsMutationMethod(r.Method) {
		return r, apierror.SystemUnderMaintenance()
	}
	return r, nil
}

var validStaffModeDomains = set.New[string](
	"clerk.dev",
	"clerk.com",
)

func ensureStaffMode(clock clockwork.Clock, db database.Database) func(http.ResponseWriter, *http.Request) (*http.Request, apierror.Error) {
	userRepo := repository.NewUsers()
	userProfile := user_profile.NewService(clock)
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		ctx := r.Context()
		claims, hasClaims := sdkutils.GetActiveSession(r.Context())
		if !hasClaims {
			return r, apierror.ResourceForbidden()
		}

		user, err := userRepo.QueryByID(ctx, db, claims.Subject)
		if err != nil {
			return r, apierror.Unexpected(err)
		} else if user == nil {
			return r, apierror.ResourceForbidden()
		}

		emailAddress, err := userProfile.GetPrimaryEmailAddress(ctx, db, user)
		if err != nil {
			return r, apierror.Unexpected(err)
		} else if emailAddress == nil {
			return r, apierror.ResourceForbidden()
		}

		primaryEmailAddressDomain := emailaddress.Domain(*emailAddress)
		if !validStaffModeDomains.Contains(primaryEmailAddressDomain) {
			return r, apierror.ResourceForbidden()
		}
		return r, nil
	}
}
