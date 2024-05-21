package integrations

import (
	"context"
	"encoding/json"
	"errors"

	"clerk/api/apierror"
	"clerk/api/dapi/v1/clients"
	"clerk/api/serialize"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/validator"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/params"
	clerksdk "clerk/pkg/sdk"
	"clerk/pkg/vercel"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	deps clerk.Deps

	// Repositories
	appRepo            *repository.Applications
	appIntegrationRepo *repository.ApplicationIntegrations
	appOwnershipRepo   *repository.ApplicationOwnerships
	integrationRepo    *repository.Integrations
	clientService      *clients.Service

	// Clients
	vercelClient *vercel.Client
}

func NewService(deps clerk.Deps, vercelClient *vercel.Client, jwksClient *jwks.Client) *Service {
	return &Service{
		deps:               deps,
		appRepo:            repository.NewApplications(),
		appIntegrationRepo: repository.NewApplicationIntegrations(),
		appOwnershipRepo:   repository.NewApplicationOwnerships(),
		integrationRepo:    repository.NewIntegrations(),
		clientService:      clients.NewService(deps, jwksClient),
		vercelClient:       vercelClient,
	}
}

// CheckIntegrationOwner returns an error if the currently active user is not the owner of the integration stored in r.Context().
func (s *Service) CheckIntegrationOwner(ctx context.Context, integrationID string) apierror.Error {
	activeSession, sessionOk := clerksdk.GetActiveSession(ctx)
	client, clientOk := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	integration, err := s.fetchIntegration(ctx, integrationID)
	if err != nil {
		return err
	}

	// TODO If accessed via a different clientID we may be able to retrieve it via userID
	// But current use case doesn't require it

	var clientID string

	if clientOk {
		clientID = client.ID
	} else if sessionOk {
		session, apiErr := s.clientService.DashboardSessionFromClaims(ctx, activeSession)
		if apiErr != nil {
			return apiErr
		}
		clientID = session.ClientID
	}

	if integration.ClientID.String != clientID {
		return apierror.IntegrationNotFound(integration.ID)
	}

	return nil
}

// UpsertVercel gets or creates a Vercel integration
func (s *Service) UpsertVercel(ctx context.Context, vercelIntegrationParams *params.VercelIntegrationParams) (*serialize.IntegrationResponse, apierror.Error) {
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	// Get or create integration for given configurationID
	integration, err := s.vercelClient.GetOrCreateIntegration(ctx, s.deps.DB(), client, vercelIntegrationParams)
	if err != nil {
		if apiErr, isAPIErr := apierror.As(err); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(err)
	}

	// respond with integration
	return serialize.Integration(integration, false), nil
}

// ToggleGoogleAnalytics creates, updates or deletes a Google Analytics integration
func (s *Service) ToggleGoogleAnalytics(ctx context.Context, instanceID string, integrationParams *params.GoogleAnalyticsIntegrationParams) (*serialize.IntegrationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	// GA is only supported on production instances
	if !env.Instance.IsProduction() {
		return nil, apierror.InvalidRequestForEnvironment(string(constants.ETProduction))
	}

	validate := validator.FromContext(ctx)
	if err := validate.Struct(integrationParams); err != nil {
		return nil, apierror.FormValidationFailed(err)
	}

	integration, err := s.integrationRepo.QueryByInstanceIDAndType(ctx, s.deps.DB(), instanceID, model.GoogleAnalyticsIntegration)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Integration disabled, delete it
	if !integrationParams.Enabled && integration != nil {
		err = s.integrationRepo.DeleteByID(ctx, s.deps.DB(), integration.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		return nil, nil
	}

	// Initialize a new integration if it doesn't already exist
	if integration == nil {
		integration = &model.Integration{Integration: &sqbmodel.Integration{}}
		integration.Type = string(model.GoogleAnalyticsIntegration)
		integration.InstanceID = instanceID
	}

	gaIntegrationMetadata := &model.GoogleAnalyticsIntegrationMetadata{
		GoogleAnalyticsType: integrationParams.GoogleAnalyticsType,
		IncludeUserID:       integrationParams.IncludeUserID,
		Events:              integrationParams.Events,
	}

	switch integrationParams.GoogleAnalyticsType {
	case string(model.GoogleAnalyticsV4):
		gaIntegrationMetadata.APISecret = integrationParams.APISecret
		gaIntegrationMetadata.MeasurementID = integrationParams.MeasurementID
	case string(model.GoogleAnalyticsUniversal):
		gaIntegrationMetadata.TrackingID = integrationParams.TrackingID
	}

	jsonBytes, err := json.Marshal(gaIntegrationMetadata)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	integration.Metadata = jsonBytes

	if integration.ID == "" {
		err = s.integrationRepo.Insert(ctx, s.deps.DB(), integration)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	} else {
		err = s.integrationRepo.Update(ctx, s.deps.DB(), integration)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	obfuscateSecrets := clerksdk.ActorHasLimitedAccess(ctx)
	return serialize.Integration(integration, obfuscateSecrets), nil
}

// ReadVercel return a Vercel integration by its id
func (s *Service) ReadVercel(ctx context.Context, integrationID string) (*serialize.IntegrationResponse, apierror.Error) {
	integration, err := s.fetchIntegration(ctx, integrationID)
	if err != nil {
		return nil, err
	}

	return serialize.Integration(integration, false), nil
}

// ReadByType return an integration by its type
// Note: assumed to be only one per type, per integration
func (s *Service) ReadByType(ctx context.Context, instanceID string, integrationType model.IntegrationType) (*serialize.IntegrationResponse, apierror.Error) {
	integration, err := s.integrationRepo.FindByInstanceIDAndType(ctx, s.deps.DB(), instanceID, integrationType)
	if err != nil {
		return nil, apierror.IntegrationNotFoundByType(instanceID, string(integrationType))
	}

	obfuscateSecrets := clerksdk.ActorHasLimitedAccess(ctx)
	return serialize.Integration(integration, obfuscateSecrets), nil
}

// GetUserInfo returns user information for external integration
func (s *Service) GetUserInfo(ctx context.Context, integrationID string) (map[string]interface{}, apierror.Error) {
	integration, apierr := s.fetchIntegration(ctx, integrationID)
	if apierr != nil {
		return nil, apierr
	}

	// Check if there is an existing Clerk user for the Vercel email correlated with the current oauth token
	email, userExists, err := s.vercelClient.GetUserInfo(ctx, s.deps.DB(), integration)
	if err != nil {
		if errors.Is(err, clerkerrors.ErrIntegrationUserInfoRetrievalFailure) {
			return nil, apierror.IntegrationUserInfoError(integration.ID)
		} else if errors.Is(err, clerkerrors.ErrIntegrationTokenMissing) {
			return nil, apierror.IntegrationTokenMissing(integration.ID)
		}
		return nil, apierror.Unexpected(err)
	}

	log.Debug(ctx, "GetUserInfo - email:", *email)
	log.Debug(ctx, "GetUserInfo - userExists:", userExists)

	return map[string]interface{}{"email": email, "user_exists": userExists}, nil
}

// GetObjects retrieves business objects accessible by the integration token
// Only Vercel projects supported for now
func (s *Service) GetObjects(ctx context.Context, integrationID string) (interface{}, apierror.Error) {
	integration, apierr := s.fetchIntegration(ctx, integrationID)
	if apierr != nil {
		return nil, apierr
	}

	projects, err := s.vercelClient.GetProjectList(ctx, integration)
	if err != nil {
		if errors.Is(err, clerkerrors.ErrIntegrationTokenMissing) {
			return nil, apierror.IntegrationTokenMissing(integration.ID)
		}
		return nil, apierror.Unexpected(err)
	}

	return projects, nil
}

// GetObject retrieves a business object accessible by the integration token, given its id
// Only Vercel projects supported for now
func (s *Service) GetObject(ctx context.Context, integrationID, objectID string) (interface{}, apierror.Error) {
	integration, apierr := s.fetchIntegration(ctx, integrationID)
	if apierr != nil {
		return nil, apierr
	}

	project, err := s.vercelClient.GetProject(ctx, integration, objectID)
	if err != nil {
		if errors.Is(err, clerkerrors.ErrIntegrationTokenMissing) {
			return nil, apierror.IntegrationTokenMissing(integration.ID)
		}
		return nil, apierror.Unexpected(err)
	}

	return project, nil
}

// Link links an integration to a user & account and performs necessary provisioning
func (s *Service) Link(ctx context.Context, integrationID string, vercelLinkParams *params.VercelLinkParams) (*serialize.IntegrationResponse, apierror.Error) {
	activeSession, _ := sdk.SessionClaimsFromContext(ctx)

	integration, err := s.fetchIntegration(ctx, integrationID)
	if err != nil {
		return nil, err
	}

	txErr := s.deps.DB().PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// Link to user if not already linked
		if !integration.UserID.Valid {
			integration.UserID = null.StringFrom(activeSession.Subject)
		}

		err := s.integrationRepo.Update(ctx, tx, integration)
		if err != nil {
			return true, err
		}

		if shouldLinkProject(vercelLinkParams) {
			// Load app
			app, err := s.appRepo.FindByID(ctx, tx, *vercelLinkParams.ApplicationID)
			if err != nil {
				return true, err
			}

			// Ensure app is owned by current user
			owned, roleErr := s.appOwnershipRepo.ExistsAppUserOwner(ctx, tx, activeSession.Subject, app.ID)
			if roleErr != nil {
				return true, roleErr
			} else if !owned {
				return true, apierror.ApplicationNotFound(app.ID)
			}

			provErr := s.vercelClient.ProvisionProject(ctx, s.deps, integration, app, *vercelLinkParams.ProjectID)
			if provErr != nil {
				switch {
				case errors.Is(provErr, clerkerrors.ErrDevelopmentInstanceMissing):
					return true, apierror.DevelopmentInstanceMissing(app.ID)
				case errors.Is(provErr, clerkerrors.ErrIntegrationProvisionFailure):
					return true, apierror.IntegrationProvisioningFailed(integration.ID, *vercelLinkParams.ProjectID)
				case errors.Is(provErr, clerkerrors.ErrIntegrationTokenMissing):
					return true, apierror.IntegrationTokenMissing(integration.ID)
				default:
					return true, apierror.Unexpected(provErr)
				}
			}

			vercelAppIntegrationMeta := &model.VercelApplicationIntegrationMetadata{ProjectID: *vercelLinkParams.ProjectID}

			appIntegration, buildErr := model.BuildAppIntegration(integration.InstanceID, app.ID, integration.ID, vercelAppIntegrationMeta)
			if buildErr != nil {
				return true, buildErr
			}

			insertErr := s.appIntegrationRepo.Insert(ctx, tx, appIntegration)
			if insertErr != nil {
				return true, insertErr
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

	obfuscateSecrets := clerksdk.ActorHasLimitedAccess(ctx)
	return serialize.Integration(integration, obfuscateSecrets), nil
}

func shouldLinkProject(vercelLinkParams *params.VercelLinkParams) bool {
	return vercelLinkParams.ApplicationID != nil && vercelLinkParams.ProjectID != nil
}

func (s *Service) fetchIntegration(ctx context.Context, integrationID string) (*model.Integration, apierror.Error) {
	integration, err := s.integrationRepo.QueryByID(ctx, s.deps.DB(), integrationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if integration == nil {
		return nil, apierror.IntegrationNotFound(integrationID)
	}
	return integration, nil
}
