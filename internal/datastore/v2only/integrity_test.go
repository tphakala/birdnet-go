package v2only

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time assurance that *Datastore satisfies the integrity accessor the
// diagnostics Database Integrity health check reads through (mirrors the
// consumer-side integrityResulter interface in internal/api/v2/system). This is
// the coupling #3939 relies on: the check reads integrity from the v2 datastore
// via this method instead of asserting the legacy *datastore.SQLiteStore type.
var _ interface {
	IntegrityResult() (string, bool)
} = (*Datastore)(nil)

// TestIntegrityResult_HealthySQLite verifies a healthy v2 SQLite datastore
// reports "ok" and not corrupted. Before #3939 the health check could not read
// integrity from a v2 datastore at all and reported StatusUnknown forever.
func TestIntegrityResult_HealthySQLite(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	t.Cleanup(cleanup)

	result, corrupted := ds.IntegrityResult()
	assert.Equal(t, "ok", result, "a healthy in-memory SQLite database passes PRAGMA quick_check")
	assert.False(t, corrupted, "a healthy database is not corrupted")
}

// TestIntegrityResult_CachesWithinTTL verifies the result is cached: a second
// call within the TTL serves the stored value without recomputing (the cache
// timestamp is unchanged). quick_check can be slow on a large database, so the
// on-demand health check must not re-run it on every page load.
func TestIntegrityResult_CachesWithinTTL(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	t.Cleanup(cleanup)

	result, corrupted := ds.IntegrityResult()
	require.Equal(t, "ok", result)
	require.False(t, corrupted)

	ds.integrityMu.RLock()
	firstCheckedAt := ds.integrityCheckedAt
	ds.integrityMu.RUnlock()
	require.False(t, firstCheckedAt.IsZero(), "first call must cache a result with a timestamp")

	result, corrupted = ds.IntegrityResult()
	assert.Equal(t, "ok", result)
	assert.False(t, corrupted)

	ds.integrityMu.RLock()
	secondCheckedAt := ds.integrityCheckedAt
	ds.integrityMu.RUnlock()
	assert.Equal(t, firstCheckedAt, secondCheckedAt, "a call within the TTL must serve the cached result, not recompute it")
}
