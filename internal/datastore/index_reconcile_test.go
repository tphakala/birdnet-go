// Package datastore: SQLite-level tests for reconcileLegacyUniqueIndexes.
//
// These tests exercise real SQLite (in-memory, no cgo) to verify that the
// reconciler correctly drops DB-side unique indexes that the GORM entity no
// longer declares. They guard against the stale UNIQUE(species_name) index on
// dynamic_thresholds that lingers after the composite idx_dt_species_model
// was introduced.
package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// openSQLiteTestDB returns a fresh in-memory SQLite GORM DB with silent logging.
// Callers get a clean schema each time; the DB is closed when t ends.
//
// SetMaxOpenConns(1) pins the pool to a single connection so the ":memory:"
// database stays consistent across all queries in the test. Without this,
// database/sql's default connection pool can create multiple connections,
// each with its own independent in-memory database, leading to flaky "no
// such table" errors under concurrent query paths.
func openSQLiteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err, "open in-memory sqlite")
	sqlDB, err := db.DB()
	require.NoError(t, err, "retrieve *sql.DB from gorm")
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	return db
}

// sqliteIndexNames returns all index names on the given table in load order.
func sqliteIndexNames(t *testing.T, db *gorm.DB, table string) []string {
	t.Helper()
	type row struct {
		Name string `gorm:"column:name"`
	}
	var rows []row
	require.NoError(t, db.Raw("PRAGMA index_list('"+table+"')").Scan(&rows).Error)
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	return names
}

// TestReconcileLegacyUniqueIndexes_SQLite_DropsStaleSpeciesName verifies the
// reconciler drops a pre-multi-model UNIQUE(species_name) index on
// dynamic_thresholds (the exact legacy shape behind the SQLite
// UNIQUE-constraint insert failures).
func TestReconcileLegacyUniqueIndexes_SQLite_DropsStaleSpeciesName(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)

	// Seed the legacy-shaped table: DynamicThreshold columns + a stale single-column
	// unique index on species_name (no composite idx_dt_species_model yet).
	require.NoError(t, db.Exec(`
		CREATE TABLE dynamic_thresholds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			species_name TEXT NOT NULL,
			model_name TEXT NOT NULL DEFAULT 'BirdNET',
			scientific_name TEXT,
			level INTEGER NOT NULL DEFAULT 0,
			current_value REAL NOT NULL,
			base_threshold REAL NOT NULL,
			high_conf_count INTEGER NOT NULL DEFAULT 0,
			valid_hours INTEGER NOT NULL,
			expires_at DATETIME NOT NULL,
			last_triggered DATETIME NOT NULL,
			first_created DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			trigger_count INTEGER NOT NULL DEFAULT 0
		)
	`).Error)
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX idx_dt_species_legacy ON dynamic_thresholds(species_name)`).Error)

	// Run the reconciler against the current DynamicThreshold entity definition.
	require.NoError(t, reconcileLegacyUniqueIndexes(db, "sqlite", "", []any{&DynamicThreshold{}}))

	// Stale single-column unique index must be gone.
	indexes := sqliteIndexNames(t, db, "dynamic_thresholds")
	assert.NotContains(t, indexes, "idx_dt_species_legacy",
		"reconciler should have dropped legacy UNIQUE(species_name) index")
}

// TestReconcileLegacyUniqueIndexes_SQLite_PreservesComposite verifies the
// reconciler leaves the declared composite idx_dt_species_model alone.
func TestReconcileLegacyUniqueIndexes_SQLite_PreservesComposite(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&DynamicThreshold{}))

	require.NoError(t, reconcileLegacyUniqueIndexes(db, "sqlite", "", []any{&DynamicThreshold{}}))

	indexes := sqliteIndexNames(t, db, "dynamic_thresholds")
	assert.Contains(t, indexes, "idx_dt_species_model",
		"declared composite unique index must survive reconciliation")
}

// TestReconcileLegacyUniqueIndexes_SQLite_DropsStaleModelName covers the
// alternative legacy shape where the stale unique index was on model_name
// alone (paranoia: the MySQL 1062 error displays 'BirdNET' as the duplicate
// value, hinting this layout is possible in the wild).
func TestReconcileLegacyUniqueIndexes_SQLite_DropsStaleModelName(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&DynamicThreshold{}))
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX idx_dt_model_legacy ON dynamic_thresholds(model_name)`).Error)

	require.NoError(t, reconcileLegacyUniqueIndexes(db, "sqlite", "", []any{&DynamicThreshold{}}))

	indexes := sqliteIndexNames(t, db, "dynamic_thresholds")
	assert.NotContains(t, indexes, "idx_dt_model_legacy")
	assert.Contains(t, indexes, "idx_dt_species_model")
}

// TestReconcileLegacyUniqueIndexes_SQLite_FreshInstall verifies the
// reconciler is a no-op when AutoMigrate has never run (table absent).
func TestReconcileLegacyUniqueIndexes_SQLite_FreshInstall(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)

	require.NoError(t, reconcileLegacyUniqueIndexes(db, "sqlite", "", []any{&DynamicThreshold{}}))

	assert.False(t, db.Migrator().HasTable(&DynamicThreshold{}),
		"reconciler must not create tables")
}

// TestReconcileLegacyUniqueIndexes_SQLite_Idempotent verifies running the
// reconciler twice on an already-correct DB is a no-op.
func TestReconcileLegacyUniqueIndexes_SQLite_Idempotent(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&DynamicThreshold{}))
	before := sqliteIndexNames(t, db, "dynamic_thresholds")

	require.NoError(t, reconcileLegacyUniqueIndexes(db, "sqlite", "", []any{&DynamicThreshold{}}))
	require.NoError(t, reconcileLegacyUniqueIndexes(db, "sqlite", "", []any{&DynamicThreshold{}}))

	after := sqliteIndexNames(t, db, "dynamic_thresholds")
	assert.ElementsMatch(t, before, after, "idempotent runs must not change index set")
}

// noisyEntity is a test-only GORM entity pinned to the "noisy" table name.
// The explicit TableName() prevents GORM's default pluralization from
// resolving the struct to "noisies", which would make the autoindex test
// vacuous (the reconciler would skip a non-existent table).
type noisyEntity struct {
	K string `gorm:"primaryKey;column:k"`
	V string `gorm:"column:v"`
}

// TableName overrides GORM's naming strategy for this test entity.
func (noisyEntity) TableName() string { return "noisy" }

// TestReconcileLegacyUniqueIndexes_SQLite_IgnoresAutoIndex verifies the
// reconciler does not attempt to drop sqlite_autoindex_* internal indexes
// that SQLite creates for PRIMARY KEY / inline UNIQUE constraints.
func TestReconcileLegacyUniqueIndexes_SQLite_IgnoresAutoIndex(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	// A table whose PRIMARY KEY is a TEXT column generates a
	// sqlite_autoindex_ entry visible in PRAGMA index_list.
	require.NoError(t, db.Exec(`CREATE TABLE noisy (k TEXT PRIMARY KEY, v TEXT)`).Error)

	// Give the reconciler an entity that maps to the same table name but
	// declares no unique indexes at all, so the implicit PK autoindex would
	// be "stale" by naive column-set comparison. The explicit TableName()
	// method overrides GORM's default pluralization (which would resolve the
	// struct "noisy" to "noisies" and skip the seeded table entirely).
	require.NoError(t, reconcileLegacyUniqueIndexes(db, "sqlite", "", []any{&noisyEntity{}}))

	// Verify the table is still usable.
	require.NoError(t, db.Exec(`INSERT INTO noisy(k, v) VALUES ('a', '1')`).Error)
}

// TestReconcileLegacyUniqueIndexes_SQLite_PreservesSuperset verifies the
// reconciler does NOT drop a stricter admin-added unique index whose
// column set is a superset of a declared composite. Preserving such
// constraints is required so the reconciler cannot relax uniqueness rules
// the operator explicitly imposed.
func TestReconcileLegacyUniqueIndexes_SQLite_PreservesSuperset(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&DynamicThreshold{}))
	// Simulated admin-added stricter constraint: composite over declared
	// (species_name, model_name) plus an extra column.
	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX idx_dt_admin_superset
		ON dynamic_thresholds(species_name, model_name, scientific_name)
	`).Error)

	require.NoError(t, reconcileLegacyUniqueIndexes(db, "sqlite", "", []any{&DynamicThreshold{}}))

	indexes := sqliteIndexNames(t, db, "dynamic_thresholds")
	assert.Contains(t, indexes, "idx_dt_admin_superset",
		"superset unique index must be preserved (not a legacy precursor)")
	assert.Contains(t, indexes, "idx_dt_species_model",
		"declared composite must still be present")
}

// TestReconcileLegacyUniqueIndexes_SQLite_PreservesUnrelated verifies the
// reconciler does NOT drop unique indexes whose column set does not overlap
// with any declared unique index. Prevents collateral damage when an
// operator added domain-specific uniqueness rules.
func TestReconcileLegacyUniqueIndexes_SQLite_PreservesUnrelated(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&DynamicThreshold{}))
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX idx_dt_unrelated ON dynamic_thresholds(scientific_name)`).Error)

	require.NoError(t, reconcileLegacyUniqueIndexes(db, "sqlite", "", []any{&DynamicThreshold{}}))

	indexes := sqliteIndexNames(t, db, "dynamic_thresholds")
	assert.Contains(t, indexes, "idx_dt_unrelated",
		"unrelated unique index (columns not a subset of any declared) must be preserved")
}

// TestReconcileLegacyUniqueIndexes_SQLite_PreservesData verifies the drop
// does not destroy existing rows.
func TestReconcileLegacyUniqueIndexes_SQLite_PreservesData(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.Exec(`
		CREATE TABLE dynamic_thresholds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			species_name TEXT NOT NULL,
			model_name TEXT NOT NULL DEFAULT 'BirdNET',
			scientific_name TEXT,
			level INTEGER NOT NULL DEFAULT 0,
			current_value REAL NOT NULL,
			base_threshold REAL NOT NULL,
			high_conf_count INTEGER NOT NULL DEFAULT 0,
			valid_hours INTEGER NOT NULL,
			expires_at DATETIME NOT NULL,
			last_triggered DATETIME NOT NULL,
			first_created DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			trigger_count INTEGER NOT NULL DEFAULT 0
		)
	`).Error)
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX idx_dt_species_legacy ON dynamic_thresholds(species_name)`).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO dynamic_thresholds
		(species_name, model_name, current_value, base_threshold, valid_hours, expires_at, last_triggered, first_created, updated_at)
		VALUES ('robin', 'BirdNET', 0.8, 0.5, 24, '2026-04-18', '2026-04-18', '2026-04-18', '2026-04-18')
	`).Error)

	require.NoError(t, reconcileLegacyUniqueIndexes(db, "sqlite", "", []any{&DynamicThreshold{}}))

	var count int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM dynamic_thresholds`).Scan(&count).Error)
	assert.Equal(t, int64(1), count, "data must survive index drop")
}
