package apierror

type duplicateInvitationEmails struct {
	EmailAddresses []string `json:"email_addresses"`
}

type identifiersMeta struct {
	Identifiers []string `json:"identifiers"`
}

type blockedCountryMeta struct {
	Alpha2      string `json:"alpha2"`
	CountryCode string `json:"country_code"`
}

type unsupportedSubscriptionParams struct {
	UnsupportedFeatures []string `json:"unsupported_features"`
}

type formParameter struct {
	Name string `json:"param_name"`
}

type formInvalidEmailAddresses struct {
	formParameter
	EmailAddresses []string `json:"email_addresses"`
}

type missingPermissions struct {
	Permissions []string `json:"permissions"`
}

type missingParameters struct {
	Names []string `json:"param_names"`
}

type passwordNotStrongEnoughParams struct {
	formParameter
	ZXCVBN suggestionsParams `json:"zxcvbn"`
}

type sessionMeta struct {
	SessionID string `json:"session_id"`
}

type ZXCVBNSuggestion struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type suggestionsParams struct {
	Suggestions []ZXCVBNSuggestion `json:"suggestions"`
}

type userLockoutMeta struct {
	LockoutExpiresInSeconds int64 `json:"lockout_expires_in_seconds"`
}

type oauthTokenWalletMeta struct {
	ProviderError string `json:"provider_error"`
}

type devLimits struct {
	DevMonthlySMSLimit int `json:"dev_monthly_sms_limit"`
}
