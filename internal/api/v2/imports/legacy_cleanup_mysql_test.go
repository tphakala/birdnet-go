//go:build integration

// internal/api/v2/imports/legacy_cleanup_mysql_test.go
package importsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
	"gorm.io/gorm"
)

const (
	cleanupTestDatabase = "birdnet_legacy_cleanup"
	cleanupTestUser     = "testuser"
	cleanupTestPassword = "testpass"
	// legacyRowDate is the key of the one row each shared-name table holds.
	legacyRowDate = "2026-01-01"
)

// cleanupTestEnv addresses the one database server the subtests share.
type cleanupTestEnv struct {
	host string
	port string
}

// newCleanupTestEnv starts the database container for one test function.
func newCleanupTestEnv(t *testing.T) *cleanupTestEnv {
	t.Helper()
	containers.SkipIfContainerRuntimeUnavailable(t)

	cfg := &containers.MySQLConfig{
		Database:     cleanupTestDatabase,
		RootPassword: "test",
		Username:     cleanupTestUser,
		Password:     cleanupTestPassword,
	}
	container, err := containers.NewMySQLContainer(t.Context(), cfg)
	require.NoError(t, err, "failed to start database container")
	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is canceled
		// just before cleanups run, so reusing it would abort termination.
		if err := container.Terminate(context.Background()); err != nil { //nolint:gocritic // cleanup must outlive the test context
			t.Logf("failed to terminate database container: %v", err)
		}
	})

	host, err := container.GetHost(t.Context())
	require.NoError(t, err)
	port, err := container.GetPort(t.Context())
	require.NoError(t, err)
	return &cleanupTestEnv{host: host, port: strconv.Itoa(port)}
}

// newManager initializes a v2 MySQL manager, with the v2_ table prefix when prefixed.
func (e *cleanupTestEnv) newManager(t *testing.T, prefixed bool) *datastoreV2.MySQLManager {
	t.Helper()
	mgr, err := datastoreV2.NewMySQLManager(&datastoreV2.MySQLConfig{
		Host:        e.host,
		Port:        e.port,
		Username:    cleanupTestUser,
		Password:    cleanupTestPassword,
		Database:    cleanupTestDatabase,
		UseV2Prefix: prefixed,
		Logger:      logger.NewConsoleLogger("legacy_cleanup_mysql_test", logger.LogLevelError),
	})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { assert.NoError(t, mgr.Close()) })
	return mgr
}

// reset drops every base table so each subtest starts from an empty database.
func (e *cleanupTestEnv) reset(t *testing.T, mgr *datastoreV2.MySQLManager) {
	t.Helper()
	db := mgr.DB()
	require.NoError(t, db.Connection(func(tx *gorm.DB) error {
		if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
			return err
		}
		for _, name := range e.tables(t, db) {
			if err := tx.Exec("DROP TABLE `" + name + "`").Error; err != nil {
				return err
			}
		}
		return tx.Exec("SET FOREIGN_KEY_CHECKS = 1").Error
	}))
}

// tables lists the base tables of the test database, lower-cased.
func (e *cleanupTestEnv) tables(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var names []string
	require.NoError(t, db.Raw(`SELECT table_name AS name FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'`).Scan(&names).Error)
	for i := range names {
		names[i] = strings.ToLower(names[i])
	}
	return names
}

// createLegacyTables creates the eleven legacy tables with one row each, with
// foreign keys from results and note_reviews to notes.
func createLegacyTables(t *testing.T, db *gorm.DB, names []string) {
	t.Helper()
	// Parents first: the drop order lists children first, so create in reverse.
	for _, name := range slices.Backward(names) {
		ddl := "CREATE TABLE `" + name + "` (id INT PRIMARY KEY, note_id INT NULL"
		if name == "results" || name == "note_reviews" {
			ddl += ", FOREIGN KEY (note_id) REFERENCES `notes`(id)"
		}
		require.NoError(t, db.Exec(ddl+") ENGINE=InnoDB").Error, name)
	}
	for _, name := range slices.Backward(names) {
		require.NoError(t, db.Exec("INSERT INTO `"+name+"` (id) VALUES (1)").Error, name)
	}
}

// markMigration sets the migration state of the manager's schema.
func markMigration(t *testing.T, mgr *datastoreV2.MySQLManager, state entities.MigrationStatus, relatedErr string) {
	t.Helper()
	require.NoError(t, mgr.DB().Model(&entities.MigrationState{}).Where("id = ?", 1).
		Updates(map[string]any{"state": state, "related_data_error": relatedErr}).Error)
}

// newCleanupHandler builds a handler on the manager with v2-only mode on. The Repo is a
// test-only wrapper; production repositories do not expose a connection.
func newCleanupHandler(t *testing.T, mgr *datastoreV2.MySQLManager) *Handler {
	t.Helper()
	settings := createMySQLTestSettings()
	settings.Output.MySQL.Database = cleanupTestDatabase
	c := createLegacyTestHandler(t, echo.New(), settings)
	c.Repo = datastore.DetectionRepository(&legacyDBRepo{db: mgr.DB()})
	c.V2Manager = mgr
	enableV2OnlyMode(t)
	return c
}

func getStatus(t *testing.T, c *Handler) LegacyStatusResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx := c.Echo.NewContext(httptest.NewRequest(http.MethodGet, "/status", http.NoBody), rec)
	require.NoError(t, c.GetLegacyStatus(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	var resp LegacyStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

// postCleanup issues the cleanup POST and waits for the background drop.
func postCleanup(t *testing.T, c *Handler) (int, CleanupActionResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx := c.Echo.NewContext(httptest.NewRequest(http.MethodPost, "/cleanup", http.NoBody), rec)
	require.NoError(t, c.StartLegacyCleanup(ctx))
	c.Wait()
	var resp CleanupActionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return rec.Code, resp
}

func rowCount(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM `"+table+"`").Scan(&n).Error)
	return n
}

// TestLegacyCleanup_MySQL runs the MySQL legacy cleanup against a real server
// (MYSQL_TEST_IMAGE selects MySQL or MariaDB). Subtests share one container and
// each starts from an empty database.
func TestLegacyCleanup_MySQL(t *testing.T) {
	t.Attr("component", "legacy_cleanup")
	t.Attr("type", "integration")

	env := newCleanupTestEnv(t)
	allLegacy := slices.Concat(legacyOnlyTables, legacySharedTables)

	t.Run("bare v2 schema keeps its tables", func(t *testing.T) {
		mgr := env.newManager(t, false)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		// Completed, so only the schema rule can refuse.
		markMigration(t, mgr, entities.MigrationStatusCompleted, "")
		require.NoError(t, mgr.DB().Create(&entities.DailyEvents{Date: legacyRowDate}).Error)
		before := env.tables(t, mgr.DB())
		c := newCleanupHandler(t, mgr)

		status := getStatus(t, c)
		assert.False(t, status.Exists)
		assert.False(t, status.CanCleanup)
		assert.Equal(t, notification.MsgErrCleanupNoLegacyTables, status.ErrorKey)

		code, resp := postCleanup(t, c)
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, notification.MsgErrCleanupNoLegacyTables, resp.ErrorKey)

		remaining, err := c.cleanupMySQLLegacy(t.Context())
		require.Error(t, err, "the background drop must re-check and refuse")
		assert.Empty(t, remaining)

		assert.ElementsMatch(t, before, env.tables(t, mgr.DB()))
		assert.Equal(t, int64(1), rowCount(t, mgr.DB(), "daily_events"))
	})

	t.Run("migrated v2_ schema drops every bare legacy table", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		markMigration(t, mgr, entities.MigrationStatusCompleted, "")
		require.NoError(t, mgr.DB().Create(&entities.DailyEvents{Date: legacyRowDate}).Error)
		createLegacyTables(t, mgr.DB(), allLegacy)
		v2Tables := slicesWithPrefix(env.tables(t, mgr.DB()), "v2_")
		require.NotEmpty(t, v2Tables)
		c := newCleanupHandler(t, mgr)

		status := getStatus(t, c)
		assert.True(t, status.Exists)
		assert.True(t, status.CanCleanup)
		assert.Equal(t, int64(len(allLegacy)), status.TotalRecords)
		names := make([]string, 0, len(status.Tables))
		for _, tbl := range status.Tables {
			names = append(names, tbl.Name)
		}
		assert.Equal(t, allLegacy, names, "tables must be listed in drop order")

		code, resp := postCleanup(t, c)
		require.Equal(t, http.StatusOK, code, resp.Message)
		state, errMsg, remaining, _ := c.cleanupStatus.Get()
		assert.Equal(t, CleanupStateCompleted, state, errMsg)
		assert.Empty(t, remaining)

		after := env.tables(t, mgr.DB())
		for _, name := range allLegacy {
			assert.NotContains(t, after, name)
		}
		assert.ElementsMatch(t, v2Tables, slicesWithPrefix(after, "v2_"))
		assert.Equal(t, int64(1), rowCount(t, mgr.DB(), "v2_daily_events"))

		code, resp = postCleanup(t, c)
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, notification.MsgErrCleanupNoLegacyTables, resp.ErrorKey)
	})

	t.Run("bare v2 tables beside a v2_ schema are kept", func(t *testing.T) {
		bare := env.newManager(t, false)
		env.reset(t, bare)
		require.NoError(t, bare.Initialize())
		require.NoError(t, bare.DB().Create(&entities.DailyEvents{Date: legacyRowDate}).Error)
		// A half-deleted bare schema: detections gone, the auxiliary tables left.
		require.NoError(t, bare.DB().Connection(func(tx *gorm.DB) error {
			if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
				return err
			}
			if err := tx.Exec("DROP TABLE `detections`").Error; err != nil {
				return err
			}
			return tx.Exec("SET FOREIGN_KEY_CHECKS = 1").Error
		}))
		mgr := env.newManager(t, true)
		markMigration(t, mgr, entities.MigrationStatusCompleted, "")
		createLegacyTables(t, mgr.DB(), []string{"notes"})
		c := newCleanupHandler(t, mgr)

		status := getStatus(t, c)
		require.True(t, status.CanCleanup)
		require.Len(t, status.Tables, 1)
		assert.Equal(t, "notes", status.Tables[0].Name)

		bareBefore := withoutNames(env.tables(t, mgr.DB()), "notes")
		code, resp := postCleanup(t, c)
		require.Equal(t, http.StatusOK, code, resp.Message)

		after := env.tables(t, mgr.DB())
		assert.NotContains(t, after, "notes")
		assert.ElementsMatch(t, bareBefore, after)
		assert.Equal(t, int64(1), rowCount(t, mgr.DB(), "daily_events"))
	})

	t.Run("related data error blocks cleanup", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		markMigration(t, mgr, entities.MigrationStatusCompleted, "reviews failed")
		createLegacyTables(t, mgr.DB(), allLegacy)
		c := newCleanupHandler(t, mgr)

		status := getStatus(t, c)
		assert.False(t, status.CanCleanup)
		assert.Equal(t, notification.MsgErrCleanupSafetyCheck, status.ErrorKey)

		code, resp := postCleanup(t, c)
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, notification.MsgErrCleanupSafetyCheck, resp.ErrorKey)

		after := env.tables(t, mgr.DB())
		for _, name := range allLegacy {
			assert.Contains(t, after, name)
		}
	})
}

// slicesWithPrefix returns the names that start with prefix.
func slicesWithPrefix(names []string, prefix string) []string {
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out
}

// withoutNames returns names minus the excluded ones.
func withoutNames(names []string, exclude ...string) []string {
	var out []string
	for _, n := range names {
		if !slices.Contains(exclude, n) {
			out = append(out, n)
		}
	}
	return out
}
