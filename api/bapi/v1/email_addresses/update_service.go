package email_addresses

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
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
	Verified *bool `json:"verified" form:"verified"`
	Primary  *bool `json:"primary" form:"primary"`
}

// Update
func (s *Service) Update(ctx context.Context, emailAddressID string, params UpdateParams) (*serialize.EmailAddressResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	emailAddress, apiErr := s.getEmailAddressInInstance(ctx, env.Instance.ID, emailAddressID)
	if apiErr != nil {
		return nil, apiErr
	}

	user, err := s.userRepo.FindByID(ctx, s.db, emailAddress.UserID.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// validate all form elements separately.
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	if apiErr = s.validateUpdateParams(ctx, s.db, userSettings, user, emailAddress, params); apiErr != nil {
		return nil, apiErr
	}

	var emailAddressResponse *serialize.EmailAddressResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		updatedUser, updatedIdent, performedUpdate, err := s.updateEmailAddress(ctx, tx, user, emailAddress, params)
		if err != nil {
			return true, err
		}

		// send event if something changed
		if performedUpdate {
			if err := s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, updatedUser); err != nil {
				return true, fmt.Errorf("user/update: send user updated event for (%+v, %+v): %w", updatedUser, env.Instance.ID, err)
			}
		}

		emailAddressResponse = serialize.IdentificationEmailAddress(updatedIdent)

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return emailAddressResponse, nil
}

func (s *Service) validateUpdateParams(
	ctx context.Context,
	exec database.Executor,
	userSettings *usersettings.UserSettings,
	user *model.User,
	emailAddress *model.Identification,
	params UpdateParams,
) apierror.Error {
	// can't set primary false, can only set other email addresses `true`
	if params.Primary != nil && !*params.Primary {
		return apierror.FormInvalidParameterValueWithAllowed(param.Primary.Name, "false", []string{"true"})
	}

	userIdentifications, err := s.identificationRepo.FindAllNonOAuthByUser(ctx, exec, user.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	parentIdentifications, err := s.identificationRepo.FindAllByTargetIdentificationID(ctx, exec, emailAddress.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	allVerified := 0
	emailsVerified := 0
	for _, ident := range userIdentifications {
		if ident.IsVerified() {
			if ident.IsEmailAddress() {
				emailsVerified++
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

	// throw error if the email_address can't be set unverified because:
	//
	// - it's the only verified email address and email addresses are required and verified during sign up
	// - OR -
	// - at least one verified identification is required, and it's the only verified identification
	if emailsVerified == 1 && requiredToBeVerifiedAttributes.Contains(string(names.EmailAddress)) {
		return apierror.BreaksInstanceInvariant("Your users must have at least one verified email address.")
	} else if allVerified == 1 && !requiredToBeVerifiedAttributes.IsEmpty() {
		return apierror.BreaksInstanceInvariant("Your users must have at least one verified identification.")
	}

	return nil
}

// this method doesn't do any validation, and assumes all data passed in
// will create a complete, valid, email address for the given instance.
func (s *Service) updateEmailAddress(
	ctx context.Context,
	tx database.Tx,
	user *model.User,
	emailAddress *model.Identification,
	params UpdateParams,
) (*model.User, *model.IdentificationSerializable, bool, error) {
	performedUpdate := false
	if params.Primary != nil && user.PrimaryEmailAddressID.Valid && user.PrimaryEmailAddressID.String != emailAddress.ID {
		user.PrimaryEmailAddressID = null.StringFrom(emailAddress.ID)
		if err := s.userRepo.UpdatePrimaryEmailAddressID(ctx, tx, user); err != nil {
			return nil, nil, false, err
		}

		performedUpdate = true
	}

	// if verification param doesn't match current status, create or delete a verification obj
	if params.Verified != nil {
		if !(*params.Verified) && emailAddress.IsVerified() {
			emailAddress.VerificationID = null.StringFromPtr(nil)
			emailAddress.Status = constants.ISNotSet
			updateCols := set.New[string](
				sqbmodel.IdentificationColumns.VerificationID,
				sqbmodel.IdentificationColumns.Status,
			)

			userIdents, err := s.identificationRepo.FindAllNonOAuthByUser(ctx, tx, user.ID)
			if err != nil {
				return nil, nil, false, err
			}
			if len(userIdents) == 1 {
				emailAddress.Status = constants.ISReserved
				updateCols.Insert(sqbmodel.IdentificationColumns.Status)
			}

			if err = s.identificationRepo.Update(ctx, tx, emailAddress, updateCols.Array()...); err != nil {
				return nil, nil, false, err
			}

			if err := s.verificationRepo.Delete(ctx, tx, emailAddress.VerificationID.String); err != nil {
				return nil, nil, false, err
			}
			performedUpdate = true
		} else if (*params.Verified) && !emailAddress.IsVerified() {
			newVer := &model.Verification{Verification: &sqbmodel.Verification{
				InstanceID:       emailAddress.InstanceID,
				Strategy:         constants.VSAdmin,
				IdentificationID: null.StringFrom(emailAddress.ID),
				Attempts:         0,
			}}

			if err := s.verificationRepo.Insert(ctx, tx, newVer); err != nil {
				return nil, nil, false, err
			}

			emailAddress.VerificationID = null.StringFrom(newVer.ID)
			emailAddress.Status = constants.ISVerified
			err := s.identificationRepo.Update(ctx, tx, emailAddress,
				sqbmodel.IdentificationColumns.VerificationID,
				sqbmodel.IdentificationColumns.Status)
			if err != nil {
				return nil, nil, false, err
			}

			performedUpdate = true
		}
	}

	emailAddressSerializable, err := s.serializableService.ConvertIdentification(ctx, tx, emailAddress)
	if err != nil {
		return nil, nil, false, err
	}

	return user, emailAddressSerializable, performedUpdate, nil
}
