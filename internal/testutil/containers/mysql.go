//go:build integration

package containers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql" // Registers the "mysql" database/sql driver.
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// validTableNameRe matches valid MySQL identifier names.
// MySQL identifier rules: letters, digits, underscore, dollar sign; must not start with a digit.
var validTableNameRe = regexp.MustCompile(`^[a-zA-Z_$][a-zA-Z0-9_$]*$`)

// MySQLContainer wraps a testcontainers MySQL or MariaDB instance with helper methods.
type MySQLContainer struct {
	container *mysql.MySQLContainer
	db        *sql.DB
	dsn       string
}

// MySQLConfig holds configuration for MySQL container creation.
type MySQLConfig struct {
	// Database name (default: "birdnet_test")
	Database string
	// Root password (default: "test")
	RootPassword string
	// Username for non-root user (default: "testuser")
	Username string
	// Password for non-root user (default: "testpass")
	Password string
	// Server flavor (default: FlavorMySQL). Ignored when MYSQL_TEST_IMAGE is set.
	Flavor MySQLFlavor
	// Image tag (default: "8.0" for MySQL, "11.8" for MariaDB). Ignored when
	// MYSQL_TEST_IMAGE is set, which wins over Flavor and ImageTag.
	ImageTag string
	// Scripts to execute on startup (path to .sql files)
	InitScripts []string
}

// MySQLFlavor names the database server a MySQLContainer runs. The values are
// also the Docker Hub repository names of the images.
type MySQLFlavor string

const (
	// FlavorMySQL selects the official MySQL image.
	FlavorMySQL MySQLFlavor = "mysql"
	// FlavorMariaDB selects the official MariaDB image.
	FlavorMariaDB MySQLFlavor = "mariadb"
)

// defaultMySQLImageTag is the MySQL image tag used when a config does not set one.
const defaultMySQLImageTag = "8.0"

// defaultMariaDBImageTag is the MariaDB image tag used when a config selects
// MariaDB without a tag.
const defaultMariaDBImageTag = "11.8"

// MySQLImageEnvVar names the environment variable that selects the server image
// for every MySQLContainer, as "mysql:<tag>" or "mariadb:<tag>".
const MySQLImageEnvVar = "MYSQL_TEST_IMAGE"

// ciEnvVar is set to a non-empty value by GitHub Actions and most CI systems.
const ciEnvVar = "CI"

// mariaDBReadyLog is logged once by the real MariaDB server. The entrypoint's
// temporary server logs "port: 0" first, so this matches only the final start.
const mariaDBReadyLog = "port: 3306  mariadb.org binary distribution"

// DefaultMySQLConfig returns a MySQLConfig with sensible defaults. Flavor and
// ImageTag stay empty so the server resolves to the flavor's default tag.
func DefaultMySQLConfig() MySQLConfig {
	return MySQLConfig{
		Database:     "birdnet_test",
		RootPassword: "test",
		Username:     "testuser",
		Password:     "testpass",
	}
}

// resolveMySQLImage picks the server image. A non-empty envImage
// (MYSQL_TEST_IMAGE) wins over the config; without it, running on CI is an
// error so a misspelled variable cannot make every CI leg test the default
// server; otherwise the config's Flavor and ImageTag apply, then the defaults.
func resolveMySQLImage(cfg *MySQLConfig, envImage string, onCI bool) (image string, flavor MySQLFlavor, err error) {
	if envImage != "" {
		repo, tag, ok := strings.Cut(envImage, ":")
		flavor = MySQLFlavor(repo)
		if !ok || tag == "" || (flavor != FlavorMySQL && flavor != FlavorMariaDB) {
			return "", "", fmt.Errorf("invalid %s %q: want \"mysql:<tag>\" or \"mariadb:<tag>\"", MySQLImageEnvVar, envImage)
		}
		return envImage, flavor, nil
	}
	if onCI {
		return "", "", fmt.Errorf("%s must be set on CI so each leg tests the server it names", MySQLImageEnvVar)
	}

	flavor = cfg.Flavor
	if flavor == "" {
		flavor = FlavorMySQL
	}
	tag := cfg.ImageTag
	switch flavor {
	case FlavorMySQL:
		if tag == "" {
			tag = defaultMySQLImageTag
		}
	case FlavorMariaDB:
		if tag == "" {
			tag = defaultMariaDBImageTag
		}
	default:
		return "", "", fmt.Errorf("unknown database flavor %q", flavor)
	}
	return string(flavor) + ":" + tag, flavor, nil
}

// NewMySQLContainer creates and starts a MySQL or MariaDB container with the
// given config. The server is chosen by MYSQL_TEST_IMAGE when set, otherwise by
// the config's Flavor and ImageTag (see resolveMySQLImage).
// If config is nil, uses DefaultMySQLConfig().
func NewMySQLContainer(ctx context.Context, config *MySQLConfig) (*MySQLContainer, error) {
	if config == nil {
		defaultCfg := DefaultMySQLConfig()
		config = &defaultCfg
	}

	image, flavor, err := resolveMySQLImage(config, os.Getenv(MySQLImageEnvVar), os.Getenv(ciEnvVar) != "")
	if err != nil {
		return nil, fmt.Errorf("failed to select database image: %w", err)
	}

	// Build container request
	opts := make([]testcontainers.ContainerCustomizer, 0, 4+len(config.InitScripts))
	opts = append(opts,
		mysql.WithDatabase(config.Database),
		mysql.WithUsername(config.Username),
		mysql.WithPassword(config.Password),
	)

	// Add init scripts if provided
	for _, script := range config.InitScripts {
		opts = append(opts, mysql.WithScripts(script))
	}

	// The module's MySQL readiness log never appears on MariaDB, so wait for
	// the MariaDB server banner instead. User options follow the module's own
	// defaults, so this one wins.
	if flavor == FlavorMariaDB {
		opts = append(opts, testcontainers.WithWaitStrategy(wait.ForLog(mariaDBReadyLog)))
	}

	// Note: Container reuse is not supported by the MySQL module at this time.
	// Containers are created fresh for each test run to ensure isolation.

	// Create and start container
	// Note: mysql.Run already handles waiting for the container to be ready.
	mysqlContainer, err := mysql.Run(ctx, image, opts...)
	if err != nil {
		// mysql.Run returns the container when only the wait failed; terminate it
		// so a failed start leaves nothing running (local runs disable Ryuk).
		if mysqlContainer != nil {
			// Use background context for cleanup to ensure it succeeds even if parent ctx expired
			_ = mysqlContainer.Terminate(context.Background())
		}
		return nil, fmt.Errorf("failed to start %s container: %w", image, err)
	}

	// Get connection string with multiStatements enabled for script execution
	connStr, err := mysqlContainer.ConnectionString(ctx, "multiStatements=true")
	if err != nil {
		// Use background context for cleanup to ensure it succeeds even if parent ctx expired
		_ = mysqlContainer.Terminate(context.Background())
		return nil, fmt.Errorf("failed to get connection string: %w", err)
	}

	// Open database connection
	db, err := sql.Open("mysql", connStr)
	if err != nil {
		// Use background context for cleanup to ensure it succeeds even if parent ctx expired
		_ = mysqlContainer.Terminate(context.Background())
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	// Verify connection with health check
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		// Use background context for cleanup to ensure it succeeds even if parent ctx expired
		_ = mysqlContainer.Terminate(context.Background())
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &MySQLContainer{
		container: mysqlContainer,
		db:        db,
		dsn:       connStr,
	}, nil
}

// GetDB returns the database connection. This connection is shared across tests
// in the same package and should not be closed by individual tests.
func (c *MySQLContainer) GetDB(t *testing.T) *sql.DB {
	t.Helper()
	if c.db == nil {
		t.Fatal("database connection is nil")
	}
	return c.db
}

// DB returns the database connection without requiring a *testing.T.
// This is useful in TestMain or other setup contexts where *testing.T is not available.
// Returns nil if the database connection was not established.
func (c *MySQLContainer) DB() *sql.DB {
	return c.db
}

// GetDSN returns the MySQL DSN (connection string) for the container.
func (c *MySQLContainer) GetDSN() string {
	return c.dsn
}

// GetHost returns the host address where the container is accessible.
func (c *MySQLContainer) GetHost(ctx context.Context) (string, error) {
	return c.container.Host(ctx)
}

// GetPort returns the mapped port where MySQL is accessible.
func (c *MySQLContainer) GetPort(ctx context.Context) (int, error) {
	mappedPort, err := c.container.MappedPort(ctx, "3306")
	if err != nil {
		return 0, err
	}
	return int(mappedPort.Num()), nil
}

// HealthCheck performs a health check on the MySQL database.
func (c *MySQLContainer) HealthCheck(ctx context.Context) error {
	if c.db == nil {
		return fmt.Errorf("database connection is nil")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Execute simple query
	var result int
	if err := c.db.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		return fmt.Errorf("health check query failed: %w", err)
	}

	if result != 1 {
		return fmt.Errorf("health check returned unexpected result: %d", result)
	}

	return nil
}

// isValidTableName validates that a table name contains only safe characters.
// Allows alphanumeric, underscore, and dollar sign (MySQL identifier rules).
func isValidTableName(name string) bool {
	if name == "" {
		return false
	}
	return validTableNameRe.MatchString(name)
}

// Reset truncates all tables in the database with foreign key checks disabled.
// This is useful for resetting state between tests. It runs on one pinned
// connection and restores the checks on every exit path, so a failed truncate
// never leaves them off on a pooled connection.
func (c *MySQLContainer) Reset(ctx context.Context, tables []string) (err error) {
	if c.db == nil {
		return fmt.Errorf("database connection is nil")
	}

	// Validate all table names before executing any queries
	for _, table := range tables {
		if !isValidTableName(table) {
			return fmt.Errorf("invalid table name: %s", table)
		}
	}

	conn, err := c.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Disable foreign key checks
	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return fmt.Errorf("failed to disable foreign key checks: %w", err)
	}
	// Runs before conn.Close (LIFO) and must not be skipped by a cancelled ctx.
	defer func() {
		if _, restoreErr := conn.ExecContext(context.WithoutCancel(ctx), "SET FOREIGN_KEY_CHECKS = 1"); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to enable foreign key checks: %w", restoreErr))
		}
	}()

	// Truncate all specified tables (names already validated)
	for _, table := range tables {
		// Use backticks for identifier quoting
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE `%s`", table)); err != nil {
			return fmt.Errorf("failed to truncate table %s: %w", table, err)
		}
	}

	return nil
}

// ExecuteScript executes a SQL script file against the database.
func (c *MySQLContainer) ExecuteScript(ctx context.Context, scriptPath string) error {
	if c.db == nil {
		return fmt.Errorf("database connection is nil")
	}

	//nolint:gosec // G304: File path is intentionally provided by caller for SQL script execution
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script file: %w", err)
	}

	if _, err := c.db.ExecContext(ctx, string(script)); err != nil {
		return fmt.Errorf("failed to execute script: %w", err)
	}

	return nil
}

// Terminate stops and removes the MySQL container.
// It also closes the database connection if open.
func (c *MySQLContainer) Terminate(ctx context.Context) error {
	// Close database connection first
	if c.db != nil {
		if err := c.db.Close(); err != nil {
			// Log error but continue with container termination
			fmt.Printf("Warning: failed to close database connection: %v\n", err)
		}
		c.db = nil
	}

	// Terminate container
	if c.container != nil {
		if err := c.container.Terminate(ctx); err != nil {
			return fmt.Errorf("failed to terminate container: %w", err)
		}
	}

	return nil
}
