//go:build integration

package v2

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Shared testcontainer state for all tests in this file.
// Tests MUST NOT use t.Parallel(); they share one database and mutate schema.
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
	if code, skip := containers.SkipTestMainIfContainerRuntimeUnavailable(); skip {
		return code
	}
	ctx := context.Background() //nolint:gocritic // testMain uses *testing.M, not *testing.T; no t.Context() available

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

	// Drop everything with FK checks off, on one connection
	require.NoError(t, withForeignKeyChecksOff(db, func(tx *gorm.DB) error {
		for _, name := range tableNames {
			if err := tx.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", name)).Error; err != nil {
				return fmt.Errorf("failed to drop table %s: %w", name, err)
			}
		}
		return nil
	}))
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
	// BeginTime and EndTime must be non-zero; MySQL strict mode rejects '0000-00-00'.
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
	settings := conftest.GetTestSettings()
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
		"bare 'detections' table should NOT exist; UseV2Prefix must be effective")
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

// seedBareV2WithDetections creates the unprefixed v2 schema the way a real fresh
// install does, marks it completed or idle, and adds detection rows. Detections
// are inserted with foreign key checks off on one pinned connection, because the
// referenced label rows are not under test.
func seedBareV2WithDetections(t *testing.T, detections int, completed bool) {
	t.Helper()

	mgr := newTestMySQLManager(t, false)
	require.NoError(t, mgr.Initialize(), "fresh unprefixed v2 init should succeed")
	require.NoError(t, mgr.Close())

	state := entities.MigrationStatusIdle
	if completed {
		state = entities.MigrationStatusCompleted
	}
	require.NoError(t, coexTestDB.Exec("UPDATE migration_states SET state = ?", string(state)).Error)
	require.Equal(t, int64(1), getRowCount(t, coexTestDB, "migration_states"), "the bare migration_states row must exist")

	require.NoError(t, withForeignKeyChecksOff(coexTestDB, func(tx *gorm.DB) error {
		var insertErr error
		for i := range detections {
			insertErr = errors.Join(insertErr, tx.Exec(
				"INSERT INTO detections (model_id, label_id, detected_at, confidence) VALUES (1, 1, ?, 0.9)",
				1000+i).Error)
		}
		return insertErr
	}))
}

// withForeignKeyChecksOff runs fn on one pinned connection with foreign key checks off,
// so the seed statements can insert rows whose references are not under test. The checks
// are switched back on before the connection returns to the pool on every exit path,
// including a require failure or a panic inside fn, and the error of fn and of the
// restore are joined. fn gets a session that skips GORM's default transaction: a Create on
// the pinned handle itself begins and commits a transaction and then points the handle back
// at the pool, so the restore would run on another connection.
func withForeignKeyChecksOff(db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.Connection(func(tx *gorm.DB) (err error) {
		if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
			return err
		}
		defer func() {
			err = errors.Join(err, tx.Exec("SET FOREIGN_KEY_CHECKS = 1").Error)
		}()
		return fn(tx.Session(&gorm.Session{SkipDefaultTransaction: true}))
	})
}

// foreignKeyProbe is a throwaway table for TestWithForeignKeyChecksOff_RestoresOnPinnedConnection.
type foreignKeyProbe struct {
	ID   uint `gorm:"primaryKey"`
	Name string
}

func (foreignKeyProbe) TableName() string { return "fk_checks_probe" }

// foreignKeyChecksProbeConns is how many pool connections the probe holds at once, so every
// idle connection the seed could have used is among them.
const foreignKeyChecksProbeConns = 8

// assertForeignKeyChecksOn fails for every pooled connection that has foreign key checks off.
func assertForeignKeyChecksOn(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	for i := range foreignKeyChecksProbeConns {
		conn, err := sqlDB.Conn(t.Context())
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		var checks int
		require.NoError(t, conn.QueryRowContext(t.Context(), "SELECT @@foreign_key_checks").Scan(&checks))
		assert.Equal(t, 1, checks, "connection %d was returned to the pool with foreign key checks off", i)
	}
}

// TestWithForeignKeyChecksOff_RestoresOnPinnedConnection pins the helper's contract: whatever fn
// does (create a row, fail, panic), the checks are back on for every connection in the pool.
func TestWithForeignKeyChecksOff_RestoresOnPinnedConnection(t *testing.T) {
	require.NoError(t, coexTestDB.Exec("CREATE TABLE IF NOT EXISTS fk_checks_probe (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, name VARCHAR(10))").Error)
	t.Cleanup(func() { assert.NoError(t, coexTestDB.Exec("DROP TABLE IF EXISTS fk_checks_probe").Error) })

	t.Run("create inside fn", func(t *testing.T) {
		require.NoError(t, withForeignKeyChecksOff(coexTestDB, func(tx *gorm.DB) error {
			return tx.Create(&foreignKeyProbe{Name: "a"}).Error
		}))
		assertForeignKeyChecksOn(t, coexTestDB)
	})

	t.Run("fn returns an error", func(t *testing.T) {
		sentinel := errors.NewStd("seed failed")
		err := withForeignKeyChecksOff(coexTestDB, func(tx *gorm.DB) error {
			require.NoError(t, tx.Create(&foreignKeyProbe{Name: "b"}).Error)
			return sentinel
		})
		require.ErrorIs(t, err, sentinel)
		assertForeignKeyChecksOn(t, coexTestDB)
	})

	t.Run("fn panics", func(t *testing.T) {
		assert.Panics(t, func() {
			_ = withForeignKeyChecksOff(coexTestDB, func(tx *gorm.DB) error {
				require.NoError(t, tx.Create(&foreignKeyProbe{Name: "c"}).Error)
				panic("seed exploded")
			})
		})
		assertForeignKeyChecksOn(t, coexTestDB)
	})
}

// insertLegacyNote adds one populated legacy notes row.
func insertLegacyNote(t *testing.T) {
	t.Helper()
	sampleTime := time.Date(2026, 1, 15, 8, 30, 0, 0, time.UTC)
	require.NoError(t, coexTestDB.Create(&datastore.Note{
		CommonName:     "Common Blackbird",
		ScientificName: "Turdus merula",
		Confidence:     0.95,
		Date:           "2026-01-15",
		Time:           "08:30:00",
		BeginTime:      sampleTime,
		EndTime:        sampleTime.Add(3 * time.Second),
	}).Error)
}

// baseTableNames lists the base tables of the test database, sorted.
func baseTableNames(t *testing.T) []string {
	t.Helper()
	var names []string
	require.NoError(t, coexTestDB.Raw(
		"SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_type = 'BASE TABLE' ORDER BY 1",
		coexTestDBName).Scan(&names).Error)
	return names
}

// tableRowCounts returns the row count of every base table of the test database.
func tableRowCounts(t *testing.T) map[string]int64 {
	t.Helper()
	counts := make(map[string]int64)
	for _, name := range baseTableNames(t) {
		counts[name] = getRowCount(t, coexTestDB, name)
	}
	return counts
}

// assertNoRowsLost fails when a table that held rows before is missing now or holds
// fewer rows (seed tables may grow when the chosen mode starts), or when a table that
// disappeared held rows at all.
func assertNoRowsLost(t *testing.T, before, after map[string]int64, msg string) {
	t.Helper()
	for table, rows := range before {
		now, exists := after[table]
		if !exists {
			assert.Zero(t, rows, "%s: table %s held %d rows and is gone", msg, table, rows)
			continue
		}
		assert.GreaterOrEqual(t, now, rows, "%s: table %s lost rows", msg, table)
	}
}

// olderReleaseCleanupApplies reports the condition under which a release before the
// reconcile drops the bare v2 tables at startup: bare detections beside notes and no
// v2_ migration marker.
func olderReleaseCleanupApplies(t *testing.T) bool {
	t.Helper()
	return tableExists(t, coexTestDB, coexTestDBName, "detections") &&
		tableExists(t, coexTestDB, coexTestDBName, "notes") &&
		!tableExists(t, coexTestDB, coexTestDBName, "v2_migration_states") &&
		!tableExists(t, coexTestDB, coexTestDBName, "v2_migration_state")
}

// bootEffect is what the mode chosen by a startup check does to the database on its
// own start, so the next boot sees the state the running app leaves behind.
type bootEffect int

const (
	effectNone bootEffect = iota
	// effectV2 opens the bare enhanced schema.
	effectV2
	// effectFreshInstall emulates InitializeFreshInstall: open the bare schema and
	// mark it completed.
	effectFreshInstall
	// effectLegacy opens the legacy store, then the v2_ migration infrastructure.
	effectLegacy
)

func applyBootEffect(t *testing.T, settings *conf.Settings, effect bootEffect) {
	t.Helper()
	switch effect {
	case effectNone:
	case effectV2, effectFreshInstall:
		mgr := newTestMySQLManager(t, false)
		require.NoError(t, mgr.Initialize())
		require.NoError(t, mgr.Close())
		if effect == effectFreshInstall {
			res := coexTestDB.Exec("UPDATE migration_states SET state = ? WHERE id = 1", string(entities.MigrationStatusCompleted))
			require.NoError(t, res.Error)
			require.EqualValues(t, 1, res.RowsAffected)
		}
	case effectLegacy:
		store := datastore.New(settings)
		require.NoError(t, store.Open(), "legacy Open must succeed on the reconciled database")
		require.NoError(t, store.Close())
		infra := newTestMySQLManager(t, true)
		require.NoError(t, infra.Initialize())
		require.NoError(t, infra.Close())
	}
}

// invalidateTableAsView replaces a base table with a view whose base table is gone, so
// listing shows the name but every SELECT from it fails: an unreadable table.
func invalidateTableAsView(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, coexTestDB.Exec("DROP TABLE `"+name+"`").Error)
	require.NoError(t, coexTestDB.Exec("CREATE TABLE tmp_view_base (id BIGINT)").Error)
	require.NoError(t, coexTestDB.Exec("CREATE VIEW `"+name+"` AS SELECT id FROM tmp_view_base").Error)
	require.NoError(t, coexTestDB.Exec("DROP TABLE tmp_view_base").Error)
	t.Cleanup(func() { _ = coexTestDB.Exec("DROP VIEW IF EXISTS `" + name + "`").Error })
}

// TestMySQL_StartupDetection_MixedSchema_ReconcilesWithoutLosingRows runs the mixed
// schema check on a real server for every shape of bare v2 tables beside a legacy
// notes table. Per shape: boot the check up to three times, run the chosen mode's own
// startup between boots, and after every boot assert that no table holding rows was
// dropped or lost rows, that a reconcile no longer matches the cleanup condition of
// older releases, and that a refusal changes nothing.
func TestMySQL_StartupDetection_MixedSchema_ReconcilesWithoutLosingRows(t *testing.T) {
	type boot struct {
		decision string
		effect   bootEffect
		check    func(t *testing.T)
	}
	refusedTwice := []boot{
		{DecisionMixedSchemaRefused, effectNone, nil},
		{DecisionMixedSchemaRefused, effectNone, nil},
	}
	tests := []struct {
		name  string
		seed  func(t *testing.T)
		boots []boot
	}{
		{
			name: "v2 detections, empty notes, completed",
			seed: func(t *testing.T) {
				t.Helper()
				seedBareV2WithDetections(t, 2, true)
				require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
			},
			boots: []boot{
				{DecisionMixedSchemaKeptV2, effectV2, func(t *testing.T) {
					t.Helper()
					assert.Equal(t, int64(2), getRowCount(t, coexTestDB, "detections"))
					for _, table := range legacyDetectionTables {
						assert.False(t, tableExists(t, coexTestDB, coexTestDBName, table), "%s must be dropped", table)
					}
					settings := buildTestSettings(coexTestHost, coexTestPort, coexTestDBName, coexTestUser, coexTestPassword)
					assert.True(t, CheckMySQLHasFreshV2Schema(settings), "the enhanced database must be picked")
				}},
				{DecisionV2Restart, effectV2, nil},
			},
		},
		{
			name: "broken nightly: empty bare v2, populated legacy",
			seed: func(t *testing.T) {
				t.Helper()
				seedLegacyTables(t, coexTestDB)
				seedOrphanedBareV2Tables(t, coexTestDB)
			},
			boots: []boot{
				{DecisionMixedSchemaKeptLegacy, effectLegacy, func(t *testing.T) {
					t.Helper()
					for _, table := range bareDetectionTables {
						assert.False(t, tableExists(t, coexTestDB, coexTestDBName, table), "%s must be dropped", table)
					}
					for _, table := range []string{"labels", "alert_rules", "label_types"} {
						assert.True(t, tableExists(t, coexTestDB, coexTestDBName, table), "%s must stay", table)
					}
					assert.Equal(t, int64(1), getRowCount(t, coexTestDB, "notes"))
				}},
				{DecisionMigrationPrefix + string(entities.MigrationStatusIdle), effectLegacy, nil},
			},
		},
		{
			name: "legacy populated, bare alert rules and labels left in place",
			seed: func(t *testing.T) {
				t.Helper()
				seedLegacyTables(t, coexTestDB)
				seedOrphanedBareV2Tables(t, coexTestDB)
				require.NoError(t, coexTestDB.Exec("INSERT INTO alert_rules (name) VALUES ('rare species')").Error)
				require.NoError(t, coexTestDB.Exec("INSERT INTO labels (scientific_name) VALUES ('Turdus merula')").Error)
			},
			boots: []boot{
				{DecisionMixedSchemaKeptLegacy, effectLegacy, func(t *testing.T) {
					t.Helper()
					assert.Equal(t, int64(1), getRowCount(t, coexTestDB, "alert_rules"), "the alert rule must stay")
					assert.Equal(t, int64(1), getRowCount(t, coexTestDB, "labels"), "the label must stay")
					assert.False(t, tableExists(t, coexTestDB, coexTestDBName, "detections"))
				}},
				{DecisionMigrationPrefix + string(entities.MigrationStatusIdle), effectLegacy, nil},
			},
		},
		{
			name: "both empty, idle marker",
			seed: func(t *testing.T) {
				t.Helper()
				seedBareV2WithDetections(t, 0, false)
				require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
			},
			boots: []boot{
				{DecisionMixedSchemaFreshV2, effectV2, func(t *testing.T) {
					t.Helper()
					var state string
					require.NoError(t, coexTestDB.Raw("SELECT state FROM migration_states WHERE id = 1").Scan(&state).Error)
					assert.Equal(t, string(entities.MigrationStatusCompleted), state)
					assert.False(t, tableExists(t, coexTestDB, coexTestDBName, "notes"))
				}},
				{DecisionV2Restart, effectV2, nil},
			},
		},
		{
			name: "both empty, marker table without a row",
			seed: func(t *testing.T) {
				t.Helper()
				seedBareV2WithDetections(t, 0, true)
				require.NoError(t, coexTestDB.Exec("DELETE FROM migration_states").Error)
				require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
			},
			boots: []boot{
				{DecisionMixedSchemaFreshV2, effectFreshInstall, func(t *testing.T) {
					t.Helper()
					assert.True(t, tableExists(t, coexTestDB, coexTestDBName, "notes"),
						"notes must not be dropped while the marker update touched no row")
				}},
				{DecisionMixedSchemaKeptV2, effectV2, nil},
				{DecisionV2Restart, effectV2, nil},
			},
		},
		{
			name: "both populated",
			seed: func(t *testing.T) {
				t.Helper()
				seedBareV2WithDetections(t, 2, true)
				require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
				insertLegacyNote(t)
			},
			boots: refusedTwice,
		},
		{
			name: "legacy populated, v2-shaped notification_histories rows",
			seed: func(t *testing.T) {
				t.Helper()
				mgr := newTestMySQLManager(t, false)
				require.NoError(t, mgr.Initialize())
				require.NoError(t, mgr.Close())
				require.NoError(t, withForeignKeyChecksOff(coexTestDB, func(tx *gorm.DB) error {
					return insertV2NotificationHistories(tx, 2)
				}))
				require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
				insertLegacyNote(t)
			},
			boots: refusedTwice,
		},
		{
			name: "v2 detections, empty legacy, idle marker",
			seed: func(t *testing.T) {
				t.Helper()
				seedBareV2WithDetections(t, 2, false)
				require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
			},
			boots: refusedTwice,
		},
		{
			name: "v2 detections, empty legacy, completed, unreadable bare table",
			seed: func(t *testing.T) {
				t.Helper()
				seedBareV2WithDetections(t, 2, true)
				require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
				invalidateTableAsView(t, "alert_actions")
			},
			boots: refusedTwice,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)
			tt.seed(t)

			settings := buildTestSettings(coexTestHost, coexTestPort, coexTestDBName, coexTestUser, coexTestPassword)
			// datastore.New picks SQLite first when it is enabled.
			settings.Output.SQLite.Enabled = false

			for i, b := range tt.boots {
				before := tableRowCounts(t)
				state := checkMySQLMigrationState(settings)
				msg := fmt.Sprintf("boot %d", i+1)

				require.Equal(t, b.decision, state.Decision, msg)
				after := tableRowCounts(t)
				assertNoRowsLost(t, before, after, msg)
				if b.decision == DecisionMixedSchemaRefused {
					require.ErrorIs(t, state.Error, ErrMixedSchemaDatabase, msg)
					assert.Equal(t, before, after, "%s: a refusal must change nothing", msg)
				} else {
					require.NoError(t, state.Error, msg)
				}
				if strings.HasPrefix(b.decision, "mixed_schema_") && b.decision != DecisionMixedSchemaRefused && b.effect != effectFreshInstall {
					// The fresh install boot keeps the empty legacy tables beside the bare
					// schema until the next boot, so it is the one reconcile an older release
					// could still clean up.
					assert.False(t, olderReleaseCleanupApplies(t), "%s: a reconcile must leave no state older releases clean up", msg)
				}
				if b.check != nil {
					b.check(t)
				}

				applyBootEffect(t, settings, b.effect)
				assertNoRowsLost(t, before, tableRowCounts(t), msg+" after the chosen mode started")
			}
		})
	}
}

// TestMySQL_DropVerifiedEmptyTables_RowInAnyTableDropsNothing pins the check-then-drop
// logic: one row in any table of the set aborts before the first DROP, listed tables
// before it included, and the session is left with foreign key checks on and no lock.
func TestMySQL_DropVerifiedEmptyTables_RowInAnyTableDropsNothing(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)
	for _, ddl := range []string{
		"CREATE TABLE drop_parent (id BIGINT PRIMARY KEY)",
		"CREATE TABLE drop_child (id BIGINT PRIMARY KEY, parent_id BIGINT, CONSTRAINT fk_drop_child FOREIGN KEY (parent_id) REFERENCES drop_parent(id))",
		"CREATE TABLE drop_other (id BIGINT PRIMARY KEY)",
		"INSERT INTO drop_child (id, parent_id) VALUES (1, NULL)",
	} {
		require.NoError(t, coexTestDB.Exec(ddl).Error)
	}
	set := []string{"drop_other", "drop_child", "drop_parent", "drop_missing"}

	dropped, err := dropVerifiedEmptyTables(coexTestDB, coexTestDBName, set)
	require.ErrorIs(t, err, errMixedSchemaTableNotEmpty)
	assert.Contains(t, err.Error(), "drop_child")
	assert.Empty(t, dropped)
	assert.Equal(t, []string{"drop_child", "drop_other", "drop_parent"}, baseTableNames(t), "nothing may be dropped")
	assert.Equal(t, int64(1), getRowCount(t, coexTestDB, "drop_child"))

	require.NoError(t, coexTestDB.Exec("DELETE FROM drop_child").Error)
	dropped, err = dropVerifiedEmptyTables(coexTestDB, coexTestDBName, set)
	require.NoError(t, err)
	assert.Equal(t, []string{"drop_other", "drop_child", "drop_parent"}, dropped, "a parent referenced by a child is dropped with it")
	assert.Empty(t, baseTableNames(t))

	// Sessions of the pool are clean: checks on, no table locked. Create a table and
	// write to it from a fresh pinned session; a leaked lock would block or fail it.
	require.NoError(t, coexTestDB.Exec("CREATE TABLE drop_probe (id BIGINT PRIMARY KEY)").Error)
	require.NoError(t, coexTestDB.Connection(func(tx *gorm.DB) error {
		var checks int
		if err := tx.Raw("SELECT @@foreign_key_checks").Scan(&checks).Error; err != nil {
			return err
		}
		assert.Equal(t, 1, checks, "foreign key checks must be restored")
		return tx.Exec("INSERT INTO drop_probe (id) VALUES (1)").Error
	}))
}

// TestMySQL_StartupDetection_MixedSchema_DropFailureRefusesAndRetries holds a table
// lock on one table of the drop set from another session so the reconcile cannot take
// its write locks. The boot must refuse with the failed step and the error number in the
// text, drop nothing (a marker written for the fresh shape stays completed, so the next
// boot classifies the same database as keep-v2), and succeed once the lock is gone.
func TestMySQL_StartupDetection_MixedSchema_DropFailureRefusesAndRetries(t *testing.T) {
	tests := []struct {
		name         string
		seed         func(t *testing.T)
		lockTable    string
		wantStep     string
		wantDecision string
		hasMarker    bool
	}{
		{
			name: "keep v2: v2 detections, empty legacy, completed",
			seed: func(t *testing.T) {
				t.Helper()
				seedBareV2WithDetections(t, 2, true)
				require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
			},
			lockTable:    "notes",
			wantStep:     "removing the empty legacy tables failed",
			wantDecision: DecisionMixedSchemaKeptV2,
			hasMarker:    true,
		},
		{
			name: "fresh v2: both empty, idle marker",
			seed: func(t *testing.T) {
				t.Helper()
				seedBareV2WithDetections(t, 0, false)
				require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
			},
			lockTable:    "notes",
			wantStep:     "removing the empty legacy tables failed",
			wantDecision: DecisionMixedSchemaKeptV2,
			hasMarker:    true,
		},
		{
			name: "keep legacy: empty bare v2, populated legacy",
			seed: func(t *testing.T) {
				t.Helper()
				seedLegacyTables(t, coexTestDB)
				seedOrphanedBareV2Tables(t, coexTestDB)
			},
			lockTable:    "detections",
			wantStep:     "removing the empty enhanced detection tables failed",
			wantDecision: DecisionMixedSchemaKeptLegacy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)
			tt.seed(t)
			settings := buildTestSettings(coexTestHost, coexTestPort, coexTestDBName, coexTestUser, coexTestPassword)

			markerState := func() string {
				if !tt.hasMarker {
					return ""
				}
				var state string
				require.NoError(t, coexTestDB.Raw("SELECT state FROM migration_states WHERE id = 1").Scan(&state).Error)
				return state
			}
			before := tableRowCounts(t)

			// Another session reads the table under a table lock; a write lock must wait for
			// it. The checks run after the callback so a failed assertion cannot skip the unlock.
			var state StartupState
			require.NoError(t, coexTestDB.Connection(func(tx *gorm.DB) error {
				if err := tx.Exec("LOCK TABLES `" + tt.lockTable + "` READ").Error; err != nil {
					return err
				}
				state = checkMySQLMigrationState(settings)
				return tx.Exec("UNLOCK TABLES").Error
			}))

			assert.Equal(t, DecisionMixedSchemaRefused, state.Decision)
			require.ErrorIs(t, state.Error, ErrMixedSchemaDatabase)
			assert.Contains(t, state.Error.Error(), tt.wantStep)
			assert.Contains(t, state.Error.Error(), "MySQL error 1205", "the error number names the lock timeout")
			assert.Equal(t, before, tableRowCounts(t), "a failed drop must not drop or add a table or row")
			if tt.hasMarker {
				assert.Equal(t, string(entities.MigrationStatusCompleted), markerState(),
					"the completed marker stays so the next boot keeps the enhanced database")
			}

			state = checkMySQLMigrationState(settings)
			require.NoError(t, state.Error)
			assert.Equal(t, tt.wantDecision, state.Decision, "the next boot retries and succeeds")
		})
	}
}

// TestMySQL_StartupDetection_MixedSchemaKeepV2Steps_RestartsV2 runs the wiki
// guide's keep-the-enhanced-database SQL on a refused database and verifies the
// next start uses the bare schema and keeps the detections.
func TestMySQL_StartupDetection_MixedSchemaKeepV2Steps_RestartsV2(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)
	seedBareV2WithDetections(t, 2, true)
	require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
	insertLegacyNote(t)

	settings := buildTestSettings(coexTestHost, coexTestPort, coexTestDBName, coexTestUser, coexTestPassword)
	require.Equal(t, DecisionMixedSchemaRefused, checkMySQLMigrationState(settings).Decision)

	require.NoError(t, coexTestDB.Connection(func(tx *gorm.DB) error {
		var err error
		for _, stmt := range mixedSchemaKeepV2SQL {
			err = errors.Join(err, tx.Exec(stmt).Error)
		}
		return err
	}))

	state := checkMySQLMigrationState(settings)
	require.NoError(t, state.Error)
	assert.Equal(t, DecisionV2Restart, state.Decision)
	assert.True(t, CheckMySQLHasFreshV2Schema(settings),
		"the next start must use the bare schema, not the v2_ prefix")
	assert.Equal(t, int64(2), getRowCount(t, coexTestDB, "detections"), "detections must survive the recovery")
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

// TestMySQL_V2Initialize_SwapsContributionDetectionIndex verifies the upgrade of a
// v2_detection_model_contributions table created before the hit statistics
// existed: InnoDB has bound the detection_id foreign key to the plain index, and
// the drop must still succeed (the unique index takes over), keep the row and
// keep the cascade working.
func TestMySQL_V2Initialize_SwapsContributionDetectionIndex(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)

	mgr := newTestMySQLManager(t, true)
	t.Cleanup(func() {
		_ = mgr.Delete()
		_ = mgr.Close()
	})
	require.NoError(t, mgr.Initialize())
	db := mgr.DB()

	const (
		table    = "v2_detection_model_contributions"
		oldIndex = "idx_v2_detection_model_contributions_detection_id"
		modelIdx = "idx_v2_detection_model_contributions_model_id"
		unique   = "idx_contrib_detection_model"
	)
	entity := &entities.DetectionModelContribution{}

	// Recreate the pre-upgrade table: plain detection_id index listed before the
	// unique index, both foreign keys.
	require.NoError(t, db.Exec("DROP TABLE "+table).Error)
	require.NoError(t, db.Exec("CREATE TABLE "+table+" ("+
		"id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, "+
		"detection_id BIGINT UNSIGNED NOT NULL, "+
		"model_id BIGINT UNSIGNED NOT NULL, "+
		"hit_count BIGINT NOT NULL, "+
		"max_confidence DOUBLE NOT NULL, "+
		"PRIMARY KEY (id), "+
		"INDEX "+oldIndex+" (detection_id), "+
		"UNIQUE INDEX "+unique+" (detection_id, model_id), "+
		"CONSTRAINT fk_old_contrib_detection FOREIGN KEY (detection_id) REFERENCES v2_detections(id) ON DELETE CASCADE ON UPDATE CASCADE, "+
		"CONSTRAINT fk_old_contrib_model FOREIGN KEY (model_id) REFERENCES v2_ai_models(id))").Error)

	// Insert a detection and an old-shape contribution row. The detection's own
	// label and model references are not under test, so FK checks are off for the
	// inserts, on one connection.
	detection := entities.Detection{ModelID: 1, LabelID: 1, DetectedAt: 1000, Confidence: 0.9}
	require.NoError(t, withForeignKeyChecksOff(db, func(tx *gorm.DB) error {
		if err := tx.Create(&detection).Error; err != nil {
			return err
		}
		return tx.Exec("INSERT INTO "+table+" (detection_id, model_id, hit_count, max_confidence) VALUES (?, 1, 3, 0.9)",
			detection.ID).Error
	}))
	require.True(t, db.Migrator().HasIndex(entity, oldIndex))

	require.NoError(t, mgr.Initialize())

	mig := db.Migrator()
	assert.False(t, mig.HasIndex(entity, oldIndex), "the redundant detection_id index must be dropped")
	assert.True(t, mig.HasIndex(entity, unique), "the unique index must survive the drop")
	assert.True(t, mig.HasIndex(entity, modelIdx), "the model_id index must be added")

	var row entities.DetectionModelContribution
	require.NoError(t, db.First(&row).Error)
	assert.Equal(t, 3, row.HitCount)
	assert.Nil(t, row.FirstHitAt)
	assert.Nil(t, row.LastHitAt)
	assert.Nil(t, row.MinConfidence)
	assert.Nil(t, row.ConfidenceSum)

	// The foreign key still cascades after the drop.
	require.NoError(t, db.Delete(&entities.Detection{}, detection.ID).Error)
	var remaining int64
	require.NoError(t, db.Model(&entities.DetectionModelContribution{}).Count(&remaining).Error)
	assert.Zero(t, remaining, "deleting the detection must still cascade to its contributions")
}

// seedV2ShapedNotificationHistoryWithoutDetections builds a fresh unprefixed v2
// database, then drops the bare detections table the way orphan cleanup leaves
// it, and adds the notes table an earlier legacy boot left behind. The legacy
// store then sees v2-shaped auxiliary tables and no v2 detections. rows v2 rows
// are inserted into notification_histories.
func seedV2ShapedNotificationHistoryWithoutDetections(t *testing.T, rows int) *conf.Settings {
	t.Helper()
	dropAllTables(t, coexTestDB, coexTestDBName)

	mgr := newTestMySQLManager(t, false)
	require.NoError(t, mgr.Initialize(), "fresh unprefixed v2 init should succeed")
	require.NoError(t, mgr.Close())

	require.NoError(t, withForeignKeyChecksOff(coexTestDB, func(tx *gorm.DB) error {
		seedErr := insertV2NotificationHistories(tx, rows)
		dropErr := tx.Exec("DROP TABLE IF EXISTS `detections`").Error
		return errors.Join(seedErr, dropErr)
	}))
	require.False(t, coexTestDB.Migrator().HasTable("detections"))

	require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))

	settings := buildTestSettings(coexTestHost, coexTestPort, coexTestDBName, coexTestUser, coexTestPassword)
	// datastore.New picks SQLite first when it is enabled.
	settings.Output.SQLite.Enabled = false
	return settings
}

// TestMySQL_LegacyOpen_V2ShapedNotificationHistory covers legacy startup over a
// fresh unprefixed v2 database that has lost its detections table and holds a
// v2-shaped notification_histories table. Rows in that table must never be
// dropped; an empty table is recreated in the legacy shape.
func TestMySQL_LegacyOpen_V2ShapedNotificationHistory(t *testing.T) {
	t.Run("holds_rows_refuses_and_keeps_them", func(t *testing.T) {
		// Two v2 rows share notification_type, which is the state that used to
		// fail legacy AutoMigrate with Error 1062.
		settings := seedV2ShapedNotificationHistoryWithoutDetections(t, 2)

		for boot := 1; boot <= 2; boot++ {
			store := datastore.New(settings)
			require.ErrorContains(t, store.Open(), "notification_histories=2",
				"legacy Open must refuse on boot %d with the v2-shaped table guard", boot)

			assert.True(t, coexTestDB.Migrator().HasColumn("notification_histories", "label_id"),
				"notification_histories must keep its v2 shape on boot %d", boot)
			assert.EqualValues(t, 2, getRowCount(t, coexTestDB, "notification_histories"),
				"no row may be dropped on boot %d", boot)
		}
	})

	t.Run("empty_is_recreated_in_legacy_shape", func(t *testing.T) {
		settings := seedV2ShapedNotificationHistoryWithoutDetections(t, 0)

		store := datastore.New(settings)
		require.NoError(t, store.Open(), "legacy Open should recreate the empty v2-shaped table")
		t.Cleanup(func() { _ = store.Close() })

		assert.False(t, coexTestDB.Migrator().HasColumn("notification_histories", "label_id"),
			"notification_histories should be recreated in the legacy shape")

		mysqlStore, ok := store.(*datastore.MySQLStore)
		require.True(t, ok, "store should be a MySQLStore")
		now := time.Now()
		require.NoError(t, mysqlStore.SaveNotificationHistory(t.Context(), &datastore.NotificationHistory{
			ScientificName: "Parus major",
			LastSent:       now,
			ExpiresAt:      now.Add(time.Hour),
			CreatedAt:      now,
		}), "legacy notification insert should work on the recreated table")
		require.NoError(t, store.Close())

		reopened := datastore.New(settings)
		require.NoError(t, reopened.Open(), "a second legacy Open should succeed")
		t.Cleanup(func() { _ = reopened.Close() })
		assert.EqualValues(t, 1, getRowCount(t, coexTestDB, "notification_histories"),
			"the legacy row must survive the second boot")
	})
}

// insertV2NotificationHistories inserts n v2-shaped notification_histories
// rows that share a notification_type.
func insertV2NotificationHistories(tx *gorm.DB, n int) error {
	for labelID := 1; labelID <= n; labelID++ {
		if err := tx.Exec(`INSERT INTO notification_histories
			(label_id, notification_type, last_sent, expires_at, created_at, updated_at)
			VALUES (?, 'new_species', NOW(), NOW() + INTERVAL 1 DAY, NOW(), NOW())`, labelID).Error; err != nil {
			return err
		}
	}
	return nil
}
