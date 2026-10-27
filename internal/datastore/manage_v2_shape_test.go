package datastore

import (
	"cmp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
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
	// v2 image_caches: keyed by label_id, unique per provider and label.
	testV2ImageCachesDDL = `CREATE TABLE image_caches (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider_name TEXT NOT NULL DEFAULT 'wikimedia',
		label_id INTEGER NOT NULL,
		source_provider TEXT NOT NULL DEFAULT 'wikimedia',
		url TEXT,
		cached_at DATETIME,
		created_at DATETIME,
		updated_at DATETIME)`
	testV2ImageCachesIndexDDL = `CREATE UNIQUE INDEX idx_image_cache_provider_label
		ON image_caches (provider_name, label_id)`
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

// insertV2ImageCacheRows inserts n rows into a v2-shaped image_caches table.
func insertV2ImageCacheRows(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	for labelID := 1; labelID <= n; labelID++ {
		require.NoError(t, db.Exec(`INSERT INTO image_caches (label_id, url) VALUES (?, 'https://example.org/a.jpg')`, labelID).Error)
	}
}

// insertV2DynamicThresholdRows inserts n rows into a v2-shaped dynamic_thresholds table.
func insertV2DynamicThresholdRows(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	now := time.Now()
	for labelID := 1; labelID <= n; labelID++ {
		require.NoError(t, db.Exec(`INSERT INTO dynamic_thresholds
			(label_id, current_value, base_threshold, valid_hours, expires_at, last_triggered, first_created, updated_at)
			VALUES (?, 0.5, 0.5, 24, ?, ?, ?, ?)`, labelID, now, now, now, now).Error)
	}
}

// openSQLiteFileTestDB opens the SQLite file at path with a fresh connection, so
// each call models one boot. The connection is closed when t ends.
func openSQLiteFileTestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err, "open sqlite file")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
	return db
}

// bootSQLiteFile runs one legacy startup migration on a fresh connection to the
// file at path, closes the connection and returns the captured log and the
// error.
func bootSQLiteFile(t *testing.T, path string) (string, error) {
	t.Helper()
	db := openSQLiteFileTestDB(t, path)
	var err error
	out := logtest.Capture(t, func() {
		err = performAutoMigration(db, false, "SQLite", "")
	})
	sqlDB, dbErr := db.DB()
	require.NoError(t, dbErr)
	require.NoError(t, sqlDB.Close())
	return out, err
}

// TestPerformAutoMigration_V2ShapedTables verifies the legacy startup guard
// that drops empty tables left in the v2 shape (NOT NULL label_id), drops the
// regenerable notification_histories and image_caches with their rows, refuses
// to drop any other table that holds rows, and refuses to migrate over a v2
// database.
func TestPerformAutoMigration_V2ShapedTables(t *testing.T) {
	t.Run("drops_regenerable_tables_with_rows_and_boots_twice", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "birdnet.db")
		seed := openSQLiteFileTestDB(t, path)
		require.NoError(t, seed.Exec(testV2NotificationHistoriesDDL).Error)
		require.NoError(t, seed.Exec(testV2NotificationHistoriesIndexDDL).Error)
		require.NoError(t, seed.Exec(testV2ImageCachesDDL).Error)
		require.NoError(t, seed.Exec(testV2ImageCachesIndexDDL).Error)
		require.NoError(t, seed.Exec(testV2DynamicThresholdsDDL).Error)
		require.NoError(t, seed.Exec(testV2ThresholdEventsDDL).Error)
		insertV2NotificationHistoryRows(t, seed, 2)
		insertV2ImageCacheRows(t, seed, 1)
		sqlSeed, err := seed.DB()
		require.NoError(t, err)
		require.NoError(t, sqlSeed.Close())

		out, err := bootSQLiteFile(t, path)
		require.NoError(t, err, "boot 1")
		assert.Contains(t, out, "table=notification_histories", "boot 1 must log the dropped table")
		assert.Contains(t, out, "rows=2", "boot 1 must log the dropped row count")
		assert.Contains(t, out, "table=image_caches", "boot 1 must log the dropped table")
		assert.Contains(t, out, "rows=1", "boot 1 must log the dropped row count")

		db := openSQLiteFileTestDB(t, path)
		for _, table := range []string{"notification_histories", "image_caches", "dynamic_thresholds", "threshold_events"} {
			assert.False(t, columnExists(t, db, table, "label_id"), "%s must be recreated in the legacy shape", table)
		}
		assert.True(t, db.Migrator().HasTable("notes"))

		ds := &DataStore{DB: db}
		require.NoError(t, ds.SaveNotificationHistory(t.Context(), &NotificationHistory{
			ScientificName: "Parus major",
			LastSent:       time.Now(),
			ExpiresAt:      time.Now().Add(time.Hour),
			CreatedAt:      time.Now(),
		}))
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())

		// The second boot finds no v2-shaped table: it drops nothing and keeps the
		// row the legacy store wrote.
		out, err = bootSQLiteFile(t, path)
		require.NoError(t, err, "boot 2")
		assert.NotContains(t, out, "Dropping", "boot 2 must not drop anything")
		db = openSQLiteFileTestDB(t, path)
		assert.EqualValues(t, 1, countRows(t, db, "notification_histories"))
	})

	t.Run("refuses_and_drops_nothing_while_non_regenerable_tables_hold_rows", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "birdnet.db")
		seed := openSQLiteFileTestDB(t, path)
		require.NoError(t, seed.Exec(testV2NotificationHistoriesDDL).Error)
		require.NoError(t, seed.Exec(testV2NotificationHistoriesIndexDDL).Error)
		require.NoError(t, seed.Exec(testV2ImageCachesDDL).Error)
		require.NoError(t, seed.Exec(testV2ImageCachesIndexDDL).Error)
		require.NoError(t, seed.Exec(testV2DynamicThresholdsDDL).Error)
		require.NoError(t, seed.Exec(testV2ThresholdEventsDDL).Error)
		insertV2NotificationHistoryRows(t, seed, 2)
		insertV2ImageCacheRows(t, seed, 1)
		insertV2DynamicThresholdRows(t, seed, 1)
		require.NoError(t, seed.Exec(`INSERT INTO threshold_events (label_id) VALUES (1)`).Error)
		sqlSeed, err := seed.DB()
		require.NoError(t, err)
		require.NoError(t, sqlSeed.Close())

		for boot := 1; boot <= 2; boot++ {
			out, err := bootSQLiteFile(t, path)

			require.ErrorIs(t, err, errV2ShapedTablesHoldRows, "boot %d", boot)
			assert.Contains(t, err.Error(),
				"notification_histories=2, dynamic_thresholds=1, threshold_events=1, image_caches=1",
				"the refusal must list every candidate with its own count on boot %d", boot)
			assert.Contains(t, err.Error(), v2ShapedTablesGuideURL, "the error must point at the recovery guide")
			assert.Contains(t, out, "recovery_hint=", "the log line must carry the recovery hint")
			assert.Contains(t, out, v2ShapedTablesGuideURL, "the recovery hint must carry the guide URL")
			assert.NotContains(t, out, "Dropping", "boot %d must drop nothing", boot)

			db := openSQLiteFileTestDB(t, path)
			for _, table := range []string{"notification_histories", "dynamic_thresholds", "threshold_events", "image_caches"} {
				assert.True(t, columnExists(t, db, table, "label_id"), "%s must be left untouched on boot %d", table, boot)
			}
			assert.EqualValues(t, 2, countRows(t, db, "notification_histories"), "boot %d", boot)
			assert.EqualValues(t, 1, countRows(t, db, "image_caches"), "boot %d", boot)
			assert.EqualValues(t, 1, countRows(t, db, "dynamic_thresholds"), "boot %d", boot)
			assert.EqualValues(t, 1, countRows(t, db, "threshold_events"), "boot %d", boot)
			assert.False(t, db.Migrator().HasTable("notes"), "refusal must not create notes")
		}
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

		// The count keeps failing, so every boot refuses and leaves the table.
		for boot := 1; boot <= 2; boot++ {
			err := performAutoMigration(db, false, "SQLite", "")

			require.ErrorIs(t, err, errV2ShapedTablesHoldRows, "boot %d", boot)
			assert.Contains(t, err.Error(), "notification_histories=unknown", "boot %d", boot)
			assert.True(t, db.Migrator().HasTable("notification_histories"), "boot %d", boot)
			assert.True(t, columnExists(t, db, "notification_histories", "label_id"), "boot %d", boot)
		}
	})

	t.Run("refuses_when_rows_appear_before_a_drop", func(t *testing.T) {
		db := openSQLiteTestDB(t)
		require.NoError(t, db.Exec(testV2NotificationHistoriesDDL).Error)
		require.NoError(t, db.Exec(testV2NotificationHistoriesIndexDDL).Error)
		require.NoError(t, db.Exec(testV2DynamicThresholdsDDL).Error)
		require.NoError(t, db.Exec(testV2ThresholdEventsDDL).Error)
		insertV2NotificationHistoryRows(t, db, 2)

		// A second instance writes dynamic_thresholds after the count pass has
		// read every table as empty: the insert runs once, right after the count
		// of the last candidate.
		inserted := false
		require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:row_appears", func(tx *gorm.DB) {
			if inserted || tx.Statement.Table != "threshold_events" {
				return
			}
			inserted = true
			now := time.Now()
			_ = tx.Session(&gorm.Session{NewDB: true}).Exec(`INSERT INTO dynamic_thresholds
				(label_id, current_value, base_threshold, valid_hours, expires_at, last_triggered, first_created, updated_at)
				VALUES (1, 0.5, 0.5, 24, ?, ?, ?, ?)`, now, now, now, now).Error
		}))

		// Not parallel: logtest.Capture swaps the process-global logger.
		var err error
		out := logtest.Capture(t, func() {
			err = performAutoMigration(db, false, "SQLite", "")
		})

		require.ErrorIs(t, err, errV2ShapedTablesHoldRows)
		assert.True(t, inserted, "the row must have been written after the count pass")
		assert.Contains(t, err.Error(), "dynamic_thresholds=1, threshold_events=0, notification_histories=2",
			"the refusal must list the remaining candidates in drop order with fresh counts")
		assert.Contains(t, out, "dropped_tables=0", "no table may be dropped before the late refusal")
		assert.True(t, columnExists(t, db, "dynamic_thresholds", "label_id"), "the table that gained rows must not be dropped")
		assert.EqualValues(t, 1, countRows(t, db, "dynamic_thresholds"))
		assert.True(t, columnExists(t, db, "notification_histories", "label_id"),
			"the regenerable table must not be dropped while another table blocks")
		assert.EqualValues(t, 2, countRows(t, db, "notification_histories"))

		// The refusal dropped nothing, so the next boot sees the same state and
		// refuses the same way, leaving the populated tables and their rows alone.
		err = performAutoMigration(db, false, "SQLite", "")

		require.ErrorIs(t, err, errV2ShapedTablesHoldRows, "second boot")
		assert.Contains(t, err.Error(), "dynamic_thresholds=1", "second boot")
		assert.True(t, columnExists(t, db, "dynamic_thresholds", "label_id"), "second boot")
		assert.EqualValues(t, 1, countRows(t, db, "dynamic_thresholds"), "second boot")
		assert.True(t, columnExists(t, db, "notification_histories", "label_id"), "second boot")
		assert.EqualValues(t, 2, countRows(t, db, "notification_histories"), "second boot")
	})

	t.Run("refuses_when_the_pre_drop_check_fails", func(t *testing.T) {
		db := openSQLiteTestDB(t)
		require.NoError(t, db.Exec(testV2NotificationHistoriesDDL).Error)
		require.NoError(t, db.Exec(testV2NotificationHistoriesIndexDDL).Error)
		require.NoError(t, db.Exec(testV2DynamicThresholdsDDL).Error)

		// Fail only the emptiness probe of dynamic_thresholds. The count pass runs
		// through the Query chain and table inspection through other statements.
		errProbeFailed := errors.NewStd("probe failed")
		require.NoError(t, db.Callback().Row().Before("gorm:row").Register("test:fail_probe", func(tx *gorm.DB) {
			sql := tx.Statement.SQL.String()
			if strings.Contains(sql, "EXISTS") && strings.Contains(sql, "dynamic_thresholds") {
				_ = tx.AddError(errProbeFailed)
			}
		}))

		// Not parallel: logtest.Capture swaps the process-global logger.
		var err error
		out := logtest.Capture(t, func() {
			err = performAutoMigration(db, false, "SQLite", "")
		})

		require.ErrorIs(t, err, errV2ShapedTablesHoldRows)
		assert.Contains(t, err.Error(), "dynamic_thresholds=unknown", "a failed probe must count as non-empty")
		assert.True(t, columnExists(t, db, "dynamic_thresholds", "label_id"), "the table must not be dropped")
		assert.Contains(t, out, "Could not check v2-shaped table before dropping it", "the probe failure must be logged")
		assert.Contains(t, out, "probe failed", "the log must carry the cause")

		// Non-regenerable tables are dropped first, so this refusal came before any
		// drop and the next boot sees the same tables and refuses again.
		err = performAutoMigration(db, false, "SQLite", "")

		require.ErrorIs(t, err, errV2ShapedTablesHoldRows, "second boot")
		assert.True(t, columnExists(t, db, "dynamic_thresholds", "label_id"), "second boot")
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
		// Only the regenerable table holds rows, so the refusal can only come from
		// the detections table beside legacy data.
		require.NoError(t, db.Exec(testV2NotificationHistoriesDDL).Error)
		require.NoError(t, db.Exec(testV2NotificationHistoriesIndexDDL).Error)
		insertV2NotificationHistoryRows(t, db, 2)
		require.NoError(t, db.Exec(testV2DetectionsDDL).Error)
		require.NoError(t, db.Exec(`INSERT INTO detections (label_id) VALUES (1)`).Error)
		require.NoError(t, db.AutoMigrate(&Note{}))
		require.NoError(t, db.Create(&Note{ScientificName: "Parus major"}).Error)

		// Both sides hold data, so every boot refuses and leaves both untouched.
		for boot := 1; boot <= 2; boot++ {
			err := performAutoMigration(db, false, "SQLite", "")

			require.ErrorIs(t, err, errV2ShapedTablesHoldRows, "boot %d", boot)
			assert.True(t, columnExists(t, db, "notification_histories", "label_id"), "boot %d", boot)
			assert.EqualValues(t, 2, countRows(t, db, "notification_histories"), "boot %d", boot)
			assert.EqualValues(t, 1, countRows(t, db, "notes"), "boot %d", boot)
			assert.EqualValues(t, 1, countRows(t, db, "detections"), "boot %d", boot)
		}
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

// TestPerformAutoMigration_V2ShapedTables_RegenerableRecheck pins the check right
// before a regenerable drop. The count pass reads the tables once; the re-check
// lists and counts again. Each case breaks only that second look, and the table
// with rows must stay.
func TestPerformAutoMigration_V2ShapedTables_RegenerableRecheck(t *testing.T) {
	cases := []struct {
		name   string
		breaks func(t *testing.T, db *gorm.DB, countPassDone *bool)
		// table is the table to inspect afterwards; empty means notification_histories.
		table string
	}{
		{
			name: "a_detections_table_appears",
			breaks: func(t *testing.T, db *gorm.DB, countPassDone *bool) {
				t.Helper()
				created := false
				require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:detections_appears", func(tx *gorm.DB) {
					if created || tx.Statement.Table != "notification_histories" {
						return
					}
					created = true
					*countPassDone = true
					_ = tx.Session(&gorm.Session{NewDB: true}).Exec(testV2DetectionsDDL).Error
				}))
			},
		},
		{
			name: "the_table_is_missing_from_the_second_listing",
			breaks: func(t *testing.T, db *gorm.DB, countPassDone *bool) {
				t.Helper()
				renamed := false
				require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:table_vanishes", func(tx *gorm.DB) {
					if renamed || tx.Statement.Table != "notification_histories" {
						return
					}
					renamed = true
					*countPassDone = true
					_ = tx.Session(&gorm.Session{NewDB: true}).Exec(`ALTER TABLE notification_histories RENAME TO notification_histories_gone`).Error
				}))
			},
			// The table is renamed, so the assertions below read the renamed table.
			table: "notification_histories_gone",
		},
		{
			name: "the_second_count_fails",
			breaks: func(t *testing.T, db *gorm.DB, countPassDone *bool) {
				t.Helper()
				errCountFailed := errors.NewStd("recount failed")
				counts := 0
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:fail_recount", func(tx *gorm.DB) {
					if tx.Statement.Table != "notification_histories" {
						return
					}
					counts++
					if counts == 2 {
						*countPassDone = true
						_ = tx.AddError(errCountFailed)
					}
				}))
			},
		},
		{
			name: "the_second_table_listing_fails",
			breaks: func(t *testing.T, db *gorm.DB, countPassDone *bool) {
				t.Helper()
				errListFailed := errors.NewStd("list failed")
				require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:count_pass_done", func(tx *gorm.DB) {
					if tx.Statement.Table == "notification_histories" {
						*countPassDone = true
					}
				}))
				require.NoError(t, db.Callback().Row().Before("gorm:row").Register("test:fail_listing", func(tx *gorm.DB) {
					if *countPassDone && strings.Contains(tx.Statement.SQL.String(), "FROM sqlite_master where type=") {
						_ = tx.AddError(errListFailed)
					}
				}))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openSQLiteTestDB(t)
			require.NoError(t, db.Exec(testV2NotificationHistoriesDDL).Error)
			require.NoError(t, db.Exec(testV2NotificationHistoriesIndexDDL).Error)
			insertV2NotificationHistoryRows(t, db, 2)
			countPassDone := false
			tc.breaks(t, db, &countPassDone)

			err := performAutoMigration(db, false, "SQLite", "")

			require.ErrorIs(t, err, errV2ShapedTablesHoldRows)
			assert.True(t, countPassDone, "the count pass must have run before the break")
			table := cmp.Or(tc.table, "notification_histories")
			assert.True(t, columnExists(t, db, table, "label_id"), "the table must not be dropped")
			assert.EqualValues(t, 2, countRows(t, db, table))
		})
	}
}

// TestDisposableV2ShapedTables_AreTheRegenerablePair pins the tables legacy
// startup may drop with their rows to the pair named in internal/datastore/AGENTS.md.
// The classifier mirror in internal/datastore/v2 asserts the same literal.
func TestDisposableV2ShapedTables_AreTheRegenerablePair(t *testing.T) {
	t.Parallel()

	got := make([]string, 0, len(disposableV2ShapedTables))
	for table := range disposableV2ShapedTables {
		got = append(got, table)
		assert.Contains(t, v2ShapedLegacyTables, table, "a disposable table must be a candidate")
	}
	assert.ElementsMatch(t, []string{"notification_histories", "image_caches"}, got)
}

// TestV2ShapedTablesGuideURL_PointsAtWikiHeading keeps the link in the refusal
// message and log in step with the wiki: rewording the section heading would
// otherwise leave users on a page that does not scroll to the explanation.
func TestV2ShapedTablesGuideURL_PointsAtWikiHeading(t *testing.T) {
	t.Parallel()

	_, anchor, found := strings.Cut(v2ShapedTablesGuideURL, "#")
	require.True(t, found, "the guide URL must carry a section anchor")
	require.NotEmpty(t, anchor)

	wiki, err := os.ReadFile(filepath.Join("..", "..", "doc", "wiki", "database-doctor.md"))
	require.NoError(t, err)

	var slugs []string
	inFence := false
	for line := range strings.SplitSeq(string(wiki), "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
		}
		if inFence {
			continue
		}
		heading, isHeading := strings.CutPrefix(line, "## ")
		if !isHeading {
			continue
		}
		slugs = append(slugs, headingSlug(heading))
	}
	assert.Contains(t, slugs, anchor, "no second-level wiki heading produces the anchor of v2ShapedTablesGuideURL")
}

// headingSlug mirrors how GitHub derives a heading anchor: lower case, spaces
// to hyphens, every other character outside [a-z0-9-] removed.
func headingSlug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}
