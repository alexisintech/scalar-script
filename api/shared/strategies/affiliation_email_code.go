package strategies

import (
	"context"
	"errors"
	"fmt"
	"time"

	"clerk/api/apierror"
	"clerk/api/shared/comms"
	"clerk/api/shared/orgdomain"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/activity"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type AffiliationEmailCodePreparer struct {
	clock              clockwork.Clock
	env                *model.Env
	organizationDomain *model.OrganizationDomain
	emailAddress       string

	commsService              *comms.Service
	orgDomainRepo             *repository.OrganizationDomain
	orgDomainVerificationRepo *repository.OrganizationDomainVerification
}

func NewAffiliationEmailCodePreparer(deps clerk.Deps, env *model.Env, orgDomain *model.OrganizationDomain, emailAddress string) AffiliationEmailCodePreparer {
	return AffiliationEmailCodePreparer{
		clock:                     deps.Clock(),
		env:                       env,
		organizationDomain:        orgDomain,
		emailAddress:              emailAddress,
		commsService:              comms.NewService(deps),
		orgDomainRepo:             repository.NewOrganizationDomain(),
		orgDomainVerificationRepo: repository.NewOrganizationDomainVerification(),
	}
}

func (p AffiliationEmailCodePreparer) Prepare(ctx context.Context, tx database.Tx) (*model.OrganizationDomainVerification, error) {
	otpCode, otpCodeDigest, err := generateOtpCodeWithHash(false)
	if err != nil {
		return nil, fmt.Errorf("prepare: creating OTP digest for affiliation: %w", err)
	}

	verification := &model.OrganizationDomainVerification{OrganizationDomainVerification: &sqbmodel.OrganizationDomainVerification{
		InstanceID:           p.env.Instance.ID,
		OrganizationID:       p.organizationDomain.OrganizationID,
		OrganizationDomainID: p.organizationDomain.ID,
		Strategy:             constants.VSAffiliationEmailCode,
		Attempts:             0,
		Token:                null.StringFrom(otpCodeDigest),
		ExpireAt:             null.TimeFrom(p.clock.Now().UTC().Add(time.Second * time.Duration(constants.ExpiryTimeTransactional))),
	}}
	if err = p.orgDomainVerificationRepo.Insert(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("createOrgDomainVerification: inserting new verification %+v: %w", verification, err)
	}

	deviceActivity := activity.FromContext(ctx)
	if err = p.commsService.SendAffiliationCodeEmail(ctx, tx, p.env, deviceActivity, p.organizationDomain.Name, p.emailAddress, otpCode); err != nil {
		return nil, fmt.Errorf("prepare: sending email code for %+v: %w", p.organizationDomain, err)
	}

	return verification, nil
}

type AffiliationEmailCodeAttemptor struct {
	code         string
	orgDomainID  string
	verification *model.OrganizationDomainVerification

	orgDomainService          *orgdomain.Service
	orgDomainVerificationRepo *repository.OrganizationDomainVerification
}

func NewAffiliationEmailCodeAttemptor(clock clockwork.Clock, verification *model.OrganizationDomainVerification, orgDomainID, code string) *AffiliationEmailCodeAttemptor {
	return &AffiliationEmailCodeAttemptor{
		code:                      code,
		orgDomainID:               orgDomainID,
		verification:              verification,
		orgDomainService:          orgdomain.NewService(clock),
		orgDomainVerificationRepo: repository.NewOrganizationDomainVerification(),
	}
}

func (v AffiliationEmailCodeAttemptor) Attempt(ctx context.Context, tx database.Tx) error {
	if err := v.checkVerificationStatus(ctx, tx); err != nil {
		return err
	}

	v.verification.Attempts++
	if err := v.orgDomainVerificationRepo.UpdateAttempts(ctx, tx, v.verification); err != nil {
		return err
	}

	isCodeValid := isOtpCodeValid(v.verification.Token, v.code)
	if !isCodeValid {
		return ErrInvalidCode
	}

	v.verification.OrganizationDomainID = v.orgDomainID
	return v.orgDomainVerificationRepo.UpdateOrganizationDomainID(ctx, tx, v.verification)
}

func (AffiliationEmailCodeAttemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, ErrInvalidCode) {
		return apierror.FormIncorrectCode(param.Code.Name)
	}
	return toAPIErrors(err)
}

func (v AffiliationEmailCodeAttemptor) checkVerificationStatus(ctx context.Context, tx database.Tx) error {
	status, err := v.orgDomainService.Status(ctx, tx, v.verification)
	if err != nil {
		return err
	}

	switch status {
	case constants.VERUnverified:
		return nil
	case constants.VERFailed:
		return ErrFailed
	case constants.VERExpired:
		return ErrExpired
	case constants.VERVerified:
		return ErrAlreadyVerified
	default:
		return NewUnknownStatusError(status)
	}
}
