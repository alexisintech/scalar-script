package feature_flags

import (
	"context"

	"clerk/api/apierror"
	"clerk/pkg/cenv"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
)

type Service struct {
	db database.Database

	// repositories
	applicationRepo *repository.Applications
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:              deps.DB(),
		applicationRepo: repository.NewApplications(),
	}
}

type FeatureFlags struct {
	AllowNewPricingCheckout             bool `json:"allow_new_pricing_checkout"`
	OAuthBlockEmailSubaddresses         bool `json:"oauth_block_email_subaddresses"`
	AllowPIIUpdate                      bool `json:"allow_pii_update"`
	AllowPasskeys                       bool `json:"allow_passkeys"`
	PreventDeletionOfActiveProdInstance bool `json:"prevent_deletion_of_active_prod_instance"`
	AllowBilling                        bool `json:"allow_billing"`
	AllowGoogleOneTap                   bool `json:"allow_google_one_tap"`
	AutoRefundCanceledSubscriptions     bool `json:"auto_refund_canceled_subscriptions"`
	CustomSMSTemplateApprovalFlow       bool `json:"custom_sms_template_approval_flow"`
	EnableEmailLinkRequireSameClient    bool `json:"enable_email_link_require_same_client"`
}

func (s *Service) Read(ctx context.Context, instanceID string) (*FeatureFlags, apierror.Error) {
	application, err := s.applicationRepo.FindByInstanceID(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return &FeatureFlags{
		AllowNewPricingCheckout:             cenv.GetBool(cenv.FlagAllowNewPricingCheckout),
		OAuthBlockEmailSubaddresses:         cenv.GetBool(cenv.FlagOAuthBlockEmailSubaddresses),
		AllowPIIUpdate:                      cenv.IsBeforeCutoff(cenv.PIIProtectionEnabledCutoffEpochTime, application.CreatedAt),
		AllowPasskeys:                       cenv.ResourceHasAccess(cenv.FlagAllowPasskeysInstanceIDs, instanceID),
		PreventDeletionOfActiveProdInstance: cenv.GetBool(cenv.FlagPreventDeletionOfActiveProdInstance),
		AllowBilling:                        cenv.ResourceHasAccess(cenv.FlagAllowBillingInstanceIDs, instanceID),
		AllowGoogleOneTap:                   cenv.ResourceHasAccess(cenv.FlagAllowGoogleOneTapInstanceIDs, instanceID),
		AutoRefundCanceledSubscriptions:     cenv.GetBool(cenv.FlagAutoRefundCanceledSubscriptions),
		CustomSMSTemplateApprovalFlow:       cenv.GetBool(cenv.FlagCustomSMSTemplateApprovalFlow),
		EnableEmailLinkRequireSameClient:    cenv.GetBool(cenv.FlagEnableEmailLinkRequireSameClient),
	}, nil
}
