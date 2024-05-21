package strategies

import (
	"context"
	"errors"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/external_account"
	"clerk/api/shared/saml"
	"clerk/api/shared/sso"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/jwks"
	"clerk/pkg/jwt"
	"clerk/pkg/oauth/provider"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

var (
	errSignInUserNotExists          = errors.New("sign in: user not exists")
	errSignUpUserAlreadyExists      = errors.New("sign up: user already exists")
	errGoogleOneTapTokenAlreadyUsed = errors.New("google one tap token already used")
	errActiveSAMLConnectionExists   = errors.New("an active saml connection exists for email address domain")
)

type GoogleOneTapAttemptor struct {
	clock  clockwork.Clock
	env    *model.Env
	token  string
	signIn *model.SignIn
	signUp *model.SignUp

	externalAccountService *external_account.Service

	identificationRepo *repository.Identification
	signInRepo         *repository.SignIn
	signUpRepo         *repository.SignUp
	verificationRepo   *repository.Verification
}

func NewGoogleOneTapAttemptor(deps clerk.Deps, env *model.Env, token string, signIn *model.SignIn, signUp *model.SignUp) GoogleOneTapAttemptor {
	return GoogleOneTapAttemptor{
		clock:                  deps.Clock(),
		env:                    env,
		token:                  token,
		signIn:                 signIn,
		signUp:                 signUp,
		externalAccountService: external_account.NewService(deps),
		identificationRepo:     repository.NewIdentification(),
		signInRepo:             repository.NewSignIn(),
		signUpRepo:             repository.NewSignUp(),
		verificationRepo:       repository.NewVerification(),
	}
}

func (v GoogleOneTapAttemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	userSettings := usersettings.NewUserSettings(v.env.AuthConfig.UserSettings)

	oauthConfig, err := sso.ActiveOauthConfigForProvider(ctx, tx, v.env.AuthConfig.ID, provider.GoogleID())
	if err != nil {
		return nil, fmt.Errorf("google_one_tap/attempt: find active oauth config: %w", err)
	}

	ost := &model.OauthStateToken{
		OauthConfigID:  oauthConfig.ID,
		ScopesReturned: oauthConfig.DefaultScopes,
	}

	tokenClaims, err := parseGoogleOneTapToken(ctx, v.clock, v.token, oauthConfig)
	if err != nil {
		return nil, fmt.Errorf("google_one_tap/attempt: parse token: %w", err)
	}

	oauthUser := tokenClaims.ToOAuthUser()

	samlConnectionExists, err := v.activeSAMLConnectionExistsForEmail(ctx, tx, v.env, oauthUser.EmailAddress)
	if err != nil {
		return nil, fmt.Errorf("google_one_tap/attempt: check if active saml connection exists for email: %w", err)
	}
	if samlConnectionExists {
		return nil, fmt.Errorf("google_one_tap/attempt: active saml connection exists for email: %w", errActiveSAMLConnectionExists)
	}

	externalAccountIdentification, err := v.identificationRepo.QueryLatestClaimedByInstanceAndTypeAndProviderUserID(ctx, tx, v.env.Instance.ID, oauthUser.ProviderUserID, oauthUser.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("google_one_tap/attempt: query external account identification: %w", err)
	}

	emailIdentification, err := v.identificationRepo.QueryClaimedVerifiedOrReservedByInstanceAndIdentifierAndTypePrioritizingVerified(ctx, tx, v.env.Instance.ID, oauthUser.EmailAddress, constants.ITEmailAddress)
	if err != nil {
		return nil, fmt.Errorf("google_one_tap/attempt: query email identification: %w", err)
	}

	userExists := externalAccountIdentification != nil || emailIdentification != nil

	if v.signIn != nil && !userExists {
		return nil, errSignInUserNotExists
	} else if v.signUp != nil && userExists {
		return nil, errSignUpUserAlreadyExists
	}

	verification := &model.Verification{Verification: &sqbmodel.Verification{
		InstanceID: v.env.Instance.ID,
		Strategy:   constants.VSGoogleOneTap,
		Attempts:   1,
		Token:      null.StringFrom(v.token),
		Nonce:      null.StringFrom(tokenClaims.ID),
	}}
	if err := v.verificationRepo.Insert(ctx, tx, verification); err != nil {
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueVerificationNonce) {
			return nil, errGoogleOneTapTokenAlreadyUsed
		}
		return nil, fmt.Errorf("google_one_tap/attempt: insert verification: %w", err)
	}

	if v.signIn != nil {
		if externalAccountIdentification == nil {
			// Complete the flow with account linking
			creationResult, err := v.externalAccountService.CreateAndLink(ctx, tx, verification, ost, oauthUser, v.env.Instance, emailIdentification.UserID.Ptr(), userSettings)
			if err != nil {
				return nil, fmt.Errorf("google_one_tap/attempt: create and link external account with email identification: %w", err)
			}

			externalAccountIdentification = creationResult.Identification
		} else {
			if _, err := v.externalAccountService.Update(ctx, tx, userSettings, ost, oauthUser, v.env.Instance); err != nil {
				return nil, fmt.Errorf("google_one_tap/attempt: update external account and user: %w", err)
			}
		}

		v.signIn.IdentificationID = null.StringFrom(externalAccountIdentification.ID)
		if err := v.signInRepo.UpdateIdentificationID(ctx, tx, v.signIn); err != nil {
			return nil, fmt.Errorf("google_one_tap/attempt: update sign in with identification: %w", err)
		}
	} else if v.signUp != nil {
		result, err := v.externalAccountService.CreateAndLink(ctx, tx, verification, ost, oauthUser, v.env.Instance, nil, userSettings)
		if err != nil {
			return nil, fmt.Errorf("google_one_tap/attempt: create and link external account and identifications: %w", err)
		}

		v.signUp.SuccessfulExternalAccountIdentificationID = null.StringFrom(result.Identification.ID)
		if err := v.signUpRepo.UpdateSuccessfulExternalAccountIdentificationID(ctx, tx, v.signUp); err != nil {
			return nil, fmt.Errorf("google_one_tap/attempt: update sign up with successful external account identification: %w", err)
		}
	}

	return verification, nil
}

func (GoogleOneTapAttemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, errSignUpUserAlreadyExists) {
		return apierror.IdentificationExists(provider.GoogleID(), nil)
	} else if errors.Is(err, errSignInUserNotExists) {
		return apierror.ExternalAccountNotFound()
	} else if errors.Is(err, errGoogleOneTapTokenAlreadyUsed) {
		return apierror.InvalidAuthorization()
	} else if errors.Is(err, jwt.ErrInvalidTokenFormat) || errors.Is(err, jwt.ErrTokenExpired) || errors.Is(err, jwt.ErrInvalidSignature) || errors.Is(err, jwt.ErrClaimsValidationFailed) {
		return apierror.GoogleOneTapTokenInvalid()
	} else if errors.Is(err, errActiveSAMLConnectionExists) {
		return apierror.SAMLEmailAddressDomainReserved()
	}
	return apierror.Unexpected(err)
}

func (GoogleOneTapAttemptor) activeSAMLConnectionExistsForEmail(ctx context.Context, exec database.Executor, env *model.Env, emailAddress string) (bool, error) {
	if !env.AuthConfig.UserSettings.SAML.Enabled {
		return false, nil
	}

	samlConnection, err := saml.New().ActiveConnectionForEmail(ctx, exec, env.Instance.ID, emailAddress)
	if err != nil {
		return false, err
	}

	return samlConnection != nil, nil
}

// TODO: DRY with google oauth provider implementation after we start validating the token for Google OAuth
const (
	googleOneTapJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
	googleOneTapIssuer  = "https://accounts.google.com"
)

func parseGoogleOneTapToken(ctx context.Context, clock clockwork.Clock, token string, oauthConfig *model.OauthConfig) (*provider.GoogleOpenIDClaims, error) {
	jwks, err := jwks.Fetch(ctx, googleOneTapJWKSURL)
	if err != nil {
		return nil, err
	}

	tokenClaims := &provider.GoogleOpenIDClaims{}
	if err := jwt.VerifyOpenIDToken(token, jwks, tokenClaims, googleOneTapIssuer, oauthConfig.ClientID, clock.Now().UTC()); err != nil {
		return nil, err
	}

	return tokenClaims, nil
}
