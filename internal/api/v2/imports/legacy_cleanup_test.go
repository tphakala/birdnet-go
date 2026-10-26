// internal/api/v2/imports/legacy_cleanup_test.go
package importsapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	_ "github.com/mattn/go-sqlite3" // SQLite driver for safety check test
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/notification"
	"gorm.io/gorm"
)

// createTestSettings creates settings for SQLite-based legacy cleanup tests.
func createTestSettings(legacyPath string) *conf.Settings {
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath
	settings.Output.MySQL.Enabled = false
	return settings
}

// createLegacyTestHandler creates a controller with initialized cleanupStatus for testing.
func createLegacyTestHandler(tb testing.TB, e *echo.Echo, settings *conf.Settings) *Handler {
	tb.Helper()
	// Handlers read the live snapshot via currentSettings(); publish the test's
	// settings so the read resolves to them (restored on cleanup).
	apitest.PublishTestSettings(tb, settings)
	c := &Handler{Core: &apicore.Core{Echo: e}, cleanupStatus: NewCleanupStatus()}
	c.Settings.Store(settings)
	return c
}

// TestGetLegacyStatus_V2OnlyMode_NoLegacy tests status when in v2-only mode with no legacy DB.
func TestGetLegacyStatus_V2OnlyMode_NoLegacy(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	// Setup test environment
	e := echo.New()
	tmpDir := t.TempDir()

	// Create settings with non-existent legacy path
	settings := createTestSettings(filepath.Join(tmpDir, "nonexistent.db"))
	controller := createLegacyTestHandler(t, e, settings)

	// Set v2-only mode
	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = true
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/legacy/status", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetLegacyStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response LegacyStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response.Exists, "Legacy should not exist")
	assert.False(t, response.CanCleanup, "Cannot cleanup non-existent DB")
	assert.Contains(t, response.Reason, "No legacy database found")
}

// TestGetLegacyStatus_V2OnlyMode_WithLegacy tests status when legacy DB exists.
func TestGetLegacyStatus_V2OnlyMode_WithLegacy(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	e := echo.New()
	tmpDir := t.TempDir()

	// Create a legacy database file
	legacyPath := filepath.Join(tmpDir, "birdnet.db")
	err := os.WriteFile(legacyPath, []byte("test data for size"), 0o600)
	require.NoError(t, err)

	settings := createTestSettings(legacyPath)
	controller := createLegacyTestHandler(t, e, settings)

	// Set v2-only mode
	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = true
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/legacy/status", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.GetLegacyStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response LegacyStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response.Exists, "Legacy should exist")
	assert.True(t, response.CanCleanup, "Should be able to cleanup")
	assert.Equal(t, legacyPath, response.Location)
	assert.Positive(t, response.SizeBytes, "Size should be > 0")
}

// TestGetLegacyStatus_NotV2OnlyMode tests status when NOT in v2-only mode.
func TestGetLegacyStatus_NotV2OnlyMode(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	e := echo.New()
	tmpDir := t.TempDir()

	legacyPath := filepath.Join(tmpDir, "birdnet.db")
	err := os.WriteFile(legacyPath, []byte("test"), 0o600)
	require.NoError(t, err)

	settings := createTestSettings(legacyPath)
	controller := createLegacyTestHandler(t, e, settings)

	// NOT in v2-only mode
	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = false
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/legacy/status", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.GetLegacyStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response LegacyStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response.Exists, "Legacy exists")
	assert.False(t, response.CanCleanup, "Cannot cleanup when not in v2-only mode")
	assert.Contains(t, response.Reason, "restart")
}

// TestStartLegacyCleanup_NotV2OnlyMode tests cleanup when not in v2-only mode.
func TestStartLegacyCleanup_NotV2OnlyMode(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	e := echo.New()
	tmpDir := t.TempDir()

	legacyPath := filepath.Join(tmpDir, "birdnet.db")
	err := os.WriteFile(legacyPath, []byte("test"), 0o600)
	require.NoError(t, err)

	settings := createTestSettings(legacyPath)
	controller := createLegacyTestHandler(t, e, settings)

	// NOT in v2-only mode
	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = false
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/legacy/cleanup", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.StartLegacyCleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var response CleanupActionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response.Success)
	assert.Contains(t, response.Message, "restart")
}

// TestStartLegacyCleanup_SQLite_Success tests successful SQLite cleanup.
func TestStartLegacyCleanup_SQLite_Success(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	e := echo.New()
	tmpDir := t.TempDir()

	// Create legacy database file
	legacyPath := filepath.Join(tmpDir, "birdnet.db")
	err := os.WriteFile(legacyPath, []byte("legacy database content"), 0o600)
	require.NoError(t, err)

	// Create WAL and SHM files
	err = os.WriteFile(legacyPath+"-wal", []byte("wal"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(legacyPath+"-shm", []byte("shm"), 0o600)
	require.NoError(t, err)

	settings := createTestSettings(legacyPath)
	controller := createLegacyTestHandler(t, e, settings)

	// Set v2-only mode
	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = true
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/legacy/cleanup", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.StartLegacyCleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response CleanupActionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)

	// Wait for async cleanup to complete using Eventually
	require.Eventually(t, func() bool {
		state, _, _, _ := controller.cleanupStatus.Get()
		return state == CleanupStateCompleted || state == CleanupStateFailed
	}, 5*time.Second, 10*time.Millisecond, "cleanup should complete")

	// Verify files are deleted
	_, err = os.Stat(legacyPath)
	assert.True(t, os.IsNotExist(err), "Legacy DB file should be deleted")

	_, err = os.Stat(legacyPath + "-wal")
	assert.True(t, os.IsNotExist(err), "WAL file should be deleted")

	_, err = os.Stat(legacyPath + "-shm")
	assert.True(t, os.IsNotExist(err), "SHM file should be deleted")

	// Verify cleanup state is completed
	state, _, _, _ := controller.cleanupStatus.Get()
	assert.Equal(t, CleanupStateCompleted, state)
}

// TestStartLegacyCleanup_SQLite_V2DatabaseSafetyCheck tests that V2 databases are not deleted.
func TestStartLegacyCleanup_SQLite_V2DatabaseSafetyCheck(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	e := echo.New()
	tmpDir := t.TempDir()

	// Create a file that would be detected as a V2 database
	// We need to mock CheckSQLiteHasV2Schema or create a real V2 database
	// For this test, we'll verify the safety check is called by checking error message
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	// Create a minimal SQLite database with migration_states table to simulate V2
	// The table structure must match what GORM expects for entities.MigrationState
	db, err := sql.Open("sqlite3", legacyPath)
	require.NoError(t, err)
	_, err = db.Exec(`
		CREATE TABLE migration_states (
			id INTEGER PRIMARY KEY,
			state TEXT NOT NULL DEFAULT 'completed',
			current_phase TEXT NOT NULL DEFAULT '',
			phase_number INTEGER DEFAULT 0,
			total_phases INTEGER DEFAULT 0,
			started_at DATETIME,
			completed_at DATETIME,
			last_migrated_id INTEGER DEFAULT 0,
			total_records INTEGER DEFAULT 0,
			migrated_records INTEGER DEFAULT 0,
			error_message TEXT,
			related_data_error TEXT,
			updated_at DATETIME
		);
		INSERT INTO migration_states (id, state) VALUES (1, 'completed');
	`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	settings := createTestSettings(legacyPath)
	controller := createLegacyTestHandler(t, e, settings)

	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = true
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/legacy/cleanup", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.StartLegacyCleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code) // Request accepted, but async will fail

	// Wait for async cleanup to complete using Eventually
	require.Eventually(t, func() bool {
		state, _, _, _ := controller.cleanupStatus.Get()
		return state == CleanupStateCompleted || state == CleanupStateFailed
	}, 5*time.Second, 10*time.Millisecond, "cleanup should complete")

	// Verify cleanup failed due to safety check
	state, errMsg, _, _ := controller.cleanupStatus.Get()
	assert.Equal(t, CleanupStateFailed, state)
	assert.Contains(t, errMsg, "V2 database")

	// Verify file was NOT deleted
	_, err = os.Stat(legacyPath)
	assert.NoError(t, err, "V2 database file should NOT be deleted")
}

// TestStartLegacyCleanup_AlreadyInProgress tests cleanup when already running.
func TestStartLegacyCleanup_AlreadyInProgress(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	e := echo.New()
	tmpDir := t.TempDir()

	legacyPath := filepath.Join(tmpDir, "birdnet.db")
	err := os.WriteFile(legacyPath, []byte("test"), 0o600)
	require.NoError(t, err)

	settings := createTestSettings(legacyPath)
	controller := createLegacyTestHandler(t, e, settings)

	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = true
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	// Set cleanup as already in progress
	controller.cleanupStatus.Set(CleanupStateInProgress, "", nil, 0)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/legacy/cleanup", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.StartLegacyCleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, rec.Code)

	var response CleanupActionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response.Success)
	assert.Contains(t, response.Message, "already in progress")
}

// createMySQLTestSettings creates settings for MySQL-based legacy cleanup tests.
func createMySQLTestSettings() *conf.Settings {
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = false
	settings.Output.MySQL.Enabled = true
	settings.Output.MySQL.Database = "test_birdnet"
	return settings
}

// legacyDBRepo is a test-only repository that exposes a GORM connection through
// GetDB, the seam the MySQL cleanup reads its connection from. Production
// repositories do not implement it.
type legacyDBRepo struct {
	datastore.DetectionRepository
	db *gorm.DB
}

// GetDB returns the wrapped connection.
func (r *legacyDBRepo) GetDB() *gorm.DB { return r.db }

// enableV2OnlyMode switches the package-wide v2-only flag on for one test.
func enableV2OnlyMode(t *testing.T) {
	t.Helper()
	prev := isV2OnlyMode
	isV2OnlyMode = true
	t.Cleanup(func() { isV2OnlyMode = prev })
}

// TestMySQLLegacyCandidates pins the candidate rule: a bare table prefix means
// the legacy-named tables are live v2 tables, and bare v2 tables beside a prefixed
// schema keep the shared names out of the list.
func TestMySQLLegacyCandidates(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	tests := []struct {
		name          string
		prefix        string
		bareV2Present bool
		want          []string
	}{
		{"bare schema offers nothing", "", false, nil},
		{"prefixed schema without bare v2 offers every legacy table", "v2_", false, slices.Concat(legacyOnlyTables, legacySharedTables)},
		{"prefixed schema beside bare v2 tables offers legacy-only tables", "v2_", true, legacyOnlyTables},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mysqlLegacyCandidates(tt.prefix, tt.bareV2Present))
		})
	}
}

// TestMySQLLegacyCandidates_DropOrder verifies the children-first order the
// foreign keys on notes require.
func TestMySQLLegacyCandidates_DropOrder(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	got := mysqlLegacyCandidates("v2_", false)
	for _, child := range []string{"note_reviews", "note_comments", "note_locks", "results"} {
		assert.Less(t, slices.Index(got, child), slices.Index(got, "notes"), "%s must be dropped before notes", child)
	}
}

// TestGetLegacyStatus_MySQL_Refuses verifies the status call fails closed when no
// connection or no usable MySQL manager is available.
func TestGetLegacyStatus_MySQL_Refuses(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	sqliteMgr := newFakeV2ManagerWithTable(t, "v2_detections", "v2_", 0)
	tests := []struct {
		name string
		repo func() datastore.DetectionRepository
		mgr  datastoreV2.Manager
	}{
		{"repository without a connection", func() datastore.DetectionRepository { return nil }, nil},
		{"no v2 manager", func() datastore.DetectionRepository { return &legacyDBRepo{db: sqliteMgr.DB()} }, nil},
		{"non-MySQL v2 manager", func() datastore.DetectionRepository { return &legacyDBRepo{db: sqliteMgr.DB()} }, sqliteMgr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := createLegacyTestHandler(t, e, createMySQLTestSettings())
			controller.Repo = tt.repo()
			if tt.mgr != nil {
				controller.V2Manager = tt.mgr
			}
			enableV2OnlyMode(t)

			rec := httptest.NewRecorder()
			ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/api/v2/system/database/legacy/status", http.NoBody), rec)
			require.NoError(t, controller.GetLegacyStatus(ctx))
			assert.Equal(t, http.StatusOK, rec.Code)

			var response LegacyStatusResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.False(t, response.Exists)
			assert.False(t, response.CanCleanup)
			assert.Equal(t, notification.MsgErrCleanupAccessFailed, response.ErrorKey)
		})
	}
}

// TestStartLegacyCleanup_MySQL_Refuses verifies a POST that cannot be resolved is
// refused synchronously and never schedules work.
func TestStartLegacyCleanup_MySQL_Refuses(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	sqliteMgr := newFakeV2ManagerWithTable(t, "v2_detections", "v2_", 0)
	tests := []struct {
		name string
		repo func() datastore.DetectionRepository
	}{
		{"repository without a connection", func() datastore.DetectionRepository { return nil }},
		{"no v2 manager", func() datastore.DetectionRepository { return &legacyDBRepo{db: sqliteMgr.DB()} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := createLegacyTestHandler(t, e, createMySQLTestSettings())
			controller.Repo = tt.repo()
			enableV2OnlyMode(t)

			rec := httptest.NewRecorder()
			ctx := e.NewContext(httptest.NewRequest(http.MethodPost, "/api/v2/system/database/legacy/cleanup", http.NoBody), rec)
			require.NoError(t, controller.StartLegacyCleanup(ctx))
			controller.Wait()

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			var response CleanupActionResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.Equal(t, notification.MsgErrCleanupAccessFailed, response.ErrorKey)
			state, _, _, _ := controller.cleanupStatus.Get()
			assert.Equal(t, CleanupStateIdle, state, "a refused POST must not enter in_progress")
		})
	}
}

// cleanupContenders is the number of goroutines racing for TryStart.
const cleanupContenders = 16

// TestCleanupStatus_TryStartHasOneWinner verifies that of many concurrent TryStart
// calls exactly one wins and the state becomes in_progress. Without the lock,
// -race reports the concurrent state access.
func TestCleanupStatus_TryStartHasOneWinner(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	cs := NewCleanupStatus()

	var winners atomic.Int32
	var wg sync.WaitGroup
	begin := make(chan struct{})
	for range cleanupContenders {
		wg.Go(func() {
			<-begin
			if cs.TryStart() {
				winners.Add(1)
			}
		})
	}
	close(begin)
	wg.Wait()

	assert.Equal(t, int32(1), winners.Load(), "exactly one TryStart may win")
	state, _, _, _ := cs.Get()
	assert.Equal(t, CleanupStateInProgress, state)
}

// TestCleanupStatus_SetGetReset verifies the Set, Get and Reset round trip.
func TestCleanupStatus_SetGetReset(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	cs := NewCleanupStatus()

	// Verify initial state
	state, errMsg, remaining, reclaimed := cs.Get()
	assert.Equal(t, CleanupStateIdle, state)
	assert.Empty(t, errMsg)
	assert.Nil(t, remaining)
	assert.Equal(t, int64(0), reclaimed)

	// Test Set
	cs.Set(CleanupStateCompleted, "", nil, 12345)
	state, errMsg, remaining, reclaimed = cs.Get()
	assert.Equal(t, CleanupStateCompleted, state)
	assert.Empty(t, errMsg)
	assert.Nil(t, remaining)
	assert.Equal(t, int64(12345), reclaimed)

	// Test Set with error
	cs.Set(CleanupStateFailed, "test error", []string{"table1", "table2"}, 0)
	state, errMsg, remaining, reclaimed = cs.Get()
	assert.Equal(t, CleanupStateFailed, state)
	assert.Equal(t, "test error", errMsg)
	assert.Equal(t, []string{"table1", "table2"}, remaining)
	assert.Equal(t, int64(0), reclaimed)

	// Test Reset
	cs.Reset()
	state, errMsg, remaining, reclaimed = cs.Get()
	assert.Equal(t, CleanupStateIdle, state)
	assert.Empty(t, errMsg)
	assert.Nil(t, remaining)
	assert.Equal(t, int64(0), reclaimed)
}
