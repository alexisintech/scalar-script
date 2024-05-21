package phone_numbers

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/phone_numbers"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/set"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/volatiletech/null/v8"
)

type UpdateParams struct {
	Verified                *bool `json:"verified" form:"verified"`
	Primary                 *bool `json:"primary" form:"primary"`
	ReservedForSecondFactor *bool `json:"reserved_for_second_factor" form:"reserved_for_second_factor"`
}

// Update
func (s *Service) Update(ctx context.Context, phoneNumberID string, params UpdateParams) (*serialize.PhoneNumberResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	phoneNumber, apiErr := s.getAndCheckPhoneNumber(ctx, env.Instance.ID, phoneNumberID)
	if apiErr != nil {
		return nil, apiErr
	}

	user, err := s.userRepo.FindByID(ctx, s.db, phoneNumber.UserID.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// validate all form elements separately.
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	if apiErr = s.validateUpdateParams(ctx, s.db, userSettings, user, phoneNumber, params); apiErr != nil {
		return nil, apiErr
	}

	var phoneNumberResponse *serialize.PhoneNumberResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		updatedUser, updatedIdent, performedUpdate, err := s.updatePhoneNumber(ctx, tx, user, phoneNumber, params)
		if err != nil {
			return true, err
		}

		// send event if something changed
		if performedUpdate {
			if err := s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, updatedUser); err != nil {
				return true, fmt.Errorf("user/update: send user updated event for (%+v, %+v): %w", updatedUser, env.Instance.ID, err)
			}
		}

		phoneNumberResponse = serialize.IdentificationPhoneNumber(updatedIdent)

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

func (s *Service) validateUpdateParams(
	ctx context.Context,
	exec database.Executor,
	userSettings *usersettings.UserSettings,
	user *model.User,
	phoneNumber *model.Identification,
	params UpdateParams,
) apierror.Error {
	// can't set primary false, can only set other phone numbers `true`
	if params.Primary != nil && !*params.Primary {
		return apierror.FormInvalidParameterValueWithAllowed(param.Primary.Name, "false", []string{"true"})
	}

	userIdentifications, err := s.identificationRepo.FindAllNonOAuthByUser(ctx, exec, user.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	parentIdentifications, err := s.identificationRepo.FindAllByTargetIdentificationID(ctx, exec, phoneNumber.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	allVerified := 0
	phonesVerified := 0
	for _, ident := range userIdentifications {
		if ident.IsVerified() {
			if ident.IsPhoneNumber() {
				phonesVerified++
			}

			allVerified++
		}
	}

	// No more checks needed if the email address is updated as verified
	if params.Verified != nil && *params.Verified {
		return nil
	}

	// Can't set as unverified, if there are parent identifications
	if len(parentIdentifications) > 0 {
		return apierror.BreaksInstanceInvariant("You can't unverify an identification if it has a parent.")
	}

	requiredToBeVerifiedAttributes := set.New[string]()
	for _, attribute := range userSettings.AllAttributes() {
		if attribute.Base().Required && attribute.Base().VerifyAtSignUp {
			requiredToBeVerifiedAttributes.Insert(attribute.Name())
		}
	}

	// throw error if the phone_number can't be set unverified because:
	//
	// - it's the only verified phone number and phone numbers are required and verified during sign up
	// - OR -
	// - at least one verified identification is required, and it's the only verified identification
	if phonesVerified == 1 && requiredToBeVerifiedAttributes.Contains(string(names.PhoneNumber)) {
		return apierror.BreaksInstanceInvariant("Your users must have at least one verified phone number.")
	} else if allVerified == 1 && !requiredToBeVerifiedAttributes.IsEmpty() {
		return apierror.BreaksInstanceInvariant("Your users must have at least one verified identification.")
	}

	return nil
}

// this method doesn't do any validation, and assumes all data passed in
// will create a complete, valid, phone number for the given instance.
func (s *Service) updatePhoneNumber(
	ctx context.Context,
	tx database.Tx,
	user *model.User,
	phoneNumber *model.Identification,
	params UpdateParams,
) (*model.User, *model.IdentificationSerializable, bool, error) {
	performedUpdate := false
	if params.Primary != nil && user.PrimaryPhoneNumberID.Valid && user.PrimaryPhoneNumberID.String != phoneNumber.ID {
		user.PrimaryPhoneNumberID = null.StringFrom(phoneNumber.ID)
		if err := s.userRepo.UpdatePrimaryPhoneNumberID(ctx, tx, user); err != nil {
			return nil, nil, false, err
		}

		performedUpdate = true
	}

	// if verification param doesn't match current status, create or delete a verification obj
	if params.Verified != nil {
		if !(*params.Verified) && phoneNumber.IsVerified() {
			phoneNumber.VerificationID = null.StringFromPtr(nil)
			phoneNumber.Status = constants.ISNotSet
			updateCols := set.New[string](
				sqbmodel.IdentificationColumns.VerificationID,
				sqbmodel.IdentificationColumns.Status,
			)

			userIdents, err := s.identificationRepo.FindAllNonOAuthByUser(ctx, tx, user.ID)
			if err != nil {
				return nil, nil, false, err
			}
			if len(userIdents) == 1 {
				phoneNumber.Status = constants.ISReserved
				updateCols.Insert(sqbmodel.IdentificationColumns.Status)
			}

			if err = s.identificationRepo.Update(ctx, tx, phoneNumber, updateCols.Array()...); err != nil {
				return nil, nil, false, err
			}

			if err := s.verificationRepo.Delete(ctx, tx, phoneNumber.VerificationID.String); err != nil {
				return nil, nil, false, err
			}
			performedUpdate = true
		} else if (*params.Verified) && !phoneNumber.IsVerified() {
			newVer := &model.Verification{Verification: &sqbmodel.Verification{
				InstanceID:       phoneNumber.InstanceID,
				Strategy:         constants.VSAdmin,
				IdentificationID: null.StringFrom(phoneNumber.ID),
				Attempts:         0,
			}}

			if err := s.verificationRepo.Insert(ctx, tx, newVer); err != nil {
				return nil, nil, false, err
			}

			phoneNumber.VerificationID = null.StringFrom(newVer.ID)
			phoneNumber.Status = constants.ISVerified
			err := s.identificationRepo.Update(ctx, tx, phoneNumber,
				sqbmodel.IdentificationColumns.VerificationID,
				sqbmodel.IdentificationColumns.Status)
			if err != nil {
				return nil, nil, false, err
			}

			performedUpdate = true
		}
	}

	var apiErr apierror.Error
	if params.ReservedForSecondFactor != nil {
		updateForMFAForm := phone_numbers.UpdateForMFAForm{
			ReservedForSecondFactor: params.ReservedForSecondFactor,
		}
		phoneNumber, _, performedUpdate, apiErr = s.phoneNumbersService.UpdateForMFA(ctx, tx, user, phoneNumber.ID, &updateForMFAForm)
		if apiErr != nil {
			return nil, nil, false, apiErr
		}
	}

	phoneNumberSerializable, err := s.serializableService.ConvertIdentification(ctx, tx, phoneNumber)
	if err != nil {
		return nil, nil, false, err
	}

	return user, phoneNumberSerializable, performedUpdate, nil
}
