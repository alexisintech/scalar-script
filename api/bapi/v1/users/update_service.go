package users

import (
	"context"
	"encoding/json"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/users"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	clerkjson "clerk/pkg/json"
	"clerk/pkg/metadata"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/utils/database"
)

type UpdateParams struct {
	FirstName                        clerkjson.String `json:"first_name" form:"first_name"`
	LastName                         clerkjson.String `json:"last_name" form:"last_name"`
	Username                         clerkjson.String `json:"username" form:"username"`
	ExternalID                       clerkjson.String `json:"external_id" form:"external_id"`
	Password                         *string          `json:"password" form:"password"`
	PasswordDigest                   *string          `json:"password_digest" form:"password_digest"`
	PasswordHasher                   *string          `json:"password_hasher" form:"password_hasher"`
	SkipPasswordChecks               *bool            `json:"skip_password_checks" form:"skip_password_checks"`
	SignOutOfOtherSessions           *bool            `json:"sign_out_of_other_sessions" form:"sign_out_of_other_sessions"`
	PrimaryEmailAddressID            *string          `json:"primary_email_address_id" form:"primary_email_address_id"`
	PrimaryPhoneNumberID             *string          `json:"primary_phone_number_id" form:"primary_phone_number_id"`
	PrimaryWeb3WalletID              *string          `json:"primary_web3_wallet_id" form:"primary_web3_wallet_id"`
	PublicMetadata                   *json.RawMessage `json:"public_metadata" form:"public_metadata"`
	PrivateMetadata                  *json.RawMessage `json:"private_metadata" form:"private_metadata"`
	UnsafeMetadata                   *json.RawMessage `json:"unsafe_metadata" form:"unsafe_metadata"`
	ProfileImageID                   *string          `json:"profile_image_id" form:"profile_image_id"`
	TOTPSecret                       *string          `json:"totp_secret" form:"totp_secret"`
	BackupCodes                      []string         `json:"backup_codes" form:"backup_codes"`
	DeleteSelfEnabled                *bool            `json:"delete_self_enabled" form:"delete_self_enabled"`
	CreateOrganizationEnabled        *bool            `json:"create_organization_enabled" form:"create_organization_enabled"`
	NotifyPrimaryEmailAddressChanged *bool            `json:"notify_primary_email_address_changed" form:"notify_primary_email_address_changed"`

	// Specified in RFC3339 format
	CreatedAt *string `json:"created_at" form:"created_at"`
}

func (p UpdateParams) toSharedUpdateForm() *users.UpdateForm {
	return &users.UpdateForm{
		FirstName:                 p.FirstName,
		LastName:                  p.LastName,
		Username:                  p.Username,
		Password:                  p.Password,
		PasswordDigest:            p.PasswordDigest,
		PasswordHasher:            p.PasswordHasher,
		SkipPasswordChecks:        p.SkipPasswordChecks != nil && *p.SkipPasswordChecks,
		SignOutOfOtherSessions:    p.SignOutOfOtherSessions != nil && *p.SignOutOfOtherSessions,
		ExternalID:                p.ExternalID,
		PrimaryEmailAddressID:     p.PrimaryEmailAddressID,
		PrimaryEmailAddressNotify: p.NotifyPrimaryEmailAddressChanged != nil && *p.NotifyPrimaryEmailAddressChanged,
		PrimaryPhoneNumberID:      p.PrimaryPhoneNumberID,
		PrimaryWeb3WalletID:       p.PrimaryWeb3WalletID,
		PublicMetadata:            p.PublicMetadata,
		PrivateMetadata:           p.PrivateMetadata,
		UnsafeMetadata:            p.UnsafeMetadata,
		ProfileImageID:            p.ProfileImageID,
		TOTPSecret:                p.TOTPSecret,
		BackupCodes:               p.BackupCodes,
		CreatedAt:                 p.CreatedAt,
		DeleteSelfEnabled:         p.DeleteSelfEnabled,
		CreateOrganizationEnabled: p.CreateOrganizationEnabled,
	}
}

func (s Service) Update(ctx context.Context, userID string, params UpdateParams) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	updatedUser, apiErr := s.shUsersService.Update(
		ctx,
		env,
		userID,
		params.toSharedUpdateForm(),
		env.Instance,
		usersettings.NewUserSettings(env.AuthConfig.UserSettings),
	)
	if apiErr != nil {
		return nil, apiErr
	}

	return s.serializeUser(ctx, userSettings, updatedUser)
}

// UpdateMetadata saves new values for the user's private, public and unsafe metadata.
// The new values will be merged with the existing ones. Only top-level keys are merged.
// Keys with null values are removed.
func (s *Service) UpdateMetadata(
	ctx context.Context,
	userID string,
	params UpdateMetadataParams,
) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(userID)
	}

	merged, mergeErr := metadata.Merge(user.Metadata(), metadata.Metadata{
		Public:  params.PublicMetadata,
		Private: params.PrivateMetadata,
		Unsafe:  params.UnsafeMetadata,
	})
	if mergeErr != nil {
		return nil, mergeErr
	}
	user.SetMetadata(merged)

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err = s.userRepo.UpdateMetadata(ctx, tx, user)
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		err = s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, user)
		if err != nil {
			return true, fmt.Errorf("user/update: send user updated event for (%+v, %+v): %w", user, env.Instance.ID, err)
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return s.serializeUser(ctx, userSettings, user)
}

// UpdateMetadataParams holds the public, private and unsafe metadata
// raw values.
type UpdateMetadataParams struct {
	PublicMetadata  json.RawMessage `json:"public_metadata"`
	PrivateMetadata json.RawMessage `json:"private_metadata"`
	UnsafeMetadata  json.RawMessage `json:"unsafe_metadata"`
}

func (s *Service) serializeUser(ctx context.Context, userSettings *usersettings.UserSettings, user *model.User) (*serialize.UserResponse, apierror.Error) {
	userSerializable, err := s.serializableService.ConvertUser(ctx, s.db, userSettings, user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.UserToServerAPI(ctx, userSerializable), nil
}
