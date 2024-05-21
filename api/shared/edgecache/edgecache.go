// package edgecache provides utilities for managing the edge cache (Cloudflare)
// of our APIs.
//
// For high-level documentation see docs/edge_caching.md.
package edgecache

import (
	"context"

	"clerk/pkg/cenv"
	errors "clerk/pkg/clerkerrors"
	"clerk/pkg/jobs"

	cf "github.com/cloudflare/cloudflare-go"
	"github.com/vgarvardt/gue/v2"
)

// MaxTagsPerPurge mirrors Cloudflare API restriction, which does not allow
// purging more than 30 tags at once. See
// https://developers.cloudflare.com/cache/how-to/purge-cache/purge-by-tags/#purge-using-cache-tags
var MaxTagsPerPurge = 30

// Resource is a particular API endpoint of which responses may be cached at
// edge.
type Resource interface {
	// The Cloudflare Zone ID in which this resource is cached. FAPI and BAPI
	// use distinct zones.
	ZoneID() string

	// The route of the resource, as declared in go-chi,
	// e.g. /v1/organizations/{organizationID}
	Route() string

	// CacheTag returns an value that uniquely identifies this particular
	// resource. It is used when calling the Cloudflare Cache Purge API to purge
	// the cache entry.
	//
	// NOTE: for now, cache tags are computed in both clerk_go and the
	// fapi-accounts-proxy-* Cloudflare Worker. Therefore, the way cache
	// tags are computed must be consistent between the two. So if you make a
	// change in the value returned by this method, make sure to make the same
	// change in the Cloudflare Worker as well. Otherwise, the cache won't be
	// purged as expected.
	//
	// See https://developers.cloudflare.com/cache/how-to/purge-cache/purge-by-tags/
	CacheTag() string
}

type Purger interface {
	Purge(ctx context.Context, zoneID string, tags ...string) (cf.PurgeCacheResponse, error)
}

// Calls Cloudflare's Purge Cache endpoint to purge the cache for the given
// tags.
//
// https://developers.cloudflare.com/cache/how-to/purge-cache/purge-by-tags/
func Purge(ctx context.Context, purger Purger, zoneID string, tags ...string) error {
	if !cenv.IsEnabled(cenv.FlagEdgeCachePurgingEnabled) {
		return nil
	}

	if len(tags) == 0 || len(tags) > MaxTagsPerPurge {
		// Cloudflare's API does not allow purging more than 30 tags at once.
		// See https://developers.cloudflare.com/cache/how-to/purge-cache/purge-by-tags/#purge-using-cache-tags
		return errors.WithStacktrace("edgecache/purge: tags count must be between 1 and 30 (%v)", tags)
	}

	if zoneID == "" {
		return errors.WithStacktrace("edgecache/purge: zoneID must be non-empty")
	}

	resp, err := purger.Purge(ctx, zoneID, tags...)
	if err != nil {
		return errors.WithStacktrace("edgecache/purge (%v): %w", tags, err)
	}

	if !resp.Success {
		return errors.WithStacktrace("edgecache/purge (%v): non-successful response (%v, %v)",
			tags, resp.Errors, resp.Messages)
	}

	return nil
}

func SchedulePurgeByResource(ctx context.Context, gueClient *gue.Client, r Resource, jobOpts ...jobs.JobOptionFunc) error {
	if !cenv.IsEnabled(cenv.FlagEdgeCachePurgingEnabled) {
		return nil
	}

	err := jobs.PurgeEdgeCache(ctx, gueClient,
		jobs.PurgeEdgeCacheArgs{
			ZoneID: r.ZoneID(),
			Tags:   []string{r.CacheTag()},
		},
		jobOpts...)
	if err != nil {
		return errors.WithStacktrace("edgecache/SchedulePurgeByResource (%s): %w",
			r.Route(), err)
	}

	return nil
}

func SchedulePurgeByResources(ctx context.Context, gueClient *gue.Client, resources []Resource, jobOpts ...jobs.JobOptionFunc) error {
	if !cenv.IsEnabled(cenv.FlagEdgeCachePurgingEnabled) {
		return nil
	}

	// the Cloudflare Purge Cache API endpoint accepts a list of tags to purge
	// by. Therefore, for efficiency, we batch tags by their resources' zone, so
	// as to purge with as few requests to the Cloudflare API as possible.
	zones := make(map[string][]Resource)
	for _, r := range resources {
		zones[r.ZoneID()] = append(zones[r.ZoneID()], r)
	}

	for zoneID, zoneResources := range zones {
		batch := make([]string, 0, MaxTagsPerPurge)

		for i := 0; i < len(zoneResources); i++ {
			batch = append(batch, zoneResources[i].CacheTag())

			if len(batch) == MaxTagsPerPurge || i == len(zoneResources)-1 {
				err := jobs.PurgeEdgeCache(ctx, gueClient, jobs.PurgeEdgeCacheArgs{
					ZoneID: zoneID,
					Tags:   batch,
				}, jobOpts...)
				if err != nil {
					return errors.WithStacktrace("edgecache/SchedulePurgeByResources (%v): %w", batch, err)
				}
				batch = make([]string, 0, MaxTagsPerPurge)
			}
		}
	}

	return nil
}

func SchedulePurgeByTag(ctx context.Context, gueClient *gue.Client, zoneID, tag string, jobOpts ...jobs.JobOptionFunc) error {
	if !cenv.IsEnabled(cenv.FlagEdgeCachePurgingEnabled) {
		return nil
	}
	if zoneID == "" {
		return errors.WithStacktrace("edgecache/SchedulePurgeByTags: no zone provided")
	}
	if tag == "" {
		return errors.WithStacktrace("edgecache/SchedulePurgeByTags: no tag provided")
	}

	return jobs.PurgeEdgeCache(ctx, gueClient, jobs.PurgeEdgeCacheArgs{
		ZoneID: zoneID,
		Tags:   []string{tag},
	}, jobOpts...)
}
