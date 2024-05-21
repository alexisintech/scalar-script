package serialize

import (
	"clerk/api/shared/emailquality"
	"clerk/model"
	"clerk/pkg/time"
)

type EmailQualityResponse struct {
	Domain        string `json:"domain"`
	Disposable    bool   `json:"disposable"`
	SourcedVia    string `json:"sourced_via"`
	LastCheckedAt int64  `json:"last_checked_at"`
}

func EmailDomain(emailDomainReport *model.EmailDomainReport) *EmailQualityResponse {
	return &EmailQualityResponse{
		Domain:        emailDomainReport.Domain,
		Disposable:    emailDomainReport.Disposable,
		SourcedVia:    emailDomainReport.SourcedVia,
		LastCheckedAt: time.UnixMilli(emailDomainReport.LastCheckedAt),
	}
}

func EmailQuality(qualityResult *emailquality.QualityResult) *EmailQualityResponse {
	return &EmailQualityResponse{
		Domain:        qualityResult.Domain,
		Disposable:    qualityResult.Disposable,
		SourcedVia:    qualityResult.SourcedVia,
		LastCheckedAt: time.UnixMilli(qualityResult.LastCheckedAt),
	}
}
