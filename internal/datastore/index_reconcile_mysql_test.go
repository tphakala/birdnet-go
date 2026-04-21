//go:build integration

// Package datastore: MySQL integration tests for reconcileLegacyUniqueIndexes.
//
// These run inside a disposable MySQL 8.0 container provisioned by
// internal/testutil/containers. Guard: build tag "integration". The tests
// share one container and MUST NOT use t.Parallel() since they mutate
// schema.
package datastore

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

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

// resetMySQLDynamicThresholds drops the table if present, so each test starts
// from a clean slate.
func resetMySQLDynamicThresholds(t *testing.T) {
	t.Helper()
	require.NoError(t, reconTestDB.Exec("DROP TABLE IF EXISTS dynamic_thresholds").Error)
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

// TestReconcile_MySQL_DropsStaleSpeciesName is the MySQL equivalent of the
// SQLite test in index_reconcile_test.go: seed a pre-multi-model legacy
// shape, run the reconciler, assert the stale index is gone and the
// composite survives.
func TestReconcile_MySQL_DropsStaleSpeciesName(t *testing.T) {
	resetMySQLDynamicThresholds(t)

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
			UNIQUE KEY idx_dt_species_legacy (species_name),
			KEY idx_expires (expires_at),
			KEY idx_last_trig (last_triggered)
		) ENGINE=InnoDB
	`).Error)

	require.NoError(t, reconcileLegacyUniqueIndexes(reconTestDB, "mysql", reconTestDBName, []any{&DynamicThreshold{}}))

	names := mysqlUniqueIndexNames(t, "dynamic_thresholds")
	assert.NotContains(t, names, "idx_dt_species_legacy",
		"reconciler should drop stale UNIQUE(species_name) index")
}

// TestReconcile_MySQL_AutoMigrateSucceedsAfterReconcile is the end-to-end
// regression for the MySQL Error 1062 restart loop: stale index present,
// reconciler runs, AutoMigrate(&DynamicThreshold{}) must then complete
// without error.
func TestReconcile_MySQL_AutoMigrateSucceedsAfterReconcile(t *testing.T) {
	resetMySQLDynamicThresholds(t)

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
			UNIQUE KEY idx_dt_species_legacy (species_name),
			KEY idx_expires (expires_at),
			KEY idx_last_trig (last_triggered)
		) ENGINE=InnoDB
	`).Error)
	require.NoError(t, reconTestDB.Exec(`
		INSERT INTO dynamic_thresholds
		(species_name, model_name, current_value, base_threshold, valid_hours, expires_at, last_triggered, first_created, updated_at)
		VALUES ('robin', 'BirdNET', 0.8, 0.5, 24, NOW(), NOW(), NOW(), NOW())
	`).Error)

	require.NoError(t, reconcileLegacyUniqueIndexes(reconTestDB, "mysql", reconTestDBName, []any{&DynamicThreshold{}}))
	require.NoError(t, reconTestDB.AutoMigrate(&DynamicThreshold{}),
		"AutoMigrate must succeed after reconciler drops the stale index (MySQL Error 1062 regression)")

	names := mysqlUniqueIndexNames(t, "dynamic_thresholds")
	assert.Contains(t, names, "idx_dt_species_model",
		"composite unique index must be created by AutoMigrate post-reconcile")

	var count int64
	require.NoError(t, reconTestDB.Raw(`SELECT COUNT(*) FROM dynamic_thresholds`).Scan(&count).Error)
	assert.Equal(t, int64(1), count, "row must survive reconcile + AutoMigrate")
}

// TestReconcile_MySQL_Idempotent verifies two consecutive reconciler runs
// leave the index set unchanged once the DB is healthy.
func TestReconcile_MySQL_Idempotent(t *testing.T) {
	resetMySQLDynamicThresholds(t)
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
	resetMySQLDynamicThresholds(t)
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
	resetMySQLDynamicThresholds(t)
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
