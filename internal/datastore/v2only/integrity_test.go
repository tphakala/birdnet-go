package v2only

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
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

// TestClassifyIntegrityQueryError exercises item-1's core logic in isolation: a
// PRAGMA quick_check that errors is a corruption verdict only when the error is a
// corruption-class error (malformed image, not a database). Transient failures
// (timeout, cancellation, a locked db) are not an integrity signal and must
// report ran=false so the check stays "not run yet" and retries. Extracting the
// classifier keeps this deterministic without needing a corrupt on-disk DB.
func TestClassifyIntegrityQueryError(t *testing.T) {
	t.Parallel()

	malformed := errors.NewStd("database disk image is malformed (11)")
	notADatabase := errors.NewStd("file is not a database")

	tests := []struct {
		name        string
		err         error
		wantResult  string
		wantCorrupt bool
	}{
		{
			name:        "malformed image is a corruption verdict",
			err:         malformed,
			wantResult:  malformed.Error(),
			wantCorrupt: true,
		},
		{
			name:        "not a database is a corruption verdict",
			err:         notADatabase,
			wantResult:  notADatabase.Error(),
			wantCorrupt: true,
		},
		{
			name:        "context deadline is transient, not a verdict",
			err:         context.DeadlineExceeded,
			wantResult:  "",
			wantCorrupt: false,
		},
		{
			name:        "context cancellation is transient, not a verdict",
			err:         context.Canceled,
			wantResult:  "",
			wantCorrupt: false,
		},
		{
			// SQLITE_INTERRUPT (context cancellation mid-scan) surfaces as
			// "interrupted": it contains "rrupt" but not "corrupt", so it must
			// not be mistaken for corruption.
			name:        "interrupted scan is transient, not a verdict",
			err:         errors.NewStd("interrupted (9)"),
			wantResult:  "",
			wantCorrupt: false,
		},
		{
			name:        "locked database is transient, not a verdict",
			err:         errors.NewStd("database is locked"),
			wantResult:  "",
			wantCorrupt: false,
		},
		{
			name:        "generic error is not a verdict",
			err:         errors.NewStd("some unrelated failure"),
			wantResult:  "",
			wantCorrupt: false,
		},
		{
			name:        "nil error is not a verdict",
			err:         nil,
			wantResult:  "",
			wantCorrupt: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, corrupt := classifyIntegrityQueryError(tt.err)
			assert.Equal(t, tt.wantCorrupt, corrupt)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

// Compile-time assurance that *Datastore exposes the integrity-cache refresh hook
// the diagnostics refresh calls through (mirrors the integrityCacheRefresher
// interface in internal/api/v2/system).
var _ interface {
	RefreshIntegrityCache() bool
} = (*Datastore)(nil)

// TestRefreshIntegrityCache_Cooldown verifies the forced-refresh hook clears the
// cache when the cached result is older than the cooldown (so the next
// IntegrityResult recomputes) but coalesces a second forced refresh inside the
// cooldown, so a rapid caller cannot re-trigger the expensive quick_check scan
// (#3939 follow-up; guards CWE-400 on the pinned SQLite connection).
func TestRefreshIntegrityCache_Cooldown(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	t.Cleanup(cleanup)

	// Populate the cache with a first check.
	result, corrupted := ds.IntegrityResult()
	require.Equal(t, integrityResultOK, result)
	require.False(t, corrupted)

	// A refresh within the cooldown is coalesced: the recent result is kept.
	require.False(t, ds.RefreshIntegrityCache(), "a refresh within the cooldown must be coalesced")
	ds.integrityMu.RLock()
	kept := ds.integrityResult
	ds.integrityMu.RUnlock()
	assert.Equal(t, integrityResultOK, kept, "a coalesced refresh keeps the cached result")

	// Age the cache past the cooldown; now a refresh clears both fields.
	ds.integrityMu.Lock()
	ds.integrityCheckedAt = time.Now().Add(-2 * v2IntegrityRefreshCooldown)
	ds.integrityMu.Unlock()

	require.True(t, ds.RefreshIntegrityCache(), "a refresh past the cooldown clears the cache")
	ds.integrityMu.RLock()
	clearedResult := ds.integrityResult
	clearedAt := ds.integrityCheckedAt
	ds.integrityMu.RUnlock()
	assert.Empty(t, clearedResult, "a refresh past the cooldown clears the cached result string")
	assert.True(t, clearedAt.IsZero(), "a refresh past the cooldown clears the cache timestamp so the fast path misses")

	// The next call recomputes and re-caches with a fresh timestamp.
	result, corrupted = ds.IntegrityResult()
	assert.Equal(t, integrityResultOK, result)
	assert.False(t, corrupted)

	ds.integrityMu.RLock()
	recomputedAt := ds.integrityCheckedAt
	ds.integrityMu.RUnlock()
	assert.False(t, recomputedAt.IsZero(), "recompute after a refresh sets a new timestamp")
}

// TestIntegrityResult_ServesCachedCorruption verifies the fast path returns a
// cached corruption verdict as corrupted=true without recomputing, so a known-bad
// database is not re-scanned on every health poll while the cache is warm. This is
// the corrupted=true direction of #3939, which the healthy-DB tests never assert.
func TestIntegrityResult_ServesCachedCorruption(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	t.Cleanup(cleanup)

	const corruption = "database disk image is malformed (11)"
	ds.integrityMu.Lock()
	ds.integrityResult = corruption
	ds.integrityCheckedAt = time.Now()
	ds.integrityMu.Unlock()

	result, corrupted := ds.IntegrityResult()
	assert.Equal(t, corruption, result, "the fast path returns the cached corruption string")
	assert.True(t, corrupted, "a cached non-ok result reports corrupted=true")

	// No recompute: the on-disk DB is healthy, so a recompute would overwrite the
	// seeded corruption string with "ok". Its survival proves the warm cache served.
	ds.integrityMu.RLock()
	after := ds.integrityResult
	ds.integrityMu.RUnlock()
	assert.Equal(t, corruption, after, "a warm cache is served, not recomputed")
}
