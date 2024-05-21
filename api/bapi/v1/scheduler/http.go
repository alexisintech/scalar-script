package scheduler

import (
	"net/http"
	"strconv"

	"clerk/api/apierror"
	"clerk/api/bapi/v1/cleanup"
	"clerk/api/bapi/v1/dnschecks"
	"clerk/api/bapi/v1/pricing"
	"clerk/api/shared/emailquality"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/dns"
	"clerk/pkg/jobs"
	"clerk/utils/clerk"
	"clerk/utils/param"

	"github.com/vgarvardt/gue/v2"
)

type HTTP struct {
	gueClient           *gue.Client
	pricingService      *pricing.Service
	cleanupService      *cleanup.Service
	dnsService          *dnschecks.Service
	schedulerService    *Service
	emailQualityService *emailquality.EmailQuality
}

func NewHTTP(
	deps clerk.Deps,
	paymentProvider clerkbilling.PaymentProvider,
	dnsResolver dns.Resolver,
) *HTTP {
	return &HTTP{
		gueClient:           deps.GueClient(),
		pricingService:      pricing.NewService(deps, paymentProvider),
		cleanupService:      cleanup.NewService(deps.Clock(), deps.DB(), deps.GueClient()),
		dnsService:          dnschecks.NewService(deps.DB(), dnsResolver, deps.GueClient(), deps.CloudflareIPRangeClient(), deps.CertCheckHostHealthHTTPClient()),
		schedulerService:    NewService(deps.GueClient()),
		emailQualityService: deps.EmailQualityChecker(),
	}
}

// Middleware /v1/internal
func (h *HTTP) CheckSchedulerToken(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	schedulerToken := r.Header.Get("X-Scheduler-Token")

	if schedulerToken != cenv.Get(cenv.GoogleSchedulerToken) {
		return nil, apierror.InvalidAuthorization()
	}

	return r, nil
}

// POST /v1/internal/cleanup/dead_sessions_job
func (h *HTTP) DeadSessionsJob(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	err := h.cleanupService.DeadSessionsJob(r.Context(), getLimit(r))
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

func getLimit(r *http.Request) int {
	// ignoring the error here...if it's 0, the default limit will be applied
	limit, _ := strconv.Atoi(r.URL.Query().Get(param.Limit.Name))
	return limit
}

// POST /v1/internal/cleanup/orphan_applications
func (h *HTTP) OrphanApplications(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if err := h.cleanupService.OrphanApplications(r.Context(), getLimit(r)); err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /v1/internal/cleanup/orphan_organizations
func (h *HTTP) OrphanOrganizations(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if err := h.cleanupService.OrphanOrganizations(r.Context(), getLimit(r)); err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /v1/internal/cleanup/expired_oauth_tokens
func (h *HTTP) ExpiredOAuthTokens(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	err := h.cleanupService.ExpiredOAuthTokens(r.Context())
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /v1/internal/stripe/usage_report_jobs
func (h *HTTP) StripeUsageReportJobs(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if err := h.pricingService.CreateUsageReportJobs(r.Context()); err != nil {
		return nil, apierror.Unexpected(err)
	}
	return nil, nil
}

// POST /v1/internal/stripe/sync_plans
func (h *HTTP) SyncStripePlans(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if err := h.pricingService.SyncPlans(r.Context()); err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /v1/internal/cloudflare/monitor_custom_hostname
func (h *HTTP) MonitorCustomHostname(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if err := h.schedulerService.MonitorCustomHostname(r.Context()); err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /v1/internal/dns/enqueue_checks
func (h *HTTP) DNSChecks(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if err := h.dnsService.EnqueueChecks(r.Context()); err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /v1/internal/email_domain_reports/populate_disposable
func (h *HTTP) PopulateDisposableEmailDomains(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	err := jobs.PopulateDisposableDomains(r.Context(), h.gueClient)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	w.WriteHeader(http.StatusNoContent)

	return nil, nil
}

// POST /v1/internal/email_domain_reports/populate_common
func (h *HTTP) PopulateCommonEmailDomains(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	err := jobs.PopulateCommonDomains(r.Context(), h.gueClient)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	w.WriteHeader(http.StatusNoContent)

	return nil, nil
}

type GeneratePRReviewReportParams struct {
	Teams               []string `json:"teams"`
	Repos               []string `json:"repos"`
	SlackChannelWebhook string   `json:"slack_channel_webhook"`
	ExcludedUsers       []string `json:"excluded_users"`
	SinceDaysBefore     int      `json:"since_days_before"`
}

// POST /v1/internal/engineering-ops/github/generate_pr_review_report
func (h *HTTP) GeneratePRReviewReport(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := GeneratePRReviewReportParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	err := jobs.GenerateGithubReport(r.Context(), h.gueClient, jobs.GenerateGithubReportArgs{
		Teams:               params.Teams,
		Repos:               params.Repos,
		SlackChannelWebhook: params.SlackChannelWebhook,
		ExcludedUsers:       params.ExcludedUsers,
		SinceDaysBefore:     params.SinceDaysBefore,
	})
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /v1/internal/hype_stats
func (h *HTTP) CreateHypeStats(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if err := h.schedulerService.EnqueueHypeStatsJob(r.Context()); err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /v1/internal/stripe/refresh_cache_responses
func (h *HTTP) StripeRefreshCacheResponses(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if err := h.pricingService.RefreshCacheResponses(r.Context()); err != nil {
		return nil, apierror.Unexpected(err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /v1/internal/webauthn/refresh_authenticator_data
func (h *HTTP) RefreshWebAuthnAuthenticatorData(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	err := jobs.RefreshWebAuthnAuthenticatorData(r.Context(), h.gueClient)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	w.WriteHeader(http.StatusNoContent)

	return nil, nil
}
