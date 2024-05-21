package dnschecks

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cert"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/dns"
	"clerk/pkg/generate"
	"clerk/pkg/jobs"
	sentryclerk "clerk/pkg/sentry"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
	"golang.org/x/sync/errgroup"
)

type Service struct {
	db                        database.Database
	dnsResolver               dns.Resolver
	dnsChecksRepo             *repository.DNSChecks
	jobsRepo                  *repository.GueJobs
	gueClient                 *gue.Client
	cloudflareIPRangeClient   clerk.CloudflareIPRangeClient
	hostHealthCheckHTTPClient *http.Client
}

type (
	CNAMEVerificationResult map[string]bool
	CNAMEDNSResult          map[string]dns.Response
)

// After this number of failures we won't try again
const maxFailures = 30

func NewService(db database.Database, dnsResolver dns.Resolver, gueClient *gue.Client, cloudflareIPRangeClient clerk.CloudflareIPRangeClient, hostHealthCheckHTTPClient *http.Client) *Service {
	return &Service{
		db:                        db,
		dnsResolver:               dnsResolver,
		cloudflareIPRangeClient:   cloudflareIPRangeClient,
		dnsChecksRepo:             repository.NewDNSChecks(),
		jobsRepo:                  repository.NewGueJobs(),
		gueClient:                 gueClient,
		hostHealthCheckHTTPClient: hostHealthCheckHTTPClient,
	}
}

// EnqueueChecks enqueues a background job for every domain which hasn't yet
// passed the DNS checks (up to a limit of retries)
func (s *Service) EnqueueChecks(ctx context.Context) apierror.Error {
	checks, err := s.dnsChecksRepo.QueryUnsuccessfulUnqueuedByTimesChecked(ctx, s.db, maxFailures)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if len(checks) == 0 {
		return nil
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		for _, check := range checks {
			err := jobs.CheckDNSRecords(ctx, s.gueClient,
				jobs.CheckDNSRecordsArgs{
					DomainID: check.DomainID,
				}, jobs.WithTx(tx))
			if err != nil {
				return true, err
			}
			check.JobInflight = true
			if err := s.dnsChecksRepo.Update(ctx, tx, check); err != nil {
				return true, err
			}
		}
		return false, nil
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}
	return nil
}

// CheckAndUpdate checks the DNS record and stores the result
func (s *Service) CheckAndUpdate(ctx context.Context, check *model.DNSCheck) error {
	var reqs generate.CNAMERequirements
	if err := json.Unmarshal(check.CnameRequirements, &reqs); err != nil {
		return err
	}

	cnameChecker := NewCNAMEChecker(s.dnsResolver, s.hostHealthCheckHTTPClient, s.cloudflareIPRangeClient)

	result, dnsResults, err := cnameChecker.CheckAll(ctx, reqs)
	if err != nil {
		return err
	}

	var investigateRecords []string
	for subdomain, isSuccess := range result {
		if isSuccess {
			continue
		}
		investigateRecords = append(investigateRecords, subdomain)
	}
	failureHints, err := cnameChecker.InvestigateDNSCheckFailures(ctx, investigateRecords, dnsResults)
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}

	allSuccess := true
	for _, isSuccess := range result {
		if !isSuccess {
			allSuccess = false
			break
		}
	}

	rawResult, err := json.Marshal(result)
	if err != nil {
		return err
	}
	failureHintResult, err := json.Marshal(failureHints)
	if err != nil {
		return clerkerrors.WithStacktrace("failure hint serialization to JSON failed: %w", err)
	}

	check.Successful = allSuccess
	check.LastResult = rawResult
	check.LastRunAt = null.TimeFrom(time.Now().UTC())
	check.TimesChecked++
	check.LastFailureHints = failureHintResult

	cols := []string{
		sqbmodel.DNSCheckColumns.Successful,
		sqbmodel.DNSCheckColumns.LastResult,
		sqbmodel.DNSCheckColumns.LastRunAt,
		sqbmodel.DNSCheckColumns.TimesChecked,
		sqbmodel.DNSCheckColumns.LastFailureHints,
	}

	return s.dnsChecksRepo.Update(ctx, s.db, check, cols...)
}

type CNAMEChecker struct {
	dnsResolver             dns.Resolver
	httpClient              *http.Client
	cloudflareIPRangeClient clerk.CloudflareIPRangeClient
}

func NewCNAMEChecker(resolver dns.Resolver, httpClient *http.Client, cloudflareIPRangeClient clerk.CloudflareIPRangeClient) CNAMEChecker {
	return CNAMEChecker{
		dnsResolver:             resolver,
		httpClient:              httpClient,
		cloudflareIPRangeClient: cloudflareIPRangeClient,
	}
}

func (c CNAMEChecker) CheckAll(ctx context.Context, reqs generate.CNAMERequirements) (CNAMEVerificationResult, CNAMEDNSResult, error) {
	result := CNAMEVerificationResult{}
	dnsResults := CNAMEDNSResult{}

	group, ctx := errgroup.WithContext(ctx)
	var lock sync.Mutex

	for from, target := range reqs {
		if target.Optional {
			continue
		}
		from, target := from, target
		group.Go(func() error {
			verified, dnsResult, err := c.CheckSingle(ctx, from, target)
			if err != nil {
				return err
			}
			lock.Lock()
			defer lock.Unlock()
			result[from] = verified
			dnsResults[from] = dnsResult
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, nil, err
	}

	return result, dnsResults, nil
}

func (c CNAMEChecker) CheckSingle(ctx context.Context, record string, target generate.CNAMETarget) (bool, dns.Response, error) {
	results, err := c.dnsResolver.Resolve(ctx, record)
	if err != nil {
		return false, results, err
	}
	if results.Matches(target.Target) {
		return true, results, nil
	}

	if !target.CanBeProxied {
		return false, results, nil
	}

	switch target.ClerkSubdomain {
	case model.AuthCNAME, model.AccountsCNAME:
		err = cert.CheckHostHealth(ctx, record, c.httpClient)
		if err != nil {
			return false, results, nil
		}
	}

	return true, results, nil
}

func (c CNAMEChecker) InvestigateDNSCheckFailures(ctx context.Context, records []string, dnsResults CNAMEDNSResult) (map[string][]generate.DNSCheckFailureHint, error) {
	// Fetch Cloudflare IP CIDR ranges
	cloudflareNetworks, err := c.cloudflareIPRangeClient.GetRangeLists(ctx)
	if err != nil {
		return nil, err
	}

	hints := make(map[string][]generate.DNSCheckFailureHint)
	for _, record := range records {
		dnsQueryResult := dnsResults[record]
		var pointsToCloudflareIP bool
		for _, resolved := range dnsQueryResult {
			ip := net.ParseIP(resolved)
			// If a result is not an IP address there's probably a CNAME in the answer chain.
			// In this case, we might still find a record pointing to Cloudflare, but it's a more
			// complicated scenario, and we can't conclude that Orange-to-Orange is the issue here.
			if ip == nil {
				break
			}
			pointsToCloudflareIP = cloudflareNetworks.Contains(ip)
			if pointsToCloudflareIP {
				break
			}
		}
		if pointsToCloudflareIP {
			hints[record] = append(hints[record], generate.DNSCheckFailureHint{
				Code: constants.DNSCheckHintCodeCloudflareOrangeToOrange,
				Message: "Record seems to be pointing directly to Cloudflare IP ranges. " +
					"Please check if your record is in Proxy status in Cloudflare.",
			})
		}
	}

	return hints, nil
}
