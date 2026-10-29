//go:build integration

package v2

import (
	"context"
	"database/sql/driver"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const (
	// charsetTestOldText is representable in latin1, so the table an older
	// release created holds it.
	charsetTestOldText = "café"
	// charsetTestUnicodeText needs utf8mb4: Cyrillic and a 4-byte emoji.
	charsetTestUnicodeText = "Денис Воронов 🐦"
	charsetTestLatin1DDL   = "CHARACTER SET latin1 COLLATE latin1_swedish_ci"
	charsetTestCustomDDL   = "CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
	charsetTestCustomColl  = "utf8mb4_unicode_ci"

	// charsetTestLockWaitTimeoutNumber is the MySQL error number of a lock wait
	// timeout, for a table lock and for a metadata lock.
	charsetTestLockWaitTimeoutNumber = 1205
	// charsetTestOtherSessionWaitSeconds bounds how long the test's own second
	// session waits for a table that the manager holds locked.
	charsetTestOtherSessionWaitSeconds = 1
	// charsetTestKillWait and charsetTestKillPoll bound the wait for a killed
	// session to leave the process list.
	charsetTestKillWait = 10 * time.Second
	charsetTestKillPoll = 50 * time.Millisecond
)

// tableCollations maps each base table of the test schema to its collation.
func tableCollations(t *testing.T) map[string]string {
	t.Helper()
	var rows []struct {
		Name      string `gorm:"column:table_name"`
		Collation string `gorm:"column:table_collation"`
	}
	require.NoError(t, coexTestDB.Raw(
		"SELECT TABLE_NAME AS table_name, TABLE_COLLATION AS table_collation "+
			"FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'",
		coexTestDBName).Scan(&rows).Error)
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Name] = r.Collation
	}
	return out
}

// roundTripUnicode writes 4-byte text through two tables the manager created
// and reads it back.
func roundTripUnicode(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now()
	threshold := entities.DynamicThreshold{
		SpeciesName: charsetTestUnicodeText,
		ExpiresAt:   now, LastTriggered: now, FirstCreated: now,
	}
	require.NoError(t, db.Create(&threshold).Error, "unicode species name must be stored")
	var gotThreshold entities.DynamicThreshold
	require.NoError(t, db.First(&gotThreshold, threshold.ID).Error)
	assert.Equal(t, charsetTestUnicodeText, gotThreshold.SpeciesName)

	var model entities.AIModel
	require.NoError(t, db.First(&model).Error)
	var labelType entities.LabelType
	require.NoError(t, db.First(&labelType).Error)
	label := entities.Label{ScientificName: "Turdus merula", ModelID: model.ID, LabelTypeID: labelType.ID}
	require.NoError(t, db.Create(&label).Error)
	cache := entities.ImageCache{LabelID: label.ID, AuthorName: charsetTestUnicodeText, CachedAt: now}
	require.NoError(t, db.Create(&cache).Error, "unicode author name must be stored")
	var gotCache entities.ImageCache
	require.NoError(t, db.First(&gotCache, cache.ID).Error)
	assert.Equal(t, charsetTestUnicodeText, gotCache.AuthorName)
}

func TestMySQL_V2Initialize_SchemaCharset_NewTablesHoldUnicodeAndPopulatedTablesUnchanged(t *testing.T) {
	// Not parallel: the subtests share one database and change its default.
	tests := []struct {
		name        string
		schemaDDL   string
		usePrefix   bool
		wantUnicode bool // new tables are forced to utf8mb4 and store 4-byte text
	}{
		{"latin1 schema fresh install", charsetTestLatin1DDL, false, true},
		{"latin1 schema migration mode", charsetTestLatin1DDL, true, true},
		{"utf8mb4 schema with custom collation", charsetTestCustomDDL, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)
			useSchemaDefault(t, tt.schemaDDL)

			prefix := ""
			if tt.usePrefix {
				prefix = v2TablePrefix
			}
			// A table the way an older release created it: no table options.
			oldTable := prefix + "app_metadata"
			require.NoError(t, coexTestDB.Table(oldTable).AutoMigrate(&entities.AppMetadata{}))
			require.NoError(t, coexTestDB.Table(oldTable).Create(
				&entities.AppMetadata{Key: "k", Value: charsetTestOldText}).Error)

			mgr := newTestMySQLManager(t, tt.usePrefix)
			require.NoError(t, mgr.Initialize())
			t.Cleanup(func() { _ = mgr.Close() })

			report, err := dbserver.QueryCharset(t.Context(), coexTestDB)
			require.NoError(t, err)
			collations := tableCollations(t)
			require.Contains(t, collations, oldTable)

			if tt.wantUnicode {
				assert.Equal(t, []string{oldTable}, report.NonUTF8MB4Tables(),
					"only the pre-existing table may stay outside utf8mb4")
				assert.Equal(t, "latin1_swedish_ci", collations[oldTable])
				roundTripUnicode(t, mgr.DB())
			} else {
				assert.Empty(t, report.NonUTF8MB4Tables())
				for name, collation := range collations {
					assert.Equal(t, charsetTestCustomColl, collation, "table %s must inherit the schema collation", name)
				}
			}

			// Boot again: nothing changes, including the pre-existing table and row.
			mgr2 := newTestMySQLManager(t, tt.usePrefix)
			require.NoError(t, mgr2.Initialize())
			t.Cleanup(func() { _ = mgr2.Close() })
			assert.Equal(t, collations, tableCollations(t))
			var row entities.AppMetadata
			require.NoError(t, coexTestDB.Table(oldTable).First(&row, "`key` = ?", "k").Error)
			assert.Equal(t, charsetTestOldText, row.Value)
		})
	}
}

func TestMySQL_V2MigrationDB_UnreadableSchemaCharsetFails(t *testing.T) {
	// Not parallel: shares the test database with the other integration tests.
	mgr := newTestMySQLManager(t, false)
	require.NoError(t, mgr.Close())

	db, _, err := mgr.migrationDB()

	require.Error(t, err, "an unreadable schema charset must not fall back to a handle without table options")
	assert.Nil(t, db)
}

// createOldReleaseSchema leaves the test database the way a release from before
// the utf8mb4 table options left it: every v2 table created without options on
// the schema default, the seeded reference tables and the migration state row
// populated, app_metadata holding a latin1 value, and two foreign tables (the
// legacy notes table and the other mode's image_caches) that are empty. The
// caller sets the schema default first.
func createOldReleaseSchema(t *testing.T, usePrefix bool) {
	t.Helper()
	dropAllTables(t, coexTestDB, coexTestDBName)

	old := newTestMySQLManager(t, usePrefix)
	t.Cleanup(func() { _ = old.Close() })
	require.NoError(t, old.DB().AutoMigrate(v2Entities()...))
	require.NoError(t, seedLookupTablesDB(old.DB()))
	require.NoError(t, seedDefaultModelDB(old.DB()))
	require.NoError(t, old.DB().FirstOrCreate(&entities.MigrationState{ID: 1}, entities.MigrationState{ID: 1}).Error)
	require.NoError(t, old.DB().Create(&entities.AppMetadata{Key: "k", Value: charsetTestOldText}).Error)

	foreignShared := "v2_image_caches"
	if usePrefix {
		foreignShared = "image_caches"
	}
	for _, name := range []string{"notes", foreignShared} {
		require.NoError(t, coexTestDB.Exec(fmt.Sprintf("CREATE TABLE `%s` (id INT PRIMARY KEY, body TEXT)", name)).Error)
	}
}

// useSchemaDefault sets the default character set of the test database and
// restores the original one when the test ends.
func useSchemaDefault(t *testing.T, schemaDDL string) {
	t.Helper()
	origCharset, origCollation, err := dbserver.QuerySchemaCharset(t.Context(), coexTestDB)
	require.NoError(t, err)
	require.NoError(t, coexTestDB.Exec(fmt.Sprintf("ALTER DATABASE `%s` %s", coexTestDBName, schemaDDL)).Error)
	t.Cleanup(func() {
		restore := fmt.Sprintf("ALTER DATABASE `%s` CHARACTER SET %s COLLATE %s", coexTestDBName, origCharset, origCollation)
		assert.NoError(t, coexTestDB.Exec(restore).Error)
	})
}

// textColumnSnapshot lists (table, column, type, character set) of every text
// column of the test schema, in a stable order.
func textColumnSnapshot(t *testing.T) []string {
	t.Helper()
	var rows []struct {
		Table   string `gorm:"column:table_name"`
		Column  string `gorm:"column:column_name"`
		Type    string `gorm:"column:column_type"`
		Charset string `gorm:"column:character_set_name"`
	}
	require.NoError(t, coexTestDB.Raw(
		"SELECT TABLE_NAME AS table_name, COLUMN_NAME AS column_name, COLUMN_TYPE AS column_type, "+
			"CHARACTER_SET_NAME AS character_set_name FROM information_schema.COLUMNS "+
			"WHERE TABLE_SCHEMA = ? AND CHARACTER_SET_NAME IS NOT NULL ORDER BY TABLE_NAME, COLUMN_NAME",
		coexTestDBName).Scan(&rows).Error)
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("%s.%s %s %s", r.Table, r.Column, r.Type, r.Charset)
	}
	return out
}

func columnType(t *testing.T, table, column string) string {
	t.Helper()
	var got string
	require.NoError(t, coexTestDB.Raw(
		"SELECT COLUMN_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = ?",
		coexTestDBName, table, column).Scan(&got).Error)
	return got
}

func TestMySQL_V2Initialize_ConvertsVerifiedEmptyTablesToUTF8MB4(t *testing.T) {
	// Not parallel: the subtests share one database and change its default.
	tests := []struct {
		name        string
		schemaDDL   string
		usePrefix   bool
		wantConvert bool
	}{
		{"latin1 schema bare", charsetTestLatin1DDL, false, true},
		{"latin1 schema v2 prefix", charsetTestLatin1DDL, true, true},
		{"utf8mb4 schema left alone", charsetTestCustomDDL, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useSchemaDefault(t, tt.schemaDDL)
			createOldReleaseSchema(t, tt.usePrefix)
			prefix := ""
			if tt.usePrefix {
				prefix = v2TablePrefix
			}
			// The collation reference: a table created new by the boot.
			require.NoError(t, coexTestDB.Exec("DROP TABLE `"+prefix+"app_events`").Error)
			if !tt.wantConvert {
				// An empty latin1 v2 table on a utf8mb4 schema.
				require.NoError(t, coexTestDB.Exec(
					"ALTER TABLE `"+prefix+"image_caches` CONVERT TO CHARACTER SET latin1").Error)
			}

			before, err := dbserver.QueryCharset(t.Context(), coexTestDB)
			require.NoError(t, err)
			candidates, err := convertibleTables(schema.NamingStrategy{TablePrefix: prefix}, before)
			require.NoError(t, err)
			emptyBefore := map[string]bool{}
			rowsBefore := map[string]int64{}
			for _, name := range candidates {
				has, err := tableHasAnyRow(coexTestDB, name)
				require.NoError(t, err)
				emptyBefore[name] = !has
				rowsBefore[name] = getRowCount(t, coexTestDB, name)
			}
			require.NotEmpty(t, candidates)

			mgr := newTestMySQLManager(t, tt.usePrefix)
			require.NoError(t, mgr.Initialize())
			t.Cleanup(func() { _ = mgr.Close() })

			after, err := dbserver.QueryCharset(t.Context(), coexTestDB)
			require.NoError(t, err)
			nonUTF8 := after.NonUTF8MB4Tables()
			collations := tableCollations(t)

			if !tt.wantConvert {
				assert.Equal(t, []string{prefix + "image_caches"}, nonUTF8,
					"a utf8mb4 schema must not be touched")
				return
			}

			// Foreign tables keep the schema default.
			assert.Contains(t, nonUTF8, "notes")
			foreignShared := "v2_image_caches"
			if tt.usePrefix {
				foreignShared = "image_caches"
			}
			assert.Contains(t, nonUTF8, foreignShared)

			reference := collations[prefix+"app_events"]
			require.NotEmpty(t, reference)
			require.NotEmpty(t, emptyBefore)
			for name, wasEmpty := range emptyBefore {
				if wasEmpty {
					assert.NotContains(t, nonUTF8, name, "empty table %s must be converted", name)
					assert.Equal(t, reference, collations[name], "table %s must match tables created new", name)
					continue
				}
				assert.Contains(t, nonUTF8, name, "populated table %s must not be altered", name)
				assert.Equal(t, rowsBefore[name], getRowCount(t, coexTestDB, name))
			}
			var row entities.AppMetadata
			require.NoError(t, coexTestDB.Table(prefix+"app_metadata").First(&row, "`key` = ?", "k").Error)
			assert.Equal(t, charsetTestOldText, row.Value)

			// CONVERT widens TEXT to MEDIUMTEXT; running before AutoMigrate lets the
			// same boot restore the declared type.
			assert.Equal(t, "text", columnType(t, prefix+"detection_comments", "entry"))
			roundTripUnicode(t, mgr.DB())

			// A second boot changes nothing.
			snapshot := textColumnSnapshot(t)
			mgr2 := newTestMySQLManager(t, tt.usePrefix)
			require.NoError(t, mgr2.Initialize())
			t.Cleanup(func() { _ = mgr2.Close() })
			assert.Equal(t, collations, tableCollations(t))
			assert.Equal(t, snapshot, textColumnSnapshot(t))
		})
	}
}

func TestMySQL_V2Initialize_ConvertEmptyTables_WriterBlockedUntilConverted(t *testing.T) {
	useSchemaDefault(t, charsetTestLatin1DDL)
	createOldReleaseSchema(t, false)

	mgr := newTestMySQLManager(t, false)
	t.Cleanup(func() { _ = mgr.Close() })
	seamRan := false
	mgr.beforeConvertAlter = func(table string) {
		if table != "app_events" {
			return
		}
		seamRan = true
		// A second session writing between the emptiness check and the ALTER must wait
		// for the table lock and give up.
		err := coexTestDB.Connection(func(tx *gorm.DB) error {
			require.NoError(t, tx.Exec(fmt.Sprintf(
				"SET SESSION lock_wait_timeout = %d", charsetTestOtherSessionWaitSeconds)).Error)
			return tx.Create(&entities.AppEvent{
				Timestamp: time.Now(), Category: "test", EventType: "writer",
			}).Error
		})
		if mysqlErr, ok := errors.AsType[*mysql.MySQLError](err); assert.True(t, ok, "the writer must fail with a MySQL error, got %v", err) {
			assert.EqualValues(t, charsetTestLockWaitTimeoutNumber, mysqlErr.Number)
		}
	}

	require.NoError(t, mgr.Initialize())
	require.True(t, seamRan, "the conversion must have reached app_events")

	report, err := dbserver.QueryCharset(t.Context(), coexTestDB)
	require.NoError(t, err)
	assert.NotContains(t, report.NonUTF8MB4Tables(), "app_events")
	assert.Zero(t, getRowCount(t, coexTestDB, "app_events"))
	require.NoError(t, mgr.DB().Create(&entities.AppEvent{
		Timestamp: time.Now(), Category: "test", EventType: "after", Message: charsetTestUnicodeText,
	}).Error)
	var got entities.AppEvent
	require.NoError(t, mgr.DB().First(&got).Error)
	assert.Equal(t, charsetTestUnicodeText, got.Message)
}

func TestMySQL_V2Initialize_ConvertEmptyTables_LockTimeoutSkipsTableAndBootContinues(t *testing.T) {
	useSchemaDefault(t, charsetTestLatin1DDL)
	createOldReleaseSchema(t, false)

	// READ, not WRITE: AutoMigrate must still be able to read the table.
	sqlDB, err := coexTestDB.DB()
	require.NoError(t, err)
	holder, err := sqlDB.Conn(t.Context())
	require.NoError(t, err)
	_, err = holder.ExecContext(t.Context(), "LOCK TABLES `app_events` READ")
	require.NoError(t, err)
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		// A live context, because t.Context() is already cancelled when Cleanup runs.
		if _, unlockErr := holder.ExecContext(context.Background(), "UNLOCK TABLES"); unlockErr != nil {
			assert.NoError(t, unlockErr)
			// End the session so the server releases the lock instead of the pool keeping it.
			_ = holder.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = holder.Close()
	}
	t.Cleanup(release)

	mgr := newTestMySQLManager(t, false)
	t.Cleanup(func() { _ = mgr.Close() })
	require.NoError(t, mgr.Initialize(), "a lock timeout must not fail the boot")

	report, err := dbserver.QueryCharset(t.Context(), coexTestDB)
	require.NoError(t, err)
	assert.Contains(t, report.NonUTF8MB4Tables(), "app_events", "the locked table keeps its character set")
	assert.NotContains(t, report.NonUTF8MB4Tables(), "dynamic_thresholds", "the next table is still handled")

	release()
	mgr2 := newTestMySQLManager(t, false)
	t.Cleanup(func() { _ = mgr2.Close() })
	require.NoError(t, mgr2.Initialize())
	report, err = dbserver.QueryCharset(t.Context(), coexTestDB)
	require.NoError(t, err)
	assert.NotContains(t, report.NonUTF8MB4Tables(), "app_events", "the next boot converts it")
}

// logEntry is one line captured by recordingLogger.
type logEntry struct {
	level  logger.LogLevel
	msg    string
	fields map[string]any
}

// recordingLogger is a logger.Logger that keeps every line, for tests that pin
// the level and fields of the conversion log lines.
type recordingLogger struct {
	mu      sync.Mutex
	entries []logEntry
}

var _ logger.Logger = (*recordingLogger)(nil)

func (l *recordingLogger) record(level logger.LogLevel, msg string, fields []logger.Field) {
	values := make(map[string]any, len(fields))
	for _, f := range fields {
		values[f.Key] = f.Value
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, logEntry{level: level, msg: msg, fields: values})
}

// lines returns the captured lines of one level for one table.
func (l *recordingLogger) lines(level logger.LogLevel, table string) []logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []logEntry
	for _, e := range l.entries {
		if e.level == level && e.fields["table"] == table {
			out = append(out, e)
		}
	}
	return out
}

func (l *recordingLogger) Module(string) logger.Logger { return l }
func (l *recordingLogger) Trace(msg string, f ...logger.Field) {
	l.record(logger.LogLevelTrace, msg, f)
}
func (l *recordingLogger) Debug(msg string, f ...logger.Field) {
	l.record(logger.LogLevelDebug, msg, f)
}
func (l *recordingLogger) Info(msg string, f ...logger.Field) { l.record(logger.LogLevelInfo, msg, f) }
func (l *recordingLogger) Warn(msg string, f ...logger.Field) { l.record(logger.LogLevelWarn, msg, f) }
func (l *recordingLogger) Error(msg string, f ...logger.Field) {
	l.record(logger.LogLevelError, msg, f)
}
func (l *recordingLogger) With(...logger.Field) logger.Logger        { return l }
func (l *recordingLogger) WithContext(context.Context) logger.Logger { return l }
func (l *recordingLogger) Flush() error                              { return nil }
func (l *recordingLogger) Log(level logger.LogLevel, msg string, f ...logger.Field) {
	l.record(level, msg, f)
}

func TestMySQL_V2Initialize_ConvertEmptyTables_CleanupFailureAfterAlterStillLogsConversion(t *testing.T) {
	useSchemaDefault(t, charsetTestLatin1DDL)
	createOldReleaseSchema(t, false)

	rec := &recordingLogger{}
	mgr := newTestMySQLManager(t, false)
	mgr.log = rec
	t.Cleanup(func() { _ = mgr.Close() })
	// End the pinned session right after each ALTER, so the UNLOCK TABLES that
	// follows fails.
	mgr.afterConvertAlter = func(tx *gorm.DB) {
		var id uint64
		require.NoError(t, tx.Raw("SELECT CONNECTION_ID()").Scan(&id).Error)
		require.NoError(t, coexTestDB.Exec(fmt.Sprintf("KILL %d", id)).Error)
		require.Eventually(t, func() bool {
			var n int64
			if queryErr := coexTestDB.Raw(
				"SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE ID = ?", id).Scan(&n).Error; queryErr != nil {
				return false
			}
			return n == 0
		}, charsetTestKillWait, charsetTestKillPoll)
	}

	require.NoError(t, mgr.Initialize())

	report, err := dbserver.QueryCharset(t.Context(), coexTestDB)
	require.NoError(t, err)
	assert.NotContains(t, report.NonUTF8MB4Tables(), "app_events", "the ALTER committed before the session ended")
	assert.NotEmpty(t, rec.lines(logger.LogLevelInfo, "app_events"), "a converted table is logged as converted")
}
