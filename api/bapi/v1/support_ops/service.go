package plain

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/constants"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/team-plain/go-sdk/customercards"
)

// Service contains business logic for various support ops functions
type Service struct {
	clock clockwork.Clock
	db    database.Database

	// repositories
	users             *repository.Users
	applications      *repository.Applications
	instances         *repository.Instances
	domains           *repository.Domain
	dnschecks         *repository.DNSChecks
	subscriptionPlans *repository.SubscriptionPlans
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock: deps.Clock(),
		db:    deps.ReadOnlyDB(),

		users:             repository.NewUsers(),
		applications:      repository.NewApplications(),
		instances:         repository.NewInstances(),
		domains:           repository.NewDomain(),
		dnschecks:         repository.NewDNSChecks(),
		subscriptionPlans: repository.NewSubscriptionPlans(),
	}
}

func (s *Service) GetCustomerCard(ctx context.Context, email string, cardKeys []string) (*customercards.Response, apierror.Error) {
	if len(cardKeys) == 0 {
		// "at least one card key is required"
		return nil, apierror.InvalidRequestBody(errors.New("at least one card key is required"))
	}

	// get system instance
	systemInstance, err := s.instances.SystemProductionDashboardInstance(ctx, s.db)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// get the user by incoming email
	user, err := s.users.FindByEmailAndInstanceID(ctx, s.db, email, systemInstance.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apierror.UserNotFound(email)
		}
		return nil, apierror.Unexpected(err)
	}

	// get all instance & application pairs that the user is an owner of
	instanceAndApplicationPairs, err := s.instances.QueryByOwnerUserID(ctx, s.db, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var cardRows []serialize.PlainCustomerCardRow

	for _, instanceAndApplicationPair := range instanceAndApplicationPairs {
		// get related subscription plans
		plans, err := s.subscriptionPlans.FindAllByApplication(ctx, s.db, instanceAndApplicationPair.Application.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		planTitles := make([]string, len(plans))
		for _, plan := range plans {
			planTitles = append(planTitles, plan.Title)
		}

		cardRows = append(cardRows, serialize.PlainCustomerCardRow{
			UserID:          user.ID,
			ApplicationName: instanceAndApplicationPair.Application.Name,
			ApplicationID:   instanceAndApplicationPair.Instance.ApplicationID,
			InstanceID:      instanceAndApplicationPair.Instance.ID,
			InstanceEnv:     instanceAndApplicationPair.Instance.EnvironmentType,
			PlanTitle:       strings.Join(planTitles, ", "),
		})
	}

	return serialize.PlainCustomerCard(cardKeys[0], cardRows), nil
}

func (s *Service) GetCustomerData(ctx context.Context, userID string) (*serialize.SupportOpsCustomerDataResponse, apierror.Error) {
	systemInstance, err := s.instances.SystemProductionDashboardInstance(ctx, s.db)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	user, err := s.users.QueryByIDAndInstance(ctx, s.db, userID, systemInstance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if user == nil {
		return nil, apierror.UserNotFound(userID)
	}

	// 1 query for app+instance pairs
	instanceAndApplicationPairs, err := s.instances.QueryByOwnerUserID(ctx, s.db, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if len(instanceAndApplicationPairs) == 0 {
		return &serialize.SupportOpsCustomerDataResponse{}, nil
	}

	// convert list of [application, instance] to
	// kv pair of applicationID => SupportOpsApplication
	appsMap := make(map[string]*serialize.SupportOpsApplication)
	for _, pair := range instanceAndApplicationPairs {
		if _, ok := appsMap[pair.Application.ID]; !ok {
			appsMap[pair.Application.ID] = &serialize.SupportOpsApplication{
				ID:                   pair.Application.ID,
				Name:                 pair.Application.Name,
				CreatedAt:            pair.Application.CreatedAt,
				UpdatedAt:            pair.Application.UpdatedAt,
				Type:                 pair.Application.Type,
				LogoPublicURL:        pair.Application.LogoPublicURL,
				FaviconPublicURL:     pair.Application.FaviconPublicURL,
				CreatorID:            pair.Application.CreatorID,
				AccountPortalAllowed: pair.Application.AccountPortalAllowed,
				ExceededMausTimes:    pair.Application.ExceededMausTimes,
				Demo:                 pair.Application.Demo,
				HardDeleteAt:         pair.Application.HardDeleteAt,
			}
		}
		appsMap[pair.Application.ID].Instances = append(appsMap[pair.Application.ID].Instances, &serialize.SupportOpsInstance{
			ID:                     pair.Instance.ID,
			EnvironmentType:        pair.Instance.EnvironmentType,
			ApplicationID:          pair.Instance.ApplicationID,
			ActiveDomainID:         pair.Instance.ActiveDomainID,
			ActiveAuthConfigID:     pair.Instance.ActiveAuthConfigID,
			ActiveDisplayConfigID:  pair.Instance.ActiveDisplayConfigID,
			CreatedAt:              pair.Instance.CreatedAt,
			UpdatedAt:              pair.Instance.UpdatedAt,
			HomeOrigin:             pair.Instance.HomeOrigin,
			SvixAppID:              pair.Instance.SvixAppID,
			AppleAppID:             pair.Instance.AppleAppID,
			AndroidTarget:          pair.Instance.AndroidTarget,
			SessionTokenTemplateID: pair.Instance.SessionTokenTemplateID,
			AllowedOrigins:         pair.Instance.AllowedOrigins,
			MinClerkjsVersion:      pair.Instance.MinClerkjsVersion,
			MaxClerkjsVersion:      pair.Instance.MaxClerkjsVersion,
			AnalyticsWentLiveAt:    pair.Instance.AnalyticsWentLiveAt,
			APIVersion:             pair.Instance.APIVersion,
		})
	}

	sOpsApplications := make([]*serialize.SupportOpsApplication, 0)

	// +N queries for domains + dnschecks (per instance)
	// +N queries for plans               (per application)
	for _, app := range appsMap {
		for i, instance := range app.Instances {
			domain, err := s.domains.QueryByID(ctx, s.db, instance.ActiveDomainID)
			if err != nil {
				return nil, apierror.Unexpected(err)
			}

			if domain != nil {
				sOpsDomain := &serialize.SupportOpsDomain{
					ID:            domain.ID,
					Name:          domain.Name,
					CreatedAt:     domain.CreatedAt,
					UpdatedAt:     domain.UpdatedAt,
					DNSSuccessful: false,
				}

				// if not production, then dns defaults to successful
				if instance.EnvironmentType != string(constants.ETProduction) {
					sOpsDomain.DNSSuccessful = true
				} else {
					dnsCheck, err := s.dnschecks.QueryByDomainID(ctx, s.db, domain.ID)
					if err != nil {
						return nil, apierror.Unexpected(err)
					}
					if dnsCheck != nil {
						sOpsDomain.DNSSuccessful = dnsCheck.Successful
					}
				}

				app.Instances[i].Domain = sOpsDomain
			}
		}

		plans, err := s.subscriptionPlans.FindAllByApplication(ctx, s.db, app.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		sOpsPlans := make([]*serialize.SupportOpsSubscriptionPlan, len(plans))
		for i, plan := range plans {
			sOpsPlans[i] = &serialize.SupportOpsSubscriptionPlan{
				ID:                          plan.ID,
				Title:                       plan.Title,
				MonthlyUserLimit:            plan.MonthlyUserLimit,
				CreatedAt:                   plan.CreatedAt,
				UpdatedAt:                   plan.UpdatedAt,
				DescriptionHTML:             plan.DescriptionHTML,
				Visible:                     plan.Visible,
				StripeProductID:             plan.StripeProductID,
				Features:                    plan.Features,
				BasePlan:                    plan.BasePlan,
				OrganizationMembershipLimit: plan.OrganizationMembershipLimit,
				MonthlyOrganizationLimit:    plan.MonthlyOrganizationLimit,
				Scope:                       plan.Scope,
				VisibleToApplicationIds:     plan.VisibleToApplicationIds,
				Addons:                      plan.Addons,
				IsAddon:                     plan.IsAddon,
			}
		}

		app.Plans = sOpsPlans

		sOpsApplications = append(sOpsApplications, app)
	}

	response := &serialize.SupportOpsCustomerDataResponse{
		Applications: sOpsApplications,
	}
	return response, nil
}
