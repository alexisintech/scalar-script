package serialize

import (
	"clerk/pkg/constants"
)

type DomainStatusResponse struct {
	DNS    *DNSStatus           `json:"dns"`
	SSL    *SSLStatusResponse   `json:"ssl"`
	Mail   *CheckStatusResponse `json:"mail,omitempty"`
	Proxy  *ProxyStatusResponse `json:"proxy,omitempty"`
	Status string               `json:"status"`
}

type CheckStatusResponse struct {
	Status   string `json:"status"`
	Required bool   `json:"required"`
}

type SSLStatusResponse struct {
	Status       string        `json:"status"`
	Required     bool          `json:"required"`
	FailureHints []FailureHint `json:"failure_hints"`
}

func SSLStatus(status string, required bool, failureHints ...FailureHint) *SSLStatusResponse {
	return &SSLStatusResponse{
		Status:       status,
		Required:     required,
		FailureHints: failureHints,
	}
}

func MailStatus(status string) *CheckStatusResponse {
	return &CheckStatusResponse{
		Status:   status,
		Required: true, // mail is always required if present
	}
}

type DNSStatus struct {
	Status string                  `json:"status"`
	CNAMES map[string]*CNAMEStatus `json:"cnames"`
}

type CNAMEStatus struct {
	ClerkSubdomain string        `json:"clerk_subdomain"`
	From           string        `json:"from"`
	To             string        `json:"to"`
	Verified       bool          `json:"verified"`
	Required       bool          `json:"required"`
	FailureHints   []FailureHint `json:"failure_hints"`
}

type FailureHint struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ProxyStatusResponse struct {
	Status   string `json:"status"`
	Required bool   `json:"required"`
}

func ProxyStatus(status string, required bool) *ProxyStatusResponse {
	return &ProxyStatusResponse{
		Status:   status,
		Required: required,
	}
}

func DomainStatus(
	dnsStatus *DNSStatus,
	sslStatus *SSLStatusResponse,
	mailStatus *CheckStatusResponse,
	proxyStatus *ProxyStatusResponse,
) *DomainStatusResponse {
	return &DomainStatusResponse{
		DNS:    dnsStatus,
		SSL:    sslStatus,
		Mail:   mailStatus,
		Proxy:  proxyStatus,
		Status: determineOverallDomainStatus(dnsStatus, sslStatus, mailStatus, proxyStatus),
	}
}

func determineOverallDomainStatus(
	dnsStatus *DNSStatus,
	sslStatus *SSLStatusResponse,
	mailStatus *CheckStatusResponse,
	proxyStatus *ProxyStatusResponse) string {
	if dnsStatus != nil && dnsStatus.Status != constants.DNSComplete {
		return constants.DomainIncomplete
	} else if sslStatus != nil && sslStatus.Required && sslStatus.Status != constants.SSLComplete {
		return constants.DomainIncomplete
	} else if mailStatus != nil && mailStatus.Required && mailStatus.Status != constants.MAILComplete {
		return constants.DomainIncomplete
	} else if proxyStatus != nil && proxyStatus.Required && proxyStatus.Status != constants.ProxyComplete {
		return constants.DomainIncomplete
	}
	return constants.DomainComplete
}
