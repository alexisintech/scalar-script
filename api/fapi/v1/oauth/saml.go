package oauth

import (
	"context"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/oauth"
	"clerk/pkg/usersettings/clerk/strategies"
	"clerk/utils/database"

	"github.com/volatiletech/null/v8"
)

// We handle the case when a user uses an OAuth provider to authenticate (sign-in, sign-up) and there is an active
// SAML Connection for this email address domain. Instead of returning an error, we are making sure to continue the
// flow by doing the following
// We create a new SAML verification and we attach it to the current sign-in/up in order to replace the OAuth one.
// Then we redirect to the corresponding IdP in order to complete the flow via the SAML strategy.
// By doing this, we are offering a better experience to the users, which are able to authenticate in the end
func (o *OAuth) handleSAML(ctx context.Context, env *model.Env, ost *model.OauthStateToken, oauthUser *oauth.User) (*string, apierror.Error) {
	if !env.AuthConfig.UserSettings.SAML.Enabled {
		return nil, nil
	}

	// We only care if the email is provided or not. In the case there is an email, but it's marked as unverified,
	// that's OK since the user will have to authenticate either way in the IdP.
	if !oauthUser.EmailAddressProvided() {
		return nil, nil
	}

	if ost.SourceType != constants.OSTSignIn && ost.SourceType != constants.OSTSignUp {
		return nil, nil
	}

	samlConnection, err := o.samlService.ActiveConnectionForEmail(ctx, o.deps.DB(), env.Instance.ID, oauthUser.EmailAddress)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if samlConnection == nil {
		// Continue with the regular OAuth flow.
		return nil, nil
	}

	if ost.SourceType == constants.OSTSignIn {
		return o.finishSAMLForSignIn(ctx, env, ost, samlConnection.ID, oauthUser.EmailAddress)
	}

	return o.finishSAMLForSignUp(ctx, env, ost, samlConnection.ID, oauthUser.EmailAddress)
}

func (o *OAuth) finishSAMLForSignUp(ctx context.Context, env *model.Env, ost *model.OauthStateToken, connectionID, emailAddress string) (*string, apierror.Error) {
	signUp, err := o.signUpRepo.QueryByIDAndInstance(ctx, o.deps.DB(), ost.SourceID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if signUp == nil {
		return nil, apierror.InvalidClientStateForAction("a get", "No sign_up.")
	}

	var verification *model.Verification
	txErr := o.deps.DB().PerformTx(ctx, func(tx database.Tx) (bool, error) {
		signUp.SamlConnectionID = null.StringFrom(connectionID)
		signUp.SamlIdentifier = null.StringFrom(emailAddress)
		if err := o.signUpRepo.UpdateSAMLConnectionAndSAMLIdentifier(ctx, tx, signUp); err != nil {
			return true, err
		}

		prepareParams := strategies.SignUpPrepareForm{
			RedirectURL:               &ost.RedirectURL,
			ActionCompleteRedirectURL: ost.ActionCompleteRedirectURL.Ptr(),
			Origin:                    ost.Origin,
		}
		preparer, apiErr := strategies.SAML{}.CreateSignUpPreparer(ctx, tx, o.deps, env, signUp, prepareParams)
		if apiErr != nil {
			return true, apiErr
		}

		verification, err = preparer.Prepare(ctx, tx)
		if err != nil {
			return true, err
		}

		signUp.ExternalAccountVerificationID = null.StringFrom(verification.ID)
		if err := o.signUpRepo.UpdateExternalAccountVerificationID(ctx, tx, signUp); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return verification.ExternalAuthorizationURL.Ptr(), nil
}

func (o *OAuth) finishSAMLForSignIn(ctx context.Context, env *model.Env, ost *model.OauthStateToken, connectionID, emailAddress string) (*string, apierror.Error) {
	signIn, err := o.signInRepo.QueryByIDAndInstance(ctx, o.deps.DB(), ost.SourceID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if signIn == nil {
		return nil, apierror.InvalidClientStateForAction("a get", "No sign_in.")
	}

	var verification *model.Verification
	txErr := o.deps.DB().PerformTx(ctx, func(tx database.Tx) (bool, error) {
		signIn.SamlConnectionID = null.StringFrom(connectionID)
		signIn.SamlIdentifier = null.StringFrom(emailAddress)
		if err := o.signInRepo.UpdateSAMLConnectionAndSAMLIdentifier(ctx, tx, signIn); err != nil {
			return true, err
		}

		prepareParams := strategies.SignInPrepareForm{
			RedirectURL:               &ost.RedirectURL,
			ActionCompleteRedirectURL: ost.ActionCompleteRedirectURL.Ptr(),
			Origin:                    ost.Origin,
		}
		preparer, apiErr := strategies.SAML{}.CreateSignInPreparer(ctx, tx, o.deps, env, signIn, prepareParams)
		if apiErr != nil {
			return true, apiErr
		}

		verification, err = preparer.Prepare(ctx, tx)
		if err != nil {
			return true, err
		}

		if err := o.signInService.AttachFirstFactorVerification(ctx, tx, signIn, verification.ID, false); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return verification.ExternalAuthorizationURL.Ptr(), nil
}
