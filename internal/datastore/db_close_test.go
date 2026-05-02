package datastore

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestSQLiteStore_Close_NilsOutDB(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	require.NotNil(t, store.DB, "DB should be non-nil before Close")

	err := store.Close()
	require.NoError(t, err)

	assert.Nil(t, store.DB, "DB should be nil after Close to prevent stale reference queries")
}

func TestSQLiteStore_ClosedDB_QueryReturnsClosedError(t *testing.T) {
	t.Parallel()

	db := openUnmanagedTestDB(t)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	var count int64
	queryErr := db.Model(&DailyEvents{}).Count(&count).Error
	require.Error(t, queryErr)
	assert.Contains(t, queryErr.Error(), "database is closed")

	assert.True(t, IsTransientDBError(queryErr),
		"'database is closed' from a real closed pool should be classified as transient")
}

func TestRetryOnLock_RetriesAfterDatabaseClosed(t *testing.T) {
	t.Parallel()

	calls := 0
	err := RetryOnLock(t.Context(), "test_closed_retry", func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("sql: database is closed")
		}
		return nil
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, 3, calls, "should have retried until success")
}

// openUnmanagedTestDB creates a file-backed SQLite database without
// registering a cleanup closer. Use this when the test needs to
// manually control the DB lifecycle (e.g., closing the pool explicitly).
func openUnmanagedTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := fmt.Sprintf("%s/lifecycle_test.db", t.TempDir())
	dsn := buildSQLiteDSN(dbPath, "_journal_mode=WAL&_busy_timeout=5000")

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&DailyEvents{}))

	return db
}

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()

	db := openUnmanagedTestDB(t)

	settings := &conf.Settings{}
	settings.Output.SQLite.Path = "test.db"

	return &SQLiteStore{
		Settings:  settings,
		DataStore: DataStore{DB: db},
	}
}
