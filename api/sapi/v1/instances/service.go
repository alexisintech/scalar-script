package instances

import (
	"context"
	"fmt"
	"strconv"

	"clerk/api/apierror"
	"clerk/api/sapi/serialize"
	"clerk/api/sapi/v1/serializable"
	"clerk/api/shared/edgecache"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/ctx/environment"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	db        database.Database
	gueClient *gue.Client

	domainRepo      *repository.Domain
	instanceService *serializable.InstanceService

	authConfigRepo *repository.AuthConfig
	instanceRepo   *repository.Instances
}

func NewService(db database.Database, gueClient *gue.Client) *Service {
	return &Service{
		db:              db,
		gueClient:       gueClient,
		instanceService: serializable.NewInstanceService(db, gueClient),
		authConfigRepo:  repository.NewAuthConfig(),
		instanceRepo:    repository.NewInstances(),
	}
}

func (s *Service) Read(ctx context.Context) (*serialize.InstanceResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	serializableInstance, err := s.instanceService.ConvertToSerializable(ctx, s.db, env)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return serialize.Instance(serializableInstance), nil
}

type UpdateOrganizationSettingsParams struct {
	CreationLimit     *int `json:"creation_limit"`
	MembershipLimit   *int `json:"membership_limit"`
	RoleCreationLimit *int `json:"role_creation_limit"`
	PermissionLimit   *int `json:"permissions_limit"`
}

func (p UpdateOrganizationSettingsParams) validate() apierror.Error {
	var apiErr apierror.Error
	if p.CreationLimit != nil && *p.CreationLimit < 0 {
		apiErr = apierror.Combine(apiErr, apierror.FormInvalidParameterValue("creation_limit", strconv.Itoa(*p.CreationLimit)))
	}
	if p.MembershipLimit != nil && *p.MembershipLimit < 0 {
		apiErr = apierror.Combine(apiErr, apierror.FormInvalidParameterValue("membership_limit", strconv.Itoa(*p.MembershipLimit)))
	}
	if p.RoleCreationLimit != nil && *p.RoleCreationLimit < 0 {
		apiErr = apierror.Combine(apiErr, apierror.FormInvalidParameterValue("role_creation_limit", strconv.Itoa(*p.RoleCreationLimit)))
	}
	if p.PermissionLimit != nil && *p.PermissionLimit < 0 {
		apiErr = apierror.Combine(apiErr, apierror.FormInvalidParameterValue("permissions_limit", strconv.Itoa(*p.PermissionLimit)))
	}
	return apiErr
}

func (s *Service) UpdateOrganizationSettings(ctx context.Context, params UpdateOrganizationSettingsParams) (*serialize.InstanceResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	shouldUpdateAuthConfig := false
	if params.CreationLimit != nil {
		env.AuthConfig.OrganizationSettings.CreateQuotaPerUser = *params.CreationLimit
		shouldUpdateAuthConfig = true
	}
	if params.MembershipLimit != nil {
		env.AuthConfig.OrganizationSettings.MaxAllowedMemberships = *params.MembershipLimit
		shouldUpdateAuthConfig = true
	}
	if params.RoleCreationLimit != nil {
		env.AuthConfig.OrganizationSettings.MaxAllowedRoles = *params.RoleCreationLimit
		shouldUpdateAuthConfig = true
	}
	if params.PermissionLimit != nil {
		env.AuthConfig.OrganizationSettings.MaxAllowedPermissions = *params.PermissionLimit
		shouldUpdateAuthConfig = true
	}

	var serializableInstance *serializable.Instance
	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		var err error
		if shouldUpdateAuthConfig {
			err := s.authConfigRepo.UpdateOrganizationSettings(ctx, txEmitter, env.AuthConfig)
			if err != nil {
				return true, err
			}
		}

		serializableInstance, err = s.instanceService.ConvertToSerializable(ctx, txEmitter, env)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}
	return serialize.Instance(serializableInstance), nil
}

type UpdateSMSSettingsParams struct {
	MaxPrice           *float32 `json:"max_price"`
	DevMonthlySMSLimit *int     `json:"dev_monthly_sms_limit"`
}

func (p UpdateSMSSettingsParams) validate(instance *model.Instance) apierror.Error {
	var apiErr apierror.Error

	if p.MaxPrice != nil && *p.MaxPrice < 0 {
		apiErr = apierror.Combine(apiErr, apierror.FormInvalidParameterValue("max_price", fmt.Sprintf("%f", *p.MaxPrice)))
	}

	if p.DevMonthlySMSLimit != nil {
		if instance.IsProduction() {
			apiErr = apierror.Combine(apiErr, apierror.FormParameterNotAllowedConditionally("dev_monthly_sms_limit", "environment", instance.EnvironmentType))
		}

		if *p.DevMonthlySMSLimit < 0 {
			apiErr = apierror.Combine(apiErr, apierror.FormInvalidParameterValue("dev_monthly_sms_limit", fmt.Sprintf("%d", *p.DevMonthlySMSLimit)))
		}
	}

	return apiErr
}

func (s *Service) UpdateSMSSettings(ctx context.Context, params UpdateSMSSettingsParams) (*serialize.InstanceResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := params.validate(env.Instance)
	if apiErr != nil {
		return nil, apiErr
	}

	shouldUpdateInstanceConfig := false

	if params.MaxPrice != nil {
		env.Instance.Communication.TwilioSMSMaxPrice = null.Float32FromPtr(params.MaxPrice)
		shouldUpdateInstanceConfig = true
	}

	if params.DevMonthlySMSLimit != nil {
		env.Instance.Communication.DevMonthlySMSLimit = null.IntFromPtr(params.DevMonthlySMSLimit)
		shouldUpdateInstanceConfig = true
	}

	var serializableInstance *serializable.Instance
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		if shouldUpdateInstanceConfig {
			err := s.instanceRepo.UpdateCommunication(ctx, tx, env.Instance)
			if err != nil {
				return true, err
			}
		}

		serializableInstance, err = s.instanceService.ConvertToSerializable(ctx, tx, env)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.Instance(serializableInstance), nil
}

type UpdateUserLimitsParams struct {
	MaxUserLimit *int `json:"max_user_limit"`
}

func (p UpdateUserLimitsParams) validate() apierror.Error {
	var apiErr apierror.Error
	if p.MaxUserLimit != nil && *p.MaxUserLimit < 0 {
		apiErr = apierror.Combine(apiErr, apierror.FormInvalidParameterValue("max_user_limit", fmt.Sprintf("%d", *p.MaxUserLimit)))
	}
	return apiErr
}

func (s *Service) UpdateUserLimits(ctx context.Context, params UpdateUserLimitsParams) (*serialize.InstanceResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if env.Instance.IsProduction() {
		return nil, apierror.CannotUpdateUserLimitsOnProductionInstance()
	}

	apiErr := params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	shouldUpdateAuthConfig := false
	if params.MaxUserLimit != nil {
		env.AuthConfig.MaxAllowedUsers = null.IntFromPtr(params.MaxUserLimit)
		shouldUpdateAuthConfig = true
	}

	var serializableInstance *serializable.Instance
	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		var err error
		if shouldUpdateAuthConfig {
			err := s.authConfigRepo.Update(ctx, txEmitter, env.AuthConfig, sqbmodel.AuthConfigColumns.MaxAllowedUsers)
			if err != nil {
				return true, err
			}
		}

		serializableInstance, err = s.instanceService.ConvertToSerializable(ctx, txEmitter, env)
		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}
	return serialize.Instance(serializableInstance), nil
}

func (s *Service) PurgeCache(ctx context.Context) (any, apierror.Error) {
	env := environment.FromContext(ctx)

	domains, err := s.domainRepo.FindAllByInstanceID(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	for _, domain := range domains {
		if err = edgecache.PurgeAllFapiByDomain(ctx, s.gueClient, s.db, domain); err != nil {
			return nil, apierror.Unexpected(err)
		}
		if err = edgecache.PurgeJWKS(ctx, s.gueClient, s.db, domain); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	return map[string]any{"message": "Cache will be purged soon"}, nil
}
