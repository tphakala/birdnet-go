package imageprovider

import "context"

// This file exposes package internals to the external imageprovider_test package.
// It is a _test.go file, so nothing here is compiled into production builds.

// IsBackgroundContext reports whether ctx carries the background-operation marker.
//
// backgroundOperationKey has the unexported named type contextKey, and context.Value
// compares the dynamic type of the key, so a mock spelling it as the untyped string
// "background" never matches. Test mocks must go through this helper, which forwards to
// the production predicate rather than restating it.
func IsBackgroundContext(ctx context.Context) bool {
	return isBackgroundContext(ctx)
}

// NegativeEntryMarker is the sentinel URL stored for "no image exists" entries.
// Exported so external tests assert against the production constant instead of
// re-spelling the literal.
const NegativeEntryMarker = negativeEntryMarker

// NegativeCacheTTL is how long a negative cache entry is honoured before the
// request path re-queries the provider.
const NegativeCacheTTL = negativeCacheTTL

// RefreshDelay is the pacing gap the background sweep leaves between entries.
const RefreshDelay = refreshDelay
