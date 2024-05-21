package strategies

import (
	"context"
	"encoding/json"
	"fmt"

	"clerk/api/shared/saml"
	"clerk/model"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/ctx/client_type"
	pkgsaml "clerk/pkg/saml"
	clerkstrings "clerk/pkg/strings"
	"clerk/utils/database"

	samlsp "github.com/crewjam/saml"
	"github.com/jonboulle/clockwork"
)

// SAMLPreparer is responsible for kickstarting a SAML authentication flow, by
// constructing the necessary IdP SSO URL that the user should be redirected to,
// in order to authenticate.
type SAMLPreparer struct {
	clock clockwork.Clock

	env    *model.Env
	params *SAMLPrepareParams
	saml   *saml.SAML
}

type SAMLPrepareParams struct {
	ConnectionID string
	SourceType   string
	SourceID     string
	RedirectURL  string
	Origin       string

	Identifier                *string
	ActionCompleteRedirectURL *string
}

func NewSAMLPreparer(clock clockwork.Clock, env *model.Env, params *SAMLPrepareParams) SAMLPreparer {
	return SAMLPreparer{
		clock:  clock,
		env:    env,
		params: params,
		saml:   saml.New(),
	}
}

func (p SAMLPreparer) Identification() *model.Identification {
	return nil
}

func (p SAMLPreparer) Prepare(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	if p.params.ConnectionID == "" {
		return nil, clerkerrors.WithStacktrace("saml/prepare: no connection_id")
	}

	sp, connection, err := p.saml.ServiceProviderForActiveConnection(ctx, tx, p.env, p.params.ConnectionID)
	if err != nil {
		return nil, clerkerrors.WithStacktrace("saml/prepare: %w", err)
	}

	authnReq, err := sp.MakeAuthenticationRequest(
		sp.GetSSOBindingLocation(samlsp.HTTPRedirectBinding),
		samlsp.HTTPRedirectBinding,
		samlsp.HTTPPostBinding)
	if err != nil {
		return nil, clerkerrors.WithStacktrace("saml/prepare: auth request: %w", err)
	}

	// To protect against forged requests, we use verification.nonce that will
	// be emitted back to us in the SAML response, as the RelayState parameter.
	// During the SAML response, we ensure that the nonce exists in
	// our database and if so, we "consume it" (i.e. null it out from the
	// verification) so that the same request cannot be replayed.
	//
	// the RelayState parameter is limited by the spec to 80 chars.
	// So we use a nonce as its value, thereby keeping it strictly under the
	// limit, while also allowing us to store any extra information that we want
	// to transmit, in the verifications.token column.
	//
	// The spec suggests to ensure the integrity and authenticity of the
	// relayState parameter (e.g. by signing it), however we avoid signing it by
	// relying on the fact that it's a securely-generated random nonce that's
	// discarded upon first use (we null it out in the verification table).
	idpSSOURL, err := p.generateRedirectURL(authnReq, sp, connection)
	if err != nil {
		return nil, clerkerrors.WithStacktrace("saml/prepare: generate IdP redirect URL: %w", err)
	}

	relayState, err := p.relayStateToken(ctx)
	if err != nil {
		return nil, err
	}

	verification, err := createVerification(ctx, tx, p.clock, &createVerificationParams{
		instanceID:               p.env.Instance.ID,
		strategy:                 constants.VSSAML,
		nonce:                    &authnReq.ID,
		token:                    &relayState,
		externalAuthorizationURL: clerkstrings.ToPtr(idpSSOURL),
	})
	if err != nil {
		return nil, clerkerrors.WithStacktrace("saml/prepare: create verification: %w", err)
	}

	return verification, nil
}

func (p SAMLPreparer) generateRedirectURL(authnReq *samlsp.AuthnRequest, sp *samlsp.ServiceProvider, connection *model.SAMLConnection) (string, error) {
	redirectURL, err := authnReq.Redirect(authnReq.ID, sp)
	if err != nil {
		return "", err
	}

	samlProvider, err := pkgsaml.GetProvider(connection.Provider)
	if err != nil {
		return "", err
	}

	// Apply any query parameter login hints for the specific provider
	query := redirectURL.Query()
	for key, value := range samlProvider.LoginHintParameter(connection.Domain, p.params.Identifier) {
		query.Add(key, value)
	}
	redirectURL.RawQuery = query.Encode()

	return redirectURL.String(), nil
}

// relayStateToken constructs a model.SAMLRelayStateToken and returns it in
// JSON-marshaled format.
func (p SAMLPreparer) relayStateToken(ctx context.Context) (string, error) {
	// convert relative redirect URLs to absolute ones
	redirectURL, err := resolveRelativeURL(p.params.Origin, p.params.RedirectURL)
	if err != nil {
		return "", fmt.Errorf("saml/prepare: resolve redirect_url (%s, %s): %w", p.params.Origin, p.params.RedirectURL, err)
	}

	actionCompleteRedirectURL := p.params.ActionCompleteRedirectURL
	if p.params.ActionCompleteRedirectURL != nil {
		newActionCompleteRedirectURL, err := resolveRelativeURL(p.params.Origin, *p.params.ActionCompleteRedirectURL)
		if err != nil {
			return "", fmt.Errorf("saml/prepare: resolve action_complete_redirect_url (%s, %s): %w", p.params.Origin, *p.params.ActionCompleteRedirectURL, err)
		}
		actionCompleteRedirectURL = &newActionCompleteRedirectURL
	}

	clerkJSVersion := clerkjs_version.FromContext(ctx)
	clientType := client_type.FromContext(ctx)

	token, err := json.Marshal(model.SAMLRelayStateToken{
		SourceType:                p.params.SourceType,
		SourceID:                  p.params.SourceID,
		RedirectURL:               redirectURL,
		ActionCompleteRedirectURL: actionCompleteRedirectURL,
		ClerkJSVersion:            clerkJSVersion,
		ClientType:                clientType,
	})
	if err != nil {
		return "", fmt.Errorf("saml/prepare: marshal RelayStateToken (%s, %s): %w",
			p.params.Origin, *p.params.ActionCompleteRedirectURL, err)
	}

	return string(token), nil
}
