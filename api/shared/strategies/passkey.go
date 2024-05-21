package strategies

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/passkeys"
	"clerk/api/shared/serializable"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cache"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/requesting_user"
	usersettings "clerk/pkg/usersettings/clerk"
	clerkwebauthn "clerk/pkg/webauthn"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"
	"clerk/utils/param"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

var (
	ErrInvalidPasskeyPublicKeyCredential = errors.New("passkey: invalid public key credential")
	ErrInvalidVerificationPasskeyNonce   = errors.New("passkey: verification nonce invalid")
)

type PasskeyPreparerParams struct {
	// registration
	Identification *model.Identification
	Creation       *protocol.CredentialCreation
	Session        *webauthn.SessionData

	// authentication
	User   *model.User
	Origin string
}

type PasskeyPreparer struct {
	clock            clockwork.Clock
	env              *model.Env
	identification   *model.Identification
	verificationRepo *repository.Verification
	passkeySerice    *passkeys.Service

	// passkey related payloads
	creation *protocol.CredentialCreation
	session  *webauthn.SessionData
	origin   string
	user     *model.User
}

func NewPasskeyPreparer(deps clerk.Deps, env *model.Env, params *PasskeyPreparerParams) PasskeyPreparer {
	return PasskeyPreparer{
		clock:            deps.Clock(),
		env:              env,
		identification:   params.Identification,
		verificationRepo: repository.NewVerification(),
		passkeySerice:    passkeys.NewService(deps),
		creation:         params.Creation,
		session:          params.Session,
		origin:           params.Origin,
		user:             params.User,
	}
}

func (p PasskeyPreparer) Identification() *model.Identification {
	return p.identification
}

func (p PasskeyPreparer) Prepare(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	// passkey registration flow
	if p.creation != nil && p.session != nil && p.identification != nil {
		return p.prepareRegistration(ctx, tx)
	}

	// passkey authentication flow
	return p.prepareAuthentication(ctx, tx)
}

// prepareRegistration creates the verificatiom and links an existing passkey identification
// for the passkey registration flow
func (p PasskeyPreparer) prepareRegistration(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	// p.creation.Response contains the payload clerk-js needs in order to
	// call the WebAuthn API for passkey registration
	creationOptionsBytes, err := json.Marshal(p.creation.Response)
	if err != nil {
		return nil, err
	}
	creationOptions := string(creationOptionsBytes)

	// utilize nonce to send passkey registration payload
	verification, err := createVerification(ctx, tx, p.clock, &createVerificationParams{
		instanceID:       p.env.Instance.ID,
		strategy:         constants.VSPasskey,
		identificationID: &p.identification.ID,
		nonce:            &creationOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare/registration: creating verification for passkey registration: %w", err)
	}

	return verification, nil
}

// prepareAuthentication generates the required payload the frontend expects to authenticate with a passkey
// using the WebAuthn API.
// The verification created in this method is not tied to an identification, since discoverable passkey login
// means that the specific passkey isn't determined yet. The user may or may not be known.
// The identification will be linked after successful attempt first factor.
func (p PasskeyPreparer) prepareAuthentication(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	rpIDOrigin := p.origin
	var err error
	if p.env.Instance.IsProduction() {
		rpIDOrigin, err = p.passkeySerice.GetRpIDOriginForProductionInstances(ctx, tx, p.env)
		if err != nil {
			return nil, err
		}
	}

	webAuthnHandler, err := clerkwebauthn.New(ctx, p.env, rpIDOrigin, p.origin, tx)
	if err != nil {
		return nil, err
	}

	var credentialAssertion *protocol.CredentialAssertion
	var apiErr error
	if p.user != nil {
		credentialAssertion, apiErr = p.beginLogin(ctx, tx, webAuthnHandler)
	} else {
		credentialAssertion, apiErr = p.beginDiscoverableLogin(webAuthnHandler)
	}
	if apiErr != nil {
		return nil, apiErr
	}

	requestOptionsBytes, err := json.Marshal(credentialAssertion.Response)
	if err != nil {
		return nil, fmt.Errorf("prepare: marshaling passkey credential assertion: %w", err)
	}
	requestOptions := string(requestOptionsBytes)

	// utilize nonce to send passkey authentication payload
	// in this case, we don't have an identification yet
	verification, err := createVerification(ctx, tx, p.clock, &createVerificationParams{
		instanceID: p.env.Instance.ID,
		strategy:   constants.VSPasskey,
		nonce:      &requestOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare: creating verification for passkey authentication: %w", err)
	}

	return verification, nil
}

func (p PasskeyPreparer) beginLogin(ctx context.Context, tx database.Tx, handler *clerkwebauthn.WebAuthn) (*protocol.CredentialAssertion, apierror.Error) {
	// convert to webAuthn user
	webAuthnUser, err := p.passkeySerice.GetWebAuthnUser(ctx, tx, p.env.Instance.ID, p.user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// user must have at least one registered passkey
	if len(webAuthnUser.WebAuthnCredentials()) == 0 {
		return nil, apierror.NoPasskeysFoundForUser()
	}

	userVerificationOpts := webauthn.WithUserVerification(protocol.VerificationRequired)
	credentialAssertion, _, err := handler.WebAuthn.BeginLogin(webAuthnUser, userVerificationOpts)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return credentialAssertion, nil
}

func (p PasskeyPreparer) beginDiscoverableLogin(handler *clerkwebauthn.WebAuthn) (*protocol.CredentialAssertion, apierror.Error) {
	userVerificationOpts := webauthn.WithUserVerification(protocol.VerificationRequired)
	credentialAssertion, _, err := handler.WebAuthn.BeginDiscoverableLogin(userVerificationOpts)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return credentialAssertion, nil
}

type PasskeyAttemptorParams struct {
	PublicKeyCredential string
	Origin              string

	// registration
	Passkey *model.Passkey
	User    *model.User

	// authentication
	SignIn *model.SignIn
}

type PasskeyAttemptor struct {
	cache        cache.Cache
	clock        clockwork.Clock
	env          *model.Env
	verification *model.Verification

	eventService             *events.Service
	identificationRepo       *repository.Identification
	passkeyAuthenticatorRepo *repository.PasskeyAuthenticator
	passkeyRepo              *repository.Passkey
	passkeyService           *passkeys.Service
	serializableService      *serializable.Service
	signInRepo               *repository.SignIn
	userRepo                 *repository.Users
	verificationRepo         *repository.Verification
	verificationService      *verifications.Service

	publicKeyCredential string
	passkey             *model.Passkey
	origin              string
	signIn              *model.SignIn
}

func NewPasskeyAttemptor(deps clerk.Deps, env *model.Env, verification *model.Verification, params *PasskeyAttemptorParams) PasskeyAttemptor {
	return PasskeyAttemptor{
		cache:                    deps.Cache(),
		clock:                    deps.Clock(),
		env:                      env,
		verification:             verification,
		eventService:             events.NewService(deps),
		identificationRepo:       repository.NewIdentification(),
		passkeyAuthenticatorRepo: repository.NewPasskeyAuthenticator(),
		passkeyRepo:              repository.NewPasskey(),
		passkeyService:           passkeys.NewService(deps),
		serializableService:      serializable.NewService(deps.Clock()),
		signInRepo:               repository.NewSignIn(),
		userRepo:                 repository.NewUsers(),
		verificationRepo:         repository.NewVerification(),
		verificationService:      verifications.NewService(deps.Clock()),
		publicKeyCredential:      params.PublicKeyCredential,
		passkey:                  params.Passkey,
		origin:                   params.Origin,
		signIn:                   params.SignIn,
	}
}

func (v PasskeyAttemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	if err := checkVerificationStatus(ctx, tx, v.verificationService, v.verification); err != nil {
		return v.verification, err
	}

	// passkey registration flow
	if v.passkey != nil {
		return v.attemptRegistration(ctx, tx)
	}

	// passkey authentication flow
	return v.attemptAuthentication(ctx, tx)
}

func (v PasskeyAttemptor) attemptRegistration(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	// unmarshal public key credential creation options
	var creationOpts protocol.PublicKeyCredentialCreationOptions
	err := json.Unmarshal([]byte(v.verification.Nonce.String), &creationOpts)
	if err != nil {
		return nil, ErrInvalidVerificationPasskeyNonce
	}

	// unmarshal credential creation response and construct http request
	var ccr protocol.CredentialCreationResponse
	err = json.Unmarshal([]byte(v.publicKeyCredential), &ccr)
	if err != nil {
		return nil, ErrInvalidPasskeyPublicKeyCredential
	}

	// third party go-webauthn library expects a http.Request object to finish passkey registration
	r, err := http.NewRequest(http.MethodPost, "", bytes.NewBuffer([]byte(v.publicKeyCredential)))
	if err != nil {
		return nil, fmt.Errorf("passkey/attempt: constructing http request for passkey finish registration: %w", err)
	}

	credential, updatedPasskeyName, apiErr := v.finishPasskeyRegistration(ctx, tx, creationOpts.Challenge, r)
	if apiErr != nil {
		return nil, apiErr
	}

	// store the credential info the database on successful registration
	var transports []string
	for _, t := range credential.Transport {
		transports = append(transports, string(t))
	}
	credentialIDStr := base64.RawURLEncoding.EncodeToString(credential.ID)
	publicKeyStr := base64.RawURLEncoding.EncodeToString(credential.PublicKey)

	v.passkey.Transports = transports
	v.passkey.CredentialID = null.StringFrom(credentialIDStr)
	v.passkey.PublicKey = null.StringFrom(publicKeyStr)
	v.passkey.LastUsedAt = null.TimeFrom(v.clock.Now().UTC())

	columnsToUpdate := []string{
		sqbmodel.PasskeyColumns.CredentialID,
		sqbmodel.PasskeyColumns.PublicKey,
		sqbmodel.PasskeyColumns.Transports,
		sqbmodel.PasskeyColumns.LastUsedAt,
	}
	if updatedPasskeyName != "" && v.passkey.Name != updatedPasskeyName {
		columnsToUpdate = append(columnsToUpdate, sqbmodel.PasskeyColumns.Name)
		v.passkey.Name = updatedPasskeyName
	}
	err = v.passkeyRepo.Update(ctx, tx, v.passkey, columnsToUpdate...)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// remove nonce value from verification
	v.verification.Nonce = null.StringFromPtr(nil)
	err = v.verificationRepo.Update(ctx, tx, v.verification, sqbmodel.VerificationColumns.Nonce)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	user, err := v.userRepo.QueryByInstanceAndIdentificationID(ctx, tx, v.env.Instance.ID, v.passkey.IdentificationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if user == nil {
		return nil, apierror.IdentificationNotFound(v.passkey.IdentificationID)
	}

	// send user updated webhook
	usersettings := usersettings.NewUserSettings(v.env.AuthConfig.UserSettings)
	if err = v.sendUserUpdatedEvent(ctx, tx, v.env.Instance, usersettings, user); err != nil {
		return nil, apierror.Unexpected(err)
	}

	// send passkey added notification
	if err = v.passkeyService.SendPasskeyNotification(ctx, tx, v.env, user, v.passkey.Name, constants.PasskeyAddedSlug); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return v.verification, nil
}

func (v PasskeyAttemptor) attemptAuthentication(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	var requestOptions protocol.PublicKeyCredentialRequestOptions
	err := json.Unmarshal([]byte(v.verification.Nonce.String), &requestOptions)
	if err != nil {
		return nil, ErrInvalidVerificationPasskeyNonce
	}

	// unmarshal credential assertion response
	var car protocol.CredentialAssertionResponse
	err = json.Unmarshal([]byte(v.publicKeyCredential), &car)
	if err != nil {
		return nil, ErrInvalidPasskeyPublicKeyCredential
	}

	// third party go-webauthn library expects a http.Request object to finish passkey authentication
	r, err := http.NewRequest(http.MethodPost, "", bytes.NewBuffer([]byte(v.publicKeyCredential)))
	if err != nil {
		return nil, fmt.Errorf("passkey/attempt: constructing http request for passkey attempt authentication: %w", err)
	}

	// get RP ID origin
	rpIDOrigin := v.origin
	if v.env.Instance.IsProduction() {
		rpIDOrigin, err = v.passkeyService.GetRpIDOriginForProductionInstances(ctx, tx, v.env)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	// instantiate webauthn server
	webAuthnHandler, err := clerkwebauthn.New(ctx, v.env, rpIDOrigin, v.origin, tx)
	if err != nil {
		return nil, fmt.Errorf("passkey/attempt: instantiating webauthn handler for passkey: %w", err)
	}

	session := webauthn.SessionData{
		Challenge:        requestOptions.Challenge.String(),
		UserVerification: requestOptions.UserVerification,
	}
	var credential *webauthn.Credential
	var apiErr error
	if v.signIn.IdentificationID.Valid {
		credential, apiErr = v.finishLogin(ctx, tx, webAuthnHandler, session, r)
	} else {
		credential, apiErr = v.finishDiscoverableLogin(ctx, webAuthnHandler, session, r)
	}
	if apiErr != nil {
		return nil, apiErr
	}

	// verify passkey exists
	credentialID := base64.RawURLEncoding.EncodeToString(credential.ID)
	passkey, err := v.passkeyRepo.QueryByCredentialID(ctx, tx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("passkey/attempt: query passkey with credential ID: %w", err)
	}
	if passkey == nil {
		return nil, apierror.PasskeyNotRegistered()
	}

	identification, err := v.identificationRepo.FindByIDAndType(ctx, tx, passkey.IdentificationID, constants.ITPasskey)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// update passkey last used at
	passkey.LastUsedAt = null.TimeFrom(v.clock.Now().UTC())
	err = v.passkeyRepo.Update(ctx, tx, passkey, sqbmodel.PasskeyColumns.LastUsedAt)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// remove nonce value from verification and add identification to verification
	v.verification.Nonce = null.StringFromPtr(nil)
	v.verification.IdentificationID = null.StringFrom(identification.ID)
	err = v.verificationRepo.Update(ctx, tx, v.verification, sqbmodel.VerificationColumns.Nonce, sqbmodel.VerificationColumns.IdentificationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// add identification to sign in
	v.signIn.IdentificationID = null.StringFrom(identification.ID)
	if err := v.signInRepo.UpdateIdentificationID(ctx, tx, v.signIn); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return v.verification, nil
}

func (v PasskeyAttemptor) finishLogin(ctx context.Context, tx database.Tx, handler *clerkwebauthn.WebAuthn, session webauthn.SessionData, r *http.Request) (*webauthn.Credential, apierror.Error) {
	user, err := v.userRepo.QueryByInstanceAndIdentificationID(ctx, tx, v.env.Instance.ID, v.signIn.IdentificationID.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if user == nil {
		return nil, apierror.PasskeyNotRegistered()
	}

	webAuthnUser, err := v.passkeyService.GetWebAuthnUser(ctx, tx, v.env.Instance.ID, user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	session.UserID = webAuthnUser.WebAuthnID()
	credential, err := handler.WebAuthn.FinishLogin(webAuthnUser, session, r)
	if err != nil {
		log.Warning(ctx, "passkey: error finishing login: %w", err)
		return nil, apierror.PasskeyAuthenticationFailure()
	}
	return credential, nil
}

func (v PasskeyAttemptor) finishDiscoverableLogin(ctx context.Context, handler *clerkwebauthn.WebAuthn, session webauthn.SessionData, r *http.Request) (*webauthn.Credential, apierror.Error) {
	credential, err := handler.WebAuthn.FinishDiscoverableLogin(handler.DiscoverableUserHandler, session, r)
	if err != nil {
		apiErr := clerkwebauthn.ConvertWebAuthnError(err)
		if apiErr != nil {
			return nil, apiErr
		}
		log.Warning(ctx, "passkey: error finishing discoverable login: %w", err)
		return nil, apierror.PasskeyAuthenticationFailure()
	}
	return credential, nil
}

func (PasskeyAttemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, ErrInvalidPasskeyPublicKeyCredential) {
		return apierror.PasskeyInvalidPublicKeyCredential(param.PublicKeyCredential.Name)
	}
	if errors.Is(err, ErrInvalidVerificationPasskeyNonce) {
		return apierror.PasskeyInvalidVerification()
	}
	return toAPIErrors(err)
}

func (v PasskeyAttemptor) finishPasskeyRegistration(ctx context.Context, tx database.Tx, challenge protocol.URLEncodedBase64, r *http.Request) (*webauthn.Credential, string, apierror.Error) {
	// instantiate webauthn handler
	webAuthnHandler, err := clerkwebauthn.New(ctx, v.env, v.passkey.Origin, v.origin, tx)
	if err != nil {
		return nil, "", apierror.Unexpected(err)
	}

	// convert to webauthn user
	user := requesting_user.FromContext(ctx)
	webAuthnUser, err := v.passkeyService.GetWebAuthnUser(ctx, tx, v.env.Instance.ID, user)
	if err != nil {
		return nil, "", apierror.Unexpected(err)
	}

	// finish registration
	session := webauthn.SessionData{
		Challenge:        challenge.String(),
		UserID:           webAuthnUser.WebAuthnID(),
		UserVerification: protocol.VerificationRequired,
	}
	credential, err := webAuthnHandler.WebAuthn.FinishRegistration(webAuthnUser, session, r)
	if err != nil {
		log.Warning(ctx, "passkey: error finishing registration: %w", err)
		return nil, "", apierror.PasskeyRegistrationFailure()
	}

	// look up authenticator name by AAGUID
	aaguid, err := uuid.FromBytes(credential.Authenticator.AAGUID)
	if err != nil {
		return nil, "", apierror.Unexpected(err)
	}
	authenticator, err := v.passkeyAuthenticatorRepo.QueryByAAGUID(ctx, tx, aaguid.String())
	if err != nil {
		return nil, "", apierror.Unexpected(err)
	}
	// do not use AAGUID to lookup if its empty (all zeroes)
	if authenticator != nil && aaguid != uuid.Nil {
		return credential, authenticator.Name, nil
	}

	return credential, "", nil
}

func (v PasskeyAttemptor) sendUserUpdatedEvent(
	ctx context.Context,
	tx database.Tx,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User) error {
	userSerializable, err := v.serializableService.ConvertUser(ctx, tx, userSettings, user)
	if err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: serializing user %+v: %w", user, err)
	}

	if err = v.eventService.UserUpdated(ctx, tx, instance, serialize.UserToServerAPI(ctx, userSerializable)); err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: send user updated event for user %s: %w", user.ID, err)
	}
	return nil
}
