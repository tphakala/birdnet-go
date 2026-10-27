//go:build integration

// Package datastore: MySQL integration tests for reconcileLegacyUniqueIndexes.
//
// These run inside a disposable MySQL or MariaDB container (see
// MYSQL_TEST_IMAGE) provisioned by internal/testutil/containers. Guard: build tag "integration". The tests
// share one container and MUST NOT use t.Parallel() since they mutate
// schema.
package datastore

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var (
	reconTestContainer *containers.MySQLContainer
	reconTestDB        *gorm.DB
	reconTestDBName    string
)

func TestMain(m *testing.M) {
	os.Exit(reconTestMain(m))
}

func reconTestMain(m *testing.M) int {
	if code, skip := containers.SkipTestMainIfContainerRuntimeUnavailable(); skip {
		return code
	}
	ctx := context.Background() //nolint:gocritic // TestMain has no *testing.T

	cfg := &containers.MySQLConfig{
		Database:     "recon_test",
		RootPassword: "test",
		Username:     "testuser",
		Password:     "testpass",
		ImageTag:     "8.0",
	}

	var err error
	reconTestContainer, err = containers.NewMySQLContainer(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mysql container: %v\n", err)
		return 1
	}
	defer func() {
		if err := reconTestContainer.Terminate(context.Background()); err != nil { //nolint:gocritic // cleanup outlives test
			fmt.Fprintf(os.Stderr, "terminate container: %v\n", err)
		}
	}()

	host, err := reconTestContainer.GetHost(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "container host: %v\n", err)
		return 1
	}
	port, err := reconTestContainer.GetPort(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "container port: %v\n", err)
		return 1
	}

	reconTestDBName = cfg.Database
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.Username, cfg.Password, host, strconv.Itoa(port), cfg.Database)
	reconTestDB, err = gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorm open: %v\n", err)
		return 1
	}

	return m.Run()
}

// dropMySQLTable drops the table if present, so each test starts from a clean
// slate. Table names are test constants, never input.
func dropMySQLTable(t *testing.T, table string) {
	t.Helper()
	require.NoError(t, reconTestDB.Exec("DROP TABLE IF EXISTS `"+table+"`").Error)
}

// mysqlUniqueIndexNames returns unique-index names (excluding PRIMARY) for
// the given table in reconTestDB.
func mysqlUniqueIndexNames(t *testing.T, table string) []string {
	t.Helper()
	type row struct {
		IndexName string `gorm:"column:INDEX_NAME"`
	}
	var rows []row
	q := `SELECT DISTINCT INDEX_NAME FROM information_schema.STATISTICS
	      WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND NON_UNIQUE = 0 AND INDEX_NAME <> 'PRIMARY'`
	require.NoError(t, reconTestDB.Raw(q, reconTestDBName, table).Scan(&rows).Error)
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.IndexName)
	}
	return names
}

// mysqlRowCount returns the number of rows in table in reconTestDB. Table
// names are test constants, never input.
func mysqlRowCount(t *testing.T, table string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, reconTestDB.Raw("SELECT COUNT(*) FROM `"+table+"`").Scan(&count).Error)
	return count
}

// TestReconcile_MySQL_DropsStalePrecursor is the MySQL equivalent of the
// NotificationHistory test in index_reconcile_test.go: seed a table whose live
// unique index is a strict subset of the declared composite, run the
// reconciler, assert the stale index is gone, the composite is then created by
// AutoMigrate and left alone by a second run, and no row is lost.
func TestReconcile_MySQL_DropsStalePrecursor(t *testing.T) {
	dropMySQLTable(t, "notification_histories")

	require.NoError(t, reconTestDB.Exec(`
		CREATE TABLE notification_histories (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			scientific_name VARCHAR(200) NOT NULL,
			notification_type VARCHAR(50) NOT NULL DEFAULT 'new_species',
			last_sent DATETIME(3) NOT NULL,
			expires_at DATETIME(3) NOT NULL,
			created_at DATETIME(3) NOT NULL,
			updated_at DATETIME(3) NOT NULL,
			UNIQUE KEY idx_nh_species_legacy (scientific_name)
		) ENGINE=InnoDB
	`).Error)
	require.NoError(t, reconTestDB.Exec(`
		INSERT INTO notification_histories
		(scientific_name, last_sent, expires_at, created_at, updated_at)
		VALUES ('Erithacus rubecula', NOW(3), NOW(3), NOW(3), NOW(3))
	`).Error)

	models := []any{&NotificationHistory{}}
	require.NoError(t, reconcileLegacyUniqueIndexes(reconTestDB, "mysql", reconTestDBName, models))

	// Checked before AutoMigrate: GORM's MySQL migrator can drop a single-column
	// unique index itself, which would hide a reconciler that did nothing.
	assert.NotContains(t, mysqlUniqueIndexNames(t, "notification_histories"), "idx_nh_species_legacy",
		"reconciler should drop the stale UNIQUE(scientific_name) index")

	require.NoError(t, reconTestDB.AutoMigrate(&NotificationHistory{}))
	afterMigrate := mysqlUniqueIndexNames(t, "notification_histories")
	assert.Contains(t, afterMigrate, "idx_notification_history_species_type",
		"AutoMigrate must create the declared composite unique index")

	// Pins multi-column introspection: a live composite reported once per column
	// would look like a strict subset of itself and be dropped here.
	require.NoError(t, reconcileLegacyUniqueIndexes(reconTestDB, "mysql", reconTestDBName, models))
	assert.ElementsMatch(t, afterMigrate, mysqlUniqueIndexNames(t, "notification_histories"),
		"second reconcile must leave the composite alone")

	assert.Equal(t, int64(1), mysqlRowCount(t, "notification_histories"), "row must survive reconcile + AutoMigrate")
}

// TestReconcile_MySQL_UpgradesPerModelThresholds runs the startup order of
// manage.go on the real pre-per-species dynamic_thresholds shape, with one
// species stored under two models. That duplicate is what made the new unique
// index fail with MySQL Error 1062 on every restart: the reconciler keeps the
// composite (a superset of the declared index), the migration consolidates
// the rows and drops it, and AutoMigrate then creates the unique index. A
// second boot must change nothing.
func TestReconcile_MySQL_UpgradesPerModelThresholds(t *testing.T) {
	dropMySQLTable(t, "dynamic_thresholds")
	createLegacyDynamicThresholdTable(t, reconTestDB)
	expiresAt := time.Now().Add(time.Hour)
	insertLegacyRow(t, reconTestDB, "robin", "BirdNET", 1, 2, 3, expiresAt)
	insertLegacyRow(t, reconTestDB, "robin", "Perch", 2, 1, 1, expiresAt)
	insertLegacyRow(t, reconTestDB, "wren", "BirdNET", 0, 0, 0, expiresAt)

	models := []any{&DynamicThreshold{}}
	reconcile := func(t *testing.T) {
		t.Helper()
		require.NoError(t, reconcileLegacyUniqueIndexes(reconTestDB, "mysql", reconTestDBName, models))
	}
	migrate := func(t *testing.T) {
		t.Helper()
		require.NoError(t, migrateDynamicThresholdsToPerSpecies(reconTestDB, GetLogger()))
		require.NoError(t, reconTestDB.AutoMigrate(&DynamicThreshold{}),
			"AutoMigrate must succeed once the rows are consolidated (MySQL Error 1062 regression)")
	}

	reconcile(t)
	assert.Contains(t, mysqlUniqueIndexNames(t, "dynamic_thresholds"), "idx_dt_species_model",
		"reconciler must keep the composite: it is a superset of the declared index")
	migrate(t)

	assertUpgraded := func(t *testing.T) {
		t.Helper()
		names := mysqlUniqueIndexNames(t, "dynamic_thresholds")
		assert.Contains(t, names, "idx_dynamic_thresholds_species_name")
		assert.NotContains(t, names, "idx_dt_species_model")
		assert.False(t, reconTestDB.Migrator().HasColumn("dynamic_thresholds", "model_name"),
			"model_name column must be dropped")
		assert.Equal(t, int64(2), mysqlRowCount(t, "dynamic_thresholds"), "one row per species")
	}
	assertUpgraded(t)
	before := mysqlUniqueIndexNames(t, "dynamic_thresholds")

	reconcile(t)
	migrate(t)
	assertUpgraded(t)
	assert.ElementsMatch(t, before, mysqlUniqueIndexNames(t, "dynamic_thresholds"),
		"second boot must change nothing")
}

// TestReconcile_MySQL_Idempotent verifies two consecutive reconciler runs
// leave the index set unchanged once the DB is healthy.
func TestReconcile_MySQL_Idempotent(t *testing.T) {
	dropMySQLTable(t, "dynamic_thresholds")
	require.NoError(t, reconTestDB.AutoMigrate(&DynamicThreshold{}))
	before := mysqlUniqueIndexNames(t, "dynamic_thresholds")

	require.NoError(t, reconcileLegacyUniqueIndexes(reconTestDB, "mysql", reconTestDBName, []any{&DynamicThreshold{}}))
	require.NoError(t, reconcileLegacyUniqueIndexes(reconTestDB, "mysql", reconTestDBName, []any{&DynamicThreshold{}}))

	after := mysqlUniqueIndexNames(t, "dynamic_thresholds")
	assert.ElementsMatch(t, before, after)
}

// TestReconcile_MySQL_EmptyDBName is defensive: passing an empty dbName
// must not error and must not drop anything.
func TestReconcile_MySQL_EmptyDBName(t *testing.T) {
	dropMySQLTable(t, "dynamic_thresholds")
	require.NoError(t, reconTestDB.AutoMigrate(&DynamicThreshold{}))
	before := mysqlUniqueIndexNames(t, "dynamic_thresholds")

	require.NoError(t, reconcileLegacyUniqueIndexes(reconTestDB, "mysql", "", []any{&DynamicThreshold{}}))

	after := mysqlUniqueIndexNames(t, "dynamic_thresholds")
	assert.ElementsMatch(t, before, after, "empty dbName path must be a no-op")
}

// TestReconcile_MySQL_ToleratesAlreadyDropped simulates the concurrent-drop
// race: the reconciler reads the stale index into memory, the operator (or
// another starting instance) drops it, and then the reconciler's DROP INDEX
// should not bubble MySQL error 1091 back as a failure.
func TestReconcile_MySQL_ToleratesAlreadyDropped(t *testing.T) {
	dropMySQLTable(t, "dynamic_thresholds")
	require.NoError(t, reconTestDB.Exec(`
		CREATE TABLE dynamic_thresholds (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			species_name VARCHAR(200) NOT NULL,
			model_name VARCHAR(100) NOT NULL DEFAULT 'BirdNET',
			scientific_name VARCHAR(200),
			level INT NOT NULL DEFAULT 0,
			current_value DOUBLE NOT NULL,
			base_threshold DOUBLE NOT NULL,
			high_conf_count INT NOT NULL DEFAULT 0,
			valid_hours INT NOT NULL,
			expires_at DATETIME NOT NULL,
			last_triggered DATETIME NOT NULL,
			first_created DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			trigger_count INT NOT NULL DEFAULT 0,
			UNIQUE KEY idx_dt_species_legacy (species_name)
		) ENGINE=InnoDB
	`).Error)

	// Directly drop the stale index first to simulate a concurrent dropper.
	// The reconciler will still think the index exists from its earlier read,
	// but the low-level dropUniqueIndex call must swallow MySQL error 1091.
	idx := dbUniqueIndex{
		Name:    "idx_dt_species_legacy",
		Table:   "dynamic_thresholds",
		Columns: []string{"species_name"},
	}
	require.NoError(t, reconTestDB.Exec(
		"ALTER TABLE `dynamic_thresholds` DROP INDEX `idx_dt_species_legacy`").Error,
		"precondition: first drop should succeed")
	assert.NoError(t, dropUniqueIndex(reconTestDB, "mysql", idx),
		"second drop via reconciler helper must swallow MySQL error 1091")
}
