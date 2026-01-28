//go:build integration && mysql

// Package migration_test contains MySQL-specific integration tests.
// Run with: go test -tags="integration,mysql" -v ./internal/datastore/v2/migration/...
//
// Requires MySQL environment variables:
//
//	MYSQL_TEST_HOST (default: localhost)
//	MYSQL_TEST_PORT (default: 3306)
//	MYSQL_TEST_USER (default: birdnet)
//	MYSQL_TEST_PASSWORD (required)
//	MYSQL_TEST_DATABASE (default: birdnet_test)
package migration_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func skipIfNoMySQL(t *testing.T) {
	t.Helper()
	if os.Getenv("MYSQL_TEST_PASSWORD") == "" {
		t.Skip("Skipping MySQL test: MYSQL_TEST_PASSWORD not set")
	}
}

func getMySQLConfig() *v2.MySQLConfig {
	return &v2.MySQLConfig{
		Host:        getEnvOrDefault("MYSQL_TEST_HOST", "localhost"),
		Port:        getEnvOrDefault("MYSQL_TEST_PORT", "3306"),
		Username:    getEnvOrDefault("MYSQL_TEST_USER", "birdnet"),
		Password:    os.Getenv("MYSQL_TEST_PASSWORD"),
		Database:    getEnvOrDefault("MYSQL_TEST_DATABASE", "birdnet_test"),
		UseV2Prefix: true, // Migration mode uses v2_ prefix
		Debug:       false,
	}
}

// TestMySQL_MigrationInfrastructure_Initializes tests that MySQL migration
// infrastructure can be initialized with v2_ prefixed tables.
func TestMySQL_MigrationInfrastructure_Initializes(t *testing.T) {
	skipIfNoMySQL(t)

	cfg := getMySQLConfig()

	// Create MySQL manager with v2_ prefix for migration mode
	manager, err := v2.NewMySQLManager(cfg)
	require.NoError(t, err, "failed to create MySQL manager")
	defer func() {
		// Clean up: delete v2 tables after test
		_ = manager.Delete()
		_ = manager.Close()
	}()

	// Initialize should create v2_ prefixed tables
	err = manager.Initialize()
	require.NoError(t, err, "failed to initialize MySQL v2 schema")

	// Verify tables exist
	require.True(t, manager.Exists(), "v2 migration_state table should exist")
	require.True(t, manager.IsMySQL(), "should identify as MySQL")

	t.Log("MySQL migration infrastructure initialized successfully")
}

// TestMySQL_StateManager_Works tests that StateManager works with MySQL.
func TestMySQL_StateManager_Works(t *testing.T) {
	skipIfNoMySQL(t)

	cfg := getMySQLConfig()

	manager, err := v2.NewMySQLManager(cfg)
	require.NoError(t, err)
	defer func() {
		_ = manager.Delete()
		_ = manager.Close()
	}()

	err = manager.Initialize()
	require.NoError(t, err)

	// Create state manager
	stateManager := v2.NewStateManager(manager.DB())

	// Get initial state
	state, err := stateManager.GetState()
	require.NoError(t, err, "failed to get migration state")
	require.Equal(t, entities.MigrationStatusIdle, state.State, "initial state should be idle")

	t.Log("MySQL StateManager works correctly")
}
