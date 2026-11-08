package testutil

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/logger"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// legacyMySQLDSNParams mirrors the parameters of the legacy MySQL DSN in
// internal/datastore/mysql.go: no clientFoundRows, so the legacy side behaves
// as it does in production.
const legacyMySQLDSNParams = "charset=utf8mb4&parseTime=True&loc=Local"

// testDatabaseSuffix is the suffix a database name needs for the harness to
// drop tables in it.
const testDatabaseSuffix = "_test"

// SetupMySQLIntegrationTest creates a migration test environment on a MySQL or
// MariaDB server: legacy tables without a prefix and v2 tables with the "v2_"
// prefix in the same database, as in a production migration. The worker runs
// in batch mode, as it does for MySQL in production.
//
// It drops and recreates every legacy and v2 table in cfg.Database, so cfg must
// point at a dedicated database whose name ends in "_test"; any other name
// fails the test before a connection is opened. cfg.UseV2Prefix is forced on. Tables are
// removed again when the test ends, after the migration worker has stopped.
func SetupMySQLIntegrationTest(t *testing.T, cfg *datastoreV2.MySQLConfig) *TestContext {
	t.Helper()

	require.True(t, strings.HasSuffix(cfg.Database, testDatabaseSuffix),
		"refusing to drop tables in database %q: its name must end in %q", cfg.Database, testDatabaseSuffix)

	log := logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)
	ctx := &TestContext{
		TempDir:      t.TempDir(),
		Logger:       log,
		useBatchMode: true,
		t:            t,
	}

	// Legacy side: plain connection with the legacy DSN parameters.
	legacyDSN := fmt.Sprintf("%s:%s@tcp(%s)/%s?%s",
		cfg.Username, cfg.Password, net.JoinHostPort(cfg.Host, cfg.Port), cfg.Database, legacyMySQLDSNParams)
	legacyGorm, err := gorm.Open(gormmysql.Open(legacyDSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err, "failed to open legacy MySQL connection")
	legacySQL, err := legacyGorm.DB()
	require.NoError(t, err, "failed to get sql.DB from GORM")
	// Cleanups run last in, first out: each is registered as soon as what it
	// releases exists, so a setup that fails part way still cleans up, and the
	// worker stop registered last runs first.
	t.Cleanup(func() { _ = legacySQL.Close() })

	models := legacyModels()
	t.Cleanup(func() { _ = legacyGorm.Migrator().DropTable(models...) })
	require.NoError(t, legacyGorm.Migrator().DropTable(models...), "failed to drop legacy tables")
	require.NoError(t, legacyGorm.AutoMigrate(models...), "failed to migrate legacy schema")

	ctx.LegacyDB = legacySQL
	ctx.LegacyGorm = legacyGorm
	ctx.Seeder = NewLegacySeeder(legacySQL)
	ctx.legacyDetectionRepo = newTestDetectionRepo(legacyGorm)
	ctx.legacyInterface = newTestLegacyInterface(legacyGorm)

	// V2 side: the production manager, created after the legacy tables as in production.
	v2Cfg := *cfg
	v2Cfg.UseV2Prefix = true
	v2Cfg.Logger = log
	mgr, err := datastoreV2.NewMySQLManager(&v2Cfg)
	require.NoError(t, err, "failed to create v2 MySQL manager")
	t.Cleanup(func() { _ = mgr.Close() })
	t.Cleanup(func() { _ = mgr.Delete() })
	require.NoError(t, mgr.Delete(), "failed to drop stale v2 tables")
	require.NoError(t, mgr.Initialize(), "failed to initialize v2 schema")
	ctx.V2Manager = mgr
	ctx.StateManager = datastoreV2.NewStateManager(mgr.DB())
	ctx.createRepositories(mgr.DB(), true, true)
	ctx.setupLookupTables(t)

	ctx.createAuxiliaryMigrator(t)
	ctx.createWorker(t)

	// Registered last, so it runs first: stop the worker before the tables are
	// dropped and the connections close.
	t.Cleanup(func() {
		ctx.stopWorker()
	})

	return ctx
}
