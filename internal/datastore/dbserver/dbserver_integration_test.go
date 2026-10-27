//go:build integration

package dbserver

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

const (
	// integrationTimeout bounds the whole real-server test.
	integrationTimeout = 3 * time.Minute
	// externalDBSuffix is the required suffix of a database the external-server
	// mode creates and drops.
	externalDBSuffix = "_test"
	// latin1Schema is the schema default the fixture sets.
	latin1Schema = "latin1"
)

// openGorm opens a gorm handle for a go-sql-driver DSN.
func openGorm(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// latin1SchemaDB returns a handle on a latin1 schema. With MYSQL_TEST_HOST set
// it creates (and drops afterwards) a throwaway database named by
// MYSQL_TEST_DATABASE on that server, which needs an account that can create
// databases. Otherwise it starts a container whose init script makes the
// schema default latin1.
func latin1SchemaDB(ctx context.Context, t *testing.T) *gorm.DB {
	t.Helper()

	if host := os.Getenv("MYSQL_TEST_HOST"); host != "" {
		name := os.Getenv("MYSQL_TEST_DATABASE")
		require.True(t, strings.HasSuffix(name, externalDBSuffix), "MYSQL_TEST_DATABASE must end in %s", externalDBSuffix)
		port := os.Getenv("MYSQL_TEST_PORT")
		if port == "" {
			port = "3306"
		}
		user, pass := os.Getenv("MYSQL_TEST_USER"), os.Getenv("MYSQL_TEST_PASSWORD")
		addr := net.JoinHostPort(host, port)
		adminDSN := fmt.Sprintf("%s:%s@tcp(%s)/", user, pass, addr)

		admin, err := sql.Open("mysql", adminDSN)
		require.NoError(t, err)
		t.Cleanup(func() { _ = admin.Close() })
		quoted := "`" + name + "`"
		_, err = admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoted)
		require.NoError(t, err)
		_, err = admin.ExecContext(ctx, "CREATE DATABASE "+quoted+" CHARACTER SET latin1 COLLATE latin1_swedish_ci")
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = admin.ExecContext(context.WithoutCancel(ctx), "DROP DATABASE IF EXISTS "+quoted)
		})
		return openGorm(t, fmt.Sprintf("%s:%s@tcp(%s)/%s", user, pass, addr, name))
	}

	cfg := containers.DefaultMySQLConfig()
	cfg.InitScripts = []string{"testdata/latin1_schema.sql"}
	c, err := containers.NewMySQLContainer(ctx, &cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(context.WithoutCancel(ctx)) })
	return openGorm(t, c.GetDSN())
}

func TestQueryVersionAndCharset_RealServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), integrationTimeout)
	defer cancel()

	db := latin1SchemaDB(ctx, t)
	for _, stmt := range []string{
		"CREATE TABLE t_inherit (id INT PRIMARY KEY, name VARCHAR(20))",
		"CREATE TABLE t_utf8mb4 (id INT PRIMARY KEY, name VARCHAR(20)) DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE t_mixed (id INT PRIMARY KEY, a VARCHAR(20), b VARCHAR(20) CHARACTER SET utf8mb4)",
	} {
		require.NoError(t, db.WithContext(ctx).Exec(stmt).Error, stmt)
	}

	raw, err := QueryVersion(ctx, db)
	require.NoError(t, err)
	t.Logf("server version %q", raw)
	a := AssessRaw(raw, time.Now())
	assert.NotEqual(t, SupportUnknown, a.Level, "version %q must parse", raw)
	if image := os.Getenv("MYSQL_TEST_IMAGE"); image != "" {
		repo, _, _ := strings.Cut(image, ":")
		assert.Equal(t, Vendor(repo), a.Version.Vendor)
	}

	report, err := QueryCharset(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, latin1Schema, report.SchemaCharset)
	assert.NotEmpty(t, report.SchemaCollation)

	byName := map[string][]string{}
	for _, tbl := range report.Tables {
		byName[tbl.Name] = tbl.Charsets
	}
	// The utf8mb4 table must be reported as utf8mb4 on every server, including
	// MariaDB 11.4 and later whose default utf8mb4 collation is uca1400.
	assert.Equal(t, []string{CharsetUTF8MB4}, byName["t_utf8mb4"])
	assert.Equal(t, []string{latin1Schema}, byName["t_inherit"])
	assert.ElementsMatch(t, []string{latin1Schema, CharsetUTF8MB4}, byName["t_mixed"])
	assert.Equal(t, []string{"t_inherit", "t_mixed"}, report.NonUTF8MB4Tables())
	assert.False(t, report.AllUTF8MB4())
}
