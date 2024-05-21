// package saml provides helper functions for fetching instance-specific
// configuration data required during SAML authentication flows.
package saml

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"clerk/model"
	"clerk/pkg/emailaddress"
	"clerk/pkg/psl"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
)

var (
	ErrConnectionNotFound = errors.New("saml_connection not found")
	ErrInvalidIdentifier  = errors.New("invalid identifier for saml_connection")
)

var (
	defaultHTTPClient = &http.Client{Timeout: time.Second * 3}
)

type IDPMetadata struct {
	EntityID    string
	SSOURL      *string
	Certificate *string
}

type SAML struct {
	samlConnectionRepo *repository.SAMLConnection
}

func New() *SAML {
	return &SAML{
		samlConnectionRepo: repository.NewSAMLConnection(),
	}
}

func (s *SAML) MetadataForConnection(ctx context.Context, exec database.Executor, env *model.Env, connectionID string) ([]byte, error) {
	conn, err := s.samlConnectionRepo.QueryByIDAndInstanceID(ctx, exec, connectionID, env.Instance.ID)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, ErrConnectionNotFound
	}

	sp, err := conn.ServiceProvider(env.Domain)
	if err != nil {
		return nil, err
	}

	metadata := sp.Metadata()
	// Our ACS only supports HTTP-POST response binding and not the HTTP-Artifact
	metadata.SPSSODescriptors[0].AssertionConsumerServices = []saml.IndexedEndpoint{
		{
			Binding:  saml.HTTPPostBinding,
			Location: sp.AcsURL.String(),
			Index:    1,
		},
	}

	return xml.MarshalIndent(metadata, "", "  ")
}

func (s *SAML) ServiceProviderForActiveConnection(ctx context.Context, exec database.Executor, env *model.Env, connectionID string) (*saml.ServiceProvider, *model.SAMLConnection, error) {
	conn, err := s.samlConnectionRepo.QueryActiveByIDAndInstanceID(ctx, exec, connectionID, env.Instance.ID)
	if err != nil {
		return nil, nil, err
	}
	if conn == nil {
		return nil, nil, ErrConnectionNotFound
	}

	sp, err := conn.ServiceProvider(env.Domain)
	if err != nil {
		return nil, nil, err
	}

	return sp, conn, nil
}

func (s *SAML) ActiveConnectionForEmail(ctx context.Context, exec database.Executor, instanceID, email string) (*model.SAMLConnection, error) {
	domain := samlDomain(email)
	if domain == "" {
		return nil, ErrInvalidIdentifier
	}

	// Try to find an active connection with the exact email address domain
	connection, err := s.samlConnectionRepo.QueryActiveByInstanceIDAndDomain(ctx, exec, instanceID, domain)
	if err != nil {
		return nil, err
	}
	if connection != nil {
		return connection, nil
	}

	// If not found, get the eTLD+1 email address domain and try to find an active connection that allows subdomains
	eTLDPlusOne, err := psl.Domain(domain)
	if err != nil {
		if errors.Is(err, psl.ErrDomainIsSuffix) {
			// domain is a public suffix like `co.uk`
			return nil, nil
		}
		return nil, err
	}

	return s.samlConnectionRepo.QueryActiveByInstanceAndDomainAndAllowSubdomains(ctx, exec, instanceID, eTLDPlusOne)
}

func (s *SAML) FetchMetadataForIDP(ctx context.Context, metadataRawURL string) (*IDPMetadata, error) {
	metadataURL, err := url.ParseRequestURI(metadataRawURL)
	if err != nil {
		return nil, err
	}

	data, err := samlsp.FetchMetadata(ctx, defaultHTTPClient, *metadataURL)
	if err != nil {
		return nil, err
	}

	return populateIDPMetadata(data), nil
}

func (s *SAML) ParseMetadataForIDP(metadataRaw string) (*IDPMetadata, error) {
	data, err := samlsp.ParseMetadata([]byte(metadataRaw))
	if err != nil {
		return nil, err
	}

	return populateIDPMetadata(data), nil
}

func populateIDPMetadata(data *saml.EntityDescriptor) *IDPMetadata {
	metadata := &IDPMetadata{
		EntityID: data.EntityID,
	}

	if len(data.IDPSSODescriptors) > 0 {
		for i, ssoService := range data.IDPSSODescriptors[0].SingleSignOnServices {
			if ssoService.Binding == saml.HTTPRedirectBinding {
				metadata.SSOURL = &data.IDPSSODescriptors[0].SingleSignOnServices[i].Location
			}
		}
	}

	if len(data.IDPSSODescriptors) > 0 && len(data.IDPSSODescriptors[0].KeyDescriptors) > 0 &&
		len(data.IDPSSODescriptors[0].KeyDescriptors[0].KeyInfo.X509Data.X509Certificates) > 0 {
		metadata.Certificate = &data.IDPSSODescriptors[0].KeyDescriptors[0].KeyInfo.X509Data.X509Certificates[0].Data
	}

	return metadata
}

// samlDomain returns the domain of the email address normalized for comparison with SAML domains.
// Since connection domains are normalized before being persisted in order to ensure uniqueness,
// we convert the email address input to all-lowercase as well.
func samlDomain(emailAddress string) string {
	return strings.ToLower(emailaddress.Domain(emailAddress))
}
