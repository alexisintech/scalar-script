package phone_numbers

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/shared/events"
	"clerk/api/shared/identifications"
	"clerk/api/shared/serializable"
	"clerk/api/shared/user_profile"
	"clerk/api/shared/users"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/backup_codes"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/vgarvardt/gue/v2"
)

type Service struct {
	gueClient *gue.Client

	eventService          *events.Service
	identificationService *identifications.Service
	serializableService   *serializable.Service
	usersService          *users.Service
	userProfileService    *user_profile.Service

	backupCodesRepo     *repository.BackupCode
	identificationsRepo *repository.Identification
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		gueClient:             deps.GueClient(),
		eventService:          events.NewService(deps),
		identificationService: identifications.NewService(deps),
		serializableService:   serializable.NewService(deps.Clock()),
		usersService:          users.NewService(deps),
		userProfileService:    user_profile.NewService(deps.Clock()),
		backupCodesRepo:       repository.NewBackupCode(),
		identificationsRepo:   repository.NewIdentification(),
	}
}

type UpdateForMFAForm struct {
	ReservedForSecondFactor *bool
	DefaultSecondFactor     *bool
}

func (f UpdateForMFAForm) isUpdatingSecondFactor() bool {
	return f.DefaultSecondFactor != nil || f.ReservedForSecondFactor != nil
}

// UpdateForMFA updates a phone number's MFA related properties
func (s *Service) UpdateForMFA(ctx context.Context, tx database.Tx, user *model.User, phoneNumberID string, updateForm *UpdateForMFAForm) (*model.Identification, []string, bool, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	phoneNumber, apiErr := s.fetchIdentification(ctx, tx, phoneNumberID, env.Instance.ID, user.ID)
	if apiErr != nil {
		return nil, nil, false, apiErr
	}

	if formErrs := s.validateUpdateForMFAForm(updateForm); formErrs != nil {
		return nil, nil, false, formErrs
	}

	// check for other errors
	if updateForm.isUpdatingSecondFactor() && !phoneNumber.IsVerified() {
		// phone must be verified before updating 2FA attributes.
		return nil, nil, false, apierror.UpdateSecondFactorUnverified()
	}

	if updateForm.ReservedForSecondFactor != nil && *updateForm.ReservedForSecondFactor {
		// check if this is the last identification that can be used for user authentication
		identifications, err := s.identificationService.CanBeUsedForUserAuthentication(ctx, tx, phoneNumber.UserID.String, userSettings)
		if err != nil {
			return nil, nil, false, apierror.Unexpected(fmt.Errorf("updatePhoneNumber: updating phone number %s with %+v: %w",
				phoneNumber.ID, updateForm, err))
		}
		if len(identifications) == 1 && identifications[0].ID == phoneNumber.ID {
			return nil, nil, false, apierror.LastIdentificationSetFor2FAFailed()
		}
	}

	updateCols := make([]string, 0)

	currentDefaultSecondFactor, err := s.identificationsRepo.QueryDefaultSecondFactorByUser(ctx, tx, user.ID)
	if err != nil {
		return nil, nil, false, apierror.Unexpected(err)
	}

	if updateForm.DefaultSecondFactor != nil {
		if currentDefaultSecondFactor != nil {
			currentDefaultSecondFactor.DefaultSecondFactor = false
		}

		phoneNumber.DefaultSecondFactor = *updateForm.DefaultSecondFactor
		updateCols = append(updateCols, sqbmodel.IdentificationColumns.DefaultSecondFactor)
	}

	var newDefaultSecondFactor bool
	var initialSecondFactor bool
	if updateForm.ReservedForSecondFactor != nil {
		phoneNumber.ReservedForSecondFactor = *updateForm.ReservedForSecondFactor
		updateCols = append(updateCols, sqbmodel.IdentificationColumns.ReservedForSecondFactor)

		// if it's the first second factor method for the user, make it the default one
		if *updateForm.ReservedForSecondFactor && currentDefaultSecondFactor == nil {
			initialSecondFactor = true

			phoneNumber.DefaultSecondFactor = true
			updateCols = append(updateCols, sqbmodel.IdentificationColumns.DefaultSecondFactor)
		}

		// if we're removing current phone number as second factor, check if it's the default one.
		// if it is, assign another one
		if !*updateForm.ReservedForSecondFactor && currentDefaultSecondFactor != nil && currentDefaultSecondFactor.ID == phoneNumber.ID {
			phoneNumber.DefaultSecondFactor = false
			updateCols = append(updateCols, sqbmodel.IdentificationColumns.DefaultSecondFactor)
			newDefaultSecondFactor = true
		}
	}

	var backupCodes []string
	performedUpdate := false
	var updateErr error
	if len(updateCols) > 0 {
		if currentDefaultSecondFactor != nil {
			if err := s.identificationsRepo.UpdateDefaultSecondFactor(ctx, tx, currentDefaultSecondFactor); err != nil {
				updateErr = fmt.Errorf("updatePhoneNumber: updating default second factor for %+v: %w", currentDefaultSecondFactor, err)
				return nil, nil, false, apierror.Unexpected(updateErr)
			}
		}

		if err := s.identificationsRepo.Update(ctx, tx, phoneNumber, updateCols...); err != nil {
			updateErr = fmt.Errorf("updatePhoneNumber: updating phone number %+v: %w", phoneNumber, err)
			return nil, nil, false, apierror.Unexpected(updateErr)
		}

		if newDefaultSecondFactor {
			err := s.makeNewDefaultSecondFactor(ctx, tx, user.ID)
			if err != nil {
				updateErr = fmt.Errorf("updatePhoneNumber: making new default second factor for user %s: %w", user.ID, err)
				return nil, nil, false, apierror.Unexpected(updateErr)
			}
		}

		if initialSecondFactor {
			backupCodes, err = s.finalizeMFAEnablement(ctx, tx, userSettings, user.ID, env.Instance.ID)
			if err != nil {
				return nil, nil, false, apierror.Unexpected(err)
			}
		}

		err := s.cleanupBackupCodes(ctx, tx, userSettings, user)
		if err != nil {
			return nil, nil, false, apierror.Unexpected(err)
		}

		performedUpdate = true
	}

	return phoneNumber, backupCodes, performedUpdate, nil
}

func (s *Service) fetchIdentification(ctx context.Context, tx database.Tx, identifierID, instanceID, userID string) (*model.Identification, apierror.Error) {
	identification, err := s.identificationsRepo.QueryByIDAndUser(ctx, tx, instanceID, identifierID, userID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if identification == nil {
		return nil, apierror.IdentificationNotFound(identifierID)
	}

	return identification, nil
}

func (s *Service) validateUpdateForMFAForm(updateForm *UpdateForMFAForm) apierror.Error {
	var formErrs apierror.Error

	if updateForm.DefaultSecondFactor != nil {
		if !*updateForm.DefaultSecondFactor {
			formErrs = apierror.Combine(formErrs, apierror.FormNotAllowedToDisableDefaultSecondFactor(param.DefaultSecondFactor.Name))
		}
	}

	return formErrs
}

// makeNewDefaultSecondFactor selects a second factor method for the given user and makes it as the default one
func (s *Service) makeNewDefaultSecondFactor(ctx context.Context, exec database.Executor, userID string) error {
	secondFactorIdentification, err := s.identificationsRepo.QuerySecondFactorByUser(ctx, exec, userID)
	if err != nil {
		return fmt.Errorf("makeNewDefaultSecondFactor: fetching one second factor identification for user %s: %w",
			userID, err)
	}
	if secondFactorIdentification == nil {
		return nil
	}

	secondFactorIdentification.DefaultSecondFactor = true
	err = s.identificationsRepo.UpdateDefaultSecondFactor(ctx, exec, secondFactorIdentification)
	if err != nil {
		return fmt.Errorf("makeNewDefaultSecondFactor: making %+v the new default second factor for user %s: %w",
			secondFactorIdentification, userID, err)
	}

	return nil
}

func (s *Service) finalizeMFAEnablement(ctx context.Context, tx database.Tx, userSettings *usersettings.UserSettings, userID, instanceID string) ([]string, error) {
	if !userSettings.GetAttribute(names.BackupCode).Base().Enabled {
		return nil, nil
	}

	backupCodesAlreadyExists, err := s.backupCodesRepo.ExistsByUser(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if backupCodesAlreadyExists {
		return nil, nil
	}

	_, plainCodes, err := s.createBackupCodes(ctx, tx, userID, instanceID)
	if err != nil {
		return nil, err
	}

	return plainCodes, nil
}

func (s *Service) createBackupCodes(ctx context.Context, tx database.Tx, userID, instanceID string) (*model.BackupCode, []string, error) {
	plainCodes, hashedCodes, err := backup_codes.GenerateAndHash()
	if err != nil {
		return nil, nil, err
	}

	newBackupCode := &model.BackupCode{BackupCode: &sqbmodel.BackupCode{
		InstanceID: instanceID,
		UserID:     userID,
		Codes:      hashedCodes,
	}}
	if err = s.backupCodesRepo.Upsert(ctx, tx, newBackupCode); err != nil {
		return nil, nil, err
	}

	return newBackupCode, plainCodes, nil
}

func (s *Service) cleanupBackupCodes(ctx context.Context, tx database.Executor, userSettings *usersettings.UserSettings, user *model.User) error {
	enabled, err := s.userProfileService.HasTwoFactorEnabled(ctx, tx, userSettings, user.ID)
	if err != nil {
		return err
	}

	if !enabled {
		err = s.backupCodesRepo.DeleteByUser(ctx, tx, user.ID)
		if err != nil {
			return err
		}
	}

	return nil
}
