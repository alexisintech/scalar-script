package phone_numbers

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/rand"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/volatiletech/null/v8"
)

type CreateParams struct {
	UserID                  string `json:"user_id" form:"user_id" validate:"required"`
	PhoneNumber             string `json:"phone_number" form:"phone_number" validate:"required"`
	Verified                *bool  `json:"verified" form:"verified"`
	Primary                 *bool  `json:"primary" form:"primary"`
	ReservedForSecondFactor *bool  `json:"reserved_for_second_factor" form:"reserved_for_second_factor"`
}

// Create
func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.PhoneNumberResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	// validate data works for supplied env.
	if !userSettings.GetAttribute(names.PhoneNumber).Base().Enabled {
		return nil, apierror.RequestInvalidForInstance()
	}

	// validate user exists in the correct instance.
	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, params.UserID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(params.UserID)
	}

	// validate all form elements separately.
	if apiErr := s.validateCreateParams(ctx, s.db, env.Instance, userSettings, user, params); apiErr != nil {
		return nil, apiErr
	}

	var phoneNumberResponse *serialize.PhoneNumberResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		newIdentification, apiErr := s.createPhoneNumber(ctx, tx, user, params)
		if err != nil {
			return true, apiErr
		}

		// send event
		if err := s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, user); err != nil {
			return true, fmt.Errorf("user/update: send user updated event for (%+v, %+v): %w", user, env.Instance.ID, err)
		}

		phoneNumberSerializable, err := s.serializableService.ConvertIdentification(ctx, tx, newIdentification)
		if err != nil {
			return true, fmt.Errorf("user/update: serializing identification %+v: %w", newIdentification, err)
		}

		phoneNumberResponse = serialize.IdentificationPhoneNumber(phoneNumberSerializable)

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return phoneNumberResponse, nil
}

func (s *Service) validateCreateParams(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User,
	params CreateParams,
) apierror.Error {
	if err := s.validator.Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}

	var formErrs apierror.Error

	phoneErr, err := s.validatorService.ValidatePhoneNumber(
		ctx,
		exec,
		params.PhoneNumber,
		instance.ID,
		&user.ID,
		!userSettings.GetAttribute(names.PhoneNumber).Base().VerifyAtSignUp,
		param.PhoneNumber.Name,
	)
	if err != nil {
		return apierror.Unexpected(err)
	} else if phoneErr != nil {
		formErrs = apierror.Combine(formErrs, phoneErr)
	}

	// check if the new phone number can't be made primary, because:
	// - it is not verified
	// - there's at least one other verified phone number.
	if params.Primary != nil && *params.Primary {
		if params.Verified == nil || (params.Verified != nil && !*params.Verified) {
			hasVerifiedPhone, err := s.userProfileService.HasVerifiedPhone(ctx, exec, user.ID)
			if err != nil {
				return apierror.Unexpected(err)
			}

			if hasVerifiedPhone {
				invariantErr := apierror.BreaksInstanceInvariant("You can't set an unverified phone number as a user's primary phone number.")
				formErrs = apierror.Combine(formErrs, invariantErr)
			}
		}
	}

	// check if the new phone number can't be a second factor because it isn't verified
	if params.ReservedForSecondFactor != nil && *params.ReservedForSecondFactor {
		if params.Verified == nil || (params.Verified != nil && !*params.Verified) {
			formErrs = apierror.Combine(formErrs, apierror.CreateSecondFactorUnverified())
		}
	}

	return formErrs
}

// this method doesn't do any validation, and assumes all data passed in
// will create a complete, valid, phone number for the given instance.
func (s *Service) createPhoneNumber(
	ctx context.Context,
	tx database.Tx,
	user *model.User,
	params CreateParams,
) (*model.Identification, apierror.Error) {
	verified := false
	if params.Verified != nil {
		verified = *params.Verified
	}

	primary := false
	if params.Primary != nil {
		primary = *params.Primary
	}

	reservedForSecondFactor := false
	if params.ReservedForSecondFactor != nil {
		reservedForSecondFactor = *params.ReservedForSecondFactor
	}

	defaultSecondFactor := false
	if reservedForSecondFactor {
		currentDefaultSecondFactor, err := s.identificationRepo.QueryDefaultSecondFactorByUser(ctx, tx, user.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		// set phone number as default second factor if there is currently none
		if currentDefaultSecondFactor == nil {
			defaultSecondFactor = true
		}
	}

	// insert identification and verification
	var wrappedVer *model.VerificationWithStatus
	if verified {
		newVer := &model.Verification{Verification: &sqbmodel.Verification{
			ID:         rand.InternalClerkID(constants.IDPVerification),
			InstanceID: user.InstanceID,
			Strategy:   constants.VSAdmin,
			Attempts:   0,
		}}

		wrappedVer = &model.VerificationWithStatus{
			Verification: newVer,
			Status:       constants.VERVerified,
		}
	}

	ident := &model.Identification{Identification: &sqbmodel.Identification{
		InstanceID:              user.InstanceID,
		UserID:                  null.StringFrom(user.ID),
		Type:                    constants.ITPhoneNumber,
		Identifier:              null.StringFrom(params.PhoneNumber),
		ReservedForSecondFactor: reservedForSecondFactor,
		DefaultSecondFactor:     defaultSecondFactor,
		Status:                  constants.ISNotSet,
	}}

	if err := s.identificationRepo.Insert(ctx, tx, ident); err != nil {
		return nil, apierror.Unexpected(err)
	}

	if wrappedVer != nil {
		wrappedVer.IdentificationID = null.StringFrom(ident.ID)
		if err := s.verificationRepo.Insert(ctx, tx, wrappedVer.Verification); err != nil {
			return nil, apierror.Unexpected(err)
		}

		ident.VerificationID = null.StringFrom(wrappedVer.ID)
		ident.Status = constants.ISVerified
		err := s.identificationRepo.Update(ctx, tx, ident,
			sqbmodel.IdentificationColumns.VerificationID,
			sqbmodel.IdentificationColumns.Status)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	// update user
	if primary {
		user.PrimaryPhoneNumberID = null.StringFrom(ident.ID)
		if err := s.userRepo.UpdatePrimaryPhoneNumberID(ctx, tx, user); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	return ident, nil
}
