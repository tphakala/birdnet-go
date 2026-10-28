//go:build integration

// internal/api/v2/imports/prerequisites_mysql_test.go
package importsapi

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	"gorm.io/gorm"
)

// TestMySQLServerPrerequisites_RealServer runs the server version and charset
// prerequisites against the real server selected by MYSQL_TEST_IMAGE.
func TestMySQLServerPrerequisites_RealServer(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "integration")
	t.Attr("feature", "prerequisites")

	migrationTestMu.Lock()
	t.Cleanup(migrationTestMu.Unlock)
	prevV2Only := isV2OnlyMode
	isV2OnlyMode = false
	t.Cleanup(func() { isV2OnlyMode = prevV2Only })

	env := newCleanupTestEnv(t)
	mgr := env.newManager(t, true)

	// setup recreates the v2_ tables under the given schema default, then adds
	// a legacy notes table with one text column of legacyTableCharset (the
	// schema default when empty). A non-empty v2TableCharset converts the v2_
	// tables to that character set, to reproduce the tables an older release
	// created on such a schema; Initialize now creates utf8mb4 tables there.
	setup := func(t *testing.T, schemaDDL, legacyTableCharset, v2TableCharset string) *Handler {
		t.Helper()
		env.reset(t, mgr)
		db := mgr.DB()
		require.NoError(t, db.Exec("ALTER DATABASE `"+cleanupTestDatabase+"` "+schemaDDL).Error)
		require.NoError(t, mgr.Initialize())
		if v2TableCharset != "" {
			convertV2TablesCharset(t, db, v2TableCharset)
		}
		ddl := "CREATE TABLE `notes` (id INT PRIMARY KEY, common_name VARCHAR(100))"
		if legacyTableCharset != "" {
			ddl += " DEFAULT CHARSET=" + legacyTableCharset
		}
		require.NoError(t, db.Exec(ddl).Error)

		c := createLegacyTestHandler(t, echo.New(), createMySQLTestSettings())
		c.V2Manager = mgr
		return c
	}
	const (
		utf8mb4Schema = "CHARACTER SET utf8mb4"
		latin1Schema  = "CHARACTER SET latin1 COLLATE latin1_swedish_ci"
		latin1Charset = "latin1"
	)

	t.Run("server version does not block", func(t *testing.T) {
		c := setup(t, utf8mb4Schema, "", "")
		check := c.checkMySQLServerVersion()
		t.Logf("server version check: %s: %s", check.Status, check.Message)
		assert.Contains(t, []string{CheckStatusPassed, CheckStatusWarning}, check.Status, check.Message)
	})

	t.Run("utf8mb4 schema passes", func(t *testing.T) {
		c := setup(t, utf8mb4Schema, "", "")
		check := c.checkMySQLCharset()
		assert.Equal(t, CheckStatusPassed, check.Status, check.Message)
	})

	t.Run("latin1 schema with target tables from this release passes", func(t *testing.T) {
		c := setup(t, latin1Schema, "", "")
		check := c.checkMySQLCharset()
		assert.Equal(t, CheckStatusPassed, check.Status, check.Message)
	})

	t.Run("latin1 schema with latin1 target tables from an older release warns", func(t *testing.T) {
		c := setup(t, latin1Schema, "", latin1Charset)
		check := c.checkMySQLCharset()
		assert.Equal(t, CheckStatusWarning, check.Status, check.Message)
	})

	t.Run("latin1 target tables from an older release cannot hold utf8mb4 legacy text", func(t *testing.T) {
		c := setup(t, latin1Schema, "utf8mb4", latin1Charset)
		before := snapshotCharsets(t, mgr.DB())

		check := c.checkMySQLCharset()

		assert.Equal(t, CheckStatusFailed, check.Status, check.Message)
		assert.Contains(t, check.Message, "v2_")
		assert.Equal(t, before, snapshotCharsets(t, mgr.DB()), "the check must not change the schema")
	})
}

// convertV2TablesCharset converts every v2_ table that has a text column to
// charset, the way an older release left them on a latin1 schema.
func convertV2TablesCharset(t *testing.T, db *gorm.DB, charset string) {
	t.Helper()
	var names []string
	require.NoError(t, db.Raw(`SELECT DISTINCT TABLE_NAME AS name FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME LIKE 'v2\_%' AND CHARACTER_SET_NAME IS NOT NULL`).Scan(&names).Error)
	require.NotEmpty(t, names)
	for _, name := range names {
		require.NoError(t, db.Exec("ALTER TABLE `"+name+"` CONVERT TO CHARACTER SET "+charset).Error)
	}
}

// tableSnapshot is the table list and charset report, for the read-only check.
type tableSnapshot struct {
	tables  []string
	charset dbserver.CharsetReport
}

func snapshotCharsets(t *testing.T, db *gorm.DB) tableSnapshot {
	t.Helper()
	var names []string
	require.NoError(t, db.Raw(`SELECT table_name AS name FROM information_schema.tables
		WHERE table_schema = DATABASE() ORDER BY table_name`).Scan(&names).Error)
	report, err := dbserver.QueryCharset(t.Context(), db)
	require.NoError(t, err)
	return tableSnapshot{tables: names, charset: report}
}
