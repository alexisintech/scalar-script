package cookies

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/wrapper"
	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	"clerk/api/shared/clients"
	"clerk/api/shared/sign_in"
	"clerk/api/shared/sign_up"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctxkeys"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/form"
	"clerk/utils/param"

	"github.com/jonboulle/clockwork"
)

type CookieSetter struct {
	deps    clerk.Deps
	db      database.Database
	clock   clockwork.Clock
	wrapper *wrapper.Wrapper

	// services
	clientService     *clients.Service
	signInService     *sign_in.Service
	signUpService     *sign_up.Service
	clientDataService *client_data.Service

	// repositories
	signInRepo *repository.SignIn
	signUpRepo *repository.SignUp
}

func NewCookieSetter(deps clerk.Deps) *CookieSetter {
	return &CookieSetter{
		deps:              deps,
		db:                deps.DB(),
		clock:             deps.Clock(),
		wrapper:           wrapper.NewWrapper(deps),
		clientService:     clients.NewService(deps),
		signInService:     sign_in.NewService(deps),
		signUpService:     sign_up.NewService(deps),
		clientDataService: client_data.NewService(deps),
		signInRepo:        repository.NewSignIn(),
		signUpRepo:        repository.NewSignUp(),
	}
}

// Set sets or deletes the auth FAPI cookie, according to the following URL query params:
//   - _set_cookie_client_id
//   - _set_cookie_subdomain
//
// This middleware facilitates the OAuth flow in dev instances, which goes
// through the shared callback URL before landing in the instance's FAPI host
// (i.e. here). The aforementioned params are set during the initial OAuth
// requests.
func (s *CookieSetter) SetAuthCookieFromURLQuery(w http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)

	finalRedirectURL := form.GetNullURL(r.Form, "_final_redirect_url")
	var finalRedirectString *string
	if finalRedirectURL != nil && finalRedirectURL.Valid {
		finalRedirectString = &finalRedirectURL.String
	}
	setClientIDArr, setCookieParamFound := r.Form["_set_cookie_client_id"]
	cookieSubdomain, cookieSubdomainFound := r.Form["_set_cookie_subdomain"]
	if !setCookieParamFound {
		return r, nil
	}

	// Ignore CSRF protection for the oauth_callback route on dev/stg instances.
	// the oauth_callback route for these instances is set to "clerk.shared.lcl.dev/v1/oauth_callback"
	// when that request redirects to drop a cookie, it doesn't recieve the __csrf_token cookie
	// because it goes back to the "clerk.abc.123.lcl.dev/v1/oauth_callback" domain, which is different
	// from the shared domain.
	skipCSRFCheck := env.Instance.IsDevelopmentOrStaging() && r.URL.Path == "/v1/oauth_callback"
	csrfValid := ctx.Value(ctxkeys.CSRFPresentAndValid).(bool)
	if !csrfValid && !skipCSRFCheck {
		return r, apierror.InvalidCSRFToken()
	}

	domain := env.Domain.AuthHost()
	cookieDmn := strings.TrimPrefix(domain, "clerk.")
	if cookieSubdomainFound {
		cookieDmn = fmt.Sprintf("%v.%v", cookieSubdomain[0], cookieDmn)
	}

	if setCookieParamFound {
		firstClientID := setClientIDArr[0]

		cdsClient, err := s.clientDataService.FindClient(ctx, env.Instance.ID, firstClientID)
		if err != nil {
			if errors.Is(err, client_data.ErrNoRecords) {
				return r, apierror.ClientNotFound(firstClientID)
			}
			return r, apierror.Unexpected(err)
		}
		client := cdsClient.ToClientModel()

		_ = SetClientCookie(ctx, s.db, s.deps.Cache(), w, client, cookieDmn)

		retObjType := r.Form.Get(param.RetObjType)
		retObjID := r.Form.Get(param.RetObjID)

		apiErr := s.writeObjResponse(ctx, w, r, env.Instance, client, retObjType, &retObjID, finalRedirectString)
		if apiErr != nil {
			if !apierror.IsInternal(apiErr) {
				return r, s.wrapper.WrapError(ctx, apiErr, client)
			}
			return r, apiErr
		}
	}
	return nil, nil
}

func (s *CookieSetter) writeWrappedResponse(ctx context.Context, w http.ResponseWriter, resp interface{}, client *model.Client) apierror.Error {
	wrappedResponse, err := s.wrapper.WrapResponse(ctx, resp, client)
	if err != nil {
		return err
	}

	w.WriteHeader(http.StatusOK)
	js, goerr := json.Marshal(wrappedResponse)
	if goerr != nil {
		return apierror.Unexpected(goerr)
	}
	_, goerr = w.Write(js)
	if goerr != nil {
		return apierror.Unexpected(goerr)
	}

	return nil
}

// RetObjResponse responds by using a ret_obj, ret_obj_id and redirectURL
func (s *CookieSetter) writeObjResponse(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	ins *model.Instance,
	client *model.Client,
	retObjType string,
	retObjID *string,
	redirectURL *string,
) apierror.Error {
	switch retObjType {
	case apierror.StrategyForUserInvalidCode:
		return apierror.InvalidStrategyForUser()
	case apierror.FormPasswordIncorrectCode:
		return apierror.FormPasswordIncorrect(param.Password.Name)

	case apierror.FormIdentifierNotFoundCode:
		return apierror.FormIdentifierNotFound(param.Identifier.Name)

	case apierror.IdentifierAlreadySignedInCode:
		if retObjID == nil || *retObjID == "" {
			return apierror.Unexpected(fmt.Errorf("missing retObjID for Type %s", retObjType))
		}

		return apierror.AlreadySignedIn(*retObjID)

	case constants.ROClient:
		env := environment.FromContext(ctx)
		userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
		clientWithSessions, apiErr := s.clientService.ConvertToClientWithSessions(ctx, client, env)
		if apiErr != nil {
			return apiErr
		}
		clientResponse, err := serialize.ClientToClientAPI(ctx, s.clock, clientWithSessions, userSettings)
		if err != nil {
			return apierror.Unexpected(err)
		}

		return s.writeWrappedResponse(ctx, w, clientResponse, nil)

	case constants.ROSignIn:
		if retObjID == nil || *retObjID == "" {
			return apierror.InvalidAuthorization()
		}

		signIn, signInErr := s.signInRepo.QueryByIDAndInstance(ctx, s.db, *retObjID, ins.ID)
		if signInErr != nil {
			return apierror.Unexpected(signInErr)
		} else if signIn == nil {
			return apierror.InvalidClientStateForAction("a get", "No sign_in.")
		}

		env := environment.FromContext(ctx)
		userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

		signInSerializable, goerr := s.signInService.ConvertToSerializable(ctx, s.db, signIn, userSettings, r.FormValue("oauth_authorization_url"))
		if goerr != nil {
			return apierror.Unexpected(goerr)
		}

		signInResponse, goerr := serialize.SignIn(s.clock, signInSerializable, userSettings)
		if goerr != nil {
			return apierror.Unexpected(goerr)
		}

		return s.writeWrappedResponse(ctx, w, signInResponse, client)

	case constants.ROSignUp:
		if retObjID == nil || *retObjID == "" {
			return apierror.Unexpected(fmt.Errorf("missing retObjID for type %s", retObjType))
		}

		signUp, err := s.signUpRepo.QueryByIDAndInstance(ctx, s.db, *retObjID, ins.ID)
		if err != nil {
			return apierror.Unexpected(err)
		} else if signUp == nil {
			return apierror.Unexpected(errors.New("no sign-up attempts found for " + *retObjID))
		}

		env := environment.FromContext(ctx)
		userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
		signUpSerializable, err := s.signUpService.ConvertToSerializable(ctx, s.db, signUp, userSettings, r.FormValue("oauth_authorization_url"))
		if err != nil {
			return apierror.Unexpected(err)
		}

		signUpResponse, err := serialize.SignUp(ctx, s.clock, signUpSerializable)
		if err != nil {
			return apierror.Unexpected(err)
		}

		return s.writeWrappedResponse(ctx, w, signUpResponse, client)
	case constants.RORedirect: // oauth
		if redirectURL == nil {
			return apierror.Unexpected(fmt.Errorf("missing redirectURL for Type %s", retObjType))
		}

		env := environment.FromContext(ctx)

		if env.Instance.IsDevelopmentOrStaging() {
			// We want the interstitial to kick in after this flow.
			// To do so, the Referer header has to be missing from
			// the request to finalRedirectURL. However,
			// header-based redirection would cause browsers
			// preserve the Referer from the original request (e.g.
			// localhost:3000).
			//
			// Thus, we use this meta-based redirection that will
			// trick browsers to actually attempt and re-construct
			// the Referer. Upon reconstruction, the Referer will
			// either be unset (because we're going from
			// HTTPS->HTTP) or will be reconstructed and will be
			// different than the customer's origin (e.g. *.lcl.dev
			// vs foo.com).
			w.Header().Set("Content-Type", "text/html")

			_, _ = w.Write([]byte(`<html><head>
<meta http-equiv="Refresh" content="0; url='` + *redirectURL + `'" />
</head></html>`))
			return nil
		}

		http.Redirect(w, r, *redirectURL, http.StatusSeeOther)
		return nil
	case "":
		return apierror.MissingQueryParameter(param.RetObjType)
	default:
		return apierror.InvalidQueryParameterValue(param.RetObjType, retObjType)
	}
}

// RespondWithCookie will either add a new cookie (and redirect),
// or respond with the new cookie value in the authorization header.
func (s *CookieSetter) RespondWithCookie(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	client *model.Client,
	resp interface{},
	apiErr apierror.Error,
) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	clientType := client_type.FromContext(ctx)

	// set cookies unless there's an Authorization header
	// otherwise just respond with the client's cookieValue in the Authorization header
	_, authHeaderExists := r.Header["Authorization"]
	if authHeaderExists || clientType.IsNative() {
		w.Header().Set("Authorization", client.CookieValue.String)

		if apiErr != nil {
			return nil, s.wrapper.WrapError(ctx, apiErr, client)
		} else if resp != nil {
			return s.wrapper.WrapResponse(ctx, resp, client)
		}

		clientWithSessions, apiErr := s.clientService.ConvertToClientWithSessions(ctx, client, env)
		if apiErr != nil {
			return nil, apiErr
		}

		clientResponse, err := serialize.ClientToClientAPI(ctx, s.clock, clientWithSessions, userSettings)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		return s.wrapper.WrapResponse(ctx, clientResponse, nil)
	}

	// drop a cookie on clerk.example.com
	_ = SetClientCookie(ctx, s.db, s.deps.Cache(), w, client, env.Domain.AuthHost())

	if apiErr != nil {
		return nil, apiErr
	}
	if resp != nil {
		return s.wrapper.WrapResponse(ctx, resp, client)
	}

	clientWithSessions, apiErr := s.clientService.ConvertToClientWithSessions(ctx, client, env)
	if apiErr != nil {
		return nil, apiErr
	}

	clientResponse, err := serialize.ClientToClientAPI(ctx, s.clock, clientWithSessions, userSettings)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return s.wrapper.WrapResponse(ctx, clientResponse, nil)
}
