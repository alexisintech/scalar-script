package restrictions

import (
	"context"
	"errors"
	"fmt"

	"clerk/api/shared/emailquality"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/emailaddress"
	"clerk/pkg/sentry"
	usersettingsmodel "clerk/pkg/usersettings/model"
	"clerk/repository"
	"clerk/utils/database"
)

type Service struct {
	allowlistRepo       *repository.Allowlist
	blocklistRepo       *repository.Blocklist
	identificationRepo  *repository.Identification
	emailQualityChecker *emailquality.EmailQuality
}

func NewService(emailQualityChecker *emailquality.EmailQuality) *Service {
	return &Service{
		allowlistRepo:       repository.NewAllowlist(),
		blocklistRepo:       repository.NewBlocklist(),
		identificationRepo:  repository.NewIdentification(),
		emailQualityChecker: emailQualityChecker,
	}
}

// CheckResult holds the result of a restrictions check for an Identifier.
// Note that the identifier cannot be both Allowed and Blocked at the same
// time.
type CheckResult struct {
	Identifier string
	Allowed    bool
	Blocked    bool
}

// Settings is the configuration settings for restricting identifiers.
type Settings struct {
	usersettingsmodel.Restrictions
	TestMode bool
}

// Identification is the subject of a restriction check. Its value is the
// Identifier, Canonical Identifier and the Type can be email address, phone number, username, etc.
type Identification struct {
	Identifier          string
	CanonicalIdentifier string
	Type                string
}

// Check determines if the provided identifier is allowed to access the
// instance specified by instanceID or the identifier is blocked for the
// instance.
// The check respects the provided restrictionSettings, in the following
// order:
//  1. block email subaddresses
//  2. ignore dots for Gmail addresses
//  3. allowlist
//  4. blocklist
//  5. Check disposable email addresses
func (s *Service) Check(
	ctx context.Context,
	exec database.Executor,
	identification Identification,
	restrictionSettings Settings,
	instanceID string,
) (CheckResult, error) {
	res := CheckResult{
		Identifier: identification.Identifier,
		Allowed:    true,
	}
	var err error
	if identification.Identifier == "" {
		return res, nil
	}

	// Email subaddressing block takes precendence over Gmail dot trick, allowlist and blocklist.
	if restrictionSettings.BlockEmailSubaddresses.Enabled && isRestrictedSubaddress(identification, restrictionSettings.TestMode) {
		res.Blocked = true
		res.Allowed = !res.Blocked
		return res, nil
	}

	// Ignore dots for Gmail addresses takes precendence over allowlist and blocklist.
	if cenv.IsEnabled(cenv.FlagAllowIgnoreDotsForGmailAddresses) && restrictionSettings.IgnoreDotsForGmailAddresses.Enabled && emailaddress.CanBenefitFromDotTrick(identification.Identifier) {
		exists, err := s.identificationRepo.ExistsVerifiedOrReservedByCanonicalIdentifierAndType(ctx, exec, identification.CanonicalIdentifier, constants.ITEmailAddress, instanceID)
		if err != nil {
			return res, err
		}

		res.Blocked = exists
		res.Allowed = !res.Blocked

		if res.Blocked {
			return res, nil
		}
	}

	// Allowlist takes precedence over blocklist.
	if restrictionSettings.Allowlist.Enabled {
		res.Allowed, err = checkIdentifierExists(identification.Identifier, func(identifier string) (bool, error) {
			return s.allowlistRepo.ExistsByInstanceAndIdentifier(ctx, exec, instanceID, identifier)
		})
		if err != nil {
			return res, err
		}
		return res, nil
	}

	// Blocklist takes precedence over disposable email addresses.
	if restrictionSettings.Blocklist.Enabled {
		res.Blocked, err = checkBlockedIdentifierExists(identification, func(identifier string) (bool, error) {
			return s.blocklistRepo.ExistsByInstanceAndIdentifier(ctx, exec, instanceID, identifier)
		})
		if err != nil {
			return res, err
		}
		res.Allowed = !res.Blocked

		if res.Blocked {
			return res, nil
		}
	}

	if !restrictionSettings.BlockDisposableEmailDomains.Enabled {
		// No further checks required
		return res, nil
	}

	// Now, Check disposable email addresses.
	qualityReport, err := s.emailQualityChecker.CheckQuality(ctx, identification.Identifier)
	if err != nil {
		var apierr emailquality.APIError
		if errors.As(err, &apierr) {
			// fail open, i.e. don't block
			sentry.CaptureException(ctx, err)
		} else {
			return res, err
		}
	} else if qualityReport.Disposable {
		res.Blocked = true
		res.Allowed = false
	}

	return res, nil
}

// CheckAllResult holds all allowed and blocked entries after a CheckAll
// execution.
type CheckAllResult struct {
	offenders []string
	allowed   []string
	blocked   []string
}

// HasAtLeastOneAllowed returns true if at least one allowed entry exists.
func (r *CheckAllResult) HasAtLeastOneAllowed() bool {
	return len(r.allowed) > 0
}

// HasAtLeastOneBlocked returns true if at least one blocked entry exists.
func (r *CheckAllResult) HasAtLeastOneBlocked() bool {
	return len(r.blocked) > 0
}

// Offenders returns a list of identifiers that exist in the blocklist and don't exist in the allowlist.
func (r *CheckAllResult) Offenders() []string {
	return r.offenders
}

// Blocked returns a list of the identifier offenders due to blocklist.
func (r *CheckAllResult) Blocked() []string {
	return r.blocked
}

// CheckAll performs checks to determine which of the provided identifiers are
// allowed to access or blocked from accessing the instance specified by
// instanceID.
// The check respects the provided restrictionSettings, in the following
// order:
//  1. block email subaddresses
//  2. allowlist
//  3. blocklist
func (s *Service) CheckAll(
	ctx context.Context,
	exec database.Executor,
	identifications []Identification,
	settings Settings,
	instanceID string,
) (CheckAllResult, error) {
	res := CheckAllResult{}

	for _, identification := range identifications {
		checkSingle, err := s.Check(ctx, exec, identification, settings, instanceID)
		if err != nil {
			return res, err
		}

		if checkSingle.Blocked || !checkSingle.Allowed {
			res.offenders = append(res.offenders, identification.Identifier)
		}

		if checkSingle.Allowed {
			res.allowed = append(res.allowed, identification.Identifier)
		} else if checkSingle.Blocked {
			res.blocked = append(res.blocked, identification.Identifier)
		}
	}
	return res, nil
}

func checkIdentifierExists(identifier string, checkIdentifierExists func(string) (bool, error)) (bool, error) {
	identifierExists, err := checkIdentifierExists(identifier)
	if err != nil {
		return false, err
	}

	if identifierExists {
		return true, nil
	}

	emailDomain := emailaddress.Domain(identifier)
	if emailDomain == "" {
		return false, nil
	}

	allEmailsFromDomain := fmt.Sprintf("*@%s", emailDomain)
	domainExists, err := checkIdentifierExists(allEmailsFromDomain)
	if err != nil {
		return false, err
	}

	return domainExists, nil
}

func checkBlockedIdentifierExists(
	identification Identification,
	queryIdentifierExists func(string) (bool, error),
) (bool, error) {
	identifierExists, err := checkIdentifierExists(identification.Identifier, queryIdentifierExists)
	if err != nil {
		return false, err
	}

	if identifierExists {
		return true, nil
	}

	if !emailaddress.ContainsSubaddress(identification.Identifier) || !cenv.GetBool(cenv.FlagBlockSubaddressesInBlocklist) {
		return false, nil
	}

	identifier := emailaddress.RemoveSubaddress(identification.Identifier)

	return queryIdentifierExists(identifier)
}

// Email addresses with a local part that contains a tag are restricted,
// unless we're in test mode, where test email addresses are allowed.
// The tag is detected by the presence of the plus, equals or hash character.
func isRestrictedSubaddress(identification Identification, testMode bool) bool {
	if identification.Type != constants.ITEmailAddress {
		return false
	}
	if testMode && emailaddress.IsTest(identification.Identifier) {
		return false
	}
	return emailaddress.ContainsSubaddress(identification.Identifier)
}
