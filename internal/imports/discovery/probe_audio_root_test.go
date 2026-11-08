// internal/imports/discovery/probe_audio_root_test.go: tests for the bounded
// search of the BirdNET-Pi audio tree that holds the source clips.
package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Clip path components of the synthetic BirdNET-Pi audio tree used by the
// tests: the clips live in Extracted/By_Date/<date>/<common name>/<file>.
const (
	audioRootTestDate    = "2024-01-01"
	audioRootTestComName = "Great Tit"
)

// TestResolveAudioRoot covers the bounded search for the BirdNET-Pi audio
// tree. The database directory is only one of the candidates: BirdNET-Pi keeps
// its database in ~/BirdNET-Pi/scripts and writes its clips to ~/BirdSongs, so
// the audio root is not always next to birds.db.
//
// A candidate only counts when the tree is readable inside it. An "Extracted"
// link that leaves the candidate therefore does not make the candidate the
// audio root: the importer reads the clips through an os.Root sandbox, and a
// tree outside that sandbox is not readable.
func TestResolveAudioRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// dirs are the directories, relative to the home directory, that build
		// the synthetic tree.
		dirs []string
		// dbPath is the database path relative to the home directory.
		dbPath string
		// link is a symbolic link relative to the home directory, or "" when the
		// fixture has none.
		link string
		// linkTarget is the target of that link, relative to the directory that
		// holds the link.
		linkTarget string
		// want is the expected audio root relative to the home directory, or
		// "" when no audio tree is expected to be found.
		want string
	}{
		{
			name:   "clips in the database directory",
			dirs:   []string{"BirdNET-Pi/scripts/Extracted/By_Date/" + audioRootTestDate + "/" + audioRootTestComName},
			dbPath: "BirdNET-Pi/scripts/birds.db",
			want:   "BirdNET-Pi/scripts",
		},
		{
			name:   "BirdSongs in the database directory",
			dirs:   []string{"BirdNET-Pi/scripts/BirdSongs/Extracted/By_Date/" + audioRootTestDate + "/" + audioRootTestComName},
			dbPath: "BirdNET-Pi/scripts/birds.db",
			want:   "BirdNET-Pi/scripts/BirdSongs",
		},
		{
			name:   "BirdSongs in the parent of the database directory",
			dirs:   []string{"BirdNET-Pi/BirdSongs/Extracted/By_Date/" + audioRootTestDate + "/" + audioRootTestComName},
			dbPath: "BirdNET-Pi/scripts/birds.db",
			want:   "BirdNET-Pi/BirdSongs",
		},
		{
			name:   "BirdSongs next to the BirdNET-Pi installation",
			dirs:   []string{"BirdSongs/Extracted/By_Date/" + audioRootTestDate + "/" + audioRootTestComName},
			dbPath: "BirdNET-Pi/scripts/birds.db",
			want:   "BirdSongs",
		},
		{
			// The tree is one level above the last searched level, so the
			// bounded search does not reach it and the caller falls back to
			// the database directory.
			name:   "BirdSongs one level past the searched depth is not found",
			dirs:   []string{"BirdSongs/Extracted/By_Date/" + audioRootTestDate + "/" + audioRootTestComName},
			dbPath: "nested/BirdNET-Pi/scripts/birds.db",
			want:   "",
		},
		{
			name:   "Extracted next to the BirdNET-Pi installation",
			dirs:   []string{"Extracted/By_Date/" + audioRootTestDate + "/" + audioRootTestComName},
			dbPath: "BirdNET-Pi/scripts/birds.db",
			want:   ".",
		},
		{
			name:   "no audio tree anywhere",
			dirs:   []string{"BirdNET-Pi/scripts"},
			dbPath: "BirdNET-Pi/scripts/birds.db",
			want:   "",
		},
		{
			name:   "Extracted without By_Date is not an audio tree",
			dirs:   []string{"Extracted"},
			dbPath: "BirdNET-Pi/scripts/birds.db",
			want:   "",
		},
		{
			name:   "an Extracted link that leaves the candidate is not an audio tree",
			dirs:   []string{"BirdSongs/Extracted/By_Date/" + audioRootTestDate + "/" + audioRootTestComName},
			dbPath: "BirdNET-Pi/scripts/birds.db",
			link:   "BirdNET-Pi/scripts/Extracted",
			// The link leaves the database directory and points at the real
			// tree, so it looks like an audio tree to an unsandboxed lookup.
			linkTarget: "../../BirdSongs/Extracted",
			want:       "BirdSongs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			for _, dir := range tt.dirs {
				require.NoError(t, os.MkdirAll(filepath.Join(home, filepath.FromSlash(dir)), 0o755))
			}
			dbPath := filepath.Join(home, filepath.FromSlash(tt.dbPath))
			require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0o755))
			require.NoError(t, os.WriteFile(dbPath, []byte("birds.db"), 0o600))
			if tt.link != "" {
				linkPath := filepath.Join(home, filepath.FromSlash(tt.link))
				if err := os.Symlink(tt.linkTarget, linkPath); err != nil {
					t.Skipf("cannot create symlink: %v", err)
				}
			}

			got, found := ResolveAudioRoot(dbPath)
			if tt.want == "" {
				assert.False(t, found, "no audio tree is expected for %q", tt.name)
				assert.Empty(t, got, "no audio root is expected for %q", tt.name)
				return
			}
			require.True(t, found, "an audio tree is expected for %q", tt.name)
			assert.Equal(t, filepath.Join(home, filepath.FromSlash(tt.want)), got,
				"the audio root of %q", tt.name)
		})
	}
}

// TestResolveAudioRoot_SymlinkedBirdSongs verifies that a BirdSongs directory
// that is reached through a symlink is returned as the path that was found:
// it is neither resolved to its target nor copied somewhere else. The importer
// opens that path as an os.Root, so the root has to stay the directory that
// holds the clip tree.
func TestResolveAudioRoot_SymlinkedBirdSongs(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	outside := t.TempDir()
	clipDir := filepath.Join(outside, birdSongsDirName, extractedDirName, byDateDirName,
		audioRootTestDate, audioRootTestComName)
	require.NoError(t, os.MkdirAll(clipDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(clipDir, "great_tit.wav"), []byte("clip"), 0o644))

	// The control file sits next to the audio root, outside it. It exists, so
	// a refused lookup proves the sandbox refuses the path and not that the
	// file is missing.
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("secret"), 0o600))

	link := filepath.Join(home, birdSongsDirName)
	if err := os.Symlink(filepath.Join(outside, birdSongsDirName), link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	dbDir := filepath.Join(home, "BirdNET-Pi", "scripts")
	require.NoError(t, os.MkdirAll(dbDir, 0o755))
	dbPath := filepath.Join(dbDir, "birds.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("birds.db"), 0o600))

	got, found := ResolveAudioRoot(dbPath)
	require.True(t, found, "the symlinked audio tree must be found")
	assert.Equal(t, link, got, "the symlink path is the audio root: it is not resolved and not copied")

	// The root stays a sandbox: the clip tree is reachable inside it, while a
	// lookup that leaves the root is refused.
	root, err := os.OpenRoot(got)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, root.Close()) })

	_, err = root.Stat(filepath.Join(extractedDirName, byDateDirName))
	require.NoError(t, err, "the audio tree is reachable inside the root")

	// The control file exists next to the root, so the sandbox is what
	// refuses the lookup: the root is the BirdSongs directory, so ".." from
	// inside it is the directory that holds the control file.
	_, err = os.Stat(secret)
	require.NoError(t, err, "the control file exists outside the root")

	_, err = root.Stat(filepath.Join("..", "secret.txt"))
	require.Error(t, err, "os.Root must refuse a lookup that leaves the root")
}
