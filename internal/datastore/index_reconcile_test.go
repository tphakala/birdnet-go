// Package datastore: SQLite-level tests for reconcileLegacyUniqueIndexes.
//
// These tests exercise real SQLite (in-memory, no cgo) to verify that the
// reconciler correctly drops DB-side unique indexes that the GORM entity no
// longer declares. They guard Forgejo #436 / #469: stale UNIQUE(species_name)
// index on dynamic_thresholds after the composite idx_dt_species_model was
// introduced.
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
func openSQLiteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err, "open in-memory sqlite")
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
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
// dynamic_thresholds (the exact legacy shape from Forgejo #436).
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
