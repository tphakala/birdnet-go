package v2

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
)

func TestWriteAndReadConsolidationState(t *testing.T) {
	tmpDir := t.TempDir()

	state := &ConsolidationState{
		LegacyPath:     "/data/birdnet.db",
		V2Path:         "/data/birdnet_v2.db",
		BackupPath:     "/data/birdnet.db.20260202-120000.old",
		ConfiguredPath: "/data/birdnet.db",
		StartedAt:      time.Now().Truncate(time.Second), // Truncate for comparison
	}

	// Write state
	err := WriteConsolidationState(tmpDir, state)
	require.NoError(t, err)

	// Verify file exists
	stateFilePath := filepath.Join(tmpDir, StateFileName)
	assert.FileExists(t, stateFilePath)

	// Read state back
	readState, err := ReadConsolidationState(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, readState)

	assert.Equal(t, state.LegacyPath, readState.LegacyPath)
	assert.Equal(t, state.V2Path, readState.V2Path)
	assert.Equal(t, state.BackupPath, readState.BackupPath)
	assert.Equal(t, state.ConfiguredPath, readState.ConfiguredPath)
	assert.Equal(t, state.StartedAt.Unix(), readState.StartedAt.Unix())
}

func TestReadConsolidationState_FileNotExists(t *testing.T) {
	tmpDir := t.TempDir()

	state, err := ReadConsolidationState(tmpDir)
	require.NoError(t, err)
	assert.Nil(t, state)
}

func TestReadConsolidationState_CorruptFile(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, StateFileName)

	// Write corrupt JSON
	err := os.WriteFile(stateFilePath, []byte("not valid json"), 0o600)
	require.NoError(t, err)

	state, err := ReadConsolidationState(tmpDir)
	require.Error(t, err)
	assert.Nil(t, state)
	assert.Contains(t, err.Error(), "failed to unmarshal")
}

func TestDeleteConsolidationState(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, StateFileName)

	// Create state file
	err := os.WriteFile(stateFilePath, []byte("{}"), 0o600)
	require.NoError(t, err)
	assert.FileExists(t, stateFilePath)

	// Delete it
	err = DeleteConsolidationState(tmpDir)
	require.NoError(t, err)
	assert.NoFileExists(t, stateFilePath)
}

func TestDeleteConsolidationState_FileNotExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Should not error if file doesn't exist
	err := DeleteConsolidationState(tmpDir)
	assert.NoError(t, err)
}

func TestGenerateBackupPath(t *testing.T) {
	tests := []struct {
		name       string
		legacyPath string
		wantPrefix string
		wantSuffix string
	}{
		{
			name:       "standard path",
			legacyPath: "/data/birdnet.db",
			wantPrefix: "/data/birdnet.db.",
			wantSuffix: ".old",
		},
		{
			name:       "relative path",
			legacyPath: "birdnet.db",
			wantPrefix: "birdnet.db.",
			wantSuffix: ".old",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateBackupPath(tt.legacyPath)
			assert.Greater(t, len(result), len(tt.wantPrefix)+len(tt.wantSuffix),
				"result should include timestamp")
			assert.Contains(t, result, tt.wantPrefix)
			assert.Contains(t, result, tt.wantSuffix)
		})
	}
}

func TestGenerateBackupPath_TimestampFormat(t *testing.T) {
	legacyPath := "/data/birdnet.db"
	result := GenerateBackupPath(legacyPath)

	// Should match format: /data/birdnet.db.YYYYMMDD-HHMMSS.old
	// Extract timestamp part
	prefix := legacyPath + "."
	suffix := ".old"
	timestamp := result[len(prefix) : len(result)-len(suffix)]

	// Verify timestamp format (YYYYMMDD-HHMMSS)
	_, err := time.Parse("20060102-150405", timestamp)
	assert.NoError(t, err, "timestamp should be in format YYYYMMDD-HHMMSS")
}

func TestResumeConsolidation_NoStateFile(t *testing.T) {
	tmpDir := t.TempDir()
	log := newTestLogger()

	resumed, path, err := ResumeConsolidation(tmpDir, log)
	require.NoError(t, err)
	assert.False(t, resumed)
	assert.Empty(t, path)
}

func TestResumeConsolidation_AlreadyComplete(t *testing.T) {
	tmpDir := t.TempDir()
	log := newTestLogger()

	configuredPath := filepath.Join(tmpDir, "birdnet.db")
	v2Path := filepath.Join(tmpDir, "birdnet_v2.db")
	backupPath := filepath.Join(tmpDir, "birdnet.db.20260202-120000.old")

	// Create a completed v2 database at the configured path
	mgr, err := NewSQLiteManager(Config{DirectPath: configuredPath})
	require.NoError(t, err)
	err = mgr.Initialize()
	require.NoError(t, err)
	// Mark as completed
	err = mgr.DB().Model(&entities.MigrationState{}).Where("id = 1").Update("state", entities.MigrationStatusCompleted).Error
	require.NoError(t, err)
	require.NoError(t, mgr.Close())

	// Write a state file (simulating interrupted state file deletion)
	state := &ConsolidationState{
		LegacyPath:     configuredPath,
		V2Path:         v2Path,
		BackupPath:     backupPath,
		ConfiguredPath: configuredPath,
		StartedAt:      time.Now(),
	}
	err = WriteConsolidationState(tmpDir, state)
	require.NoError(t, err)

	// Resume should detect completion and clean up state file
	resumed, path, err := ResumeConsolidation(tmpDir, log)
	require.NoError(t, err)
	assert.True(t, resumed)
	assert.Equal(t, configuredPath, path)

	// State file should be cleaned up
	stateFilePath := filepath.Join(tmpDir, StateFileName)
	assert.NoFileExists(t, stateFilePath)
}

func TestResumeConsolidation_ResumeFromStep8(t *testing.T) {
	tmpDir := t.TempDir()
	log := newTestLogger()

	configuredPath := filepath.Join(tmpDir, "birdnet.db")
	v2Path := filepath.Join(tmpDir, "birdnet_v2.db")
	backupPath := filepath.Join(tmpDir, "birdnet.db.20260202-120000.old")

	// Create a v2 database at v2Path (simulating step 7 completed but not step 8)
	mgr, err := NewSQLiteManager(Config{DirectPath: v2Path})
	require.NoError(t, err)
	err = mgr.Initialize()
	require.NoError(t, err)
	err = mgr.DB().Model(&entities.MigrationState{}).Where("id = 1").Update("state", entities.MigrationStatusCompleted).Error
	require.NoError(t, err)
	require.NoError(t, mgr.Close())

	// Create backup file (simulating legacy was renamed)
	err = os.WriteFile(backupPath, []byte("legacy content"), 0o600)
	require.NoError(t, err)

	// Write state file
	state := &ConsolidationState{
		LegacyPath:     configuredPath,
		V2Path:         v2Path,
		BackupPath:     backupPath,
		ConfiguredPath: configuredPath,
		StartedAt:      time.Now(),
	}
	err = WriteConsolidationState(tmpDir, state)
	require.NoError(t, err)

	// Resume should complete the rename
	resumed, path, err := ResumeConsolidation(tmpDir, log)
	require.NoError(t, err)
	assert.True(t, resumed)
	assert.Equal(t, configuredPath, path)

	// V2 should now be at configured path
	assert.FileExists(t, configuredPath)
	assert.NoFileExists(t, v2Path)

	// State file should be cleaned up
	stateFilePath := filepath.Join(tmpDir, StateFileName)
	assert.NoFileExists(t, stateFilePath)
}

func TestCleanupWALFiles(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create WAL and SHM files
	walPath := dbPath + "-wal"
	shmPath := dbPath + "-shm"
	err := os.WriteFile(walPath, []byte("wal content"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(shmPath, []byte("shm content"), 0o600)
	require.NoError(t, err)

	assert.FileExists(t, walPath)
	assert.FileExists(t, shmPath)

	// Clean up
	cleanupWALFiles(dbPath)

	assert.NoFileExists(t, walPath)
	assert.NoFileExists(t, shmPath)
}

func TestCleanupWALFiles_NoFilesExist(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Should not panic when files don't exist
	cleanupWALFiles(dbPath)
}

// testLogger is a simple logger for testing
type testLogger struct{}

func newTestLogger() *testLogger {
	return &testLogger{}
}

func (l *testLogger) Module(name string) logger.Logger                { return l }
func (l *testLogger) Trace(msg string, fields ...logger.Field)        {}
func (l *testLogger) Debug(msg string, fields ...logger.Field)        {}
func (l *testLogger) Info(msg string, fields ...logger.Field)         {}
func (l *testLogger) Warn(msg string, fields ...logger.Field)         {}
func (l *testLogger) Error(msg string, fields ...logger.Field)        {}
func (l *testLogger) With(fields ...logger.Field) logger.Logger       { return l }
func (l *testLogger) WithContext(ctx context.Context) logger.Logger   { return l }
func (l *testLogger) Log(level logger.LogLevel, msg string, fields ...logger.Field) {
}
func (l *testLogger) Flush() error { return nil }

// Ensure testLogger implements logger.Logger
var _ logger.Logger = (*testLogger)(nil)
