package character_validator_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"clerk/api/shared/character_validator"
)

func TestSanitizer(t *testing.T) {
	t.Parallel()

	var disallowedRunes = []rune{
		0x00, // NUL
	}

	for i := range disallowedRunes {
		disallowedRune := disallowedRunes[i]

		t.Run(fmt.Sprintf("testing with disallowed rune %s", string(disallowedRune)), func(t *testing.T) {
			t.Parallel()
			testValue := "value"

			// test form
			formData := url.Values{}
			formData.Add("key", testValue+string(disallowedRune))
			r, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
			r.Form = formData
			require.NoError(t, err)

			apiErr := character_validator.Form(r)
			require.Error(t, apiErr)

			// test query params
			params := url.Values{}
			params.Set("key", testValue+string(disallowedRune))
			reqURL, err := url.Parse("https://example.com")
			require.NoError(t, err)
			reqURL.RawQuery = params.Encode()
			r, err = http.NewRequest(http.MethodGet, reqURL.String(), nil)
			require.NoError(t, err)

			apiErr = character_validator.QueryParams(r)
			require.Error(t, apiErr)
		})
	}
}
