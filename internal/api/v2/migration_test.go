// migration_test.go: Package api provides tests for migration API endpoints.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
)

// migrationTestMu protects the global stateManager and migrationWorker during tests.
var migrationTestMu sync.Mutex

// setupMigrationTestDB creates an in-memory SQLite database with migration state tables.
func setupMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err, "Failed to open in-memory database")

	// Auto-migrate the migration state tables
	err = db.AutoMigrate(&entities.MigrationState{}, &entities.MigrationDirtyID{})
	require.NoError(t, err, "Failed to auto-migrate tables")

	// Create the initial migration state row
	initialState := entities.MigrationState{
		ID:    1,
		State: entities.MigrationStatusIdle,
	}
	err = db.Create(&initialState).Error
	require.NoError(t, err, "Failed to create initial migration state")

	return db
}

// setupMigrationTestEnvironment creates a test environment for migration API tests.
// Note: Tests using this function must NOT be run in parallel due to global state.
func setupMigrationTestEnvironment(t *testing.T) (*echo.Echo, *Controller, *datastoreV2.StateManager, func()) {
	t.Helper()

	// Lock to prevent parallel tests from interfering
	migrationTestMu.Lock()

	e := echo.New()
	db := setupMigrationTestDB(t)

	sm := datastoreV2.NewStateManager(db)

	// Save previous state manager and worker to restore after test
	prevSM := stateManager
	prevWorker := migrationWorker

	// Set the global state manager for the handlers
	stateManager = sm
	migrationWorker = nil // No worker for basic tests

	mockDS := mocks.NewMockInterface(t)
	controller := &Controller{
		Echo:     e,
		Group:    e.Group("/api/v2"),
		Settings: getTestSettings(t),
		DS:       mockDS,
	}

	cleanup := func() {
		// Restore previous state
		stateManager = prevSM
		migrationWorker = prevWorker

		// Get underlying SQL DB and close it
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}

		// Unlock the mutex
		migrationTestMu.Unlock()
	}

	return e, controller, sm, cleanup
}

// TestGetMigrationStatus_Idle tests getting migration status when idle.
// Note: Not parallel due to global state modification.
func TestGetMigrationStatus_Idle(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "status")

	e, controller, _, cleanup := setupMigrationTestEnvironment(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/status", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetMigrationStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response MigrationStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, string(entities.MigrationStatusIdle), response.State)
	assert.True(t, response.CanStart)
	assert.False(t, response.CanPause)
	assert.False(t, response.CanResume)
	assert.False(t, response.CanCancel)
	assert.False(t, response.WorkerRunning)
	assert.False(t, response.WorkerPaused)
}

// TestStartMigration_Success tests starting migration successfully.
// Note: Not parallel due to global state modification.
func TestStartMigration_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm, cleanup := setupMigrationTestEnvironment(t)
	defer cleanup()

	body := `{"total_records": 1000}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/start", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.StartMigration(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response MigrationActionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Contains(t, response.Message, "1000 records")
	assert.Equal(t, string(entities.MigrationStatusDualWrite), response.State)

	// Verify state was updated
	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusDualWrite, state.State)
	assert.Equal(t, int64(1000), state.TotalRecords)
}

// TestStartMigration_AlreadyRunning tests starting migration when already running.
// Note: Not parallel due to global state modification.
func TestStartMigration_AlreadyRunning(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm, cleanup := setupMigrationTestEnvironment(t)
	defer cleanup()

	// Start migration first
	err := sm.StartMigration(100)
	require.NoError(t, err)
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)

	// Try to start again
	body := `{"total_records": 1000}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/start", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.StartMigration(ctx)
	// Should return conflict since already running
	require.NoError(t, err) // Error is handled in response
	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestPauseMigration_Success tests pausing migration successfully.
// Note: Not parallel due to global state modification.
//
//nolint:dupl // Similar structure to TestCancelMigration_Success but tests different behavior
func TestPauseMigration_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "pause")

	e, controller, sm, cleanup := setupMigrationTestEnvironment(t)
	defer cleanup()

	// Start migration first
	err := sm.StartMigration(100)
	require.NoError(t, err)
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/pause", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.PauseMigration(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response MigrationActionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, string(entities.MigrationStatusPaused), response.State)

	// Verify state was updated
	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusPaused, state.State)
}

// TestPauseMigration_NotRunning tests pausing when migration is not running.
// Note: Not parallel due to global state modification.
func TestPauseMigration_NotRunning(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "pause")

	e, controller, _, cleanup := setupMigrationTestEnvironment(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/pause", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.PauseMigration(ctx)
	require.NoError(t, err) // Error is handled in response
	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestResumeMigration_Success tests resuming migration successfully.
// Note: Not parallel due to global state modification.
func TestResumeMigration_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "resume")

	e, controller, sm, cleanup := setupMigrationTestEnvironment(t)
	defer cleanup()

	// Start and pause migration first
	err := sm.StartMigration(100)
	require.NoError(t, err)
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)
	err = sm.Pause()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/resume", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.ResumeMigration(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response MigrationActionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, string(entities.MigrationStatusDualWrite), response.State)

	// Verify state was updated
	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusDualWrite, state.State)
}

// TestResumeMigration_NotPaused tests resuming when migration is not paused.
// Note: Not parallel due to global state modification.
func TestResumeMigration_NotPaused(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "resume")

	e, controller, _, cleanup := setupMigrationTestEnvironment(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/resume", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.ResumeMigration(ctx)
	require.NoError(t, err) // Error is handled in response
	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestCancelMigration_Success tests cancelling migration successfully.
// Note: Not parallel due to global state modification.
//
//nolint:dupl // Similar structure to TestPauseMigration_Success but tests different behavior
func TestCancelMigration_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "cancel")

	e, controller, sm, cleanup := setupMigrationTestEnvironment(t)
	defer cleanup()

	// Start migration first
	err := sm.StartMigration(100)
	require.NoError(t, err)
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/cancel", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.CancelMigration(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response MigrationActionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, string(entities.MigrationStatusIdle), response.State)

	// Verify state was updated
	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusIdle, state.State)
}

// TestCancelMigration_NotRunning tests cancelling when migration is not running.
// Note: Not parallel due to global state modification.
func TestCancelMigration_NotRunning(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "cancel")

	e, controller, _, cleanup := setupMigrationTestEnvironment(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/cancel", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.CancelMigration(ctx)
	require.NoError(t, err) // Error is handled in response
	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestFormatDuration tests the duration formatting helper.
func TestFormatDuration(t *testing.T) {
	t.Parallel()
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "formatting")

	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "seconds only",
			duration: 45 * time.Second,
			expected: "45s",
		},
		{
			name:     "minutes and seconds",
			duration: 3*time.Minute + 30*time.Second,
			expected: "3m 30s",
		},
		{
			name:     "hours and minutes",
			duration: 2*time.Hour + 15*time.Minute,
			expected: "2h 15m",
		},
		{
			name:     "zero",
			duration: 0,
			expected: "0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := formatDuration(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestMigrationStatusResponse_Progress tests progress calculation in status response.
// Note: Not parallel due to global state modification.
func TestMigrationStatusResponse_Progress(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "progress")

	e, controller, sm, cleanup := setupMigrationTestEnvironment(t)
	defer cleanup()

	// Start migration
	err := sm.StartMigration(1000)
	require.NoError(t, err)
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)

	// Simulate some progress
	err = sm.IncrementProgress(500, 500)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/status", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.GetMigrationStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response MigrationStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, string(entities.MigrationStatusDualWrite), response.State)
	assert.Equal(t, int64(1000), response.TotalRecords)
	assert.Equal(t, int64(500), response.MigratedRecords)
	assert.InDelta(t, 50.0, response.ProgressPercent, 0.1)
	assert.Equal(t, uint(500), response.LastMigratedID)
	assert.True(t, response.IsDualWriteActive)
	assert.False(t, response.ShouldReadFromV2)
}

// TestGetMigrationStatus_NoStateManager tests behavior when state manager is not configured.
// Note: Not parallel due to global state modification.
func TestGetMigrationStatus_NoStateManager(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "error-handling")

	e := echo.New()
	controller := &Controller{
		Echo:     e,
		Group:    e.Group("/api/v2"),
		Settings: getTestSettings(t),
	}

	// Save and clear the global state manager
	prevSM := stateManager
	stateManager = nil
	defer func() { stateManager = prevSM }()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/status", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetMigrationStatus(ctx)
	require.NoError(t, err) // Error is handled in response
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
