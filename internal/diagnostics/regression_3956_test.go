package diagnostics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegression3956DBLost reproduces the GitHub #3956 signature: a boot
// that previously saw a present ~250 MB database is followed by a boot
// that resolves to a fresh install (no .db anywhere) while the clips
// directory survives. The journal must contain a db_lost anomaly and the
// new boot record must prove the database is not hiding elsewhere
// (db_files_found empty).
func TestRegression3956DBLost(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	j := NewJournal(filepath.Join(configDir, "diagnostics", "journal.jsonl"))

	dbPath := filepath.Join(dataDir, "birdnet.db")
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "clips", "2026", "07"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "clips", "2026", "07", "bird.wav"), []byte("audio"), 0o600))

	// Boot 1: healthy v2 install with a ~250 MB database on disk.
	const prevDBSize = 250 * 1024 * 1024
	prev := &BootRecord{
		RecordHeader: NewRecordHeader(RecordTypeBoot),
		App:          AppInfo{Version: "20260601"},
		Datastore: DatastoreSnapshot{
			Dialect:          "sqlite",
			ConfiguredPath:   dbPath,
			ResolvedAbsPath:  dbPath,
			ConfiguredExists: true,
			ConfiguredSize:   prevDBSize,
			StartupDecision:  "v2_restart",
			MigrationState:   "completed",
		},
	}
	require.NoError(t, j.Append(prev))

	// Boot 2 (after "the update"): no .db file anywhere, decision fresh_install.
	p := &BootParams{
		Version:          "20260716",
		ConfigPath:       filepath.Join(configDir, "config.yaml"),
		Dialect:          "sqlite",
		ConfiguredDBPath: dbPath,
		V2SidecarPath:    filepath.Join(dataDir, "birdnet_v2.db"),
		StartupDecision:  "fresh_install",
		MigrationState:   "idle",
		DataDir:          dataDir,
		ConfigDir:        configDir,
	}
	rec, anomalies := RecordBoot(j, p)
	require.NotNil(t, rec)

	// The headline signal: db_lost returned to the caller (for telemetry
	// reporting by database_service) AND persisted in the journal.
	require.NotEmpty(t, anomalies)
	assert.Equal(t, AnomalyDBLost, anomalies[0].Kind)
	data, err := os.ReadFile(j.Path())
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, `"kind":"db_lost"`)
	assert.Contains(t, content, `"type":"anomaly"`)

	// The boot record proves the old DB is not hiding in either tree.
	assert.Empty(t, rec.Datastore.DBFilesFound)
	assert.False(t, rec.Datastore.ConfiguredExists)

	// The clips survived: the exact #3956 shape is documented in the record.
	var clipsListed bool
	for _, f := range rec.DataDirFiles {
		if f.Name == "clips" && f.IsDir {
			clipsListed = true
		}
	}
	assert.True(t, clipsListed)

	// The upgrade direction is recorded (no false version_rollback).
	assert.NotContains(t, content, `"kind":"version_rollback"`)
}
