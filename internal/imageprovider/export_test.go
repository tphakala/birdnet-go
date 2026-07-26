package imageprovider

import (
	"context"
	"net/http"
)

// This file exposes package internals to the external imageprovider_test package.
// It is a _test.go file, so nothing here is compiled into production builds.

// IsBackgroundContext reports whether ctx carries the background-operation marker
// set by refreshStaleEntries.
//
// Test mocks must not spell the key as an untyped string: backgroundOperationKey has
// the unexported named type contextKey, and context.Value compares the dynamic type
// of the key, so ctx.Value("background") never matches ctx.Value(backgroundOperationKey).
// Mocks that made that mistake silently classified every background fetch as a user
// fetch, which permanently skipped the tests asserting on background behaviour.
func IsBackgroundContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	bg, ok := ctx.Value(backgroundOperationKey).(bool)
	return ok && bg
}

// SetFileCacheHTTPClient overrides the HTTP client used for image byte downloads.
// The production client (imageHTTPClient) rejects loopback addresses as SSRF
// protection, so an httptest server is unreachable without this override.
func SetFileCacheHTTPClient(fc *ImageFileCache, client *http.Client) {
	fc.httpClient = client
}

// BuildUserAgent exposes buildUserAgent so external tests can assert that the
// image download sends the same policy-compliant User-Agent as the API path.
func BuildUserAgent(appVersion string) string {
	return buildUserAgent(appVersion)
}

// NegativeEntryMarker is the sentinel URL stored for "no image exists" entries.
// Exported so external tests assert against the production constant instead of
// re-spelling the literal.
const NegativeEntryMarker = negativeEntryMarker

// NegativeCacheTTL is how long a negative cache entry is honoured before the
// request path re-queries the provider.
const NegativeCacheTTL = negativeCacheTTL
