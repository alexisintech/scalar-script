package serializable

import (
	"context"
	"fmt"

	"clerk/api/shared/orgdomain"
	"clerk/api/shared/user_profile"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/set"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	clock clockwork.Clock

	// services
	orgDomainService    *orgdomain.Service
	userProfileService  *user_profile.Service
	verificationService *verifications.Service

	// repositories
	backupCodeRepo          *repository.BackupCode
	billingPlanRepo         *repository.BillingPlans
	billingSubscriptionRepo *repository.BillingSubscriptions
	externalAccountRepo     *repository.ExternalAccount
	identificationRepo      *repository.Identification
	orgInvitationRepo       *repository.OrganizationInvitation
	orgSuggestionRepo       *repository.OrganizationSuggestion
	passkeyRepo             *repository.Passkey
	permissionRepo          *repository.Permission
	samlAccountRepo         *repository.SAMLAccount
	totpRepo                *repository.TOTP
	userRepo                *repository.Users
	verificationRepo        *repository.Verification
}

func NewService(clock clockwork.Clock) *Service {
	return &Service{
		clock:                   clock,
		orgDomainService:        orgdomain.NewService(clock),
		userProfileService:      user_profile.NewService(clock),
		verificationService:     verifications.NewService(clock),
		backupCodeRepo:          repository.NewBackupCode(),
		billingPlanRepo:         repository.NewBillingPlans(),
		billingSubscriptionRepo: repository.NewBillingSubscriptions(),
		externalAccountRepo:     repository.NewExternalAccount(),
		identificationRepo:      repository.NewIdentification(),
		orgInvitationRepo:       repository.NewOrganizationInvitation(),
		orgSuggestionRepo:       repository.NewOrganizationSuggestion(),
		passkeyRepo:             repository.NewPasskey(),
		permissionRepo:          repository.NewPermission(),
		samlAccountRepo:         repository.NewSAMLAccount(),
		totpRepo:                repository.NewTOTP(),
		userRepo:                repository.NewUsers(),
		verificationRepo:        repository.NewVerification(),
	}
}

func (s *Service) ConvertUsers(ctx context.Context, exec database.Executor, userSettings *usersettings.UserSettings, users []*model.User) ([]*model.UserSerializable, error) {
	if len(users) == 0 {
		return []*model.UserSerializable{}, nil
	}

	userIDs := make([]string, len(users))
	for i, user := range users {
		userIDs[i] = user.ID
	}

	// Fetch everything we will need up-front.
	// This is done to avoid the N+1 queries that would be required
	// if we were building for each user separately.
	allIdentifications, identificationsByUser, err := s.fetchAllIdentificationsByUser(ctx, exec, userIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all identifications for users %v: %w",
			users, err)
	}

	parentIdentificationsByIdentification, err := s.fetchAllParentIdentificationsByIdentification(ctx, exec, allIdentifications)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all parent identifications for identifications %v: %w",
			allIdentifications, err)
	}

	externalAccountsByIdentification, err := s.fetchAllExternalAccountsByIdentification(ctx, exec, allIdentifications)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all external accounts for %v: %w",
			allIdentifications, err)
	}

	samlAccountsByIdentification, err := s.fetchAllSAMLAccountsByIdentification(ctx, exec, allIdentifications)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all saml accounts for %v: %w", allIdentifications, err)
	}

	verificationsByIdentification, err := s.fetchAllVerificationsByIdentification(ctx, exec, allIdentifications)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all verifications for %v: %w",
			allIdentifications, err)
	}

	passkeysByIdentification, err := s.fetchAllPasskeysByIdentification(ctx, exec, allIdentifications)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all passkeys for %v: %w",
			allIdentifications, err)
	}

	totpsByUser := make(map[string]*model.TOTP)
	if userSettings.SecondFactors().Contains(constants.VSTOTP) {
		totpsByUser, err = s.fetchAllTOTPsByUser(ctx, exec, userIDs)
		if err != nil {
			return nil, fmt.Errorf("fetch totps by users %v: %w", userIDs, err)
		}
	}

	backupCodesByUser := make(map[string]*model.BackupCode)
	if userSettings.SecondFactors().Contains(constants.VSBackupCode) {
		backupCodesByUser, err = s.fetchAllBackupCodesByUser(ctx, exec, userIDs)
		if err != nil {
			return nil, fmt.Errorf("fetch backup codes for users %v: %w", userIDs, err)
		}
	}

	userPlanKeyByUser, err := s.fetchAllUserPlanKeysByUser(ctx, exec, users)
	if err != nil {
		return nil, fmt.Errorf("fetching user plan keys for users %v: %w", userIDs, err)
	}

	// build user serializables
	result := make([]*model.UserSerializable, len(users))
	for i, user := range users {
		userSerializable := &model.UserSerializable{User: user}

		// Get profile image URL
		userSerializable.ProfileImageURL, _ = s.userProfileService.GetProfileImageURL(user)
		userSerializable.ImageURL, err = s.userProfileService.GetImageURL(user)
		if err != nil {
			return nil, fmt.Errorf("building image url for user %s: %w", users[i].ID, err)
		}

		// Build identification serializables
		userSerializable.Identifications = s.createIdentificationSerializable(
			identificationsByUser[user.ID],
			verificationsByIdentification,
			externalAccountsByIdentification,
			samlAccountsByIdentification,
			parentIdentificationsByIdentification,
			passkeysByIdentification,
		)

		// Assign the username identification's value as the username
		usernames := userSerializable.Identifications[constants.ITUsername]
		if len(usernames) > 0 {
			userSerializable.Username = usernames[0].Username()
		}

		_, userSerializable.TOTPEnabled = totpsByUser[user.ID]
		_, userSerializable.BackupCodeEnabled = backupCodesByUser[user.ID]

		// Check if 2FA is enabled
		if userSerializable.TOTPEnabled {
			userSerializable.TwoFactorEnabled = true
		} else if userSettings.SecondFactors().Contains(constants.VSPhoneCode) {
			for _, ident := range userSerializable.Identifications[constants.ITPhoneNumber] {
				if ident.IsVerified() && ident.ReservedForSecondFactor {
					userSerializable.TwoFactorEnabled = true
					break
				}
			}
		}

		userLockoutStatus := user.LockoutStatus(s.clock, userSettings.AttackProtection.UserLockout)

		userSerializable.Locked = userLockoutStatus.Locked
		if userLockoutStatus.LockoutExpiresIn != nil {
			lockoutExpiresInSeconds := int64(userLockoutStatus.LockoutExpiresIn.Seconds())
			userSerializable.LockoutExpiresInSeconds = &lockoutExpiresInSeconds
		}
		userSerializable.VerificationAttemptsRemaining = userLockoutStatus.VerificationAttemptsRemaining

		if plan, ok := userPlanKeyByUser[user.ID]; ok {
			userSerializable.BillingPlan = &plan
		}

		result[i] = userSerializable
	}

	return result, nil
}

func (s *Service) fetchAllIdentificationsByUser(
	ctx context.Context,
	exec database.Executor,
	userIDs []string) ([]*model.Identification, map[string][]*model.Identification, error) {
	allIdentifications, err := s.identificationRepo.FindAllByUsers(ctx, exec, userIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch all identifications for users %v: %w", userIDs, err)
	}

	groupedIdentifications := make(map[string][]*model.Identification)
	for _, identification := range allIdentifications {
		if !identification.UserID.Valid {
			continue
		}
		if group := groupedIdentifications[identification.UserID.String]; group == nil {
			groupedIdentifications[identification.UserID.String] = make([]*model.Identification, 0)
		}
		groupedIdentifications[identification.UserID.String] =
			append(groupedIdentifications[identification.UserID.String], identification)
	}
	return allIdentifications, groupedIdentifications, nil
}

// fetchAllParentIdentificationsByIdentification returns a map of all parent
// identifications grouped by child identification
func (s *Service) fetchAllParentIdentificationsByIdentification(
	ctx context.Context,
	exec database.Executor,
	identifications []*model.Identification) (map[string][]*model.Identification, error) {
	identificationIDs := make([]string, len(identifications))
	for i, identification := range identifications {
		identificationIDs[i] = identification.ID
	}
	parentIdentifications, err := s.identificationRepo.FindAllByTargetIdentificationIDs(ctx, exec, identificationIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all parent identifications for %v: %w",
			identificationIDs, err)
	}

	groupedParentIdentifications := make(map[string][]*model.Identification)
	for i, parentIdentification := range parentIdentifications {
		if group := groupedParentIdentifications[parentIdentification.TargetIdentificationID.String]; group == nil {
			groupedParentIdentifications[parentIdentification.TargetIdentificationID.String] = make([]*model.Identification, 0)
		}
		groupedParentIdentifications[parentIdentification.TargetIdentificationID.String] =
			append(groupedParentIdentifications[parentIdentification.TargetIdentificationID.String], parentIdentifications[i])
	}
	return groupedParentIdentifications, nil
}

func (s *Service) fetchAllExternalAccountsByIdentification(
	ctx context.Context,
	exec database.Executor,
	identifications []*model.Identification) (map[string]*model.ExternalAccount, error) {
	externalAccountIDs := make([]string, 0)
	for _, identification := range identifications {
		if identification.ExternalAccountID.Valid {
			externalAccountIDs = append(externalAccountIDs, identification.ExternalAccountID.String)
		}
	}
	groupedExternalAccounts := make(map[string]*model.ExternalAccount)
	if len(externalAccountIDs) == 0 {
		return groupedExternalAccounts, nil
	}

	externalAccounts, err := s.externalAccountRepo.FindAllByIDs(ctx, exec, externalAccountIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all external accounts for %v: %w",
			externalAccountIDs, err)
	}
	for i, externalAccount := range externalAccounts {
		groupedExternalAccounts[externalAccount.IdentificationID] = externalAccounts[i]
	}
	return groupedExternalAccounts, nil
}

func (s *Service) fetchAllSAMLAccountsByIdentification(ctx context.Context, exec database.Executor, identifications []*model.Identification) (map[string]*model.SAMLAccountWithDeps, error) {
	samlAccountIDs := make([]string, 0)
	for _, identification := range identifications {
		if identification.SamlAccountID.Valid {
			samlAccountIDs = append(samlAccountIDs, identification.SamlAccountID.String)
		}
	}
	groupedSAMLAccounts := make(map[string]*model.SAMLAccountWithDeps)
	if len(samlAccountIDs) == 0 {
		return groupedSAMLAccounts, nil
	}

	samlAccounts, err := s.samlAccountRepo.FindAllByIDsWithDeps(ctx, exec, samlAccountIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all saml accounts for %v: %w", samlAccountIDs, err)
	}
	for _, samlAccount := range samlAccounts {
		groupedSAMLAccounts[samlAccount.IdentificationID] = samlAccount
	}
	return groupedSAMLAccounts, nil
}

func (s *Service) fetchAllVerificationsByIdentification(
	ctx context.Context,
	exec database.Executor,
	identifications []*model.Identification) (map[string]*model.Verification, error) {
	verificationIDs := make([]string, 0)
	for _, identification := range identifications {
		if identification.VerificationID.Valid {
			verificationIDs = append(verificationIDs, identification.VerificationID.String)
		}
	}
	verifications, err := s.verificationRepo.FindAllByIDs(ctx, exec, verificationIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all verifications for %v: %w",
			verificationIDs, err)
	}

	groupedVerifications := make(map[string]*model.Verification)
	for _, identification := range identifications {
		if !identification.VerificationID.Valid {
			continue
		}
		for i, verification := range verifications {
			if verification.ID == identification.VerificationID.String {
				groupedVerifications[identification.ID] = verifications[i]
				break
			}
		}
	}
	return groupedVerifications, nil
}

func (s *Service) fetchAllPasskeysByIdentification(
	ctx context.Context,
	exec database.Executor,
	identifications []*model.Identification) (map[string]*model.Passkey, error) {
	passkeyIdentIDs := make([]string, 0)
	for _, identification := range identifications {
		if identification.IsPasskey() && identification.IsVerified() {
			passkeyIdentIDs = append(passkeyIdentIDs, identification.ID)
		}
	}

	passkeysMap := make(map[string]*model.Passkey)
	if len(passkeyIdentIDs) == 0 {
		return passkeysMap, nil
	}

	passkeys, err := s.passkeyRepo.FindAllByIdentificationIDs(ctx, exec, passkeyIdentIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all passkeys for %v: %w", passkeyIdentIDs, err)
	}
	for _, passkey := range passkeys {
		passkeysMap[passkey.IdentificationID] = passkey
	}
	return passkeysMap, nil
}

func (s *Service) fetchAllTOTPsByUser(
	ctx context.Context,
	exec database.Executor,
	userIDs []string) (map[string]*model.TOTP, error) {
	totps, err := s.totpRepo.FindAllVerifiedByUsers(ctx, exec, userIDs)
	if err != nil {
		return nil, fmt.Errorf("fetching totps for users %v: %w", userIDs, err)
	}
	groupedTOTPs := make(map[string]*model.TOTP)
	for i, totp := range totps {
		groupedTOTPs[totp.UserID] = totps[i]
	}
	return groupedTOTPs, nil
}

func (s *Service) fetchAllBackupCodesByUser(
	ctx context.Context,
	exec database.Executor,
	userIDs []string) (map[string]*model.BackupCode, error) {
	backupCodes, err := s.backupCodeRepo.FindAllByUsers(ctx, exec, userIDs)
	if err != nil {
		return nil, fmt.Errorf("fetching backup codes for users %v: %w", userIDs, err)
	}
	groupedBackupCodes := make(map[string]*model.BackupCode)
	for i, backupCode := range backupCodes {
		groupedBackupCodes[backupCode.UserID] = backupCodes[i]
	}
	return groupedBackupCodes, nil
}

func (s *Service) fetchAllUserPlanKeysByUser(ctx context.Context, exec database.Executor, users []*model.User) (map[string]string, error) {
	if len(users) == 0 {
		return nil, nil
	}

	userIDs := set.New[string]()
	for _, user := range users {
		userIDs.Insert(user.ID)
	}

	instanceID := users[0].InstanceID
	plans, err := s.billingPlanRepo.FindAllByInstanceAndCustomerType(ctx, exec, instanceID, constants.BillingUserType)
	if err != nil {
		return nil, fmt.Errorf("fetching plans for instance %s: %w", instanceID, err)
	}
	if len(plans) == 0 {
		return nil, nil
	}

	plansByPlanID := make(map[string]*model.BillingPlan, len(plans))
	for _, plan := range plans {
		plansByPlanID[plan.ID] = plan
	}

	subscriptions, err := s.billingSubscriptionRepo.FindAllByResourceIDs(ctx, exec, userIDs.Array())
	if err != nil {
		return nil, fmt.Errorf("fetching subscriptions for users %v: %w", userIDs.Array(), err)
	}
	if len(subscriptions) == 0 {
		return nil, nil
	}

	subscriptionToPlanKey := make(map[string]string, len(subscriptions))
	for _, subscription := range subscriptions {
		subscriptionToPlanKey[subscription.ID] = plansByPlanID[subscription.BillingPlanID].Key
	}

	planKeyPerUser := make(map[string]string, len(users))
	for _, user := range users {
		if planKey, ok := subscriptionToPlanKey[user.BillingSubscriptionID.String]; ok {
			planKeyPerUser[user.ID] = planKey
		}
	}

	return planKeyPerUser, nil
}

func (s *Service) ConvertUser(ctx context.Context, exec database.Executor, userSettings *usersettings.UserSettings, user *model.User) (*model.UserSerializable, error) {
	userSerializables, err := s.ConvertUsers(ctx, exec, userSettings, []*model.User{user})
	if err != nil {
		return nil, err
	}
	if len(userSerializables) != 1 {
		return nil, fmt.Errorf("convert user expected to get 1 user serializable, got %d instead: %v",
			len(userSerializables), userSerializables)
	}
	return userSerializables[0], nil
}

func (s *Service) ConvertIdentification(ctx context.Context, exec database.Executor, ident *model.Identification) (*model.IdentificationSerializable, error) {
	identSerializable := &model.IdentificationSerializable{
		Identification: ident,
	}

	var err error
	if ident.VerificationID.Valid {
		identSerializable.Verification, err = s.verificationService.VerificationWithStatus(ctx, exec, ident.VerificationID.String)
		if err != nil {
			return nil, fmt.Errorf("serializable/ConvertIdentification: failed to fetch verification %s with status for %s: %w", ident.VerificationID.String, ident.ID, err)
		}
	}

	if ident.ExternalAccountID.Valid {
		identSerializable.ExternalAccount, err = s.externalAccountRepo.FindByIDAndInstance(ctx, exec, ident.ExternalAccountID.String, ident.InstanceID)
		if err != nil {
			return nil, fmt.Errorf("serializable/ConvertIdentification: failed to fetch external account %s for %s: %w", ident.ExternalAccountID.String, ident.ID, err)
		}
	}

	if ident.SamlAccountID.Valid {
		identSerializable.SAMLAccountWithDeps, err = s.samlAccountRepo.FindByIDAndInstanceWithDeps(ctx, exec, ident.SamlAccountID.String, ident.InstanceID)
		if err != nil {
			return nil, fmt.Errorf("serializable/ConvertIdentification: failed to fetch saml account %s for %s: %w", ident.SamlAccountID.String, ident.ID, err)
		}
	}

	identSerializable.ParentIdentifications, err = s.identificationRepo.FindAllByTargetIdentificationID(ctx, exec, ident.ID)
	if err != nil {
		return nil, fmt.Errorf("serializable/ConvertIdentification: failed to fetch parent identifications %s: %w", ident.ID, err)
	}

	if ident.IsPasskey() {
		passkey, err := s.passkeyRepo.FindByIdentificationID(ctx, exec, ident.ID)
		if err != nil {
			return nil, fmt.Errorf("serializable/ConvertIdentification: failed to fetch passkey for %s: %w", ident.ID, err)
		}
		identSerializable.Passkey = passkey
	}

	return identSerializable, nil
}

func (s *Service) createIdentificationSerializable(
	identifications []*model.Identification,
	verificationsByIdentification map[string]*model.Verification,
	externalAccountsByIdentification map[string]*model.ExternalAccount,
	samlAccountsByIdentification map[string]*model.SAMLAccountWithDeps,
	parentIdentificationsByIdentification map[string][]*model.Identification,
	passkeysByIdentification map[string]*model.Passkey,
) map[string][]*model.IdentificationSerializable {
	identificationSerializables := make(map[string][]*model.IdentificationSerializable)
	for _, identification := range identifications {
		// skip unverified passkeys
		if identification.IsPasskey() && !identification.IsVerified() {
			continue
		}

		identificationSerializable := &model.IdentificationSerializable{
			Identification:        identification,
			ExternalAccount:       externalAccountsByIdentification[identification.ID],
			SAMLAccountWithDeps:   samlAccountsByIdentification[identification.ID],
			ParentIdentifications: parentIdentificationsByIdentification[identification.ID],
			Passkey:               passkeysByIdentification[identification.ID],
		}

		identificationVerification := identification.WithVerification(verificationsByIdentification[identification.ID])
		if identificationVerification.Verification != nil {
			identificationSerializable.Verification = &model.VerificationWithStatus{
				Verification: identificationVerification.Verification,
				Status:       identificationVerification.Status(s.clock),
			}
		}

		if i := identificationSerializables[identification.Type]; i == nil {
			identificationSerializables[identification.Type] = make([]*model.IdentificationSerializable, 0)
		}
		identificationSerializables[identification.Type] =
			append(identificationSerializables[identification.Type], identificationSerializable)
	}
	return identificationSerializables
}

func (s *Service) ConvertOrganizationDomain(ctx context.Context, exec database.Executor, orgDomain *model.OrganizationDomain) (*model.OrganizationDomainSerializable, error) {
	serializable := &model.OrganizationDomainSerializable{
		OrganizationDomain: orgDomain,
	}

	if orgDomain.VerificationID.Valid {
		var err error
		serializable.Verification, err = s.orgDomainService.VerificationWithStatus(ctx, exec, orgDomain.VerificationID.String)
		if err != nil {
			return nil, fmt.Errorf("serializable/ConvertOrganizationDomain: failed to fetch verification %s with status for %s: %w", orgDomain.VerificationID.String, orgDomain.ID, err)
		}
	}

	// get total pending invitations and suggestions
	pendingInvitations, err := s.orgInvitationRepo.CountPendingByOrgDomain(ctx, exec, orgDomain.ID)
	if err != nil {
		return nil, fmt.Errorf("serializable/ConvertOrganizationDomain: failed to fetch pending org domain invitations for %s: %w", orgDomain.ID, err)
	}
	serializable.TotalPendingInvitations = int(pendingInvitations)

	pendingSuggestions, err := s.orgSuggestionRepo.CountPendingByOrgDomain(ctx, exec, orgDomain.ID)
	if err != nil {
		return nil, fmt.Errorf("serializable/ConvertOrganizationDomain: failed to fetch pending org domain suggestions for %s: %w", orgDomain.ID, err)
	}
	serializable.TotalPendingSuggestions = int(pendingSuggestions)

	return serializable, nil
}

func (s *Service) ConvertOrganizationMembershipRequest(ctx context.Context, exec database.Executor, memberReq *model.OrganizationMembershipRequest) (*model.OrganizationMembershipRequestSerializable, error) {
	user, err := s.userRepo.FindByID(ctx, exec, memberReq.UserID)
	if err != nil {
		return nil, fmt.Errorf("serializable/ConvertOrganizationMembershipRequest: failed to get user %s: %w", user.ID, err)
	}

	imageURL, err := s.userProfileService.GetImageURL(user)
	if err != nil {
		return nil, fmt.Errorf("serializable/ConvertOrganizationMembershipRequest: failed to get user's %s image url: %w", user.ID, err)
	}

	orgSuggestion, err := s.orgSuggestionRepo.FindByID(ctx, exec, memberReq.OrganizationSuggestionID)
	if err != nil {
		return nil, fmt.Errorf("serializable/ConvertOrganizationMembershipRequest: failed to get organization suggestion %s: %w", memberReq.OrganizationSuggestionID, err)
	}

	return &model.OrganizationMembershipRequestSerializable{
		OrganizationMembershipRequest: memberReq,
		User:                          user,
		Identifier:                    orgSuggestion.EmailAddress,
		ImageURL:                      imageURL,
	}, nil
}

func (s *Service) ConvertOrganizationRole(ctx context.Context, exec database.Executor, role *model.Role) (*model.RoleSerializable, error) {
	permissions, err := s.permissionRepo.FindAllByRole(ctx, exec, role.ID)
	if err != nil {
		return nil, fmt.Errorf("serializable/ConvertOrganizationRole: failed to get role permissions %s: %w", role.ID, err)
	}

	return &model.RoleSerializable{
		Role:        role,
		Permissions: permissions,
	}, nil
}
