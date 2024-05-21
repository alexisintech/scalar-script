package verification

import (
	"context"
	"errors"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/shared/client_data"
	"clerk/api/shared/identifications"
	"clerk/api/shared/sessions"
	"clerk/api/shared/sign_in"
	"clerk/api/shared/sign_up"
	"clerk/api/shared/strategies"
	"clerk/model"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctxkeys"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	db    database.Database
	clock clockwork.Clock

	// services
	identificationService *identifications.Service
	signInService         *sign_in.Service
	signUpService         *sign_up.Service
	sessionService        *sessions.Service

	// repositories
	identificationRepo *repository.Identification
	signInRepo         *repository.SignIn
	signUpRepo         *repository.SignUp
	userRepo           *repository.Users
	verificationRepo   *repository.Verification

	// Session management
	clientDataService *client_data.Service
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:                 deps.Clock(),
		db:                    deps.DB(),
		identificationService: identifications.NewService(deps),
		signInService:         sign_in.NewService(deps),
		signUpService:         sign_up.NewService(deps),
		sessionService:        sessions.NewService(deps),
		identificationRepo:    repository.NewIdentification(),
		signInRepo:            repository.NewSignIn(),
		signUpRepo:            repository.NewSignUp(),
		userRepo:              repository.NewUsers(),
		verificationRepo:      repository.NewVerification(),
		clientDataService:     client_data.NewService(deps),
	}
}

// VerifyTokenClaims validates the provided claims, attempts to complete
// the Verification that's referenced in the claims and performs all
// necessary actions depending on the claims source type, upon successful
// verification.
// VerifyTokenClaims returns the newly created session id (if a new session
// has been created), or an error if there's a problem with the provided JWT,
// like being invalid or expired.
// If the requesting is the same as the client of the source of the claims,
// and we have a completed sign in or sign up flow, we also return the new
// client. This new client can be used to drop a new cookie in the HTTP
// layer.
// Finally, it will also return an error if, for some reason, the identification
// cannot be verified.
func (s *Service) VerifyTokenClaims(ctx context.Context, claims strategies.VerificationLinkTokenClaims, userSettings *usersettings.UserSettings) (*model.Session, *model.Client, apierror.Error) {
	env := environment.FromContext(ctx)
	requestingClient, _ := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	var requestingClientID string
	if requestingClient != nil {
		requestingClientID = requestingClient.ID
	}

	if claims.InstanceID != env.Instance.ID {
		return nil, nil, apierror.ResourceNotFound()
	}

	var newSession *model.Session
	var newClient *model.Client

	switch claims.SourceType {
	case constants.OSTSignUp:
		if userSettings.AttackProtection.EmailLink.RequireSameClient && requestingClientID == "" {
			return nil, nil, apierror.SignUpEmailLinkNotSameClient()
		}
		signUp, err := s.signUpRepo.QueryByIDAndInstance(ctx, s.db, claims.SourceID, claims.InstanceID)
		if err != nil {
			return nil, nil, apierror.Unexpected(err)
		}
		if signUp == nil {
			return nil, nil, apierror.SignUpNotFound(claims.SourceID)
		}
		if !signUp.EmailAddressID.Valid {
			return nil, nil, apierror.VerificationMissing()
		}

		var attemptor strategies.Attemptor
		txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
			attemptor = strategies.NewEmailLinkAttemptor(claims.VerificationID, claims.InstanceID, s.clock)
			if _, err := strategies.AttemptVerification(ctx, tx, attemptor, s.verificationRepo, requestingClientID); err != nil {
				return true, err
			}
			return false, nil
		})
		if errors.Is(txErr, strategies.ErrAlreadyVerified) {
			// Ignore verification already verified error but check if there is a session associated with sign up
			var session *model.Session
			if signUp.CreatedSessionID.Valid {
				// When SignUp.CreatedSessionID is created the client is the same as the one associated with the signup record.
				// Source: https://github.com/clerk/clerk_go/blob/858584e00fa35a297f8a4245e46ff26a1e1e2241/api/shared/sign_up/service.go#L219
				cdsSession, err := s.clientDataService.FindSession(ctx, claims.InstanceID, signUp.ClientID, signUp.CreatedSessionID.String)
				if err != nil {
					return nil, nil, apierror.Unexpected(err)
				}
				session = cdsSession.ToSessionModel()
			}
			return session, nil, nil
		} else if txErr != nil {
			if attemptor != nil {
				return nil, nil, attemptor.ToAPIError(txErr)
			}
			return nil, nil, apierror.Unexpected(txErr)
		}

		identification, err := s.identificationRepo.FindByID(ctx, s.db, signUp.EmailAddressID.String)
		if err != nil {
			return nil, nil, apierror.VerificationMissing()
		}

		cdsSignUpClient, err := s.clientDataService.FindClient(ctx, env.Instance.ID, signUp.ClientID)
		if err != nil {
			return nil, nil, apierror.Unexpected(err)
		}
		signUpClient := cdsSignUpClient.ToClientModel()

		isSameClient := requestingClientID == signUp.ClientID

		if userSettings.AttackProtection.EmailLink.RequireSameClient && !isSameClient {
			return nil, nil, apierror.SignUpEmailLinkNotSameClient()
		}

		txErr = s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
			// Mark identification as verified.
			identification.Status = constants.ISVerified
			if err := s.identificationRepo.UpdateStatus(ctx, tx, identification); err != nil {
				return true, err
			}
			newSession, err = s.signUpService.FinalizeFlow(
				ctx,
				tx,
				sign_up.FinalizeFlowParams{
					SignUp:               signUp,
					Env:                  env,
					Client:               signUpClient,
					UserSettings:         userSettings,
					PostponeCookieUpdate: !isSameClient,
				},
			)
			return err != nil, err
		})
		if txErr != nil {
			if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
				return nil, nil, apiErr
			} else if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueIdentification) {
				return nil, nil, apierror.IdentificationExists(constants.ITEmailAddress, nil)
			} else if errors.Is(txErr, clerkerrors.ErrIdentificationClaimed) {
				return nil, nil, apierror.IdentificationClaimed()
			} else if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueReservedIdentification) {
				return nil, nil, apierror.IdentificationClaimed()
			}
			return nil, nil, apierror.Unexpected(txErr)
		}

		if newSession != nil {
			if err := s.sessionService.Activate(ctx, env.Instance, newSession); err != nil {
				return nil, nil, apierror.Unexpected(err)
			}
		}

		if isSameClient && newSession != nil {
			newClient = signUpClient
		}

	case constants.OSTSignIn:
		// if magic link verification needs to be on the same client as the sign in,
		// requestingClientID should be non-empty
		if userSettings.AttackProtection.EmailLink.RequireSameClient && requestingClientID == "" {
			return nil, nil, apierror.SignInEmailLinkNotSameClient()
		}

		signIn, err := s.signInRepo.QueryByIDAndInstance(ctx, s.db, claims.SourceID, claims.InstanceID)
		if err != nil {
			return nil, nil, apierror.Unexpected(err)
		}
		if signIn == nil {
			return nil, nil, apierror.SignInNotFound(claims.SourceID)
		}
		if signIn.CreatedSessionID.Valid {
			// sign in is complete, return the session associated with current sign in
			// The SignIn.CreatedSessionID references a session associated with the same client.
			// For a more detailed analysis on how different flows are handling this, see the PR description: https://github.com/clerk/clerk_go/pull/5062
			cdsSession, err := s.clientDataService.FindSession(ctx, claims.InstanceID, signIn.ClientID, signIn.CreatedSessionID.String)
			if err != nil {
				return nil, nil, apierror.Unexpected(err)
			}
			return cdsSession.ToSessionModel(), nil, nil
		}
		if signIn.FirstFactorSuccessVerificationID.Valid {
			// first factor of current sign in is already verified, we're good to go
			return nil, nil, nil
		}
		if !signIn.FirstFactorCurrentVerificationID.Valid {
			// there is no pending verification for sign in first factor, data integrity issue
			return nil, nil, apierror.Unexpected(fmt.Errorf("verifyToken: sign in %s does not have a pending verification",
				signIn.ID))
		}
		if !signIn.IdentificationID.Valid {
			return nil, nil, apierror.Unexpected(fmt.Errorf("verifyToken: sign in %s does not have an identification",
				signIn.ID))
		}

		cdsSignInClient, err := s.clientDataService.FindClient(ctx, env.Instance.ID, signIn.ClientID)
		if err != nil {
			return nil, nil, apierror.Unexpected(err)
		}

		signInClient := cdsSignInClient.ToClientModel()

		isSameClient := requestingClientID == signIn.ClientID

		if userSettings.AttackProtection.EmailLink.RequireSameClient && !isSameClient {
			return nil, nil, apierror.SignInEmailLinkNotSameClient()
		}

		var attemptor strategies.Attemptor
		txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
			attemptor = strategies.NewEmailLinkAttemptor(claims.VerificationID, claims.InstanceID, s.clock)
			if _, err := strategies.AttemptVerification(ctx, tx, attemptor, s.verificationRepo, requestingClientID); err != nil {
				return true, err
			}
			return false, nil
		})
		// Ignore verification already verified error
		if txErr != nil {
			if errors.Is(txErr, strategies.ErrAlreadyVerified) {
				return nil, nil, nil
			} else if attemptor != nil {
				return nil, nil, attemptor.ToAPIError(txErr)
			}
			return nil, nil, apierror.Unexpected(txErr)
		}

		identification, err := s.identificationRepo.FindByID(ctx, s.db, signIn.IdentificationID.String)
		if err != nil {
			return nil, nil, apierror.Unexpected(err)
		}

		user, err := s.userRepo.FindByID(ctx, s.db, identification.UserID.String)
		if err != nil {
			return nil, nil, apierror.Unexpected(err)
		}

		txErr = s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
			if err = s.signInService.ResetReVerificationState(ctx, tx, signIn); err != nil {
				return true, err
			}

			if err = s.signInService.LinkIdentificationToUser(ctx, tx, signIn, user.ID); err != nil {
				return true, err
			}

			if err := s.signInService.AttachFirstFactorVerification(ctx, tx, signIn, claims.VerificationID, true); err != nil {
				return true, err
			}

			readyToConvert, err := s.signInService.IsReadyToConvert(
				ctx,
				tx,
				signIn,
				usersettings.NewUserSettings(env.AuthConfig.UserSettings),
			)
			if err != nil {
				return true, err
			}

			if readyToConvert {
				if newSession, err = s.signInService.ConvertToSession(
					ctx,
					tx,
					sign_in.ConvertToSessionParams{
						Client:               signInClient,
						Env:                  env,
						SignIn:               signIn,
						User:                 user,
						PostponeCookieUpdate: !isSameClient,
					},
				); err != nil {
					return true, err
				}
			}

			return false, nil
		})
		if txErr != nil {
			if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
				return nil, nil, apiErr
			}
			return nil, nil, apierror.Unexpected(txErr)
		}
		if newSession != nil {
			if err := s.sessionService.Activate(ctx, env.Instance, newSession); err != nil {
				return nil, nil, apierror.Unexpected(err)
			}
		}

		if isSameClient && newSession != nil {
			newClient = signInClient
		}

	case constants.OSTUser:
		ident, err := s.identificationRepo.FindByID(ctx, s.db, claims.SourceID)
		if err != nil {
			return nil, nil, apierror.VerificationMissing()
		}

		var attemptor strategies.Attemptor
		txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
			attemptor = strategies.NewEmailLinkAttemptor(claims.VerificationID, claims.InstanceID, s.clock)
			if _, err := strategies.AttemptVerification(ctx, tx, attemptor, s.verificationRepo, requestingClientID); err != nil {
				return true, err
			}
			return false, nil
		})
		// Ignore verification already verified error
		if txErr != nil {
			if errors.Is(txErr, strategies.ErrAlreadyVerified) {
				return nil, nil, nil
			} else if attemptor != nil {
				return nil, nil, attemptor.ToAPIError(txErr)
			}
			return nil, nil, apierror.Unexpected(txErr)
		}

		txErr = s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
			err = s.identificationService.FinalizeVerification(ctx, tx, ident, env.Instance, userSettings)
			return err != nil, err
		})
		if txErr != nil {
			if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
				return nil, nil, apiErr
			}
			if errors.Is(txErr, identifications.ErrIdentifierAlreadyExists) {
				return nil, nil, apierror.IdentificationExists(ident.Type, nil)
			}
			return nil, nil, apierror.Unexpected(txErr)
		}
		return nil, nil, nil
	default:
		return nil, nil, apierror.VerificationInvalidLinkTokenSource()
	}

	return newSession, newClient, nil
}
