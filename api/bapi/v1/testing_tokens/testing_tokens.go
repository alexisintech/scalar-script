// Testing tokens are short-lived, instance-scoped tokens that may be used to
// bypass Bot Protection WAF rules in that particular instance's FAPI.
//
// A testing token is essentially an HMAC-SHA256 of a message that includes the
// instance's FAPI domain and the token generation timestamp. Cloudflare's WAF
// then verifies the token by recomputing the HMAC, using
// `is_timed_hmac_valid_v0` function in the WAF rules[1].
//
// [1] https://developers.cloudflare.com/ruleset-engine/rules-language/functions/#hmac-validation
package testing_tokens

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/jonboulle/clockwork"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	clerktime "clerk/pkg/time"
)

// How much the token will be valid for. This has to be consistent with the TTL
// value we set in Cloudflare
const TokenTTL = time.Hour

type HTTP struct {
	clock clockwork.Clock
}

func NewHTTP(clock clockwork.Clock) *HTTP {
	return &HTTP{clock: clock}
}

func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	env := environment.FromContext(r.Context())

	if env.Instance.IsProduction() {
		return nil, apierror.InvalidRequestForEnvironment(string(constants.ETDevelopment))
	}

	key := []byte(cenv.Get(cenv.TestingTokenSecret))

	// since testing tokens may be used to bypass rate limiting in the future,
	// we do not want to hand out a different token on every request, because
	// that would result in customers being able to effectively bypass our rate
	// limits by cycling through tokens.
	//
	// So we provide the a fixed token inside each 10-minute slot. For example:
	//
	//   14:10:00 -> token A
	//   14:13:19 -> token A
	//   14:21:25 -> token B
	//   14:29:59 -> token B
	//   14:32:00 -> token C
	issuedAt := clerktime.RoundDownToNearestMinutes(h.clock.Now(), 10)

	// Cloudflare expects the last part of the message to be the timestamp (even
	// though they don't document this).
	message := []byte(fmt.Sprintf("%s%d", env.Domain.FapiHost(), issuedAt.Unix()))

	m := hmac.New(sha256.New, key)
	_, err := m.Write(message)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	mac := m.Sum(nil)

	// we don't need to use padding, so we omit it. Note that for this to work,
	// we also pass the 's' flag to is_timed_hmac_valid_v0, on Cloudflare's
	// side.
	macEncoded := base64.RawURLEncoding.EncodeToString(mac)
	token := fmt.Sprintf("%d-%s", issuedAt.Unix(), macEncoded)
	expiresAt := issuedAt.Add(TokenTTL).Unix()

	return serialize.TestingToken(token, expiresAt), nil
}
