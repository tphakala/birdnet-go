package diagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMountInfoBasicMounts(t *testing.T) {
	t.Parallel()
	content := `22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw
615 22 8:2 /home/user/birdnet/data /data rw,relatime - ext4 /dev/sda2 rw
616 22 8:2 /home/user/birdnet/config /config rw,relatime - ext4 /dev/sda2 rw
617 22 0:25 / /proc rw,nosuid - proc proc rw`

	mounts := ParseMountInfo(content)
	require.Len(t, mounts, 2, "root and /proc are skipped")
	assert.Equal(t, "/home/user/birdnet/data", mounts[0].Source, "source stays RAW, never anonymized here")
	assert.Equal(t, "/data", mounts[0].Destination)
	assert.Equal(t, "ext4", mounts[0].FSType)
	assert.Equal(t, "/config", mounts[1].Destination)
}

func TestParseMountInfoEmptyAndMalformed(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ParseMountInfo(""))
	assert.Empty(t, ParseMountInfo("too few fields\n\n"))
}

func TestListDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "birdnet.db"), make([]byte, 1024), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "clips"), 0o750))

	entries, err := ListDirectory(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	byName := map[string]DirEntryInfo{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	assert.Equal(t, int64(1024), byName["birdnet.db"].Size)
	assert.False(t, byName["birdnet.db"].IsDir)
	assert.True(t, byName["clips"].IsDir)
	assert.False(t, byName["clips"].Modified.IsZero())
}

func TestListDirectoryTruncatesAtCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for i := range MaxDirEntries + 10 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d", i)), nil, 0o600))
	}
	entries, err := ListDirectory(dir)
	require.NoError(t, err)
	assert.Len(t, entries, MaxDirEntries)
}

func TestListDirectoryMissingDir(t *testing.T) {
	t.Parallel()
	_, err := ListDirectory(filepath.Join(t.TempDir(), "nope"))
	assert.Error(t, err)
}

func TestCollectRawDeployment(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "birdnet.db"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("y"), 0o600))

	raw := CollectRawDeployment(dataDir, configDir)
	require.NotNil(t, raw)
	assert.NotEmpty(t, raw.WorkingDirectory)
	require.Len(t, raw.DataDirFiles, 1)
	assert.Equal(t, "birdnet.db", raw.DataDirFiles[0].Name)
	require.Len(t, raw.ConfigDirFiles, 1)
	assert.Equal(t, "config.yaml", raw.ConfigDirFiles[0].Name)
	// Mounts are nil outside a container; either way no error entry for them.
	for _, e := range raw.CollectionErrors {
		assert.NotContains(t, e, "data directory")
		assert.NotContains(t, e, "config directory")
	}
}

func TestCollectRawDeploymentRecordsListingErrors(t *testing.T) {
	t.Parallel()
	raw := CollectRawDeployment(filepath.Join(t.TempDir(), "missing-data"), filepath.Join(t.TempDir(), "missing-config"))
	require.NotNil(t, raw)
	assert.Empty(t, raw.DataDirFiles)
	assert.Empty(t, raw.ConfigDirFiles)
	assert.Len(t, raw.CollectionErrors, 2)
}

func TestCollectRawDeploymentSkipsEmptyDirs(t *testing.T) {
	t.Parallel()
	// MySQL boots have no data directory: empty inputs must be skipped,
	// not recorded as permanent listing errors.
	raw := CollectRawDeployment("", "")
	require.NotNil(t, raw)
	assert.Empty(t, raw.DataDirFiles)
	assert.Empty(t, raw.ConfigDirFiles)
	for _, e := range raw.CollectionErrors {
		assert.NotContains(t, e, "data directory")
		assert.NotContains(t, e, "config directory")
	}
}
