// Package jwt_template implements data-driven templates for generating
// arbitrary JWTs.
//
// A Template is essentially a JSON object which contains the claims that should
// go into the resulting token.
//
// These claims may contain special string values called shortcodes as the means
// to inject dynamic data into the token. During template execution, any
// encountered shortcodes will be substituted with their actual values.
// Shortcodes will not be substituted if they appear as JSON keys.
//
// The actual claims and other token settings come from
// [clerk/model.JWTTemplate], which this package consumes.
//
// For the available shortcodes and the feature powered by this package, refer
// to https://clerk.com/docs/request-authentication/jwt-templates.
package jwt_template

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"clerk/api/shared/jwt_template/shortcodes"
	"clerk/model"
	"clerk/pkg/jwt"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/tidwall/gjson"
	"github.com/volatiletech/sqlboiler/v4/types"
)

const (
	expressionStart = "{{"
	expressionEnd   = "}}"
)

// ErrReservedAud depicts an attempt to set the 'aud' claim to 'clerk',
// which is a reserved value for session tokens.
var ErrReservedAud = errors.New("reserved value for 'aud' claim")

// Template is the representation of a parsed template.
//
// The result of the template (i.e. JWT claims) is computed by calling Execute.
type Template struct {
	// randSrc is a random byte source used to generate the 'jti' claim.
	// It must provide at least 10 bytes, otherwise an error will occur.
	randSrc io.Reader

	clock          clockwork.Clock
	user           *model.User
	orgMemberships model.OrganizationMembershipsWithRole
	metadata       json.RawMessage

	// the duration to add from the current time when generating the exp
	// claim. If not a positive number, exp claim won't be added.
	exp time.Duration

	// the duration to subtract from the current time when generating the
	// nbf claim. If not a positive number, nbfClockSkew won't be
	// added.
	nbfClockSkew time.Duration

	// the value to be used in the 'iss' claim.
	issuer string

	// the Origin header value of the FAPI HTTP request that resulted in this
	// token.
	// This value will be included in the 'azp' claim, so the customer will have
	// the ability to also verify this value before trusting a token.
	origin string

	// mapping between shortcode keys (e.g. 'user.id') and their corresponding
	// values (e.g. 'user_123abc')
	shortcodes map[string]shortcode

	// the resulting token claims, populated after the template has been
	// executed
	result map[string]any
}

type Data struct {
	// The template that contains the claims, token lifetime and clock skew
	// settings that will go into the resulting token.
	JWTTmpl *model.JWTTemplate

	User                *model.User
	UserSettings        *usersettings.UserSettings
	OrgMemberships      model.OrganizationMembershipsWithRole
	ActiveOrgMembership *model.OrganizationMembershipWithDeps
	Issuer              string
	Origin              string
	SessionActor        json.RawMessage
}

func New(exec database.Executor, clock clockwork.Clock, data Data) (*Template, error) {
	t := &Template{
		randSrc:        rand.Reader,
		clock:          clock,
		user:           data.User,
		orgMemberships: data.OrgMemberships,
		exp:            time.Second * time.Duration(data.JWTTmpl.Lifetime),
		nbfClockSkew:   time.Second * time.Duration(data.JWTTmpl.ClockSkew),
		issuer:         data.Issuer,
		origin:         data.Origin,
	}

	err := json.Unmarshal(data.JWTTmpl.Claims, &t.result)
	if err != nil {
		return nil, err
	}

	t.metadata, err = populateTemplateMetadata(data.User, data.ActiveOrgMembership, data.SessionActor)
	if err != nil {
		return nil, err
	}

	t.populateShortcodes(exec, clock, &data)

	return t, nil
}

// Execute applies the template, substituting any variables for their actual
// values, and returns the resulting claims.
//
// Shortcodes may appear in the following forms:
//
//   - exact: for example, "{{user.id}}". The resulting type after
//     substitution will be that of the underlying field.
//
//   - coalesced: for example, "{{user.id||org.id||5}}". The resulting type
//     after substitution will be that of the first non-nil and non-false value.
//
//   - interpolation: for example, "foo {{user.id}}". The resulting type after
//     substitution will be a string.
//
//   - interpolation can be combined with coalescing, e.g.
//     "foo {{user.id||org.id}}".
//
// Also see documentation of shortcode.
func (t *Template) Execute(ctx context.Context) (map[string]any, error) {
	// apply user-provided claims
	err := t.execute(ctx, t.result)
	if err != nil {
		return nil, err
	}

	// apply default claims. Those override the user-provided ones
	aud, ok := t.result["aud"].(string)
	if ok && strings.TrimSpace(strings.ToLower(aud)) == "clerk" {
		return nil, ErrReservedAud
	}

	t.result["sub"] = t.user.ID
	t.result["iat"] = t.clock.Now().Unix()
	t.result["iss"] = t.issuer

	if t.exp > 0 {
		t.result["exp"] = t.clock.Now().Add(t.exp).Unix()
	}

	if t.nbfClockSkew > 0 {
		t.result["nbf"] = t.clock.Now().Add(-t.nbfClockSkew).Unix()
	}

	t.result["jti"], err = jwt.GenerateJTI(t.randSrc)
	if err != nil {
		return nil, err
	}

	// The Origin header may also be 'null' if it's privacy-sensitive or opaque
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Origin#description
	if t.origin != "" && t.origin != "null" {
		t.result["azp"] = t.origin
	}

	return t.result, nil
}

func (t *Template) populateShortcodes(exec database.Executor, clock clockwork.Clock, data *Data) {
	user := data.User
	activeOrgMembership := data.ActiveOrgMembership

	availableShortcodes := []shortcode{
		shortcodes.NewUserID(user),
		shortcodes.NewUserExternalID(user),
		shortcodes.NewUserFirstName(user),
		shortcodes.NewUserLastName(user),
		shortcodes.NewUserFullName(user),
		shortcodes.NewUserCreatedAt(user),
		shortcodes.NewUserUpdatedAt(user),
		shortcodes.NewUserPrimaryEmailAddress(clock, exec, user),
		shortcodes.NewUserPrimaryPhoneNumber(clock, exec, user),
		shortcodes.NewUserPrimaryWeb3Wallet(clock, exec, user),
		shortcodes.NewUserProfileImageURL(clock, user),
		shortcodes.NewUserImageURL(clock, user),
		shortcodes.NewUserHasImage(user),
		shortcodes.NewUserEmailVerified(clock, exec, user),
		shortcodes.NewUserPhoneNumberVerified(clock, exec, user),
		shortcodes.NewUserOrganizations(data.OrgMemberships),
		shortcodes.NewUserUsername(exec, clock, user),
		shortcodes.NewUserTwoFactorEnabled(exec, clock, user, data.UserSettings),
		shortcodes.NewOrgID(activeOrgMembership),
		shortcodes.NewOrgRole(activeOrgMembership),
		shortcodes.NewOrgName(activeOrgMembership),
		shortcodes.NewOrgSlug(activeOrgMembership),
		shortcodes.NewOrgImageURL(activeOrgMembership),
		shortcodes.NewOrgHasImage(activeOrgMembership),
		shortcodes.NewOrgMembershipPermissions(activeOrgMembership),
	}

	t.shortcodes = make(map[string]shortcode, len(availableShortcodes))

	for _, s := range availableShortcodes {
		code := expressionStart + s.Identifier() + expressionEnd

		if t.shortcodes[code] != nil {
			panic("jwt_template: duplicate shortcode: " + code)
		}

		t.shortcodes[code] = s
	}
}

func (t *Template) execute(ctx context.Context, data map[string]any) error {
	var err error

	for k, v := range data {
		switch e := v.(type) {
		case string:
			data[k], err = t.substituteShortcodes(ctx, e)
		case map[string]any:
			err = t.execute(ctx, e)
		case []any:
			for i, elem := range e {
				str, ok := elem.(string)
				if !ok {
					continue
				}

				e[i], err = t.substituteShortcodes(ctx, str)
				if err != nil {
					return err
				}
			}
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// substituteShortcodes tries to substitute any shortcodes encountered in s.
// If no valid shortcodes are present, s is returned as is.
//
// See documentation of type shortcode for supported syntax and capabilities of
// the templating language.
func (t *Template) substituteShortcodes(ctx context.Context, s string) (any, error) {
	v, ok, err := t.substituteNonInterpolatedShortcodes(ctx, s)
	if err != nil {
		return nil, err
	}
	if ok {
		return v, nil
	}

	return t.substituteInterpolatedShortcodes(ctx, s)
}

// substituteNonInterpolatedShortcodes tries to substitute shortcodes of the
// form "{{user.id}}" or "{{user.id||org.id}}", i.e. non-interpolated.
//
// If s contains a valid shortcode, the resulting substituted value is returned
// and the third return value will be true. Otherwise, s will be returned as is
// and the third return value will be false.
func (t *Template) substituteNonInterpolatedShortcodes(ctx context.Context, s string) (any, bool, error) {
	var v any

	v, ok, err := t.substituteExactShortcodes(ctx, s)
	if err != nil {
		return nil, false, err
	}
	if ok {
		return v, true, nil
	}

	v, ok, err = t.substituteNonInterpolatedCoalescedShortcodes(ctx, s)
	if err != nil {
		return nil, false, err
	}
	if ok {
		return v, true, nil
	}

	return s, false, nil
}

// a shortcode is considered exact if it appears in the form
// "{{<shortcode>}}", i.e. it's not part of a coalescing or interpolation
// expression.
func (t *Template) substituteExactShortcodes(ctx context.Context, s string) (any, bool, error) {
	sc, ok := t.shortcodes[s]
	if ok { // "{{user.id}}"
		v, err := sc.Substitute(ctx)
		if err != nil {
			return nil, false, err
		}
		return v, true, nil
	}

	v, ok := t.substituteExactMetadataShortcodes(s)
	if ok { // "{{user.public_metadata.foo}}"
		return v, true, nil
	}

	return s, false, nil
}

var shortcodeCoalescingMatcher = regexp.MustCompile(`\A{{[\w\d\s\.]+((\|\|\s*'[^']*'\s*)|(\|\|[\w\d\s\.]+))+}}\z`)

// Example input: "{{user.id||org.id}}"
func (t *Template) substituteNonInterpolatedCoalescedShortcodes(ctx context.Context, s string) (any, bool, error) {
	if !shortcodeCoalescingMatcher.MatchString(s) {
		return s, false, nil
	}

	s = strings.TrimPrefix(s, expressionStart)
	s = strings.TrimSuffix(s, expressionEnd)

	parts := strings.Split(s, "||")

	for i, operand := range parts {
		operand = strings.TrimSpace(operand)
		isLastOperand := i == len(parts)-1
		expr := expressionStart + strings.TrimSpace(operand) + expressionEnd

		v, ok, err := t.substituteExactShortcodes(ctx, expr)
		if err != nil {
			return nil, false, err
		}
		if ok {
			if isLastOperand || valueIsTruthy(v) {
				return v, true, nil
			}
			continue
		}

		if isLastOperand || isTruthy(operand) {
			return coalescionTerminalStatementToJSON(operand), true, nil
		}
	}

	return nil, false, errors.New("jwt_template: unexpected code path")
}

// metadata shortcodes
var (
	userMetadataReMatch          = regexp.MustCompile(`^{{user\.(public|unsafe)_metadata(\.\w+)*}}$`).MatchString
	orgMetadataReMatch           = regexp.MustCompile(`^{{org\.public_metadata(\.\w+)*}}$`).MatchString
	orgMembershipMetadataReMatch = regexp.MustCompile(`^{{org_membership\.public_metadata(\.\w+)*}}$`).MatchString
	sessionActorReMatch          = regexp.MustCompile(`^{{session\.actor(\.\w+)*}}$`).MatchString
)

// e.g. "{{user.public_metadata.foo}}"
func (t *Template) substituteExactMetadataShortcodes(s string) (any, bool) {
	if !(userMetadataReMatch(s) || orgMetadataReMatch(s) || orgMembershipMetadataReMatch(s) || sessionActorReMatch(s)) {
		return s, false
	}

	s = strings.TrimPrefix(s, expressionStart)
	s = strings.TrimSuffix(s, expressionEnd)

	return gjson.GetBytes(t.metadata, s).Value(), true
}

var shortcodeMatcher = regexp.MustCompile("{{[^}]+}}")

// scan input for shortcodes with interpolation, e.g. "foo-{{user.id}}" and
// substistute them
func (t *Template) substituteInterpolatedShortcodes(ctx context.Context, input string) (string, error) {
	var err error

	result := shortcodeMatcher.ReplaceAllStringFunc(input, func(m string) string {
		var (
			serr         error
			substitution string
		)

		v, _, serr := t.substituteNonInterpolatedShortcodes(ctx, m)
		if err != nil {
			err = serr
			return ""
		}

		substitution, serr = anyToString(v)
		if serr != nil {
			err = serr
			return ""
		}

		return substitution
	})

	return result, err
}

func populateTemplateMetadata(
	user *model.User,
	activeOrgMembership *model.OrganizationMembershipWithDeps,
	actor json.RawMessage,
) (json.RawMessage, error) {
	m := struct {
		User struct {
			Public types.JSON `json:"public_metadata"`
			Unsafe types.JSON `json:"unsafe_metadata"`
		} `json:"user"`
		Org struct {
			Public types.JSON `json:"public_metadata"`
		} `json:"org"`
		OrgMembership struct {
			Public types.JSON `json:"public_metadata"`
		} `json:"org_membership"`
		Session struct {
			Actor json.RawMessage `json:"actor,omitempty"`
		} `json:"session,omitempty"`
	}{}

	m.User.Public = user.PublicMetadata
	m.User.Unsafe = user.UnsafeMetadata

	// Initialize the below metadata to empty object in case an active org membership doesn't exist
	// to avoid json.Marshal error
	m.Org.Public = []byte("{}")
	m.OrgMembership.Public = []byte("{}")

	if activeOrgMembership != nil {
		m.Org.Public = activeOrgMembership.Organization.PublicMetadata
		m.OrgMembership.Public = activeOrgMembership.PublicMetadata
	}

	m.Session.Actor = actor

	return json.Marshal(m)
}

// anyToString converts a substitution value of any type to a string,
// so that it can be used in concatenated shortcodes.
func anyToString(value any) (string, error) {
	res, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return trimJSONStringQuotes(string(res)), nil
}

func coalescionTerminalStatementToJSON(s string) any {
	switch s {
	case "null":
		return nil
	case "true":
		return true
	case "false":
		return false
	}

	if strings.ContainsRune(s, '.') {
		if v, err := json.Number(s).Float64(); err == nil {
			return v
		}
	}
	if v, err := json.Number(s).Int64(); err == nil {
		return v
	}

	return trimJSONStringQuotes(s)
}

func trimJSONStringQuotes(s string) string {
	if strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`) {
		s = strings.TrimPrefix(s, `'`)
		s = strings.TrimSuffix(s, `'`)
	} else if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		s = strings.TrimPrefix(s, `"`)
		s = strings.TrimSuffix(s, `"`)
	}

	return s
}

func isTruthy(s string) bool {
	return s != "null" && s != "false"
}

func valueIsTruthy(v any) bool {
	if v == nil {
		return false
	}

	b, ok := v.(bool)
	if ok && !b {
		return false
	}

	// handle custom JSON types like json.String etc.
	marshaler, ok := v.(json.Marshaler)
	if ok {
		bytes, err := marshaler.MarshalJSON()
		if err != nil {
			return false
		}
		return isTruthy(string(bytes))
	}

	return true
}
