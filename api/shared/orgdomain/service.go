package orgdomain

import (
	"context"
	"fmt"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/emailaddress"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock clockwork.Clock

	// repositories
	orgDomainRepo             *repository.OrganizationDomain
	orgDomainVerificationRepo *repository.OrganizationDomainVerification
	orgInvitationRepo         *repository.OrganizationInvitation
	orgMembershipRepo         *repository.OrganizationMembership
	orgSuggestionRepo         *repository.OrganizationSuggestion
	roleRepo                  *repository.Role
}

func NewService(clock clockwork.Clock) *Service {
	return &Service{
		clock:                     clock,
		orgDomainRepo:             repository.NewOrganizationDomain(),
		orgDomainVerificationRepo: repository.NewOrganizationDomainVerification(),
		orgInvitationRepo:         repository.NewOrganizationInvitation(),
		orgMembershipRepo:         repository.NewOrganizationMembership(),
		orgSuggestionRepo:         repository.NewOrganizationSuggestion(),
		roleRepo:                  repository.NewRole(),
	}
}

func (s *Service) CreateInvitationsSuggestionsForUserEmail(ctx context.Context, tx database.Tx, authConfig *model.AuthConfig, emailAddress, instanceID, userID string) error {
	if !authConfig.IsOrganizationDomainsEnabled() {
		return nil
	}

	emailDomain := emailaddress.Domain(emailAddress)

	orgDomain, err := s.orgDomainRepo.QueryVerifiedByInstanceAndName(ctx, tx, instanceID, emailDomain)
	if err != nil {
		return err
	}
	if orgDomain == nil {
		return nil
	}

	exists, err := s.orgMembershipRepo.ExistsByOrganizationAndUser(ctx, tx, orgDomain.OrganizationID, userID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	switch orgDomain.EnrollmentMode {
	case constants.EnrollmentModeManualInvitation:
		return nil
	case constants.EnrollmentModeAutomaticInvitation:
		exists, err := s.orgInvitationRepo.ExistsPendingByOrganizationAndEmail(ctx, tx, orgDomain.OrganizationID, emailAddress)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}

		defaultInvitationRole, err := s.roleRepo.FindByKeyAndInstance(ctx, tx, authConfig.OrganizationSettings.Domains.DefaultRole, instanceID)
		if err != nil {
			return err
		}

		invitation := &model.OrganizationInvitation{OrganizationInvitation: &sqbmodel.OrganizationInvitation{
			InstanceID:           orgDomain.InstanceID,
			EmailAddress:         emailAddress,
			Status:               constants.StatusPending,
			OrganizationID:       orgDomain.OrganizationID,
			UserID:               null.StringFrom(userID),
			OrganizationDomainID: null.StringFrom(orgDomain.ID),
			RoleID:               null.StringFrom(defaultInvitationRole.ID),
		}}
		return s.orgInvitationRepo.Insert(ctx, tx, invitation)
	case constants.EnrollmentModeAutomaticSuggestion:
		suggestion := &model.OrganizationSuggestion{OrganizationSuggestion: &sqbmodel.OrganizationSuggestion{
			InstanceID:           orgDomain.InstanceID,
			OrganizationID:       orgDomain.OrganizationID,
			UserID:               userID,
			OrganizationDomainID: null.StringFrom(orgDomain.ID),
			Status:               constants.StatusPending,
			EmailAddress:         emailAddress,
		}}
		return s.orgSuggestionRepo.Insert(ctx, tx, suggestion)
	default:
		return nil
	}
}

func (s *Service) VerificationWithStatus(ctx context.Context, exec database.Executor, verID string) (*model.OrganizationDomainVerificationWithStatus, error) {
	verification, err := s.orgDomainVerificationRepo.QueryByID(ctx, exec, verID)
	if err != nil {
		return nil, fmt.Errorf("verificationWithStatus: fetching verification %s: %w", verID, err)
	}
	if verification == nil {
		return nil, nil
	}

	status, err := s.Status(ctx, exec, verification)
	if err != nil {
		return nil, fmt.Errorf("verificationWithStatus: fetching verification %s: %w", verID, err)
	}

	return &model.OrganizationDomainVerificationWithStatus{
		OrganizationDomainVerification: verification,
		Status:                         status,
	}, nil
}

func (s *Service) Status(ctx context.Context, exec database.Executor, verification *model.OrganizationDomainVerification) (string, error) {
	orgDomain, err := s.orgDomainRepo.QueryByVerification(ctx, exec, verification.ID)
	if err != nil {
		return "", err
	}

	if orgDomain != nil && orgDomain.Verified {
		return constants.VERVerified, nil
	}

	if verification.Attempts > 2 {
		return constants.VERFailed, nil
	}

	now := s.clock.Now().UTC()
	if verification.ExpireAt.Valid && now.After(verification.ExpireAt.Time) || now.Equal(verification.ExpireAt.Time) {
		return constants.VERExpired, nil
	}

	return constants.VERUnverified, nil
}

func (s *Service) DeletePendingInvitationsAndSuggestionsForVerifiedEmail(ctx context.Context, tx database.Tx, ident *model.Identification) error {
	if ident.IsEmailAddress() && ident.IsVerified() {
		err := s.orgInvitationRepo.DeleteDomainAndPendingByInstanceAndEmailAddress(ctx, tx, ident.InstanceID, *ident.EmailAddress())
		if err != nil {
			return err
		}
		err = s.orgSuggestionRepo.DeletePendingByInstanceAndEmailAddress(ctx, tx, ident.InstanceID, *ident.EmailAddress())
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) DeletePendingInvitationsAndSuggestionsForUserAndOrg(ctx context.Context, tx database.Tx, userID, orgID string) error {
	err := s.orgInvitationRepo.DeleteDomainAndPendingByUserAndOrg(ctx, tx, userID, orgID)
	if err != nil {
		return err
	}
	return s.orgSuggestionRepo.DeletePendingByUserAndOrg(ctx, tx, userID, orgID)
}
