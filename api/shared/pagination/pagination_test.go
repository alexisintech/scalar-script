package pagination

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/volatiletech/sqlboiler/v4/queries/qm"
)

func TestNewFromRequest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		url    string
		limit  int
		offset int
		hasErr bool
	}{
		{
			"/v1/bananas",
			10,
			0,
			false,
		},
		{
			"/v1/peaches?limit=20",
			20,
			0,
			false,
		},
		{
			"/v1/apricots?limit=30&offset=15",
			30,
			15,
			false,
		},
		{
			"/v1/pumpkins?limit=50&offset=-5",
			50,
			0,
			false,
		},
		{
			"/v1/margaritas?limit=999&offset=333",
			DefaultLimit,
			333,
			false,
		},
		{
			"/v1/margaritas?limit=0&offset=333",
			DefaultLimit,
			333,
			false,
		},
		{
			"/v1/avocados?limit=25&offset=lol",
			25,
			0,
			true,
		},
	}

	for _, tc := range testCases {
		req, err := http.NewRequest(http.MethodGet, tc.url, nil)
		require.NoError(t, err)

		params, apiErr := NewFromRequest(req)

		assert.Equal(t, tc.limit, params.Limit)
		assert.Equal(t, tc.offset, params.Offset)

		if tc.hasErr {
			require.Error(t, apiErr)
		} else {
			require.NoError(t, apiErr)
		}
	}
}

func TestToQueryMods(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		limit     int
		offset    int
		queryMods []qm.QueryMod
	}{
		{
			0,
			0,
			[]qm.QueryMod{},
		},
		{
			20,
			100,
			[]qm.QueryMod{qm.Limit(20), qm.Offset(100)},
		},
		{
			50,
			0,
			[]qm.QueryMod{qm.Limit(50)},
		},
	}

	for _, tc := range testCases {
		params := Params{Limit: tc.limit, Offset: tc.offset}
		assert.Equal(t, tc.queryMods, params.ToQueryMods())
	}
}
