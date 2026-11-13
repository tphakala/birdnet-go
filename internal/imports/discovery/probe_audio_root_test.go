// internal/imports/discovery/probe_audio_root_test.go: tests for the bounded
// search of the BirdNET-Pi audio tree that holds the source clips.
package discovery

import (
	"cmp"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// audioRootTestTree is a non-empty BirdNET-Pi clip tree: one date directory
	// inside Extracted/By_Date is enough to make By_Date hold an entry.
	audioRootTestTree = "Extracted/By_Date/2024-01-01"
	// audioRootTestDB is the database path of a BirdNET-Pi installation,
	// relative to the temporary base directory.
	audioRootTestDB = "home/BirdNET-Pi/scripts/birds.db"
)

// TestResolveAudioRoot covers the search for the BirdNET-Pi audio tree.
// BirdNET-Pi keeps its database in ~/BirdNET-Pi/scripts and writes its clips to
// ~/BirdSongs, so the audio root is not always next to birds.db. All paths in
// the table are relative to a temporary base directory.
func TestResolveAudioRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// dirs are the directories that build the synthetic tree.
		dirs []string
		// link and linkTarget describe one symbolic link, "" for none.
		link       string
		linkTarget string
		// db is the database path, audioRootTestDB when empty.
		db string
		// bound is the directory the search may not leave, "" for unbounded.
		bound string
		// want is the expected audio root, "" when none is expected.
		want string
	}{
		{
			name: "real BirdNET-Pi layout",
			dirs: []string{"home/BirdSongs/" + audioRootTestTree},
			want: "home/BirdSongs",
		},
		{
			name: "nearest tree wins",
			dirs: []string{"home/BirdNET-Pi/scripts/" + audioRootTestTree, "home/BirdSongs/" + audioRootTestTree},
			want: "home/BirdNET-Pi/scripts",
		},
		{
			name: "tree past the third level is not found",
			dirs: []string{"home/BirdSongs/" + audioRootTestTree},
			db:   "home/nested/BirdNET-Pi/scripts/birds.db",
		},
		{
			name: "empty By_Date does not win",
			dirs: []string{"home/BirdNET-Pi/scripts/Extracted/By_Date", "home/BirdSongs/" + audioRootTestTree},
			want: "home/BirdSongs",
		},
		{
			name:       "Extracted link leaving the candidate does not win",
			dirs:       []string{"home/BirdSongs/" + audioRootTestTree},
			link:       "home/BirdNET-Pi/scripts/Extracted",
			linkTarget: "home/BirdSongs/Extracted",
			want:       "home/BirdSongs",
		},
		{
			name:       "symlinked BirdSongs is returned unresolved",
			dirs:       []string{"home-other/BirdSongs/" + audioRootTestTree},
			link:       "home/BirdSongs",
			linkTarget: "home-other/BirdSongs",
			want:       "home/BirdSongs",
		},
		{
			name:       "BirdSongs link leaving a bound that is a string prefix of the target",
			dirs:       []string{"home-other/BirdSongs/" + audioRootTestTree},
			link:       "home/BirdSongs",
			linkTarget: "home-other/BirdSongs",
			bound:      "home",
		},
		{
			name:  "tree in the parent of the bound is not found",
			dirs:  []string{"home/BirdSongs/" + audioRootTestTree},
			bound: "home/BirdNET-Pi",
		},
		{
			name:       "symlinked bound",
			dirs:       []string{"home/BirdSongs/" + audioRootTestTree},
			link:       "mnt",
			linkTarget: "home",
			bound:      "mnt",
			want:       "home/BirdSongs",
		},
		{
			name:  "unresolvable bound is not treated as unbounded",
			dirs:  []string{"home/BirdNET-Pi/scripts/" + audioRootTestTree},
			bound: "missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			for _, dir := range tt.dirs {
				require.NoError(t, os.MkdirAll(filepath.Join(base, dir), 0o755))
			}
			db := filepath.Join(base, cmp.Or(tt.db, audioRootTestDB))
			require.NoError(t, os.MkdirAll(filepath.Dir(db), 0o755))
			require.NoError(t, os.WriteFile(db, nil, 0o600))
			if tt.link != "" {
				linkPath := filepath.Join(base, tt.link)
				require.NoError(t, os.MkdirAll(filepath.Dir(linkPath), 0o755))
				if err := os.Symlink(filepath.Join(base, tt.linkTarget), linkPath); err != nil {
					t.Skipf("symlinks are not available: %v", err)
				}
			}

			bound := ""
			if tt.bound != "" {
				bound = filepath.Join(base, tt.bound)
			}
			got, found := ResolveAudioRoot(db, bound)

			if tt.want == "" {
				assert.False(t, found)
				assert.Empty(t, got)
				return
			}
			assert.True(t, found)
			assert.Equal(t, filepath.Join(base, tt.want), got)
		})
	}
}
