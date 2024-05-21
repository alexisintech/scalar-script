package samlaccount

import (
	"context"
	"encoding/json"
	"reflect"

	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/serializable"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	clerkjson "clerk/pkg/json"
	"clerk/pkg/saml"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/types"
)

type Service struct {
	// services
	eventService        *events.Service
	serializableService *serializable.Service

	// repositories
	identificationRepo *repository.Identification
	samlAccountRepo    *repository.SAMLAccount
	userRepo           *repository.Users
	verificationRepo   *repository.Verification
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		eventService:        events.NewService(deps),
		serializableService: serializable.NewService(deps.Clock()),
		identificationRepo:  repository.NewIdentification(),
		samlAccountRepo:     repository.NewSAMLAccount(),
		userRepo:            repository.NewUsers(),
		verificationRepo:    repository.NewVerification(),
	}
}

type CreateResult struct {
	SAMLIdentification  *model.Identification
	EmailIdentification *model.Identification
	SAMLAccount         *model.SAMLAccount
}

// Create creates the necessary records after a SAML flow
//  1. An 'email_address' type identification and verification which uses as identifier the email provided by the IdP provider
//     If an email identification already exists, use that one instead
//  2. A 'saml' type identification and links to it the verification retrieved by the ACS endpoint and the above email identification (1)
//  3. The saml account which stores the data returned by the IdP provider and links it to the above saml identification (2)
func (s *Service) Create(ctx context.Context, tx database.Tx, verification *model.Verification, user *saml.User, samlConnection *model.SAMLConnection) (*CreateResult, error) {
	emailVerification := &model.Verification{Verification: &sqbmodel.Verification{
		InstanceID: samlConnection.InstanceID,
		Strategy:   constants.VSSAML,
		Attempts:   1,
	}}
	if err := s.verificationRepo.Insert(ctx, tx, emailVerification); err != nil {
		return nil, err
	}

	emailIdentification, err := s.getOrCreateEmailIdentification(ctx, tx, user, emailVerification, samlConnection)
	if err != nil {
		return nil, err
	}

	emailVerification.IdentificationID = null.StringFrom(emailIdentification.ID)
	if err := s.verificationRepo.UpdateIdentificationID(ctx, tx, emailVerification); err != nil {
		return nil, err
	}

	samlIdentification := &model.Identification{Identification: &sqbmodel.Identification{
		InstanceID:             samlConnection.InstanceID,
		Type:                   constants.ITSAML,
		VerificationID:         null.StringFrom(verification.ID),
		TargetIdentificationID: null.StringFrom(emailIdentification.ID),
		Status:                 constants.ISVerified,
	}}
	if err := s.identificationRepo.Insert(ctx, tx, samlIdentification); err != nil {
		return nil, err
	}

	verification.IdentificationID = null.StringFrom(samlIdentification.ID)
	if err := s.verificationRepo.UpdateIdentificationID(ctx, tx, verification); err != nil {
		return nil, err
	}

	samlAccount := &model.SAMLAccount{SamlAccount: &sqbmodel.SamlAccount{
		InstanceID:       samlConnection.InstanceID,
		IdentificationID: samlIdentification.ID,
		SamlConnectionID: samlConnection.ID,
		EmailAddress:     user.EmailAddress,
		FirstName:        null.StringFromPtr(user.FirstName),
		LastName:         null.StringFromPtr(user.LastName),
		ProviderUserID:   null.StringFromPtr(user.ID),
		PublicMetadata:   types.JSON(user.PublicMetadata),
	}}

	if err := s.samlAccountRepo.Insert(ctx, tx, samlAccount); err != nil {
		return nil, err
	}

	samlIdentification.SamlAccountID = null.StringFrom(samlAccount.ID)
	if err := s.identificationRepo.UpdateSAMLAccountID(ctx, tx, samlIdentification); err != nil {
		return nil, err
	}

	return &CreateResult{
		SAMLIdentification:  samlIdentification,
		EmailIdentification: emailIdentification,
		SAMLAccount:         samlAccount,
	}, nil
}

// getOrCreateEmailIdentification checks for an existing email identification
// if it doesn't exist, we create a new verified email identification
func (s *Service) getOrCreateEmailIdentification(ctx context.Context, tx database.Tx, user *saml.User, verification *model.Verification, samlConnection *model.SAMLConnection) (*model.Identification, error) {
	emailIdentification, err := s.QueryEmailIdentificationForAccountLinking(ctx, tx, samlConnection, user)
	if err != nil {
		return nil, err
	}

	// verify reserved email identifications for SAML connections with verified emails
	if emailIdentification != nil && emailIdentification.IsReserved() && samlConnection.IdpEmailsVerified {
		emailIdentification.Status = constants.ISVerified
		emailIdentification.VerificationID = null.StringFrom(verification.ID)
		err = s.identificationRepo.Update(
			ctx,
			tx,
			emailIdentification,
			sqbmodel.IdentificationColumns.Status,
			sqbmodel.IdentificationColumns.VerificationID,
		)
		if err != nil {
			return nil, err
		}
	}

	if emailIdentification == nil {
		emailIdentification = &model.Identification{Identification: &sqbmodel.Identification{
			InstanceID:     samlConnection.InstanceID,
			Type:           constants.ITEmailAddress,
			VerificationID: null.StringFrom(verification.ID),
			Identifier:     null.StringFrom(user.EmailAddress),
			Status:         constants.ISVerified,
		}}

		emailIdentification.SetCanonicalIdentifier()
		if err := s.identificationRepo.Insert(ctx, tx, emailIdentification); err != nil {
			return nil, err
		}
	}

	return emailIdentification, nil
}

// Update updates saml account and user if applicable during a sign in
// 1. Update the saml account data (first name, last name, public metadata) if needed based on the data retrieved by the IdP provider
// 2. Update the user's data (first name, last name, public metadata) if not already set based on the data retrieved by the IdP provider
func (s *Service) Update(ctx context.Context, tx database.Tx, userSettings *usersettings.UserSettings, instance *model.Instance, user *model.User, samlAccount *model.SAMLAccount, samlUser *saml.User) error {
	samlAccountCols := make([]string, 0)

	if samlUser.FirstName != nil && samlAccount.FirstName.String != *samlUser.FirstName {
		samlAccount.FirstName = null.StringFromPtr(samlUser.FirstName)
		samlAccountCols = append(samlAccountCols, sqbmodel.SamlAccountColumns.FirstName)
	}
	if samlUser.LastName != nil && samlAccount.LastName.String != *samlUser.LastName {
		samlAccount.LastName = null.StringFromPtr(samlUser.LastName)
		samlAccountCols = append(samlAccountCols, sqbmodel.SamlAccountColumns.LastName)
	}

	samlAccountPublicMetadata := json.RawMessage(samlAccount.PublicMetadata)
	if len(samlUser.PublicMetadata) > 0 && !reflect.DeepEqual(samlAccountPublicMetadata, samlUser.PublicMetadata) {
		merged, err := clerkjson.Patch(samlAccountPublicMetadata, samlUser.PublicMetadata)
		if err != nil {
			return err
		}
		samlAccount.PublicMetadata = types.JSON(merged)
		samlAccountCols = append(samlAccountCols, sqbmodel.SamlAccountColumns.PublicMetadata)
	}

	if len(samlAccountCols) > 0 {
		if err := s.samlAccountRepo.Update(ctx, tx, samlAccount, samlAccountCols...); err != nil {
			return err
		}
	}

	userCols := make([]string, 0)

	if samlUser.FirstName != nil && *samlUser.FirstName != user.FirstName.String {
		user.FirstName = null.StringFromPtr(samlUser.FirstName)
		userCols = append(userCols, sqbmodel.UserColumns.FirstName)
	}
	if samlUser.LastName != nil && *samlUser.LastName != user.LastName.String {
		user.LastName = null.StringFromPtr(samlUser.LastName)
		userCols = append(userCols, sqbmodel.UserColumns.LastName)
	}

	userPublicMetadata := json.RawMessage(user.PublicMetadata)
	if len(samlUser.PublicMetadata) > 0 && !reflect.DeepEqual(userPublicMetadata, samlUser.PublicMetadata) {
		merged, err := clerkjson.Patch(userPublicMetadata, samlUser.PublicMetadata)
		if err != nil {
			return err
		}
		user.PublicMetadata = types.JSON(merged)
		userCols = append(userCols, sqbmodel.UserColumns.PublicMetadata)
	}

	if len(userCols) > 0 {
		if err := s.userRepo.Update(ctx, tx, user, userCols...); err != nil {
			return err
		}

		userSerializable, err := s.serializableService.ConvertUser(ctx, tx, userSettings, user)
		if err != nil {
			return err
		}
		if err = s.eventService.UserUpdated(ctx, tx, instance, serialize.UserToServerAPI(ctx, userSerializable)); err != nil {
			return err
		}
	}

	return nil
}

// QueryForUser fetches the SAML account depending on the data provided by the IdP provider, if already exists
// - If a user id has been provided, we query based on the 'saml_accounts.provider_user_id' column
// - If not, we query based on the 'saml_accounts.email_address' column
func (s *Service) QueryForUser(ctx context.Context, exec database.Executor, samlConnection *model.SAMLConnection, samlUser *saml.User) (*model.SAMLAccount, error) {
	if samlUser.ID != nil {
		return s.samlAccountRepo.QueryClaimedBySAMLConnectionAndProviderUserID(ctx, exec, samlConnection.ID, *samlUser.ID)
	}
	return s.samlAccountRepo.QueryClaimedBySAMLConnectionAndEmailAddress(ctx, exec, samlConnection.ID, samlUser.EmailAddress)
}

// QueryEmailIdentificationForAccountLinking fetches an email address identification, with the same identifier as
// provided by the IdP, in order to link the user to the existing identification instead of creating a new one.
func (s *Service) QueryEmailIdentificationForAccountLinking(ctx context.Context, exec database.Executor, samlConnection *model.SAMLConnection, samlUser *saml.User) (*model.Identification, error) {
	return s.identificationRepo.QueryClaimedVerifiedOrReservedByInstanceAndIdentifierAndTypePrioritizingVerified(ctx, exec, samlConnection.InstanceID, samlUser.EmailAddress, constants.ITEmailAddress)
}
