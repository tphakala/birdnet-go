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

			// Every fixture lives inside home, so bounding the search by home
			// must give the same answer as the unbounded search.
			for _, bound := range []string{"", home} {
				got, found := ResolveAudioRoot(dbPath, bound)
				if tt.want == "" {
					assert.False(t, found, "no audio tree is expected for %q (bound %q)", tt.name, bound)
					assert.Empty(t, got, "no audio root is expected for %q (bound %q)", tt.name, bound)
					continue
				}
				require.True(t, found, "an audio tree is expected for %q (bound %q)", tt.name, bound)
				assert.Equal(t, filepath.Join(home, filepath.FromSlash(tt.want)), got,
					"the audio root of %q (bound %q)", tt.name, bound)
			}
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

	got, found := ResolveAudioRoot(dbPath, "")
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

// TestResolveAudioRoot_Bound verifies that a bounded search never selects an
// audio tree outside the bound: the import source root of a container install
// or the staging directory of a staged import. Without that bound a database
// at <root>/birds.db would make <root>/../BirdSongs a candidate, and a
// db-audio import could copy clips from outside the mount the user is allowed
// to import from.
func TestResolveAudioRoot_Bound(t *testing.T) {
	t.Parallel()

	mkTree := func(t *testing.T, dir string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, extractedDirName, byDateDirName,
			audioRootTestDate, audioRootTestComName), 0o755))
	}
	mkDB := func(t *testing.T, path string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("birds.db"), 0o600))
	}

	t.Run("a tree above the bound is not found", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		mount := filepath.Join(home, "mount")
		mkTree(t, filepath.Join(home, birdSongsDirName))
		dbPath := filepath.Join(mount, "birds.db")
		mkDB(t, dbPath)

		// The unbounded search climbs out of the mount and finds the tree,
		// which proves the fixture would be found without the bound.
		got, found := ResolveAudioRoot(dbPath, "")
		require.True(t, found, "the unbounded search reaches the tree above the mount")
		assert.Equal(t, filepath.Join(home, birdSongsDirName), got)

		got, found = ResolveAudioRoot(dbPath, mount)
		assert.False(t, found, "the bounded search must not leave the mount")
		assert.Empty(t, got)
	})

	t.Run("a BirdSongs link that leaves the bound is skipped", func(t *testing.T) {
		t.Parallel()
		mount := t.TempDir()
		outside := t.TempDir()
		mkTree(t, filepath.Join(outside, birdSongsDirName))
		if err := os.Symlink(filepath.Join(outside, birdSongsDirName),
			filepath.Join(mount, birdSongsDirName)); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}
		dbPath := filepath.Join(mount, "scripts", "birds.db")
		mkDB(t, dbPath)

		got, found := ResolveAudioRoot(dbPath, mount)
		assert.False(t, found, "a candidate that resolves outside the bound must be skipped")
		assert.Empty(t, got)
	})

	t.Run("a tree inside the bound is found through a symlinked bound", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		realDir := filepath.Join(base, "real")
		mkTree(t, filepath.Join(realDir, birdSongsDirName))
		dbPath := filepath.Join(realDir, "BirdNET-Pi", "scripts", "birds.db")
		mkDB(t, dbPath)
		boundLink := filepath.Join(base, "external")
		if err := os.Symlink(realDir, boundLink); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}

		got, found := ResolveAudioRoot(dbPath, boundLink)
		require.True(t, found, "the bound is compared after its symlinks are resolved")
		assert.Equal(t, filepath.Join(realDir, birdSongsDirName), got)
	})

	t.Run("a bound that does not exist finds nothing", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		mkTree(t, home)
		dbPath := filepath.Join(home, "birds.db")
		mkDB(t, dbPath)

		got, found := ResolveAudioRoot(dbPath, filepath.Join(home, "missing"))
		assert.False(t, found, "an unresolvable bound trusts no candidate")
		assert.Empty(t, got)
	})
}

// TestResolveAudioRoot_SkipsUnreadableTree verifies that a tree the service
// user cannot traverse is not selected: the search goes on to the next
// candidate, here the BirdSongs directory next to the installation.
func TestResolveAudioRoot_SkipsUnreadableTree(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not restrict root")
	}

	home := t.TempDir()
	dbDir := filepath.Join(home, "BirdNET-Pi", "scripts")
	locked := filepath.Join(dbDir, extractedDirName, byDateDirName)
	require.NoError(t, os.MkdirAll(filepath.Join(locked, audioRootTestDate), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, birdSongsDirName, extractedDirName, byDateDirName,
		audioRootTestDate, audioRootTestComName), 0o755))
	dbPath := filepath.Join(dbDir, "birds.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("birds.db"), 0o600))

	// Read but no search permission: the entries can be listed but not entered.
	require.NoError(t, os.Chmod(locked, 0o600))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	got, found := ResolveAudioRoot(dbPath, home)
	require.True(t, found, "the readable tree must be found")
	assert.Equal(t, filepath.Join(home, birdSongsDirName), got,
		"an untraversable tree next to the database must be skipped")
}

// TestResolveAudioRoot_SearchOnlyTreeIsRejected verifies that a By_Date
// directory the service user can enter but not list is rejected, and the
// search moves on to the next candidate. The importer opens clips through an
// os.Root, which opens every directory on the path and so needs read
// permission: in a search-only tree every clip read would fail.
func TestResolveAudioRoot_SearchOnlyTreeIsRejected(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not restrict root")
	}

	home := t.TempDir()
	dbDir := filepath.Join(home, "BirdNET-Pi", "scripts")
	byDate := filepath.Join(dbDir, extractedDirName, byDateDirName)
	require.NoError(t, os.MkdirAll(filepath.Join(byDate, audioRootTestDate, audioRootTestComName), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, birdSongsDirName, extractedDirName, byDateDirName,
		audioRootTestDate, audioRootTestComName), 0o755))
	dbPath := filepath.Join(dbDir, "birds.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("birds.db"), 0o600))

	// Search but no read permission: entries cannot be listed, only entered.
	require.NoError(t, os.Chmod(byDate, 0o311))
	t.Cleanup(func() { _ = os.Chmod(byDate, 0o755) })

	// Prove the premise: the importer's own os.Root cannot reach a path
	// inside the search-only directory.
	root, err := os.OpenRoot(dbDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	_, err = root.Stat(filepath.Join(extractedDirName, byDateDirName, audioRootTestDate))
	require.Error(t, err, "os.Root cannot traverse a search-only directory")

	got, found := ResolveAudioRoot(dbPath, home)
	require.True(t, found, "the next, readable tree must be found")
	assert.Equal(t, filepath.Join(home, birdSongsDirName), got,
		"a search-only tree the importer cannot read is skipped")
}

// TestResolveAudioRoot_BrokenEntryDoesNotRejectTree verifies that a dangling
// symlink inside By_Date does not make an otherwise usable tree look
// unreadable.
func TestResolveAudioRoot_BrokenEntryDoesNotRejectTree(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dbDir := filepath.Join(home, "BirdNET-Pi", "scripts")
	byDate := filepath.Join(dbDir, extractedDirName, byDateDirName)
	require.NoError(t, os.MkdirAll(filepath.Join(byDate, audioRootTestDate, audioRootTestComName), 0o755))
	// "0000" sorts before the date directory, so a check that looks at the
	// first entry would hit the broken link.
	if err := os.Symlink(filepath.Join(home, "missing"), filepath.Join(byDate, "0000")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	dbPath := filepath.Join(dbDir, "birds.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("birds.db"), 0o600))

	got, found := ResolveAudioRoot(dbPath, home)
	require.True(t, found, "a broken entry must not reject the tree")
	assert.Equal(t, dbDir, got)
}
