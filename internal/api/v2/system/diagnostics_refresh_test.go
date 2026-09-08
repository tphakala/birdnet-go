package system

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// refreshingDatastore is a datastore.Interface (via the generated mock, which
// needs no expectations because the refresh path only type-asserts and calls
// RefreshIntegrityCache) that additionally counts integrity-cache refreshes,
// mirroring what the v2 datastore exposes.
type refreshingDatastore struct {
	*mocks.MockInterface
	refreshes int
}

func (d *refreshingDatastore) RefreshIntegrityCache() bool {
	d.refreshes++
	return true
}

// TestRefreshIntegrityCache covers the item-5 opt-in wiring helper: an explicit
// refresh asks a caching datastore to refresh, while the default path and
// datastores without the capability (or a nil one) are no-ops.
func TestRefreshIntegrityCache(t *testing.T) {
	t.Parallel()

	t.Run("refresh true refreshes a caching datastore", func(t *testing.T) {
		t.Parallel()
		ds := &refreshingDatastore{MockInterface: mocks.NewMockInterface(t)}
		refreshIntegrityCache(ds, true)
		assert.Equal(t, 1, ds.refreshes, "an explicit refresh asks the datastore to refresh its integrity cache")
	})

	t.Run("refresh false leaves the cache alone", func(t *testing.T) {
		t.Parallel()
		ds := &refreshingDatastore{MockInterface: mocks.NewMockInterface(t)}
		refreshIntegrityCache(ds, false)
		assert.Zero(t, ds.refreshes, "without a refresh the cached result is preserved")
	})

	t.Run("datastore without refresh support is a no-op", func(t *testing.T) {
		t.Parallel()
		// A plain mock implements datastore.Interface but not
		// integrityCacheRefresher, so the assertion misses and nothing happens.
		assert.NotPanics(t, func() {
			refreshIntegrityCache(mocks.NewMockInterface(t), true)
		})
	})

	t.Run("nil datastore is a no-op", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() {
			refreshIntegrityCache(nil, true)
		})
	})
}

// TestRunDiagnostics_RefreshIntegrity exercises the full handler path (query
// parsing plus the refresh wiring), which the helper test bypasses: the datastore
// integrity cache is refreshed only for an explicit truthy refresh_integrity, and
// a present-but-unparseable value is a 400 like the window param.
func TestRunDiagnostics_RefreshIntegrity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		query         string
		wantStatus    int
		wantRefreshes int
	}{
		{name: "explicit true refreshes", query: "?refresh_integrity=true", wantStatus: http.StatusOK, wantRefreshes: 1},
		{name: "explicit false does not refresh", query: "?refresh_integrity=false", wantStatus: http.StatusOK, wantRefreshes: 0},
		{name: "omitted does not refresh", query: "", wantStatus: http.StatusOK, wantRefreshes: 0},
		{name: "unparseable value is a client error", query: "?refresh_integrity=maybe", wantStatus: http.StatusBadRequest, wantRefreshes: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e, controller := setupDiagnosticsTest(t, observability.NewHealthMetricsStore(), nil)
			ds := &refreshingDatastore{MockInterface: mocks.NewMockInterface(t)}
			controller.DS = ds

			req := httptest.NewRequest(http.MethodPost, "/api/v2/system/diagnostics/run"+tt.query, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := controller.RunDiagnostics(c)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantRefreshes, ds.refreshes)
		})
	}
}
