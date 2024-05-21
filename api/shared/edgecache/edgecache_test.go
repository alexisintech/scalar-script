package edgecache

import (
	"context"
	"errors"
	"testing"

	"clerk/pkg/cenv"

	cf "github.com/cloudflare/cloudflare-go"
	"github.com/stretchr/testify/require"
)

type mockPurger struct {
	// If true, the response will mimick a successful cache purge.
	Success   bool
	ReturnErr bool
}

func (m mockPurger) Purge(_ context.Context, _ string, _ ...string) (cf.PurgeCacheResponse, error) {
	resp := cf.PurgeCacheResponse{}
	resp.Success = m.Success

	var err error
	if m.ReturnErr {
		err = errors.New("mock_edge_cache_purger/PurgeByTags: error")
	}

	return resp, err
}

// nolint:paralleltest
func TestPurge(t *testing.T) {
	t.Setenv(cenv.FlagEdgeCachePurgingEnabled, "true")
	ctx := context.Background()
	purger := &mockPurger{Success: true}

	// successful response
	require.NoError(t, Purge(ctx, purger, "zoneA", "tag1"))

	// no tags provided
	require.Error(t, Purge(ctx, purger, "zoneA"))

	// no zone provided
	require.Error(t, Purge(ctx, purger, "", "tag1"))

	// too many tags provided
	tags := make([]string, 31)
	for i := range tags {
		tags[i] = "foo"
	}
	require.Error(t, Purge(ctx, purger, "", tags...))

	// non-successful response
	purger = &mockPurger{Success: false}
	require.Error(t, Purge(ctx, purger, "", "tag1"))

	// error during HTTP request
	purger = &mockPurger{ReturnErr: true}
	require.Error(t, Purge(ctx, purger, "", "tag1"))
}
