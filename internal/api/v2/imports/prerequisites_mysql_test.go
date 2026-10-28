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
	// schema default when empty).
	setup := func(t *testing.T, schemaDDL, legacyTableCharset string) *Handler {
		t.Helper()
		env.reset(t, mgr)
		db := mgr.DB()
		require.NoError(t, db.Exec("ALTER DATABASE `"+cleanupTestDatabase+"` "+schemaDDL).Error)
		require.NoError(t, mgr.Initialize())
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
	)

	t.Run("server version does not block", func(t *testing.T) {
		c := setup(t, utf8mb4Schema, "")
		check := c.checkMySQLServerVersion()
		t.Logf("server version check: %s: %s", check.Status, check.Message)
		assert.Contains(t, []string{CheckStatusPassed, CheckStatusWarning}, check.Status, check.Message)
	})

	t.Run("utf8mb4 schema passes", func(t *testing.T) {
		c := setup(t, utf8mb4Schema, "")
		check := c.checkMySQLCharset()
		assert.Equal(t, CheckStatusPassed, check.Status, check.Message)
	})

	t.Run("latin1 schema with latin1 legacy text warns", func(t *testing.T) {
		c := setup(t, latin1Schema, "")
		check := c.checkMySQLCharset()
		assert.Equal(t, CheckStatusWarning, check.Status, check.Message)
	})

	t.Run("latin1 target cannot hold utf8mb4 legacy text", func(t *testing.T) {
		c := setup(t, latin1Schema, "utf8mb4")
		before := snapshotCharsets(t, mgr.DB())

		check := c.checkMySQLCharset()

		assert.Equal(t, CheckStatusFailed, check.Status, check.Message)
		assert.Contains(t, check.Message, "v2_")
		assert.Equal(t, before, snapshotCharsets(t, mgr.DB()), "the check must not change the schema")
	})
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
