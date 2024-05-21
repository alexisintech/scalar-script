package jwt_services

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/validator"
	"clerk/pkg/jwt_services"
	"clerk/pkg/params"
	clerksdk "clerk/pkg/sdk"
	"clerk/repository"
	"clerk/utils/database"
)

type Service struct {
	db              database.Database
	jwtServicesRepo *repository.JWTServices
}

func NewService(db database.Database) *Service {
	return &Service{
		db:              db,
		jwtServicesRepo: repository.NewJWTServices(),
	}
}

// Read returns the services for the given instance
func (s *Service) Read(ctx context.Context) (map[model.JWTServiceType]*serialize.JWTServiceResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	jwtSvcs, err := s.jwtServicesRepo.FindAllByAuthConfigID(ctx, s.db, env.Instance.ActiveAuthConfigID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	obfuscateSecrets := clerksdk.ActorHasLimitedAccess(ctx)
	return serialize.JWTService(jwtSvcs, obfuscateSecrets), nil
}

// Update updates the services for the given instance
func (s *Service) Update(ctx context.Context, updateForm *params.UpdateJWTServicesForm) (map[model.JWTServiceType]*serialize.JWTServiceResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	validate := validator.FromContext(ctx)
	if err := validate.Struct(updateForm); err != nil {
		return nil, apierror.FormValidationFailed(err)
	}

	// jwt services
	jwtSvcs, err := s.jwtServicesRepo.FindAllByAuthConfigID(ctx, s.db, env.Instance.ActiveAuthConfigID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	jwtSvcMap := model.JWTServiceClass.ToMap(jwtSvcs)

	// For each applicable service, perform CRUD as necessary

	updateMap := getJWTSvcUpdateMap(updateForm)

	for jwtSvcType, enabled := range updateMap {
		vendor, err := jwt_services.GetVendor(jwtSvcType)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		jwtSvc := jwtSvcMap[jwtSvcType]

		if enabled {
			jwtSvc, err = vendor.CreateOrUpdate(ctx, s.db, jwtSvc, env.Instance, updateForm)
			if err != nil {
				return nil, apierror.Unexpected(err)
			}
			jwtSvcMap[jwtSvcType] = jwtSvc
		} else {
			err = vendor.Delete(ctx, s.db, jwtSvc)
			if err != nil {
				return nil, apierror.Unexpected(err)
			}
			delete(jwtSvcMap, jwtSvcType)
		}
	}

	respArray := []*model.JWTService{}
	for _, v := range jwtSvcMap {
		respArray = append(respArray, v)
	}

	obfuscateSecrets := clerksdk.ActorHasLimitedAccess(ctx)
	return serialize.JWTService(respArray, obfuscateSecrets), nil
}

func getJWTSvcUpdateMap(updateForm *params.UpdateJWTServicesForm) map[model.JWTServiceType]bool {
	updateMap := map[model.JWTServiceType]bool{}

	if updateForm.Firebase != nil {
		updateMap[model.JWTServiceTypeFirebase] = updateForm.Firebase.Enabled
	}

	return updateMap
}
