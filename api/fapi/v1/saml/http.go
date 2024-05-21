package saml

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/clients"
	"clerk/api/fapi/v1/cookies"
	"clerk/api/fapi/v1/samlaccount"
	"clerk/api/shared/client_data"
	"clerk/api/shared/identifications"
	"clerk/api/shared/restrictions"
	"clerk/api/shared/saml"
	"clerk/api/shared/sessions"
	"clerk/api/shared/sign_in"
	"clerk/api/shared/sign_up"
	userlockout "clerk/api/shared/user_lockout"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cache"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/emailaddress"
	"clerk/pkg/jwt"
	pkgsaml "clerk/pkg/saml"
	"clerk/pkg/ticket"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"
	"clerk/utils/validate"

	samlsp "github.com/crewjam/saml"
	"github.com/go-chi/chi/v5"
	josejwt "github.com/go-jose/go-jose/v3/jwt"
	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

var (
	errTransferToSignIn = errors.New("transfer sign up to sign in")
	errTransferToSignUp = errors.New("transfer sign in to sign up")
)

type HTTP struct {
	cache cache.Cache
	clock clockwork.Clock
	db    database.Database

	// services
	clientService       *clients.Service
	restrictionService  *restrictions.Service
	samlAccountService  *samlaccount.Service
	samlService         *saml.SAML
	signInService       *sign_in.Service
	signUpService       *sign_up.Service
	userLockoutService  *userlockout.Service
	verificationService *verifications.Service
	sessionService      *sessions.Service

	// repositories
	accountTransfersRepo *repository.AccountTransfers
	identificationRepo   *repository.Identification
	signInRepo           *repository.SignIn
	signUpRepo           *repository.SignUp
	userRepo             *repository.Users
	verificationRepo     *repository.Verification

	// Session Management
	clientDataService *client_data.Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		cache:                deps.Cache(),
		clock:                deps.Clock(),
		db:                   deps.DB(),
		clientService:        clients.NewService(deps),
		restrictionService:   restrictions.NewService(deps.EmailQualityChecker()),
		samlAccountService:   samlaccount.NewService(deps),
		samlService:          saml.New(),
		signInService:        sign_in.NewService(deps),
		signUpService:        sign_up.NewService(deps),
		userLockoutService:   userlockout.NewService(deps),
		verificationService:  verifications.NewService(deps.Clock()),
		sessionService:       sessions.NewService(deps),
		accountTransfersRepo: repository.NewAccountTransfers(),
		identificationRepo:   repository.NewIdentification(),
		signInRepo:           repository.NewSignIn(),
		signUpRepo:           repository.NewSignUp(),
		userRepo:             repository.NewUsers(),
		verificationRepo:     repository.NewVerification(),
		clientDataService:    client_data.NewService(deps),
	}
}

// GET /v1/saml/metadata/{samlConnectionID}.xml
func (s HTTP) Metadata(w http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)

	metadataBytes, err := s.samlService.MetadataForConnection(ctx, s.db, env, chi.URLParam(r, "samlConnectionID"))
	if errors.Is(err, saml.ErrConnectionNotFound) {
		return nil, apierror.ResourceNotFound()
	} else if err != nil {
		return nil, apierror.Unexpected(err)
	}

	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	_, _ = w.Write(metadataBytes)

	return nil, nil
}

// POST /v1/saml/acs/{samlConnectionID} (when protocol_binding=HTTP-Post)
func (s HTTP) AssertionConsumerService(w http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	connectionID := chi.URLParam(r, "samlConnectionID")
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	sp, samlConnection, err := s.samlService.ServiceProviderForActiveConnection(ctx, s.db, env, connectionID)
	if err != nil {
		if errors.Is(err, saml.ErrConnectionNotFound) {
			return nil, apierror.SAMLConnectionActiveNotFound(connectionID)
		}
		return nil, apierror.Unexpected(err)
	}

	relayState := r.Form.Get("RelayState")
	if relayState == "" {
		if !samlConnection.AllowIdpInitiated {
			return nil, apierror.SAMLResponseRelayStateMissing()
		}

		redirectURL, apiErr := s.finishFlowForIdpInitiated(ctx, r, env, sp, samlConnection)
		if apiErr != nil {
			return nil, apiErr
		}

		http.Redirect(w, r, redirectURL, http.StatusFound)
		return nil, nil
	}

	possibleRequestIDs, err := s.possibleRequestIDs(ctx, env.Instance)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// NOTE: this is security-critical, to mitigate replay of SAML responses. It
	// has to happen after we fetch the possibleRequestIDs though, since
	// otherwise the said verification wouldn't be included in that set.
	verification, relayStateToken, err := s.consumeVerification(ctx, env.Instance, relayState)
	if err != nil {
		return nil, apierror.SAMLResponseInvalid(err)
	}

	if status, err := s.verificationService.Status(ctx, s.db, verification); err != nil {
		return s.handleACSError(ctx, w, r, verification, relayStateToken, apierror.Unexpected(err))
	} else if status != constants.VERUnverified {
		// verification already in invalid state, redirect back to UI for processing
		http.Redirect(w, r, relayStateToken.RedirectURL, http.StatusSeeOther)
		return nil, nil
	}

	assertion, err := sp.ParseResponse(r, possibleRequestIDs)
	if err != nil {
		var e *samlsp.InvalidResponseError
		if errors.As(err, &e) {
			// invalid signature or other XML error.
			return s.handleACSError(ctx, w, r, verification, relayStateToken, apierror.SAMLResponseInvalid(e.PrivateErr))
		}
		return s.handleACSError(ctx, w, r, verification, relayStateToken, apierror.Unexpected(err))
	}

	samlUser, apiErr := userFromAssertionAttributes(assertion, samlConnection)
	if apiErr != nil {
		return s.handleACSError(ctx, w, r, verification, relayStateToken, apiErr)
	}

	// Check instance restrictions (Allowlist, Blocklist) based on user's data
	apiErr = s.enforceInstanceRestrictions(
		ctx,
		s.db,
		userSettings,
		samlUser.EmailAddress,
		env.Instance.ID,
		env.AuthConfig.TestMode,
		relayStateToken.SourceType != constants.OSTSignIn,
	)
	if apiErr != nil {
		return s.handleACSError(ctx, w, r, verification, relayStateToken, apiErr)
	}

	var clientID string
	var (
		signIn *model.SignIn
		signUp *model.SignUp
	)
	switch relayStateToken.SourceType {
	case constants.OSTSignIn:
		var err error
		signIn, err = s.signInRepo.QueryByIDAndInstance(ctx, s.db, relayStateToken.SourceID, env.Instance.ID)
		if err != nil {
			return s.handleACSError(ctx, w, r, verification, relayStateToken, err)
		}
		if signIn == nil {
			return s.handleACSError(ctx, w, r, verification, relayStateToken, apierror.InvalidClientStateForAction("a get", "No sign_in."))
		}
		clientID = signIn.ClientID
	case constants.OSTSignUp:
		var err error
		signUp, err = s.signUpRepo.QueryByIDAndInstance(ctx, s.db, relayStateToken.SourceID, env.Instance.ID)
		if err != nil {
			return s.handleACSError(ctx, w, r, verification, relayStateToken, err)
		}
		if signUp == nil {
			return s.handleACSError(ctx, w, r, verification, relayStateToken, apierror.InvalidClientStateForAction("a get", "No sign_up."))
		}
		clientID = signUp.ClientID
	default:
		return s.handleACSError(ctx, w, r, verification, relayStateToken, fmt.Errorf("SAMLRelayStateToken SourceType not implemented: %s", relayStateToken.SourceType))
	}
	client, err := s.getClient(ctx, env, clientID)
	if err != nil {
		return s.handleACSError(ctx, w, r, verification, relayStateToken, err)
	}

	var createdSession *model.Session
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		switch relayStateToken.SourceType {
		case constants.OSTSignIn:
			createdSession, err = s.finishFlowForSignIn(ctx, tx, env, userSettings, client, signIn, verification, samlConnection, samlUser)
			if err != nil {
				if errors.Is(err, errTransferToSignUp) {
					// In case of a transfer, we don't want to roll back the transaction, but instead we want
					// to propagate a specific error code 'external_account_exists' to the UI
					return false, apierror.ExternalAccountNotFound()
				}
				return true, err
			}

			return false, nil
		case constants.OSTSignUp:
			createdSession, err = s.finishFlowForSignUp(ctx, tx, env, client, signUp, verification, samlConnection, samlUser)
			if err != nil {
				if errors.Is(err, errTransferToSignIn) {
					// In case of a transfer, we don't want to roll back the transaction, but instead we want
					// to propagate a specific error code 'external_account_exists' to the UI
					return false, apierror.IdentificationExists(constants.ITSAML, nil)
				}
				return true, err
			}

			return false, nil
		default:
			return true, fmt.Errorf("SAMLRelayStateToken SourceType not implemented: %s", relayStateToken.SourceType)
		}
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueReservedIdentification) {
			return nil, apierror.IdentificationClaimed()
		}
		return s.handleACSError(ctx, w, r, verification, relayStateToken, txErr)
	}

	if createdSession == nil {
		// unable to complete sign in/up, redirect back to UI for processing
		http.Redirect(w, r, relayStateToken.RedirectURL, http.StatusSeeOther)
		return nil, nil
	}

	if err := s.sessionService.Activate(ctx, env.Instance, createdSession); err != nil {
		return s.handleACSError(ctx, w, r, verification, relayStateToken, err)
	}

	redirectURL := relayStateToken.RedirectURL
	if relayStateToken.ActionCompleteRedirectURL != nil {
		redirectURL = *relayStateToken.ActionCompleteRedirectURL
	}

	handshakeRedirectURL, handshakeError := s.clientService.SetHandshakeTokenInResponse(ctx, w, client, redirectURL, relayStateToken.ClientType, relayStateToken.ClerkJSVersion)
	if handshakeError != nil {
		return nil, handshakeError
	}

	_ = cookies.SetClientCookie(r.Context(), s.db, s.cache, w, client, env.Domain.AuthHost())
	// In contrast with OAuth flow, which are using a 307, here we need to use a 302 instead for the redirect
	// This is because the 307 propagates the original http method, which usually is POST for the ACS endpoint,
	// and should be a GET as it will be redirected in the customer's app.
	http.Redirect(w, r, handshakeRedirectURL, http.StatusFound)
	return nil, nil
}

func (s HTTP) consumeVerification(ctx context.Context, instance *model.Instance, relayState string) (*model.Verification, *model.SAMLRelayStateToken, error) {
	verification, err := s.verificationRepo.QueryPendingSAMLByNonceAndInstance(ctx, s.db, relayState, instance.ID)
	if err != nil {
		return nil, nil, err
	}
	if verification == nil {
		return nil, nil, fmt.Errorf("saml: verification by nonce %s not found", relayState)
	}

	if !verification.Token.Valid {
		return nil, nil, fmt.Errorf("saml: verification %s doesn't contain relay state token", verification.ID)
	}

	verification.Attempts++
	verification.Nonce = null.StringFromPtr(nil)

	err = s.verificationRepo.Update(ctx, s.db, verification, sqbmodel.VerificationColumns.Attempts, sqbmodel.VerificationColumns.Nonce)
	if err != nil {
		return nil, nil, err
	}

	relayStateToken := &model.SAMLRelayStateToken{}
	if err = json.Unmarshal([]byte(verification.Token.String), relayStateToken); err != nil {
		return nil, nil, err
	}

	return verification, relayStateToken, nil
}

func (s HTTP) possibleRequestIDs(ctx context.Context, instance *model.Instance) ([]string, error) {
	pendingVerifications, err := s.verificationRepo.FindAllPendingSAMLByInstanceAndMinimumExpireAt(ctx, s.db, instance.ID, s.clock.Now().UTC())
	if err != nil {
		return nil, err
	}

	ids := make([]string, len(pendingVerifications))
	for i, v := range pendingVerifications {
		ids[i] = v.Nonce.String
	}

	return ids, nil
}

func userFromAssertionAttributes(assertion *samlsp.Assertion, samlConnection *model.SAMLConnection) (*pkgsaml.User, apierror.Error) {
	samlUser := &pkgsaml.User{
		EmailAddress: emailFromNameID(assertion.Subject),
	}

	// No attributes statements, validate user and return early
	if len(assertion.AttributeStatements) == 0 {
		if apiErr := samlUser.Validate(samlConnection); apiErr != nil {
			return nil, apiErr
		}

		return samlUser, nil
	}

	publicMetadata := make(map[string]any)
	attributesPerName := make(map[string]string)
	for _, attribute := range assertion.AttributeStatements[0].Attributes {
		if len(attribute.Values) == 0 {
			continue
		}
		sanitizedValue := strings.TrimSpace(attribute.Values[0].Value)
		if sanitizedValue == "" {
			// Ignore empty string attribute values
			continue
		}

		attributesPerName[attribute.Name] = sanitizedValue

		if strings.HasPrefix(attribute.Name, constants.SAMLAttributePublicMetadataPrefix) {
			attr := strings.TrimSpace(strings.TrimPrefix(attribute.Name, constants.SAMLAttributePublicMetadataPrefix))
			if attr == "" {
				continue
			}
			if attr == "memberOf" {
				// Special handling of the 'memberOf' attribute value, until we integrate organizations with SAML.
				// If exist in the public metadata, then always return an array, as users are expected most of the
				// time to have multiple roles or be part of multiple groups
				publicMetadata[attr] = retrievePublicMetadataMemberOf(attribute.Values)
			} else {
				publicMetadata[attr] = sanitizedValue
			}
		}
	}

	// TODO: Convert this to an attribute interface, as we do for sign by AddToSignIn()
	// Populate user based on connection attribute mapping
	if value, ok := attributesPerName[samlConnection.AttributeMapping.UserID]; ok {
		samlUser.ID = &value
	}
	if value, ok := attributesPerName[samlConnection.AttributeMapping.EmailAddress]; ok {
		// Email address value from NameID takes precedence over attribute
		if samlUser.EmailAddress == "" {
			samlUser.EmailAddress = strings.ToLower(value)
		}
	}
	if value, ok := attributesPerName[samlConnection.AttributeMapping.FirstName]; ok {
		samlUser.FirstName = &value
	}
	if value, ok := attributesPerName[samlConnection.AttributeMapping.LastName]; ok {
		samlUser.LastName = &value
	}

	publicMetadataBytes, err := json.Marshal(publicMetadata)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	samlUser.PublicMetadata = publicMetadataBytes

	if apiErr := samlUser.Validate(samlConnection); apiErr != nil {
		return nil, apiErr
	}

	return samlUser, nil
}

// Inspect the subject's name ID, to check if the email address is included there
func emailFromNameID(subject *samlsp.Subject) string {
	if subject == nil || subject.NameID == nil {
		return ""
	}

	if subject.NameID.Format != constants.SAMLNameIDFormatEmailAddress {
		return ""
	}

	email := strings.ToLower(strings.TrimSpace(subject.NameID.Value))
	if err := validate.EmailAddress(email, "email"); err != nil {
		return ""
	}

	return email
}

func retrievePublicMetadataMemberOf(attributeValues []samlsp.AttributeValue) []string {
	sanitizedValues := make([]string, 0)
	for _, value := range attributeValues {
		sanitizedValue := strings.TrimSpace(value.Value)
		if sanitizedValue == "" {
			// Ignore empty string attribute values
			continue
		}
		sanitizedValues = append(sanitizedValues, sanitizedValue)
	}

	return sanitizedValues
}

func (s HTTP) getClient(ctx context.Context, env *model.Env, clientID string) (*model.Client, apierror.Error) {
	cdsClient, err := s.clientDataService.FindClient(ctx, env.Instance.ID, clientID)
	if err != nil {
		if errors.Is(err, client_data.ErrNoRecords) {
			return nil, apierror.ClientNotFound(clientID)
		}
		return nil, apierror.Unexpected(err)
	}
	return cdsClient.ToClientModel(), nil
}

func (s HTTP) finishFlowForSignUp(ctx context.Context, tx database.Tx, env *model.Env, client *model.Client, signUp *model.SignUp, verification *model.Verification, samlConnection *model.SAMLConnection, samlUser *pkgsaml.User) (*model.Session, error) {
	samlAccount, err := s.samlAccountService.QueryForUser(ctx, tx, samlConnection, samlUser)
	if err != nil {
		return nil, err
	}

	if samlAccount != nil {
		// User already exists, transfer to a sign in instead
		err = s.transferToSignIn(ctx, tx, client, verification, samlAccount, env.Instance.ID)
		if err != nil {
			return nil, err
		}

		return nil, errTransferToSignIn
	}

	samlResults, err := s.samlAccountService.Create(ctx, tx, verification, samlUser, samlConnection)
	if err != nil {
		return nil, err
	}

	emailIdentification := samlResults.EmailIdentification
	samlIdentification := samlResults.SAMLIdentification
	samlAccount = samlResults.SAMLAccount

	// check if user already exists with an account before SAML was activated
	existingUserID := identifications.FindUserIDIfExists(emailIdentification, samlIdentification)
	if existingUserID != nil {
		// User exists, update saml identification user id and transfer to sign in
		samlIdentification.UserID = null.StringFrom(*existingUserID)
		err = s.identificationRepo.UpdateUserID(ctx, tx, samlIdentification)
		if err != nil {
			return nil, err
		}

		err = s.transferToSignIn(ctx, tx, client, verification, samlAccount, env.Instance.ID)
		if err != nil {
			return nil, err
		}

		return nil, errTransferToSignIn
	}

	signUp.SuccessfulExternalAccountIdentificationID = null.StringFrom(samlIdentification.ID)
	err = s.signUpRepo.UpdateSuccessfulExternalAccountIdentificationID(ctx, tx, signUp)
	if err != nil {
		return nil, err
	}

	return s.signUpService.FinalizeFlow(ctx, tx, sign_up.FinalizeFlowParams{
		SignUp:       signUp,
		Env:          env,
		Client:       client,
		SAMLAccount:  samlAccount,
		UserSettings: usersettings.NewUserSettings(env.AuthConfig.UserSettings),
	})
}

func (s HTTP) finishFlowForSignIn(ctx context.Context, tx database.Tx, env *model.Env, userSettings *usersettings.UserSettings, client *model.Client, signIn *model.SignIn, verification *model.Verification, samlConnection *model.SAMLConnection, samlUser *pkgsaml.User) (*model.Session, error) {
	samlAccount, err := s.samlAccountService.QueryForUser(ctx, tx, samlConnection, samlUser)
	if err != nil {
		return nil, err
	}

	if samlAccount == nil {
		// create or get email identification, saml identification, and saml account
		samlResults, err := s.samlAccountService.Create(ctx, tx, verification, samlUser, samlConnection)
		if err != nil {
			return nil, err
		}

		samlIdentification := samlResults.SAMLIdentification
		emailIdentification := samlResults.EmailIdentification
		samlAccount = samlResults.SAMLAccount

		// check if user already exists
		existingUserID := identifications.FindUserIDIfExists(emailIdentification, samlIdentification)
		if existingUserID == nil {
			// User doesn't exist, transfer to a sign-up instead
			err := s.transferToSignUp(ctx, tx, client, verification, samlConnection, samlIdentification)
			if err != nil {
				return nil, err
			}
			return nil, errTransferToSignUp
		}

		// update saml identification user id
		samlIdentification.UserID = null.StringFrom(*existingUserID)
		err = s.identificationRepo.UpdateUserID(ctx, tx, samlIdentification)
		if err != nil {
			return nil, err
		}
	}

	samlIdentification, err := s.identificationRepo.FindByIDAndInstance(ctx, tx, samlAccount.IdentificationID, env.Instance.ID)
	if err != nil {
		return nil, err
	}

	user, err := s.userRepo.FindByIDAndInstance(ctx, tx, samlIdentification.UserID.String, env.Instance.ID)
	if err != nil {
		return nil, err
	}

	apiErr := s.userLockoutService.EnsureUserNotLocked(ctx, tx, env, user)
	if apiErr != nil {
		return nil, apiErr
	}

	// If instance in single session mode, make sure that the user is not already signed in
	if env.AuthConfig.SessionSettings.SingleSessionMode {
		if err = s.checkIfAlreadySignedIn(ctx, env.Instance.ID, client.ID, user.ID); err != nil {
			return nil, err
		}
	}

	if samlConnection.SyncUserAttributes {
		if err = s.samlAccountService.Update(ctx, tx, userSettings, env.Instance, user, samlAccount, samlUser); err != nil {
			return nil, err
		}
	}

	if err = s.signInService.AttachFirstFactorVerification(ctx, tx, signIn, verification.ID, true); err != nil {
		return nil, err
	}

	signIn.IdentificationID = null.StringFrom(samlIdentification.ID)
	if err = s.signInRepo.UpdateIdentificationID(ctx, tx, signIn); err != nil {
		return nil, err
	}

	readyToConvert, err := s.signInService.IsReadyToConvert(ctx, tx, signIn, userSettings)
	if err != nil {
		return nil, err
	}

	if !readyToConvert {
		return nil, nil
	}

	return s.signInService.ConvertToSession(ctx, tx, sign_in.ConvertToSessionParams{
		Client: client,
		Env:    env,
		SignIn: signIn,
		User:   user,
	})
}

// In case of a SAML IdP-initiated flow, we are using the ticket in order to complete the flow.
// After validating and parsing the SAML response we received from the IdP provider and extract the
// user attributes, we generate a ticket token and redirect to the FAPI /v1/tickets/accept endpoint
// in order to continue and handle the flow.
func (s HTTP) finishFlowForIdpInitiated(ctx context.Context, r *http.Request, env *model.Env, sp *samlsp.ServiceProvider, samlConnection *model.SAMLConnection) (string, apierror.Error) {
	// As in a SAML IdP-initiated flows there is not a SAML request and only a SAML response
	// from the provider directly, we pass an empty array for the possible request IDs
	assertion, err := sp.ParseResponse(r, []string{})
	if err != nil {
		var e *samlsp.InvalidResponseError
		if errors.As(err, &e) {
			// invalid signature or other XML error.
			return "", apierror.SAMLResponseInvalid(e.PrivateErr)
		}
		return "", apierror.Unexpected(err)
	}

	// NOTE: this is security-critical, to mitigate replay and MIM attacks of SAML responses.
	if apiErr := s.performIdpInitiatedSecurityValidations(ctx, r.PostForm.Get("SAMLResponse"), samlConnection.ID); apiErr != nil {
		return "", apiErr
	}

	samlUser, apiErr := userFromAssertionAttributes(assertion, samlConnection)
	if apiErr != nil {
		return "", apiErr
	}

	jti, err := jwt.GenerateJTI(rand.Reader)
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	ticketDuration := constants.ExpiryTimeSAMLIdpInitiated
	claims := ticket.Claims{
		Claims: josejwt.Claims{
			ID: jti,
		},
		InstanceID:       env.Instance.ID,
		SourceType:       constants.OSTSAMLIdpInitiated,
		SourceID:         samlConnection.ID,
		SAMLUser:         samlUser,
		ExpiresInSeconds: &ticketDuration,
	}
	ticketToken, err := ticket.Generate(claims, env.Instance, s.clock)
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	fullURL, err := url.JoinPath(env.Domain.FapiURL(), "/v1/tickets/accept")
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	ticketURL, err := url.Parse(fullURL)
	if err != nil {
		return "", apierror.Unexpected(err)
	}
	query := ticketURL.Query()
	query.Add("ticket", ticketToken)
	ticketURL.RawQuery = query.Encode()

	return ticketURL.String(), nil
}

func (s HTTP) transferToSignIn(ctx context.Context, tx database.Tx, client *model.Client, verification *model.Verification, samlAccount *model.SAMLAccount, instanceID string) error {
	samlIdentification, err := s.identificationRepo.FindByIDAndInstance(ctx, tx, samlAccount.IdentificationID, instanceID)
	if err != nil {
		return err
	}

	user, err := s.userRepo.FindByIDAndInstance(ctx, tx, samlIdentification.UserID.String, instanceID)
	if err != nil {
		return err
	}

	if err = s.checkIfAlreadySignedIn(ctx, instanceID, client.ID, user.ID); err != nil {
		return err
	}

	accTransfer := model.AccountTransfer{AccountTransfer: &sqbmodel.AccountTransfer{
		InstanceID:       instanceID,
		IdentificationID: samlIdentification.ID,
		ExpireAt:         s.clock.Now().UTC().Add(time.Second * time.Duration(constants.ExpiryTimeTransactional)),
	}}
	if err = s.accountTransfersRepo.Insert(ctx, tx, &accTransfer); err != nil {
		return err
	}

	client.ToSignInAccountTransferID = null.StringFrom(accTransfer.ID)
	cdsClient := client_data.NewClientFromClientModel(client)
	if err = s.clientDataService.UpdateClientToSignInAccountTransferID(ctx, cdsClient); err != nil {
		return err
	}
	cdsClient.CopyToClientModel(client)

	verification.AccountTransferID = null.StringFrom(accTransfer.ID)
	return s.verificationRepo.UpdateAccountTransferID(ctx, tx, verification)
}

func (s HTTP) transferToSignUp(
	ctx context.Context,
	tx database.Tx,
	client *model.Client,
	verification *model.Verification,
	samlConnection *model.SAMLConnection,
	samlIdentification *model.Identification,
) error {
	accTransfer := model.AccountTransfer{AccountTransfer: &sqbmodel.AccountTransfer{
		InstanceID:       samlConnection.InstanceID,
		IdentificationID: samlIdentification.ID,
		ExpireAt:         s.clock.Now().UTC().Add(time.Second * time.Duration(constants.ExpiryTimeTransactional)),
	}}
	if err := s.accountTransfersRepo.Insert(ctx, tx, &accTransfer); err != nil {
		return err
	}

	client.ToSignUpAccountTransferID = null.StringFrom(accTransfer.ID)
	cdsClient := client_data.NewClientFromClientModel(client)
	if err := s.clientDataService.UpdateClientToSignUpAccountTransferID(ctx, cdsClient); err != nil {
		return err
	}
	cdsClient.CopyToClientModel(client)

	verification.AccountTransferID = null.StringFrom(accTransfer.ID)
	return s.verificationRepo.UpdateAccountTransferID(ctx, tx, verification)
}

func (s HTTP) checkIfAlreadySignedIn(ctx context.Context, instanceID, clientID, userID string) error {
	signedInSess, err := s.clientDataService.QuerySessionsActiveByInstanceAndClientAndUser(ctx, instanceID, clientID, userID)
	if err != nil {
		return err
	}
	if signedInSess != nil {
		return apierror.AlreadySignedIn(signedInSess.ID)
	}
	return nil
}

func (s HTTP) enforceInstanceRestrictions(
	ctx context.Context,
	exec database.Executor,
	userSettings *usersettings.UserSettings,
	email,
	instanceID string,
	testMode bool,
	blockEmailSubaddresses bool,
) apierror.Error {
	// Override the block email subaddresses check
	userSettings.Restrictions.BlockEmailSubaddresses.Enabled = userSettings.Restrictions.BlockEmailSubaddresses.Enabled && blockEmailSubaddresses
	// Override the ignore dots for gmail addresses check
	userSettings.Restrictions.IgnoreDotsForGmailAddresses.Enabled = userSettings.Restrictions.IgnoreDotsForGmailAddresses.Enabled && blockEmailSubaddresses

	res, err := s.restrictionService.Check(
		ctx,
		exec,
		restrictions.Identification{
			Identifier:          email,
			Type:                constants.ITEmailAddress,
			CanonicalIdentifier: emailaddress.Canonical(email),
		},
		restrictions.Settings{
			Restrictions: userSettings.Restrictions,
			TestMode:     testMode,
		},
		instanceID,
	)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if res.Blocked || !res.Allowed {
		return apierror.IdentifierNotAllowedAccess(email)
	}
	return nil
}

type samlResponse struct {
	ID           string  `xml:"ID,attr"`
	InResponseTo *string `xml:"InResponseTo,attr"`
	Assertion    struct {
		Subject struct {
			SubjectConfirmation struct {
				SubjectConfirmationData struct {
					InResponseTo *string `xml:"InResponseTo,attr"`
				} `xml:"SubjectConfirmationData"`
			} `xml:"SubjectConfirmation"`
		} `xml:"Subject"`
	} `xml:"Assertion"`
}

// Perform additional security validations in case of an IdP-initiated flow in order to mitigate replay and MIM attacks.
// Those are the following:
// 1. Response MUST NOT contain the 'InResponseTo' attribute. This is an indication that the response
// is part of an SP-initiated flow instead.
// 2. The response ID MUST NOT have been used in the past. This is an indication of a replay attack.
func (s HTTP) performIdpInitiatedSecurityValidations(ctx context.Context, rawResponse, samlConnectionID string) apierror.Error {
	responseBytes, err := base64.StdEncoding.DecodeString(rawResponse)
	if err != nil {
		return apierror.Unexpected(err)
	}

	samlResp := samlResponse{}
	if err = xml.Unmarshal(responseBytes, &samlResp); err != nil {
		return apierror.Unexpected(err)
	}

	// Make sure SAML response doesn't contain the 'InResponseTo' attribute
	if samlResp.InResponseTo != nil || samlResp.Assertion.Subject.SubjectConfirmation.SubjectConfirmationData.InResponseTo != nil {
		return apierror.SAMLResponseInvalid(fmt.Errorf("SAML response contains the 'InResponseTo' attribute"))
	}

	// Make sure SAML response id has not been used already
	cacheKey := fmt.Sprintf("saml:%s:%s", samlConnectionID, samlResp.ID)
	exists, err := s.cache.Exists(ctx, cacheKey)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if exists {
		return apierror.SAMLResponseInvalid(fmt.Errorf("SAML response ID already used"))
	}

	if err := s.cache.Set(ctx, cacheKey, samlResp.ID, time.Second*time.Duration(constants.ExpiryTimeMediumShort)); err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}

// The below contains the logic on how we handle the error in the ACS endpoint. As we do for OAuth, if an error
// occurs, we don't respond with it as it will end up in the end user, unable to do anything. Instead, we store it
// in the 'verifications.error' column. In this way, we keep a state for debugging purposes but also UI knows how to
// handle them and preview a proper error message in our components.
func (s HTTP) handleACSError(ctx context.Context, w http.ResponseWriter, r *http.Request, verification *model.Verification, relayStateToken *model.SAMLRelayStateToken, err error) (interface{}, apierror.Error) {
	var apiErr apierror.Error
	if e, ok := apierror.As(err); ok {
		apiErr = e
	} else {
		apiErr = apierror.Unexpected(err)
	}

	if !apiErr.IsTypeOf(apierror.ExternalAccountNotFoundCode) && !apiErr.IsTypeOf(apierror.ExternalAccountExistsCode) {
		// reporting to logger in order to be able to find the root cause of the error if needed
		log.Warning(ctx, "saml: error during ACS: %s", apiErr.Error())
	}

	resp := apierror.ToResponse(ctx, apiErr)
	errorBytes, jsonErr := json.Marshal(resp.Errors[0])
	if jsonErr != nil {
		return nil, apierror.Unexpected(jsonErr)
	}

	verification.Error = null.JSONFrom(errorBytes)
	if err := s.verificationRepo.UpdateError(ctx, s.db, verification); err != nil {
		return nil, apierror.Unexpected(err)
	}

	http.Redirect(w, r, relayStateToken.RedirectURL, http.StatusSeeOther)
	return nil, nil
}
