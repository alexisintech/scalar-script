package users

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"clerk/api/shared/applications"
	"clerk/api/shared/client_data"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/comms"
	"clerk/api/shared/events"
	"clerk/api/shared/identifications"
	"clerk/api/shared/images"
	"clerk/api/shared/serializable"
	"clerk/api/shared/sessions"
	"clerk/api/shared/user_profile"
	"clerk/api/shared/validators"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/backup_codes"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	cevents "clerk/pkg/events"
	"clerk/pkg/hash"
	"clerk/pkg/jobs"
	clerkjson "clerk/pkg/json"
	"clerk/pkg/metadata"
	"clerk/pkg/oauth"
	clerkstrings "clerk/pkg/strings"
	clerktime "clerk/pkg/time"
	"clerk/pkg/unverifiedemails"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	usersettingsmodel "clerk/pkg/usersettings/model"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock     clockwork.Clock
	db        database.Database
	gueClient *gue.Client

	// services
	applicationDeleter    *applications.Deleter
	commsService          *comms.Service
	eventService          *events.Service
	identificationService *identifications.Service
	validatorService      *validators.Service
	imageService          *images.Service
	sessionService        *sessions.Service
	serializableService   *serializable.Service
	userProfileService    *user_profile.Service
	clientDataService     *client_data.Service

	// repositories
	applicationRepo    *repository.Applications
	backupCodeRepo     *repository.BackupCode
	identificationRepo *repository.Identification
	imagesRepo         *repository.Images
	signInRepo         *repository.SignIn
	totpRepo           *repository.TOTP
	userRepo           *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:                 deps.Clock(),
		db:                    deps.DB(),
		gueClient:             deps.GueClient(),
		applicationDeleter:    applications.NewDeleter(deps),
		commsService:          comms.NewService(deps),
		eventService:          events.NewService(deps),
		identificationService: identifications.NewService(deps),
		validatorService:      validators.NewService(),
		imageService:          images.NewService(deps.StorageClient()),
		sessionService:        sessions.NewService(deps),
		serializableService:   serializable.NewService(deps.Clock()),
		userProfileService:    user_profile.NewService(deps.Clock()),
		clientDataService:     client_data.NewService(deps),
		applicationRepo:       repository.NewApplications(),
		backupCodeRepo:        repository.NewBackupCode(),
		identificationRepo:    repository.NewIdentification(),
		imagesRepo:            repository.NewImages(),
		signInRepo:            repository.NewSignIn(),
		totpRepo:              repository.NewTOTP(),
		userRepo:              repository.NewUsers(),
	}
}

type UpdateForm struct {
	FirstName                 clerkjson.String
	LastName                  clerkjson.String
	Username                  clerkjson.String
	ExternalID                clerkjson.String
	Password                  *string
	PasswordDigest            *string
	PasswordHasher            *string
	SkipPasswordChecks        bool
	SignOutOfOtherSessions    bool
	PrimaryEmailAddressID     *string
	PrimaryEmailAddressNotify bool
	PrimaryPhoneNumberID      *string
	PrimaryWeb3WalletID       *string
	PublicMetadata            *json.RawMessage
	PrivateMetadata           *json.RawMessage
	UnsafeMetadata            *json.RawMessage
	ProfileImageID            *string
	TOTPSecret                *string
	BackupCodes               []string
	CreatedAt                 *string
	DeleteSelfEnabled         *bool
	CreateOrganizationEnabled *bool
	usernameID                *string `json:"-"`

	profileImagePublicURL *string
}

func (f UpdateForm) toMetadata() metadata.Metadata {
	v := metadata.Metadata{}
	if f.PrivateMetadata != nil {
		v.Private = *f.PrivateMetadata
	}
	if f.PublicMetadata != nil {
		v.Public = *f.PublicMetadata
	}
	if f.UnsafeMetadata != nil {
		v.Unsafe = *f.UnsafeMetadata
	}
	return v
}

func (s *Service) Update(
	ctx context.Context,
	env *model.Env,
	userID string,
	updateForm *UpdateForm,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
) (*model.User, apierror.Error) {
	user, goerr := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, instance.ID)
	if goerr != nil {
		return nil, apierror.Unexpected(goerr)
	} else if user == nil {
		return nil, apierror.UserNotFound(userID)
	}

	if updateForm.Username.Valid {
		updateForm.Username = clerkjson.StringFrom(strings.ToLower(updateForm.Username.Value))
	}

	var previousPrimaryEmailAddress *string
	if updateForm.PrimaryEmailAddressID != nil {
		// We're updating user's primary email address ID.
		// Get the user's previous email address, so we can notify them that
		// the primary email address has changed.
		var err error
		previousPrimaryEmailAddress, err = s.userProfileService.GetPrimaryEmailAddress(ctx, s.db, user)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	var updatedUser *model.User
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		apiErr := s.validateUpdateForm(ctx, tx, user, updateForm, instance.ID, userSettings)
		if apiErr != nil {
			return true, apiErr
		}

		var updateCols []string

		identification, err := s.updateUsername(ctx, tx, updateForm.Username, user, instance, userSettings)
		if err != nil {
			if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueIdentification) {
				return true, apierror.IdentificationExists(constants.ITUsername, nil)
			}
			return true, err
		}

		if identification != nil {
			updateForm.usernameID = &identification.ID
		}

		updatedUser, updateCols = s.updateUserAndGetColumns(user, updateForm)

		if len(updateCols) > 0 {
			err := s.userRepo.Update(ctx, tx, updatedUser, updateCols...)
			if err != nil {
				return true, err
			}
		}

		err = s.updateTOTP(ctx, tx, updateForm.TOTPSecret, updatedUser.ID, instance.ID)
		if err != nil {
			return true, err
		}

		err = s.updateBackupCode(ctx, tx, updateForm.BackupCodes, updatedUser.ID, instance.ID)
		if err != nil {
			return true, err
		}

		// Password was updated and we need to revoke all other sessions for the user.
		if updateForm.PasswordDigest != nil && updateForm.SignOutOfOtherSessions {
			if err := s.sessionService.RevokeAllForUserID(ctx, instance.ID, updatedUser.ID); err != nil {
				return true, err
			}
		}

		_, err = s.SendUserUpdatedEvent(ctx, tx, instance, userSettings, updatedUser)
		if err != nil {
			return true, fmt.Errorf("user/update: send user updated event for (%+v, %+v): %w", updatedUser, instance.ID, err)
		}

		if updateForm.PrimaryEmailAddressNotify {
			err = s.NotifyPrimaryEmailChanged(ctx, tx, updatedUser, env, previousPrimaryEmailAddress)
			if err != nil {
				return true, err
			}
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

	return updatedUser, nil
}

// SendUserUpdatedEvent sends a user.updated event
// It returns the serialized user payload so that the caller can use it in the response if necessary
func (s *Service) SendUserUpdatedEvent(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User,
) (*model.UserSerializable, error) {
	userSerializable, err := s.serializableService.ConvertUser(ctx, exec, userSettings, user)
	if err != nil {
		return nil, fmt.Errorf("sendUserUpdatedEvent: serializing user %+v: %w", user, err)
	}

	if err = s.eventService.UserUpdated(ctx, exec, instance, serialize.UserToServerAPI(ctx, userSerializable)); err != nil {
		return nil, fmt.Errorf("sendUserUpdatedEvent: send user updated event for user %s: %w", user.ID, err)
	}

	return userSerializable, nil
}

func (s *Service) updateUserAndGetColumns(user *model.User, updateForm *UpdateForm) (*model.User, []string) {
	updateCols := make([]string, 0)

	if updateForm.FirstName.IsSet {
		user.FirstName = null.StringFromPtr(updateForm.FirstName.BlankPtr())
		updateCols = append(updateCols, sqbmodel.UserColumns.FirstName)
	}

	if updateForm.LastName.IsSet {
		user.LastName = null.StringFromPtr(updateForm.LastName.BlankPtr())
		updateCols = append(updateCols, sqbmodel.UserColumns.LastName)
	}

	if updateForm.ExternalID.IsSet {
		user.ExternalID = null.StringFromPtr(updateForm.ExternalID.BlankPtr())
		updateCols = append(updateCols, sqbmodel.UserColumns.ExternalID)
	}

	if updateForm.PrimaryEmailAddressID != nil {
		user.PrimaryEmailAddressID = null.StringFromPtr(updateForm.PrimaryEmailAddressID)
		updateCols = append(updateCols, sqbmodel.UserColumns.PrimaryEmailAddressID)
	}

	if updateForm.Username.IsSet {
		user.UsernameID = null.StringFromPtr(updateForm.usernameID)
		updateCols = append(updateCols, sqbmodel.UserColumns.UsernameID)
	}

	if updateForm.PrimaryPhoneNumberID != nil {
		user.PrimaryPhoneNumberID = null.StringFromPtr(updateForm.PrimaryPhoneNumberID)
		updateCols = append(updateCols, sqbmodel.UserColumns.PrimaryPhoneNumberID)
	}
	if updateForm.PrimaryWeb3WalletID != nil {
		user.PrimaryWeb3WalletID = null.StringFromPtr(updateForm.PrimaryWeb3WalletID)
		updateCols = append(updateCols, sqbmodel.UserColumns.PrimaryWeb3WalletID)
	}

	if updateForm.PasswordDigest != nil {
		user.PasswordDigest = null.StringFrom(*updateForm.PasswordDigest)
		user.PasswordHasher = null.StringFrom(*updateForm.PasswordHasher)
		user.PasswordLastUpdatedAt = null.TimeFrom(s.clock.Now().UTC())
		updateCols = append(updateCols, sqbmodel.UserColumns.PasswordDigest, sqbmodel.UserColumns.PasswordHasher, sqbmodel.UserColumns.PasswordLastUpdatedAt)
	}

	if updateForm.PublicMetadata != nil {
		user.PublicMetadata = []byte(*updateForm.PublicMetadata)
		updateCols = append(updateCols, sqbmodel.UserColumns.PublicMetadata)
	}

	if updateForm.PrivateMetadata != nil {
		user.PrivateMetadata = []byte(*updateForm.PrivateMetadata)
		updateCols = append(updateCols, sqbmodel.UserColumns.PrivateMetadata)
	}

	if updateForm.UnsafeMetadata != nil {
		user.UnsafeMetadata = []byte(*updateForm.UnsafeMetadata)
		updateCols = append(updateCols, sqbmodel.UserColumns.UnsafeMetadata)
	}

	if updateForm.profileImagePublicURL != nil {
		user.ProfileImagePublicURL = null.StringFromPtr(updateForm.profileImagePublicURL)
		updateCols = append(updateCols, sqbmodel.UserColumns.ProfileImagePublicURL)
	}

	if updateForm.CreatedAt != nil {
		user.CreatedAt, _ = clerktime.ParseRFC3339(*updateForm.CreatedAt)
		updateCols = append(updateCols, sqbmodel.UserColumns.CreatedAt)
	}

	if updateForm.DeleteSelfEnabled != nil {
		user.DeleteSelfEnabled = *updateForm.DeleteSelfEnabled
		updateCols = append(updateCols, sqbmodel.UserColumns.DeleteSelfEnabled)
	}

	if updateForm.CreateOrganizationEnabled != nil {
		user.CreateOrganizationEnabled = *updateForm.CreateOrganizationEnabled
		updateCols = append(updateCols, sqbmodel.UserColumns.CreateOrganizationEnabled)
	}

	return user, updateCols
}

func (s *Service) validateUpdateForm(
	ctx context.Context,
	tx database.Tx,
	user *model.User,
	updateForm *UpdateForm,
	instanceID string,
	userSettings *usersettings.UserSettings,
) apierror.Error {
	emailAddresses, err := s.identificationRepo.FindAllByUserAndType(ctx, tx, instanceID, user.ID, constants.ITEmailAddress)
	if err != nil {
		return apierror.Unexpected(err)
	}

	var formErrs apierror.Error

	if updateForm.PrimaryEmailAddressID != nil {
		var verifiedEmailID *string
		objFound := false
		for _, emailAddress := range emailAddresses {
			if *updateForm.PrimaryEmailAddressID == emailAddress.ID {
				objFound = true
				if emailAddress.IsVerified() {
					verifiedEmailID = updateForm.PrimaryEmailAddressID
					break
				}
			}
		}

		if !objFound {
			formErrs = apierror.Combine(formErrs, apierror.FormMissingResource(param.PrimaryEmailAddressID.Name))
		} else if verifiedEmailID == nil {
			formErrs = apierror.Combine(formErrs, apierror.FormUnverifiedIdentification(param.PrimaryEmailAddressID.Name))
		}
	}

	phoneNumbers, err := s.identificationRepo.FindAllByUserAndType(ctx, tx, instanceID, user.ID, constants.ITPhoneNumber)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if updateForm.PrimaryPhoneNumberID != nil {
		var verifiedPhoneNumberID *string
		objFound := false
		for _, phoneNumber := range phoneNumbers {
			if *updateForm.PrimaryPhoneNumberID == phoneNumber.ID {
				objFound = true
				if phoneNumber.IsVerified() {
					verifiedPhoneNumberID = updateForm.PrimaryPhoneNumberID
					break
				}
			}
		}

		if !objFound {
			formErrs = apierror.Combine(formErrs, apierror.FormMissingResource(param.PrimaryPhoneNumberID.Name))
		} else if verifiedPhoneNumberID == nil {
			formErrs = apierror.Combine(formErrs, apierror.FormUnverifiedIdentification(param.PrimaryPhoneNumberID.Name))
		}
	}

	web3Wallets, err := s.identificationRepo.FindAllByUserAndType(ctx, tx, instanceID, user.ID, constants.ITWeb3Wallet)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if updateForm.PrimaryWeb3WalletID != nil {
		var verifiedWeb3WalletID *string
		objFound := false
		for _, web3Wallet := range web3Wallets {
			if *updateForm.PrimaryWeb3WalletID == web3Wallet.ID {
				objFound = true
				if web3Wallet.IsVerified() {
					verifiedWeb3WalletID = updateForm.PrimaryWeb3WalletID
					break
				}
			}
		}

		if !objFound {
			formErrs = apierror.Combine(formErrs, apierror.FormMissingResource(param.PrimaryWeb3WalletID.Name))
		} else if verifiedWeb3WalletID == nil {
			formErrs = apierror.Combine(formErrs, apierror.FormUnverifiedIdentification(param.PrimaryWeb3WalletID.Name))
		}
	}

	// Validate metadata attributes
	formErrs = apierror.Combine(formErrs, metadata.Validate(updateForm.toMetadata()))

	// Validate password related fields
	if updateForm.PasswordDigest != nil {
		if updateForm.Password != nil {
			formErrs = apierror.Combine(formErrs, apierror.FormParameterNotAllowedIfAnotherParameterIsPresent(param.Password.Name, param.PasswordDigest.Name))
		}

		if updateForm.PasswordHasher == nil {
			formErrs = apierror.Combine(formErrs, apierror.FormMissingConditionalParameterOnExistence(param.PasswordDigest.Name, param.PasswordHasher.Name))
		}
	}

	if updateForm.PasswordHasher != nil {
		supportedAlgorithms := hash.SupportedAlgorithms()
		algorithmExists := supportedAlgorithms.Contains(*updateForm.PasswordHasher)
		if !algorithmExists {
			formErrs = apierror.Combine(formErrs, apierror.FormInvalidParameterValueWithAllowed(param.PasswordHasher.Name, *updateForm.PasswordHasher, supportedAlgorithms.Array()))
		}

		if updateForm.PasswordDigest == nil {
			formErrs = apierror.Combine(formErrs, apierror.FormMissingConditionalParameterOnExistence(param.PasswordDigest.Name, param.PasswordHasher.Name))
		}

		if algorithmExists && updateForm.PasswordDigest != nil && !hash.Validate(*updateForm.PasswordHasher, *updateForm.PasswordDigest) {
			formErrs = apierror.Combine(formErrs, apierror.FormPasswordDigestInvalid(param.PasswordDigest.Name, *updateForm.PasswordHasher))
		}
	}

	if updateForm.Password != nil {
		if !updateForm.SkipPasswordChecks {
			apiErr := validate.Password(ctx, *updateForm.Password, param.Password.Name, userSettings.PasswordSettings)
			if apiErr != nil {
				formErrs = apierror.Combine(formErrs, apiErr)
			}
		}

		passwordDigest, err := hash.GenerateBcryptHash(*updateForm.Password)
		if err != nil {
			if errors.Is(err, hash.ErrPasswordTooLong) {
				return apierror.FormInvalidPasswordSizeInBytesExceeded(param.Password.Name)
			}
			return apierror.Unexpected(err)
		}

		bcryptStr := hash.Bcrypt
		updateForm.PasswordDigest = &passwordDigest
		updateForm.PasswordHasher = &bcryptStr
	}

	usernameValidErrs, err := s.validateUsername(ctx, tx, user, updateForm.Username, userSettings.GetAttribute(names.Username).Base(), instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	formErrs = apierror.Combine(formErrs, usernameValidErrs)

	if updateForm.ProfileImageID != nil {
		img, err := s.imagesRepo.QueryByID(ctx, tx, *updateForm.ProfileImageID)
		if err != nil {
			return apierror.Unexpected(err)
		}
		if img == nil {
			return apierror.ImageNotFound()
		}
		updateForm.profileImagePublicURL = &img.PublicURL

		// update image's used_by_resource_type to user
		img.UsedByResourceType = null.StringFrom(constants.UserResource)
		if err := s.imagesRepo.UpdateUsedByResourceType(ctx, tx, img); err != nil {
			return apierror.Unexpected(err)
		}
	}

	// Validate first name
	firstNameAttribute := userSettings.GetAttribute(names.FirstName)
	formErrs = apierror.Combine(formErrs, validateNameAttribute(updateForm.FirstName, firstNameAttribute, param.FirstName.Name))

	// Validate last name
	lastNameAttribute := userSettings.GetAttribute(names.LastName)
	formErrs = apierror.Combine(formErrs, validateNameAttribute(updateForm.LastName, lastNameAttribute, param.LastName.Name))

	// if Authenticator app is not enabled, don't allow users to have TOTP
	if updateForm.TOTPSecret != nil && !userSettings.GetAttribute(names.AuthenticatorApp).Base().Enabled {
		formErrs = apierror.Combine(formErrs, apierror.FormUnknownParameter("totp_secret"))
	}

	// if Backup code is not enabled, don't allow users to have Backup codes
	if len(updateForm.BackupCodes) > 0 && !userSettings.GetAttribute(names.BackupCode).Base().Enabled {
		formErrs = apierror.Combine(formErrs, apierror.FormUnknownParameter("backup_codes"))
	}

	if updateForm.CreatedAt != nil {
		_, err := clerktime.ParseRFC3339(*updateForm.CreatedAt)
		if err != nil {
			formErrs = apierror.Combine(formErrs, apierror.FormInvalidTime("created_at"))
		}
	}

	return formErrs
}

type UpdateProfileImageParams struct {
	Filename string
	Data     io.ReadCloser
	UserID   string
}

// UpdateProfileImage updates the user with userID profile
// image with the provided file.
// It will save the image in the database and associate it
// with the user.
func (s *Service) UpdateProfileImage(
	ctx context.Context,
	params UpdateProfileImageParams,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
) (*serialize.ImageResponse, apierror.Error) {
	var img *model.Image
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var apiErr apierror.Error
		img, apiErr = s.imageService.Create(
			ctx,
			tx,
			images.ImageParams{
				Filename:           params.Filename,
				Prefix:             images.PrefixUploaded,
				Src:                params.Data,
				UploaderUserID:     params.UserID,
				UsedByResourceType: clerkstrings.ToPtr(constants.UserResource),
			},
		)
		if apiErr != nil {
			return true, apiErr
		}

		user, err := s.userRepo.FindByID(ctx, tx, params.UserID)
		if err != nil {
			return true, err
		}

		if user.ProfileImagePublicURL.Valid {
			err := s.EnqueueCleanupImageJob(ctx, tx, user.ProfileImagePublicURL.String)
			if err != nil {
				return true, err
			}
		}

		user.ProfileImagePublicURL = null.StringFrom(img.PublicURL)
		err = s.userRepo.UpdateProfileImage(ctx, tx, user)
		if err != nil {
			return true, err
		}

		_, err = s.SendUserUpdatedEvent(ctx, tx, instance, userSettings, user)
		if err != nil {
			return true, fmt.Errorf("user/updateProfileImage: send user updated event for (%+v, %+v): %w",
				user, instance, err)
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.Image(img), nil
}

// Delete deletes the given user.
func (s *Service) Delete(ctx context.Context, env *model.Env, userID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	// Delete all sessions
	if err := s.sessionService.DeleteUserSessions(ctx, env.Instance.ID, userID); err != nil {
		return nil, apierror.Unexpected(err)
	}

	var deleted *serialize.DeletedObjectResponse

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		user, err := s.userRepo.QueryByID(ctx, tx, userID)
		if err != nil {
			return true, fmt.Errorf("shared/users: query user by id %s: %w", userID, err)
		}
		if user == nil {
			return true, apierror.UserNotFound(userID)
		}

		if env.Application.Type == string(constants.RTSystem) {
			// Schedule a soft-delete for all applications that the user being deleted owns.
			// We schedule the soft-delete instead of doing it in place, because soft-deletion also
			// involves Stripe cancellation, which is an action that cannot be reverted, if the
			// transaction fails.
			err := s.applicationDeleter.ScheduleSoftDeleteOfOwnedApplications(ctx, tx, user.ID, constants.UserResource)
			if err != nil {
				return true, err
			}
		}

		rowsDeleted, err := s.userRepo.DeleteByID(ctx, tx, user.ID)
		if err != nil {
			return true, fmt.Errorf("shared/users: delete user %s: %w", user.ID, err)
		}
		if rowsDeleted == 0 {
			return true, apierror.UserNotFound(userID)
		}

		deleted = serialize.DeletedObject(user.ID, serialize.UserObjectName)

		if user.ProfileImagePublicURL.Valid {
			err := s.EnqueueCleanupImageJob(ctx, tx, user.ProfileImagePublicURL.String)
			if err != nil {
				return true, apierror.Unexpected(err)
			}
		}

		if err := s.eventService.UserDeleted(ctx, tx, env.Instance, deleted); err != nil {
			return true, fmt.Errorf("shared/users: send event %s for instance %+v with payload %+v: %w",
				cevents.EventTypes.UserDeleted, env.Instance.ID, deleted, err)
		}

		// Ensure that any in-flight sessions are deleted from the data store as well
		sessionDeletionAt := s.clock.Now().UTC().Add(30 * time.Second)
		err = jobs.DeleteUserSessions(ctx, s.gueClient,
			jobs.DeleteUserSessionsArgs{UserID: userID, InstanceID: env.Instance.ID},
			jobs.WithTx(tx), jobs.WithRunAt(&sessionDeletionAt))
		if err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return deleted, nil
}

// DeleteProfileImage clears the users profile_image_url.
// The actual image record will be deleted by the images cleanup background
// task, which will also remove the file from the remote storage.
// Triggers a user.updated event on successful execution.
func (s *Service) DeleteProfileImage(ctx context.Context, userID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	instance := env.Instance
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(userID)
	}

	if !user.ProfileImagePublicURL.Valid {
		return nil, apierror.ImageNotFound()
	}

	img, err := s.imagesRepo.QueryByPublicURL(ctx, s.db, user.ProfileImagePublicURL.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err = s.EnqueueCleanupImageJob(ctx, tx, user.ProfileImagePublicURL.String)
		if err != nil {
			return true, err
		}

		user.ProfileImagePublicURL = null.StringFromPtr(nil)
		err = s.userRepo.UpdateProfileImage(ctx, tx, user)
		if err != nil {
			return true, err
		}

		_, err = s.SendUserUpdatedEvent(ctx, tx, instance, userSettings, user)
		if err != nil {
			return true, fmt.Errorf("user/deleteProfileImage: send user updated event for (%+v, %+v): %w", user, instance.ID, err)
		}

		// The image might have come from an external account and not have been
		// downloaded to our CDN yet. It's ok if there's no image record, just
		// set a dummy one for the response.
		if img == nil {
			img = model.NewImageWithPublicURL(user.ProfileImagePublicURL.String)
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.DeletedObject(img.ID, serialize.ObjectImage), nil
}

func (s *Service) validateUsername(
	ctx context.Context,
	tx database.Tx,
	user *model.User,
	value clerkjson.String,
	attribute usersettingsmodel.Attribute,
	instanceID string,
) (apierror.Error, error) {
	isBlank := !value.Valid || value.Value == ""
	if attribute.Required && value.IsSet && isBlank {
		return apierror.FormMissingParameter("username"), nil
	}
	if !isBlank {
		existingUsername, err := s.userProfileService.GetUsernameIdentification(ctx, s.db, user)
		if err != nil {
			return nil, err
		}
		// Return early if the new username is the same as the existing user's
		// username
		if existingUsername != nil && existingUsername.Identifier.Valid && existingUsername.Identifier.String == value.Value {
			return nil, nil
		}

		return s.validatorService.ValidateUsername(ctx, tx, value.Value, instanceID)
	}
	return nil, nil
}

func (s *Service) updateUsername(
	ctx context.Context,
	tx database.Tx,
	value clerkjson.String,
	user *model.User,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
) (*model.Identification, error) {
	if !value.IsSet {
		return nil, nil
	}

	// Check if an identification already exists and update or delete it.
	existingUsername, err := s.userProfileService.GetUsernameIdentification(ctx, tx, user)
	if err != nil {
		return nil, err
	}
	if existingUsername != nil {
		// Value is blank; clear username by removing the identification
		if !value.Valid || value.Value == "" {
			_, err := s.identificationService.Delete(ctx, tx, instance, userSettings, user, existingUsername)
			return nil, err
		}
		existingUsername.Identifier = null.StringFrom(value.Value)
		return existingUsername, s.identificationRepo.Update(ctx, tx, existingUsername, sqbmodel.IdentificationColumns.Identifier)
	}

	// Create a new identification for the user.
	identification, err := s.identificationService.CreateUsername(ctx, tx, value.Value, user, instance.ID)
	if err != nil {
		return nil, err
	}

	return identification, nil
}

func validateNameAttribute(value clerkjson.String, attribute usersettings.Attribute, paramName string) apierror.Error {
	if !value.IsSet {
		return nil
	}

	// null or ""
	isBlank := !value.Valid || value.Value == ""

	if isBlank {
		if attribute.Base().Required {
			return apierror.FormMissingParameter(attribute.Name())
		}
		return nil
	}

	return attribute.Validate(value.Value, paramName)
}

func (s *Service) updateTOTP(ctx context.Context, tx database.Tx, totpSecret *string, userID, instanceID string) error {
	if totpSecret == nil {
		return nil
	}

	newTOTP := &model.TOTP{Totp: &sqbmodel.Totp{
		InstanceID: instanceID,
		UserID:     userID,
		Secret:     *totpSecret,
		Verified:   true,
	}}
	return s.totpRepo.Upsert(ctx, tx, newTOTP)
}

func (s *Service) updateBackupCode(ctx context.Context, tx database.Tx, backupCodes []string, userID, instanceID string) error {
	if len(backupCodes) == 0 {
		return nil
	}

	hashedCodes, err := backup_codes.Migrate(backupCodes)
	if err != nil {
		return err
	}

	newBackupCode := &model.BackupCode{BackupCode: &sqbmodel.BackupCode{
		InstanceID: instanceID,
		UserID:     userID,
		Codes:      hashedCodes,
	}}
	return s.backupCodeRepo.Upsert(ctx, tx, newBackupCode)
}

// NotifyPrimaryEmailChanged sends an email to the user's previous email address informing them that
// their primary email address has been updated.
func (s *Service) NotifyPrimaryEmailChanged(
	ctx context.Context,
	tx database.Tx,
	user *model.User,
	env *model.Env,
	previousEmailAddress *string,
) error {
	if previousEmailAddress == nil {
		return nil
	}

	newEmailAddress, err := s.userProfileService.GetPrimaryEmailAddress(ctx, tx, user)
	if err != nil {
		return err
	}
	if newEmailAddress == nil {
		return nil
	}

	err = s.commsService.SendPrimaryEmailAddressChangedEmail(ctx, tx, env, comms.EmailPrimaryEmailAddressChanged{
		PreviousEmailAddress: *previousEmailAddress,
		NewEmailAddress:      *newEmailAddress,
	})
	if err != nil {
		return fmt.Errorf("user/NotifyPrimaryEmailChanged: sending primary email address changed email (user=%s): %w", user.ID, err)
	}
	return nil
}

// FlagUserForPasswordReset forces a user for a password reset, if email wasn't verified
// and a password was set.
func (s *Service) FlagUserForPasswordReset(ctx context.Context, exec database.Executor, ost *model.OauthStateToken, oauthUser *oauth.User, targetIdent, emailIdent *model.Identification) error {
	if !s.shouldFlagUserForPasswordReset(ost, oauthUser, targetIdent, emailIdent) {
		return nil
	}

	user, err := s.userRepo.FindByID(ctx, exec, emailIdent.UserID.String)
	if err != nil {
		return err
	}

	if !user.PasswordDigest.Valid {
		return nil
	}

	user.RequiresNewPassword = null.BoolFrom(true)
	return s.userRepo.UpdateRequiresNewPassword(ctx, exec, user)
}

func (s *Service) shouldFlagUserForPasswordReset(ost *model.OauthStateToken, oauthUser *oauth.User, targetIdent, emailIdent *model.Identification) bool {
	if !cenv.IsEnabled(cenv.FlagPreventAccountTakeoverUnverifiedEmails) {
		return false
	}

	// skip in case of Connect, as the user is already signed in and confirmed as the rightful owner
	if ost.SourceType == constants.OSTOAuthConnect {
		return false
	}

	// it only applies to OAuth identifications
	if !targetIdent.IsOAuth() {
		return false
	}

	// the email that came from the OAuth provider must be verified,
	// while the email that was used to sign in must not be verified
	return oauthUser.EmailAddressVerified && !emailIdent.IsVerified() && emailIdent.UserID.Valid
}

// SyncSignInPasswordReset syncs the RequiresNewPassword state between a user and a sign-in instance.
func (s *Service) SyncSignInPasswordReset(ctx context.Context, tx database.Tx, instance *model.Instance, signIn *model.SignIn, user *model.User) error {
	if signIn == nil {
		return nil
	}

	if !user.RequiresNewPassword.Valid {
		return nil
	}

	// If the user has a ClerkJS version that supports required password reset
	// we'll enforce it, else we're going to reset and rely on the email.
	if unverifiedemails.IsVerifyFlowSupportedByClerkJSVersion(ctx) {
		signIn.RequiresNewPassword = user.RequiresNewPassword.Bool
		if err := s.signInRepo.UpdateRequireNewPassword(ctx, tx, signIn); err != nil {
			return err
		}
	} else {
		user.RequiresNewPassword = null.BoolFromPtr(nil)
		if err := s.userRepo.UpdateRequiresNewPassword(ctx, tx, user); err != nil {
			return err
		}
	}
	return s.sessionService.RevokeAllForUserID(ctx, instance.ID, user.ID)
}

func (s *Service) EnqueueCleanupImageJob(ctx context.Context, tx database.Tx, publicURL string) error {
	err := jobs.CleanupImage(
		ctx,
		s.gueClient,
		jobs.CleanupImageArgs{
			PublicURL: publicURL,
		},
		jobs.WithTx(tx),
	)
	return err
}
