package passkeys

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/users"
	"clerk/api/serialize"
	"clerk/api/shared/identifications"
	"clerk/api/shared/passkeys"
	"clerk/api/shared/serializable"
	"clerk/api/shared/strategies"
	"clerk/api/shared/user_profile"
	sharedusers "clerk/api/shared/users"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	clerkwebauthn "clerk/pkg/webauthn"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/volatiletech/null/v8"
)

const (
	maxNameLength             = 256
	maxAllowedPasskeysPerUser = 10
)

type Service struct {
	deps clerk.Deps
	db   database.Database

	// repositories
	identificationRepo *repository.Identification
	passkeyRepo        *repository.Passkey

	// services
	identificationService *identifications.Service
	passkeyService        *passkeys.Service
	serializableService   *serializable.Service
	userProfileService    *user_profile.Service
	usersService          *users.Service
	sharedUsersService    *sharedusers.Service
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		deps:                  deps,
		db:                    deps.DB(),
		identificationRepo:    repository.NewIdentification(),
		passkeyRepo:           repository.NewPasskey(),
		identificationService: identifications.NewService(deps),
		passkeyService:        passkeys.NewService(deps),
		serializableService:   serializable.NewService(deps.Clock()),
		userProfileService:    user_profile.NewService(deps.Clock()),
		usersService:          users.NewService(deps),
		sharedUsersService:    sharedusers.NewService(deps),
	}
}

// CreatePasskey intializes the passkey registration flow for the given user
func (s *Service) CreatePasskey(ctx context.Context, user *model.User, origin, passkeyName string) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	passkeyAttribute := userSettings.GetAttribute(names.Passkey)

	if !passkeyAttribute.Base().Enabled {
		return nil, apierror.FeatureNotEnabled()
	}

	var identification *model.Identification
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// check that the number of passkey identifications for user is less than the max
		numPasskeys, err := s.identificationRepo.CountByUserAndTypeAndClaimed(ctx, tx, user.ID, constants.ITPasskey)
		if err != nil {
			return true, err
		}
		if int(numPasskeys) == maxAllowedPasskeysPerUser {
			return true, apierror.PasskeyQuotaExceeded(maxAllowedPasskeysPerUser)
		}

		response, apiErr := s.beginPasskeyRegistration(ctx, tx, env, &beginPasskeyRegistrationParams{
			Origin: origin,
			User:   user,
		})
		if apiErr != nil {
			return true, apiErr
		}

		ident, _, err := s.createPasskeyAndIdentification(ctx, tx, &createPasskeyParams{
			UserID:     user.ID,
			InstanceID: env.Instance.ID,
			Name:       passkeyName,
			Origin:     response.rpIDOrigin,
		})
		if err != nil {
			return true, err
		}

		// prepare step happens along with the create step for passkeys
		preparerParams := &strategies.PasskeyPreparerParams{
			Identification: ident,
			Creation:       response.creation,
			Session:        response.sessionData,
		}
		preparer := strategies.NewPasskeyPreparer(s.deps, env, preparerParams)
		if err != nil {
			return true, err
		}

		verification, err := preparer.Prepare(ctx, tx)
		if err != nil {
			return true, err
		}

		ident.VerificationID = null.StringFrom(verification.ID)
		if err := s.identificationRepo.UpdateVerificationID(ctx, tx, ident); err != nil {
			return true, err
		}

		identification = ident
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return s.toIdentificationResponse(ctx, identification)
}

type createPasskeyParams struct {
	UserID     string
	InstanceID string
	Name       string
	Origin     string
}

func (s *Service) createPasskeyAndIdentification(ctx context.Context, tx database.Tx, params *createPasskeyParams) (*model.Identification, *model.Passkey, error) {
	// delete the old unverified passkey identifications
	// passkey registration cannot be completed later if user cancels or something goes wrong during attempt verification
	err := s.identificationRepo.DeleteUnverifiedByUserAndType(ctx, tx, params.UserID, constants.ITPasskey)
	if err != nil {
		return nil, nil, err
	}

	// create identification for passkey
	createIdentificationData := identifications.CreateIdentificationData{
		InstanceID: params.InstanceID,
		UserID:     &params.UserID,
		Type:       constants.ITPasskey,
	}
	identification, err := s.identificationService.CreateIdentification(ctx, tx, createIdentificationData)
	if err != nil {
		return nil, nil, err
	}

	// create and save passkey data
	passkey := &model.Passkey{Passkey: &sqbmodel.Passkey{
		InstanceID:       params.InstanceID,
		IdentificationID: identification.ID,
		Name:             params.Name,
		Origin:           params.Origin,
	}}
	err = s.passkeyRepo.Insert(ctx, tx, passkey)
	if err != nil {
		return nil, nil, err
	}

	return identification, passkey, nil
}

type beginPasskeyRegistrationParams struct {
	Origin string
	User   *model.User
}

type registrationResponse struct {
	creation    *protocol.CredentialCreation
	sessionData *webauthn.SessionData
	rpIDOrigin  string
}

func (s *Service) beginPasskeyRegistration(ctx context.Context, tx database.Tx, env *model.Env, params *beginPasskeyRegistrationParams) (*registrationResponse, apierror.Error) {
	// get RP ID origin
	var err error
	rpIDOrigin := params.Origin
	if env.Instance.IsProduction() {
		rpIDOrigin, err = s.passkeyService.GetRpIDOriginForProductionInstances(ctx, tx, env)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	// instantiate webAuthn handler
	webAuthnHandler, err := clerkwebauthn.New(ctx, env, rpIDOrigin, params.Origin, tx)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// convert to webAuthn user
	webAuthnUser, err := s.passkeyService.GetWebAuthnUser(ctx, tx, env.Instance.ID, params.User)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// exclude user's existing passkey credentials
	var existingCredentials []protocol.CredentialDescriptor
	for _, cred := range webAuthnUser.WebAuthnCredentials() {
		existingCredentials = append(existingCredentials, cred.Descriptor())
	}
	excludeCredentialsOpts := webauthn.WithExclusions(existingCredentials)

	// set registration options
	authenticatorSelectionOpts := webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
		RequireResidentKey: protocol.ResidentKeyRequired(),
		ResidentKey:        protocol.ResidentKeyRequirementRequired,
		UserVerification:   protocol.VerificationRequired,
	})
	conveyancePreferenceOpts := webauthn.WithConveyancePreference(protocol.PreferDirectAttestation)

	creation, session, err := webAuthnHandler.WebAuthn.BeginRegistration(webAuthnUser, excludeCredentialsOpts, authenticatorSelectionOpts, conveyancePreferenceOpts)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return &registrationResponse{
		creation:    creation,
		sessionData: session,
		rpIDOrigin:  rpIDOrigin,
	}, nil
}

func (s *Service) ReadPasskey(ctx context.Context, user *model.User, passkeyIdentID string) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)
	identification, err := s.identificationRepo.QueryByIDAndUser(ctx, s.db, env.Instance.ID, passkeyIdentID, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if identification == nil {
		return nil, apierror.IdentificationNotFound(passkeyIdentID)
	}
	if !identification.IsVerified() {
		return nil, apierror.PasskeyIdentificationNotVerified()
	}

	return s.toIdentificationResponse(ctx, identification)
}

func (s *Service) UpdatePasskey(ctx context.Context, user *model.User, passkeyIdentID string, passkeyName *string) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)
	usersettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	var identificationSerializable *model.IdentificationSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		passkeyIdent, performedUpdate, err := s.update(ctx, tx, user, passkeyIdentID, passkeyName)
		if err != nil {
			return true, err
		}

		// send user updated webhook if there was a change
		if performedUpdate {
			if _, err = s.sharedUsersService.SendUserUpdatedEvent(ctx, tx, env.Instance, usersettings, user); err != nil {
				return true, err
			}
		}

		identificationSerializable, err = s.serializableService.ConvertIdentification(ctx, tx, passkeyIdent)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIError := apierror.As(txErr); isAPIError {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.IdentificationPasskey(identificationSerializable), nil
}

func (s *Service) validatePasskeyName(name string) apierror.Error {
	if len(name) > maxNameLength {
		return apierror.FormParameterMaxLengthExceeded(param.PasskeyName.Name, maxNameLength)
	}
	return nil
}

func (s *Service) update(ctx context.Context, tx database.Tx, user *model.User, passkeyIdentID string, passkeyName *string) (*model.Identification, bool, error) {
	env := environment.FromContext(ctx)
	identification, err := s.identificationRepo.QueryByIDAndUser(ctx, tx, env.Instance.ID, passkeyIdentID, user.ID)
	if err != nil {
		return nil, false, apierror.Unexpected(err)
	}
	if identification == nil {
		return nil, false, apierror.IdentificationNotFound(passkeyIdentID)
	}

	performedUpdate := false
	if passkeyName != nil {
		apiErr := s.validatePasskeyName(*passkeyName)
		if apiErr != nil {
			return nil, false, apiErr
		}

		passkey, err := s.passkeyRepo.QueryByIdentificationID(ctx, tx, passkeyIdentID)
		if err != nil {
			return nil, false, apierror.Unexpected(err)
		}
		if passkey == nil {
			return nil, false, apierror.PasskeyNotRegistered()
		}

		passkey.Name = *passkeyName
		err = s.passkeyRepo.Update(ctx, tx, passkey, sqbmodel.PasskeyColumns.Name)
		if err != nil {
			return nil, false, apierror.Unexpected(err)
		}

		performedUpdate = true
	}

	return identification, performedUpdate, nil
}

func (s *Service) toIdentificationResponse(ctx context.Context, ident *model.Identification) (interface{}, apierror.Error) {
	identificationSerializable, err := s.serializableService.ConvertIdentification(ctx, s.db, ident)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response, err := serialize.Identification(identificationSerializable)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return response, nil
}

func (s *Service) DeletePasskey(ctx context.Context, user *model.User, passkeyIdentID string) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)

	var deletedObjectResponse *serialize.DeletedObjectResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		passkey, err := s.passkeyRepo.QueryByIdentificationID(ctx, tx, passkeyIdentID)
		if err != nil {
			return true, err
		}
		if passkey == nil {
			return true, apierror.PasskeyNotRegistered()
		}
		passkeyName := passkey.Name

		response, err := s.usersService.DeleteIdentification(ctx, user, passkeyIdentID)
		if err != nil {
			return true, err
		}

		err = s.passkeyService.SendPasskeyNotification(ctx, tx, env, user, passkeyName, constants.PasskeyRemovedSlug)
		if err != nil {
			return true, err
		}

		deletedObjectResponse = response
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIError := apierror.As(txErr); isAPIError {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return deletedObjectResponse, nil
}
