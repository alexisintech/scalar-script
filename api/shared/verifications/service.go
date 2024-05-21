package verifications

import (
	"context"
	"errors"
	"fmt"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

var (
	ErrVerificationNotFound = errors.New("verification not found")
)

type Service struct {
	clock clockwork.Clock

	// repositories
	accountTransferRepo *repository.AccountTransfers
	identificationRepo  *repository.Identification
	signInRepo          *repository.SignIn
	verificationRepo    *repository.Verification
}

func NewService(clock clockwork.Clock) *Service {
	return &Service{
		clock:               clock,
		accountTransferRepo: repository.NewAccountTransfers(),
		identificationRepo:  repository.NewIdentification(),
		signInRepo:          repository.NewSignIn(),
		verificationRepo:    repository.NewVerification(),
	}
}

func (s *Service) VerificationWithStatus(
	ctx context.Context,
	exec database.Executor,
	verificationID string) (*model.VerificationWithStatus, error) {
	verification, err := s.verificationRepo.QueryByID(ctx, exec, verificationID)
	if err != nil {
		return nil, fmt.Errorf("verificationWithStatus: fetching verification %s: %w", verificationID, err)
	}
	if verification == nil {
		return nil, nil
	}

	status, err := s.Status(ctx, exec, verification)
	if err != nil {
		return nil, fmt.Errorf("verificationWithStatus: status of %+v: %w", verification, err)
	}

	return &model.VerificationWithStatus{
		Verification: verification,
		Status:       status,
	}, nil
}

func (s *Service) Clear(ctx context.Context, exec database.Executor, accountTransferID string) (*model.Verification, error) {
	verification, err := s.verificationRepo.QueryByAccountTransferID(ctx, exec, accountTransferID)
	if err != nil {
		return nil, err
	}
	if verification == nil {
		return nil, ErrVerificationNotFound
	}

	verification.Error = null.JSONFromPtr(nil)
	verification.AccountTransferID = null.StringFromPtr(nil)
	err = s.verificationRepo.Update(ctx, exec, verification, sqbmodel.VerificationColumns.Error, sqbmodel.VerificationColumns.AccountTransferID)
	if err != nil {
		return nil, err
	}

	return verification, nil
}

func (s *Service) ClearErrors(ctx context.Context, exec database.Tx, verificationID null.String) error {
	if !verificationID.Valid {
		return nil
	}

	ver, err := s.verificationRepo.FindByID(ctx, exec, verificationID.String)
	if err != nil {
		return fmt.Errorf("clearVerificationErrors: fetching verification %s: %w", verificationID.String, err)
	}

	ver.Error = null.JSONFromPtr(nil)
	if err = s.verificationRepo.Update(ctx, exec, ver, sqbmodel.VerificationColumns.Error); err != nil {
		return fmt.Errorf("clearVerificationErrors: updating verification %s: %w", verificationID.String, err)
	}

	return nil
}

// Status returns the current status of the provided verification.
// NOTE: this is a security-sensitive method, since a verification's status determines if it can be processed
// during the OAuth Callback flow and so plays a critical role in protecting us against replay attacks.
// That is, the OAuth Callback flow can only proceed if the verification's status is 'Unverified'.
func (s *Service) Status(ctx context.Context, exec database.Executor, ver *model.Verification) (string, error) {
	if ver.AccountTransferID.Valid {
		accountTransfer, err := s.accountTransferRepo.FindByInstanceAndID(ctx, exec, ver.InstanceID, ver.AccountTransferID.String)
		if err != nil {
			return "", fmt.Errorf("verifications/status: failed to fetch account transfer %s for %+v: %w", ver.AccountTransferID.String, ver, err)
		}

		if !accountTransfer.Expired(s.clock) {
			return constants.VERTransferable, nil
		}
	}

	isVerified, err := s.isVerified(ctx, exec, ver.ID)
	if err != nil {
		return "", err
	} else if isVerified {
		return constants.VERVerified, nil
	}

	if ver.Failed() {
		return constants.VERFailed, nil
	} else if ver.Expired(s.clock) {
		return constants.VERExpired, nil
	}

	return constants.VERUnverified, nil
}

func (s *Service) isVerified(ctx context.Context, exec database.Executor, verID string) (bool, error) {
	identification, err := s.identificationRepo.QueryByVerificationID(ctx, exec, verID)
	if err != nil {
		return false, fmt.Errorf("verifications/isVerified: failed to fetch identification for verification %s: %w", verID, err)
	}

	if identification != nil {
		return identification.IsVerified(), nil
	}

	signIn, err := s.signInRepo.QueryBySuccessfulVerificationID(ctx, exec, verID)
	if err != nil {
		return false, fmt.Errorf("verifications/isVerified: failed to fetch sign in for success verification %s: %w", verID, err)
	}

	if signIn != nil {
		return true, nil
	}

	return false, nil
}
