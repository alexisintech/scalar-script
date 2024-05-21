package identifications

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/orgdomain"
	"clerk/api/shared/serializable"
	"clerk/api/shared/sessions"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/oauth"
	"clerk/pkg/set"
	"clerk/pkg/unverifiedemails"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

var ErrIdentifierAlreadyExists = errors.New("identifier already exists")

type Service struct {
	clock clockwork.Clock

	eventService        *events.Service
	orgDomainService    *orgdomain.Service
	serializableService *serializable.Service
	sessionService      *sessions.Service

	identificationRepo *repository.Identification
	userRepo           *repository.Users
	verificationRepo   *repository.Verification
	orgInvitationRepo  *repository.OrganizationInvitation
	orgSuggestionRepo  *repository.OrganizationSuggestion
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:               deps.Clock(),
		eventService:        events.NewService(deps),
		orgDomainService:    orgdomain.NewService(deps.Clock()),
		serializableService: serializable.NewService(deps.Clock()),
		sessionService:      sessions.NewService(deps),
		identificationRepo:  repository.NewIdentification(),
		userRepo:            repository.NewUsers(),
		verificationRepo:    repository.NewVerification(),
		orgInvitationRepo:   repository.NewOrganizationInvitation(),
		orgSuggestionRepo:   repository.NewOrganizationSuggestion(),
	}
}

type CreateIdentificationData struct {
	InstanceID             string
	Identifier             string
	CanonicalIdentifier    *string
	Type                   string
	ReserveForSecondFactor bool
	UserID                 *string
}

func (s *Service) CreateIdentification(
	ctx context.Context,
	exec database.Executor,
	data CreateIdentificationData,
) (*model.Identification, error) {
	if data.UserID != nil {
		unverifiedIdentifications, err := s.identificationRepo.CountUnverifiedByUser(ctx, exec, *data.UserID)
		if err != nil {
			return nil, fmt.Errorf("createIdentification: count unverified identifications for user %s: %w",
				*data.UserID, err)
		}

		if unverifiedIdentifications >= 5 {
			return nil, apierror.TooManyUnverifiedIdentifications()
		}
	}

	identification := &model.Identification{Identification: &sqbmodel.Identification{
		InstanceID:              data.InstanceID,
		Type:                    data.Type,
		Identifier:              null.StringFrom(data.Identifier),
		ReservedForSecondFactor: data.ReserveForSecondFactor,
		UserID:                  null.StringFromPtr(data.UserID),
		Status:                  constants.ISNotSet,
	}}

	identification.SetCanonicalIdentifier()

	// passkeys do not have user inputted identifier values
	if data.Type == constants.ITPasskey {
		identification.Identifier = null.StringFromPtr(nil)
	}

	err := s.identificationRepo.Insert(ctx, exec, identification)
	if err != nil {
		return nil, err
	}

	return identification, nil
}

func (s *Service) CreateUsername(
	ctx context.Context,
	exec database.Executor,
	username string,
	user *model.User,
	instanceID string,
) (*model.Identification, error) {
	username = strings.ToLower(username)

	verification := &model.Verification{Verification: &sqbmodel.Verification{
		InstanceID: instanceID,
		Strategy:   constants.ITUsername,
		Attempts:   1,
	}}

	err := s.verificationRepo.Insert(ctx, exec, verification)
	if err != nil {
		return nil, err
	}

	identification := &model.Identification{Identification: &sqbmodel.Identification{
		InstanceID:     instanceID,
		Type:           constants.ITUsername,
		VerificationID: null.StringFrom(verification.ID),
		Identifier:     null.StringFrom(username),
		Status:         constants.ISVerified,
	}}

	if user != nil {
		identification.UserID = null.StringFrom(user.ID)
	}

	err = s.identificationRepo.Insert(ctx, exec, identification)
	if err != nil {
		return nil, err
	}

	verification.IdentificationID = null.StringFrom(identification.ID)
	err = s.verificationRepo.UpdateIdentificationID(ctx, exec, verification)
	if err != nil {
		return nil, err
	}

	return identification, err
}

// Delete attempts to delete the identification with the provided
// identificationID for the user specified with userID.
// Touches the user upon successful email deletion.
// Identifications with linked parent identifications cannot be deleted.
func (s *Service) Delete(
	ctx context.Context,
	tx database.Tx,
	ins *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User,
	ident *model.Identification,
) (*serialize.DeletedObjectResponse, apierror.Error) {
	// if identification is a verified email, delete all related org domain invitations & suggestions
	err := s.orgDomainService.DeletePendingInvitationsAndSuggestionsForVerifiedEmail(ctx, tx, ident)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Delete identification and touch user.
	if err := s.attemptDelete(ctx, tx, ident, user, userSettings, ins.ID); err != nil {
		return nil, err
	}

	// Trigger a user.updated event
	err = s.sendUserUpdatedEvent(ctx, tx, ins, userSettings, user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.DeletedObject(ident.ID, ident.Type), nil
}

// attemptDelete will check the user's auth config and determine whether the
// identification can be deleted or not. The user will be touched as well.
// In case the identification to be deleted is the user's primary, a new
// primary identification of the same type will be set for the user.
func (s *Service) attemptDelete(
	ctx context.Context,
	tx database.Tx,
	identification *model.Identification,
	user *model.User,
	userSettings *usersettings.UserSettings,
	instanceID string,
) apierror.Error {
	// Can't delete any identifications that have a parent.
	parentIdentifications, err := s.identificationRepo.FindAllByTargetIdentificationID(ctx, tx, identification.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if len(parentIdentifications) > 0 {
		return apierror.DeleteLinkedCommNotAllowed()
	}

	// Can't delete user's only identification. Doesn't include OAuth identifications, so in
	// case of an OAuth identification we can safely skip this check
	allIdents, err := s.identificationRepo.FindAllNonOAuthByUser(ctx, tx, user.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if len(allIdents) == 1 && !identification.IsOAuth() {
		return apierror.LastIdentificationDeletionFailed()
	}

	apiErr := s.ensureNotLastRequiredIdentification(ctx, tx, identification, userSettings, user.ID)
	if apiErr != nil {
		return apiErr
	}

	// Lock the user and all entries with a foreign key pointing to it, identifications in our case, to protect
	// against a possible race condition and guarantee there is always at least an identification left
	if _, err = s.userRepo.SelectForUpdateByID(ctx, tx, user.ID); err != nil {
		return apierror.Unexpected(err)
	}

	requiredIdentifications, err := s.CanBeUsedForUserAuthentication(ctx, tx, user.ID, userSettings)
	if err != nil {
		return apierror.Unexpected(fmt.Errorf("identifications/attemptDelete: retrieving required identifications for %s: %w",
			user.ID, err))
	}

	if len(requiredIdentifications) == 1 && requiredIdentifications[0].ID == identification.ID {
		// trying to delete the last identification that can identify the given user based
		// on the identification strategies defined in the instance
		return apierror.LastIdentificationDeletionFailed()
	}

	if !identification.IsOAuth() {
		switch identification.Type {
		case constants.ITEmailAddress:
			userEmails, err := s.identificationRepo.FindAllByUserAndType(ctx, tx, instanceID, user.ID, constants.ITEmailAddress)
			if err != nil {
				return apierror.Unexpected(err)
			}

			// The email being deleted is the user's primary identification. Set the
			// user's PrimaryEmailAddressID accordingly.
			if identification.ID == user.PrimaryEmailAddressID.String {
				user.PrimaryEmailAddressID = selectNewPrimaryIdentificationID(userEmails, identification.ID)
			}
		case constants.ITPhoneNumber:
			userPhones, err := s.identificationRepo.FindAllByUserAndType(ctx, tx, instanceID, user.ID, constants.ITPhoneNumber)
			if err != nil {
				return apierror.Unexpected(err)
			}

			// The phone being deleted is the user's primary identification. Set the
			// user's PrimaryPhoneNumberID accordingly.
			if identification.ID == user.PrimaryPhoneNumberID.String {
				user.PrimaryPhoneNumberID = selectNewPrimaryIdentificationID(userPhones, identification.ID)
			}
		case constants.ITWeb3Wallet:
			web3Wallets, err := s.identificationRepo.FindAllByUserAndType(ctx, tx, instanceID, user.ID, constants.ITWeb3Wallet)
			if err != nil {
				return apierror.Unexpected(err)
			}
			// The web3 wallet being deleted is the user's primary identification. Set the
			// user's PrimaryWeb3WalletID accordingly.
			if identification.ID == user.PrimaryWeb3WalletID.String {
				user.PrimaryWeb3WalletID = selectNewPrimaryIdentificationID(web3Wallets, identification.ID)
			}

		case constants.ITUsername:
			user.UsernameID = null.StringFromPtr(nil)

		case constants.ITPasskey:
			// TODO(mary): update user resource to include passkeys

		default:
			return apierror.Unexpected(fmt.Errorf("unexpected identification type %s", identification.Type))
		}
	}

	if err = s.identificationRepo.DeleteByID(ctx, tx, identification.ID); err != nil {
		return apierror.Unexpected(err)
	}

	// check if the deleted identification was the default 2FA, if it was choose another one for default
	if identification.DefaultSecondFactor {
		secondFactors, err := s.identificationRepo.FindAllSecondFactorsByUser(ctx, tx, user.ID)
		if err != nil {
			return apierror.Unexpected(err)
		}
		if len(secondFactors) > 0 {
			newDefaultSecondFactor := secondFactors[0]
			newDefaultSecondFactor.DefaultSecondFactor = true
			if err := s.identificationRepo.UpdateDefaultSecondFactor(ctx, tx, newDefaultSecondFactor); err != nil {
				return apierror.Unexpected(err)
			}
		}
	}

	// Touch user
	err = s.userRepo.Update(ctx, tx, user)
	if err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}

func (s *Service) ensureNotLastRequiredIdentification(ctx context.Context, tx database.Tx, ident *model.Identification, userSettings *usersettings.UserSettings, userID string) apierror.Error {
	if !userSettings.GetAttribute(names.AttributeName(ident.Type)).Base().Required {
		return nil
	}
	if !ident.IsClaimed() {
		return nil
	}

	totalIdents, err := s.identificationRepo.CountByUserAndTypeAndClaimed(ctx, tx, userID, ident.Type)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if totalIdents == 1 {
		return apierror.LastRequiredIdentificationDeletionFailed(ident.Type)
	}

	return nil
}

// CanBeUsedForUserAuthentication returns all the identifications of the given user
// which can be used to authenticate him based on the identification strategies
// defined in the given auth config.
func (s *Service) CanBeUsedForUserAuthentication(
	ctx context.Context,
	exec database.Executor,
	userID string,
	userSettings *usersettings.UserSettings,
) ([]*model.Identification, error) {
	identifications, err := s.identificationRepo.FindAllFirstFactorsByUser(ctx, exec, userID)
	if err != nil {
		return nil, fmt.Errorf("identificationsUsedAsFirstFactors: fetching all first factors for user %s: %w",
			userID, err)
	}

	identificationStrategies := userSettings.IdentificationStrategies()
	var requiredIdentifications []*model.Identification
	for _, identification := range identifications {
		if identificationStrategies.Contains(identification.Type) {
			requiredIdentifications = append(requiredIdentifications, identification)
		}
	}
	return requiredIdentifications, nil
}

func getMostRecentVerifiedIdentification(idents []*model.Identification) *model.Identification {
	var mostRecent *model.Identification
	for _, ident := range idents {
		if ident.IsVerified() && (mostRecent == nil || ident.CreatedAt.After(mostRecent.CreatedAt)) {
			mostRecent = ident
		}
	}
	return mostRecent
}

// Pick the most recent identification from idents and return its ID. If there's none,
// just return a null ID.
func selectNewPrimaryIdentificationID(idents []*model.Identification, currentID string) null.String {
	newID := null.StringFromPtr(nil)

	var otherIdents []*model.Identification
	for _, ident := range idents {
		if ident.ID != currentID {
			otherIdents = append(otherIdents, ident)
		}
	}
	mostRecentIdent := getMostRecentVerifiedIdentification(otherIdents)
	if mostRecentIdent != nil {
		newID = null.StringFrom(mostRecentIdent.ID)
	}

	return newID
}

// FinalizeVerification will mark the provided Identification as verified and
// check if it needs to be the primary identification for the associated User.
// It'll also take care of figuring out if the newly verified identification
// should be the default 2FA method for the user.
func (s *Service) FinalizeVerification(ctx context.Context, tx database.Tx, ident *model.Identification, instance *model.Instance, userSettings *usersettings.UserSettings) error {
	verifiedExists, err := s.identificationRepo.ExistsVerifiedByIdentifierAndType(ctx, tx, ident.Identifier.String, ident.Type, instance.ID)
	if err != nil {
		return err
	}
	if verifiedExists {
		return ErrIdentifierAlreadyExists
	}

	reserved, err := s.identificationRepo.QueryReservedByInstanceAndIdentifier(ctx, tx, instance.ID, ident.Identifier.String)
	if err != nil {
		return err
	}

	if reserved != nil && reserved.UserID.String != ident.UserID.String {
		return ErrIdentifierAlreadyExists
	}

	err = s.updateVerifiedIdentification(ctx, tx, ident)
	if err != nil {
		return err
	}

	user, err := s.userRepo.FindByID(ctx, tx, ident.UserID.String)
	if err != nil {
		return err
	}

	err = s.RestoreUserReservedAndPrimaryIdentifications(ctx, tx, ident, userSettings, instance.ID, user)
	if err != nil {
		return err
	}

	// Trigger user.updated event
	if err = s.sendUserUpdatedEvent(ctx, tx, instance, userSettings, user); err != nil {
		return fmt.Errorf("FinalizeVerification: send user updated event for (%+v, %+v): %w", user, instance.ID, err)
	}

	return nil
}

func (s Service) RestoreUserReservedAndPrimaryIdentifications(ctx context.Context, exec database.Executor, ident *model.Identification, userSettings *usersettings.UserSettings, instanceID string, user *model.User) error {
	var err error

	if err = s.updateUserPrimaryIdentification(ctx, exec, ident, user); err != nil {
		return err
	}

	// In case of contact info (email or phone), restore any reserved user identifications
	// to non-reserved. The user now has at least one verified and can successfully sign-in,
	// and there is no need to lock other identifications as well
	attribute := userSettings.GetAttribute(names.AttributeName(ident.Type))
	if !attribute.IsVerifiable() || !attribute.UsedAsContactInfo() {
		return nil
	}

	return s.identificationRepo.RestoreReservedByInstanceAndUserAndType(ctx, exec, instanceID, user.ID, ident.Type)
}

// updateVerifiedIdentification performs all the necessary updates for a verified
// Identification. It marks it as verified and if needed sets it as the
// default 2FA method.
func (s *Service) updateVerifiedIdentification(ctx context.Context, exec database.Executor, ident *model.Identification) error {
	ident.Status = constants.ISVerified
	if err := s.identificationRepo.UpdateStatus(ctx, exec, ident); err != nil {
		return err
	}

	if ident.ReservedForSecondFactor {
		// check if it's the only 2FA for this user, if it is add it as the default
		defaultSecondFactor, err := s.identificationRepo.QueryDefaultSecondFactorByUser(ctx, exec, ident.UserID.String)
		if err != nil {
			return err
		}
		if defaultSecondFactor == nil {
			ident.DefaultSecondFactor = true
			if err := s.identificationRepo.UpdateDefaultSecondFactor(ctx, exec, ident); err != nil {
				return err
			}
		}
	}
	return nil
}

// updateUserPrimaryIdentification performs all the necessary update on a
// verified identification's user. The model.Identification and model.User
// are accepted as parameters.
func (s *Service) updateUserPrimaryIdentification(ctx context.Context, exec database.Executor, ident *model.Identification, user *model.User) error {
	primaryIdents, err := s.identificationRepo.FindAllByID(ctx, exec, user.PrimaryIdentificationIDs()...)
	if err != nil {
		return err
	}

	reservedIdentificationsIDs := set.New[string]()
	userUpdateCols := set.New[string]()
	for _, primaryIdent := range primaryIdents {
		if primaryIdent.IsReserved() {
			reservedIdentificationsIDs.Insert(primaryIdent.ID)
		}
	}

	if user.PrimaryEmailAddressID.Valid && reservedIdentificationsIDs.Contains(user.PrimaryEmailAddressID.String) {
		user.PrimaryEmailAddressID = null.StringFromPtr(nil)
		userUpdateCols.Insert(sqbmodel.UserColumns.PrimaryEmailAddressID)
	}

	if user.PrimaryPhoneNumberID.Valid && reservedIdentificationsIDs.Contains(user.PrimaryPhoneNumberID.String) {
		user.PrimaryPhoneNumberID = null.StringFromPtr(nil)
		userUpdateCols.Insert(sqbmodel.UserColumns.PrimaryPhoneNumberID)
	}

	if user.PrimaryWeb3WalletID.Valid && reservedIdentificationsIDs.Contains(user.PrimaryWeb3WalletID.String) {
		user.PrimaryWeb3WalletID = null.StringFromPtr(nil)
		userUpdateCols.Insert(sqbmodel.UserColumns.PrimaryWeb3WalletID)
	}

	// Check if we need to set the identification as the user's primary
	if ident.IsEmailAddress() && !user.PrimaryEmailAddressID.Valid {
		user.PrimaryEmailAddressID = null.StringFrom(ident.ID)
		userUpdateCols.Insert(sqbmodel.UserColumns.PrimaryEmailAddressID)
	}

	if ident.IsPhoneNumber() && !user.PrimaryPhoneNumberID.Valid {
		user.PrimaryPhoneNumberID = null.StringFrom(ident.ID)
		userUpdateCols.Insert(sqbmodel.UserColumns.PrimaryPhoneNumberID)
	}

	if ident.IsWeb3Wallet() && !user.PrimaryWeb3WalletID.Valid {
		user.PrimaryWeb3WalletID = null.StringFrom(ident.ID)
		userUpdateCols.Insert(sqbmodel.UserColumns.PrimaryWeb3WalletID)
	}
	if userUpdateCols.IsEmpty() {
		return nil
	}
	return s.userRepo.Update(ctx, exec, user, userUpdateCols.Array()...)
}

// InitiateReVerifyFlow initiates re-verification for an external account by initiating the RequiresVerification value.
func (s *Service) InitiateReVerifyFlow(ctx context.Context, exec database.Executor, oauthUser *oauth.User, extAccIdent, emailIdent *model.Identification, userSettings *usersettings.UserSettings) error {
	if shouldSkipReVerifyFlow(oauthUser, extAccIdent, emailIdent, userSettings) {
		return nil
	}

	// We're setting the RequiresVerification to false, to allow the email identification owner to complete
	// the flow, before switching to true.
	extAccIdent.RequiresVerification = null.BoolFrom(false)
	return s.identificationRepo.Update(ctx, exec, extAccIdent, sqbmodel.IdentificationColumns.RequiresVerification)
}

func shouldSkipReVerifyFlow(oauthUser *oauth.User, extAccIdent, emailIdent *model.Identification, userSettings *usersettings.UserSettings) bool {
	// check if the external account is associated with OAuth
	if !extAccIdent.IsOAuth() {
		return true
	}

	// check if the email address has been verified
	if oauthUser.EmailAddressVerified || emailIdent.IsVerified() {
		return true
	}

	// skip verification if it's enforced or unavailable based on user settings
	// * enforced means that the instance verifies all unverified email addresses at sign-up
	// * unavailable means that the instance don't have email addresses enabled
	if unverifiedemails.IsVerifyFlowEnforcedByUserSettings(userSettings) ||
		!unverifiedemails.IsVerifyFlowAvailableByUserSettings(userSettings) {
		return true
	}

	return false
}

// FinalizeReVerifyFlow completes the re-verification process for external accounts
// by setting RequiresVerification to true and updating the verification error.
func (s *Service) FinalizeReVerifyFlow(ctx context.Context, tx database.Tx, instanceID, userID string) error {
	idents, err := s.identificationRepo.FindAllByInstanceAndUser(ctx, tx, instanceID, userID)
	if err != nil {
		return err
	}

	var needsRevoke bool

	for _, ident := range idents {
		// We only want to change the require to true, if it's filled and it's false.
		if !ident.RequiresVerification.Valid || ident.RequiresVerification.Bool || !ident.IsOAuth() {
			continue
		}

		targetIdent := getTargetIdentification(idents, ident)
		if targetIdent == nil || !targetIdent.IsVerified() {
			continue
		}

		// If the user has a ClerkJS version that supports verify flow for external accounts,
		// we'll enforce it, else we're going to reset and rely on the email.
		if unverifiedemails.IsVerifyFlowSupportedByClerkJSVersion(ctx) {
			ident.RequiresVerification = null.BoolFrom(true)
			if err = s.identificationRepo.Update(ctx, tx, ident); err != nil {
				return err
			}
		} else {
			ident.RequiresVerification = null.BoolFromPtr(nil)
			if err = s.identificationRepo.Update(ctx, tx, ident, sqbmodel.IdentificationColumns.RequiresVerification); err != nil {
				return err
			}
		}

		ver, err := s.verificationRepo.FindByID(ctx, tx, ident.VerificationID.String)
		if err != nil {
			return err
		}

		if err = ver.SetCustomError(apierror.ExternalAccountEmailAddressVerificationRequired()); err != nil {
			return err
		}

		if err = s.verificationRepo.UpdateError(ctx, tx, ver); err != nil {
			return err
		}

		needsRevoke = true
	}

	if !needsRevoke {
		return nil
	}

	return s.sessionService.RevokeAllForUserID(ctx, instanceID, userID)
}

func getTargetIdentification(idents []*model.Identification, ident *model.Identification) *model.Identification {
	if !ident.TargetIdentificationID.Valid {
		return nil
	}

	targetIdentificationID := ident.TargetIdentificationID.String
	for _, i := range idents {
		if targetIdentificationID == i.ID {
			return i
		}
	}
	return nil
}

func (s *Service) sendUserUpdatedEvent(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User,
) error {
	userSerializable, err := s.serializableService.ConvertUser(ctx, exec, userSettings, user)
	if err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: serializing user %+v: %w", user, err)
	}

	if err = s.eventService.UserUpdated(ctx, exec, instance, serialize.UserToServerAPI(ctx, userSerializable)); err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: send user updated event for user %s: %w", user.ID, err)
	}
	return nil
}

func FindUserIDIfExists(identifications ...*model.Identification) *string {
	for _, identification := range identifications {
		if identification != nil && identification.UserID.Valid {
			return identification.UserID.Ptr()
		}
	}
	return nil
}
