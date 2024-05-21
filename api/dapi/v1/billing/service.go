package billing

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"clerk/api/apierror"
	"clerk/api/dapi/serialize"
	sharedserialize "clerk/api/serialize"
	"clerk/api/shared/instances"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/rand"
	"clerk/pkg/set"
	"clerk/pkg/slices"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/go-playground/validator/v10"
	"github.com/stripe/stripe-go/v72"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

const (
	stripeOAuthConnectURL = "https://connect.stripe.com/oauth/authorize"
)

type Service struct {
	db               database.Database
	billingConnector billing.Connector

	// services
	instanceService *instances.Service

	// repositories
	authConfigRepo             *repository.AuthConfig
	billingOAuthStateTokenRepo *repository.BillingOAuthStateToken
	billingPlanRepo            *repository.BillingPlans
	instanceRepo               *repository.Instances
	permissionRepo             *repository.Permission
	rolePermissionRepo         *repository.RolePermission
	roleRepo                   *repository.Role
}

func NewService(db database.Database, gueClient *gue.Client, billingConnector billing.Connector) *Service {
	return &Service{
		db:                         db,
		billingConnector:           billingConnector,
		authConfigRepo:             repository.NewAuthConfig(),
		billingOAuthStateTokenRepo: repository.NewBillingOAuthStateToken(),
		billingPlanRepo:            repository.NewBillingPlans(),
		instanceRepo:               repository.NewInstances(),
		permissionRepo:             repository.NewPermission(),
		rolePermissionRepo:         repository.NewRolePermission(),
		roleRepo:                   repository.NewRole(),
		instanceService:            instances.NewService(db, gueClient),
	}
}

type connectParams struct {
	RedirectURL string `json:"redirect_url"`
}

func (p connectParams) validate() apierror.Error {
	if _, err := url.ParseRequestURI(p.RedirectURL); err != nil {
		return apierror.FormInvalidTypeParameter(param.RedirectURL.Name, "valid url")
	}
	return nil
}

func (s *Service) Connect(ctx context.Context, params *connectParams) (*serialize.ConnectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	nonce, err := rand.Token()
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	ost := &model.BillingOAuthStateToken{
		BillingOauthStateToken: &sqbmodel.BillingOauthStateToken{
			InstanceID:  env.Instance.ID,
			RedirectURL: params.RedirectURL,
			Nonce:       nonce,
		},
	}

	var redirectURL *url.URL
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.billingOAuthStateTokenRepo.Insert(ctx, tx, ost)
		if err != nil {
			return true, err
		}

		redirectURL, err = buildStripeConnectURL(nonce)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.BillingConnect(redirectURL.String()), nil
}

type connectCallbackParams struct {
	code  string
	nonce string
}

func (p connectCallbackParams) validate() apierror.Error {
	var apiErr apierror.Error
	if p.code == "" {
		apiErr = apierror.Combine(apiErr, apierror.FormMissingParameter("code"))
	}
	if p.nonce == "" {
		apiErr = apierror.Combine(apiErr, apierror.FormMissingParameter("state"))
	}
	return apiErr
}

func (s *Service) ConnectCallback(ctx context.Context, params connectCallbackParams) (string, apierror.Error) {
	apiErr := params.validate()
	if apiErr != nil {
		return "", apiErr
	}

	var redirectURL string
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		ost, err := s.billingOAuthStateTokenRepo.QueryByNonceNotConsumedForUpdate(ctx, tx, params.nonce)
		if err != nil {
			return true, err
		}
		if ost == nil {
			return true, apierror.ResourceNotFound()
		}

		// mark the nonce as consumed to make sure that it cannot be used,
		// in order to avoid replay attacks
		ost.Consumed = true
		if err := s.billingOAuthStateTokenRepo.UpdateConsumed(ctx, tx, ost); err != nil {
			return true, err
		}

		// make a call to stripe to get the account id
		stripeAccountID, err := s.billingConnector.AccountID(ctx, params.code)
		if err != nil {
			return true, err
		}

		instance, err := s.instanceRepo.FindByID(ctx, tx, ost.InstanceID)
		if err != nil {
			return true, err
		}

		// store the account id into the instance to use it for any billing
		// operation within the instance
		instance.ExternalBillingAccountID = null.StringFrom(stripeAccountID)

		hasBillingPortalEnabled, err := s.hasBillingPortalEnabled(ctx, instance)
		if err != nil {
			return true, err
		}
		instance.BillingPortalEnabled = null.BoolFrom(hasBillingPortalEnabled)

		err = s.instanceRepo.Update(ctx, tx, instance,
			sqbmodel.InstanceColumns.ExternalBillingAccountID,
			sqbmodel.InstanceColumns.BillingPortalEnabled,
		)
		if err != nil {
			return true, err
		}

		redirectURL = ost.RedirectURL
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return "", apiErr
		}
		return "", apierror.Unexpected(txErr)
	}
	return redirectURL, nil
}

func buildStripeConnectURL(nonce string) (*url.URL, error) {
	connectURL, err := url.Parse(stripeOAuthConnectURL)
	if err != nil {
		return nil, err
	}
	values := connectURL.Query()
	values.Add("client_id", cenv.Get(cenv.BillingStripeClientID))
	values.Add("response_type", "code")
	values.Add("scope", "read_write")
	values.Add("redirect_uri", cenv.Get(cenv.BillingOAuthConnectCallbackURL))
	values.Add("state", nonce)

	connectURL.RawQuery = values.Encode()
	return connectURL, nil
}

type CreatePlanParams struct {
	Name         string   `json:"name" validate:"required"`
	Key          string   `json:"key" validate:"required"`
	Description  *string  `json:"description"`
	CustomerType string   `json:"customer_type" validate:"required"`
	PriceInCents int64    `json:"price_in_cents" validate:"min=0"`
	Features     []string `json:"features"`
}

func (params *CreatePlanParams) validate() apierror.Error {
	if err := validator.New().Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

func (s *Service) CreatePlan(ctx context.Context, params *CreatePlanParams) (*serialize.BillingPlanResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	return s.createPlan(ctx, env.Instance, params, false)
}

type GetPlansParams struct {
	CustomerType string
	Query        *string
}

func (p GetPlansParams) toMods() repository.BillingPlanFindAllModifiers {
	return repository.BillingPlanFindAllModifiers{
		Query: p.Query,
	}
}

func (s *Service) GetPlans(ctx context.Context, params GetPlansParams) (*sharedserialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if !constants.BillingCustomerTypes.Contains(params.CustomerType) {
		return nil, apierror.FormInvalidParameterValueWithAllowed("customer_type", params.CustomerType, constants.BillingCustomerTypes.Array())
	}

	mods := params.toMods()
	plans, err := s.billingPlanRepo.FindAllByInstanceAndCustomerTypeWithModifiers(ctx, s.db, env.Instance.ID, params.CustomerType, mods)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return sharedserialize.Paginated(slices.ToInterfaceArray(plans), int64(len(plans))), nil
}

func (s *Service) DeletePlan(ctx context.Context, planID string) (*sharedserialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	plan, err := s.billingPlanRepo.QueryByIDAndInstance(ctx, s.db, planID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if plan == nil {
		return nil, apierror.ResourceNotFound()
	}

	// Sync with Stripe
	updateProductParams := billing.UpdateProductParams{
		Active: null.BoolFrom(false).Ptr(),
	}
	err = s.billingConnector.
		Connect(env.Instance.ExternalBillingAccountID.String).
		UpdateProduct(ctx, plan.StripeProductID.String, updateProductParams)
	if err != nil {
		var stripeErr *stripe.Error
		// Ignore the error if the product is already deleted
		if !errors.As(err, &stripeErr) || stripeErr.Code != stripe.ErrorCodeResourceMissing {
			return nil, apierror.Unexpected(err)
		}
	}

	deleted, err := s.billingPlanRepo.DeleteByIDAndInstance(ctx, s.db, planID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if deleted == 0 {
		return nil, apierror.ResourceNotFound()
	}
	return sharedserialize.DeletedObject(planID, sharedserialize.BillingPlanObjectName), nil
}

type UpdatePlanParams struct {
	Name         *string  `json:"name"`
	Key          *string  `json:"key"`
	Description  *string  `json:"description"`
	PriceInCents *int64   `json:"price_in_cents" validate:"omitempty,min=0"`
	Features     []string `json:"features"`
}

func (params *UpdatePlanParams) validate() apierror.Error {
	if err := validator.New().Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

func (s *Service) UpdatePlan(ctx context.Context, planID string, params *UpdatePlanParams) (*serialize.BillingPlanResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	plan, err := s.billingPlanRepo.QueryByIDAndInstance(ctx, s.db, planID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if plan == nil {
		return nil, apierror.ResourceNotFound()
	}

	if plan.InitialPlan && params.PriceInCents != nil && *params.PriceInCents != plan.PriceInCents {
		return nil, apierror.FormInvalidParameterFormat("price_in_cents", "Cannot change the price of an initial plan.")
	}

	columnsToUpdate := make([]string, 0)
	if params.Name != nil {
		plan.Name = *params.Name
		columnsToUpdate = append(columnsToUpdate, sqbmodel.BillingPlanColumns.Name)
	}
	if params.Key != nil {
		plan.Key = *params.Key
		columnsToUpdate = append(columnsToUpdate, sqbmodel.BillingPlanColumns.Key)
	}
	if params.Description != nil {
		plan.Description = null.StringFromPtr(params.Description)
		columnsToUpdate = append(columnsToUpdate, sqbmodel.BillingPlanColumns.Description)
	}
	prevPriceInCents := plan.PriceInCents
	if params.PriceInCents != nil {
		plan.PriceInCents = *params.PriceInCents
		columnsToUpdate = append(columnsToUpdate, sqbmodel.BillingPlanColumns.PriceInCents)
	}
	if params.Features != nil {
		plan.Features = params.Features
		columnsToUpdate = append(columnsToUpdate, sqbmodel.BillingPlanColumns.Features)
	}

	apiErr = validatePlan(plan)
	if apiErr != nil {
		return nil, apiErr
	}

	billingConnector := s.billingConnector.Connect(env.Instance.ExternalBillingAccountID.String)

	// Sync the product and price with stripe
	if params.Name != nil || params.Description != nil {
		updateProductParams := billing.UpdateProductParams{
			Name:        params.Name,
			Description: plan.Description.Ptr(),
		}
		if err := billingConnector.UpdateProduct(ctx, plan.StripeProductID.String, updateProductParams); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}
	if params.PriceInCents != nil && prevPriceInCents != *params.PriceInCents {
		prevStripePriceID := plan.StripePriceID.String

		// TODO(kostas): Fetch the price from provider and use the values from it
		newPriceParams := billing.CreatePriceParams{
			ProductID:         plan.StripeProductID.String,
			UnitAmount:        *params.PriceInCents,
			Currency:          string(stripe.CurrencyUSD),
			TaxBehavior:       string(stripe.PriceTaxBehaviorExclusive),
			RecurringInterval: null.StringFrom(string(stripe.PriceRecurringIntervalMonth)).Ptr(),
		}
		newPriceID, err := billingConnector.
			CreatePrice(ctx, newPriceParams)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		plan.StripePriceID = null.StringFrom(newPriceID)
		columnsToUpdate = append(columnsToUpdate, sqbmodel.BillingPlanColumns.StripePriceID)

		// TODO(kostas): for every subscription using the previous price, update the subscription with the new price

		// Archive the previous price item
		prevPriceParams := billing.UpdatePriceParams{
			Active: stripe.Bool(false),
		}
		if err := billingConnector.UpdatePrice(ctx, prevStripePriceID, prevPriceParams); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	if err := s.billingPlanRepo.Update(ctx, s.db, plan, columnsToUpdate...); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.BillingPlan(plan), nil
}

func (s *Service) createFirstFreePlan(ctx context.Context, instance *model.Instance, customerType string) error {
	keyPrefix, description := constants.BillingUserPlanKeyPrefix, "Free plan for users"
	if customerType == constants.BillingOrganizationType {
		keyPrefix, description = constants.BillingOrganizationPlanKeyPrefix, "Free plan for organizations"
	}

	params := &CreatePlanParams{
		Name:         "Free",
		Key:          keyPrefix + ":free",
		Description:  &description,
		CustomerType: customerType,
		PriceInCents: 0,
	}
	_, err := s.createPlan(ctx, instance, params, true)
	return err
}

func validatePlan(plan *model.BillingPlan) apierror.Error {
	switch plan.CustomerType {
	case constants.BillingUserType:
		if !strings.HasPrefix(plan.Key, "user:") {
			return apierror.FormInvalidParameterFormat("key", "Needs to start with `user:`.")
		}
	case constants.BillingOrganizationType:
		if !strings.HasPrefix(plan.Key, "org:") {
			return apierror.FormInvalidParameterFormat("key", "Needs to start with `org:`.")
		}
	default:
		return apierror.FormInvalidParameterValueWithAllowed("customer_type", plan.CustomerType, constants.BillingCustomerTypes.Array())
	}
	return nil
}

type UpdateConfigParams struct {
	CustomerTypes *[]string `json:"customer_types"`
	PortalEnabled *bool     `json:"portal_enabled"`
}

func (params *UpdateConfigParams) validate() apierror.Error {
	if params.CustomerTypes != nil {
		for _, customerType := range *params.CustomerTypes {
			if !constants.BillingCustomerTypes.Contains(customerType) {
				return apierror.FormInvalidParameterValueWithAllowed("customer_types", customerType, constants.BillingCustomerTypes.Array())
			}
		}
	}
	return nil
}

func (s *Service) UpdateConfig(ctx context.Context, instanceID string, params *UpdateConfigParams) (*serialize.BillingConfigResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	instance, err := s.instanceRepo.QueryByID(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if instance == nil {
		return nil, apierror.ResourceNotFound()
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		colsToUpdate := set.New[string]()

		if params.CustomerTypes != nil {
			if err := s.updateCustomerTypes(ctx, tx, instance, *params.CustomerTypes); err != nil {
				return true, err
			}
			colsToUpdate.Insert(sqbmodel.InstanceColumns.BillingCustomerTypes)
		}

		if params.PortalEnabled != nil && *params.PortalEnabled != instance.BillingPortalEnabled.Bool {
			if err := s.updatePortalEnabled(ctx, instance, *params.PortalEnabled); err != nil {
				return true, err
			}
			colsToUpdate.Insert(sqbmodel.InstanceColumns.BillingPortalEnabled)
		}

		if !colsToUpdate.IsEmpty() {
			if err := s.instanceRepo.Update(ctx, tx, instance, colsToUpdate.Array()...); err != nil {
				return true, err
			}
		}

		if instance.HasBillingEnabledForOrganizations() && env.AuthConfig.IsOrganizationsEnabled() {
			if err := s.ensureOrganizationPermissions(ctx, instance); err != nil {
				return true, err
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

	return serialize.BillingConfig(instance), nil
}

func (s *Service) createPlan(ctx context.Context, instance *model.Instance, params *CreatePlanParams, isInitial bool) (*serialize.BillingPlanResponse, apierror.Error) {
	plan := &model.BillingPlan{
		BillingPlan: &sqbmodel.BillingPlan{
			Name:         params.Name,
			Key:          params.Key,
			Description:  null.StringFromPtr(params.Description),
			CustomerType: params.CustomerType,
			PriceInCents: params.PriceInCents,
			Features:     params.Features,
			InstanceID:   instance.ID,
			InitialPlan:  isInitial,
		},
	}

	apiErr := validatePlan(plan)
	if apiErr != nil {
		return nil, apiErr
	}

	billingConnector := s.billingConnector.Connect(instance.ExternalBillingAccountID.String)

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		productParams := billing.CreateProductParams{
			Name:        plan.Name,
			Description: plan.Description.Ptr(),
		}
		stripeProductID, err := billingConnector.CreateProduct(ctx, productParams)
		if err != nil {
			return true, err
		}
		plan.StripeProductID = null.StringFrom(stripeProductID)

		priceParams := billing.CreatePriceParams{
			ProductID:         stripeProductID,
			UnitAmount:        params.PriceInCents,
			Currency:          string(stripe.CurrencyUSD),
			TaxBehavior:       string(stripe.PriceTaxBehaviorExclusive),
			RecurringInterval: null.StringFrom(string(stripe.PriceRecurringIntervalMonth)).Ptr(),
		}
		stripePriceID, err := billingConnector.CreatePrice(ctx, priceParams)
		if err != nil {
			return true, err
		}
		plan.StripePriceID = null.StringFrom(stripePriceID)

		err = s.billingPlanRepo.Insert(ctx, tx, plan)
		return err != nil, err
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueBillingPlanInstanceIDKey) {
			return nil, apierror.FormInvalidParameterFormat("key", "Key already exists.")
		}
		return nil, apierror.Unexpected(txErr)
	}
	return serialize.BillingPlan(plan), nil
}

// TODO: When out of PoC those should be moved along with other system permissions
// Currently we don't want every instance to have those permissions, unless they have
// the billing feature enabled.
func (s *Service) ensureOrganizationPermissions(ctx context.Context, instance *model.Instance) error {
	permissions := []struct {
		Name        string
		Key         string
		Description string
	}{
		{Name: "Read billing", Key: constants.PermissionBillingRead, Description: "Permission to view billing."},
		{Name: "Manage billing", Key: constants.PermissionBillingManage, Description: "Permission to manage billing."},
	}

	systemPerms, err := s.permissionRepo.FindAllSystemByInstance(ctx, s.db, instance.ID)
	if err != nil {
		return err
	}

	systemPermsMap := make(map[string]*model.Permission)
	for _, perm := range systemPerms {
		systemPermsMap[perm.Key] = perm
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		authConfig, err := s.authConfigRepo.FindByInstanceActiveAuthConfigID(ctx, tx, instance.ID)
		if err != nil {
			return true, err
		}

		role, err := s.roleRepo.FindByKeyAndInstance(ctx, tx, authConfig.OrganizationSettings.CreatorRole, instance.ID)
		if err != nil {
			return true, err
		}

		for _, permission := range permissions {
			if _, ok := systemPermsMap[permission.Key]; ok {
				continue
			}

			newPermission := &model.Permission{
				Permission: &sqbmodel.Permission{
					InstanceID:  instance.ID,
					Name:        permission.Name,
					Key:         permission.Key,
					Description: permission.Description,
					Type:        string(constants.RTSystem),
				},
			}

			if err := s.permissionRepo.Insert(ctx, tx, newPermission); err != nil {
				return true, err
			}

			if err := s.assignPermissionToRole(ctx, tx, instance, role, newPermission); err != nil {
				return true, err
			}
		}

		return false, nil
	})
	return txErr
}

func (s *Service) hasBillingPortalEnabled(ctx context.Context, instance *model.Instance) (bool, error) {
	configurations, err := s.billingConnector.
		Connect(instance.ExternalBillingAccountID.String).
		CustomerPortalConfigurations(ctx)
	if err != nil {
		return false, err
	}
	return len(configurations) > 0, nil
}

func (s *Service) updateCustomerTypes(ctx context.Context, tx database.Tx, instance *model.Instance, customerTypes []string) error {
	instance.BillingCustomerTypes = customerTypes

	for _, customerType := range instance.BillingCustomerTypes {
		plans, err := s.billingPlanRepo.FindAllByInstanceAndCustomerType(ctx, tx, instance.ID, customerType)
		if err != nil {
			return err
		}

		if plans.HasInitial() {
			continue
		}

		if err := s.createFirstFreePlan(ctx, instance, customerType); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) updatePortalEnabled(ctx context.Context, instance *model.Instance, value bool) error {
	if instance.BillingPortalEnabled.Bool && !value {
		return apierror.FormInvalidParameterFormat("portal_enabled", "Cannot disable billing portal once enabled.")
	}

	_, err := s.billingConnector.
		Connect(instance.ExternalBillingAccountID.String).
		CreateCustomerPortalConfiguration(ctx)
	if err != nil {
		return err
	}

	instance.BillingPortalEnabled = null.BoolFrom(value)
	return nil
}

func (s *Service) assignPermissionToRole(ctx context.Context, tx database.Tx, instance *model.Instance, role *model.Role, permission *model.Permission) error {
	// avoid duplicate role-permission assignments
	_, err := s.rolePermissionRepo.DeleteByRoleAndPermission(ctx, tx, role.ID, permission.ID)
	if err != nil {
		return err
	}

	return s.rolePermissionRepo.Insert(ctx, tx, &model.RolePermission{
		RolePermission: &sqbmodel.RolePermission{
			InstanceID:   instance.ID,
			RoleID:       role.ID,
			PermissionID: permission.ID,
		},
	})
}
