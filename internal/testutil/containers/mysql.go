//go:build integration

package containers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
)

// validTableNameRe matches valid MySQL identifier names.
// MySQL identifier rules: letters, digits, underscore, dollar sign; must not start with a digit.
var validTableNameRe = regexp.MustCompile(`^[a-zA-Z_$][a-zA-Z0-9_$]*$`)

// MySQLContainer wraps a testcontainers MySQL instance with helper methods.
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
	// Image tag (default: "8.0")
	ImageTag string
	// Scripts to execute on startup (path to .sql files)
	InitScripts []string
}

// DefaultMySQLConfig returns a MySQLConfig with sensible defaults.
func DefaultMySQLConfig() MySQLConfig {
	return MySQLConfig{
		Database:     "birdnet_test",
		RootPassword: "test",
		Username:     "testuser",
		Password:     "testpass",
		ImageTag:     "8.0",
	}
}

// NewMySQLContainer creates and starts a MySQL container with the given config.
// If config is nil, uses DefaultMySQLConfig().
func NewMySQLContainer(ctx context.Context, config *MySQLConfig) (*MySQLContainer, error) {
	if config == nil {
		defaultCfg := DefaultMySQLConfig()
		config = &defaultCfg
	}

	// Build container request
	opts := []testcontainers.ContainerCustomizer{
		mysql.WithDatabase(config.Database),
		mysql.WithUsername(config.Username),
		mysql.WithPassword(config.Password),
	}

	// Add init scripts if provided
	for _, script := range config.InitScripts {
		opts = append(opts, mysql.WithScripts(script))
	}

	// Note: Container reuse is not supported by the MySQL module at this time.
	// Containers are created fresh for each test run to ensure isolation.

	// Create and start container
	// Note: mysql.RunContainer already handles waiting for the container to be ready
	mysqlContainer, err := mysql.RunContainer(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to start MySQL container: %w", err)
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
	return mappedPort.Int(), nil
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
// This is useful for resetting state between tests.
func (c *MySQLContainer) Reset(ctx context.Context, tables []string) error {
	if c.db == nil {
		return fmt.Errorf("database connection is nil")
	}

	// Validate all table names before executing any queries
	for _, table := range tables {
		if !isValidTableName(table) {
			return fmt.Errorf("invalid table name: %s", table)
		}
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Disable foreign key checks
	if _, err := tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return fmt.Errorf("failed to disable foreign key checks: %w", err)
	}

	// Truncate all specified tables (names already validated)
	for _, table := range tables {
		// Use backticks for identifier quoting
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE `%s`", table)); err != nil {
			return fmt.Errorf("failed to truncate table %s: %w", table, err)
		}
	}

	// Re-enable foreign key checks
	if _, err := tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		return fmt.Errorf("failed to enable foreign key checks: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
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
