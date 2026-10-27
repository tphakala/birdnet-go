//go:build integration && mysql

// Package migration_test contains MySQL-specific integration tests.
// Run with: go test -tags="integration,mysql" -v ./internal/datastore/v2/migration/...
//
// Requires MySQL environment variables:
//
//	MYSQL_TEST_HOST (default: localhost)
//	MYSQL_TEST_PORT (default: 3306)
//	MYSQL_TEST_USER (default: birdnet)
//	MYSQL_TEST_PASSWORD (required)
//	MYSQL_TEST_DATABASE (default: birdnet_test)
//
// The migration tests drop and recreate the legacy and v2_ tables in
// MYSQL_TEST_DATABASE, so it must be a dedicated database whose name ends in
// "_test"; the harness refuses any other name.
package migration_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration/testutil"
)

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func skipIfNoMySQL(t *testing.T) {
	t.Helper()
	if os.Getenv("MYSQL_TEST_PASSWORD") == "" {
		t.Skip("Skipping MySQL test: MYSQL_TEST_PASSWORD not set")
	}
}

func getMySQLConfig() *v2.MySQLConfig {
	return &v2.MySQLConfig{
		Host:        getEnvOrDefault("MYSQL_TEST_HOST", "localhost"),
		Port:        getEnvOrDefault("MYSQL_TEST_PORT", "3306"),
		Username:    getEnvOrDefault("MYSQL_TEST_USER", "birdnet"),
		Password:    os.Getenv("MYSQL_TEST_PASSWORD"),
		Database:    getEnvOrDefault("MYSQL_TEST_DATABASE", "birdnet_test"),
		UseV2Prefix: true, // Migration mode uses v2_ prefix
		Debug:       false,
	}
}

// TestMySQL_MigrationInfrastructure_Initializes tests that MySQL migration
// infrastructure can be initialized with v2_ prefixed tables.
func TestMySQL_MigrationInfrastructure_Initializes(t *testing.T) {
	skipIfNoMySQL(t)

	cfg := getMySQLConfig()

	// Create MySQL manager with v2_ prefix for migration mode
	manager, err := v2.NewMySQLManager(cfg)
	require.NoError(t, err, "failed to create MySQL manager")
	defer func() {
		// Clean up: delete v2 tables after test
		_ = manager.Delete()
		_ = manager.Close()
	}()

	// Initialize should create v2_ prefixed tables
	err = manager.Initialize()
	require.NoError(t, err, "failed to initialize MySQL v2 schema")

	// Verify tables exist
	require.True(t, manager.Exists(), "v2 migration_states table should exist")
	require.True(t, manager.IsMySQL(), "should identify as MySQL")

	t.Log("MySQL migration infrastructure initialized successfully")
}

// TestMySQL_StateManager_Works tests that StateManager works with MySQL.
func TestMySQL_StateManager_Works(t *testing.T) {
	skipIfNoMySQL(t)

	cfg := getMySQLConfig()

	manager, err := v2.NewMySQLManager(cfg)
	require.NoError(t, err)
	defer func() {
		_ = manager.Delete()
		_ = manager.Close()
	}()

	err = manager.Initialize()
	require.NoError(t, err)

	// Create state manager
	stateManager := v2.NewStateManager(manager.DB())

	// Get initial state
	state, err := stateManager.GetState()
	require.NoError(t, err, "failed to get migration state")
	require.Equal(t, entities.MigrationStatusIdle, state.State, "initial state should be idle")

	t.Log("MySQL StateManager works correctly")
}

// Timings of the migration tests below: completion of a small migration, and
// the wait for tail sync, whose cycle is 10 s.
const (
	mysqlMigrationTimeout = 30 * time.Second
	mysqlTailSyncTimeout  = 30 * time.Second
	mysqlPollInterval     = time.Second
)

// seedNotesWithResults seeds count legacy notes with one secondary result each.
func seedNotesWithResults(t *testing.T, ctx *testutil.TestContext, count int) []datastore.Note {
	t.Helper()

	notes := testutil.GenerateDetections(count)
	require.NoError(t, ctx.Seeder.SeedDetections(notes))

	results := make([]datastore.Results, 0, len(notes))
	for i := range notes {
		id := uint(i + 1) //nolint:gosec // G115: test data uses small values
		results = append(results, testutil.NewResultsBuilder().
			WithID(id).
			WithNoteID(id).
			WithConfidence(0.6).
			Build())
	}
	require.NoError(t, ctx.Seeder.SeedResults(results))
	return notes
}

// TestMySQL_Migration_PredictionsKeepWatermarkThroughTailSync runs the
// migration on MySQL in batch mode, as in production: the detections watermark
// must survive the predictions phase, and tail sync must continue from it.
func TestMySQL_Migration_PredictionsKeepWatermarkThroughTailSync(t *testing.T) {
	skipIfNoMySQL(t)

	ctx := testutil.SetupMySQLIntegrationTest(t, getMySQLConfig())

	const initial = 10
	notes := seedNotesWithResults(t, ctx, initial)

	ctx.StartMigration(t, len(notes))
	ctx.WaitForCompletion(t, mysqlMigrationTimeout)

	assert.Equal(t, int64(initial), ctx.GetV2DetectionCount(t))

	// Checked right after completion, before the first tail sync cycle.
	state, err := ctx.StateManager.GetState()
	require.NoError(t, err)
	assert.Equal(t, uint(initial), state.LastMigratedID, "predictions phase must not reset the watermark")

	var predictions int64
	require.NoError(t, ctx.V2Manager.DB().Model(&entities.DetectionPrediction{}).Count(&predictions).Error)
	assert.Equal(t, int64(initial), predictions, "every seeded result must be migrated")

	const postCompletion = 5
	require.NoError(t, ctx.Seeder.SeedDetections(testutil.GenerateDetectionsWithIDOffset(postCompletion, initial+1)))

	require.Eventually(t, func() bool {
		st, stErr := ctx.StateManager.GetState()
		return stErr == nil && st.LastMigratedID == initial+postCompletion
	}, mysqlTailSyncTimeout, mysqlPollInterval,
		"tail sync should advance the watermark over the post-completion detections")

	assert.Equal(t, int64(initial+postCompletion), ctx.GetV2DetectionCount(t))

	// Tail sync migrated only the new rows; a re-walk from 0 would count all of them.
	state, err = ctx.StateManager.GetState()
	require.NoError(t, err)
	assert.Equal(t, int64(postCompletion), state.MigratedRecords)

	dirty, err := ctx.StateManager.GetDirtyIDCount()
	require.NoError(t, err)
	assert.Equal(t, int64(0), dirty)
}

// TestMySQL_TailSync_DrainsDirtyIDsOfRowsAlreadyInV2 recreates an install that
// completed its migration with the watermark at 0 and every id in the dirty
// set, although all rows are in v2. One tail sync cycle must drain the dirty
// set: re-migrating an unchanged row is a success, and a locked row is skipped.
func TestMySQL_TailSync_DrainsDirtyIDsOfRowsAlreadyInV2(t *testing.T) {
	skipIfNoMySQL(t)

	ctx := testutil.SetupMySQLIntegrationTest(t, getMySQLConfig())

	const total = 10
	const lockedID = uint(3)
	notes := seedNotesWithResults(t, ctx, total)

	ctx.StartMigration(t, len(notes))
	ctx.WaitForCompletion(t, mysqlMigrationTimeout)
	require.Equal(t, int64(total), ctx.GetV2DetectionCount(t))

	// The stuck shape: watermark back at 0, every id dirty, one row locked.
	state, err := ctx.StateManager.GetState()
	require.NoError(t, err)
	require.NoError(t, ctx.V2Manager.DB().Model(&entities.MigrationState{}).
		Where("id = ?", state.ID).Update("last_migrated_id", 0).Error)
	for id := uint(1); id <= total; id++ {
		require.NoError(t, ctx.StateManager.AddDirtyID(id))
	}
	require.NoError(t, ctx.DetectionRepo.Lock(t.Context(), lockedID))

	require.Eventually(t, func() bool {
		dirty, dErr := ctx.StateManager.GetDirtyIDCount()
		st, stErr := ctx.StateManager.GetState()
		return dErr == nil && stErr == nil && dirty == 0 && st.LastMigratedID == total
	}, mysqlTailSyncTimeout, mysqlPollInterval,
		"one tail sync cycle should drain the dirty set and walk the watermark back to the end")

	assert.Equal(t, int64(total), ctx.GetV2DetectionCount(t))
	locked, lockErr := ctx.DetectionRepo.IsLocked(t.Context(), lockedID)
	require.NoError(t, lockErr)
	assert.True(t, locked)
}

// TestMySQL_RerunAfterRollbackPassesValidation pins that a migration run after
// a rollback is not failed by dirty IDs from the earlier run, with the
// transactional clear on the v2_ prefixed dirty table.
func TestMySQL_RerunAfterRollbackPassesValidation(t *testing.T) {
	skipIfNoMySQL(t)

	ctx := testutil.SetupMySQLIntegrationTest(t, getMySQLConfig())
	assertRerunAfterRollbackPassesValidation(t, ctx, mysqlMigrationTimeout)
}
