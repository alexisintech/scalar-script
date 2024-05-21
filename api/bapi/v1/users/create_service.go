package users

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/users"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/backup_codes"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/hash"
	"clerk/pkg/metadata"
	"clerk/pkg/phonenumber"
	"clerk/pkg/set"
	"clerk/pkg/time"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/types"
)

type CreateParams struct {
	ExternalID              *string          `json:"external_id" form:"external_id"`
	EmailAddresses          []string         `json:"email_address" form:"email_address"`
	PhoneNumbers            []string         `json:"phone_number" form:"phone_number"`
	Web3Wallets             []string         `json:"web3_wallet" form:"web3_wallet"`
	Username                *string          `json:"username" form:"username"`
	Password                *string          `json:"password" form:"password"`
	FirstName               *string          `json:"first_name" form:"first_name"`
	LastName                *string          `json:"last_name" form:"last_name"`
	UnsafeMetadata          *json.RawMessage `json:"unsafe_metadata" form:"unsafe_metadata"`
	PublicMetadata          *json.RawMessage `json:"public_metadata" form:"public_metadata"`
	PrivateMetadata         *json.RawMessage `json:"private_metadata" form:"private_metadata"`
	SkipPasswordRequirement *bool            `json:"skip_password_requirement" form:"skip_password_requirement"`
	SkipPasswordChecks      *bool            `json:"skip_password_checks" form:"skip_password_checks"`
	PasswordDigest          *string          `json:"password_digest" form:"password_digest"`
	PasswordHasher          *string          `json:"password_hasher" form:"password_hasher"`
	TOTPSecret              *string          `json:"totp_secret" form:"totp_secret"`

	// Should be either bcrypt-hashed or plaintext.
	BackupCodes []string `json:"backup_codes" form:"backup_codes"`

	// Specified in RFC3339 format
	CreatedAt *string `json:"created_at" form:"created_at"`
}

func (p CreateParams) toMetadata() metadata.Metadata {
	v := metadata.Metadata{}
	if p.PrivateMetadata != nil {
		v.Private = *p.PrivateMetadata
	}
	if p.PublicMetadata != nil {
		v.Public = *p.PublicMetadata
	}
	if p.UnsafeMetadata != nil {
		v.Unsafe = *p.UnsafeMetadata
	}
	return v
}

// Create
func (s *Service) Create(ctx context.Context, params CreateParams) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	var sanitizedEmailAddresses []string
	for _, emailAddress := range params.EmailAddresses {
		sanitizedEmailAddress := strings.TrimSpace(strings.ToLower(emailAddress))
		sanitizedEmailAddresses = append(sanitizedEmailAddresses, sanitizedEmailAddress)
	}
	params.EmailAddresses = sanitizedEmailAddresses

	var sanitizedPhoneNumbers []string
	for _, phoneNum := range params.PhoneNumbers {
		sanitizedPhoneNumber, err := phonenumber.Sanitize(strings.TrimSpace(phoneNum))
		if err != nil {
			return nil, apierror.FormInvalidPhoneNumber(param.PhoneNumber.Name)
		}
		sanitizedPhoneNumbers = append(sanitizedPhoneNumbers, sanitizedPhoneNumber)
	}
	params.PhoneNumbers = sanitizedPhoneNumbers

	var sanitizedWeb3Wallets []string
	for _, web3Wallet := range params.Web3Wallets {
		sanitizedWeb3Wallet := strings.TrimSpace(web3Wallet)
		sanitizedWeb3Wallets = append(sanitizedWeb3Wallets, sanitizedWeb3Wallet)
	}
	params.Web3Wallets = sanitizedWeb3Wallets

	if params.Username != nil {
		*params.Username = strings.ToLower(*params.Username)
	}

	// validate all form elements separately.
	if apiErr := s.validateCreateParams(ctx, env.Instance.ID, params, userSettings); apiErr != nil {
		return nil, apiErr
	}

	var userResponse *serialize.UserResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		iUser, err := s.createUser(ctx, tx, env, params)
		if errors.Is(err, hash.ErrPasswordTooLong) {
			return true, apierror.FormInvalidPasswordSizeInBytesExceeded(param.Password.Name)
		} else if err != nil {
			return true, err
		}

		userSerializable, err := s.serializableService.ConvertUser(ctx, tx, userSettings, iUser)
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		userResponse = serialize.UserToServerAPI(ctx, userSerializable)

		if err = s.eventService.UserCreated(ctx, tx, env.Instance, userSerializable); err != nil {
			return true, fmt.Errorf("user/update: send user updated event for (%+v, %+v): %w", iUser, env.Instance.ID, err)
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueExternalID) {
			return nil, apierror.FormIdentifierExists(param.ExternalID.Name)
		}
		return nil, apierror.Unexpected(txErr)
	}

	return userResponse, nil
}

func (s *Service) validateCreateParams(ctx context.Context, instanceID string, params CreateParams, userSettings *usersettings.UserSettings) apierror.Error {
	var apiErrs apierror.Error

	// email addresses
	// make sure passed in array is unique
	if set.New(params.EmailAddresses...).Count() != len(params.EmailAddresses) {
		apiErrs = apierror.Combine(apiErrs, apierror.FormDuplicateParameter(param.EmailAddress.Name))
	}

	// if email addresses are not enabled, don't allow users to have email addresses
	if len(params.EmailAddresses) > 0 && !userSettings.GetAttribute(names.EmailAddress).Base().Enabled {
		apiErrs = apierror.Combine(apiErrs, apierror.FormUnknownParameter(param.EmailAddress.Name))
	}

	emailAddressAttribute := userSettings.GetAttribute(names.EmailAddress)
	for _, emailAddress := range params.EmailAddresses {
		emailErr, err := s.validatorService.ValidateEmailAddress(ctx, s.db, emailAddress, instanceID, nil, !emailAddressAttribute.Base().VerifyAtSignUp, param.EmailAddress.Name)
		if err != nil {
			return apierror.Unexpected(err)
		}

		apiErrs = apierror.Combine(apiErrs, emailErr)
	}

	// phone numbers
	// make sure passed in array is unique
	if set.New(params.PhoneNumbers...).Count() != len(params.PhoneNumbers) {
		apiErrs = apierror.Combine(apiErrs, apierror.FormDuplicateParameter(param.PhoneNumber.Name))
	}

	// if phone numbers are not enabled, don't allow users to have phone numbers
	if len(params.PhoneNumbers) > 0 && !userSettings.GetAttribute(names.PhoneNumber).Base().Enabled {
		apiErrs = apierror.Combine(apiErrs, apierror.FormUnknownParameter(param.PhoneNumber.Name))
	}

	phoneNumberAttribute := userSettings.GetAttribute(names.PhoneNumber)
	for _, phoneNumber := range params.PhoneNumbers {
		phoneErr, err := s.validatorService.ValidatePhoneNumber(ctx, s.db, phoneNumber, instanceID, nil, !phoneNumberAttribute.Base().VerifyAtSignUp, param.PhoneNumber.Name)
		if err != nil {
			return apierror.Unexpected(err)
		}

		apiErrs = apierror.Combine(apiErrs, phoneErr)
	}

	// web3 wallets
	// make sure passed in array is unique
	if set.New(params.Web3Wallets...).Count() != len(params.Web3Wallets) {
		apiErrs = apierror.Combine(apiErrs, apierror.FormDuplicateParameter(param.Web3Wallet.Name))
	}

	// if web3 wallets are not enabled, don't allow users to have web3 wallets
	if len(params.Web3Wallets) > 0 && !userSettings.GetAttribute(names.Web3Wallet).Base().Enabled {
		apiErrs = apierror.Combine(apiErrs, apierror.FormUnknownParameter(param.Web3Wallet.Name))
	}

	for _, web3Wallet := range params.Web3Wallets {
		web3WalletError, err := s.validatorService.ValidateWeb3Wallet(ctx, s.db, web3Wallet, instanceID)
		if err != nil {
			return apierror.Unexpected(err)
		}

		apiErrs = apierror.Combine(apiErrs, web3WalletError)
	}

	// username
	if params.Username != nil {
		usernameValidErrs, err := s.validatorService.ValidateUsername(ctx, s.db, *params.Username, instanceID)
		if err != nil {
			return apierror.Unexpected(err)
		}

		apiErrs = apierror.Combine(apiErrs, usernameValidErrs)
	}

	// first name
	if params.FirstName != nil {
		firstNameAttribute := userSettings.GetAttribute(names.FirstName)
		apiErrs = apierror.Combine(apiErrs, firstNameAttribute.Validate(*params.FirstName, param.FirstName.Name))
	}

	// last name
	if params.LastName != nil {
		lastNameAttribute := userSettings.GetAttribute(names.LastName)
		apiErrs = apierror.Combine(apiErrs, lastNameAttribute.Validate(*params.LastName, param.LastName.Name))
	}

	// password
	// TODO: We should check PasswordDigest before Password. When done, it's going save us from some CPU cycles.
	skipPasswordChecks := params.SkipPasswordChecks != nil && *params.SkipPasswordChecks
	if params.Password != nil && !skipPasswordChecks {
		apiErr := validate.Password(ctx, *params.Password, param.Password.Name, userSettings.PasswordSettings)
		if apiErr != nil {
			apiErrs = apierror.Combine(apiErrs, apiErr)
		}
	}

	if params.PasswordDigest != nil {
		if params.Password != nil {
			apiErrs = apierror.Combine(apiErrs, apierror.FormParameterNotAllowedIfAnotherParameterIsPresent(param.Password.Name, param.PasswordDigest.Name))
		}

		if params.PasswordHasher == nil {
			apiErrs = apierror.Combine(apiErrs, apierror.FormMissingConditionalParameterOnExistence(param.PasswordDigest.Name, param.PasswordHasher.Name))
		}
	}

	if params.PasswordHasher != nil {
		supportedAlgorithms := hash.SupportedAlgorithms()
		algorithmExists := supportedAlgorithms.Contains(*params.PasswordHasher)
		if !algorithmExists {
			apiErrs = apierror.Combine(apiErrs, apierror.FormInvalidParameterValueWithAllowed(param.PasswordHasher.Name, *params.PasswordHasher, supportedAlgorithms.Array()))
		}

		if params.PasswordDigest == nil {
			apiErrs = apierror.Combine(apiErrs, apierror.FormMissingConditionalParameterOnExistence(param.PasswordDigest.Name, param.PasswordHasher.Name))
		}

		if algorithmExists && params.PasswordDigest != nil && !hash.Validate(*params.PasswordHasher, *params.PasswordDigest) {
			apiErrs = apierror.Combine(apiErrs, apierror.FormPasswordDigestInvalid(param.PasswordDigest.Name, *params.PasswordHasher))
		}
	}

	if params.SkipPasswordRequirement != nil && *params.SkipPasswordRequirement && params.Password == nil && params.PasswordDigest == nil {
		// In order to create a user without a password, we need to make sure there is at least
		// one other first factor that could be used instead.
		// For this, we need to ignore attributes which are also strategies, like `ticket`
		methodsNotUsedAsAlternativeSignIn := set.New[string]()
		for _, attribute := range userSettings.EnabledAttributes() {
			if attribute.UsedForVerification() {
				methodsNotUsedAsAlternativeSignIn.Insert(attribute.Name())
			}
		}
		methodsNotUsedAsAlternativeSignIn.Insert(constants.VSResetPasswordEmailCode, constants.VSResetPasswordPhoneCode)

		firstFactorStrategies := userSettings.FirstFactors()
		firstFactorStrategies.Subtract(methodsNotUsedAsAlternativeSignIn)
		if firstFactorStrategies.IsEmpty() {
			apiErrs = apierror.Combine(apiErrs, apierror.FormParameterNotAllowedConditionally(param.SkipPasswordRequirement.Name, constants.VSPassword, "the only authentication factor"))
		}
	}

	if params.TOTPSecret != nil {
		if userSettings.GetAttribute(names.AuthenticatorApp).Base().Enabled {
			if *params.TOTPSecret == "" {
				apiErrs = apierror.Combine(apiErrs, apierror.FormNilParameter("totp_secret"))
			}
		} else {
			apiErrs = apierror.Combine(apiErrs, apierror.FormUnknownParameter("totp_secret"))
		}
	}

	if len(params.BackupCodes) > 0 && !userSettings.GetAttribute(names.BackupCode).Base().Enabled {
		apiErrs = apierror.Combine(apiErrs, apierror.FormUnknownParameter("backup_codes"))
	}

	if params.CreatedAt != nil {
		_, err := time.ParseRFC3339(*params.CreatedAt)
		if err != nil {
			apiErrs = apierror.Combine(apiErrs, apierror.FormInvalidTime("created_at"))
		}
	}

	// metadata
	apiErrs = apierror.Combine(apiErrs, metadata.Validate(params.toMetadata()))

	if apiErrs != nil {
		return apiErrs
	}

	// validate data works for supplied env.
	missingParams := s.validateCreateUserData(params, userSettings)
	if len(missingParams) != 0 {
		return apierror.UserDataMissing(missingParams)
	}

	return nil
}

// validateCreateUserData returns true if the supplied CreateUserData can be
// turned into a user
func (s *Service) validateCreateUserData(params CreateParams, userSettings *usersettings.UserSettings) []string {
	missingParams := set.New[string]()

	if userSettings.GetAttribute(names.FirstName).Base().Required && params.FirstName == nil {
		missingParams.Insert(param.FirstName.Name)
	}

	if userSettings.GetAttribute(names.LastName).Base().Required && params.LastName == nil {
		missingParams.Insert(param.LastName.Name)
	}

	if userSettings.GetAttribute(names.Username).Base().Required && params.Username == nil {
		missingParams.Insert(param.Username.Name)
	}

	if userSettings.GetAttribute(names.Password).Base().Required {
		skipPasswordRequirement := params.SkipPasswordRequirement != nil && *params.SkipPasswordRequirement
		if params.Password == nil && params.PasswordDigest == nil && !skipPasswordRequirement {
			missingParams.Insert(param.Password.Name)
		}
	}

	emailCollected := len(params.EmailAddresses) > 0
	if userSettings.GetAttribute(names.EmailAddress).Base().Required && !emailCollected {
		missingParams.Insert(param.EmailAddress.Name)
	}

	phoneCollected := len(params.PhoneNumbers) > 0
	if userSettings.GetAttribute(names.PhoneNumber).Base().Required && !phoneCollected {
		missingParams.Insert(param.PhoneNumber.Name)
	}

	web3WalletCollected := len(params.Web3Wallets) > 0
	if userSettings.GetAttribute(names.Web3Wallet).Base().Required && !web3WalletCollected {
		missingParams.Insert(param.Web3Wallet.Name)
	}

	// if usernames are the only ident required, then we don't need to check anything else
	if !userSettings.GetAttribute(names.EmailAddress).Base().Required &&
		!userSettings.GetAttribute(names.PhoneNumber).Base().Required &&
		!userSettings.GetAttribute(names.Web3Wallet).Base().Required {
		return missingParams.Array()
	}

	requirements := userSettings.WeirdDeprecatedIdentificationRequirementsDoubleArray()[0]
	requirementsSet := set.New(requirements...)

	if (requirementsSet.Contains(constants.ITEmailAddress) && emailCollected) ||
		(requirementsSet.Contains(constants.ITPhoneNumber) && phoneCollected) ||
		(requirementsSet.Contains(constants.ITWeb3Wallet) && web3WalletCollected) ||
		requirementsSet.Count() == 0 {
		return missingParams.Array()
	}

	missingParams.Insert(requirements...)
	return missingParams.Array()
}

// this method doesn't do any validation, and assumes all data passed in
// will create a complete, valid, user for the given instance.
func (s *Service) createUser(ctx context.Context, tx database.Tx, env *model.Env, params CreateParams) (*model.User, error) {
	user := &model.User{User: &sqbmodel.User{
		InstanceID: env.Instance.ID,
	}}

	if params.ExternalID != nil {
		user.ExternalID = null.StringFrom(*params.ExternalID)
	}

	if params.Password != nil {
		pwd, err := hash.GenerateBcryptHash(*params.Password)
		if err != nil {
			return nil, err
		}

		user.PasswordDigest = null.StringFrom(pwd)
		user.PasswordHasher = null.StringFrom(hash.Bcrypt)
	}

	if params.PasswordDigest != nil && params.PasswordHasher != nil {
		user.PasswordDigest = null.StringFrom(*params.PasswordDigest)
		user.PasswordHasher = null.StringFrom(*params.PasswordHasher)
	}

	if params.FirstName != nil {
		user.FirstName = null.StringFrom(*params.FirstName)
	}

	if params.LastName != nil {
		user.LastName = null.StringFrom(*params.LastName)
	}

	if params.UnsafeMetadata != nil {
		user.UnsafeMetadata = types.JSON(*params.UnsafeMetadata)
	}

	if params.PublicMetadata != nil {
		user.PublicMetadata = types.JSON(*params.PublicMetadata)
	}

	if params.PrivateMetadata != nil {
		user.PrivateMetadata = types.JSON(*params.PrivateMetadata)
	}

	if params.CreatedAt != nil {
		var err error
		user.CreatedAt, err = time.ParseRFC3339(*params.CreatedAt)
		if err != nil {
			return nil, err
		}
	}

	err := s.userCreateService.Create(ctx, tx, users.CreateParams{
		AuthConfig:   env.AuthConfig,
		Instance:     env.Instance,
		Subscription: env.Subscription,
		User:         user,
	})
	if err != nil {
		return nil, err
	}

	// username
	if params.Username != nil {
		if err := s.addVerifiedIdentification(ctx, tx, user, constants.ITUsername, *params.Username); err != nil {
			return nil, err
		}
	}

	// email addresses
	for _, emailAddress := range params.EmailAddresses {
		if err := s.addVerifiedIdentification(ctx, tx, user, constants.ITEmailAddress, strings.ToLower(emailAddress)); err != nil {
			return nil, err
		}
	}

	// phone numbers
	for _, phoneNumber := range params.PhoneNumbers {
		if err := s.addVerifiedIdentification(ctx, tx, user, constants.ITPhoneNumber, phoneNumber); err != nil {
			return nil, err
		}
	}

	// web3 wallets
	for _, web3Wallet := range params.Web3Wallets {
		if err := s.addVerifiedIdentification(ctx, tx, user, constants.ITWeb3Wallet, web3Wallet); err != nil {
			return nil, err
		}
	}

	// TOTP
	if params.TOTPSecret != nil {
		newTOTP := &model.TOTP{Totp: &sqbmodel.Totp{
			InstanceID: env.Instance.ID,
			UserID:     user.ID,
			Secret:     *params.TOTPSecret,
			Verified:   true,
		}}
		if err := s.totpRepo.Upsert(ctx, tx, newTOTP); err != nil {
			return nil, err
		}
	}

	if len(params.BackupCodes) > 0 {
		hashedCodes, err := backup_codes.Migrate(params.BackupCodes)
		if err != nil {
			return nil, err
		}

		newBackupCode := &model.BackupCode{BackupCode: &sqbmodel.BackupCode{
			InstanceID: env.Instance.ID,
			UserID:     user.ID,
			Codes:      hashedCodes,
		}}
		if err := s.backupCodeRepo.Upsert(ctx, tx, newBackupCode); err != nil {
			return nil, err
		}
	}

	return user, nil
}

func (s *Service) addVerifiedIdentification(
	ctx context.Context,
	exec database.Executor,
	user *model.User,
	identType string,
	identVal string,
) error {
	verification := &model.Verification{Verification: &sqbmodel.Verification{
		InstanceID: user.InstanceID,
		Strategy:   constants.VSAdmin,
		Attempts:   0,
	}}

	if err := s.verRepo.Insert(ctx, exec, verification); err != nil {
		return err
	}

	identification := &model.Identification{Identification: &sqbmodel.Identification{
		InstanceID:     user.InstanceID,
		UserID:         null.StringFrom(user.ID),
		Type:           identType,
		VerificationID: null.StringFrom(verification.ID),
		Identifier:     null.StringFrom(identVal),
		Status:         constants.ISVerified,
	}}

	identification.SetCanonicalIdentifier()
	if err := s.identRepo.Insert(ctx, exec, identification); err != nil {
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueIdentification) {
			return apierror.IdentificationExists(identType, nil)
		}
		return err
	}

	verification.IdentificationID = null.StringFrom(identification.ID)
	if err := s.verRepo.UpdateIdentificationID(ctx, exec, verification); err != nil {
		return err
	}

	if identType == constants.ITEmailAddress && !user.PrimaryEmailAddressID.Valid {
		user.PrimaryEmailAddressID = null.StringFrom(identification.ID)
		if err := s.userRepo.UpdatePrimaryEmailAddressID(ctx, exec, user); err != nil {
			return err
		}
	}

	if identType == constants.ITPhoneNumber && !user.PrimaryPhoneNumberID.Valid {
		user.PrimaryPhoneNumberID = null.StringFrom(identification.ID)
		if err := s.userRepo.UpdatePrimaryPhoneNumberID(ctx, exec, user); err != nil {
			return err
		}
	}

	if identType == constants.ITWeb3Wallet && !user.PrimaryWeb3WalletID.Valid {
		user.PrimaryWeb3WalletID = null.StringFrom(identification.ID)
		if err := s.userRepo.UpdatePrimaryWeb3WalletID(ctx, exec, user); err != nil {
			return err
		}
	}

	if identType == constants.ITUsername {
		user.UsernameID = null.StringFrom(identification.ID)
		if err := s.userRepo.UpdateUsernameID(ctx, exec, user); err != nil {
			return err
		}
	}

	return nil
}
