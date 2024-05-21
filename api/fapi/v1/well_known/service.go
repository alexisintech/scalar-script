package well_known

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/repository"
	"clerk/utils/database"
)

type Service struct {
	db           database.Database
	domainRepo   *repository.Domain
	instanceRepo *repository.Instances
}

func NewService(db database.Database) *Service {
	return &Service{
		db:           db,
		instanceRepo: repository.NewInstances(),
		domainRepo:   repository.NewDomain(),
	}
}

func (s *Service) AssetLinks(_ context.Context, instance *model.Instance) (interface{}, apierror.Error) {
	return serialize.AssetLinks(instance.AndroidTarget)
}

func (s *Service) AppleAppSiteAssociation(_ context.Context, instance *model.Instance) (interface{}, apierror.Error) {
	includeWebCredentials := cenv.ResourceHasAccess(cenv.FlagAllowPasskeysInstanceIDs, instance.ID)
	return serialize.AppleAppSiteAssociation(instance.AppleAppID.String, []string{"/v1/oauth-native-callback"}, includeWebCredentials), nil
}

func (s *Service) OpenIDConfiguration(_ context.Context, domain *model.Domain) (interface{}, apierror.Error) {
	return serialize.OpenIDConfiguration(domain), nil
}
