package serializable

import (
	"context"
	"fmt"

	"clerk/api/shared/user_profile"
	"clerk/model"
	"clerk/pkg/constants"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	clock clockwork.Clock

	// services
	userProfileService *user_profile.Service

	// repositories
	identificationRepo *repository.Identification
}

func NewService(clock clockwork.Clock) *Service {
	return &Service{
		clock:              clock,
		identificationRepo: repository.NewIdentification(),
		userProfileService: user_profile.NewService(clock),
	}
}

type RowUserSerializable struct {
	User *model.User

	PrimaryEmailAddress *string
	PrimaryPhoneNumber  *string
	PrimaryWeb3Wallet   *string
	ImageURL            string
	Username            *string
	Locked              bool
	Identifier          string
}

func (s *Service) ConvertUsers(ctx context.Context, exec database.Database, userSettings *usersettings.UserSettings, users []*model.User) ([]*RowUserSerializable, error) {
	if len(users) == 0 {
		return []*RowUserSerializable{}, nil
	}

	identificationsByUser, err := s.fetchIdentificationsForUsers(ctx, exec, users)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all identifications for users %v: %w", users, err)
	}

	responses := make([]*RowUserSerializable, len(users))

	for i, user := range users {
		responses[i] = &RowUserSerializable{User: user}
		responses[i].Locked = user.LockoutStatus(s.clock, userSettings.AttackProtection.UserLockout).Locked

		imageURL, err := s.userProfileService.GetImageURL(user)
		if err != nil {
			return nil, fmt.Errorf("failed to get image url for user %s: %w", user.ID, err)
		}
		responses[i].ImageURL = imageURL

		if identificationsByUser[user.ID][constants.ITEmailAddress] != nil {
			responses[i].PrimaryEmailAddress = identificationsByUser[user.ID][constants.ITEmailAddress].Identifier.Ptr()
		}
		if identificationsByUser[user.ID][constants.ITPhoneNumber] != nil {
			responses[i].PrimaryPhoneNumber = identificationsByUser[user.ID][constants.ITPhoneNumber].Identifier.Ptr()
		}
		if identificationsByUser[user.ID][constants.ITUsername] != nil {
			responses[i].Username = identificationsByUser[user.ID][constants.ITUsername].Identifier.Ptr()
		}
		if identificationsByUser[user.ID][constants.ITWeb3Wallet] != nil {
			responses[i].PrimaryWeb3Wallet = identificationsByUser[user.ID][constants.ITWeb3Wallet].Identifier.Ptr()
		}

		responses[i].Identifier = s.getIdentifier(responses[i])
	}

	return responses, nil
}

// getIdentifier returns the user's first found primary identifier by the following order (email, phone, web3 wallet, username)
func (s *Service) getIdentifier(user *RowUserSerializable) string {
	if user.PrimaryEmailAddress != nil {
		return *user.PrimaryEmailAddress
	}

	if user.PrimaryPhoneNumber != nil {
		return *user.PrimaryPhoneNumber
	}

	if user.Username != nil {
		return *user.Username
	}

	if user.PrimaryWeb3Wallet != nil {
		return *user.PrimaryWeb3Wallet
	}

	return user.User.ID
}

func (s *Service) fetchIdentificationsForUsers(
	ctx context.Context,
	exec database.Executor,
	users []*model.User,
) (map[string]map[string]*model.Identification, error) {
	usersMap := make(map[string]*model.User, len(users))
	userIDs := make([]string, len(users))

	for i, user := range users {
		usersMap[user.ID] = user
		userIDs[i] = user.ID
	}

	allIdentifications, err := s.identificationRepo.FindAllByUsers(ctx, exec, userIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all identifications for users %v: %w", userIDs, err)
	}

	groupedIdentifications := make(map[string]map[string]*model.Identification)
	for _, identification := range allIdentifications {
		if !identification.UserID.Valid {
			continue
		}

		userID := identification.UserID.String
		if _, exists := groupedIdentifications[userID]; !exists {
			groupedIdentifications[userID] = make(map[string]*model.Identification)
		}

		switch identification.Type {
		case constants.ITUsername:
			{
				groupedIdentifications[userID][identification.Type] = identification
			}
		case constants.ITEmailAddress, constants.ITPhoneNumber, constants.ITWeb3Wallet:
			{
				if identification.IsUserPrimary(usersMap[userID]) {
					groupedIdentifications[userID][identification.Type] = identification
				}
			}
		}
	}
	return groupedIdentifications, nil
}
