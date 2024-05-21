package user_profile

import (
	"context"

	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/externalapis/clerkimages"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/volatiletech/null/v8"

	"github.com/jonboulle/clockwork"
)

const gravatarMysteryPersonURL = "https://www.gravatar.com/avatar?d=mp"

type Service struct {
	clock clockwork.Clock

	backupCodeRepo      *repository.BackupCode
	externalAccountRepo *repository.ExternalAccount
	identificationRepo  *repository.Identification
	totpRepo            *repository.TOTP
}

func NewService(clock clockwork.Clock) *Service {
	return &Service{
		clock:               clock,
		backupCodeRepo:      repository.NewBackupCode(),
		externalAccountRepo: repository.NewExternalAccount(),
		identificationRepo:  repository.NewIdentification(),
		totpRepo:            repository.NewTOTP(),
	}
}

// GetPublicDisplayName returns the user's best publicly available name. It will not include the users last name.
func (s *Service) GetPublicDisplayName(ctx context.Context, exec database.Executor, user *model.User) (string, error) {
	if user.FirstName.Valid {
		return user.FirstName.String, nil
	}

	username, err := s.GetUsername(ctx, exec, user)
	if err != nil {
		return "", err
	}

	if username != nil {
		return *username, nil
	}

	return "anonymous", nil
}

// GetIdentifier returns the user's first found primary identifier by the following order (email, phone, web3 wallet, username)
func (s *Service) GetIdentifier(ctx context.Context, exec database.Executor, user *model.User) string {
	email, err := s.GetPrimaryEmailAddress(ctx, exec, user)
	if err == nil && email != nil {
		return *email
	}

	phone, err := s.GetPrimaryPhoneNumber(ctx, exec, user)
	if err == nil && phone != nil {
		return *phone
	}

	web3Wallet, err := s.GetPrimaryWeb3Wallet(ctx, exec, user)
	if err == nil && web3Wallet != nil {
		return *web3Wallet
	}

	username, err := s.GetUsername(ctx, exec, user)
	if err == nil && username != nil {
		return *username
	}

	return ""
}

// GetUsernameIdentification returns the users username identification obj.
func (s *Service) GetUsernameIdentification(ctx context.Context, exec database.Executor, user *model.User) (*model.Identification, error) {
	return s.identificationRepo.QueryByTypeAndUser(ctx, exec, constants.ITUsername, user.ID)
}

// GetUsername returns the user username.
func (s *Service) GetUsername(ctx context.Context, exec database.Executor, user *model.User) (*string, error) {
	ident, err := s.identificationRepo.QueryByTypeAndUser(ctx, exec, constants.ITUsername, user.ID)
	if err != nil {
		return nil, err
	}
	if ident == nil {
		return nil, nil
	}

	return ident.Username(), nil
}

// GetPrimaryPhoneNumber returns the user phone number that is marked primary, if there are no phone_numbers, this will return nil.
func (s *Service) GetPrimaryPhoneNumber(ctx context.Context, exec database.Executor, user *model.User) (*string, error) {
	return s.getPrimaryIdentification(ctx, exec, user.PrimaryPhoneNumberID)
}

// GetPrimaryEmailAddress returns the user email address that is marked primary, if there are no email_addresses, this will return nil.
func (s *Service) GetPrimaryEmailAddress(ctx context.Context, exec database.Executor, user *model.User) (*string, error) {
	return s.getPrimaryIdentification(ctx, exec, user.PrimaryEmailAddressID)
}

// GetPrimaryWeb3Wallet returns the user web3 wallet that is marked primary, otherwise will return nil.
func (s *Service) GetPrimaryWeb3Wallet(ctx context.Context, exec database.Executor, user *model.User) (*string, error) {
	return s.getPrimaryIdentification(ctx, exec, user.PrimaryWeb3WalletID)
}

func (s *Service) getPrimaryIdentification(ctx context.Context, exec database.Executor, primaryIdentID null.String) (*string, error) {
	if !primaryIdentID.Valid {
		return nil, nil
	}

	ident, err := s.identificationRepo.QueryByID(ctx, exec, primaryIdentID.String)
	if err != nil {
		return nil, err
	}
	if ident == nil {
		return nil, nil
	}

	return ident.Identifier.Ptr(), nil
}

// GetProfileImageURL returns the users profile image URL.
// Deprecated after ClerkJS 4.36.0 version , replaced by `GetImageURL`.
func (s *Service) GetProfileImageURL(user *model.User) (string, bool) {
	if user.ProfileImagePublicURL.Valid {
		return model.NewImageWithPublicURL(user.ProfileImagePublicURL.String).GetCDNURL(), true
	}
	return gravatarMysteryPersonURL, false
}

// GetImageURL returns the user's image URL.
func (s *Service) GetImageURL(user *model.User) (string, error) {
	options := clerkimages.NewProxyOrDefaultOptions(user.ProfileImagePublicURL.Ptr(), user.InstanceID, user.GetInitials(), user.ID)
	return clerkimages.GenerateImageURL(options)
}

func (s *Service) HasVerifiedEmail(ctx context.Context, exec database.Executor, userID string) (bool, error) {
	return s.identificationRepo.ExistsVerifiedByTypeAndUser(ctx, exec, constants.ITEmailAddress, userID)
}

func (s *Service) HasVerifiedPhone(ctx context.Context, exec database.Executor, userID string) (bool, error) {
	return s.identificationRepo.ExistsVerifiedByTypeAndUser(ctx, exec, constants.ITPhoneNumber, userID)
}

// HasTwoFactorEnabled denotes if the user has enabled any MFA method other than Backup codes
func (s *Service) HasTwoFactorEnabled(ctx context.Context, exec database.Executor, userSettings *usersettings.UserSettings, userID string) (bool, error) {
	totpEnabled, err := s.HasTwoFactorTOTPEnabled(ctx, exec, userSettings, userID)
	if err != nil {
		return false, err
	}
	if totpEnabled {
		return true, nil
	}

	return s.HasTwoFactorPhoneCodeEnabled(ctx, exec, userSettings, userID)
}

// HasTwoFactorPhoneCodeEnabled denotes if the user has enabled MFA via phone code
func (s *Service) HasTwoFactorPhoneCodeEnabled(ctx context.Context, exec database.Executor, userSettings *usersettings.UserSettings, userID string) (bool, error) {
	if !userSettings.SecondFactors().Contains(constants.VSPhoneCode) {
		return false, nil
	}

	return s.identificationRepo.ExistsSecondFactorByUser(ctx, exec, userID)
}

// HasTwoFactorTOTPEnabled denotes if the user has enabled MFA via TOTP
func (s *Service) HasTwoFactorTOTPEnabled(ctx context.Context, exec database.Executor, userSettings *usersettings.UserSettings, userID string) (bool, error) {
	if !userSettings.SecondFactors().Contains(constants.VSTOTP) {
		return false, nil
	}

	return s.totpRepo.ExistsVerifiedByUser(ctx, exec, userID)
}

// HasTwoFactorBackupCodeEnabled denotes if the user has enabled MFA via backup code
func (s *Service) HasTwoFactorBackupCodeEnabled(ctx context.Context, exec database.Executor, userSettings *usersettings.UserSettings, userID string) (bool, error) {
	if !userSettings.SecondFactors().Contains(constants.VSBackupCode) {
		return false, nil
	}

	return s.backupCodeRepo.ExistsByUser(ctx, exec, userID)
}
