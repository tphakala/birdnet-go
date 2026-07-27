package diagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupBootEnv creates a synthetic data dir + config dir with a database,
// a stale consolidation backup, a WAL file, and standard subdirectories.
func setupBootEnv(t *testing.T) (dataDir, configDir string) {
	t.Helper()
	dataDir = t.TempDir()
	configDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "birdnet.db"), make([]byte, 4096), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "birdnet.db-wal"), make([]byte, 512), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "birdnet.db.20260101-090000.old"), make([]byte, 2048), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "clips"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("main:\n"), 0o600))
	return dataDir, configDir
}

func testBootParams(dataDir, configDir string) *BootParams {
	return &BootParams{
		Version:          "20260716",
		BuildDate:        "2026-07-16",
		ConfigPath:       filepath.Join(configDir, "config.yaml"),
		ConfigExisted:    true,
		Dialect:          "sqlite",
		ConfiguredDBPath: filepath.Join(dataDir, "birdnet.db"),
		V2SidecarPath:    filepath.Join(dataDir, "birdnet_v2.db"),
		StartupDecision:  "v2_restart",
		MigrationState:   "completed",
		DataDir:          dataDir,
		ConfigDir:        configDir,
	}
}

func TestRecordBootPopulatesSnapshot(t *testing.T) {
	t.Parallel()
	dataDir, configDir := setupBootEnv(t)
	j := newTestJournal(t)

	rec, anomalies := RecordBoot(j, testBootParams(dataDir, configDir))
	require.NotNil(t, rec)
	assert.Empty(t, anomalies, "first boot has no previous boot to diff against")

	assert.Equal(t, RecordTypeBoot, rec.Type)
	assert.Equal(t, "20260716", rec.App.Version)
	assert.NotEmpty(t, rec.Runtime.OS)
	assert.NotEmpty(t, rec.Runtime.GoVersion)
	assert.Positive(t, rec.Runtime.PID)
	assert.True(t, rec.Config.Existed)

	ds := rec.Datastore
	assert.Equal(t, "sqlite", ds.Dialect)
	assert.True(t, ds.ConfiguredExists)
	assert.Equal(t, int64(4096), ds.ConfiguredSize)
	assert.False(t, ds.V2SidecarExists)
	assert.Equal(t, "v2_restart", ds.StartupDecision)
	assert.True(t, filepath.IsAbs(ds.ResolvedAbsPath))

	// db_files_found: birdnet.db, birdnet.db-wal, birdnet.db.<ts>.old
	require.Len(t, ds.DBFilesFound, 3)
	found := map[string]int64{}
	for _, f := range ds.DBFilesFound {
		found[filepath.Base(f.Path)] = f.Size
	}
	assert.Equal(t, int64(4096), found["birdnet.db"])
	assert.Equal(t, int64(512), found["birdnet.db-wal"])
	assert.Equal(t, int64(2048), found["birdnet.db.20260101-090000.old"])

	assert.NotEmpty(t, rec.DataDirFiles)
	assert.NotEmpty(t, rec.ConfigDirFiles)
	assert.NotEmpty(t, rec.Cwd)
	assert.NotEmpty(t, rec.Disk, "disk usage collected for data and config dirs")

	// The record was persisted and is readable back.
	got, ok := j.LastBoot()
	require.True(t, ok)
	assert.Equal(t, rec.App.Version, got.App.Version)
}

func TestRecordBootAppendsConfigDefaultedEvent(t *testing.T) {
	t.Parallel()
	dataDir, configDir := setupBootEnv(t)
	j := newTestJournal(t)
	p := testBootParams(dataDir, configDir)
	p.ConfigDefaulted = true
	p.ConfigExisted = false

	rec, _ := RecordBoot(j, p)
	require.NotNil(t, rec)
	assert.True(t, rec.Config.Defaulted)

	data, err := os.ReadFile(j.Path())
	require.NoError(t, err)
	assert.Contains(t, string(data), `"type":"config_defaulted"`)
}

func TestRecordBootEmitsAnomalyAgainstPreviousBoot(t *testing.T) {
	t.Parallel()
	dataDir, configDir := setupBootEnv(t)
	j := newTestJournal(t)

	// Previous boot: big DB present.
	prev := bootWith(func(r *BootRecord) {
		r.Datastore.ResolvedAbsPath = filepath.Join(dataDir, "birdnet.db")
	})
	require.NoError(t, j.Append(prev))

	// Current boot: DB gone, fresh install decision.
	require.NoError(t, os.Remove(filepath.Join(dataDir, "birdnet.db")))
	require.NoError(t, os.Remove(filepath.Join(dataDir, "birdnet.db-wal")))
	p := testBootParams(dataDir, configDir)
	p.StartupDecision = "fresh_install"
	rec, anomalies := RecordBoot(j, p)
	require.NotNil(t, rec)

	// Anomalies are both returned (for the caller's telemetry reporting)
	// and journaled.
	require.Len(t, anomalies, 1)
	assert.Equal(t, AnomalyDBLost, anomalies[0].Kind)

	data, err := os.ReadFile(j.Path())
	require.NoError(t, err)
	assert.Contains(t, string(data), `"kind":"db_lost"`)
}

func TestRecordBootNeverPanicsOnUnwritableJournal(t *testing.T) {
	t.Parallel()
	dataDir, configDir := setupBootEnv(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	j := NewJournal(filepath.Join(blocker, "sub", "journal.jsonl"))

	assert.NotPanics(t, func() {
		rec, _ := RecordBoot(j, testBootParams(dataDir, configDir))
		assert.NotNil(t, rec, "snapshot is still assembled and returned")
	})
}

func TestScanDBFilesSkipsMediaDirsAndHonorsCap(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "clips", "2026"), 0o750))
	// A .db inside clips must NOT be found (clips is skipped by design).
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "clips", "2026", "decoy.db"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "real.db"), []byte("x"), 0o600))

	files := scanDBFiles([]string{dataDir})
	require.Len(t, files, 1)
	assert.Equal(t, "real.db", filepath.Base(files[0].Path))

	// Cap: more matches than maxDBScanEntries yields exactly the cap.
	capDir := t.TempDir()
	for i := range maxDBScanEntries + 5 {
		require.NoError(t, os.WriteFile(filepath.Join(capDir, fmt.Sprintf("f%03d.db", i)), []byte("x"), 0o600))
	}
	assert.Len(t, scanDBFiles([]string{capDir}), maxDBScanEntries)
}

func TestScanDBFilesHonorsDepthCap(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	// A .db within the depth budget IS found.
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "shallow.db"), []byte("x"), 0o600))
	// A .db below a directory at maxDBScanDepth is NOT found (dir not descended).
	deep := filepath.Join(dataDir, "a", "b", "c", "d")
	require.NoError(t, os.MkdirAll(deep, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(deep, "toodeep.db"), []byte("x"), 0o600))

	files := scanDBFiles([]string{dataDir})
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, filepath.Base(f.Path))
	}
	assert.Contains(t, names, "shallow.db")
	assert.NotContains(t, names, "toodeep.db", "files below maxDBScanDepth dirs are skipped")
}

func TestRecordBootPopulatesV2SidecarWhenPresent(t *testing.T) {
	t.Parallel()
	dataDir, configDir := setupBootEnv(t)
	// Create the v2 sidecar so the "exists" branch is exercised.
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "birdnet_v2.db"), make([]byte, 8192), 0o600))
	j := newTestJournal(t)

	rec, _ := RecordBoot(j, testBootParams(dataDir, configDir))
	require.NotNil(t, rec)
	assert.True(t, rec.Datastore.V2SidecarExists)
	assert.Equal(t, int64(8192), rec.Datastore.V2SidecarSize)
}
