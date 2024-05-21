package instance_keys

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/dapi/serialize"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/validator"
	"clerk/pkg/generate"
	sdkutils "clerk/pkg/sdk"
	"clerk/repository"
	"clerk/utils/database"
)

type Service struct {
	db database.Database

	// repositories
	appRepo      *repository.Applications
	instanceRepo *repository.Instances
	keyRepo      *repository.InstanceKeys
}

func NewService(db database.Database) *Service {
	return &Service{
		db:           db,
		appRepo:      repository.NewApplications(),
		instanceRepo: repository.NewInstances(),
		keyRepo:      repository.NewInstanceKeys(),
	}
}

func (s *Service) ListAll(ctx context.Context) (any, apierror.Error) {
	session, _ := sdkutils.GetActiveSession(ctx)

	apps, err := s.appRepo.QueryByUserWithInstanceKeys(ctx, s.db, session.Subject)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.InstanceKeys(apps), nil
}

// List returns all instance keys for given instance
func (s *Service) List(ctx context.Context, instanceID string) ([]*serialize.InstanceKeyResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	instanceKeys, err := s.keyRepo.FindAllByInstance(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]*serialize.InstanceKeyResponse, len(instanceKeys))
	for i, key := range instanceKeys {
		responses[i] = serialize.InstanceKey(key, true)
	}

	// Include instance public key in the response
	responses = append(responses, serialize.InstanceKeyPublic(env.Instance))

	// Include instance public key in the response in PEM format as well
	responses = append(responses, serialize.InstanceKeyPublicPEM(env.Instance))

	// Include FAPIv2 key
	responses = append(responses, serialize.InstanceFAPIKeyV2(env.Instance, env.Domain))

	// Include post-Kima secret key
	obfuscateSecrets := sdkutils.ActorHasLimitedAccess(ctx)
	for _, key := range instanceKeys {
		responses = append(responses, serialize.SecretKey(key, obfuscateSecrets))
	}

	return responses, nil
}

// Read returns the requested instance key
func (s *Service) Read(ctx context.Context, instanceID string, instanceKeyID string) (*serialize.InstanceKeyResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	instanceKey, err := s.keyRepo.FindByIDAndInstance(ctx, s.db, instanceKeyID, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if env.Instance.UsesKimaKeys() {
		isImpersonationSession := activeSession.Actor != nil
		return serialize.SecretKey(instanceKey, isImpersonationSession), nil
	}

	return serialize.InstanceKey(instanceKey, false), nil
}

type InstanceKey struct {
	ID     string `json:"id"`
	Name   string `json:"name" validate:"required,min=1,max=255"`
	Secret string `json:"secret"`
}

// Create a new instance key
func (s *Service) Create(ctx context.Context, instanceKey *InstanceKey) (*serialize.InstanceKeyResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	validate := validator.FromContext(ctx)
	if err := validate.Struct(instanceKey); err != nil {
		return nil, apierror.FormValidationFailed(err)
	}

	var newKey *model.InstanceKey
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		newKey, err = generate.InstanceKey(
			ctx,
			tx,
			env.Instance,
			generate.WithInstanceKeyName(instanceKey.Name),
		)
		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.SecretKey(newKey, false), nil
}

// Delete deletes the given instance key
func (s *Service) Delete(ctx context.Context, instanceID, instanceKeyID string) apierror.Error {
	// Start transaction to be able to SELECT instance for UPDATE
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// Lock on parent instance
		_, apiErr := s.getInstanceForUpdate(ctx, tx, instanceID)
		if apiErr != nil {
			return true, apiErr
		}

		// Ensure this is not the last key for this instance
		count, err := s.keyRepo.CountForInstance(ctx, tx, instanceID)
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		if count == 1 {
			return true, apierror.LastInstanceKey(instanceID)
		}

		err = s.keyRepo.DeleteByIDAndInstance(ctx, tx, instanceKeyID, instanceID)
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		return false, nil
	})
	if txErr != nil {
		// Check if already an api error
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return apiErr
		}

		return apierror.Unexpected(txErr)
	}

	return nil
}

func (s *Service) getInstanceForUpdate(ctx context.Context, exec database.Executor, instanceID string) (
	*model.Instance, apierror.Error) {
	instance, err := s.instanceRepo.QueryByIDForUpdate(ctx, exec, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if instance == nil {
		return nil, apierror.InstanceNotFound(instanceID)
	}

	return instance, nil
}
