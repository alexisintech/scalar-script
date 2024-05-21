// package emailquality provides various email domain-specific quality
// information (e.g. whether the domain is a disposable email service).
package emailquality

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clerk/model"
	"clerk/model/sqbmodel"
	errors "clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/emailaddress"
	"clerk/pkg/externalapis/ipqs"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

// we only keep the 10 most popular domains here, to avoid querying IPQS
// completely. Those that did not make it to this list, will be eventually
// persisted in the email_domain_reports table.
var knownGoodDomains = map[string]bool{
	"gmail.com":     true,
	"outlook.com":   true,
	"yahoo.com":     true,
	"yahoo.co.uk":   true,
	"hotmail.com":   true,
	"hotmail.co.uk": true,
	"hotmail.fr":    true,
	"hotmail.it":    true,
	"icloud.com":    true,
	"msn.com":       true,
	"qq.com":        true,
	"aol.com":       true,
	"me.com":        true,
	"fastmail.com":  true,
}

type EmailQuality struct {
	clock clockwork.Clock
	exec  database.Executor
	repo  *repository.EmailDomainReport
	ipqs  *ipqs.Client
}

func New(clock clockwork.Clock, exec database.Executor, ipqs *ipqs.Client) *EmailQuality {
	return &EmailQuality{
		clock: clock,
		exec:  exec,
		repo:  repository.NewEmailDomainReport(),
		ipqs:  ipqs,
	}
}

type QualityResult struct {
	Domain        string
	Common        bool
	Disposable    bool
	SourcedVia    string
	LastCheckedAt time.Time
}

func newQualityResult(report *model.EmailDomainReport) *QualityResult {
	return &QualityResult{
		Domain:        report.Domain,
		Common:        report.Common,
		Disposable:    report.Disposable,
		SourcedVia:    report.SourcedVia,
		LastCheckedAt: report.LastCheckedAt,
	}
}

// CheckQuality returns a quality report for the given email address, including
// information like whether the domain is from a disposable email service, or from
// a common one like `gmail.com`, etc.
func (d *EmailQuality) CheckQuality(ctx context.Context, email string) (*QualityResult, error) {
	domain := emailaddress.Domain(email)
	if domain == "" {
		return nil, errors.WithStacktrace("emailquality/checkQuality: empty domain (%s)", email)
	}

	domain = strings.ToLower(domain)

	if knownGoodDomains[domain] {
		return &QualityResult{
			Domain:        domain,
			Common:        true,
			Disposable:    false,
			SourcedVia:    model.SourcedFromCommonDomainList,
			LastCheckedAt: d.clock.Now().UTC(),
		}, nil
	}

	domainReport, err := d.repo.QueryByDomain(ctx, d.exec, domain)
	if err != nil {
		return nil, errors.WithStacktrace("emailquality/checkQuality: %w", err)
	}

	if domainReport != nil && !domainReport.NeedsRefresh(d.clock) {
		return newQualityResult(domainReport), nil
	}

	// query IPQS
	disposable, err := d.ipqs.DisposableEmailAddress(ctx, email)
	if err != nil {
		return nil, APIError{Err: err}
	}

	domainReport = &model.EmailDomainReport{
		EmailDomainReport: &sqbmodel.EmailDomainReport{
			Domain:        domain,
			Disposable:    disposable,
			SourcedVia:    constants.EmailDomainReportSourceIPQS,
			LastCheckedAt: d.clock.Now(),
		}}

	return newQualityResult(domainReport), d.repo.UpsertDisposable(ctx, d.exec, domainReport)
}

// CheckDomain returns the quality check results for the given domain (common & disposable)
func (d *EmailQuality) CheckDomain(ctx context.Context, domain string) (*QualityResult, error) {
	domain = strings.ToLower(domain)
	// The actual email address doesn't really matter, only the email domain.
	// However, we need an email address because of what the external IPQS service is expecting.
	dummyEmailAddress := "foo@" + domain
	return d.CheckQuality(ctx, dummyEmailAddress)
}

func (*EmailQuality) IsKnownGoodDomain(domain string) bool {
	return knownGoodDomains[domain]
}

// PopulateDisposableDomains populates the email_domain_reports table with
// known disposable domains, sourced from
// https://github.com/disposable/disposable-email-domains.
func (d *EmailQuality) PopulateDisposableDomains(ctx context.Context, client *http.Client) error {
	const domainsURL = "https://disposable.github.io/disposable-email-domains/domains.txt"

	resp, err := client.Get(domainsURL)
	if err != nil {
		return errors.WithStacktrace("emailquality/PopulateDisposableDomains: fetch list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return errors.WithStacktrace("emailquality/PopulateDisposableDomains: unexpected GitHub response (%d): %s",
			resp.StatusCode, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		err = d.repo.UpsertDisposable(ctx, d.exec, &model.EmailDomainReport{
			EmailDomainReport: &sqbmodel.EmailDomainReport{
				Domain:        scanner.Text(),
				Disposable:    true,
				SourcedVia:    constants.EmailDomainReportSourceGithubDisposable,
				LastCheckedAt: d.clock.Now(),
			}})
		if err != nil {
			return fmt.Errorf("emailquality/PopulateDisposableDomains: upsert: %w", err)
		}
	}

	err = scanner.Err()
	if err != nil {
		return fmt.Errorf("emailquality/PopulateDisposableDomains: scan: %w", err)
	}

	return nil
}

// PopulateCommonDomains populates the email_domain_reports table with
// known common email provider domains, sourced from Hubspot.
// https://knowledge.hubspot.com/forms/what-domains-are-blocked-when-using-the-forms-email-domains-to-block-feature
func (d *EmailQuality) PopulateCommonDomains(ctx context.Context, client *http.Client) error {
	const hubspotFreeDomainsURL = "https://f.hubspotusercontent40.net/hubfs/2832391/Marketing/Lead-Capture/free-domains-2.csv"

	resp, err := client.Get(hubspotFreeDomainsURL)
	if err != nil {
		return errors.WithStacktrace("emailquality/PopulateCommonDomains: fetch list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return errors.WithStacktrace("emailquality/PopulateCommonDomains: unexpected Hubspot response (%d): %s",
			resp.StatusCode, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		// SourcedVia is only set on insert
		err = d.repo.UpsertCommon(ctx, d.exec, &model.EmailDomainReport{
			EmailDomainReport: &sqbmodel.EmailDomainReport{
				Domain:     scanner.Text(),
				Common:     true,
				SourcedVia: constants.EmailDomainReportSourceHubspot,
			}})
		if err != nil {
			return fmt.Errorf("emailquality/PopulateCommonDomains: upsert: %w", err)
		}
	}

	err = scanner.Err()
	if err != nil {
		return fmt.Errorf("emailquality/PopulateCommonDomains: scan: %w", err)
	}

	return nil
}
