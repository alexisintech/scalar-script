package certs

import (
	"context"

	"clerk/pkg/ctx/requestdomain"
	"clerk/utils/database"
)

type Service struct {
	db database.Database
}

func NewService(db database.Database) *Service {
	return &Service{
		db: db,
	}
}

// Health makes sure DNS is propagating
func (s *Service) CertificateHostExists(ctx context.Context, host string) bool {
	domain := requestdomain.FromContext(ctx)

	for _, certificateHost := range domain.DefaultCertificateHosts() {
		if host == certificateHost {
			return true
		}
	}

	return false
}
