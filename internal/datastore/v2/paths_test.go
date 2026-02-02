package v2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

func TestDeriveV2Path(t *testing.T) {
	tests := []struct {
		name           string
		configuredPath string
		expectedV2Path string
	}{
		{
			name:           "standard db extension",
			configuredPath: "/data/birdnet.db",
			expectedV2Path: "/data/birdnet_v2.db",
		},
		{
			name:           "sqlite extension",
			configuredPath: "/data/mybirds.sqlite",
			expectedV2Path: "/data/mybirds_v2.sqlite",
		},
		{
			name:           "no extension",
			configuredPath: "/data/database",
			expectedV2Path: "/data/database_v2",
		},
		{
			name:           "multiple dots in filename",
			configuredPath: "/data/my.bird.db",
			expectedV2Path: "/data/my.bird_v2.db",
		},
		{
			name:           "hidden file with extension",
			configuredPath: "/data/.birdnet.db",
			expectedV2Path: "/data/.birdnet_v2.db",
		},
		{
			name:           "relative path",
			configuredPath: "birdnet.db",
			expectedV2Path: "birdnet_v2.db",
		},
		{
			name:           "nested directory",
			configuredPath: "/home/user/data/db/birdnet.db",
			expectedV2Path: "/home/user/data/db/birdnet_v2.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deriveV2Path(tt.configuredPath)
			assert.Equal(t, tt.expectedV2Path, result)
		})
	}
}

func TestPathDeriver_V2MigrationPath(t *testing.T) {
	deriver := NewPathDeriver("/data/birdnet.db")

	assert.Equal(t, "/data/birdnet.db", deriver.ConfiguredPath())
	assert.Equal(t, "/data/birdnet_v2.db", deriver.V2MigrationPath())
	assert.Equal(t, "/data", deriver.DataDir())
}

func TestPathDeriver_ValidateV2PathAvailable_PathDoesNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")

	deriver := NewPathDeriver(configuredPath)
	err := deriver.ValidateV2PathAvailable()

	assert.NoError(t, err, "should succeed when v2 path doesn't exist")
}

func TestPathDeriver_ValidateV2PathAvailable_PathExistsAsV2Database(t *testing.T) {
	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")
	v2Path := filepath.Join(tmpDir, "birdnet_v2.db")

	// Create a v2 database at the v2 path
	mgr, err := NewSQLiteManager(Config{DirectPath: v2Path})
	require.NoError(t, err)
	err = mgr.Initialize()
	require.NoError(t, err)

	// Mark as completed so CheckSQLiteHasV2Schema returns true
	err = mgr.DB().Model(&entities.MigrationState{}).Where("id = 1").Update("state", entities.MigrationStatusCompleted).Error
	require.NoError(t, err)
	require.NoError(t, mgr.Close())

	deriver := NewPathDeriver(configuredPath)
	err = deriver.ValidateV2PathAvailable()

	assert.NoError(t, err, "should succeed when v2 path is a valid v2 migration database")
}

func TestPathDeriver_ValidateV2PathAvailable_PathExistsButNotV2Database(t *testing.T) {
	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")
	v2Path := filepath.Join(tmpDir, "birdnet_v2.db")

	// Create a random file at the v2 path (not a database)
	err := os.WriteFile(v2Path, []byte("not a database"), 0o600)
	require.NoError(t, err)

	deriver := NewPathDeriver(configuredPath)
	err = deriver.ValidateV2PathAvailable()

	require.Error(t, err, "should fail when v2 path exists but is not a v2 database")
	assert.Contains(t, err.Error(), "already exists and is not a v2 migration database")
}

func TestV2MigrationPathFromConfigured(t *testing.T) {
	result := V2MigrationPathFromConfigured("/data/birdnet.db")
	assert.Equal(t, "/data/birdnet_v2.db", result)
}
