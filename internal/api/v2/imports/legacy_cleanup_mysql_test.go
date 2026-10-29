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
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/datastore/v2only"
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
	// legacyFixtureLastMigratedID is the migration watermark the fixtures record. It
	// covers the id 1 rows createLegacyTables writes; a note with a higher id is a
	// tail record the proof must find in v2.
	legacyFixtureLastMigratedID = 1
	// legacyStatusPath and legacyCleanupPath are the routes of the cleanup endpoints.
	legacyStatusPath  = "/api/v2/system/database/legacy/status"
	legacyCleanupPath = "/api/v2/system/database/legacy/cleanup"
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
	// Runs before Close: the cleanup keeps the shared auxiliary tables, and a bare
	// manager initialized on their legacy shape in the next subtest would fail.
	t.Cleanup(func() { e.reset(t, mgr) })
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

// markMigration sets the migration state of the manager's schema, including the
// auxiliary data outcome. StartedAt and AuxiliaryDataStartedAt get one whole-second
// literal in the same UPDATE, so the outcome belongs to the recorded attempt and
// survives the MySQL round trip.
func markMigration(t *testing.T, mgr *datastoreV2.MySQLManager, state entities.MigrationStatus, relatedErr string, aux entities.AuxiliaryDataStatus) {
	t.Helper()
	require.NoError(t, mgr.DB().Model(&entities.MigrationState{}).Where("id = ?", 1).
		Updates(map[string]any{
			"state":                     state,
			"related_data_error":        relatedErr,
			"auxiliary_data_status":     aux,
			"started_at":                fixtureAttemptStart,
			"auxiliary_data_started_at": fixtureAttemptStart,
			"last_migrated_id":          legacyFixtureLastMigratedID,
		}).Error)
}

// newCleanupHandler builds a handler on the manager with v2-only mode on. The
// cleanup takes its connection from the manager alone.
func newCleanupHandler(t *testing.T, mgr *datastoreV2.MySQLManager) *Handler {
	t.Helper()
	settings := createMySQLTestSettings()
	settings.Output.MySQL.Database = cleanupTestDatabase
	c := createLegacyTestHandler(t, echo.New(), settings)
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
	// The eleven legacy tables in drop order, written out so a name dropped from
	// the production lists fails the test.
	allLegacy := []string{
		"note_reviews", "note_comments", "note_locks", "results", "notes",
		"hourly_weathers", "daily_events", "image_caches", "dynamic_thresholds",
		"threshold_events", "notification_histories",
	}
	legacyOnly := allLegacy[:5]
	legacyShared := allLegacy[5:]

	t.Run("bare v2 schema keeps its tables", func(t *testing.T) {
		mgr := env.newManager(t, false)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		// Completed, so only the schema rule can refuse.
		markMigration(t, mgr, entities.MigrationStatusCompleted, "", entities.AuxiliaryDataStatusSucceeded)
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

		remaining, err := c.cleanupMySQLLegacy(t.Context(), mgr)
		require.Error(t, err, "the background drop must re-check and refuse")
		assert.Empty(t, remaining)

		assert.ElementsMatch(t, before, env.tables(t, mgr.DB()))
		assert.Equal(t, int64(1), rowCount(t, mgr.DB(), "daily_events"))
	})

	t.Run("migrated v2_ schema drops the legacy-only tables and keeps the shared auxiliary tables", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		markMigration(t, mgr, entities.MigrationStatusCompleted, "", entities.AuxiliaryDataStatusSucceeded)
		require.NoError(t, mgr.DB().Create(&entities.DailyEvents{Date: legacyRowDate}).Error)
		createLegacyTables(t, mgr.DB(), allLegacy)
		v2Tables := slicesWithPrefix(env.tables(t, mgr.DB()), "v2_")
		require.NotEmpty(t, v2Tables)
		c := newCleanupHandler(t, mgr)

		status := getStatus(t, c)
		assert.True(t, status.Exists)
		assert.True(t, status.CanCleanup)
		assert.Equal(t, int64(len(legacyOnly)), status.TotalRecords)
		names := make([]string, 0, len(status.Tables))
		for _, tbl := range status.Tables {
			names = append(names, tbl.Name)
		}
		assert.Equal(t, legacyOnly, names, "tables must be listed in drop order")

		code, resp := postCleanup(t, c)
		require.Equal(t, http.StatusOK, code, resp.Message)
		state, errMsg, remaining, _ := c.cleanupStatus.Get()
		assert.Equal(t, CleanupStateCompleted, state, errMsg)
		assert.Empty(t, remaining)

		after := env.tables(t, mgr.DB())
		for _, name := range legacyOnly {
			assert.NotContains(t, after, name)
		}
		for _, name := range legacyShared {
			assert.Contains(t, after, name)
			assert.Equal(t, int64(1), rowCount(t, mgr.DB(), name), name)
		}
		assert.ElementsMatch(t, v2Tables, slicesWithPrefix(after, "v2_"))
		assert.Equal(t, int64(1), rowCount(t, mgr.DB(), "v2_daily_events"))

		status = getStatus(t, c)
		assert.False(t, status.Exists)
		assert.False(t, status.CanCleanup)
		assert.Equal(t, notification.MsgErrCleanupNoLegacyTables, status.ErrorKey)
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
		markMigration(t, mgr, entities.MigrationStatusCompleted, "", entities.AuxiliaryDataStatusSucceeded)
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
		markMigration(t, mgr, entities.MigrationStatusCompleted, "reviews failed", entities.AuxiliaryDataStatusSucceeded)
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

	t.Run("migration state other than completed blocks cleanup", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		createLegacyTables(t, mgr.DB(), allLegacy)
		c := newCleanupHandler(t, mgr)

		for _, state := range []entities.MigrationStatus{entities.MigrationStatusIdle, entities.MigrationStatusMigrating, entities.MigrationStatusFailed} {
			markMigration(t, mgr, state, "", entities.AuxiliaryDataStatusSucceeded)
			status := getStatus(t, c)
			assert.False(t, status.CanCleanup, string(state))
			assert.Equal(t, notification.MsgErrCleanupRestartRequired, status.ErrorKey, string(state))
			assert.Contains(t, status.Reason, "Migration has not completed", "the state gate, not the v2-only gate, refused")
			code, resp := postCleanup(t, c)
			assert.Equal(t, http.StatusBadRequest, code, string(state))
			assert.Equal(t, notification.MsgErrCleanupRestartRequired, resp.ErrorKey, string(state))
		}

		// Without a state row the state is unreadable, which also refuses.
		require.NoError(t, mgr.DB().Where("1 = 1").Delete(&entities.MigrationState{}).Error)
		status := getStatus(t, c)
		assert.False(t, status.CanCleanup)
		assert.Equal(t, notification.MsgErrCleanupAccessFailed, status.ErrorKey)
		code, resp := postCleanup(t, c)
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, notification.MsgErrCleanupAccessFailed, resp.ErrorKey)

		after := env.tables(t, mgr.DB())
		for _, name := range allLegacy {
			assert.Contains(t, after, name)
		}
	})

	t.Run("production wiring reaches the database", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		markMigration(t, mgr, entities.MigrationStatusCompleted, "", entities.AuxiliaryDataStatusSucceeded)
		createLegacyTables(t, mgr.DB(), allLegacy)

		// The same objects the facade wires in v2-only mode: a v2only detection
		// repository over the manager's connection, and the manager itself.
		ds := newV2OnlyDatastore(t, mgr)
		core := apitest.NewCore(t, apitest.WithSettingsFunc(func(s *conf.Settings) {
			s.Output.SQLite.Enabled = false
			s.Output.MySQL.Enabled = true
			s.Output.MySQL.Database = cleanupTestDatabase
		}))
		core.Repo = v2only.NewDetectionRepository(ds)
		core.V2Manager = mgr
		core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		_, hasGetDB := core.Repo.(interface{ GetDB() *gorm.DB })
		require.False(t, hasGetDB, "the production repository must not offer a connection, or this test no longer covers the manager seam")
		h := New(core, nil)
		h.RegisterLegacyCleanupRoutes(core.Group)
		enableV2OnlyMode(t)

		var status LegacyStatusResponse
		serveJSON(t, core.Echo, http.MethodGet, legacyStatusPath, &status)
		assert.True(t, status.Exists)
		assert.True(t, status.CanCleanup, status.Reason)

		var resp CleanupActionResponse
		assert.Equal(t, http.StatusOK, serveJSON(t, core.Echo, http.MethodPost, legacyCleanupPath, &resp), resp.Message)
		core.Wait()

		state, errMsg, remaining, _ := h.cleanupStatus.Get()
		assert.Equal(t, CleanupStateCompleted, state, errMsg)
		assert.Empty(t, remaining)
		after := env.tables(t, mgr.DB())
		for _, name := range legacyOnly {
			assert.NotContains(t, after, name)
		}
	})

	t.Run("unmigrated legacy tail refuses at POST and keeps every table", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		markMigration(t, mgr, entities.MigrationStatusCompleted, "", entities.AuxiliaryDataStatusSucceeded)
		createLegacyTables(t, mgr.DB(), allLegacy)
		insertLegacyTailNote(t, mgr.DB())
		c := newCleanupHandler(t, mgr)

		code, resp := postCleanup(t, c)
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, notification.MsgErrCleanupSafetyCheck, resp.ErrorKey)
		assert.Equal(t, reasonLegacyUnmigrated, resp.Message)
		assert.Subset(t, env.tables(t, mgr.DB()), allLegacy)
		state, _, _, _ := c.cleanupStatus.Get()
		assert.Equal(t, CleanupStateIdle, state)
	})

	t.Run("the background drop re-proves the tail before dropping", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		markMigration(t, mgr, entities.MigrationStatusCompleted, "", entities.AuxiliaryDataStatusSucceeded)
		createLegacyTables(t, mgr.DB(), allLegacy)
		c := newCleanupHandler(t, mgr)

		status := getStatus(t, c)
		require.True(t, status.CanCleanup, "the tail is reconciled when the card is shown")
		insertLegacyTailNote(t, mgr.DB())

		remaining, err := c.cleanupMySQLLegacy(t.Context(), mgr)
		require.Error(t, err)
		assert.Empty(t, remaining)
		assert.Subset(t, env.tables(t, mgr.DB()), allLegacy)
	})

	t.Run("proof read error refuses and keeps every table", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		markMigration(t, mgr, entities.MigrationStatusCompleted, "", entities.AuxiliaryDataStatusSucceeded)
		createLegacyTables(t, mgr.DB(), allLegacy)
		insertLegacyTailNote(t, mgr.DB())
		// The state read and the listing still work; the membership query does not.
		require.NoError(t, mgr.DB().Connection(func(tx *gorm.DB) error {
			if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
				return err
			}
			if err := tx.Exec("DROP TABLE `v2_detections`").Error; err != nil {
				return err
			}
			return tx.Exec("SET FOREIGN_KEY_CHECKS = 1").Error
		}))
		c := newCleanupHandler(t, mgr)

		code, resp := postCleanup(t, c)
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, notification.MsgErrCleanupAccessFailed, resp.ErrorKey)
		assert.Equal(t, reasonCannotVerify, resp.Message)
		assert.Subset(t, env.tables(t, mgr.DB()), allLegacy)
	})

	t.Run("notes already dropped: a retry drops the remaining legacy-only tables", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		markMigration(t, mgr, entities.MigrationStatusCompleted, "", entities.AuxiliaryDataStatusSucceeded)
		createLegacyTables(t, mgr.DB(), allLegacy)
		// What a manual drop with foreign key checks off leaves behind.
		require.NoError(t, mgr.DB().Connection(func(tx *gorm.DB) error {
			if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
				return err
			}
			if err := tx.Exec("DROP TABLE `notes`").Error; err != nil {
				return err
			}
			return tx.Exec("SET FOREIGN_KEY_CHECKS = 1").Error
		}))
		c := newCleanupHandler(t, mgr)

		status := getStatus(t, c)
		require.True(t, status.CanCleanup)
		names := make([]string, 0, len(status.Tables))
		for _, tbl := range status.Tables {
			names = append(names, tbl.Name)
		}
		assert.Equal(t, legacyOnly[:4], names)

		code, resp := postCleanup(t, c)
		require.Equal(t, http.StatusOK, code, resp.Message)
		after := env.tables(t, mgr.DB())
		for _, name := range legacyOnly {
			assert.NotContains(t, after, name)
		}
		assert.Subset(t, after, legacyShared)

		status = getStatus(t, c)
		assert.False(t, status.Exists)
		assert.Equal(t, notification.MsgErrCleanupNoLegacyTables, status.ErrorKey)
	})

	t.Run("cancelled context refuses at the state read", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		markMigration(t, mgr, entities.MigrationStatusCompleted, "", entities.AuxiliaryDataStatusSucceeded)
		createLegacyTables(t, mgr.DB(), allLegacy)
		c := newCleanupHandler(t, mgr)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, tables, refusal := c.resolveMySQLLegacyTables(ctx, mgr, mysqlResolveStatus)
		assert.Empty(t, tables)
		require.NotNil(t, refusal)
		assert.Equal(t, notification.MsgErrCleanupAccessFailed, refusal.errorKey)
		assert.Equal(t, reasonStateUnreadable, refusal.reason)
	})

	t.Run("drop failure reports the tables left", func(t *testing.T) {
		mgr := env.newManager(t, true)
		env.reset(t, mgr)
		require.NoError(t, mgr.Initialize())
		createLegacyTables(t, mgr.DB(), allLegacy)
		c := newCleanupHandler(t, mgr)

		// results drops cleanly; notes is still referenced by note_reviews, so the
		// second drop fails and only notes is reported as left.
		remaining, err := c.dropMySQLLegacyTables(t.Context(), mgr.DB(), []LegacyTableInfo{{Name: "results"}, {Name: "notes"}})
		require.Error(t, err)
		assert.Equal(t, []string{"notes"}, remaining)
		after := env.tables(t, mgr.DB())
		assert.NotContains(t, after, "results")
		assert.Contains(t, after, "notes")
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

// insertLegacyTailNote adds a legacy note above the migration watermark that has no
// detection in v2, the record the tail proof must refuse to drop.
func insertLegacyTailNote(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec("INSERT INTO `notes` (id) VALUES (?)", legacyFixtureLastMigratedID+1).Error)
}

// newV2OnlyDatastore builds the v2-only datastore the facade builds over mgr.
func newV2OnlyDatastore(t *testing.T, mgr *datastoreV2.MySQLManager) *v2only.Datastore {
	t.Helper()
	db := mgr.DB()
	ds, err := v2only.New(&v2only.Config{
		Manager:   mgr,
		Detection: repository.NewDetectionRepository(db, nil, true, true),
		Label:     repository.NewLabelRepository(db, nil, true, true),
		Model:     repository.NewModelRepository(db, nil, true, true),
	})
	require.NoError(t, err)
	return ds
}

// serveJSON sends one request through the echo router and decodes the JSON
// answer into out. It returns the status code.
func serveJSON(t *testing.T, e *echo.Echo, method, path string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(method, path, http.NoBody))
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), out))
	return rec.Code
}
