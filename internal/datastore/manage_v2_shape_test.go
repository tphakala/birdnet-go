package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Raw DDL for tables as the enhanced database (v2) creates them on a fresh
// unprefixed install. The legacy models never declared label_id, so a NOT NULL
// label_id marks a table the legacy store cannot write to.
const (
	testV2NotificationHistoriesDDL = `CREATE TABLE notification_histories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		label_id INTEGER NOT NULL,
		notification_type TEXT NOT NULL DEFAULT 'new_species',
		last_sent DATETIME NOT NULL,
		expires_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL)`
	testV2NotificationHistoriesIndexDDL = `CREATE UNIQUE INDEX idx_notification_label_type
		ON notification_histories (label_id, notification_type)`
	// Pre-#4198 v2 dynamic_thresholds: keyed by label_id, no species_name.
	testV2DynamicThresholdsDDL = `CREATE TABLE dynamic_thresholds (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		label_id INTEGER NOT NULL UNIQUE,
		level INTEGER NOT NULL DEFAULT 0,
		current_value REAL NOT NULL,
		base_threshold REAL NOT NULL,
		high_conf_count INTEGER NOT NULL DEFAULT 0,
		valid_hours INTEGER NOT NULL,
		expires_at DATETIME NOT NULL,
		last_triggered DATETIME NOT NULL,
		first_created DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		trigger_count INTEGER NOT NULL DEFAULT 0)`
	testV2ThresholdEventsDDL = `CREATE TABLE threshold_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		label_id INTEGER NOT NULL,
		previous_level INTEGER NOT NULL DEFAULT 0)`
	testV2DetectionsDDL = `CREATE TABLE detections (id INTEGER PRIMARY KEY AUTOINCREMENT, label_id INTEGER)`
)

// insertV2NotificationHistoryRows inserts n rows into a v2-shaped
// notification_histories table, one per label_id.
func insertV2NotificationHistoryRows(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	now := time.Now()
	for labelID := 1; labelID <= n; labelID++ {
		require.NoError(t, db.Exec(`INSERT INTO notification_histories
			(label_id, notification_type, last_sent, expires_at, created_at, updated_at)
			VALUES (?, 'new_species', ?, ?, ?, ?)`, labelID, now, now, now, now).Error)
	}
}

// seedV2ShapedAuxTables creates the v2-shaped notification_histories and
// dynamic_thresholds tables with two rows each.
func seedV2ShapedAuxTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(testV2NotificationHistoriesDDL).Error)
	require.NoError(t, db.Exec(testV2NotificationHistoriesIndexDDL).Error)
	require.NoError(t, db.Exec(testV2DynamicThresholdsDDL).Error)
	insertV2NotificationHistoryRows(t, db, 2)
	now := time.Now()
	for labelID := 1; labelID <= 2; labelID++ {
		require.NoError(t, db.Exec(`INSERT INTO dynamic_thresholds
			(label_id, current_value, base_threshold, valid_hours, expires_at, last_triggered, first_created, updated_at)
			VALUES (?, 0.5, 0.5, 24, ?, ?, ?, ?)`, labelID, now, now, now, now).Error)
	}
}

func columnExists(t *testing.T, db *gorm.DB, table, column string) bool {
	t.Helper()
	return db.Migrator().HasColumn(table, column)
}

func countRows(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Table(table).Count(&n).Error)
	return n
}

// TestPerformAutoMigration_V2ShapedTables verifies the legacy startup guard
// that drops empty tables left in the v2 shape (NOT NULL label_id), refuses to
// drop ones that hold rows, and refuses to migrate over a v2 database.
func TestPerformAutoMigration_V2ShapedTables(t *testing.T) {
	t.Run("refuses_while_v2_shaped_tables_hold_rows", func(t *testing.T) {
		db := openSQLiteTestDB(t)
		require.NoError(t, db.Exec(testV2NotificationHistoriesDDL).Error)
		require.NoError(t, db.Exec(testV2NotificationHistoriesIndexDDL).Error)
		require.NoError(t, db.Exec(testV2DynamicThresholdsDDL).Error)
		require.NoError(t, db.Exec(testV2ThresholdEventsDDL).Error)
		insertV2NotificationHistoryRows(t, db, 2)

		assertRefused := func(t *testing.T) {
			t.Helper()
			err := performAutoMigration(db, false, "SQLite", "")
			require.ErrorIs(t, err, errV2ShapedTablesHoldRows)
			assert.Contains(t, err.Error(), "notification_histories=2")
			for _, table := range []string{"notification_histories", "dynamic_thresholds", "threshold_events"} {
				assert.True(t, columnExists(t, db, table, "label_id"), "%s must be left untouched", table)
			}
			assert.EqualValues(t, 2, countRows(t, db, "notification_histories"))
			assert.False(t, db.Migrator().HasTable("notes"), "refusal must not create notes")
		}
		assertRefused(t)
		// A second boot refuses the same way.
		assertRefused(t)

		// Once the rows are gone, every candidate is empty and gets recreated.
		require.NoError(t, db.Exec(`DELETE FROM notification_histories`).Error)
		require.NoError(t, performAutoMigration(db, false, "SQLite", ""))
		for _, table := range []string{"notification_histories", "dynamic_thresholds", "threshold_events"} {
			assert.False(t, columnExists(t, db, table, "label_id"), "%s must be recreated in the legacy shape", table)
		}

		ds := &DataStore{DB: db}
		require.NoError(t, ds.SaveNotificationHistory(t.Context(), &NotificationHistory{
			ScientificName: "Parus major",
			LastSent:       time.Now(),
			ExpiresAt:      time.Now().Add(time.Hour),
			CreatedAt:      time.Now(),
		}))

		// A later boot must leave legacy rows alone.
		require.NoError(t, performAutoMigration(db, false, "SQLite", ""))
		assert.EqualValues(t, 1, countRows(t, db, "notification_histories"))
	})

	t.Run("refuses_when_row_count_fails", func(t *testing.T) {
		db := openSQLiteTestDB(t)
		require.NoError(t, db.Exec(testV2NotificationHistoriesDDL).Error)
		require.NoError(t, db.Exec(testV2NotificationHistoriesIndexDDL).Error)

		// Fail only GORM Query-chain statements on this table. Table listing and
		// column inspection run through the Row chain and are not affected.
		errCountFailed := errors.NewStd("count failed")
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:fail_count", func(tx *gorm.DB) {
			if tx.Statement.Table == "notification_histories" {
				_ = tx.AddError(errCountFailed)
			}
		}))

		err := performAutoMigration(db, false, "SQLite", "")

		require.ErrorIs(t, err, errV2ShapedTablesHoldRows)
		assert.Contains(t, err.Error(), "notification_histories=unknown")
		assert.True(t, db.Migrator().HasTable("notification_histories"))
		assert.True(t, columnExists(t, db, "notification_histories", "label_id"))
	})

	t.Run("refuses_v2_database_and_leaves_it_untouched", func(t *testing.T) {
		db := openSQLiteTestDB(t)
		seedV2ShapedAuxTables(t, db)
		require.NoError(t, db.Exec(testV2DetectionsDDL).Error)

		err := performAutoMigration(db, false, "SQLite", "")

		require.ErrorIs(t, err, errV2DatabaseInLegacyMode)
		assert.True(t, columnExists(t, db, "notification_histories", "label_id"))
		assert.EqualValues(t, 2, countRows(t, db, "notification_histories"))
		assert.False(t, db.Migrator().HasTable("notes"), "refusal must not create notes")
	})

	t.Run("refuses_v2_database_without_candidate_tables", func(t *testing.T) {
		db := openSQLiteTestDB(t)
		require.NoError(t, db.Exec(testV2DetectionsDDL).Error)

		err := performAutoMigration(db, false, "SQLite", "")

		require.ErrorIs(t, err, errV2DatabaseInLegacyMode)
		assert.False(t, db.Migrator().HasTable("notes"), "refusal must not create notes")
	})

	t.Run("refuses_split_brain_with_populated_v2_shaped_tables", func(t *testing.T) {
		db := openSQLiteTestDB(t)
		seedV2ShapedAuxTables(t, db)
		require.NoError(t, db.Exec(testV2DetectionsDDL).Error)
		require.NoError(t, db.Exec(`INSERT INTO detections (label_id) VALUES (1)`).Error)
		require.NoError(t, db.AutoMigrate(&Note{}))
		require.NoError(t, db.Create(&Note{ScientificName: "Parus major"}).Error)

		err := performAutoMigration(db, false, "SQLite", "")

		require.ErrorIs(t, err, errV2ShapedTablesHoldRows)
		assert.True(t, columnExists(t, db, "notification_histories", "label_id"))
		assert.EqualValues(t, 2, countRows(t, db, "notification_histories"))
		assert.EqualValues(t, 1, countRows(t, db, "notes"))
	})

	t.Run("leaves_nullable_label_id_alone", func(t *testing.T) {
		db := openSQLiteTestDB(t)
		require.NoError(t, db.AutoMigrate(&DynamicThreshold{}))
		require.NoError(t, db.Create(&DynamicThreshold{
			SpeciesName:   "Parus major",
			CurrentValue:  0.5,
			BaseThreshold: 0.5,
			ValidHours:    24,
			ExpiresAt:     time.Now().Add(time.Hour),
			LastTriggered: time.Now(),
			FirstCreated:  time.Now(),
		}).Error)
		require.NoError(t, db.Exec(`ALTER TABLE dynamic_thresholds ADD COLUMN label_id INTEGER`).Error)

		require.NoError(t, performAutoMigration(db, false, "SQLite", ""))

		assert.True(t, columnExists(t, db, "dynamic_thresholds", "label_id"))
		assert.EqualValues(t, 1, countRows(t, db, "dynamic_thresholds"))
	})
}
