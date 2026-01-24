package v2

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// getMySQLConfig returns MySQL config from environment variables.
// Returns nil if MySQL is not configured for testing.
func getMySQLConfig() *MySQLConfig {
	host := os.Getenv("MYSQL_TEST_HOST")
	if host == "" {
		return nil
	}

	port := os.Getenv("MYSQL_TEST_PORT")
	if port == "" {
		port = "3306"
	}

	return &MySQLConfig{
		Host:     host,
		Port:     port,
		Username: os.Getenv("MYSQL_TEST_USER"),
		Password: os.Getenv("MYSQL_TEST_PASSWORD"),
		Database: os.Getenv("MYSQL_TEST_DATABASE"),
		Debug:    false,
	}
}

// skipIfNoMySQL skips the test if MySQL is not available.
func skipIfNoMySQL(t *testing.T) *MySQLConfig {
	t.Helper()
	cfg := getMySQLConfig()
	if cfg == nil {
		t.Skip("MySQL not configured. Set MYSQL_TEST_HOST, MYSQL_TEST_USER, MYSQL_TEST_PASSWORD, MYSQL_TEST_DATABASE to enable.")
	}
	return cfg
}

func TestV2TableName(t *testing.T) {
	tests := []struct {
		baseName string
		want     string
	}{
		{"labels", "v2_labels"},
		{"detections", "v2_detections"},
		{"migration_state", "v2_migration_state"},
	}

	for _, tt := range tests {
		t.Run(tt.baseName, func(t *testing.T) {
			got := V2TableName(tt.baseName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMySQLManager_IsMySQL(t *testing.T) {
	// This test doesn't require a real MySQL connection
	// We're just testing the interface
	cfg := skipIfNoMySQL(t)

	mgr, err := NewMySQLManager(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	assert.True(t, mgr.IsMySQL())
	assert.Equal(t, v2TablePrefix, mgr.TablePrefix())
}

func TestMySQLManager_Initialize(t *testing.T) {
	cfg := skipIfNoMySQL(t)

	mgr, err := NewMySQLManager(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = mgr.Delete()
		_ = mgr.Close()
	})

	err = mgr.Initialize()
	require.NoError(t, err)

	// Verify tables exist with v2_ prefix
	assert.True(t, mgr.db.Migrator().HasTable(V2TableName("labels")))
	assert.True(t, mgr.db.Migrator().HasTable(V2TableName("ai_models")))
	assert.True(t, mgr.db.Migrator().HasTable(V2TableName("detections")))
	assert.True(t, mgr.db.Migrator().HasTable(V2TableName("migration_state")))
}

func TestMySQLManager_Initialize_SeedsBirdNETModel(t *testing.T) {
	cfg := skipIfNoMySQL(t)

	mgr, err := NewMySQLManager(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = mgr.Delete()
		_ = mgr.Close()
	})

	err = mgr.Initialize()
	require.NoError(t, err)

	// Verify BirdNET model was seeded
	var model entities.AIModel
	err = mgr.DB().Where("name = ? AND version = ?", "BirdNET", "2.4").First(&model).Error
	require.NoError(t, err)
	assert.Equal(t, "BirdNET", model.Name)
	assert.Equal(t, entities.ModelTypeBird, model.ModelType)
}

func TestMySQLManager_Exists(t *testing.T) {
	cfg := skipIfNoMySQL(t)

	mgr, err := NewMySQLManager(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = mgr.Delete()
		_ = mgr.Close()
	})

	// Before initialization, v2 tables don't exist
	assert.False(t, mgr.Exists())

	err = mgr.Initialize()
	require.NoError(t, err)

	// After initialization, v2 tables exist
	assert.True(t, mgr.Exists())
}

func TestMySQLManager_Delete(t *testing.T) {
	cfg := skipIfNoMySQL(t)

	mgr, err := NewMySQLManager(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	err = mgr.Initialize()
	require.NoError(t, err)

	assert.True(t, mgr.Exists())

	err = mgr.Delete()
	require.NoError(t, err)

	// After deletion, v2 tables should not exist
	assert.False(t, mgr.db.Migrator().HasTable(V2TableName("labels")))
	assert.False(t, mgr.db.Migrator().HasTable(V2TableName("detections")))
	assert.False(t, mgr.db.Migrator().HasTable(V2TableName("migration_state")))
}

func TestMySQLManager_Path(t *testing.T) {
	cfg := skipIfNoMySQL(t)

	mgr, err := NewMySQLManager(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	path := mgr.Path()
	assert.Contains(t, path, cfg.Host)
	assert.Contains(t, path, cfg.Database)
}
