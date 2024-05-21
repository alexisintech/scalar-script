package apierror

// Error codes
//
// nolint:gosec
const (
	FormConditionalParamDisallowedCode             = "form_conditional_param_disallowed"
	FormConditionalParamValueDisallowedCode        = "form_conditional_param_value_disallowed"
	FormConditionalParamMissingCode                = "form_conditional_param_missing"
	FormInvalidSessionInactivityTimeoutCode        = "form_session_inactivity_timeout_invalid"
	FormParamDuplicateCode                         = "form_param_duplicate"
	FormIdentificationNeededCode                   = "form_verification_needed"
	FormIdentifierExistsCode                       = "form_identifier_exists"
	FormAlreadyExistsCode                          = "form_already_exists"
	FormIdentifierNotFoundCode                     = "form_identifier_not_found"
	FormIncorrectCodeCode                          = "form_code_incorrect"
	FormIncorrectSignatureCode                     = "form_incorrect_signature"
	FormInvalidOriginCode                          = "form_invalid_origin"
	FormInvalidTimeCode                            = "form_param_invalid_time"
	FormInvalidDateCode                            = "form_param_invalid_date"
	FormParamFormatInvalidCode                     = "form_param_format_invalid"
	FormParamValueInvalidCode                      = "form_param_value_invalid"
	FormParamValueDisabled                         = "form_param_value_disabled"
	FormPasswordValidationFailedCode               = "form_password_validation_failed"
	FormEmailAddressBlockedCode                    = "form_email_address_blocked"
	FormParamTypeInvalidCode                       = "form_param_type_invalid"
	FormParamMissingCode                           = "form_param_missing"
	FormPasswordIncorrectCode                      = "form_password_incorrect"
	FormPasswordPwnedCode                          = "form_password_pwned"
	FormPasswordDigestInvalidCode                  = "form_password_digest_invalid_code"
	FormResourceNotFoundCode                       = "form_resource_not_found"
	FormParamNilCode                               = "form_param_nil"
	FormParamUnknownCode                           = "form_param_unknown"
	FormUsernameInvalidLengthCode                  = "form_username_invalid_length"
	FormParamExceedsAllowedSizeCode                = "form_param_exceeds_allowed_size"
	FormParameterValueTooLargeCode                 = "form_param_value_too_large"
	FormParameterMaxLengthExceededCode             = "form_param_max_length_exceeded"
	FormParameterMinLengthExceededCode             = "form_param_min_length_exceeded"
	FormUsernameInvalidCharacterCode               = "form_username_invalid_character"
	FormUsernameNeedsNonNumberCharCode             = "form_username_needs_non_number_char"
	FormNotAllowedToDisableDefaultSecondFactorCode = "form_disable_default_second_factor_not_allowed"
	FormDataMissing                                = "form_data_missing"
	ClerkKeyInvalidCode                            = "clerk_key_invalid"
	AuthenticationInvalidCode                      = "authentication_invalid"
	AuthorizationInvalidCode                       = "authorization_invalid"
	ApplicationAlreadyBelongsToOrganizationCode    = "application_already_belongs_to_organization"
	ApplicationAlreadyBelongsToUserCode            = "application_already_belongs_to_user"
	DevelopmentInstanceMissingCode                 = "development_instance_missing"
	DomainUpdateForbiddenCode                      = "domain_update_forbidden"
	RequestHeaderMissingCode                       = "request_header_missing"
	OriginAndAuthorizationHeadersSetCode           = "origin_authorization_headers_conflict"
	MultipleOriginHeaderValuesCode                 = "multiple_origin_header_values_forbidden"
	MultipleAuthorizationHeaderValuesCode          = "multiple_authorization_header_values_forbidden"
	OriginInvalidCode                              = "origin_invalid"
	OriginHeaderMissingCode                        = "origin_missing"
	DevBrowserUnauthenticatedCode                  = "dev_browser_unauthenticated"
	URLBasedSessionSyncingDisabledCode             = "url_based_session_syncing_disabled"
	RequestInvalidForEnvironmentCode               = "request_invalid_for_environment"
	RequestInvalidForInstanceCode                  = "request_invalid_for_instance"
	HostInvalidCode                                = "host_invalid"
	ClientNotFoundCode                             = "client_not_found"
	CookieInvalidCode                              = "cookie_invalid"
	InvalidActionForSessionCode                    = "invalid_action_for_session"
	InvalidCSRFTokenCode                           = "csrf_token_invalid" //nolint:gosec
	SessionExistsCode                              = "session_exists"
	UnauthorizedActionForSessionCode               = "action_for_session_not_authorized"
	InvalidSessionTokenCode                        = "invalid_session_token"
	IdentifierAlreadySignedInCode                  = "identifier_already_signed_in"
	AccountTransferInvalidCode                     = "account_transfer_invalid"
	ClientStateInvalid                             = "client_state_invalid"
	StrategyForUserInvalidCode                     = "strategy_for_user_invalid"
	IdentificationClaimsCode                       = "identification_claimed"
	DeleteLinkedIdentificationDisallowedCode       = "delete_linked_identification_disallowed"
	VerificationAlreadyVerifiedCode                = "verification_already_verified"
	VerificationExpiredCode                        = "verification_expired"
	VerificationFailedCode                         = "verification_failed"
	VerificationStrategyInvalidCode                = "verification_strategy_invalid"
	VerificationMissingCode                        = "verification_missing"
	VerificationNotSentCode                        = "verification_not_sent"
	VerificationStatusUnknownCode                  = "verification_status_unknown"
	VerificationInvalidLinkTokenCode               = "verification_link_token_invalid"
	VerificationInvalidLinkTokenSourceCode         = "verification_link_token_source_invalid"
	VerificationLinkTokenExpiredCode               = "verification_link_token_expired"
	ProductionInstanceExistsCode                   = "production_instance_exists"
	InstanceTypeInvalidCode                        = "instance_type_invalid"
	InstanceNotLiveCode                            = "not_live"
	IntegrationOauthFailureCode                    = "integration_oauth_failure"
	IntegrationProvisioningFailedCode              = "integration_provisioning_failed"
	IntegrationTokenMissingCode                    = "integration_token_missing"
	IntegrationUserInfoErrorCode                   = "integration_user_info_error"
	RequestBodyInvalidCode                         = "request_body_invalid"
	EmailAddressExistsCode                         = "email_address_exists"
	PhoneNumberExistsCode                          = "phone_number_exists"
	UsernameExistsCode                             = "username_exists"
	ExternalAccountExistsCode                      = "external_account_exists"
	IdentificationDeletionFailedCode               = "identification_deletion_failed"
	LastRequiredIdentificationDeletionFailedCode   = "last_required_identification_deletion_failed"
	IdentificationSetFor2FAFailedCode              = "identification_update_failed"
	MissingQueryParameterCode                      = "missing_query_parameter"
	InvalidQueryParameterValueCode                 = "invalid_query_parameter_value"
	MalformedPublishableKeyCode                    = "malformed_publishable_key"
	SvixAppCreateErrorCode                         = "svix_app_create_error"
	SvixAppExistsCode                              = "svix_app_exists"
	SvixAppMissingCode                             = "svix_app_missing"
	SignedOutCode                                  = "signed_out"
	UnsupportedIntegrationTypeCode                 = "unsupported_integration_type"
	AuthorizationHeaderFormatInvalidCode           = "authorization_header_format_invalid"
	InvalidPlan                                    = "invalid_plan"
	BillingAccountNotAccessibleCode                = "billing_account_not_accessible"
	BillingAccountWithoutCustomerIDCode            = "billing_account_without_customer_id"
	IdentificationUpdateSecondFactorUnverified     = "identification_update_second_factor_unverified"
	IdentificationCreateSecondFactorUnverified     = "identification_create_second_factor_unverified"
	CheckoutLockedCode                             = "checkout_locked"
	CheckoutSessionMismatchCode                    = "checkout_session_mismatch"
	UnsupportedSubscriptionPlanFeaturesCode        = "unsupported_subscription_plan_features"
	InvalidSubscriptionPlanSwitchCode              = "invalid_subscription_plan_switch_code"
	ProductAlreadySubscribedCode                   = "product_already_subscribed"
	ProductNotSupportedBySubscriptionPlanCode      = "product_not_supported_by_subscription_plan"
	InactiveSubscriptionCode                       = "inactive_subscription"
	MissingSessionLifetimeSettingCode              = "session_lifetime_setting_missing"
	SessionCreationNotAllowedCode                  = "session_creation_not_allowed"
	NoSecondFactorsForStrategyCode                 = "no_second_factors"
	UnsupportedContentTypeCode                     = "unsupported_content_type"
	MalformedRequestParametersCode                 = "malformed_request_parameters"
	InvalidUserSettingsCode                        = "user_settings_invalid"
	ImageTooLargeCode                              = "image_too_large"
	ImageNotFoundCode                              = "image_not_found"
	OperationNotAllowedOnSatelliteDomainCode       = "operation_not_allowed_on_satellite_domain"
	OperationNotAllowedOnPrimaryDomainCode         = "operation_not_allowed_on_primary_domain"
	ProxyRequestMissingSecretKeyCode               = "proxy_request_missing_secret_key"
	ProxyRequestInvalidSecretKeyCode               = "proxy_request_invalid_secret_key"
	SyncNonceAlreadyConsumedCode                   = "sync_nonce_already_consumed"
	PrimaryDomainAlreadyExistsCode                 = "primary_domain_already_exists"
	InvalidProxyConfigurationCode                  = "invalid_proxy_configuration"

	FormPasswordLengthTooShortCode      = "form_password_length_too_short"
	FormPasswordLengthTooLongCode       = "form_password_length_too_long"
	FormPasswordNoLowercaseCode         = "form_password_no_lowercase"
	FormPasswordNoUppercaseCode         = "form_password_no_uppercase"
	FormPasswordNoNumberCode            = "form_password_no_number"
	FormPasswordNoSpecialCharCode       = "form_password_no_special_char"
	FormPasswordNotStrongEnoughCode     = "form_password_not_strong_enough"
	FormPasswordSizeInBytesExceededCode = "form_password_size_in_bytes_exceeded"

	InternalClerkErrorCode = "internal_clerk_error"
	ResourceNotFoundCode   = "resource_not_found"
	ResourceForbiddenCode  = "resource_forbidden"
	ResourceInvalidCode    = "resource_invalid"
	// TODO: typo of "mismatch" will be fixed in FAPI v2 to avoid breaking customers [AUTH-291]
	ResourceMismatchCode = "resource_missmatch"

	DuplicateRecordCode            = "duplicate_record"
	IdentifierNotAllowedAccessCode = "not_allowed_access"
	BlockedCountryCode             = "blocked_country_code"

	MaintenanceModeCode = "maintenance_mode"

	// Backoffice
	CannotSetUnlimitedSeatsForUserApplicationCode = "cannot_set_unlimited_seats_for_user"
	CannotUnsetUnlimitedSeatsForOrganizationCode  = "cannot_unset_unlimited_seats_for_organization"
	CannotUpdateUserLimitsOnProductionCode        = "cannot_update_user_limits_on_production"
	CannotUpdateGivenDomainCode                   = "cannot_update_given_domain"
	EmailDomainNotFoundCode                       = "email_domain_not_found"

	// Billing
	NoBillingAccountConnectedCode              = "no_billing_account_connected"
	BillingCheckoutSessionNotFoundCode         = "billing_checkout_session_not_found"
	BillingCheckoutSessionAlreadyProcessedCode = "billing_checkout_session_already_processed"
	BillingCheckoutSessionNotCompletedCode     = "billing_checkout_session_not_completed"
	BillingPlanAlreadyActiveCode               = "billing_plan_already_active"

	// Pricing
	PricingPlanAlreadyExistsCode = "pricing_plan_already_exists"

	TemplateTypeUnsupportedCode      = "template_type_unsupported"
	TemplateDeletionRestrictedCode   = "template_deletion_restricted"
	TemplateRevertRestrictedCode     = "template_revert_error"
	CustomTemplateRequiredCode       = "custom_template_required"
	CustomTemplatesNotAvailableCode  = "custom_templates_not_available"
	RequiredVariableMissingCode      = "required_variable_missing"
	InvalidTemplateBodyCode          = "invalid_template_body"
	SMSTemplateMaxLengthExceededCode = "sms_max_length_exceeded"
	DevMonthlySMSLimitExceededCode   = "dev_monthly_sms_limit_exceeded"

	InvitationsNotSupportedInInstanceCode = "invitations_not_supported"
	InvitationAccountAlreadyExistsCode    = "invitation_account_exists"
	InvitationIdentificationNotExistCode  = "invitation_account_not_exists"
	InvitationAlreadyAcceptedCode         = "invitation_already_accepted"
	InvitationAlreadyRevokedCode          = "invitation_already_revoked"

	RevokedInvitationCode                     = "revoked_invitation"
	EnhancedEmailDeliverabilityProhibitedCode = "enhanced_email_deliverability_prohibited"
	InvalidCaptchaWidgetTypeCode              = "invalid_captcha_widget_type"

	InstanceKeyRequiredCode = "instance_key_required"

	LastInstanceKeyCode                         = "last_instance_key"
	TooManyRequestsCode                         = "too_many_requests"
	QuotaExceededCode                           = "quota_exceeded"
	BadRequestCode                              = "bad_request"
	ConflictCode                                = "conflict"
	OrganizationQuotaExceededCode               = "organization_quota_exceeded"
	OrganizationDomainQuotaExceededCode         = "organization_domain_quota_exceeded"
	OrganizationMembershipQuotaExceededCode     = "organization_membership_quota_exceeded"
	OrganizationMembershipPlanQuotaExceededCode = "organization_membership_plan_quota_exceeded"
	OrganizationAdminDeleteNotEnabledCode       = "organization_admin_delete_not_enabled"
	UserQuotaExceededCode                       = "user_quota_exceeded"
	TooManyUnverifiedIdentificationsCode        = "too_many_unverified_identifications"
	PrimaryIdentificationNotFoundCode           = "primary_identification_not_found"
	UpdatingUserPasswordDeprecatedCode          = "updating_user_password_deprecated"

	ActiveApplicationDeletionNotAllowedCode         = "active_app_deletion_not_allowed"
	ActiveProductionInstanceDeletionNotAllowedCode  = "active_production_instance_deletion_not_allowed"
	TransferPaidAppToFreeAccountCode                = "transfer_paid_app_to_free_account"
	TransferPaidAppToAccountWithNoPaymentMethodCode = "transfer_paid_app_to_account_no_payment_method"

	BreaksInstanceInvariantCode = "breaks_instance_invariant"

	AlreadyAMemberInOrganizationCode                      = "already_a_member_in_organization"
	NotAMemberInOrganizationCode                          = "not_a_member_in_organization"
	NotAnAdminInOrganizationCode                          = "not_an_admin_in_organization"
	OrganizationInvitationNotPendingCode                  = "organization_invitation_not_pending"
	OrganizationInvitationNotFoundCode                    = "organization_invitation_not_found"
	OrganizationCreatorNotFoundCode                       = "organization_creator_not_found"
	OrganizationInvitationRevokedCode                     = "organization_invitation_revoked_code"
	OrganizationInvitationAlreadyAcceptedCode             = "organization_invitation_already_accepted"
	OrganizationInvitationIdentificationNotExistCode      = "organization_invitation_identification_not_exist"
	OrganizationInvitationIdentificationAlreadyExistsCode = "organization_invitation_identification_already_exists"
	OrganizationInvitationNotUniqueCode                   = "organization_invitation_not_unique"
	OrganizationSuggestionAlreadyAcceptedCode             = "organization_suggestion_already_accepted"
	OrganizationNotEnabledInInstanceCode                  = "organization_not_enabled_in_instance"
	OrganizationInvitationToDeletedOrganizationCode       = "organization_invitation_to_deleted_organization"
	OrganizationDomainMismatchCode                        = "organization_domain_mismatch"
	OrganizationUnlimitedMembershipsRequiredCode          = "organization_unlimited_membership_required"
	OrganizationDomainCommonCode                          = "organization_domain_common"
	OrganizationDomainBlockedCode                         = "organization_domain_blocked"
	OrganizationDomainAlreadyExistsCode                   = "organization_domain_already_exists"
	OrganizationDomainsNotEnabledCode                     = "organization_domains_not_enabled"
	OrganizationDomainEnrollmentModeNotEnabledCode        = "organization_domain_enrollment_mode_not_enabled"
	MissingOrganizationPermissionCode                     = "missing_organization_permission"
	OrganizationRoleUsedAsDefaultCreatorRoleCode          = "organization_role_default_creator_role"
	OrganizationRoleUsedAsDomainDefaultRoleCode           = "organization_role_domain_default_role"
	OrganizationRoleAssignedToMembersCode                 = "organization_role_assigned_members"
	OrganizationRoleExistsInInvitationsCode               = "organization_role_exists_in_invitations"
	OrganizationMinimumPermissionsNeededCode              = "organzation_minimum_permissions_needed"
	OrganizationMissingCreatorRolePermissionsCode         = "organization_missing_creator_role_permissions"
	OrganizationSystemPermissionNotModifiableCode         = "organization_system_permission_not_modifiable"
	OrganizationRolePermissionAssociationExistsCode       = "organization_role_permission_association_exists"
	OrganizationRolePermissionAssociationNotFoundCode     = "organization_role_permission_association_not_found"
	OrganizationInstanceRolesQuotaExceededCode            = "organization_instance_roles_quota_exceeded"
	OrganizationInstancePermissionsQuotaExceededCode      = "organization_instance_permissions_quota_exceeded"

	FeatureNotEnabledCode     = "feature_not_enabled"
	FeatureNotImplementedCode = "feature_not_implemented"
	FeatureRequiresPSUCode    = "feature_requires_progressive_sign_up"

	SignInNoIdentificationForUserCode     = "sign_in_no_identification_for_user"
	SignInIdentificationOrUserDeletedCode = "sign_in_identification_or_user_deleted"
	SignInEmailLinkNotSameClientCode      = "sign_in_email_link_not_same_client"

	SignInTokenRevokedCode         = "sign_in_token_revoked_code"
	SignInTokenAlreadyUsedCode     = "sign_in_token_already_used_code"
	SignInTokenCannotBeUsedCode    = "sign_in_token_cannot_be_used_code"
	SignInTokenNotInSignInCode     = "sign_in_token_not_in_sign_in_code"
	SignInTokenCannotBeRevokedCode = "sign_in_token_cannot_be_revoked_code"

	ActorTokenRevokedCode         = "actor_token_revoked_code"
	ActorTokenAlreadyUsedCode     = "actor_token_already_used_code"
	ActorTokenCannotBeUsedCode    = "actor_token_cannot_be_used_code"
	ActorTokenNotInSignInCode     = "actor_token_not_in_sign_in_code"
	ActorTokenSubjectNotFoundCode = "actor_token_subject_not_found"
	ActorTokenCannotBeRevokedCode = "actor_token_cannot_be_revoked_code"

	TicketExpiredCode = "ticket_expired_code"
	TicketInvalidCode = "ticket_invalid_code"

	GatewayTimeoutCode = "gateway_timeout"

	ReservedDomainCode     = "reserved_domain"
	KnownHostingDomainCode = "known_hosting_domain"
	ReservedSubdomainCode  = "reserved_subdomain"
	HomeURLTakenCode       = "home_url_taken"

	InvalidRedirectURLCode = "invalid_redirect_url"

	TOTPAlreadyEnabledCode      = "totp_already_enabled"
	BackupCodesNotAvailableCode = "backup_codes_not_available"

	PasswordRequiredCode  = "password_required"
	NoPasswordSetCode     = "no_password_set"
	IncorrectPasswordCode = "incorrect_password"

	// verify TOTP (BAPI)
	TOTPDisabledCode      = "totp_disabled"
	IncorrectTOTPCode     = "totp_incorrect_code"
	InvalidLengthTOTPCode = "totp_invalid_length"

	SignUpCannotBeUpdatedCode        = "sign_up_cannot_be_updated"
	SignUpOutdatedVerificationCode   = "sign_up_outdated_verification"
	SignUpEmailLinkNotSameClientCode = "sign_up_email_link_not_same_client"

	BulkSizeExceededCode = "bulk_size_exceeded"

	CaptchaInvalidCode          = "captcha_invalid"
	CaptchaNotEnabledCode       = "captcha_not_enabled"
	CaptchaNotSupportedByClient = "captcha_not_supported_by_client"

	// user and org actions settings
	UserDeleteSelfNotEnabledCode         = "user_delete_self_not_enabled"
	UserCreateOrganizationNotEnabledCode = "user_create_organization_not_enabled"

	InfiniteRedirectLoopCode = "infinite_redirect_loop"

	UserLockedCode = "user_locked"

	// entitlements
	FormInvalidEntitlementKeyCode    = "form_param_invalid_entitlement_key"
	EntitlementAlreadyAssociatedCode = "entitlement_already_associated"

	// handshake
	InvalidHandshakeCode = "invalid_handshake"

	CannotDetectIPCode = "cannot_detect_ip"

	// passkeys
	PasskeyRegistrationFailureCode        = "passkey_registration_failure"
	NoPasskeysFoundForUserCode            = "no_passkeys_found_for_user"
	PasskeyNotRegisteredCode              = "passkey_not_registered"
	PasskeyIdentificationNotVerifiedCode  = "passkey_identification_not_verified"
	PasskeyInvalidPublicKeyCredentialCode = "passkey_invalid_public_key_credential"
	PasskeyInvalidVerificationCode        = "passkey_invalid_verification"
	PasskeyAuthenticationFailureCode      = "passkey_authentication_failure"
	PasskeyQuotaExceededCode              = "passkey_quota_exceeded"
)

// JWT Templates
const (
	JWTTemplateReservedClaimCode         = "jwt_template_reserved_claim"
	SessionTokenTemplateNotDeletableCode = "session_token_jwt_template"
)

// OAuth related
//
// nolint:gosec
const (
	OAuthUnsupportedProviderCode        = "oauth_unsupported_provider"
	OauthNonAuthenticatableProviderCode = "oauth_non_authenticatable_provider"

	// FAPI
	ExternalAccountNotFoundCode                         = "external_account_not_found"
	ExternalAccountEmailAddressVerificationRequiredCode = "external_account_email_address_verification_required"
	ExternalAccountMissingRefreshTokenCode              = "external_account_missing_refresh_token"
	OAuthAccountAlreadyConnectedCode                    = "oauth_account_already_connected"
	OAuthCallbackInvalidCode                            = "oauth_callback_invalid"
	OAuthConfigMissingCode                              = "oauth_config_missing"
	OAuthFetchUserErrorCode                             = "oauth_fetch_user_error"
	OAuthFetchUserForbiddenErrorCode                    = "oauth_fetch_user_forbidden_error"
	OAuthIdentificationClaimedCode                      = "oauth_identification_claimed"
	OAuthMisconfiguredProviderCode                      = "misconfigured_oauth_provider"
	OAuthSharedCredentialsNotSupportedCode              = "oauth_shared_credentials_not_supported"
	OAuthProviderNotEnabledCode                         = "oauth_provider_not_enabled"
	OAuthTokenExchangeErrorCode                         = "oauth_token_exchange_error"
	OAuthAccessDeniedCode                               = "oauth_access_denied"
	OAuthRedirectURIMismatch                            = "redirect_uri_mismatch"

	// BAPI
	OAuthMissingRefreshTokenCode     = "oauth_missing_refresh_token"
	OAuthMissingAccessTokenCode      = "oauth_missing_access_token"
	OAuthTokenProviderNotEnabledCode = "oauth_token_provider_not_enabled"
	OauthTokenRetrievalErrorCode     = "oauth_token_retrieval_error"

	// DAPI
	OAuthCustomProfileMissingCode = "_custom_profile_missing"
)

// OAuth IDP related
const (
	OAuthAuthorizeRequestErrorCode = "oauth_authorize_request_error"
)

// SAML
const (
	// FAPI
	SAMLNotEnabledCode                 = "saml_connection_not_found"
	SAMLResponseInvalidCode            = "saml_response_invalid"
	SAMLResponseRelayStateMissingCode  = "saml_response_relaystate_missing"
	SAMLSignInConnectionMissingCode    = "saml_sign_in_connection_missing"
	SAMLSignUpConnectionMissingCode    = "saml_sign_up_connection_missing"
	SAMLUserAttributeMissingCode       = "saml_user_attribute_missing"
	SAMLEmailAddressDomainMismatchCode = "saml_email_address_domain_mismatch"
	SAMLConnectionActiveNotFoundCode   = "saml_connection_active_not_found"

	// BAPI
	SAMLConnectionCantBeActivatedCode  = "saml_connection_cant_be_activated"
	SAMLFailedToFetchIDPMetadataCode   = "saml_failed_to_fetch_idp_metadata"
	SAMLFailedToParseIDPMetadataCode   = "saml_failed_to_parse_idp_metadata"
	SAMLEmailAddressDomainReservedCode = "saml_email_address_domain_reserved"
)

// Endpoint Deprecations
const (
	APIOperationDeprecatedCode = "operation_deprecated"
)

// API Versioning
const (
	APIVersionInvalidCode = "api_version_invalid"
)

// Google One Tap
// nolint:gosec
const (
	GoogleOneTapTokenInvalidCode = "google_one_tap_token_invalid"
)
