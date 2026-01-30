// internal/api/v2/legacy_cleanup_test.go
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	_ "github.com/mattn/go-sqlite3" // SQLite driver for safety check test
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// createTestSettings creates settings for SQLite-based legacy cleanup tests.
func createTestSettings(legacyPath string) *conf.Settings {
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath
	settings.Output.MySQL.Enabled = false
	return settings
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

	controller := &Controller{
		Settings: settings,
		Echo:     e,
	}

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

	controller := &Controller{
		Settings: settings,
		Echo:     e,
	}

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

	controller := &Controller{
		Settings: settings,
		Echo:     e,
	}

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

	controller := &Controller{
		Settings: settings,
		Echo:     e,
	}

	// NOT in v2-only mode
	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = false
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	// Reset cleanup state
	resetCleanupState()

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

	controller := &Controller{
		Settings: settings,
		Echo:     e,
	}

	// Set v2-only mode
	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = true
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	// Reset cleanup state
	resetCleanupState()

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

	// Wait for async cleanup to complete (small timeout for test)
	time.Sleep(100 * time.Millisecond)

	// Verify files are deleted
	_, err = os.Stat(legacyPath)
	assert.True(t, os.IsNotExist(err), "Legacy DB file should be deleted")

	_, err = os.Stat(legacyPath + "-wal")
	assert.True(t, os.IsNotExist(err), "WAL file should be deleted")

	_, err = os.Stat(legacyPath + "-shm")
	assert.True(t, os.IsNotExist(err), "SHM file should be deleted")

	// Verify cleanup state is completed
	state, _, _, _ := getCleanupState()
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

	// Create a minimal SQLite database with migration_state table to simulate V2
	// The table structure must match what GORM expects for entities.MigrationState
	db, err := sql.Open("sqlite3", legacyPath)
	require.NoError(t, err)
	_, err = db.Exec(`
		CREATE TABLE migration_state (
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
		INSERT INTO migration_state (id, state) VALUES (1, 'completed');
	`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	settings := createTestSettings(legacyPath)

	controller := &Controller{
		Settings: settings,
		Echo:     e,
	}

	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = true
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	resetCleanupState()

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/legacy/cleanup", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.StartLegacyCleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code) // Request accepted, but async will fail

	// Wait for async cleanup to complete
	time.Sleep(200 * time.Millisecond)

	// Verify cleanup failed due to safety check
	state, errMsg, _, _ := getCleanupState()
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

	controller := &Controller{
		Settings: settings,
		Echo:     e,
	}

	prevV2OnlyMode := isV2OnlyMode
	isV2OnlyMode = true
	t.Cleanup(func() { isV2OnlyMode = prevV2OnlyMode })

	// Set cleanup as already in progress
	setCleanupState(CleanupStateInProgress, "", nil, 0)
	t.Cleanup(resetCleanupState)

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
