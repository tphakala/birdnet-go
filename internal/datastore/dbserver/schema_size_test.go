package dbserver

import (
	"database/sql"
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	// schemaSizeTestDriver is a SQLite driver whose connections emulate the
	// parts of MySQL that the schema size query uses: an attached
	// information_schema database and a DATABASE() function.
	schemaSizeTestDriver = "sqlite3_schema_size_test"
	schemaSizeTestSchema = "birdnet"
)

func init() {
	sql.Register(schemaSizeTestDriver, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			if err := conn.RegisterFunc("DATABASE", func() string { return schemaSizeTestSchema }, true); err != nil {
				return err
			}
			_, err := conn.Exec("ATTACH ':memory:' AS information_schema; "+
				"CREATE TABLE information_schema.TABLES (TABLE_SCHEMA TEXT, DATA_LENGTH INTEGER, INDEX_LENGTH INTEGER)", nil)
			return err
		},
	})
}

func openSchemaSizeDB(t *testing.T, driver string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Dialector{DriverName: driver, DSN: ":memory:"}, &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1) // every connection would get its own in-memory database
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestQuerySchemaSize(t *testing.T) {
	db := openSchemaSizeDB(t, schemaSizeTestDriver)

	// No tables sum to NULL, which must read as zero.
	size, err := QuerySchemaSize(t.Context(), db)
	require.NoError(t, err)
	assert.Zero(t, size)

	require.NoError(t, db.Exec("INSERT INTO information_schema.TABLES VALUES "+
		"('birdnet', 100, 20), ('birdnet', 300, 40), ('other', 1000, 1000), ('birdnet', NULL, NULL)").Error)
	size, err = QuerySchemaSize(t.Context(), db)
	require.NoError(t, err)
	assert.Equal(t, int64(460), size, "data plus index of the current schema only")
}

func TestQuerySchemaSize_QueryError(t *testing.T) {
	// A plain SQLite handle has no information_schema.
	db := openSchemaSizeDB(t, "sqlite3")
	size, err := QuerySchemaSize(t.Context(), db)
	require.Error(t, err)
	assert.Zero(t, size)
}
