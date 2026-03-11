//go:build integration

package v2

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Shared testcontainer state for all tests in this file.
// Tests MUST NOT use t.Parallel() — they share one database and mutate schema.
var (
	coexTestContainer *containers.MySQLContainer
	coexTestDB        *gorm.DB
	coexTestDBName    string
	coexTestHost      string
	coexTestPort      string
	coexTestUser      string
	coexTestPassword  string
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	ctx := context.Background() //nolint:gocritic // testMain uses *testing.M, not *testing.T — no t.Context() available

	cfg := &containers.MySQLConfig{
		Database:     "coexistence_test",
		RootPassword: "test",
		Username:     "testuser",
		Password:     "testpass",
		ImageTag:     "8.0",
	}

	var err error
	coexTestContainer, err = containers.NewMySQLContainer(ctx, cfg)
	if err != nil {
		panic("failed to create MySQL container: " + err.Error())
	}
	defer func() {
		if err := coexTestContainer.Terminate(context.Background()); err != nil { //nolint:gocritic // cleanup context must outlive test
			fmt.Fprintf(os.Stderr, "error terminating test container: %v\n", err)
		}
	}()

	// Extract host and port from the container
	host, err := coexTestContainer.GetHost(ctx)
	if err != nil {
		panic("failed to get container host: " + err.Error())
	}
	port, err := coexTestContainer.GetPort(ctx)
	if err != nil {
		panic("failed to get container port: " + err.Error())
	}

	coexTestHost = host
	coexTestPort = strconv.Itoa(port)
	coexTestDBName = cfg.Database
	coexTestUser = cfg.Username
	coexTestPassword = cfg.Password

	// Open a plain GORM connection (no NamingStrategy) for seeding and assertions
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.Username, cfg.Password, host, coexTestPort, cfg.Database)
	coexTestDB, err = gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		panic("failed to open GORM connection: " + err.Error())
	}

	return m.Run()
}

// dropAllTables dynamically drops every user table in the database.
// This ensures a completely clean slate between tests.
func dropAllTables(t *testing.T, db *gorm.DB, database string) {
	t.Helper()

	// Query all user tables
	var tableNames []string
	err := db.Raw(
		"SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_type = 'BASE TABLE'",
		database,
	).Scan(&tableNames).Error
	require.NoError(t, err, "failed to query table names")

	if len(tableNames) == 0 {
		return
	}

	// Disable FK checks, drop everything, re-enable
	require.NoError(t, db.Exec("SET FOREIGN_KEY_CHECKS = 0").Error)
	defer func() {
		assert.NoError(t, db.Exec("SET FOREIGN_KEY_CHECKS = 1").Error,
			"failed to re-enable foreign key checks")
	}()
	for _, name := range tableNames {
		require.NoError(t, db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", name)).Error,
			"failed to drop table %s", name)
	}
}

// seedLegacyTables creates legacy schema tables with sample data.
func seedLegacyTables(t *testing.T, db *gorm.DB) {
	t.Helper()

	// AutoMigrate the legacy models (creates tables with legacy column names)
	err := db.AutoMigrate(
		&datastore.Note{},
		&datastore.Results{},
		&datastore.DailyEvents{},
		&datastore.HourlyWeather{},
		&datastore.ImageCache{},
		&datastore.DynamicThreshold{},
		&datastore.ThresholdEvent{},
		&datastore.NotificationHistory{},
	)
	require.NoError(t, err, "failed to migrate legacy schema")

	// Insert sample data into each table.
	// BeginTime and EndTime must be non-zero — MySQL strict mode rejects '0000-00-00'.
	sampleTime := time.Date(2026, 1, 15, 8, 30, 0, 0, time.UTC)
	require.NoError(t, db.Create(&datastore.Note{
		CommonName:     "Common Blackbird",
		ScientificName: "Turdus merula",
		Confidence:     0.95,
		Date:           "2026-01-15",
		Time:           "08:30:00",
		BeginTime:      sampleTime,
		EndTime:        sampleTime.Add(3 * time.Second),
	}).Error, "failed to insert legacy note")

	require.NoError(t, db.Create(&datastore.DailyEvents{
		Date:    "2026-01-15",
		Sunrise: 1705300800,
		Sunset:  1705333200,
	}).Error, "failed to insert legacy daily event")

	require.NoError(t, db.Create(&datastore.DynamicThreshold{
		SpeciesName:   "common blackbird",
		CurrentValue:  0.15,
		BaseThreshold: 0.10,
		ValidHours:    24,
		ExpiresAt:     sampleTime.Add(24 * time.Hour),
		LastTriggered: sampleTime,
		FirstCreated:  sampleTime,
		UpdatedAt:     sampleTime,
	}).Error, "failed to insert legacy dynamic threshold")

	require.NoError(t, db.Create(&datastore.ImageCache{
		ScientificName: "Turdus merula",
		ProviderName:   "wikimedia",
		SourceProvider: "wikimedia",
		URL:            "https://example.com/blackbird.jpg",
		CachedAt:       sampleTime,
	}).Error, "failed to insert legacy image cache")
}

// seedOrphanedBareV2Tables creates bare v2-only tables using raw DDL,
// simulating what the broken nightly (nightly-20260309) would have created.
// Uses singular table names for migration_state and alert_history to match
// the old TableName() override behavior.
func seedOrphanedBareV2Tables(t *testing.T, db *gorm.DB) {
	t.Helper()

	// DDL for v2-only tables with FK constraints matching what GORM AutoMigrate creates.
	// Reference tables must be created first, then tables that reference them.
	// FK constraints are critical for reproducing the bug in #2194.
	orphanedDDL := []string{
		// Reference tables first (no FK dependencies)
		`CREATE TABLE label_types (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50))`,
		`CREATE TABLE taxonomic_classes (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100))`,
		`CREATE TABLE ai_models (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100))`,
		`CREATE TABLE audio_sources (id BIGINT AUTO_INCREMENT PRIMARY KEY, source_uri VARCHAR(500))`,
		// Labels with FKs to reference tables
		`CREATE TABLE labels (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			scientific_name VARCHAR(255),
			model_id BIGINT, label_type_id BIGINT, taxonomic_class_id BIGINT,
			CONSTRAINT fk_labels_model FOREIGN KEY (model_id) REFERENCES ai_models(id),
			CONSTRAINT fk_labels_label_type FOREIGN KEY (label_type_id) REFERENCES label_types(id),
			CONSTRAINT fk_labels_taxonomic_class FOREIGN KEY (taxonomic_class_id) REFERENCES taxonomic_classes(id)
		)`,
		// Detections with FKs to labels and ai_models
		`CREATE TABLE detections (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			label_id BIGINT, model_id BIGINT,
			CONSTRAINT fk_detections_label FOREIGN KEY (label_id) REFERENCES labels(id),
			CONSTRAINT fk_detections_model FOREIGN KEY (model_id) REFERENCES ai_models(id)
		)`,
		// Detection children with FKs to detections
		`CREATE TABLE detection_predictions (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			detection_id BIGINT, label_id BIGINT,
			CONSTRAINT fk_predictions_detection FOREIGN KEY (detection_id) REFERENCES detections(id),
			CONSTRAINT fk_predictions_label FOREIGN KEY (label_id) REFERENCES labels(id)
		)`,
		`CREATE TABLE detection_reviews (id BIGINT AUTO_INCREMENT PRIMARY KEY, detection_id BIGINT UNIQUE,
			CONSTRAINT fk_reviews_detection FOREIGN KEY (detection_id) REFERENCES detections(id))`,
		`CREATE TABLE detection_comments (id BIGINT AUTO_INCREMENT PRIMARY KEY, detection_id BIGINT,
			CONSTRAINT fk_comments_detection FOREIGN KEY (detection_id) REFERENCES detections(id))`,
		`CREATE TABLE detection_locks (id BIGINT AUTO_INCREMENT PRIMARY KEY, detection_id BIGINT UNIQUE,
			CONSTRAINT fk_locks_detection FOREIGN KEY (detection_id) REFERENCES detections(id))`,
		// Old singular names from TableName() overrides
		`CREATE TABLE migration_state (id BIGINT AUTO_INCREMENT PRIMARY KEY, state VARCHAR(50))`,
		`CREATE TABLE migration_dirty_ids (id BIGINT AUTO_INCREMENT PRIMARY KEY)`,
		`CREATE TABLE alert_rules (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(200))`,
		`CREATE TABLE alert_conditions (id BIGINT AUTO_INCREMENT PRIMARY KEY, rule_id BIGINT)`,
		`CREATE TABLE alert_actions (id BIGINT AUTO_INCREMENT PRIMARY KEY, rule_id BIGINT)`,
		`CREATE TABLE alert_history (id BIGINT AUTO_INCREMENT PRIMARY KEY, rule_id BIGINT)`,
	}

	for _, ddl := range orphanedDDL {
		require.NoError(t, db.Exec(ddl).Error, "failed to create orphaned table: %s", ddl)
	}

	// Simulate v2 AutoMigrate contaminating a preserved legacy table:
	// add a label_id column with FK to the orphaned labels table.
	// This exercises the FK-toggle path where a non-dropped table references a dropped one.
	require.NoError(t, db.Exec(
		`ALTER TABLE dynamic_thresholds ADD COLUMN label_id BIGINT, ADD CONSTRAINT fk_dyn_thresh_label FOREIGN KEY (label_id) REFERENCES labels(id)`,
	).Error, "failed to contaminate dynamic_thresholds with FK to labels")
}

// tableExists checks if a table exists in the given database.
func tableExists(t *testing.T, db *gorm.DB, database, tableName string) bool {
	t.Helper()
	var count int64
	err := db.Raw(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		database, tableName,
	).Scan(&count).Error
	require.NoError(t, err, "failed to check table existence for %s", tableName)
	return count > 0
}

// getRowCount returns the number of rows in the given table.
func getRowCount(t *testing.T, db *gorm.DB, tableName string) int64 {
	t.Helper()
	var count int64
	err := db.Table(tableName).Count(&count).Error
	require.NoError(t, err, "failed to count rows in %s", tableName)
	return count
}

// buildTestSettings creates a minimal conf.Settings for checkMySQLMigrationState.
func buildTestSettings(host, port, database, username, password string) *conf.Settings {
	settings := conf.GetTestSettings()
	settings.Output.MySQL.Enabled = true
	settings.Output.MySQL.Host = host
	settings.Output.MySQL.Port = port
	settings.Output.MySQL.Database = database
	settings.Output.MySQL.Username = username
	settings.Output.MySQL.Password = password
	return settings
}

// newTestMySQLManager creates a MySQLManager pointing at the test container.
func newTestMySQLManager(t *testing.T, useV2Prefix bool) *MySQLManager {
	t.Helper()
	mgr, err := NewMySQLManager(&MySQLConfig{
		Host:        coexTestHost,
		Port:        coexTestPort,
		Username:    coexTestUser,
		Password:    coexTestPassword,
		Database:    coexTestDBName,
		UseV2Prefix: useV2Prefix,
		Debug:       false,
	})
	require.NoError(t, err, "failed to create MySQLManager")
	return mgr
}

// ============================================================================
// V2 Initialize Coexistence Tests
// ============================================================================

// TestMySQL_V2Initialize_WithLegacyTables_CreatesV2Prefixed verifies that
// v2 Initialize with UseV2Prefix=true creates v2_ prefixed tables and
// does NOT touch legacy tables. This is the primary regression test for #2149.
func TestMySQL_V2Initialize_WithLegacyTables_CreatesV2Prefixed(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)
	seedLegacyTables(t, coexTestDB)

	// Record legacy row counts before v2 init
	legacyNotesCount := getRowCount(t, coexTestDB, "notes")
	legacyThresholdCount := getRowCount(t, coexTestDB, "dynamic_thresholds")
	legacyImageCount := getRowCount(t, coexTestDB, "image_caches")
	require.Positive(t, legacyNotesCount, "legacy notes should have data")

	// Initialize v2 with prefix
	mgr := newTestMySQLManager(t, true)
	t.Cleanup(func() {
		_ = mgr.Delete()
		_ = mgr.Close()
	})

	err := mgr.Initialize()
	require.NoError(t, err, "v2 Initialize should succeed alongside legacy tables")

	// Assert v2 prefixed tables exist
	v2Tables := []string{
		"v2_labels", "v2_detections", "v2_ai_models", "v2_audio_sources",
		"v2_dynamic_thresholds", "v2_image_caches", "v2_notification_histories",
		"v2_threshold_events", "v2_daily_events", "v2_hourly_weathers",
		"v2_migration_states", "v2_alert_histories", "v2_alert_rules",
		"v2_alert_conditions", "v2_alert_actions",
	}
	for _, table := range v2Tables {
		assert.True(t, tableExists(t, coexTestDB, coexTestDBName, table),
			"v2 table %s should exist", table)
	}

	// Assert legacy tables still exist with data intact
	assert.Equal(t, legacyNotesCount, getRowCount(t, coexTestDB, "notes"),
		"legacy notes data should be untouched")
	assert.Equal(t, legacyThresholdCount, getRowCount(t, coexTestDB, "dynamic_thresholds"),
		"legacy dynamic_thresholds data should be untouched")
	assert.Equal(t, legacyImageCount, getRowCount(t, coexTestDB, "image_caches"),
		"legacy image_caches data should be untouched")

	// Assert no bare v2 tables created (detections without prefix should NOT exist,
	// since that would mean UseV2Prefix was ignored)
	assert.False(t, tableExists(t, coexTestDB, coexTestDBName, "detections"),
		"bare 'detections' table should NOT exist — UseV2Prefix must be effective")
}

// TestMySQL_V2Initialize_WithLegacyTables_LegacyDataIntact verifies that
// legacy table data and schema are completely untouched after v2 initialization.
func TestMySQL_V2Initialize_WithLegacyTables_LegacyDataIntact(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)
	seedLegacyTables(t, coexTestDB)

	// Initialize v2 with prefix
	mgr := newTestMySQLManager(t, true)
	t.Cleanup(func() {
		_ = mgr.Delete()
		_ = mgr.Close()
	})

	err := mgr.Initialize()
	require.NoError(t, err)

	// Verify legacy data content (not just counts)
	var note datastore.Note
	require.NoError(t, coexTestDB.First(&note).Error)
	assert.Equal(t, "Turdus merula", note.ScientificName)
	assert.Equal(t, "Common Blackbird", note.CommonName)

	var threshold datastore.DynamicThreshold
	require.NoError(t, coexTestDB.First(&threshold).Error)
	assert.Equal(t, "common blackbird", threshold.SpeciesName)

	var imgCache datastore.ImageCache
	require.NoError(t, coexTestDB.First(&imgCache).Error)
	assert.Equal(t, "Turdus merula", imgCache.ScientificName)

	// Verify legacy dynamic_thresholds still has species_name column (not label_id)
	assert.True(t, coexTestDB.Migrator().HasColumn(&datastore.DynamicThreshold{}, "species_name"),
		"legacy dynamic_thresholds should still have species_name column")
}

// TestMySQL_V2Delete_WithLegacyTables_OnlyDeletesV2 verifies that
// manager.Delete() only removes v2-prefixed tables, leaving legacy tables intact.
func TestMySQL_V2Delete_WithLegacyTables_OnlyDeletesV2(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)
	seedLegacyTables(t, coexTestDB)

	legacyNotesCount := getRowCount(t, coexTestDB, "notes")

	// Initialize and then delete v2
	mgr := newTestMySQLManager(t, true)
	t.Cleanup(func() { _ = mgr.Close() })

	err := mgr.Initialize()
	require.NoError(t, err)

	// Confirm v2 tables exist before deletion
	assert.True(t, tableExists(t, coexTestDB, coexTestDBName, "v2_detections"))

	err = mgr.Delete()
	require.NoError(t, err, "Delete should succeed")

	// v2 tables should be gone
	assert.False(t, tableExists(t, coexTestDB, coexTestDBName, "v2_detections"),
		"v2_detections should be deleted")
	assert.False(t, tableExists(t, coexTestDB, coexTestDBName, "v2_labels"),
		"v2_labels should be deleted")
	assert.False(t, tableExists(t, coexTestDB, coexTestDBName, "v2_migration_states"),
		"v2_migration_states should be deleted")

	// Legacy tables should still exist with data
	assert.True(t, tableExists(t, coexTestDB, coexTestDBName, "notes"),
		"legacy notes should still exist")
	assert.Equal(t, legacyNotesCount, getRowCount(t, coexTestDB, "notes"),
		"legacy notes data should be intact after v2 Delete")
	assert.True(t, tableExists(t, coexTestDB, coexTestDBName, "dynamic_thresholds"),
		"legacy dynamic_thresholds should still exist")
}

// ============================================================================
// Startup Detection Tests
// ============================================================================

// TestMySQL_StartupDetection_OrphanedV2WithLegacy_ReturnsLegacyMode verifies
// that orphaned bare v2 tables alongside legacy data are correctly identified
// and cleaned up, returning legacy mode. This is the regression test for the
// secondary bug in #2149 (missing !legacyExists guard).
func TestMySQL_StartupDetection_OrphanedV2WithLegacy_ReturnsLegacyMode(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)
	seedLegacyTables(t, coexTestDB)
	seedOrphanedBareV2Tables(t, coexTestDB)

	// Confirm orphaned tables exist before calling startup detection
	assert.True(t, tableExists(t, coexTestDB, coexTestDBName, "detections"),
		"orphaned detections table should exist before cleanup")

	settings := buildTestSettings(coexTestHost, coexTestPort, coexTestDBName, coexTestUser, coexTestPassword)
	state := checkMySQLMigrationState(settings)

	// Should return legacy mode, NOT completed
	assert.Equal(t, entities.MigrationStatusIdle, state.MigrationStatus,
		"should return IDLE (legacy mode), not COMPLETED")
	assert.True(t, state.LegacyRequired, "legacy should be required")
	assert.False(t, state.V2Available, "v2 should not be available")
	assert.False(t, state.FreshInstall, "should not be a fresh install")
	require.NoError(t, state.Error)

	// All orphaned bare v2 tables should be cleaned up
	for _, table := range []string{
		"labels", "label_types", "taxonomic_classes", "ai_models", "audio_sources",
		"detections", "detection_predictions", "detection_reviews", "detection_comments", "detection_locks",
		"alert_rules", "alert_conditions", "alert_actions", "alert_history",
		"migration_state", "migration_dirty_ids",
	} {
		assert.False(t, tableExists(t, coexTestDB, coexTestDBName, table),
			"orphaned %s should be cleaned up", table)
	}

	// Legacy/preserved tables should still be intact (even though dynamic_thresholds
	// had an FK pointing to the now-dropped labels table)
	assert.True(t, tableExists(t, coexTestDB, coexTestDBName, "notes"),
		"legacy notes should survive cleanup")
	assert.True(t, tableExists(t, coexTestDB, coexTestDBName, "dynamic_thresholds"),
		"legacy dynamic_thresholds should survive cleanup")
}

// TestMySQL_StartupDetection_FreshV2Only_ReturnsCompleted verifies that
// fresh v2 tables without legacy are correctly identified as completed.
func TestMySQL_StartupDetection_FreshV2Only_ReturnsCompleted(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)

	// Create fresh v2 tables (no prefix)
	mgr := newTestMySQLManager(t, false)
	t.Cleanup(func() {
		_ = mgr.Delete()
		_ = mgr.Close()
	})

	err := mgr.Initialize()
	require.NoError(t, err)

	settings := buildTestSettings(coexTestHost, coexTestPort, coexTestDBName, coexTestUser, coexTestPassword)
	state := checkMySQLMigrationState(settings)

	assert.Equal(t, entities.MigrationStatusCompleted, state.MigrationStatus,
		"fresh v2 without legacy should be COMPLETED")
	assert.False(t, state.LegacyRequired, "legacy should not be required")
	assert.True(t, state.V2Available, "v2 should be available")
	assert.False(t, state.FreshInstall, "should not be fresh install")
	require.NoError(t, state.Error)
}

// TestMySQL_StartupDetection_EmptyDB_ReturnsFreshInstall verifies that
// an empty database returns fresh install status.
func TestMySQL_StartupDetection_EmptyDB_ReturnsFreshInstall(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)

	settings := buildTestSettings(coexTestHost, coexTestPort, coexTestDBName, coexTestUser, coexTestPassword)
	state := checkMySQLMigrationState(settings)

	assert.Equal(t, entities.MigrationStatusIdle, state.MigrationStatus)
	assert.True(t, state.FreshInstall, "empty DB should be fresh install")
	assert.False(t, state.LegacyRequired)
	assert.False(t, state.V2Available)
	require.NoError(t, state.Error)
}

// TestMySQL_StartupDetection_LegacyOnly_ReturnsLegacyMode verifies that
// a database with only legacy tables returns legacy mode.
func TestMySQL_StartupDetection_LegacyOnly_ReturnsLegacyMode(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)
	seedLegacyTables(t, coexTestDB)

	settings := buildTestSettings(coexTestHost, coexTestPort, coexTestDBName, coexTestUser, coexTestPassword)
	state := checkMySQLMigrationState(settings)

	assert.Equal(t, entities.MigrationStatusIdle, state.MigrationStatus)
	assert.True(t, state.LegacyRequired, "legacy-only DB should require legacy")
	assert.False(t, state.V2Available)
	assert.False(t, state.FreshInstall)
	require.NoError(t, state.Error)
}
