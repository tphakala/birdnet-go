// migration_test.go: tests for the migration API endpoints.
package importsapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	"github.com/tphakala/birdnet-go/internal/notification"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
)

// testDatastoreWrapper wraps a mock interface and provides GORM DB access for tests.
// This allows prerequisite checks like checkSQLiteIntegrity to run properly.
type testDatastoreWrapper struct {
	*mocks.MockInterface
	db *gorm.DB
}

// GetDB implements the gormProvider interface for prerequisite checks.
func (w *testDatastoreWrapper) GetDB() *gorm.DB {
	return w.db
}

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
func setupMigrationTestEnvironment(t *testing.T) (*echo.Echo, *Handler, *datastoreV2.StateManager) {
	t.Helper()

	e := echo.New()
	db := setupMigrationTestDB(t)

	sm := datastoreV2.NewStateManager(db)

	// Set the global state manager for the handlers; no worker for basic tests.
	setMigrationState(t, sm, nil)

	// Register DB close cleanup
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	mockDS := mocks.NewMockInterface(t)
	mockRepo := mocks.NewMockDetectionRepository(t)

	// Set up common mock expectations for prerequisite checks
	// These are called by runPreflightChecks -> runCriticalPrerequisiteChecks
	mockDS.EXPECT().GetLastDetections(1).Return([]datastore.Note{}, nil).Maybe()
	mockRepo.EXPECT().CountAll(mock.Anything).Return(int64(100), nil).Maybe()

	// Create a wrapper that provides both the mock interface and the GORM DB
	// This is needed for checkSQLiteIntegrity which requires a real database
	testDS := &testDatastoreWrapper{
		MockInterface: mockDS,
		db:            db,
	}

	controller := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2"), DS: testDS, Repo: mockRepo}}
	controller.diskFreeFn = stubDiskFree
	controller.Settings.Store(getTestSettings(t))
	apitest.PublishTestSettings(t, controller.Settings.Load())

	return e, controller, sm
}

// TestGetMigrationStatus_Idle tests getting migration status when idle.
// Note: Not parallel due to global state modification.
func TestGetMigrationStatus_Idle(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "status")

	e, controller, _ := setupMigrationTestEnvironment(t)

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
	assert.Empty(t, response.AuxiliaryDataStatus, "no outcome recorded reads as unknown")
	assert.Empty(t, response.AuxiliaryDataError)
	assert.True(t, response.CanStart)
	assert.False(t, response.CanPause)
	assert.False(t, response.CanResume)
	assert.False(t, response.CanRetryValidation)
	assert.False(t, response.CanCancel)
	assert.False(t, response.WorkerRunning)
	assert.False(t, response.WorkerPaused)
	assert.Equal(t, "not_started", response.LegacyNotice)
}

// TestStartMigration_Success tests starting migration successfully.
// Note: Not parallel due to global state modification.
func TestStartMigration_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm := setupMigrationTestEnvironment(t)

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

// migrationActionTestPath is the request path the action tests post to; the
// handlers only log it.
const migrationActionTestPath = "/api/v2/system/database/migration/action"

// migrationTestTotalRecords is the record total the action tests start a migration with.
const migrationTestTotalRecords = 100

// toDualWrite moves the state machine from idle into dual-write.
func toDualWrite(t *testing.T, sm *datastoreV2.StateManager) {
	t.Helper()
	require.NoError(t, sm.StartMigration(migrationTestTotalRecords))
	require.NoError(t, sm.TransitionToDualWrite())
}

// TestStartMigration_AlreadyRunning tests that a start request while a
// migration is in progress is refused by the state check, and that the running
// migration is left untouched.
// Note: Not parallel due to global state modification.
func TestStartMigration_AlreadyRunning(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm := setupMigrationTestEnvironment(t)

	// The raw preflight error only reaches the response in debug mode.
	settings := getTestSettings(t)
	settings.WebServer.Debug = true
	controller.Settings.Store(settings)
	apitest.PublishTestSettings(t, settings)

	toDualWrite(t, sm)

	body := `{"total_records": 1000}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/start", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()

	require.NoError(t, controller.StartMigration(e.NewContext(req, rec))) // Error is handled in response
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp apicore.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, notification.MsgErrMigrationPreFlight, resp.ErrorKey)
	assert.Contains(t, resp.Error, "'Migration State'", "the state check must name the refusal")

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusDualWrite, state.State)
	assert.Equal(t, int64(migrationTestTotalRecords), state.TotalRecords)
}

// TestMigrationAction_FromDualWrite tests that pause and cancel succeed from
// dual-write: the response and the stored state both carry the new state.
// Note: Not parallel due to global state modification.
func TestMigrationAction_FromDualWrite(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "action")

	tests := []struct {
		name      string
		call      func(*Handler, echo.Context) error
		wantState entities.MigrationStatus
	}{
		{name: "pause", call: (*Handler).PauseMigration, wantState: entities.MigrationStatusPaused},
		{name: "cancel", call: (*Handler).CancelMigration, wantState: entities.MigrationStatusIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller, sm := setupMigrationTestEnvironment(t)
			toDualWrite(t, sm)

			req := httptest.NewRequest(http.MethodPost, migrationActionTestPath, http.NoBody)
			rec := httptest.NewRecorder()

			require.NoError(t, tt.call(controller, e.NewContext(req, rec)))
			assert.Equal(t, http.StatusOK, rec.Code)

			var response MigrationActionResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.True(t, response.Success)
			assert.Equal(t, string(tt.wantState), response.State)

			state, err := sm.GetState()
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state.State)
		})
	}
}

// TestMigrationAction_RefusedReturns409 tests that an action the state machine
// refuses answers 409 and leaves the stored migration as it was.
// Note: Not parallel due to global state modification.
func TestMigrationAction_RefusedReturns409(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "action")

	tests := []struct {
		name  string
		setup func(*testing.T, *datastoreV2.StateManager)
		call  func(*Handler, echo.Context) error
		// wantState and wantTotal are the stored state and record total after the refusal.
		wantState entities.MigrationStatus
		wantTotal int64
	}{
		{name: "pause when idle", call: (*Handler).PauseMigration, wantState: entities.MigrationStatusIdle},
		{name: "resume when not paused", call: (*Handler).ResumeMigration, wantState: entities.MigrationStatusIdle},
		{name: "cancel when idle", call: (*Handler).CancelMigration, wantState: entities.MigrationStatusIdle},
		{name: "retry validation when not failed", call: (*Handler).RetryValidation, wantState: entities.MigrationStatusIdle},
		{
			// Rollback is only allowed from completed; a refused one must not reset a live migration.
			name:      "rollback from dual-write",
			setup:     toDualWrite,
			call:      (*Handler).RollbackMigration,
			wantState: entities.MigrationStatusDualWrite,
			wantTotal: migrationTestTotalRecords,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller, sm := setupMigrationTestEnvironment(t)
			if tt.setup != nil {
				tt.setup(t, sm)
			}

			req := httptest.NewRequest(http.MethodPost, migrationActionTestPath, http.NoBody)
			rec := httptest.NewRecorder()

			require.NoError(t, tt.call(controller, e.NewContext(req, rec))) // Error is handled in response
			assert.Equal(t, http.StatusConflict, rec.Code)

			state, err := sm.GetState()
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state.State)
			assert.Equal(t, tt.wantTotal, state.TotalRecords)
		})
	}
}

// TestResumeMigration_Success tests resuming migration successfully.
// Note: Not parallel due to global state modification.
func TestResumeMigration_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "resume")

	e, controller, sm := setupMigrationTestEnvironment(t)

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

// TestRetryValidation_Success tests retrying validation after failure.
// Note: Not parallel due to global state modification.
func TestRetryValidation_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "retry-validation")

	e, controller, sm := setupMigrationTestEnvironment(t)

	// Setup: get to FAILED state
	require.NoError(t, sm.StartMigration(100))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.TransitionToValidating())
	require.NoError(t, sm.Fail())

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/retry-validation", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.RetryValidation(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response MigrationActionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, string(entities.MigrationStatusValidating), response.State)

	// Verify state was updated
	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusValidating, state.State)
}

// TestGetMigrationStatus_Failed tests getting migration status when in failed state.
// Note: Not parallel due to global state modification.
func TestGetMigrationStatus_Failed(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "status")

	e, controller, sm := setupMigrationTestEnvironment(t)

	// Setup: get to FAILED state
	require.NoError(t, sm.StartMigration(100))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.TransitionToValidatingWithAuxiliaryOutcome(entities.AuxiliaryDataStatusFailed, "weather: boom"))
	require.NoError(t, sm.Fail())
	require.NoError(t, sm.SetError("validation failed: v2 count (95) is less than legacy count (100)"))

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/status", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetMigrationStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response MigrationStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, string(entities.MigrationStatusFailed), response.State)
	assert.False(t, response.CanStart)
	assert.False(t, response.CanPause)
	assert.False(t, response.CanResume)
	assert.True(t, response.CanRetryValidation)
	assert.True(t, response.CanCancel)
	assert.Contains(t, response.ErrorMessage, "validation failed")
	assert.Equal(t, "needs_attention", response.LegacyNotice)
	assert.Equal(t, string(entities.AuxiliaryDataStatusFailed), response.AuxiliaryDataStatus)
	assert.Equal(t, "weather: boom", response.AuxiliaryDataError)
}

// TestGetMigrationStatus_AuxiliaryDataSucceeded checks that a recorded success
// is reported without an error text.
func TestGetMigrationStatus_AuxiliaryDataSucceeded(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "status")

	e, controller, sm := setupMigrationTestEnvironment(t)

	require.NoError(t, sm.StartMigration(100))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.TransitionToValidatingWithAuxiliaryOutcome(entities.AuxiliaryDataStatusSucceeded, ""))

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/status", http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.GetMigrationStatus(e.NewContext(req, rec)))

	var response MigrationStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, string(entities.AuxiliaryDataStatusSucceeded), response.AuxiliaryDataStatus)
	assert.Empty(t, response.AuxiliaryDataError)
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

	e, controller, sm := setupMigrationTestEnvironment(t)

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

// TestGetMigrationStatus_NoStateManager pins the response for a legacy install
// without a migration state manager: a 503 with the not-configured error key,
// and nothing logged at warn or error, because the dashboard banner requests
// this endpoint on every page load and the condition is reported once at startup.
// Note: Not parallel due to global state modification.
func TestGetMigrationStatus_NoStateManager(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "error-handling")

	buf := logtest.CaptureBufferAt(t, slog.LevelWarn)

	e := echo.New()
	controller := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2"), APILogger: logger.Global().Module("api")}}
	controller.Settings.Store(getTestSettings(t))

	setMigrationState(t, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/status", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetMigrationStatus(ctx)
	require.NoError(t, err) // Error is handled in response
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var resp apicore.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, http.StatusServiceUnavailable, resp.Code)
	assert.Equal(t, migrationNotConfiguredMessage, resp.Message)
	assert.Equal(t, notification.MsgErrMigrationNotConfigured, resp.ErrorKey)
	assert.NotEmpty(t, resp.CorrelationID)
	assert.Empty(t, buf.String(), "an expected missing state manager must not log at warn or error")
}

// TestRequireMigrationStateManager_NoStateManagerLogsError pins that a
// user-initiated migration action without a state manager is still logged as
// an error, unlike the polled status request.
// Note: Not parallel due to global state modification.
func TestRequireMigrationStateManager_NoStateManagerLogsError(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "error-handling")

	buf := logtest.CaptureBufferAt(t, slog.LevelWarn)

	e := echo.New()
	controller := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2"), APILogger: logger.Global().Module("api")}}
	controller.Settings.Store(getTestSettings(t))

	setMigrationState(t, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/start", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	next := func(echo.Context) error {
		t.Error("next handler must not run without a state manager")
		return nil
	}
	require.NoError(t, controller.requireMigrationStateManager(next)(ctx))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var resp apicore.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, migrationNotConfiguredMessage, resp.Message)
	assert.Equal(t, notification.MsgErrMigrationNotConfigured, resp.ErrorKey)
	assert.Contains(t, buf.String(), "API Error")
}

// TestGetMigrationStatus_V2OnlyLegacyNotice pins that an install running the
// v2-only datastore reports no legacy notice, whether or not a state manager
// is present, so the dashboard banner never shows on a migrated install.
// Note: Not parallel due to global state modification.
func TestGetMigrationStatus_V2OnlyLegacyNotice(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "status")

	t.Run("with state manager", func(t *testing.T) {
		e, controller, _ := setupMigrationTestEnvironment(t)

		setV2OnlyMode(t, true)

		response := getMigrationStatusResponse(t, e, controller)
		assert.True(t, response.IsV2OnlyMode)
		assert.Equal(t, "none", response.LegacyNotice)
	})

	t.Run("without state manager", func(t *testing.T) {
		e := echo.New()
		controller := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")}}
		controller.Settings.Store(getTestSettings(t))

		setMigrationState(t, nil, nil)
		setV2OnlyMode(t, true)

		response := getMigrationStatusResponse(t, e, controller)
		assert.Equal(t, string(entities.MigrationStatusCompleted), response.State)
		assert.Equal(t, "none", response.LegacyNotice)
	})
}

// getMigrationStatusResponse calls GetMigrationStatus and decodes a 200 response.
func getMigrationStatusResponse(t *testing.T, e *echo.Echo, controller *Handler) MigrationStatusResponse {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/status", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, controller.GetMigrationStatus(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	var response MigrationStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	return response
}

// seedPassingPrerequisiteResult commits a fresh passing prerequisites result to
// the handler's runner, as a finished run would, and checks it is reusable.
func seedPassingPrerequisiteResult(t *testing.T, h *Handler) {
	t.Helper()
	r := h.prereqRunner()
	r.mu.Lock()
	r.status = PrereqRunStatusCompleted
	r.result = &PrerequisitesResponse{AllPassed: true, CanStartMigration: true}
	r.resultFingerprint = h.dbFingerprint()
	r.finishedAt = r.clock()
	r.mu.Unlock()
	require.Equal(t, PrereqRunStatusCompleted, r.snapshot().Status, "the seeded result must be visible")
}

// TestMigrationStateChange_DropsPrerequisiteResult pins that a start, a cancel
// and a rollback each drop the stored prerequisites result, so a passing result
// cannot be reused across a migration state change.
// Note: Not parallel due to global state modification.
func TestMigrationStateChange_DropsPrerequisiteResult(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	tests := []struct {
		name  string
		setup func(*testing.T, *datastoreV2.StateManager)
		call  func(*Handler, echo.Context) error
		body  string
	}{
		{name: "start", setup: func(*testing.T, *datastoreV2.StateManager) {}, call: (*Handler).StartMigration, body: `{"total_records": 1000}`},
		{name: "cancel", setup: toDualWrite, call: (*Handler).CancelMigration},
		{name: "rollback", setup: toCompleted, call: (*Handler).RollbackMigration},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller, sm := setupMigrationTestEnvironment(t)
			tt.setup(t, sm)
			seedPassingPrerequisiteResult(t, controller)

			req := httptest.NewRequest(http.MethodPost, migrationActionTestPath, strings.NewReader(tt.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			require.NoError(t, tt.call(controller, e.NewContext(req, rec)))
			require.Equal(t, http.StatusOK, rec.Code)

			assert.Equal(t, PrereqRunStatusIdle, controller.prereqRunner().snapshot().Status)
		})
	}
}

// TestCancelMigration_KeepsPrerequisiteResultOnFailure pins that a rejected
// cancel leaves the stored result alone: the state did not change.
// Note: Not parallel due to global state modification.
func TestCancelMigration_KeepsPrerequisiteResultOnFailure(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	e, controller, sm := setupMigrationTestEnvironment(t)
	toCompleted(t, sm) // a completed migration cannot be cancelled
	seedPassingPrerequisiteResult(t, controller)

	rec := httptest.NewRecorder()
	require.NoError(t, controller.CancelMigration(e.NewContext(httptest.NewRequest(http.MethodPost, migrationActionTestPath, http.NoBody), rec)))
	require.Equal(t, http.StatusConflict, rec.Code)

	assert.Equal(t, PrereqRunStatusCompleted, controller.prereqRunner().snapshot().Status)
}

// toCompleted moves the state machine from idle to completed.
func toCompleted(t *testing.T, sm *datastoreV2.StateManager) {
	t.Helper()
	toDualWrite(t, sm)
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.TransitionToValidating())
	require.NoError(t, sm.TransitionToCutover())
	require.NoError(t, sm.Complete())
}

// TestStartMigration_ShutdownDuringPreflightDoesNotStart pins that a start whose
// preflight ended because the server is shutting down never starts a migration.
// Note: Not parallel due to global state modification.
func TestStartMigration_ShutdownDuringPreflightDoesNotStart(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm := setupMigrationTestEnvironment(t)
	// The server shuts down while the last critical check, the record count,
	// runs; that check still passes, so only the context says the run is void.
	lifecycle, cancel := context.WithCancel(t.Context())
	controller.SetTestContext(lifecycle, cancel)
	repo := mocks.NewMockDetectionRepository(t)
	repo.EXPECT().CountAll(mock.Anything).RunAndReturn(func(context.Context) (int64, error) {
		cancel()
		return int64(migrationTestTotalRecords), nil
	}).Once()
	controller.Repo = repo

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/start", strings.NewReader(`{"total_records": 1000}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.StartMigration(e.NewContext(req, rec)))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusIdle, state.State, "no migration may start while the server shuts down")
}

// failDualWriteTransitionSQL makes the state table refuse the move into dual-write,
// so the start path fails after sm.StartMigration succeeded.
const failDualWriteTransitionSQL = `CREATE TRIGGER fail_dual_write BEFORE UPDATE ON migration_states
WHEN NEW.state = 'dual_write' BEGIN SELECT RAISE(ABORT, 'forced failure'); END`

// TestStartMigration_FailedInitDropsPrerequisiteResult pins that a start whose
// dual-write transition fails, and is rolled back to idle, still drops the stored
// prerequisites result: the state moved and came back, so the result is void.
// Note: Not parallel due to global state modification.
func TestStartMigration_FailedInitDropsPrerequisiteResult(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm := setupMigrationTestEnvironment(t)
	db := controller.DS.(*testDatastoreWrapper).GetDB()
	require.NoError(t, db.Exec(failDualWriteTransitionSQL).Error)
	seedPassingPrerequisiteResult(t, controller)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/start", strings.NewReader(`{"total_records": 1000}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.StartMigration(e.NewContext(req, rec)))
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusIdle, state.State, "the failed start must be rolled back")
	assert.Equal(t, PrereqRunStatusIdle, controller.prereqRunner().snapshot().Status)
}

// TestStartMigration_ShutdownAfterChecksDoesNotStart pins that a shutdown that
// begins after the pre-flight checks passed, while the record count is
// fetched for the request, still keeps the migration from starting.
// Note: Not parallel due to global state modification.
func TestStartMigration_ShutdownAfterChecksDoesNotStart(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm := setupMigrationTestEnvironment(t)
	lifecycle, cancel := context.WithCancel(t.Context())
	controller.SetTestContext(lifecycle, cancel)
	// The first count belongs to the record_count check; the second is the
	// handler's own fallback count, which runs after the checks.
	var counts int
	repo := mocks.NewMockDetectionRepository(t)
	repo.EXPECT().CountAll(mock.Anything).RunAndReturn(func(context.Context) (int64, error) {
		counts++
		if counts == 2 {
			cancel()
		}
		return int64(migrationTestTotalRecords), nil
	}).Twice()
	controller.Repo = repo

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/start", strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.StartMigration(e.NewContext(req, rec)))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusIdle, state.State, "no migration may start once the server is stopping")
}
