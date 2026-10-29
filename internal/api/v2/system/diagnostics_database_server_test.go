package system

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/health"
)

// dialectManagerStub reports a dialect and a handle; nothing else is called.
type dialectManagerStub struct {
	datastoreV2.Manager
	mysql bool
	db    *gorm.DB
}

func (s dialectManagerStub) IsMySQL() bool { return s.mysql }
func (s dialectManagerStub) DB() *gorm.DB  { return s.db }

func TestMySQLServerDB_OnlyForMySQL(t *testing.T) {
	handle := &gorm.DB{}
	tests := []struct {
		name    string
		manager datastoreV2.Manager
		want    *gorm.DB
	}{
		{"no v2 manager", nil, nil},
		{"sqlite manager", dialectManagerStub{mysql: false, db: handle}, nil},
		{"mysql manager", dialectManagerStub{mysql: true, db: handle}, handle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{Core: apitest.NewCore(t)}
			h.V2Manager = tt.manager
			assert.Same(t, tt.want, h.mysqlServerDB())
		})
	}
}

func TestRegisterHealthChecks_DatabaseServerChecksSkippedWithoutMySQL(t *testing.T) {
	h := &Handler{Core: apitest.NewCore(t)}
	h.V2Manager = nil
	h.DS = nil // the other database checks then report unknown instead of calling the mock
	h.healthRegistry = health.NewRegistry()
	h.registerHealthChecks()

	statuses := map[string]health.Status{}
	for _, r := range h.healthRegistry.RunCategory(t.Context(), health.CategoryDatabase) {
		statuses[r.Name] = r.Status
	}
	require.Contains(t, statuses, "database_server_version")
	require.Contains(t, statuses, "database_charset")
	assert.Equal(t, health.StatusSkipped, statuses["database_server_version"])
	assert.Equal(t, health.StatusSkipped, statuses["database_charset"])
}

const (
	// schemaSizeTestDriver is a SQLite driver whose connections emulate the parts
	// of MySQL that the schema size query uses: an attached information_schema
	// database holding one 3 MiB table of the current schema, and DATABASE().
	schemaSizeTestDriver = "sqlite3_system_schema_size_test"
	schemaSizeTestSchema = "birdnet"
	schemaDataBytes      = 2 << 20
	schemaIndexBytes     = 1 << 20
)

func init() {
	sql.Register(schemaSizeTestDriver, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			if err := conn.RegisterFunc("DATABASE", func() string { return schemaSizeTestSchema }, true); err != nil {
				return err
			}
			_, err := conn.Exec("ATTACH ':memory:' AS information_schema; "+
				"CREATE TABLE information_schema.TABLES (TABLE_SCHEMA TEXT, DATA_LENGTH INTEGER, INDEX_LENGTH INTEGER); "+
				"INSERT INTO information_schema.TABLES VALUES ('birdnet', 2097152, 1048576)", nil)
			return err
		},
	})
}

// TestRegisterHealthChecks_DatabaseSizeFollowsDialect pins that the open v2
// manager, not the presence of a SQLite file, decides what the size check
// measures.
func TestRegisterHealthChecks_DatabaseSizeFollowsDialect(t *testing.T) {
	content := []byte("leftover sqlite file")
	staleFile := filepath.Join(t.TempDir(), "birdnet.db")
	require.NoError(t, os.WriteFile(staleFile, content, 0o600))

	// A SQLite handle has no information_schema, so the server query fails and
	// proves the MySQL branch ran without needing a MySQL server.
	sqliteHandle, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, dbErr := sqliteHandle.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	// A handle whose server query succeeds, so the probe's success return is exercised.
	serverHandle, err := gorm.Open(sqlite.Dialector{DriverName: schemaSizeTestDriver, DSN: ":memory:"}, &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	serverSQL, err := serverHandle.DB()
	require.NoError(t, err)
	serverSQL.SetMaxOpenConns(1) // every connection would get its own in-memory database
	t.Cleanup(func() { _ = serverSQL.Close() })

	tests := []struct {
		name         string
		manager      datastoreV2.Manager
		mysqlEnabled bool
		wantStatus   health.Status
		wantMessage  string
		wantPath     bool
		wantSize     int64 // expected size_bytes when there is no path
	}{
		{"mysql manager ignores a leftover sqlite file", dialectManagerStub{mysql: true, db: sqliteHandle}, true, health.StatusUnknown, "Unable to read the database size", false, 0},
		{"mysql manager reports the schema size", dialectManagerStub{mysql: true, db: serverHandle}, true, health.StatusHealthy, "Database size OK (3.0 MB)", false, schemaDataBytes + schemaIndexBytes},
		{"sqlite manager stats the configured file", dialectManagerStub{mysql: false}, false, health.StatusHealthy, "Database size OK", true, 0},
		{"sqlite manager with stale mysql setting stats the configured file", dialectManagerStub{mysql: false}, true, health.StatusHealthy, "Database size OK", true, 0},
		{"no manager with mysql configured", nil, true, health.StatusUnknown, "no MySQL connection is available", false, 0},
		{"no manager with sqlite configured", nil, false, health.StatusHealthy, "Database size OK", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{Core: apitest.NewCore(t, apitest.WithSettingsFunc(func(s *conf.Settings) {
				s.Output.SQLite.Path = staleFile
				s.Output.MySQL.Enabled = tt.mysqlEnabled
			}))}
			h.DS = nil
			h.V2Manager = tt.manager
			h.healthRegistry = health.NewRegistry()
			h.registerHealthChecks()

			var got *health.Result
			for _, r := range h.healthRegistry.RunCategory(t.Context(), health.CategoryDatabase) {
				if r.Name == "database_size" {
					got = &r
				}
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantStatus, got.Status)
			assert.Contains(t, got.Message, tt.wantMessage)
			if tt.wantPath {
				assert.Equal(t, staleFile, got.Details["path"])
				assert.Equal(t, int64(len(content)), got.Details["size_bytes"])
			} else {
				assert.NotContains(t, got.Details, "path")
				if tt.wantSize != 0 {
					assert.Equal(t, tt.wantSize, got.Details["size_bytes"])
				}
			}
		})
	}
}
