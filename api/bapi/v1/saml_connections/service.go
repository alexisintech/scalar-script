package saml_connections

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/pagination"
	"clerk/api/shared/saml"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/psl"
	pkgsaml "clerk/pkg/saml"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

const (
	maximumConnectionsDevInstances = 25

	pemHeader = "-----BEGIN CERTIFICATE-----"
	pemFooter = "-----END CERTIFICATE-----"
)

type Service struct {
	db        database.Database
	gueClient *gue.Client
	validator *validator.Validate

	eventService *events.Service
	samlService  *saml.SAML

	authConfigRepo     *repository.AuthConfig
	samlConnectionRepo *repository.SAMLConnection
	userRepo           *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                 deps.DB(),
		gueClient:          deps.GueClient(),
		validator:          validator.New(),
		eventService:       events.NewService(deps),
		samlService:        saml.New(),
		authConfigRepo:     repository.NewAuthConfig(),
		samlConnectionRepo: repository.NewSAMLConnection(),
		userRepo:           repository.NewUsers(),
	}
}

type CreateParams struct {
	Name             string                        `json:"name" form:"name" validate:"required"`
	Domain           string                        `json:"domain" form:"domain" validate:"required,fqdn,endsnotwith=."`
	Provider         string                        `json:"provider" form:"provider" validate:"required"`
	AttributeMapping *model.AttributeMappingParams `json:"attribute_mapping" form:"attribute_mapping"`
	IdpConfigurationParams
}

type IdpConfigurationParams struct {
	IdpEntityID    *string `json:"idp_entity_id" form:"idp_entity_id"`
	IdpSsoURL      *string `json:"idp_sso_url" form:"idp_sso_url"`
	IdpCertificate *string `json:"idp_certificate" form:"idp_certificate"`
	IdpMetadataURL *string `json:"idp_metadata_url" form:"idp_metadata_url"`
	IdpMetadata    *string `json:"idp_metadata" form:"idp_metadata"`
}

func (params *CreateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}

	if !pkgsaml.ProviderExists(params.Provider) {
		return apierror.FormInvalidParameterValueWithAllowed("provider", params.Provider, pkgsaml.ProviderIDs())
	}

	return params.IdpConfigurationParams.validate()
}

func (params *CreateParams) sanitize() {
	// Normalize domain name before persisting it in order to allow consistent comparisons with email addresses.
	params.Domain = strings.ToLower(params.Domain)

	if params.IdpCertificate != nil {
		sanitizedIdpCert := sanitizeCertificate(*params.IdpCertificate)
		params.IdpCertificate = &sanitizedIdpCert
	}
}

func (params IdpConfigurationParams) validate() apierror.Error {
	if params.IdpEntityID != nil && *params.IdpEntityID == "" {
		return apierror.FormMissingParameter("idp_entity_id")
	}

	if params.IdpSsoURL != nil {
		if _, err := url.ParseRequestURI(*params.IdpSsoURL); err != nil {
			return apierror.FormInvalidParameterFormat("idp_sso_url", "Must be a valid url")
		}
	}

	if params.IdpCertificate != nil {
		if apiErr := validateCertificate(*params.IdpCertificate); apiErr != nil {
			return apiErr
		}
	}

	if params.IdpMetadataURL != nil {
		if _, err := url.ParseRequestURI(*params.IdpMetadataURL); err != nil {
			return apierror.FormInvalidParameterFormat("idp_metadata_url", "Must be a valid url")
		}
	}

	return nil
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.SAMLConnectionResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if env.Instance.IsDevelopment() {
		totalConnections, err := s.samlConnectionRepo.CountByInstance(ctx, s.db, env.Instance.ID, repository.SAMLConnectionFindAllModifiers{})
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		if totalConnections == maximumConnectionsDevInstances {
			return nil, apierror.QuotaExceeded()
		}
	}

	if !env.AuthConfig.UserSettings.SignUp.Progressive {
		return nil, apierror.FeatureRequiresPSU("Enterprise Connections")
	}

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	if apiErr := s.processIDPConfiguration(ctx, &params.IdpConfigurationParams); apiErr != nil {
		return nil, apiErr
	}

	params.sanitize()

	samlProvider, err := pkgsaml.GetProvider(params.Provider)
	if err != nil {
		return nil, apierror.FormInvalidParameterValueWithAllowed("provider", params.Provider, pkgsaml.ProviderIDs())
	}

	attributeMapping := samlProvider.DefaultAttributeMapping()
	if params.AttributeMapping != nil {
		attributeMapping = params.AttributeMapping.ToModel()
	}

	samlConnection := &model.SAMLConnection{SamlConnection: &sqbmodel.SamlConnection{
		InstanceID:         env.Instance.ID,
		Name:               params.Name,
		Domain:             params.Domain,
		Provider:           params.Provider,
		IdpEntityID:        null.StringFromPtr(params.IdpEntityID),
		IdpSsoURL:          null.StringFromPtr(params.IdpSsoURL),
		IdpCertificate:     null.StringFromPtr(params.IdpCertificate),
		IdpMetadataURL:     null.StringFromPtr(params.IdpMetadataURL),
		IdpMetadata:        null.StringFromPtr(params.IdpMetadata),
		AttributeMapping:   attributeMapping,
		AllowIdpInitiated:  false,
		IdpEmailsVerified:  true,
		Active:             false,
		SyncUserAttributes: true,
		AllowSubdomains:    false,
	}}

	err = s.samlConnectionRepo.Insert(ctx, s.db, samlConnection)
	if err != nil {
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueSAMLConnectionName) {
			return nil, apierror.FormIdentifierExists("name")
		} else if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueSAMLConnectionDomain) {
			return nil, apierror.FormIdentifierExists("domain")
		}
		return nil, apierror.Unexpected(err)
	}

	return serialize.SAMLConnection(samlConnection, env.Domain, 0), nil
}

type UpdateParams struct {
	Name               *string                       `json:"name" form:"name"`
	Domain             *string                       `json:"domain" form:"domain" validate:"omitempty,required,fqdn,endsnotwith=."`
	AttributeMapping   *model.AttributeMappingParams `json:"attribute_mapping" form:"attribute_mapping"`
	Active             *bool                         `json:"active" form:"active"`
	SyncUserAttributes *bool                         `json:"sync_user_attributes" form:"sync_user_attributes"`
	AllowSubdomains    *bool                         `json:"allow_subdomains" form:"allow_subdomains"`
	AllowIdpInitiated  *bool                         `json:"allow_idp_initiated" form:"allow_idp_initiated"`
	IdpConfigurationParams
}

func (params *UpdateParams) validate(validator *validator.Validate, connectionDomain string) apierror.Error {
	if err := validator.Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}

	if params.Name != nil && *params.Name == "" {
		return apierror.FormMissingParameter("name")
	}

	if params.AllowSubdomains != nil && *params.AllowSubdomains {
		// In order to enable the setting, the connection's domain must be eTLD+1
		if !psl.IsETLDPlusOne(connectionDomain) {
			return apierror.FormParameterNotAllowedConditionally("allow_subdomains", "connection domain", "not eTLD+1")
		}
	}

	return params.IdpConfigurationParams.validate()
}

func (params *UpdateParams) sanitize() {
	if params.Domain != nil {
		sanitizedDomain := strings.ToLower(*params.Domain)
		params.Domain = &sanitizedDomain
	}

	if params.IdpCertificate != nil {
		sanitizedIdpCert := sanitizeCertificate(*params.IdpCertificate)
		params.IdpCertificate = &sanitizedIdpCert
	}
}

func (s *Service) Update(ctx context.Context, samlConnectionID string, params UpdateParams) (*serialize.SAMLConnectionResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	samlConnection, err := s.samlConnectionRepo.QueryByIDAndInstanceID(ctx, s.db, samlConnectionID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if samlConnection == nil {
		return nil, apierror.ResourceNotFound()
	}

	if err := params.validate(s.validator, samlConnection.Domain); err != nil {
		return nil, err
	}

	if apiErr := s.processIDPConfiguration(ctx, &params.IdpConfigurationParams); apiErr != nil {
		return nil, apiErr
	}

	params.sanitize()

	activeBefore := samlConnection.Active

	columnsToUpdate := updateAndFindColumns(samlConnection, params)

	if apiErr := connectionCanBeActivated(samlConnection); apiErr != nil {
		return nil, apiErr
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		err = s.samlConnectionRepo.Update(ctx, txEmitter, samlConnection, columnsToUpdate...)
		if err != nil {
			if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueSAMLConnectionName) {
				return true, apierror.FormIdentifierExists("name")
			} else if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueSAMLConnectionDomain) {
				return true, apierror.FormIdentifierExists("domain")
			}
			return true, apierror.Unexpected(err)
		}

		// Create an event only if user made the connection active
		if !activeBefore && samlConnection.Active {
			err = s.eventService.SAMLConnectionActivated(ctx, txEmitter, env.Instance, samlConnection.ID)
			if err != nil {
				return true, fmt.Errorf("send saml connection activated event for %s: %w", samlConnection.ID, err)
			}
		}

		if samlConnection.Active != activeBefore {
			err = s.updateUserSettings(ctx, txEmitter, env.AuthConfig)
			if err != nil {
				return true, apierror.Unexpected(err)
			}
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	userCount, err := s.userRepo.CountSAMLByInstanceAndSAMLConnectionID(ctx, s.db, env.Instance.ID, samlConnection.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.SAMLConnection(samlConnection, env.Domain, userCount), nil
}

func updateAndFindColumns(samlConnection *model.SAMLConnection, params UpdateParams) []string {
	columnsToUpdate := make([]string, 0)

	if params.Name != nil {
		samlConnection.Name = *params.Name
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.Name)
	}
	if params.Domain != nil {
		samlConnection.Domain = *params.Domain
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.Domain)
	}
	if params.IdpEntityID != nil {
		samlConnection.IdpEntityID = null.StringFromPtr(params.IdpEntityID)
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.IdpEntityID)
	}
	if params.IdpSsoURL != nil {
		samlConnection.IdpSsoURL = null.StringFromPtr(params.IdpSsoURL)
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.IdpSsoURL)
	}
	if params.IdpCertificate != nil {
		samlConnection.IdpCertificate = null.StringFromPtr(params.IdpCertificate)
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.IdpCertificate)
	}
	if params.IdpMetadataURL != nil {
		samlConnection.IdpMetadataURL = null.StringFromPtr(params.IdpMetadataURL)
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.IdpMetadataURL)
	}
	if params.IdpMetadata != nil {
		samlConnection.IdpMetadata = null.StringFromPtr(params.IdpMetadata)
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.IdpMetadata)
	}
	if params.AttributeMapping != nil {
		samlConnection.AttributeMapping = params.AttributeMapping.ToModel()
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.AttributeMapping)
	}
	if params.Active != nil {
		samlConnection.Active = *params.Active
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.Active)
	}
	if params.SyncUserAttributes != nil {
		samlConnection.SyncUserAttributes = *params.SyncUserAttributes
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.SyncUserAttributes)
	}
	if params.AllowSubdomains != nil {
		samlConnection.AllowSubdomains = *params.AllowSubdomains
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.AllowSubdomains)
	}
	if params.AllowIdpInitiated != nil {
		samlConnection.AllowIdpInitiated = *params.AllowIdpInitiated
		columnsToUpdate = append(columnsToUpdate, sqbmodel.SamlConnectionColumns.AllowIdpInitiated)
	}

	return columnsToUpdate
}

func (s *Service) Read(ctx context.Context, samlConnectionID string) (*serialize.SAMLConnectionResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	samlConnection, err := s.samlConnectionRepo.QueryByIDAndInstanceID(ctx, s.db, samlConnectionID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if samlConnection == nil {
		return nil, apierror.ResourceNotFound()
	}

	userCount, err := s.userRepo.CountSAMLByInstanceAndSAMLConnectionID(ctx, s.db, env.Instance.ID, samlConnection.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.SAMLConnection(samlConnection, env.Domain, userCount), nil
}

type ListParams struct {
	pagination pagination.Params
	query      *string
	orderBy    *string
}

func (p ListParams) toMods() (repository.SAMLConnectionFindAllModifiers, apierror.Error) {
	validSAMLConnectionOrderByFields := set.New(
		sqbmodel.SamlConnectionColumns.CreatedAt,
		sqbmodel.SamlConnectionColumns.Name,
	)

	mods := repository.SAMLConnectionFindAllModifiers{
		Query: p.query,
	}

	if p.orderBy != nil {
		orderByField, err := repository.ConvertToOrderByField(*p.orderBy, validSAMLConnectionOrderByFields)
		if err != nil {
			return mods, err
		}

		mods.OrderBy = &orderByField
	}

	return mods, nil
}

func (s *Service) List(ctx context.Context, params ListParams) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	mods, apiErr := params.toMods()
	if apiErr != nil {
		return nil, apiErr
	}

	samlConnections, err := s.samlConnectionRepo.FindAllByInstanceWithUserCount(ctx, s.db, env.Instance.ID, mods, params.pagination)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalCount, err := s.samlConnectionRepo.CountByInstance(ctx, s.db, env.Instance.ID, mods)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]interface{}, len(samlConnections))
	for i, samlConnection := range samlConnections {
		responses[i] = serialize.SAMLConnection(samlConnection.SAMLConnection, env.Domain, samlConnection.UserCount)
	}

	return serialize.Paginated(responses, totalCount), nil
}

func (s *Service) Delete(ctx context.Context, samlConnectionID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	samlConnection, err := s.samlConnectionRepo.QueryByIDAndInstanceID(ctx, s.db, samlConnectionID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if samlConnection == nil {
		return nil, apierror.ResourceNotFound()
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		_, err := s.samlConnectionRepo.DeleteByIDAndInstance(ctx, txEmitter, samlConnectionID, env.Instance.ID)
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		if samlConnection.Active {
			err = s.updateUserSettings(ctx, txEmitter, env.AuthConfig)
			if err != nil {
				return true, apierror.Unexpected(err)
			}
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.DeletedObject(samlConnectionID, serialize.SAMLConnectionObjectName), nil
}

// Update user settings when the active SAML connections change
func (s *Service) updateUserSettings(ctx context.Context, txEmitter database.TxEmitter, authConfig *model.AuthConfig) error {
	count, err := s.samlConnectionRepo.CountActiveByInstance(ctx, txEmitter, authConfig.InstanceID)
	if err != nil {
		return err
	}

	authConfig.UserSettings.SAML.Enabled = count > 0

	return s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, authConfig)
}

func (s *Service) processIDPConfiguration(ctx context.Context, params *IdpConfigurationParams) apierror.Error {
	var idpMetadata *saml.IDPMetadata
	if params.IdpMetadata != nil {
		var err error
		idpMetadata, err = s.samlService.ParseMetadataForIDP(*params.IdpMetadata)
		if err != nil {
			return apierror.SAMLFailedToParseIDPMetadata()
		}
	} else if params.IdpMetadataURL != nil {
		var err error
		idpMetadata, err = s.samlService.FetchMetadataForIDP(ctx, *params.IdpMetadataURL)
		if err != nil {
			return apierror.SAMLFailedToFetchIDPMetadata()
		}
	}

	// IdP Metadata retrieved from URL or file, take priority over the corresponding IdP related properties
	if idpMetadata != nil {
		params.IdpEntityID = &idpMetadata.EntityID
		if idpMetadata.SSOURL != nil {
			params.IdpSsoURL = idpMetadata.SSOURL
		}
		if idpMetadata.Certificate != nil {
			params.IdpCertificate = idpMetadata.Certificate
		}
	}

	return nil
}

// We have to make sure all the required IdP data has been provided, before activating a SAML Connection
func connectionCanBeActivated(samlConnection *model.SAMLConnection) apierror.Error {
	if !samlConnection.Active {
		return nil
	}

	missingFields := make([]string, 0)

	if !samlConnection.IdpEntityID.Valid {
		missingFields = append(missingFields, "IdP Entity ID")
	}
	if !samlConnection.IdpSsoURL.Valid {
		missingFields = append(missingFields, "IdP SSO URL")
	}
	if !samlConnection.IdpCertificate.Valid {
		missingFields = append(missingFields, "IdP Certificate")
	}

	if len(missingFields) > 0 {
		return apierror.SAMLConnectionCantBeActivated(missingFields)
	}

	return nil
}

// convert certificate to PEM format and validate it. SAML responses contain
// the certificate in base64-encoded form, but without the PEM
// header/footer, so we have to add them manually.
func validateCertificate(cert string) apierror.Error {
	if !strings.HasPrefix(cert, pemHeader) {
		cert = pemHeader + "\n" + cert
	}
	if !strings.HasSuffix(cert, pemFooter) {
		cert = cert + "\n" + pemFooter
	}

	pemblock, _ := pem.Decode([]byte(cert))
	if pemblock == nil {
		return apierror.FormInvalidParameterFormat("idp_certificate", "malformed X.509 certificate")
	}

	_, err := x509.ParseCertificate(pemblock.Bytes)
	if err != nil {
		return apierror.FormInvalidParameterFormat("idp_certificate", "malformed X.509 certificate")
	}

	return nil
}

// Remove the PEM header & footer and any new lines from the IdP certificate
func sanitizeCertificate(cert string) string {
	cert = strings.TrimSpace(cert)
	cert = strings.ReplaceAll(cert, "\r", "")
	cert = strings.ReplaceAll(cert, "\n", "")
	cert = strings.TrimPrefix(cert, pemHeader)
	cert = strings.TrimSuffix(cert, pemFooter)
	return cert
}
