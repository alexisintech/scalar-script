package strategies

import (
	"context"
	"errors"
	"fmt"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/backup_codes"
	"clerk/pkg/constants"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/param"
)

type BackupCodeAttemptor struct {
	backupCode *model.BackupCode
	env        *model.Env

	providedCode string

	backupCodeRepo   *repository.BackupCode
	verificationRepo *repository.Verification
}

type BackupCodeAttemptorParams struct {
	Env          *model.Env
	BackupCode   *model.BackupCode
	ProvidedCode string
}

func NewBackupCodeAttemptor(params BackupCodeAttemptorParams) BackupCodeAttemptor {
	return BackupCodeAttemptor{
		env:              params.Env,
		backupCode:       params.BackupCode,
		providedCode:     params.ProvidedCode,
		backupCodeRepo:   repository.NewBackupCode(),
		verificationRepo: repository.NewVerification(),
	}
}

func (v BackupCodeAttemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	currentBackupCode := v.backupCode

	verification := &model.Verification{Verification: &sqbmodel.Verification{
		InstanceID: v.env.Instance.ID,
		Strategy:   constants.VSBackupCode,
		Attempts:   1,
	}}
	err := v.verificationRepo.Insert(ctx, tx, verification)
	if err != nil {
		return nil, fmt.Errorf("backup_code/attempt: inserting new verification %+v: %w", verification, err)
	}

	updatedCodes, err := backup_codes.Consume(currentBackupCode.Codes, v.providedCode)
	if err != nil {
		return nil, fmt.Errorf("backup_code/attempt: consuming backup code: %w", err)
	}

	currentBackupCode.Codes = updatedCodes
	if err = v.backupCodeRepo.UpdateCodes(ctx, tx, currentBackupCode); err != nil {
		return nil, fmt.Errorf("backup_code/attempt: updating backup codes: %w", err)
	}

	return verification, nil
}

func (BackupCodeAttemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, backup_codes.ErrInvalidCode) {
		return apierror.FormIncorrectCode(param.Code.Name)
	}
	return apierror.Unexpected(err)
}
