package sign_ups

import (
	"context"
	"errors"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	"clerk/api/shared/sessions"
	"clerk/api/shared/sign_up"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	clerkjson "clerk/pkg/json"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/volatiletech/null/v8"

	"github.com/jonboulle/clockwork"
)

// Service contains the business logic for phone_number operations in the Backend API.
type Service struct {
	clock clockwork.Clock
	db    database.Database

	// services
	signUpService     *sign_up.Service
	clientDataService client_data.Service
	sessionService    *sessions.Service

	// repositories
	signUpRepo *repository.SignUp
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:             deps.Clock(),
		db:                deps.DB(),
		clientDataService: *client_data.NewService(deps),
		sessionService:    sessions.NewService(deps),
		signUpService:     sign_up.NewService(deps),
		signUpRepo:        repository.NewSignUp(),
	}
}

// CheckSignUpInInstance checks whether the given sign up belongs to the current instance and returns an error if it doesn't
func (s *Service) CheckSignUpInInstance(ctx context.Context, signUpID string, instanceID string) apierror.Error {
	user, err := s.signUpRepo.QueryByIDAndInstance(ctx, s.db, signUpID, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	} else if user == nil {
		return apierror.ResourceNotFound()
	}

	return nil
}

func (s *Service) Read(ctx context.Context, userSettings *usersettings.UserSettings, signUpID string) (*serialize.SignUpResponse, apierror.Error) {
	signUp, err := s.signUpRepo.FindByID(ctx, s.db, signUpID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	signUpSerializable, err := s.signUpService.ConvertToSerializable(ctx, s.db, signUp, userSettings, "")
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	signUpResponse, err := serialize.SignUp(ctx, s.clock, signUpSerializable)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return signUpResponse, nil
}

type UpdateParams struct {
	CustomAction *bool            `json:"custom_action" form:"custom_action"`
	ExternalID   clerkjson.String `json:"external_id" form:"external_id"`
}

func (s *Service) Update(ctx context.Context, signUpID string, params *UpdateParams) (*serialize.SignUpResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	signUp, err := s.signUpRepo.FindByID(ctx, s.db, signUpID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if signUp.Status(s.clock) != constants.SignUpMissingRequirements {
		return nil, apierror.SignUpCannotBeUpdated()
	}

	whitelistCols := make([]string, 0)

	if params.CustomAction != nil && signUp.CustomAction != *params.CustomAction {
		whitelistCols = append(whitelistCols, sqbmodel.SignUpColumns.CustomAction)
		signUp.CustomAction = *params.CustomAction
	}

	if params.ExternalID.IsSet {
		signUp.ExternalID = null.StringFromPtr(params.ExternalID.Ptr())
	}

	client, err := s.clientDataService.FindClient(ctx, env.Instance.ID, signUp.ClientID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	compatibleClient := client.ToClientModel()

	var session *model.Session
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err := s.signUpRepo.Update(ctx, tx, signUp, whitelistCols...); err != nil {
			return true, apierror.Unexpected(err)
		}

		// finalize flow if needed
		session, err = s.signUpService.FinalizeFlow(ctx, tx, sign_up.FinalizeFlowParams{
			SignUp:               signUp,
			Env:                  env,
			Client:               compatibleClient,
			UserSettings:         usersettings.NewUserSettings(env.AuthConfig.UserSettings),
			PostponeCookieUpdate: true,
		},
		)
		if errors.Is(err, clerkerrors.ErrIdentificationClaimed) {
			return true, apierror.IdentificationClaimed()
		} else if err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, apiErr
		} else if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueReservedIdentification) {
			return nil, apierror.IdentificationClaimed()
		}

		return nil, apierror.Unexpected(txErr)
	}

	signUpSerializable, err := s.signUpService.ConvertToSerializable(ctx, s.db, signUp, userSettings, "")
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	signUpResponse, err := serialize.SignUp(ctx, s.clock, signUpSerializable)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if session != nil {
		if err := s.sessionService.Activate(ctx, env.Instance, session); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	return signUpResponse, nil
}
