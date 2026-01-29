// prerequisites_test.go: Package api provides tests for prerequisites API endpoint.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"

	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
)

// TestGetPrerequisites_AllPassed tests the prerequisites endpoint when all checks pass.
// Note: Not parallel due to global state modification.
func TestGetPrerequisites_AllPassed(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	e, controller, _ := setupPrerequisitesTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/prerequisites", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetPrerequisites(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response PrerequisitesResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// When all checks pass, can_start_migration should be true (unless v2-only mode)
	assert.True(t, response.AllPassed, "AllPassed should be true when no critical failures")
	assert.Equal(t, 0, response.CriticalFailures, "Should have no critical failures")
	assert.NotEmpty(t, response.Checks, "Should have check results")
	assert.False(t, response.CheckedAt.IsZero(), "CheckedAt should be set")
}

// TestGetPrerequisites_Response_Structure tests that the response has the expected structure.
func TestGetPrerequisites_Response_Structure(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	e, controller, _ := setupPrerequisitesTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/prerequisites", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetPrerequisites(ctx)
	require.NoError(t, err)

	var response PrerequisitesResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify expected checks are present
	checkIDs := make(map[string]bool)
	for _, check := range response.Checks {
		checkIDs[check.ID] = true

		// Each check should have required fields
		assert.NotEmpty(t, check.ID, "Check ID should not be empty")
		assert.NotEmpty(t, check.Name, "Check Name should not be empty")
		assert.NotEmpty(t, check.Severity, "Check Severity should not be empty")
		assert.NotEmpty(t, check.Status, "Check Status should not be empty")

		// Status should be one of the valid values
		validStatuses := []string{CheckStatusPassed, CheckStatusFailed, CheckStatusWarning, CheckStatusSkipped, CheckStatusError}
		assert.Contains(t, validStatuses, check.Status, "Check status should be valid")

		// Severity should be critical or warning
		assert.Contains(t, []string{CheckSeverityCritical, CheckSeverityWarning}, check.Severity, "Check severity should be valid")
	}

	// Verify critical checks are included
	assert.True(t, checkIDs["state_idle"], "Should include state_idle check")
	assert.True(t, checkIDs["disk_space"], "Should include disk_space check")
	assert.True(t, checkIDs["legacy_accessible"], "Should include legacy_accessible check")
	assert.True(t, checkIDs["record_count"], "Should include record_count check")
}

// TestCheckStateIdle_Idle tests checkStateIdle when migration is in IDLE state.
func TestCheckStateIdle_Idle(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkStateIdle()

	assert.Equal(t, "state_idle", check.ID)
	assert.Equal(t, CheckStatusPassed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "IDLE")
}

// TestCheckStateIdle_NotIdle tests checkStateIdle when migration is in a non-IDLE state.
func TestCheckStateIdle_NotIdle(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, sm := setupPrerequisitesTestEnvironment(t)

	// Set state to migrating by going through the state machine
	err := sm.StartMigration(100)
	require.NoError(t, err)
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)
	err = sm.TransitionToMigrating()
	require.NoError(t, err)

	check := controller.checkStateIdle()

	assert.Equal(t, "state_idle", check.ID)
	assert.Equal(t, CheckStatusFailed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "must be IDLE")
}

// TestCheckLegacyAccessible_Success tests checkLegacyAccessible when database is accessible.
func TestCheckLegacyAccessible_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkLegacyAccessible()

	assert.Equal(t, "legacy_accessible", check.ID)
	assert.Equal(t, CheckStatusPassed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "readable")
}

// TestCheckLegacyAccessible_NilDS tests checkLegacyAccessible when datastore is nil.
func TestCheckLegacyAccessible_NilDS(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Controller{DS: nil}

	check := controller.checkLegacyAccessible()

	assert.Equal(t, "legacy_accessible", check.ID)
	assert.Equal(t, CheckStatusError, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "not available")
}

// TestCheckRecordCount_Success tests checkRecordCount when count succeeds.
func TestCheckRecordCount_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkRecordCount()

	assert.Equal(t, "record_count", check.ID)
	assert.Equal(t, CheckStatusPassed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "records to migrate")
}

// TestCheckRecordCount_NilRepo tests checkRecordCount when repository is nil.
func TestCheckRecordCount_NilRepo(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Controller{Repo: nil}

	check := controller.checkRecordCount()

	assert.Equal(t, "record_count", check.ID)
	assert.Equal(t, CheckStatusError, check.Status)
	assert.Contains(t, check.Message, "not available")
}

// TestCheckSQLiteIntegrity_Success tests checkSQLiteIntegrity with a healthy database.
func TestCheckSQLiteIntegrity_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkSQLiteIntegrity()

	assert.Equal(t, "sqlite_integrity", check.ID)
	assert.Equal(t, CheckStatusPassed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "verified")
}

// TestCheckSQLiteIntegrity_NilDB tests checkSQLiteIntegrity when database is nil.
func TestCheckSQLiteIntegrity_NilDB(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Controller{DS: nil}

	check := controller.checkSQLiteIntegrity()

	assert.Equal(t, "sqlite_integrity", check.ID)
	assert.Equal(t, CheckStatusError, check.Status)
	assert.Contains(t, check.Message, errMsgDBConnectionUnavailable)
}

// TestCheckMemoryAvailable tests checkMemoryAvailable function.
func TestCheckMemoryAvailable(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Controller{}

	check := controller.checkMemoryAvailable()

	assert.Equal(t, "memory_available", check.ID)
	assert.Equal(t, CheckSeverityWarning, check.Severity)
	// Status depends on actual system memory, so just verify it's valid
	assert.Contains(t, []string{CheckStatusPassed, CheckStatusWarning, CheckStatusSkipped}, check.Status)
}

// TestCheckExistingV2Data_NilManager tests checkExistingV2Data when V2Manager is nil.
func TestCheckExistingV2Data_NilManager(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Controller{V2Manager: nil}

	check := controller.checkExistingV2Data()

	assert.Equal(t, "existing_v2_data", check.ID)
	assert.Equal(t, CheckStatusSkipped, check.Status)
	assert.Equal(t, CheckSeverityWarning, check.Severity)
}

// TestCheckWritePermission_Success tests checkWritePermission in a writable directory.
func TestCheckWritePermission_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkWritePermission()

	assert.Equal(t, "write_permission", check.ID)
	assert.Equal(t, CheckStatusPassed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "Write access verified")
}

// TestCheckDiskSpace tests checkDiskSpace function.
func TestCheckDiskSpace(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkDiskSpace()

	assert.Equal(t, "disk_space", check.ID)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	// Status depends on actual disk space, so just verify it's valid
	assert.Contains(t, []string{CheckStatusPassed, CheckStatusFailed, CheckStatusError}, check.Status)
	assert.NotEmpty(t, check.Message)
}

// TestIsUsingMySQL_SQLite tests isUsingMySQL returns false for SQLite config.
func TestIsUsingMySQL_SQLite(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	result := controller.isUsingMySQL()

	assert.False(t, result, "Should return false for SQLite configuration")
}

// TestIsUsingMySQL_NilSettings tests isUsingMySQL returns false when Settings is nil.
func TestIsUsingMySQL_NilSettings(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Controller{Settings: nil}

	result := controller.isUsingMySQL()

	assert.False(t, result, "Should return false when Settings is nil")
}

// TestGetDatabaseDirectoryResolved_SQLite tests path resolution for SQLite.
func TestGetDatabaseDirectoryResolved_SQLite(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	dir := controller.getDatabaseDirectoryResolved()

	assert.NotEmpty(t, dir, "Should return a non-empty directory path")
}

// TestGetDatabaseDirectoryResolved_NilSettings tests path resolution when Settings is nil.
func TestGetDatabaseDirectoryResolved_NilSettings(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Controller{Settings: nil}

	dir := controller.getDatabaseDirectoryResolved()

	assert.Equal(t, ".", dir, "Should return current directory when Settings is nil")
}

// TestGetLegacyGormDB_Success tests getLegacyGormDB returns DB from testDatastoreWrapper.
func TestGetLegacyGormDB_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	db := controller.getLegacyGormDB()

	assert.NotNil(t, db, "Should return non-nil GORM DB")
}

// TestGetLegacyGormDB_NilDS tests getLegacyGormDB returns nil when DS is nil.
func TestGetLegacyGormDB_NilDS(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Controller{DS: nil}

	db := controller.getLegacyGormDB()

	assert.Nil(t, db, "Should return nil when DS is nil")
}

// TestRunCriticalPrerequisiteChecks tests the shared critical check runner.
func TestRunCriticalPrerequisiteChecks(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	checks := controller.runCriticalPrerequisiteChecks()

	assert.NotEmpty(t, checks, "Should return a list of checks")

	// All critical checks should have critical severity
	for _, check := range checks {
		assert.Equal(t, CheckSeverityCritical, check.Severity, "Critical checks should have critical severity")
	}
}

// setupPrerequisitesTestEnvironment creates a test environment for prerequisites API tests.
// Note: Tests using this function must NOT be run in parallel due to global state.
func setupPrerequisitesTestEnvironment(t *testing.T) (*echo.Echo, *Controller, *datastoreV2.StateManager) {
	t.Helper()

	// Lock to prevent parallel tests from interfering
	migrationTestMu.Lock()

	// Register mutex unlock with t.Cleanup FIRST to ensure it runs even if setup fails.
	t.Cleanup(func() {
		migrationTestMu.Unlock()
	})

	e := echo.New()
	db := setupMigrationTestDB(t)

	sm := datastoreV2.NewStateManager(db)

	// Save previous state manager to restore after test
	prevSM := stateManager
	prevWorker := migrationWorker
	prevV2Only := isV2OnlyMode

	// Set the global state manager for the handlers
	stateManager = sm
	migrationWorker = nil
	isV2OnlyMode = false // Ensure we're not in v2-only mode for tests

	// Register state restoration cleanup
	t.Cleanup(func() {
		stateManager = prevSM
		migrationWorker = prevWorker
		isV2OnlyMode = prevV2Only
	})

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
	mockDS.EXPECT().GetLastDetections(1).Return([]datastore.Note{}, nil).Maybe()
	mockRepo.EXPECT().CountAll(mock.Anything).Return(int64(100), nil).Maybe()

	// Create a wrapper that provides both the mock interface and the GORM DB
	testDS := &testDatastoreWrapper{
		MockInterface: mockDS,
		db:            db,
	}

	controller := &Controller{
		Echo:     e,
		Group:    e.Group("/api/v2"),
		Settings: getTestSettings(t),
		DS:       testDS,
		Repo:     mockRepo,
	}

	return e, controller, sm
}
