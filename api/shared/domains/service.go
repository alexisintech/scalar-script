package domains

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"clerk/api/bapi/v1/dnschecks"
	"clerk/api/shared/edgecache"
	sharedserialize "clerk/api/shared/serialize"
	"clerk/model"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/generate"
	"clerk/pkg/jobs"
	sentryclerk "clerk/pkg/sentry"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
)

type Service struct {
	clock         clockwork.Clock
	gueClient     *gue.Client
	dnsChecksRepo *repository.DNSChecks
	domainRepo    *repository.Domain
	gueJobRepo    *repository.GueJobs

	// DNS checks
	cnameChecker dnschecks.CNAMEChecker
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:         deps.Clock(),
		gueClient:     deps.GueClient(),
		dnsChecksRepo: repository.NewDNSChecks(),
		domainRepo:    repository.NewDomain(),
		gueJobRepo:    repository.NewGueJobs(),
		cnameChecker:  dnschecks.NewCNAMEChecker(deps.DNSResolver(), deps.CertCheckHostHealthHTTPClient(), deps.CloudflareIPRangeClient()),
	}
}

// Delete cleans up any resources that we have set up in 3rd party
// services for the domain, like Sendgrid and Cloudflare.
// It will also delete any DNS checks for the domain before deleting
// the domain itself.
func (s *Service) Delete(
	ctx context.Context,
	txEmitter database.TxEmitter,
	domain *model.Domain,
) error {
	err := s.cleanupDomain(ctx, &cleanupDomainParams{
		tx:           txEmitter,
		domain:       domain,
		reinitialize: false,
	})
	if err != nil {
		return err
	}
	err = s.domainRepo.DeleteByID(ctx, txEmitter, domain.ID)
	if err != nil {
		return err
	}

	err = edgecache.PurgeAllFapiByDomain(ctx, s.gueClient, txEmitter, domain)
	if err != nil {
		return err
	}

	return edgecache.PurgeJWKS(ctx, s.gueClient, txEmitter, domain)
}

// Reset performs the same operations as Delete but instead of deleting
// the domain record, it resets the attributes related to sendgrid and
// cloudflare.
func (s *Service) Reset(
	ctx context.Context,
	tx database.Tx,
	domain *model.Domain,
) error {
	return s.cleanupDomain(ctx, &cleanupDomainParams{
		tx:           tx,
		domain:       domain,
		reinitialize: true,
	})
}

type cleanupDomainParams struct {
	tx           database.Tx
	domain       *model.Domain
	reinitialize bool
}

func (s *Service) cleanupDomain(ctx context.Context, params *cleanupDomainParams) error {
	s.cleanupSendgrid(ctx, params)
	s.cleanupCloudflare(ctx, params)
	return s.dnsChecksRepo.DeleteByDomainID(ctx, params.tx, params.domain.ID)
}

func (s *Service) cleanupSendgrid(ctx context.Context, params *cleanupDomainParams) {
	if !params.domain.SendgridDomainID.Valid || !params.domain.SendgridSubusername.Valid {
		return
	}

	subuser := params.domain.SendgridSubusername.String
	sendgridDomainID := strconv.Itoa(params.domain.SendgridDomainID.Int)

	err := jobs.SendgridCleanupResources(
		ctx,
		s.gueClient,
		jobs.SendgridCleanupResourcesArgs{
			SengridSubuser:  subuser,
			SengridDomainID: sendgridDomainID,
			DomainID:        params.domain.ID,
			Reinitialize:    params.reinitialize,
		},
		jobs.WithTx(params.tx),
	)
	if err != nil {
		sentryclerk.CaptureException(ctx, clerkerrors.WithStacktrace(
			"domain/cleanupSendgrid: error enqueuing domain deletion (subuser=%s, domain=%+v): %w", subuser, params.domain, err))
	}
}

func (s *Service) cleanupCloudflare(ctx context.Context, params *cleanupDomainParams) {
	for _, hostID := range params.domain.CloudflareHostIDs() {
		jobArgs := jobs.RemoveHostnameCloudflareArgs{
			DomainID:         params.domain.ID,
			CloudflareHostID: hostID,
		}

		err := jobs.RemoveHostnameCloudflare(ctx, s.gueClient, jobArgs, jobs.WithTx(params.tx))
		if err != nil {
			sentryclerk.CaptureException(ctx, clerkerrors.WithStacktrace(
				"domain/cleanupCloudflare: (host=%s, domain=%+v): %w",
				hostID, params.domain.ID, err))
		}
	}
}

func (s *Service) ProvisionCertificate(ctx context.Context, tx database.Tx, domain *model.Domain) error {
	return jobs.ProvisionDomainCloudflare(ctx,
		s.gueClient,
		jobs.ProvisionDomainCloudflareArgs{DomainID: domain.ID},
		jobs.WithTx(tx),
	)
}

func (s *Service) ScheduleSendgridVerification(ctx context.Context, tx database.Tx, domain *model.Domain) error {
	err := jobs.VerifySendgrid(ctx, s.gueClient,
		jobs.VerifySendgridArgs{DomainID: domain.ID},
		jobs.WithTx(tx))
	if err != nil {
		return fmt.Errorf("scheduling verify sendgrid job: %w", err)
	}

	domain.SendgridJobInflight = true
	if err = s.domainRepo.UpdateSendgridJobInflight(ctx, tx, domain); err != nil {
		return fmt.Errorf("updating sendgrid job in flight for domain %s: %w",
			domain.ID, err)
	}
	return nil
}

func (s *Service) RefreshCNAMERequirements(
	ctx context.Context, exec database.Executor, instance *model.Instance, domain *model.Domain) error {
	dnsCheck, err := s.dnsChecksRepo.QueryByDomainID(ctx, exec, domain.ID)
	if err != nil {
		return err
	} else if dnsCheck == nil {
		return nil
	}

	cnameRequirements := model.CNAMERequirements(instance, domain)
	reqs := generate.CNAMERequirements{}
	for _, cnameRequirement := range cnameRequirements {
		reqs[cnameRequirement.Label.FqdnSource(domain)] = generate.CNAMETarget{
			Target:         cnameRequirement.Label.FqdnTarget(domain),
			CanBeProxied:   cnameRequirement.Label.CanBeProxied(),
			ClerkSubdomain: cnameRequirement.Label.ClerkSubdomain(domain),
			Optional:       cnameRequirement.Optional,
		}
	}

	rawRequirements, err := json.Marshal(reqs)
	if err != nil {
		return err
	}

	dnsCheck.CnameRequirements = rawRequirements
	return s.dnsChecksRepo.UpdateCNAMERequirements(ctx, exec, dnsCheck)
}

func (s *Service) GetDeployStatus(
	ctx context.Context,
	domain *model.Domain,
	instance *model.Instance,
	dnsCheck *model.DNSCheck,
	proxyCheck *model.ProxyCheck,
) (*sharedserialize.DomainStatusResponse, error) {
	dnsStatus, err := s.getDNSStatus(ctx, domain, instance, dnsCheck)
	if err != nil {
		return nil, err
	}

	return sharedserialize.DomainStatus(
		dnsStatus,
		getSSLStatus(domain, instance, dnsStatus),
		getMailStatus(domain, instance),
		GetProxyStatus(domain, proxyCheck),
	), nil
}

func (s *Service) getDNSStatus(
	ctx context.Context,
	domain *model.Domain,
	instance *model.Instance,
	dnsCheck *model.DNSCheck,
) (*sharedserialize.DNSStatus, error) {
	if !instance.IsProduction() {
		return &sharedserialize.DNSStatus{
			Status: constants.DNSComplete,
			CNAMES: make(map[string]*sharedserialize.CNAMEStatus),
		}, nil
	}
	if dnsCheck != nil {
		return getCachedDNSStatus(dnsCheck)
	}
	return getRealtimeDNSStatus(ctx, domain, instance, s.cnameChecker)
}

// Returns the DNS status by checking the columns of the DNS check model
func getCachedDNSStatus(dnsCheck *model.DNSCheck) (*sharedserialize.DNSStatus, error) {
	var cnameReqs generate.CNAMERequirements
	if err := json.Unmarshal(dnsCheck.CnameRequirements, &cnameReqs); err != nil {
		return nil, err
	}
	var result generate.CNAMEResult
	if err := json.Unmarshal(dnsCheck.LastResult, &result); err != nil {
		return nil, err
	}
	var failureHints generate.CNAMEFailureHints
	if err := json.Unmarshal(dnsCheck.LastFailureHints, &failureHints); err != nil {
		return nil, err
	}

	respMap := make(map[string]*sharedserialize.CNAMEStatus)

	var allVerified = true
	currentStatus := constants.DNSNotStarted

	for host := range cnameReqs {
		subdomain := cnameReqs[host].ClerkSubdomain
		isRequired := !cnameReqs[host].Optional

		hints := make([]sharedserialize.FailureHint, 0)
		if fh, ok := failureHints[host]; ok {
			for _, h := range fh {
				hints = append(hints, sharedserialize.FailureHint{
					Code:    h.Code,
					Message: h.Message,
				})
			}
		}
		respMap[subdomain] = &sharedserialize.CNAMEStatus{
			From:           host,
			To:             cnameReqs[host].Target,
			ClerkSubdomain: subdomain,
			Verified:       result[host],
			Required:       isRequired,
			FailureHints:   hints,
		}

		if isRequired && !result[host] {
			allVerified = false
		}
	}

	if allVerified {
		currentStatus = constants.DNSComplete
	} else if dnsCheck.JobInflight {
		currentStatus = constants.DNSInProgress
	}

	return &sharedserialize.DNSStatus{
		Status: currentStatus,
		CNAMES: respMap,
	}, nil
}

// Executes realtime DNS queries in order to get the domain's DNS status
// To be used on development instances. If you want the DNS status of
// production instances use getCachedDNSStatus instead.
func getRealtimeDNSStatus(ctx context.Context, domain *model.Domain, instance *model.Instance, checker dnschecks.CNAMEChecker) (*sharedserialize.DNSStatus, error) {
	domainCNAMEReqs := generate.DomainCNAMERequirements(instance, domain)
	validationResults, _, err := checker.CheckAll(ctx, domainCNAMEReqs)
	if err != nil {
		return nil, err
	}

	respMap := make(map[string]*sharedserialize.CNAMEStatus)

	var allVerified = true
	currentStatus := constants.DNSNotStarted

	for record, isVerified := range validationResults {
		req := domainCNAMEReqs[record]
		isRequired := !req.Optional
		label, ok := model.CNAMEFromSubdomain(record, domain)
		if !ok {
			return nil, fmt.Errorf("unknown subdomain: %s", record)
		}

		statusObj := sharedserialize.CNAMEStatus{
			ClerkSubdomain: req.ClerkSubdomain,
			From:           label.FqdnSource(domain),
			To:             req.Target,
			Verified:       isVerified,
			Required:       isRequired,
		}

		respMap[req.ClerkSubdomain] = &statusObj

		if isRequired && !statusObj.Verified {
			allVerified = false
		}
	}

	if allVerified {
		currentStatus = constants.DNSComplete
	}

	return &sharedserialize.DNSStatus{
		Status: currentStatus,
		CNAMES: respMap,
	}, nil
}

// Pings each of the domain's certificate hosts on port 443 to
// verify that the SSL certificate is valid.
func getSSLStatus(domain *model.Domain, instance *model.Instance, dnsStatus *sharedserialize.DNSStatus) *sharedserialize.SSLStatusResponse {
	if instance.IsDevelopment() {
		return sharedserialize.SSLStatus(constants.SSLComplete, true)
	}
	if !domain.DeploymentStarted() || !domain.NeedsSSLSetup() {
		return sharedserialize.SSLStatus(constants.SSLNotStarted, domain.NeedsSSLSetup())
	}

	// Check if the DNS validation is incomplete for a host requiring SSL.
	// This can occur when a customer enables the Account Portal, in which case
	// the accounts.* subdomain becomes required but is not yet verified.
	for _, status := range dnsStatus.CNAMES {
		if !status.Required || status.Verified {
			continue
		}
		for _, host := range domain.DefaultCertificateHosts() {
			if status.From == host {
				return sharedserialize.SSLStatus(constants.SSLNotStarted, domain.NeedsSSLSetup())
			}
		}
	}

	for _, host := range domain.DefaultCertificateHosts() {
		// For domains that have Cloudflare's CustomHostname status stored we can first check that
		// to determine the SSL status. If the certificate issuance for the hostname is not complete
		// we can return that status with any errors.
		cloudflareStatus := getCloudflareSSLStatus(domain, host)
		if cloudflareStatus != nil && cloudflareStatus.Status != constants.SSLComplete {
			return cloudflareStatus
		}

		// After we check that the ssl certificate has been issued attempt to connect to the host
		// on port 443 to verify that the setup is complete.
		_, err := tls.DialWithDialer(
			&net.Dialer{Timeout: 3 * time.Second},
			"tcp",
			fmt.Sprintf("%s:443", host),
			nil,
		)
		if err != nil {
			hint := sharedserialize.FailureHint{
				Code:    constants.SSLCannotEstablishConnectionHint,
				Message: fmt.Sprintf("Attempting to establish a tls connection to %s.", host),
			}
			return sharedserialize.SSLStatus(constants.SSLInProgress, domain.NeedsSSLSetup(), hint)
		}
	}

	return sharedserialize.SSLStatus(constants.SSLComplete, domain.NeedsSSLSetup())
}

func getCloudflareSSLStatus(domain *model.Domain, host string) *sharedserialize.SSLStatusResponse {
	hostnameStatus := domain.CloudflareHostnameStatus(host)
	if hostnameStatus == nil {
		return nil
	}

	// If the hostname is not active or pending, the SSL status should be considered as failed.
	// This can only happen when the CNAME is not pointing to our servers so Cloudflare marks it as moved and then later as deleted.
	// https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/reference/troubleshooting/#custom-hostname-in-moved-status
	if hostnameStatus.Status != constants.CloudflareHostnameActive && hostnameStatus.Status != constants.CloudflareHostnamePending {
		hint := sharedserialize.FailureHint{
			Code:    constants.CloudflareSSLHostnameMovedHint,
			Message: "The hostname is no longer pointing to our servers, please check your CNAME records.",
		}

		return sharedserialize.SSLStatus(constants.SSLFailed, domain.NeedsSSLSetup(), hint)
	}

	if hostnameStatus.SSLStatus == constants.CloudflareSSLActive {
		return sharedserialize.SSLStatus(constants.SSLComplete, domain.NeedsSSLSetup())
	}

	errors := make([]sharedserialize.FailureHint, 0)
	// Cloudflare does not provide a specific error code or a list of errors that can happen
	// during SSL provisioning. These are some common errors that we have encoureted.
	for _, errorMessage := range hostnameStatus.Errors {
		errorMessage = strings.ToLower(errorMessage)
		if strings.Contains(errorMessage, "caa records block issuance") {
			hint := sharedserialize.FailureHint{
				Code:    constants.CloudflareSSLCAABlockedHint,
				Message: "A CAA record blocks issuance of the SSL certificate, please add 'letsencrypt.org' or 'pki.goog' to the CAA records or remove them entirely.",
			}
			errors = append(errors, hint)
		} else if strings.Contains(errorMessage, "the authority has rate limited these domains") {
			hint := sharedserialize.FailureHint{
				Code:    constants.CloudflareSSLRateLimitedHint,
				Message: "The Certificate Authority has rate limited these domains, the certificate issuance will be retried when the timeout expires.",
			}
			errors = append(errors, hint)
		} else if strings.Contains(errorMessage, "the certificate authority had trouble performing a dns lookup") {
			// Transient cloudflare error, should go away on its own after a while.
			hint := sharedserialize.FailureHint{
				Code:    constants.CloudflareSSLCAAFetchErrorHint,
				Message: "The Certificate Authority had trouble retrieving CAA records for this domain. Please wait for the operation to be retried.",
			}
			errors = append(errors, hint)
		} else {
			hint := sharedserialize.FailureHint{
				Code:    constants.CloudflareSSLUnknownErrorHint,
				Message: "An error occurred while provisioning the SSL certificate, please wait for the operation to be retried or contact suppport.",
			}
			errors = append(errors, hint)
		}
	}

	if constants.CloudflarePendingSSLStatuses.Contains(hostnameStatus.SSLStatus) {
		return sharedserialize.SSLStatus(constants.SSLInProgress, domain.NeedsSSLSetup(), errors...)
	}

	// If the SSL status is not pending, it means that the SSL status is failed.
	// When this happens Cloudflare will no longer validate the SSL certificate
	// and it needs to be manually retried.
	//
	// There are two reasons this can happen :
	// 1. Validation timed out due to an error.
	// 2. The CNAMES are not pointing to our servers and cloudflare has not updated the hostname status yet.
	return sharedserialize.SSLStatus(constants.SSLFailed, domain.NeedsSSLSetup(), errors...)
}

func getMailStatus(domain *model.Domain, instance *model.Instance) *sharedserialize.CheckStatusResponse {
	if !instance.IsProduction() || !domain.NeedsEmailSetup(instance) {
		return nil
	}

	status := constants.MAILNotStarted
	if domain.SendgridDomainVerified {
		status = constants.MAILComplete
	} else if domain.SendgridDomainVerifiedResponse.Valid {
		status = constants.MAILFailed
	} else if domain.SendgridJobInflight {
		status = constants.MAILInProgress
	}
	return sharedserialize.MailStatus(status)
}

func GetProxyStatus(
	domain *model.Domain,
	proxyCheck *model.ProxyCheck,
) *sharedserialize.ProxyStatusResponse {
	required := domain.ProxyURL.Valid
	if proxyCheck == nil {
		return sharedserialize.ProxyStatus(constants.ProxyNotConfigured, required)
	}
	if !proxyCheck.Successful {
		return sharedserialize.ProxyStatus(constants.ProxyFailed, required)
	}
	return sharedserialize.ProxyStatus(constants.ProxyComplete, required)
}
