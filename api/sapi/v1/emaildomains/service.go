package emaildomains

import (
	"context"
	"strings"

	"clerk/api/apierror"
	"clerk/api/sapi/serialize"
	"clerk/api/shared/emailquality"
	"clerk/model"
	"clerk/pkg/emailaddress"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	clock                 clockwork.Clock
	db                    database.Database
	emailQualityChecker   *emailquality.EmailQuality
	emailDomainReportRepo *repository.EmailDomainReport
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:                 deps.Clock(),
		db:                    deps.DB(),
		emailQualityChecker:   deps.EmailQualityChecker(),
		emailDomainReportRepo: repository.NewEmailDomainReport(),
	}
}

func (s *Service) Read(ctx context.Context, emailDomain string) (*serialize.EmailQualityResponse, apierror.Error) {
	emailDomain = strings.ToLower(emailDomain)
	report, err := s.emailDomainReportRepo.QueryByDomain(ctx, s.db, emailDomain)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if report == nil {
		return nil, apierror.ResourceNotFound()
	}
	return serialize.EmailDomain(report), nil
}

type CheckEmailQualityParams struct {
	EmailAddressOrDomain string `json:"email_address_or_domain"`
}

func (s *Service) CheckQuality(ctx context.Context, params CheckEmailQualityParams) (*serialize.EmailQualityResponse, apierror.Error) {
	params.EmailAddressOrDomain = strings.ToLower(params.EmailAddressOrDomain)

	domain := emailaddress.Domain(params.EmailAddressOrDomain)
	if domain == "" {
		// we have a domain, not an email address
		domain = params.EmailAddressOrDomain
	}

	qualityResult, err := s.emailQualityChecker.CheckDomain(ctx, domain)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return serialize.EmailQuality(qualityResult), nil
}

type UpdateParams struct {
	EmailAddressOrDomain string `json:"email_address_or_domain"`
	Disposable           bool   `json:"disposable"`
}

var nonModifiableDomains = set.New[string](
	"clerkstage.dev",
	"clerk.dev",
	"clerk.com",
)

func (s *Service) Update(ctx context.Context, params UpdateParams) (*serialize.EmailQualityResponse, apierror.Error) {
	params.EmailAddressOrDomain = strings.ToLower(params.EmailAddressOrDomain)

	domain := emailaddress.Domain(params.EmailAddressOrDomain)
	if domain == "" {
		// we have a domain, not an email address
		domain = params.EmailAddressOrDomain
	}

	if nonModifiableDomains.Contains(domain) || s.emailQualityChecker.IsKnownGoodDomain(domain) {
		return nil, apierror.CannotUpdateGivenDomain(domain)
	}

	report, err := s.emailDomainReportRepo.QueryByDomain(ctx, s.db, domain)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if report == nil {
		return nil, apierror.EmailDomainNotFound(domain)
	}
	report.Disposable = params.Disposable
	report.LastCheckedAt = s.clock.Now().UTC()
	report.SourcedVia = model.SourcedManually
	if err := s.emailDomainReportRepo.UpsertDisposable(ctx, s.db, report); err != nil {
		return nil, apierror.Unexpected(err)
	}
	return serialize.EmailDomain(report), nil
}
