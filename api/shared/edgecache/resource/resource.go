// package resource declares the FAPI and BAPI responses that may be cached at
// edge. These are essentially implementations of the edgecache.Resource
// interface.
package resource

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"clerk/pkg/cenv"
	errors "clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	clerkstrings "clerk/pkg/strings"
)

// common utilities for FAPI resources
type fapiResource struct{}

func (r fapiResource) ZoneID() string {
	return cenv.Get(cenv.CloudflareFAPIZoneID)
}

func (r fapiResource) cacheTag(fapiHost, route string) string {
	return fapiHost + ":" + route
}

// common utility for BAPI resources.
//
// For BAPI resources, we maintain a cache entry per instance's secret key. This
// is because in the Cloudflare Worker, given an incoming BAPI request, the
// secret key contained in the 'Authorization' request header is the
// only value we have that uniquely identifies the instance. Therefore we use it
// (after SHA256-hashing it) as the cache key.
//
// Consequently, for any given BAPI resource we have to issue N purge requests
// (N=no. of secret keys of the instance).
type bapiResource struct{}

// hashedInstanceSecretKey is the SHA256 hash of an instance's secret key. Used
// for grouping BAPI Resources of the same instance under a common namespace.
type hashedInstanceSecretKey string

func (r bapiResource) ZoneID() string {
	return cenv.Get(cenv.CloudflareBAPIZoneID)
}

func (r bapiResource) hash(instanceSecretKey string) (hashedInstanceSecretKey, error) {
	if instanceSecretKey == "" {
		return "", errors.WithStacktrace("edgecache/bapi: empty secret key")
	}

	// BAPI requests contain the secret key in the 'Authorization' header, in
	// either of the following formats:
	//
	// 1. `sk_<live|test>_<secret_key>`
	// 2. `<live|test>_<secret_key>`
	//
	// For caching purposes, we want to treat those two requests as equivalent
	// and maintain a unified cache entry for the two. Hence we compute the
	// cache key only after normalizing the secret key to contain the `sk_`
	// prefix (i.e. the same format that is stored in our database).
	//
	// NOTE: This is (and should stay) consistent with the way the Cloudflare
	// Worker computes the cache key.
	instanceSecretKey = clerkstrings.AddPrefixIfNeeded(instanceSecretKey, constants.SecretKeyPrefix)

	hashBytes := sha256.Sum256([]byte(instanceSecretKey))
	hexstring := strings.ToLower(hex.EncodeToString(hashBytes[:]))

	return hashedInstanceSecretKey(hexstring), nil
}

func (r bapiResource) cacheTag(h hashedInstanceSecretKey, route string) string {
	return string(h) + ":" + route
}
