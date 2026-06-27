// app_snapshot_race_test.go: regression coverage for GetAppConfig serving the
// live published settings snapshot and reading it race-free against concurrent
// settings saves.
//
// GetAppConfig reads settings via apicore.Core.CurrentSettings(), which resolves
// the process-global atomic snapshot. These tests pin the contract: the handler
// must reflect the live published snapshot, and concurrent reads must be race-free
// against a save.
package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// withRestoredGlobalSettings snapshots the package-global settings pointer and
// restores it on cleanup so a test that publishes its own snapshot via
// conftest.SetTestSettings does not leak into sibling tests.
func withRestoredGlobalSettings(t *testing.T) {
	t.Helper()
	orig := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(orig) })
}

// TestGetAppConfigReadsLiveSnapshot proves GetAppConfig serves the latest
// published settings snapshot rather than the construction-time c.Settings
// pointer. On the pre-fix code the handler reads bare c.Settings.X and returns
// the stale construction-time values, so the assertions on the live values
// fail.
func TestGetAppConfigReadsLiveSnapshot(t *testing.T) {
	withRestoredGlobalSettings(t)

	e, controller := setupAppConfigTest(t, nil)

	// Publish a divergent live snapshot to the global atomic pointer. The
	// handler's own c.Settings still holds the construction-time snapshot
	// (Version "1.0.0-test", empty ColorScheme), so a stale read and a live
	// read return different values.
	live := conf.CloneSettings(controller.Settings.Load())
	live.Version = "9.9.9-live"
	live.Realtime.Dashboard.ColorScheme = "live-scheme"
	conftest.SetTestSettings(live)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/app/config")

	require.NoError(t, controller.GetAppConfig(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))

	assert.Equal(t, "9.9.9-live", response.Version,
		"GetAppConfig must reflect the live published version, not the stale construction-time snapshot")
	assert.Equal(t, "live-scheme", response.ColorScheme,
		"GetAppConfig must reflect the live published color scheme, not the stale construction-time snapshot")
}

// TestGetAppConfigConcurrentSaveIsRaceFree hammers GetAppConfig from multiple
// readers while a writer repeatedly republishes the settings snapshot the same
// way UpdateSettings does (StoreSettings + c.Settings reassignment). Under
// `go test -race`, the pre-fix bare c.Settings.X reads in the handler are flagged
// as a data race against the c.Settings write.
//
// The app Handler does not own the facade's settings mutex (the facade
// serializes the settings-update read-modify-write); both stores below are
// individually atomic, which is exactly what GetAppConfig's CurrentSettings()
// reads, so the contract is exercised without the mutex.
func TestGetAppConfigConcurrentSaveIsRaceFree(t *testing.T) {
	withRestoredGlobalSettings(t)

	e, controller := setupAppConfigTest(t, nil)

	// Ensure the global snapshot is never nil for the duration of the test so
	// currentSettings() always resolves via the atomic pointer.
	base := conf.CloneSettings(controller.Settings.Load())
	conftest.SetTestSettings(base)

	const (
		writers        = 2
		readers        = 4
		itersPerWriter = 50
		itersPerReader = 50
	)

	var wg sync.WaitGroup

	for w := range writers {
		wg.Go(func() {
			for i := range itersPerWriter {
				snap := conf.CloneSettings(base)
				snap.Version = "rev-" + string(rune('a'+w)) + "-" + string(rune('0'+i%10))
				// Mirror UpdateSettings: publish to the global atomic, then keep
				// the handler-cached pointer in sync. Both stores are atomic.
				conf.StoreSettings(snap)
				controller.Settings.Store(snap)
			}
		})
	}

	for range readers {
		wg.Go(func() {
			for range itersPerReader {
				req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
				rec := httptest.NewRecorder()
				ctx := e.NewContext(req, rec)
				ctx.SetPath("/api/v2/app/config")
				if err := controller.GetAppConfig(ctx); err != nil {
					t.Errorf("GetAppConfig returned error: %v", err)
					return
				}
			}
		})
	}

	wg.Wait()
}
