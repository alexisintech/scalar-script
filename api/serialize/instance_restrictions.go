package serialize

import "clerk/pkg/usersettings/model"

type InstanceRestrictionsResponse struct {
	Object                      string `json:"object"`
	Allowlist                   bool   `json:"allowlist"`
	Blocklist                   bool   `json:"blocklist"`
	BlockEmailSubaddresses      bool   `json:"block_email_subaddresses"`
	BlockDisposableEmailDomains bool   `json:"block_disposable_email_domains"`
	IgnoreDotsForGmailAddresses bool   `json:"ignore_dots_for_gmail_addresses"`
}

func InstanceRestrictions(userSettings model.UserSettings) *InstanceRestrictionsResponse {
	return &InstanceRestrictionsResponse{
		Object:                      "instance_restrictions",
		Allowlist:                   userSettings.Restrictions.Allowlist.Enabled,
		Blocklist:                   userSettings.Restrictions.Blocklist.Enabled,
		BlockEmailSubaddresses:      userSettings.Restrictions.BlockEmailSubaddresses.Enabled,
		BlockDisposableEmailDomains: userSettings.Restrictions.BlockDisposableEmailDomains.Enabled,
		IgnoreDotsForGmailAddresses: userSettings.Restrictions.IgnoreDotsForGmailAddresses.Enabled,
	}
}
