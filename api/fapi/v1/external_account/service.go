package external_account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/identifications"
	"clerk/api/shared/serializable"
	"clerk/api/shared/users"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/metadata"
	"clerk/pkg/oauth"
	"clerk/pkg/oauth/provider"
	"clerk/pkg/unverifiedemails"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/volatiletech/null/v8"
	"golang.org/x/oauth2"
)

type Service struct {
	// services
	eventService          *events.Service
	identificationService *identifications.Service
	serializableService   *serializable.Service
	userService           *users.Service

	// repositories
	authConfigRepo      *repository.AuthConfig
	externalAccountRepo *repository.ExternalAccount
	identificationRepo  *repository.Identification
	userRepo            *repository.Users
	verificationRepo    *repository.Verification
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		eventService:          events.NewService(deps),
		identificationService: identifications.NewService(deps),
		serializableService:   serializable.NewService(deps.Clock()),
		userService:           users.NewService(deps),
		authConfigRepo:        repository.NewAuthConfig(),
		externalAccountRepo:   repository.NewExternalAccount(),
		identificationRepo:    repository.NewIdentification(),
		userRepo:              repository.NewUsers(),
		verificationRepo:      repository.NewVerification(),
	}
}

func FetchUser(
	ctx context.Context,
	oauthProvider oauth.Provider,
	oauthConfig *oauth.Config,
	ost *model.OauthStateToken,
	userSettings *usersettings.UserSettings,
	urlValues url.Values,
) (*oauth.User, error) {
	if oauthProvider.IsOAuth1() {
		oauthConfig.OAuth1.AccessToken = ost.OAuth1AccessToken
	} else {
		oauthConfig.AuthorizationCode = ost.OauthExchangeCode.String
	}

	var opts []oauth2.AuthCodeOption
	if oauthProvider.UsesPKCE() && ost.PKCECodeVerifier.Ptr() != nil {
		opts = append(opts, oauth2.VerifierOption(ost.PKCECodeVerifier.String))
	}

	user, err := oauthProvider.FetchUser(provider.NewContextWithOAuth2Client(ctx), oauthConfig, opts...)
	if err != nil {
		return nil, err
	}

	user.EmailAddress = strings.ToLower(user.EmailAddress)
	user.Username = strings.ToLower(user.Username)

	currentProviderID := oauthProvider.ID()

	// Sadly Apple during an OAuth flow, for user data protection reasons (lol), doesn't make available the user's
	// name in the id_token or in some endpoint, so we can retrieve it during fetching the other user data.
	// What they do, is to provide this data as a post form parameter that ends up in our /oauth_callback endpoint
	// So in order to retrieve such info, we have to implement the below hacky way
	// References:
	// - https://developer.apple.com/documentation/sign_in_with_apple/sign_in_with_apple_rest_api/authenticating_users_with_sign_in_with_apple
	// - https://developer.apple.com/forums/thread/125112
	if currentProviderID == provider.AppleID() {
		if err = handleAppleUserFormPostData(urlValues, user); err != nil {
			return nil, err
		}
	}

	// Before PSU, we always treated the case where a provider does not return an email as an error, halting the
	// sign-up process. After PSU, we gracefully handle this case by asking the user to fill their email.
	// TikTok is excluded because we supported it even before PSU. Mock is excluded for testing purposes.
	if !userSettings.SignUp.Progressive {
		if !user.EmailAddressProvided() && currentProviderID != provider.TiktokID() && currentProviderID != provider.MockID() {
			return nil, oauth.FetchUserError{Err: fmt.Errorf("missing email for OAuth provider %s", oauthProvider.Name())}
		}
	}

	// For OAuth Connect & Reauthorize flows we allow unverified OAuth emails as we are able to handle
	// such scenarios
	if ost.SourceType == constants.OSTOAuthConnect || ost.SourceType == constants.OSTOAuthReauthorize {
		return user, nil
	}

	// This check is needed to ensure backward compatibility cases we don't support verifying unverified emails.
	if !unverifiedemails.IsVerifyFlowSupported(ctx, oauthProvider, user, userSettings) {
		return nil, oauth.FetchUserError{Err: fmt.Errorf("unverified email for OAuth provider %s", oauthProvider.Name())}
	}

	return user, nil
}

type appleUserFormPostData struct {
	Name struct {
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	} `json:"name"`
}

func handleAppleUserFormPostData(urlValues url.Values, user *oauth.User) error {
	if !urlValues.Has("user") {
		return nil
	}

	userURLValue := urlValues.Get("user")

	userData := appleUserFormPostData{}
	if err := json.Unmarshal([]byte(userURLValue), &userData); err != nil {
		return oauth.FetchUserError{Err: fmt.Errorf("failed to unmarshal apple user post form value %s", userURLValue)}
	}

	user.FirstName = userData.Name.FirstName
	user.LastName = userData.Name.LastName
	return nil
}

type CreateResult struct {
	Identification  *model.Identification
	ExternalAccount *model.ExternalAccount
}

// Create will create a new external account along with a verified identification.
func (s *Service) Create(ctx context.Context, exec database.Executor, ver *model.Verification, ost *model.OauthStateToken, oauthUser *oauth.User, instanceID string, userID *string) (*CreateResult, error) {
	existingIdentification, err := s.identificationRepo.QueryLatestClaimedByInstanceAndTypeAndProviderUserID(ctx, exec, instanceID, oauthUser.ProviderUserID, oauthUser.ProviderID)
	if err != nil {
		return nil, err
	}

	if existingIdentification != nil {
		return &CreateResult{Identification: existingIdentification}, nil
	}

	// Create identification
	identification := &model.Identification{Identification: &sqbmodel.Identification{
		InstanceID:     instanceID,
		Type:           oauthUser.ProviderID,
		VerificationID: null.StringFrom(ver.ID),
		Status:         constants.ISVerified,
		UserID:         null.StringFromPtr(userID),
	}}

	if err = s.identificationRepo.Insert(ctx, exec, identification); err != nil {
		return nil, err
	}

	// create external account
	externalAccount := &model.ExternalAccount{ExternalAccount: &sqbmodel.ExternalAccount{
		InstanceID:            instanceID,
		OauthConfigID:         ost.OauthConfigID,
		IdentificationID:      identification.ID,
		ApprovedScopes:        ost.ReturnedScopesSorted(),
		Provider:              oauthUser.ProviderID,
		ProviderUserID:        oauthUser.ProviderUserID,
		EmailAddress:          oauthUser.EmailAddress,
		FirstName:             oauthUser.FirstName,
		LastName:              oauthUser.LastName,
		AvatarURL:             oauthUser.AvatarURL,
		RefreshToken:          null.NewString("", false),
		AccessToken:           oauthUser.AccessToken,
		AccessTokenExpiration: null.NewTime(time.Time{}, false),
	}}

	if oauthUser.Username != "" {
		externalAccount.Username = null.StringFrom(oauthUser.Username)
	}

	if oauthUser.RefreshToken != "" {
		externalAccount.RefreshToken = null.StringFrom(oauthUser.RefreshToken)
	}

	if !oauthUser.AccessTokenExpiration.IsZero() {
		externalAccount.AccessTokenExpiration = null.TimeFrom(oauthUser.AccessTokenExpiration)
	}

	if oauthUser.OAuth1AccessTokenSecret != "" {
		externalAccount.Oauth1AccessTokenSecret = null.StringFrom(oauthUser.OAuth1AccessTokenSecret)
	}

	if oauthUser.Metadata != nil {
		if err = externalAccount.PublicMetadata.Marshal(oauthUser.Metadata); err != nil {
			return nil, fmt.Errorf("external_account/create: marshalling metadata %+v: %w", oauthUser.Metadata, err)
		}
		valErr := metadata.Validate(metadata.Metadata{Public: json.RawMessage(externalAccount.PublicMetadata)})
		if valErr != nil {
			return nil, err
		}
	}

	if oauthUser.TokenLabel != "" {
		externalAccount.Label = null.StringFrom(oauthUser.TokenLabel)
	}

	if err = s.externalAccountRepo.Insert(ctx, exec, externalAccount); err != nil {
		return nil, err
	}

	ver.IdentificationID = null.StringFrom(identification.ID)
	if err = s.verificationRepo.UpdateIdentificationID(ctx, exec, ver); err != nil {
		return nil, err
	}

	identification.ExternalAccountID = null.StringFrom(externalAccount.ID)
	if err = s.identificationRepo.UpdateExternalAccountID(ctx, exec, identification); err != nil {
		return nil, err
	}

	return &CreateResult{
		Identification:  identification,
		ExternalAccount: externalAccount,
	}, nil
}

// CreateAndLink creates a new external account along with a verified external account identification.
// It attempts to link an existing email address identification with the external account identification. If no
// identification exists, a new one will be created, verified and linked with the external account identification
// as the target identification.
func (s Service) CreateAndLink(ctx context.Context, exec database.Executor, ver *model.Verification, ost *model.OauthStateToken, oauthUser *oauth.User, instance *model.Instance, userID *string, userSettings *usersettings.UserSettings) (*CreateResult, error) {
	res, err := s.Create(ctx, exec, ver, ost, oauthUser, instance.ID, userID)
	if err != nil {
		return nil, err
	}
	ident, err := s.CreateOrLinkEmailIdentification(ctx, exec, res.Identification, ost, oauthUser, instance.ID, userID, userSettings)
	if err != nil {
		return nil, err
	}
	res.Identification = ident

	if userID == nil {
		return res, nil
	}

	user, err := s.userRepo.QueryByIDAndInstance(ctx, exec, *userID, instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(*userID)
	}
	updated, err := s.updateUserData(ctx, exec, user, oauthUser)
	if err != nil {
		return nil, err
	}
	if updated {
		if err := s.sendUserUpdatedEvent(ctx, exec, instance, userSettings, user); err != nil {
			return nil, err
		}
	}

	return res, nil
}

// Connect handles the OAuth connect flow. At this point we have available the user's data retrieved from the
// OAuth provider and updating the respective records
func (s Service) Connect(ctx context.Context, exec database.Executor, ver *model.Verification, ost *model.OauthStateToken, oauthUser *oauth.User, instanceID, userID string, userSettings *usersettings.UserSettings) error {
	oauthIdentification, err := s.identificationRepo.FindByIDAndInstance(ctx, exec, ver.IdentificationID.String, instanceID)
	if err != nil {
		return err
	}

	externalAccount, err := s.externalAccountRepo.FindByIDAndInstanceAndProvider(ctx, exec, instanceID, oauthIdentification.ExternalAccountID.String, oauthUser.ProviderID)
	if err != nil {
		return err
	}

	// Update external account
	externalAccount.OauthConfigID = ost.OauthConfigID
	externalAccount.ApprovedScopes = ost.ReturnedScopesSorted()
	externalAccount.ProviderUserID = oauthUser.ProviderUserID
	externalAccount.EmailAddress = oauthUser.EmailAddress
	externalAccount.FirstName = oauthUser.FirstName
	externalAccount.LastName = oauthUser.LastName
	externalAccount.AvatarURL = oauthUser.AvatarURL
	externalAccount.AccessToken = oauthUser.AccessToken

	if oauthUser.Username != "" {
		externalAccount.Username = null.StringFrom(oauthUser.Username)
	}

	if oauthUser.RefreshToken != "" {
		externalAccount.RefreshToken = null.StringFrom(oauthUser.RefreshToken)
	}

	if !oauthUser.AccessTokenExpiration.IsZero() {
		externalAccount.AccessTokenExpiration = null.TimeFrom(oauthUser.AccessTokenExpiration)
	}

	if oauthUser.OAuth1AccessTokenSecret != "" {
		externalAccount.Oauth1AccessTokenSecret = null.StringFrom(oauthUser.OAuth1AccessTokenSecret)
	}

	if oauthUser.Metadata != nil {
		if err := externalAccount.PublicMetadata.Marshal(oauthUser.Metadata); err != nil {
			return fmt.Errorf("external_account/connect: marshalling metadata %+v: %w", oauthUser.Metadata, err)
		}
		valErr := metadata.Validate(metadata.Metadata{Public: json.RawMessage(externalAccount.PublicMetadata)})
		if valErr != nil {
			return err
		}
	}

	if oauthUser.TokenLabel != "" {
		externalAccount.Label = null.StringFrom(oauthUser.TokenLabel)
	}

	if err = s.externalAccountRepo.Update(ctx, exec, externalAccount); err != nil {
		return err
	}

	oauthIdentification.Status = constants.ISVerified
	if err = s.identificationRepo.UpdateStatus(ctx, exec, oauthIdentification); err != nil {
		return err
	}

	// TODO(auth): When we support unverified emails for other flows as well (sign in/up) we can drop the
	// below logic and replace it with the 'CreateOrLinkEmailIdentification' logic for every scenario
	if oauthUser.EmailAddressProvided() && !oauthUser.EmailAddressVerified {
		emailIdentificationToLink, err := s.identificationRepo.QueryByInstanceAndIdentifierAndUser(ctx, exec, instanceID, oauthUser.EmailAddress, userID)
		if err != nil {
			return err
		}

		if emailIdentificationToLink == nil {
			emailIdentificationToLink, err = s.createEmailIdentificationAndVerification(ctx, exec, oauthUser, instanceID, &userID)
			if err != nil {
				return err
			}
		}

		oauthIdentification.TargetIdentificationID = null.StringFrom(emailIdentificationToLink.ID)
		return s.identificationRepo.UpdateTargetIdentificationID(ctx, exec, oauthIdentification)
	}

	_, err = s.CreateOrLinkEmailIdentification(ctx, exec, oauthIdentification, ost, oauthUser, instanceID, &userID, userSettings)
	return err
}

// Reauthorize handles the OAuth reauthorization flow. At this point, the user has authorized the new policies
// (e.g. scopes) and we will update the relevant columns (approved scopes, access token, refresh token etc) of
// the existing external account.
func (s *Service) Reauthorize(ctx context.Context, tx database.Tx, userSettings *usersettings.UserSettings, instance *model.Instance, user *model.User, ost *model.OauthStateToken, oauthUser *oauth.User) error {
	externalAccount, err := s.externalAccountRepo.QueryByIDAndInstance(ctx, tx, ost.SourceID, instance.ID)
	if err != nil {
		return err
	}
	if externalAccount == nil {
		return apierror.ExternalAccountNotFound()
	}

	updateColumns := []string{
		sqbmodel.ExternalAccountColumns.ApprovedScopes,
		sqbmodel.ExternalAccountColumns.AccessToken,
	}

	externalAccount.ApprovedScopes = ost.ReturnedScopesSorted()
	externalAccount.AccessToken = oauthUser.AccessToken

	if oauthUser.RefreshToken != "" {
		externalAccount.RefreshToken = null.StringFrom(oauthUser.RefreshToken)
		updateColumns = append(updateColumns, sqbmodel.ExternalAccountColumns.RefreshToken)
	}

	if !oauthUser.AccessTokenExpiration.IsZero() {
		externalAccount.AccessTokenExpiration = null.TimeFrom(oauthUser.AccessTokenExpiration)
		updateColumns = append(updateColumns, sqbmodel.ExternalAccountColumns.AccessTokenExpiration)
	}

	if oauthUser.OAuth1AccessTokenSecret != "" {
		externalAccount.Oauth1AccessTokenSecret = null.StringFrom(oauthUser.OAuth1AccessTokenSecret)
		updateColumns = append(updateColumns, sqbmodel.ExternalAccountColumns.Oauth1AccessTokenSecret)
	}

	if err = s.externalAccountRepo.Update(ctx, tx, externalAccount, updateColumns...); err != nil {
		return err
	}

	identification, err := s.identificationRepo.FindByIDAndInstance(ctx, tx, externalAccount.IdentificationID, externalAccount.InstanceID)
	if err != nil {
		return err
	}

	identification.Status = constants.ISVerified
	if err := s.identificationRepo.UpdateStatus(ctx, tx, identification); err != nil {
		return err
	}

	return s.sendUserUpdatedEvent(ctx, tx, instance, userSettings, user)
}

// CreateOrLinkEmailIdentification checks if we need to create any email identification after a successful OAuth flow,
// or need to link the OAuth identification with an existing email identification.
// Also, if the OAuth provider returns a verified email address, also verifies any existing unverified email
// identification.
func (s Service) CreateOrLinkEmailIdentification(
	ctx context.Context,
	exec database.Executor,
	identification *model.Identification,
	ost *model.OauthStateToken,
	oauthUser *oauth.User,
	instanceID string,
	userID *string,
	userSettings *usersettings.UserSettings,
) (*model.Identification, error) {
	var err error

	// Some OAuth providers (e.g. TikTok) don't provide a user email address. In this case we don't
	// need to check for any linked identifications and we can return early.
	if !oauthUser.EmailAddressProvided() {
		return identification, nil
	}

	// Check if we need to create linked identifications
	var emailIdentificationToLink *model.Identification
	if userID == nil {
		emailIdentificationToLink, err = s.identificationRepo.QueryClaimedVerifiedOrReservedByInstanceAndIdentifierAndTypePrioritizingVerified(ctx, exec, instanceID, oauthUser.EmailAddress, constants.ITEmailAddress)
		if err != nil {
			return nil, err
		}
	} else {
		emailIdentificationToLink, err = s.identificationRepo.QueryByInstanceAndIdentifierAndUser(ctx, exec, instanceID, oauthUser.EmailAddress, *userID)
		if err != nil {
			return nil, err
		}
	}

	// Create email_address identification if needed
	if emailIdentificationToLink == nil {
		emailIdentificationToLink, err = s.createEmailIdentificationAndVerification(ctx, exec, oauthUser, instanceID, userID)
		if err != nil {
			return nil, err
		}
	}

	if err = s.userService.FlagUserForPasswordReset(ctx, exec, ost, oauthUser, identification, emailIdentificationToLink); err != nil {
		return nil, err
	}

	// If needed, initiate the unverified email verification flow, which currently applies only to instances
	// that allow unverified emails at sign-up
	if err = s.identificationService.InitiateReVerifyFlow(ctx, exec, oauthUser, identification, emailIdentificationToLink, userSettings); err != nil {
		return nil, err
	}

	// This check is required for backward compatibility cases we don't support verifying unverified emails.
	shouldProceedWithUnverifiedEmail, err := unverifiedemails.IsVerifyFlowOverrideAllowed(ctx, oauthUser)
	if err != nil {
		return nil, err
	}
	if shouldProceedWithUnverifiedEmail {
		return identification, nil
	}

	// If the existing verification is not verified and the provider returns verified emails, verify it
	if oauthUser.EmailAddressVerified && !emailIdentificationToLink.IsVerified() {
		if emailIdentificationToLink.IsReserved() {
			reservedEmailVerification := &model.Verification{Verification: &sqbmodel.Verification{
				InstanceID:       instanceID,
				IdentificationID: null.StringFrom(emailIdentificationToLink.ID),
				Strategy:         constants.StrategyFrom(oauthUser.ProviderID),
				Attempts:         1,
				ExpireAt:         time.Time{},
			}}

			if err = s.verificationRepo.Insert(ctx, exec, reservedEmailVerification); err != nil {
				return nil, err
			}

			emailIdentificationToLink.Status = constants.ISVerified
			emailIdentificationToLink.VerificationID = null.StringFrom(reservedEmailVerification.ID)
			err = s.identificationRepo.Update(ctx, exec, emailIdentificationToLink,
				sqbmodel.IdentificationColumns.Status,
				sqbmodel.IdentificationColumns.VerificationID)
			if err != nil {
				return nil, err
			}
		} else {
			existingEmailVerification, err := s.verificationRepo.FindByIDAndInstance(ctx, exec, instanceID, emailIdentificationToLink.VerificationID.String)
			if err != nil {
				return nil, err
			}

			existingEmailVerification.Strategy = constants.StrategyFrom(oauthUser.ProviderID)
			existingEmailVerification.Attempts++
			existingEmailVerification.IdentificationID = null.StringFrom(emailIdentificationToLink.ID)

			err = s.verificationRepo.Update(ctx, exec, existingEmailVerification, sqbmodel.VerificationColumns.Strategy, sqbmodel.VerificationColumns.Attempts, sqbmodel.VerificationColumns.IdentificationID)
			if err != nil {
				return nil, err
			}

			emailIdentificationToLink.Status = constants.ISVerified
			if err = s.identificationRepo.UpdateStatus(ctx, exec, emailIdentificationToLink); err != nil {
				return nil, err
			}
		}
	}

	if oauthUser.EmailAddressVerified && emailIdentificationToLink.UserID.Valid {
		authConfig, err := s.authConfigRepo.FindByInstanceActiveAuthConfigID(ctx, exec, instanceID)
		if err != nil {
			return nil, err
		}

		userSettings := usersettings.NewUserSettings(authConfig.UserSettings)

		// NOTE: we're inside a transaction, so we can safely assume the user
		// exists, because of the foreign key
		user, err := s.userRepo.FindByIDAndInstance(ctx, exec, emailIdentificationToLink.UserID.String, instanceID)
		if err != nil {
			return nil, err
		}

		err = s.identificationService.RestoreUserReservedAndPrimaryIdentifications(ctx, exec, emailIdentificationToLink, userSettings, instanceID, user)
		if err != nil {
			return nil, err
		}
	}

	// link the oauth identification with the email
	return s.LinkIdentification(ctx, exec, identification, emailIdentificationToLink, oauthUser)
}

// LinkIdentification will associate the provided identification with the targetIdentification.
func (s *Service) LinkIdentification(
	ctx context.Context,
	exec database.Executor,
	identification *model.Identification,
	target *model.Identification,
	oauthUser *oauth.User,
) (*model.Identification, error) {
	shouldProceedWithUnverifiedEmail, err := unverifiedemails.IsVerifyFlowOverrideAllowed(ctx, oauthUser)
	if err != nil {
		return nil, err
	}
	if shouldProceedWithUnverifiedEmail {
		return identification, nil
	}

	// set email identification as the target only if the email is either verified or not yet claimed.
	if oauthUser.EmailAddressVerified || !target.IsClaimed() {
		identification.TargetIdentificationID = null.StringFrom(target.ID)
		if err := s.identificationRepo.UpdateTargetIdentificationID(ctx, exec, identification); err != nil {
			return identification, err
		}
	}

	return identification, nil
}

func (s Service) Update(ctx context.Context, tx database.Tx, userSettings *usersettings.UserSettings, ost *model.OauthStateToken, oauthUser *oauth.User, instance *model.Instance) (*model.ExternalAccount, error) {
	account, err := s.externalAccountRepo.FindLatestByInstanceAndProviderUserIDAndProvider(ctx, tx, instance.ID, oauthUser.ProviderUserID, oauthUser.ProviderID)
	if err != nil {
		return nil, err
	}

	// The existing external account doesn't contain an email address, but the OAuth provider now provided us with one
	// Along with the regular update flow, we should create the corresponding email address identification and
	// link it with the existing OAuth identification
	if account.EmailAddress == "" && oauthUser.EmailAddressProvided() {
		oauthIdent, err := s.identificationRepo.FindByIDAndInstance(ctx, tx, account.IdentificationID, instance.ID)
		if err != nil {
			return nil, err
		}

		_, err = s.CreateOrLinkEmailIdentification(ctx, tx, oauthIdent, ost, oauthUser, instance.ID, oauthIdent.UserID.Ptr(), userSettings)
		if err != nil {
			return nil, err
		}
	}

	scopesChanged := account.ApprovedScopes != ost.ReturnedScopesSorted()

	updateColumns := []string{
		sqbmodel.ExternalAccountColumns.FirstName,
		sqbmodel.ExternalAccountColumns.LastName,
		sqbmodel.ExternalAccountColumns.Username,
		sqbmodel.ExternalAccountColumns.EmailAddress,
		sqbmodel.ExternalAccountColumns.AvatarURL,
		sqbmodel.ExternalAccountColumns.ApprovedScopes,
	}

	account.FirstName = oauthUser.FirstName
	account.LastName = oauthUser.LastName
	account.Username = null.StringFrom(oauthUser.Username)
	account.EmailAddress = oauthUser.EmailAddress
	account.AvatarURL = oauthUser.AvatarURL

	// TODO(oauth): validate that overriding the old scopes is correct. We
	// did this for Facebook, but for Google we appended the new scopes to
	// any existing ones. See https://clerkinc.slack.com/archives/C01SN2R6DSN/p1623758474003800
	account.ApprovedScopes = ost.ReturnedScopesSorted()

	if oauthUser.Metadata != nil {
		if err = account.PublicMetadata.Marshal(oauthUser.Metadata); err != nil {
			return nil, fmt.Errorf("external_account/update: marshalling metadata %+v: %w", oauthUser.Metadata, err)
		}
		valErr := metadata.Validate(metadata.Metadata{Public: json.RawMessage(account.PublicMetadata)})
		if valErr != nil {
			return nil, err
		}
		updateColumns = append(updateColumns, sqbmodel.ExternalAccountColumns.PublicMetadata)
	}

	if oauthUser.TokenLabel != "" {
		account.Label = null.StringFrom(oauthUser.TokenLabel)
		updateColumns = append(updateColumns, sqbmodel.ExternalAccountColumns.Label)
	}

	if oauthUser.AccessToken != "" {
		account.AccessToken = oauthUser.AccessToken
		updateColumns = append(updateColumns, sqbmodel.ExternalAccountColumns.AccessToken)
	}

	if oauthUser.RefreshToken != "" {
		account.RefreshToken = null.StringFrom(oauthUser.RefreshToken)
		updateColumns = append(updateColumns, sqbmodel.ExternalAccountColumns.RefreshToken)
	}

	if !oauthUser.AccessTokenExpiration.IsZero() {
		account.AccessTokenExpiration = null.TimeFrom(oauthUser.AccessTokenExpiration)
		updateColumns = append(updateColumns, sqbmodel.ExternalAccountColumns.AccessTokenExpiration)
	}

	if err = s.externalAccountRepo.Update(ctx, tx, account, updateColumns...); err != nil {
		return nil, err
	}

	user, err := s.userRepo.QueryByInstanceAndIdentificationID(ctx, tx, instance.ID, account.IdentificationID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, apierror.IdentificationNotFound(account.IdentificationID)
	}

	userUpdated, err := s.updateUserData(ctx, tx, user, oauthUser)
	if err != nil {
		return nil, err
	}

	if userUpdated || scopesChanged {
		if err := s.sendUserUpdatedEvent(ctx, tx, instance, userSettings, user); err != nil {
			return nil, err
		}
	}

	return account, nil
}

func (s *Service) updateUserData(ctx context.Context, tx database.Executor, user *model.User, oauthUser *oauth.User) (bool, error) {
	var columns []string

	if !user.FirstName.Valid && oauthUser.FirstName != "" {
		user.FirstName = null.StringFrom(oauthUser.FirstName)
		columns = append(columns, sqbmodel.UserColumns.FirstName)
	}

	if !user.LastName.Valid && oauthUser.LastName != "" {
		user.LastName = null.StringFrom(oauthUser.LastName)
		columns = append(columns, sqbmodel.UserColumns.LastName)
	}

	if len(columns) == 0 {
		return false, nil
	}

	if err := s.userRepo.Update(ctx, tx, user, columns...); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) createEmailIdentificationAndVerification(ctx context.Context, exec database.Executor, oauthUser *oauth.User, instanceID string, userID *string) (*model.Identification, error) {
	verification := &model.Verification{Verification: &sqbmodel.Verification{
		InstanceID: instanceID,
		Strategy:   constants.StrategyFrom(oauthUser.ProviderID),
		Attempts:   1,
		ExpireAt:   time.Time{},
	}}

	if err := s.verificationRepo.Insert(ctx, exec, verification); err != nil {
		return nil, err
	}

	emailIdentification := &model.Identification{Identification: &sqbmodel.Identification{
		InstanceID:     instanceID,
		Type:           constants.ITEmailAddress,
		Identifier:     null.StringFrom(oauthUser.EmailAddress),
		VerificationID: null.StringFrom(verification.ID),
		Status:         constants.ISNotSet,
	}}

	emailIdentification.SetCanonicalIdentifier()
	if oauthUser.EmailAddressVerified {
		emailIdentification.Status = constants.ISVerified
	}
	if userID != nil {
		emailIdentification.UserID = null.StringFromPtr(userID)
	}

	if err := s.identificationRepo.Insert(ctx, exec, emailIdentification); err != nil {
		return nil, err
	}

	verification.IdentificationID = null.StringFrom(emailIdentification.ID)
	if err := s.verificationRepo.UpdateIdentificationID(ctx, exec, verification); err != nil {
		return nil, err
	}

	return emailIdentification, nil
}

func (s *Service) sendUserUpdatedEvent(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User) error {
	userSerializable, err := s.serializableService.ConvertUser(ctx, exec, userSettings, user)
	if err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: serializing user %+v: %w", user, err)
	}

	if err = s.eventService.UserUpdated(ctx, exec, instance, serialize.UserToServerAPI(ctx, userSerializable)); err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: send user updated event for user %s: %w", user.ID, err)
	}
	return nil
}
