// edgereplication is responsible for replicating
// data change to our edge workers.
//
// replication is accomplished through background workers
// that fire off a request to the edge workers to update
// or delete data.
package edgereplication

import (
	"context"
	"encoding/json"
	"fmt"

	"clerk/api/shared/token"
	"clerk/model"
	"clerk/pkg/clerkerrors"
	edge_client_service "clerk/pkg/edgeclientservice"
	"clerk/pkg/jobs"
	"clerk/pkg/jwt"
	"clerk/pkg/keygen"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/vgarvardt/gue/v2"
)

type Service struct {
	Enabled bool

	gueClient       *gue.Client
	authConfigRepo  *repository.AuthConfig
	jwtTemplateRepo *repository.JWTTemplate
}

// NewService creates a new instance of the Service.
func NewService(gueClient *gue.Client, enabled bool) *Service {
	return &Service{
		Enabled:         enabled,
		gueClient:       gueClient,
		authConfigRepo:  repository.NewAuthConfig(),
		jwtTemplateRepo: repository.NewJWTTemplate(),
	}
}

func (s *Service) EnqueuePutInstance(ctx context.Context, tx database.Tx, instanceID string) error {
	if !s.Enabled {
		return nil
	}

	jobArgs := jobs.ReplicateInstanceToEdgeArgs{
		InstanceID: instanceID,
		Action:     jobs.ReplicateActionTypePut,
	}
	return jobs.ReplicateInstanceToEdge(ctx, s.gueClient, jobArgs, jobs.WithTx(tx))
}

func (s *Service) EnqueuePutDomain(ctx context.Context, tx database.Tx, domainID string) error {
	if !s.Enabled {
		return nil
	}

	jobArgs := jobs.ReplicateDomainToEdgeArgs{
		DomainID: domainID,
		Action:   jobs.ReplicateActionTypePut,
	}
	return jobs.ReplicateDomainToEdge(ctx, s.gueClient, jobArgs, jobs.WithTx(tx))
}

func (s *Service) SerializeInstance(ctx context.Context, db database.Executor, instance *model.Instance) (*edge_client_service.PutInstanceByIdRequest, error) {
	authConfig, err := s.authConfigRepo.FindByID(ctx, db, instance.ActiveAuthConfigID)
	if err != nil {
		return nil, fmt.Errorf("authConfig.FindByID %s: %w", instance.ActiveAuthConfigID, err)
	}
	jwtTemplateCategory := token.GetTemplateCategory(authConfig)
	allowedOrigins := []string(instance.AllowedOrigins)

	sessionTokenTemplate, err := s.getSessionTokenTemplateParam(ctx, db, instance)
	if err != nil {
		return nil, err
	}

	privateKey := instance.PrivateKey
	// Skip conversion for EdDSA keys
	if instance.KeyAlgorithm == string((keygen.RSA{}).ID()) {
		var err error
		privateKey, err = jwt.ConvertPKCS1ToPKCS8(instance.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("private key conversion from PKCS1 to PKCS8 failed: id=%s err=%w", instance.ID, err)
		}
	}

	req := edge_client_service.PutInstanceByIdRequest{
		InstanceId: instance.ID,
		Data: edge_client_service.PutInstancesInstanceIdBody{
			CreatedAt:            float64(instance.CreatedAt.UnixMilli()),
			UpdatedAt:            float64(instance.UpdatedAt.UnixMilli()),
			EnvironmentType:      instance.EnvironmentType,
			SessionTokenTemplate: sessionTokenTemplate,
			AuthConfig: edge_client_service.PutInstancesInstanceIdBodyAuthConfig{
				SessionSettings: edge_client_service.PutInstancesInstanceIdBodyAuthConfigSessionSettings{
					UrlBasedSessionSyncing: authConfig.SessionSettings.URLBasedSessionSyncing,
					JwtTemplateCategory:    &jwtTemplateCategory,
				},
				OrganizationSettings: edge_client_service.PutInstancesInstanceIdBodyAuthConfigOrganizationSettings{
					Enabled: authConfig.OrganizationSettings.Enabled,
				},
			},
			SigningKeys: []edge_client_service.PutInstancesInstanceIdBodySigningKeysItem{
				{
					PublicKey:    instance.PublicKey,
					PrivateKey:   privateKey,
					KeyAlgorithm: instance.KeyAlgorithm,
				},
			},
			AllowedOrigins: &allowedOrigins,
		},
	}
	return &req, nil
}

func (s *Service) getSessionTokenTemplateParam(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
) (*edge_client_service.PutInstancesInstanceIdBodySessionTokenTemplateUnion, error) {
	if !instance.SessionTokenTemplateID.Valid {
		return nil, nil
	}
	jwtTempl, err := s.jwtTemplateRepo.QueryByIDAndInstance(ctx, exec, instance.SessionTokenTemplateID.String, instance.ID)
	if err != nil {
		return nil, fmt.Errorf("cannot query session token template %s for instance %s: %w", instance.SessionTokenTemplateID.String, instance.ID, err)
	}
	if jwtTempl == nil {
		return nil, nil
	}
	var claims map[string]any
	if err := json.Unmarshal(jwtTempl.Claims, &claims); err != nil {
		return nil, clerkerrors.WithStacktrace("cannot unmarshal session token %s claims: %w", jwtTempl.ID, err)
	}
	return &edge_client_service.PutInstancesInstanceIdBodySessionTokenTemplateUnion{
		ID:     jwtTempl.ID,
		Claims: claims,
	}, nil
}
