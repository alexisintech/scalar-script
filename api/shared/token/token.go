package token

import (
	"context"
	"errors"

	"clerk/api/shared/jwt_template"
	"clerk/model"
	"clerk/pkg/auth"
	"clerk/pkg/cenv"
	"clerk/pkg/jwt"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

// ErrUserNotFound indicates that a session token was requested for a user that
// no longer exists in the database.
var ErrUserNotFound = errors.New("user not found")

// GenerateSessionToken creates a session token for the given session. If there
// are custom claims configured (i.e. JWT Template), they are applied as well.
//
// For more info on session tokens refer to package auth.
func GenerateSessionToken(
	ctx context.Context,
	clock clockwork.Clock,
	exec database.Executor,
	env *model.Env,
	session *model.Session,
	origin string,
	issuer string,
) (string, error) {
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	orgMembershipRepo := repository.NewOrganizationMembership()

	params := auth.GenerateParams{
		Session:        session,
		OrgMemberships: make(model.OrganizationMembershipsWithRole, 0),
		Issuer:         issuer,
		Origin:         origin,
	}

	if shouldUseLegacyOrgsClaim(env.Application) && env.AuthConfig.IsOrganizationsEnabled() {
		var err error
		params.OrgMemberships, err = orgMembershipRepo.FindAllByUserWithRole(ctx, exec, session.UserID)
		if err != nil {
			return "", err
		}
	}

	if session.ActiveOrganizationID.Valid && env.AuthConfig.IsOrganizationsEnabled() {
		var err error
		params.ActiveOrgMembership, err = orgMembershipRepo.QueryByOrganizationAndUser(ctx, exec, session.ActiveOrganizationID.String, session.UserID)
		if err != nil {
			return "", err
		}
	}

	if err := applyBillingParams(ctx, exec, env, session, &params); err != nil {
		return "", err
	}

	// apply configured custom claims, if any
	if env.Instance.CustomSessionTokenTemplate() {
		templateID := env.Instance.SessionTokenTemplateID.String
		instanceID := env.Instance.ID

		tmpldata := jwt_template.Data{
			UserSettings:   userSettings,
			OrgMemberships: params.OrgMemberships,
			Issuer:         issuer,
			Origin:         origin,
			SessionActor:   session.Actor.JSON,
		}

		var err error

		tmpldata.JWTTmpl, err = repository.NewJWTTemplate().FindByIDAndInstance(ctx, exec, templateID, instanceID)
		if err != nil {
			return "", err
		}

		tmpldata.User, err = repository.NewUsers().QueryByIDAndInstance(ctx, exec, session.UserID, instanceID)
		if err != nil {
			return "", err
		}
		if tmpldata.User == nil {
			return "", ErrUserNotFound
		}

		tmpldata.ActiveOrgMembership = params.ActiveOrgMembership

		tmpldata.OrgMemberships, err = orgMembershipRepo.FindAllByUserWithRole(ctx, exec, session.UserID)
		if err != nil {
			return "", err
		}

		tmpl, err := jwt_template.New(exec, clock, tmpldata)
		if err != nil {
			return "", err
		}

		params.CustomClaims, err = tmpl.Execute(ctx)
		if err != nil {
			return "", err
		}
	}

	return auth.GenerateSessionToken(clock, env, params)
}

type Service struct {
	domainRepo *repository.Domain
}

func NewService() *Service {
	return &Service{
		domainRepo: repository.NewDomain(),
	}
}

// GetIssuer returns the proper issuer to be used as a JWT's "iss" claim.
// The issuer is always set according to the instance's primary domain.
func (s *Service) GetIssuer(
	ctx context.Context,
	exec database.Executor,
	dmn *model.Domain,
	instance *model.Instance,
) (string, error) {
	// Issuer for satellite domains is always the primary.
	if dmn.IsSatellite(instance) {
		var err error
		dmn, err = s.domainRepo.FindByID(ctx, exec, instance.ActiveDomainID)
		if err != nil {
			return "", err
		}
	}
	return dmn.FapiURL(), nil
}

// GetTemplateCategory returns the value of the "cat" header based on
// AuthConfig settings.
func GetTemplateCategory(authConfig *model.AuthConfig) string {
	jwtCat := jwt.ClerkJWTTemplateCategory
	if authConfig.SessionSettings.UseIgnoreJWTCat {
		jwtCat = jwt.ClerkIgnoreTokenCategory
	}
	return jwtCat
}

// shouldUseLegacyOrgsClaim calculates whether we should include the legacy 'orgs' claim in the session token.
// This is happening based on the two below settings
// Application is created before the specified cutoff timestamp
// Application has manually opt-out of the usage of this claim
func shouldUseLegacyOrgsClaim(app *model.Application) bool {
	orgsClaimCuttoffTimestamp := cenv.GetInt64(cenv.OrgsClaimCutoffTimestamp)
	if app.CreatedAt.UnixMilli() >= orgsClaimCuttoffTimestamp {
		return false
	}
	if cenv.ResourceHasAccess(cenv.FlagOrgsClaimOptOutApplicationIDs, app.ID) {
		return false
	}

	return true
}

func applyBillingParams(ctx context.Context, exec database.Executor, env *model.Env, session *model.Session, params *auth.GenerateParams) error {
	if !env.Instance.HasBillingEnabled() {
		return nil
	}

	plans, err := repository.NewBillingPlans().FindAllByInstanceID(ctx, exec, env.Instance.ID)
	if err != nil {
		return err
	}
	planKeysMap := make(map[string]string)
	for _, plan := range plans {
		planKeysMap[plan.ID] = plan.Key
	}

	hasOrganizationsEnabled := env.AuthConfig.IsOrganizationsEnabled() && session.ActiveOrganizationID.Valid

	resourceIDs := []string{session.UserID}
	if hasOrganizationsEnabled {
		resourceIDs = append(resourceIDs, session.ActiveOrganizationID.String)
	}
	subscriptions, err := repository.NewBillingSubscriptions().FindAllByResourceIDs(ctx, exec, resourceIDs)
	if err != nil {
		return err
	}

	if env.Instance.HasBillingEnabledForUsers() {
		params.BillingUserPlanKey = getBillingPlanKeyForResourceID(subscriptions, planKeysMap, session.UserID)
	}

	if env.Instance.HasBillingEnabledForOrganizations() && hasOrganizationsEnabled {
		params.BillingOrgPlanKey = getBillingPlanKeyForResourceID(subscriptions, planKeysMap, params.ActiveOrgMembership.OrganizationID)
	}
	return nil
}

func getBillingPlanKeyForResourceID(subscriptions []*model.BillingSubscription, planKeysMap map[string]string, resourceID string) *string {
	for _, sub := range subscriptions {
		if sub.ResourceID == resourceID {
			if planKey, ok := planKeysMap[sub.BillingPlanID]; ok {
				return &planKey
			}
			break
		}
	}
	return nil
}
