package validators

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/validate"
)

type Service struct {
	// repositories
	identificationRepo *repository.Identification
	userRepo           *repository.Users
}

func NewService() *Service {
	return &Service{
		identificationRepo: repository.NewIdentification(),
		userRepo:           repository.NewUsers(),
	}
}

// ValidateEmailAddress validates whether the given email address is well-formed and unique in the context of the
// given instance and user.
func (s *Service) ValidateEmailAddress(ctx context.Context, exec database.Executor, emailAddress, instanceID string, userID *string, canBeReserved bool, emailAddressParam string) (apierror.Error, error) {
	if err := validate.EmailAddress(emailAddress, emailAddressParam); err != nil {
		return err, nil
	}

	if userID != nil {
		exists, err := s.identificationRepo.ExistsByIdentifierAndUser(ctx, exec, emailAddress, constants.ITEmailAddress, *userID)
		if err != nil {
			return nil, err
		}
		if exists {
			return apierror.FormIdentifierExists(emailAddressParam), nil
		}
	}

	return s.validateUniqueness(ctx, exec, instanceID, emailAddress, constants.ITEmailAddress, canBeReserved, emailAddressParam)
}

// ValidatePhoneNumber validates whether the given phone number is well-formed and unique in the context of the
// given instance and user.
func (s *Service) ValidatePhoneNumber(ctx context.Context, exec database.Executor, phoneNumber, instanceID string, userID *string, canBeReserved bool, phoneNumberParam string) (apierror.Error, error) {
	if err := validate.PhoneNumber(phoneNumber, phoneNumberParam); err != nil {
		return err, nil
	}

	if userID != nil {
		exists, err := s.identificationRepo.ExistsByIdentifierAndUser(ctx, exec, phoneNumber, constants.ITPhoneNumber, *userID)
		if err != nil {
			return nil, err
		}
		if exists {
			return apierror.FormIdentifierExists(phoneNumberParam), nil
		}
	}

	return s.validateUniqueness(ctx, exec, instanceID, phoneNumber, constants.ITPhoneNumber, canBeReserved, phoneNumberParam)
}

// ValidateUsername checks whether the given username is valid and unique in the context of the given instance
func (s *Service) ValidateUsername(ctx context.Context, exec database.Executor, username string, instanceID string) (apierror.Error, error) {
	if validUsernameErr := validate.Username(username, param.Username.Name); validUsernameErr != nil {
		return validUsernameErr, nil
	}

	return s.validateUniqueness(ctx, exec, instanceID, username, constants.ITUsername, false, param.Username.Name)
}

// ValidateWeb3Wallet checks whether the given web3 wallet address is valid and unique in the context of the given instance
func (s *Service) ValidateWeb3Wallet(ctx context.Context, exec database.Executor, web3Wallet string, instanceID string) (apierror.Error, error) {
	if err := validate.Web3Wallet(web3Wallet, param.Web3Wallet.Name); err != nil {
		return err, nil
	}

	return s.validateUniqueness(ctx, exec, instanceID, web3Wallet, constants.ITWeb3Wallet, false, param.Web3Wallet.Name)
}

// IsUniqueIdentifier verifies whether the given identifier is unique to the instance
func (s *Service) IsUniqueIdentifier(
	ctx context.Context,
	exec database.Executor,
	identifier, identificationType, instanceID string,
	canBeReserved bool,
) (bool, error) {
	if !model.IsValidIdentificationType(identificationType) {
		return false, fmt.Errorf("validator/isUniqueIdentifier: invalid identification %s", identificationType)
	}

	var exists bool
	var err error
	if !canBeReserved {
		exists, err = s.identificationRepo.ExistsVerifiedByIdentifierAndType(ctx, exec, identifier, identificationType, instanceID)
	} else {
		exists, err = s.identificationRepo.ExistsVerifiedOrReservedByIdentifierAndType(ctx, exec, identifier, identificationType, instanceID)
	}
	if err != nil {
		return false, fmt.Errorf("validator/isUniqueIdentifier: exists verified identification (%s, %s, canBeReserved=%v) on instance %s: %w",
			identifier, identificationType, canBeReserved, instanceID, err)
	}
	return !exists, nil
}

func (s *Service) validateUniqueness(
	ctx context.Context,
	exec database.Executor,
	instanceID string,
	identifier string,
	identificationType string,
	canBeReserved bool,
	paramForError string,
) (apierror.Error, error) {
	isUnique, err := s.IsUniqueIdentifier(ctx, exec, identifier, identificationType, instanceID, canBeReserved)
	if err != nil {
		return nil, err
	}
	if !isUnique {
		return apierror.FormIdentifierExists(paramForError), nil
	}

	return nil, nil
}
