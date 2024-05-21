package users

import (
	"context"
	"strconv"
	"strings"
	"time"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/pagination"
	"clerk/api/shared/serializable"
	"clerk/model/sqbmodel"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/set"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type ListService struct {
	db                  database.Database
	serializableService *serializable.Service
	userRepo            *repository.Users
}

func NewListService(clock clockwork.Clock, db database.Database) *ListService {
	return &ListService{
		db:                  db,
		serializableService: serializable.NewService(clock),
		userRepo:            repository.NewUsers(),
	}
}

type readAllParams struct {
	externalIDs       []string
	userIDs           []string
	organizationIDs   []string
	emailAddresses    []string
	phoneNumbers      []string
	usernames         []string
	web3Wallets       []string
	lastActiveAtSince []string
	query             string
	orderBy           string
}

var validUsersOrderByFields = set.New(
	sqbmodel.UserColumns.CreatedAt,
	sqbmodel.UserColumns.UpdatedAt,
	sqbmodel.UserColumns.LastSignInAt,
	"last_active_at",
	repository.OrderFieldEmailAddress,
	repository.OrderFieldFirstName,
	repository.OrderFieldLastName,
	repository.OrderFieldUsername,
	repository.OrderFieldPhoneNumber,
	repository.OrderFieldWeb3Wallet,
)

func (r readAllParams) convertToUserMods() (repository.UsersFindAllModifiers, apierror.Error) {
	var mods repository.UsersFindAllModifiers

	if r.orderBy != "" {
		orderByField, err := repository.ConvertToOrderByField(r.orderBy, validUsersOrderByFields)
		if err != nil {
			return mods, err
		}
		mods.OrderBy = &orderByField
	}

	// Normalize usernames, they are case-insensitive.
	mods.Usernames = make([]string, len(r.usernames))
	for i, username := range r.usernames {
		mods.Usernames[i] = strings.ToLower(username)
	}

	mods.UserIDs = repository.NewParamsWithExclusion(r.userIDs...)
	mods.ExternalIDs = repository.NewParamsWithExclusion(r.externalIDs...)
	mods.OrganizationIDs = repository.NewParamsWithExclusion(r.organizationIDs...)
	mods.EmailAddresses = r.emailAddresses
	mods.PhoneNumbers = r.phoneNumbers
	mods.Web3Wallets = r.web3Wallets
	mods.Query = r.query

	if len(r.lastActiveAtSince) > 0 && r.lastActiveAtSince[0] != "" {
		v, err := strconv.ParseInt(r.lastActiveAtSince[0], 10, 64)
		if err != nil {
			return mods, apierror.FormInvalidDate("last_active_at_since")
		}
		if v%86400 > 0 {
			return mods, apierror.FormInvalidDate("last_active_at_since")
		}
		mods.LastActiveAtSince = time.UnixMilli(v).UTC()
	}

	return mods, nil
}

func (r *readAllParams) normalize() {
	emails := make([]string, len(r.emailAddresses))

	for i, email := range r.emailAddresses {
		emails[i] = strings.ToLower(email)
	}

	r.emailAddresses = emails
}

// ReadAll returns all users for the given instance.
func (s *ListService) ReadAll(ctx context.Context, readParams readAllParams, pagination pagination.Params) ([]*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	readParams.normalize()

	findAllParams, apierr := readParams.convertToUserMods()
	if apierr != nil {
		return nil, apierr
	}

	users, err := s.userRepo.FindAllWithModifiers(ctx, s.db, env.Instance.ID, findAllParams, pagination)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	userSerializables, err := s.serializableService.ConvertUsers(ctx, s.db, userSettings, users)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	userResponses := make([]*serialize.UserResponse, len(users))
	for i, userSerializable := range userSerializables {
		userResponses[i] = serialize.UserToServerAPI(ctx, userSerializable)
	}

	return userResponses, nil
}

// CountAll returns the total count of users in the given instance given
// the supplied parameters.
func (s *Service) CountAll(ctx context.Context, params readAllParams) (*serialize.TotalCountResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	params.normalize()

	countByParams, apiErr := params.convertToUserMods()
	if apiErr != nil {
		return nil, apiErr
	}

	totalCount, err := s.userRepo.CountByModifiers(ctx, s.db, env.Instance.ID, countByParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.TotalCount(totalCount), nil
}
