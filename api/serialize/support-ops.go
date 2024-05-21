package serialize

import (
	"clerk/model/sqbmodel_extensions"
	"time"

	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/types"
)

type SupportOpsDomain struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	DNSSuccessful bool      `json:"dns_successful"`
}

type SupportOpsInstance struct {
	ID                     string            `json:"id"`
	EnvironmentType        string            `json:"environment_type"`
	ApplicationID          string            `json:"application_id"`
	ActiveDomainID         string            `json:"active_domain_id"`
	ActiveAuthConfigID     string            `json:"active_auth_config_id"`
	ActiveDisplayConfigID  string            `json:"active_display_config_id"`
	CreatedAt              time.Time         `json:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at"`
	HomeOrigin             null.String       `json:"home_origin,omitempty"`
	SvixAppID              null.String       `json:"svix_app_id,omitempty"`
	AppleAppID             null.String       `json:"apple_app_id,omitempty"`
	AndroidTarget          types.JSON        `json:"android_target"`
	SessionTokenTemplateID null.String       `json:"session_token_template_id,omitempty"`
	AllowedOrigins         types.StringArray `json:"allowed_origins,omitempty"`
	MinClerkjsVersion      null.String       `json:"min_clerkjs_version,omitempty"`
	MaxClerkjsVersion      null.String       `json:"max_clerkjs_version,omitempty"`
	AnalyticsWentLiveAt    null.Time         `json:"analytics_went_live_at,omitempty"`
	APIVersion             string            `json:"api_version"`

	Domain *SupportOpsDomain `json:"domain"`
}

type SupportOpsSubscriptionPlan struct {
	ID                          string                              `json:"id"`
	Title                       string                              `json:"title"`
	MonthlyUserLimit            int                                 `json:"monthly_user_limit"`
	CreatedAt                   time.Time                           `json:"created_at"`
	UpdatedAt                   time.Time                           `json:"updated_at"`
	DescriptionHTML             null.String                         `json:"description_html,omitempty"`
	Visible                     bool                                `json:"visible"`
	StripeProductID             null.String                         `json:"stripe_product_id,omitempty"`
	Features                    sqbmodel_extensions.StringJSONArray `json:"features"`
	BasePlan                    null.String                         `json:"base_plan,omitempty"`
	OrganizationMembershipLimit int                                 `json:"organization_membership_limit"`
	MonthlyOrganizationLimit    int                                 `json:"monthly_organization_limit"`
	Scope                       string                              `json:"scope"`
	VisibleToApplicationIds     sqbmodel_extensions.StringJSONArray `json:"visible_to_application_ids"`
	Addons                      sqbmodel_extensions.StringJSONArray `json:"addons"`
	IsAddon                     bool                                `json:"is_addon"`
}

type SupportOpsApplication struct {
	ID                   string      `json:"id"`
	Name                 string      `json:"name"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
	Type                 string      `json:"type"`
	LogoPublicURL        null.String `json:"logo_public_url"`
	FaviconPublicURL     null.String `json:"favicon_public_url"`
	CreatorID            null.String `json:"creator_id"`
	AccountPortalAllowed bool        `json:"account_portal_allowed"`
	ExceededMausTimes    int         `json:"exceeded_maus_times"`
	Demo                 bool        `json:"demo"`
	HardDeleteAt         null.Time   `json:"hard_delete_at"`

	Instances []*SupportOpsInstance         `json:"instances"`
	Plans     []*SupportOpsSubscriptionPlan `json:"plans"`
}

type SupportOpsCustomerDataResponse struct {
	Applications []*SupportOpsApplication `json:"applications"`
}
