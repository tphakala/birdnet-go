// internal/api/v2/imports/legacy_cleanup_test.go
package importsapi

import (
	"context"
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
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/notification"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
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
	if settings.Output.SQLite.Enabled {
		// The failed-consolidation layout: the process runs on the _v2 sidecar and
		// the configured path is the legacy file, the only layout where the SQLite
		// delete is legitimate.
		c.V2Manager = &fakeV2Manager{
			path: datastoreV2.V2MigrationPathFromConfigured(settings.Output.SQLite.Path),
			db:   newV2StateDB(tb, defaultSQLiteFixtureConfig()),
		}
	}
	return c
}

// Fixture values for the SQLite cleanup tests. The watermark and the ids below
// it are in both databases; ids above it are the tail.
const (
	fixtureLastMigratedID = 10
	fixtureTailID         = fixtureLastMigratedID + 1
)

// fixtureAttemptStart is the migration attempt start shared by StartedAt and
// AuxiliaryDataStartedAt, so the recorded outcome belongs to the attempt. A whole
// second, so it survives a database round trip unchanged.
var fixtureAttemptStart = time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)

// sqliteFixtureConfig describes the migration state and databases of a SQLite
// cleanup fixture.
type sqliteFixtureConfig struct {
	state       entities.MigrationStatus
	aux         entities.AuxiliaryDataStatus
	relatedErr  string
	legacyTable string // table holding the legacy ids; "notes" in a legacy database
	legacyIDs   []uint
	v2IDs       []uint
	// staleAux records the auxiliary outcome against an earlier migration attempt.
	staleAux bool
	// dirtyIDs leaves a pending dirty ID in the v2 database.
	dirtyIDs bool
}

// defaultSQLiteFixtureConfig is a fully proven migration: completed, auxiliary
// data succeeded, no related-data error, every legacy id in v2.
func defaultSQLiteFixtureConfig() *sqliteFixtureConfig {
	ids := make([]uint, 0, fixtureLastMigratedID)
	for id := uint(1); id <= fixtureLastMigratedID; id++ {
		ids = append(ids, id)
	}
	return &sqliteFixtureConfig{
		state:       entities.MigrationStatusCompleted,
		aux:         entities.AuxiliaryDataStatusSucceeded,
		legacyTable: "notes",
		legacyIDs:   slices.Clone(ids),
		v2IDs:       slices.Clone(ids),
	}
}

// newV2StateDB creates the v2 database of a fixture: a migration state row and a
// detections table holding cfg.v2IDs.
func newV2StateDB(tb testing.TB, cfg *sqliteFixtureConfig) *gorm.DB {
	tb.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(tb.TempDir(), "v2.db")), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(tb, err)
	sqlDB, err := db.DB()
	require.NoError(tb, err)
	tb.Cleanup(func() { assert.NoError(tb, sqlDB.Close()) })

	require.NoError(tb, db.AutoMigrate(&entities.MigrationState{}))
	attempt := fixtureAttemptStart
	auxAttempt := attempt
	if cfg.staleAux {
		auxAttempt = attempt.Add(-time.Hour)
	}
	require.NoError(tb, db.Create(&entities.MigrationState{
		ID:                     1,
		State:                  cfg.state,
		StartedAt:              &attempt,
		AuxiliaryDataStartedAt: &auxAttempt,
		AuxiliaryDataStatus:    cfg.aux,
		RelatedDataError:       cfg.relatedErr,
		LastMigratedID:         fixtureLastMigratedID,
	}).Error)
	require.NoError(tb, db.Exec("CREATE TABLE detections (id INTEGER PRIMARY KEY)").Error)
	for _, id := range cfg.v2IDs {
		require.NoError(tb, db.Exec("INSERT INTO detections (id) VALUES (?)", id).Error)
	}
	if cfg.dirtyIDs {
		require.NoError(tb, db.Exec("CREATE TABLE migration_dirty_ids (id INTEGER PRIMARY KEY)").Error)
		require.NoError(tb, db.Exec("INSERT INTO migration_dirty_ids (id) VALUES (?)", fixtureTailID).Error)
	}
	return db
}

// writeLegacySQLite creates a real SQLite database at path with one table holding ids.
func writeLegacySQLite(tb testing.TB, path, table string, ids []uint) {
	tb.Helper()
	db, err := sql.Open("sqlite3", path)
	require.NoError(tb, err)
	defer func() { assert.NoError(tb, db.Close()) }()
	_, err = db.Exec("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY)")
	require.NoError(tb, err)
	for _, id := range ids {
		_, err = db.Exec("INSERT INTO "+table+" (id) VALUES (?)", id)
		require.NoError(tb, err)
	}
}

// sqliteCleanupFixture is a failed-consolidation layout: a real legacy database at
// legacyPath and a manager that opened the _v2 sidecar beside it.
type sqliteCleanupFixture struct {
	legacyPath string
	mgr        *fakeV2Manager
}

// newSQLiteCleanupFixture builds the legacy database and the manager for cfg.
func newSQLiteCleanupFixture(tb testing.TB, cfg *sqliteFixtureConfig) *sqliteCleanupFixture {
	tb.Helper()
	legacyPath := filepath.Join(tb.TempDir(), "birdnet.db")
	writeLegacySQLite(tb, legacyPath, cfg.legacyTable, cfg.legacyIDs)
	return &sqliteCleanupFixture{
		legacyPath: legacyPath,
		mgr: &fakeV2Manager{
			path: datastoreV2.V2MigrationPathFromConfigured(legacyPath),
			db:   newV2StateDB(tb, cfg),
		},
	}
}

// handler returns a v2-only handler wired to the fixture.
func (f *sqliteCleanupFixture) handler(t *testing.T) *Handler {
	t.Helper()
	c := createLegacyTestHandler(t, echo.New(), createTestSettings(f.legacyPath))
	c.V2Manager = f.mgr
	enableV2OnlyMode(t)
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

	// A real legacy database whose migration is proven.
	fx := newSQLiteCleanupFixture(t, defaultSQLiteFixtureConfig())
	legacyPath := fx.legacyPath

	// Create WAL and SHM files
	err := os.WriteFile(legacyPath+"-wal", []byte("wal"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(legacyPath+"-shm", []byte("shm"), 0o600)
	require.NoError(t, err)

	controller := fx.handler(t)
	e := controller.Echo

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

// TestStartLegacyCleanup_SQLite_RefusesTargetWithoutNotesTable tests that the POST
// refuses a target that holds a migration state table but no legacy notes table.
func TestStartLegacyCleanup_SQLite_RefusesTargetWithoutNotesTable(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	e := echo.New()
	tmpDir := t.TempDir()

	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	// A minimal SQLite database that looks like a v2 database: it has a
	// migration_states table and no notes table.
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
	enableV2OnlyMode(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/legacy/cleanup", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.StartLegacyCleanup(ctx)
	require.NoError(t, err)
	// The target holds no legacy notes table, so the proof refuses synchronously.
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var response CleanupActionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, notification.MsgErrCleanupSafetyCheck, response.ErrorKey)
	state, _, _, _ := controller.cleanupStatus.Get()
	assert.Equal(t, CleanupStateIdle, state, "a refused POST must not enter in_progress")

	// Verify file was NOT deleted
	_, err = os.Stat(legacyPath)
	assert.NoError(t, err, "the refused target must not be deleted")
}

// TestStartLegacyCleanup_AlreadyInProgress tests cleanup when already running.
func TestStartLegacyCleanup_AlreadyInProgress(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	// The proof runs before the in-progress check, so the target must be proven.
	controller := newSQLiteCleanupFixture(t, defaultSQLiteFixtureConfig()).handler(t)
	e := controller.Echo

	// Set cleanup as already in progress
	controller.cleanupStatus.Set(CleanupStateInProgress, "", nil, 0)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/legacy/cleanup", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.StartLegacyCleanup(ctx)
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

// enableV2OnlyMode switches the package-wide v2-only flag on for one test.
func enableV2OnlyMode(t *testing.T) {
	t.Helper()
	prev := isV2OnlyMode
	isV2OnlyMode = true
	t.Cleanup(func() { isV2OnlyMode = prev })
}

// wantLegacyOnly spells out the legacy-only table names so a name dropped from
// the production list fails the test.
var wantLegacyOnly = []string{"note_reviews", "note_comments", "note_locks", "results", "notes"}

// TestMySQLLegacyCandidates pins the candidate rule: a bare table prefix means
// the legacy-named tables are live v2 tables, and a prefixed schema offers only
// the legacy-only tables, never the shared auxiliary ones.
func TestMySQLLegacyCandidates(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	tests := []struct {
		name   string
		prefix string
		want   []string
	}{
		{"bare schema offers nothing", "", nil},
		{"prefixed schema offers only the legacy-only tables", "v2_", wantLegacyOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mysqlLegacyCandidates(tt.prefix))
		})
	}
}

// TestMySQLLegacyCandidates_DropOrder verifies the children-first order the
// foreign keys on notes require.
func TestMySQLLegacyCandidates_DropOrder(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	got := mysqlLegacyCandidates("v2_")
	for _, child := range []string{"note_reviews", "note_comments", "note_locks", "results"} {
		assert.Less(t, slices.Index(got, child), slices.Index(got, "notes"), "%s must be dropped before notes", child)
	}
}

// TestDropMySQLLegacyTables_Cancelled verifies a cancelled context drops nothing
// and reports every table as left, without touching the connection.
func TestDropMySQLLegacyTables_Cancelled(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	controller := createLegacyTestHandler(t, echo.New(), createMySQLTestSettings())

	remaining, err := controller.dropMySQLLegacyTables(ctx, nil, []LegacyTableInfo{{Name: "notes"}, {Name: "results"}})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{"notes", "results"}, remaining)
}

// TestGetLegacyStatus_MySQL_Refuses verifies the status call fails closed when no
// connection or no usable MySQL manager is available.
func TestGetLegacyStatus_MySQL_Refuses(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	tests := []struct {
		name string
		mgr  datastoreV2.Manager
	}{
		{"manager without a connection", &fakeV2Manager{isMySQL: true, tablePrefix: "v2_"}},
		{"no v2 manager", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := createLegacyTestHandler(t, e, createMySQLTestSettings())
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

	tests := []struct {
		name string
		mgr  datastoreV2.Manager
	}{
		{"manager without a connection", &fakeV2Manager{isMySQL: true, tablePrefix: "v2_"}},
		{"no v2 manager", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := createLegacyTestHandler(t, e, createMySQLTestSettings())
			if tt.mgr != nil {
				controller.V2Manager = tt.mgr
			}
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

// legacyCleanupCall runs GET status and then POST cleanup against the handler and
// waits for any scheduled work, returning both decoded responses and the POST code.
func legacyCleanupCall(t *testing.T, c *Handler) (status LegacyStatusResponse, post CleanupActionResponse, postCode int) {
	t.Helper()
	e := c.Echo

	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/api/v2/system/database/legacy/status", http.NoBody), rec)
	require.NoError(t, c.GetLegacyStatus(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))

	rec = httptest.NewRecorder()
	ctx = e.NewContext(httptest.NewRequest(http.MethodPost, "/api/v2/system/database/legacy/cleanup", http.NoBody), rec)
	require.NoError(t, c.StartLegacyCleanup(ctx))
	c.Wait()
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &post))
	return status, post, rec.Code
}

// TestLegacyCleanup_BackendFollowsOpenedDatabase pins that the cleanup picks the
// SQLite delete or the MySQL drop from the database the process opened, not from
// the live settings, which only take effect after a restart.
func TestLegacyCleanup_BackendFollowsOpenedDatabase(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	t.Run("manager MySQL, settings switched to SQLite", func(t *testing.T) {
		legacyPath := filepath.Join(t.TempDir(), "birdnet.db")
		require.NoError(t, os.WriteFile(legacyPath, []byte("legacy"), 0o600))
		c := createLegacyTestHandler(t, echo.New(), createTestSettings(legacyPath))
		c.V2Manager = &fakeV2Manager{isMySQL: true, tablePrefix: "v2_"}
		enableV2OnlyMode(t)

		status, post, code := legacyCleanupCall(t, c)

		assert.False(t, status.CanCleanup)
		assert.Equal(t, notification.MsgErrCleanupAccessFailed, status.ErrorKey)
		assert.Equal(t, http.StatusBadRequest, code)
		assert.False(t, post.Success)
		state, _, _, _ := c.cleanupStatus.Get()
		assert.Equal(t, CleanupStateIdle, state)
		assert.FileExists(t, legacyPath, "the settings switch must not make the SQLite delete reachable")
	})

	t.Run("manager SQLite, settings switched to MySQL", func(t *testing.T) {
		fx := newSQLiteCleanupFixture(t, defaultSQLiteFixtureConfig())
		legacyPath := fx.legacyPath
		settings := createMySQLTestSettings()
		settings.Output.SQLite.Path = legacyPath
		c := createLegacyTestHandler(t, echo.New(), settings)
		c.V2Manager = fx.mgr
		enableV2OnlyMode(t)

		status, post, code := legacyCleanupCall(t, c)

		assert.True(t, status.CanCleanup)
		assert.Equal(t, legacyPath, status.Location)
		assert.Equal(t, http.StatusOK, code)
		assert.True(t, post.Success)
		assert.NoFileExists(t, legacyPath)
	})
}

// TestLegacyCleanup_SQLiteTargetRefusals pins every reason the SQLite delete
// refuses before touching the file.
func TestLegacyCleanup_SQLiteTargetRefusals(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	tests := []struct {
		name         string
		noManager    bool
		managerPath  func(dir string) string
		settingsPath func(dir string) string
		statusExists bool
		statusKey    string
		postKey      string
	}{
		{
			name:         "no manager",
			noManager:    true,
			settingsPath: func(dir string) string { return filepath.Join(dir, "birdnet.db") },
			statusExists: false,
			statusKey:    notification.MsgErrCleanupAccessFailed,
			postKey:      notification.MsgErrCleanupAccessFailed,
		},
		{
			name:         "path changed since start",
			managerPath:  func(dir string) string { return filepath.Join(dir, "boot_v2.db") },
			settingsPath: func(dir string) string { return filepath.Join(dir, "other.db") },
			statusExists: true,
			statusKey:    notification.MsgErrCleanupRestartRequired,
			postKey:      notification.MsgErrCleanupRestartNeeded,
		},
		{
			name:         "path is the active database",
			managerPath:  func(dir string) string { return filepath.Join(dir, "birdnet.db") },
			settingsPath: func(dir string) string { return filepath.Join(dir, "birdnet.db") },
			statusExists: true,
			statusKey:    notification.MsgErrCleanupSafetyCheck,
			postKey:      notification.MsgErrCleanupSafetyCheck,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			target := tt.settingsPath(dir)
			// Plain bytes, so a schema probe alone would not stop the delete.
			require.NoError(t, os.WriteFile(target, []byte("plain bytes"), 0o600))
			c := createLegacyTestHandler(t, echo.New(), createTestSettings(target))
			if tt.noManager {
				c.V2Manager = nil
			} else {
				c.V2Manager = &fakeV2Manager{path: tt.managerPath(dir)}
			}
			enableV2OnlyMode(t)

			status, post, code := legacyCleanupCall(t, c)

			assert.Equal(t, tt.statusExists, status.Exists)
			assert.False(t, status.CanCleanup)
			assert.Equal(t, tt.statusKey, status.ErrorKey)
			assert.Equal(t, http.StatusBadRequest, code)
			assert.False(t, post.Success)
			assert.Equal(t, tt.postKey, post.ErrorKey)
			state, _, _, _ := c.cleanupStatus.Get()
			assert.Equal(t, CleanupStateIdle, state, "a refused POST must not enter in_progress")
			assert.FileExists(t, target)
		})
	}
}

// TestCleanupSQLiteLegacy_RefusesUnownedPath pins the background recheck: a path
// that is not the legacy companion of the opened database is never deleted, even
// when the job is handed it directly.
func TestCleanupSQLiteLegacy_RefusesUnownedPath(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	dir := t.TempDir()
	otherPath := filepath.Join(dir, "other.db")
	require.NoError(t, os.WriteFile(otherPath, []byte("plain bytes"), 0o600))
	c := createLegacyTestHandler(t, echo.New(), createTestSettings(otherPath))
	mgr := &fakeV2Manager{path: datastoreV2.V2MigrationPathFromConfigured(filepath.Join(dir, "boot.db"))}

	err := c.cleanupSQLiteLegacy(t.Context(), mgr, otherPath)

	require.Error(t, err)
	assert.FileExists(t, otherPath)
}

// TestLegacyCleanup_SQLiteRequiresMigrationProof pins that the SQLite delete is
// offered only when the migration state and the target prove the legacy data is in
// v2. The status call checks the state only; the POST also reads the target.
func TestLegacyCleanup_SQLiteRequiresMigrationProof(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	tests := []struct {
		name      string
		configure func(cfg *sqliteFixtureConfig)
		mutate    func(t *testing.T, fx *sqliteCleanupFixture)
		// statusKey is the error key of the status refusal; empty means the card is offered.
		statusKey string
		// postKey is the error key of the POST refusal; empty means the delete runs.
		postKey string
	}{
		{name: "migration proven"},
		{
			name:      "auxiliary failed",
			configure: func(cfg *sqliteFixtureConfig) { cfg.aux = entities.AuxiliaryDataStatusFailed },
			statusKey: notification.MsgErrCleanupSafetyCheck,
			postKey:   notification.MsgErrCleanupSafetyCheck,
		},
		{
			name:      "auxiliary not recorded",
			configure: func(cfg *sqliteFixtureConfig) { cfg.aux = entities.AuxiliaryDataStatusUnknown },
			statusKey: notification.MsgErrCleanupSafetyCheck,
			postKey:   notification.MsgErrCleanupSafetyCheck,
		},
		{
			name:      "auxiliary outcome belongs to an earlier attempt",
			configure: func(cfg *sqliteFixtureConfig) { cfg.staleAux = true },
			statusKey: notification.MsgErrCleanupSafetyCheck,
			postKey:   notification.MsgErrCleanupSafetyCheck,
		},
		{
			name:      "dirty IDs pending sync to v2",
			configure: func(cfg *sqliteFixtureConfig) { cfg.dirtyIDs = true },
			postKey:   notification.MsgErrCleanupSafetyCheck,
		},
		{
			name:      "fresh install database",
			configure: func(cfg *sqliteFixtureConfig) { cfg.aux = entities.AuxiliaryDataStatusNotRequired },
			statusKey: notification.MsgErrCleanupNoLegacyDB,
			postKey:   notification.MsgErrCleanupNoLegacyDB,
		},
		{
			name:      "migration not completed",
			configure: func(cfg *sqliteFixtureConfig) { cfg.state = entities.MigrationStatusMigrating },
			statusKey: notification.MsgErrCleanupRestartRequired,
			postKey:   notification.MsgErrCleanupRestartNeeded,
		},
		{
			name:      "related data error",
			configure: func(cfg *sqliteFixtureConfig) { cfg.relatedErr = "reviews failed" },
			statusKey: notification.MsgErrCleanupSafetyCheck,
			postKey:   notification.MsgErrCleanupSafetyCheck,
		},
		{
			name:      "target is not a legacy database",
			configure: func(cfg *sqliteFixtureConfig) { cfg.legacyTable = "other" },
			postKey:   notification.MsgErrCleanupSafetyCheck,
		},
		{
			name:      "legacy tail missing from v2",
			configure: func(cfg *sqliteFixtureConfig) { cfg.legacyIDs = append(cfg.legacyIDs, fixtureTailID) },
			postKey:   notification.MsgErrCleanupSafetyCheck,
		},
		{
			name: "legacy tail present in v2",
			configure: func(cfg *sqliteFixtureConfig) {
				cfg.legacyIDs = append(cfg.legacyIDs, fixtureTailID)
				cfg.v2IDs = append(cfg.v2IDs, fixtureTailID)
			},
		},
		{
			name: "target unreadable",
			mutate: func(t *testing.T, fx *sqliteCleanupFixture) {
				t.Helper()
				require.NoError(t, os.WriteFile(fx.legacyPath, []byte("not a sqlite database"), 0o600))
			},
			postKey: notification.MsgErrCleanupAccessFailed,
		},
		{
			name: "migration state unreadable",
			mutate: func(t *testing.T, fx *sqliteCleanupFixture) {
				t.Helper()
				require.NoError(t, fx.mgr.db.Exec("DROP TABLE migration_states").Error)
			},
			statusKey: notification.MsgErrCleanupAccessFailed,
			postKey:   notification.MsgErrCleanupAccessFailed,
		},
		{
			name: "legacy file missing",
			mutate: func(t *testing.T, fx *sqliteCleanupFixture) {
				t.Helper()
				require.NoError(t, os.Remove(fx.legacyPath))
			},
			statusKey: notification.MsgErrCleanupNoLegacyDB,
			postKey:   notification.MsgErrCleanupNoLegacyDB,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultSQLiteFixtureConfig()
			if tt.configure != nil {
				tt.configure(cfg)
			}
			fx := newSQLiteCleanupFixture(t, cfg)
			if tt.mutate != nil {
				tt.mutate(t, fx)
			}
			c := fx.handler(t)

			status, post, code := legacyCleanupCall(t, c)

			assert.Equal(t, tt.statusKey == "", status.CanCleanup)
			assert.Equal(t, tt.statusKey, status.ErrorKey)
			assert.Equal(t, tt.name != "legacy file missing", status.Exists, "the status reports the legacy file whenever it is on disk")
			assert.Equal(t, tt.postKey, post.ErrorKey)
			if tt.postKey == "" {
				assert.Equal(t, http.StatusOK, code)
				assert.True(t, post.Success)
				assert.NoFileExists(t, fx.legacyPath)
				return
			}
			assert.Equal(t, http.StatusBadRequest, code)
			assert.False(t, post.Success)
			state, _, _, _ := c.cleanupStatus.Get()
			assert.Equal(t, CleanupStateIdle, state, "a refused POST must not enter in_progress")
			if tt.name != "legacy file missing" {
				assert.FileExists(t, fx.legacyPath)
			}
		})
	}
}

// TestCleanupSQLiteLegacy_RechecksMigrationProof pins the drop-time recheck: a
// legacy record that appears beyond the watermark after the POST was accepted
// stops the delete.
func TestCleanupSQLiteLegacy_RechecksMigrationProof(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	fx := newSQLiteCleanupFixture(t, defaultSQLiteFixtureConfig())
	c := fx.handler(t)

	db, err := sql.Open("sqlite3", fx.legacyPath)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO notes (id) VALUES (?)", fixtureTailID)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	err = c.cleanupSQLiteLegacy(t.Context(), fx.mgr, fx.legacyPath)

	require.Error(t, err)
	assert.FileExists(t, fx.legacyPath)
}

// TestLegacyCleanup_MySQL_RequiresAuxiliaryOutcome pins that the MySQL resolver
// applies the auxiliary outcome gate before it lists any table. The backing store
// is SQLite, so a passing gate ends at the table listing.
func TestLegacyCleanup_MySQL_RequiresAuxiliaryOutcome(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	tests := []struct {
		name      string
		aux       entities.AuxiliaryDataStatus
		wantKey   string
		wantAfter string // reason of the listing failure that follows a passed gate
	}{
		{name: "not recorded", aux: entities.AuxiliaryDataStatusUnknown, wantKey: notification.MsgErrCleanupSafetyCheck},
		{name: "failed", aux: entities.AuxiliaryDataStatusFailed, wantKey: notification.MsgErrCleanupSafetyCheck},
		{name: "not required is a fresh install with no legacy database", aux: entities.AuxiliaryDataStatusNotRequired, wantKey: notification.MsgErrCleanupNoLegacyDB},
		{name: "succeeded passes the gate", aux: entities.AuxiliaryDataStatusSucceeded, wantKey: notification.MsgErrCleanupAccessFailed, wantAfter: reasonListTablesFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultSQLiteFixtureConfig()
			cfg.aux = tt.aux
			db := newV2StateDB(t, cfg)
			c := createLegacyTestHandler(t, echo.New(), createMySQLTestSettings())
			c.V2Manager = &fakeV2Manager{isMySQL: true, tablePrefix: "v2_", db: db}
			enableV2OnlyMode(t)

			status, post, code := legacyCleanupCall(t, c)

			assert.False(t, status.CanCleanup)
			assert.Equal(t, tt.wantKey, status.ErrorKey)
			assert.Equal(t, http.StatusBadRequest, code)
			assert.Equal(t, tt.wantKey, post.ErrorKey)
			if tt.wantAfter != "" {
				assert.Equal(t, tt.wantAfter, status.Reason)
			}
		})
	}
}

// TestCleanupSQLiteLegacy_RefusesV2SchemaTarget pins the last guard before the
// delete: a target that passes the migration proof (it has an empty notes table, so
// no tail) but carries a completed v2 migration state is never removed.
func TestCleanupSQLiteLegacy_RefusesV2SchemaTarget(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "unit")

	cfg := defaultSQLiteFixtureConfig()
	cfg.legacyIDs, cfg.v2IDs = nil, nil
	fx := newSQLiteCleanupFixture(t, cfg)
	c := fx.handler(t)
	db, err := sql.Open("sqlite3", fx.legacyPath)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE migration_states (id INTEGER PRIMARY KEY, state TEXT NOT NULL DEFAULT 'completed');
		INSERT INTO migration_states (id, state) VALUES (1, 'completed');`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	err = c.cleanupSQLiteLegacy(t.Context(), fx.mgr, fx.legacyPath)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "V2 database")
	assert.FileExists(t, fx.legacyPath)
}
