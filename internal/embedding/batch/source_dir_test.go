package batch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func touch(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
}

func TestDirectoryItems(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "blackbird", "a.wav"))
	touch(t, filepath.Join(dir, "blackbird", "b.flac"))
	touch(t, filepath.Join(dir, "robin", "c.mp3"))
	touch(t, filepath.Join(dir, "robin", "notes.txt")) // ignored
	touch(t, filepath.Join(dir, ".hidden", "d.wav"))   // ignored (hidden dir)

	items, err := DirectoryItems(dir)
	require.NoError(t, err)
	require.Len(t, items, 3)

	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.Key)
		assert.Empty(t, it.DetectionID)
		assert.True(t, filepath.IsAbs(it.Path))
	}
	assert.Equal(t, []string{
		filepath.Join("blackbird", "a.wav"),
		filepath.Join("blackbird", "b.flac"),
		filepath.Join("robin", "c.mp3"),
	}, keys, "keys must be sorted lexicographically")
}

func TestDirectoryItemsMissingDir(t *testing.T) {
	t.Parallel()
	_, err := DirectoryItems("/nonexistent-dir-xyz")
	require.Error(t, err)
}
