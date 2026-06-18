package audiotemp_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
)

func TestUniquePath_PrefixedByOutputAndEndsInExt(t *testing.T) {
	t.Parallel()
	out := filepath.Join("clips", "2026", "05", "turdus_merula_84p_20260531T084138Z.flac")
	p := audiotemp.UniquePath(out)
	assert.True(t, strings.HasPrefix(p, out+"."), "temp path must start with the output path plus a dot: %s", p)
	assert.True(t, strings.HasSuffix(p, audiotemp.Ext), "temp path must end in Ext: %s", p)
}

// TestUniquePath_DistinctAcrossConcurrentCalls is the core guarantee: concurrent
// exports targeting the same output path must each get a different temp path.
func TestUniquePath_DistinctAcrossConcurrentCalls(t *testing.T) {
	t.Parallel()
	const n = 256
	const out = "clip.flac"
	var wg sync.WaitGroup
	paths := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i] = audiotemp.UniquePath(out)
		}(i)
	}
	wg.Wait()

	seen := make(map[string]struct{}, n)
	for _, p := range paths {
		_, dup := seen[p]
		require.Falsef(t, dup, "duplicate temp path generated: %s", p)
		seen[p] = struct{}{}
	}
	assert.Len(t, seen, n)
}

func TestFinalize_RenamesTempToFinal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	final := filepath.Join(dir, "clip.wav")
	temp := audiotemp.UniquePath(final)
	require.NoError(t, os.WriteFile(temp, []byte("payload"), 0o600))

	require.NoError(t, audiotemp.Finalize(temp, final))

	got, err := os.ReadFile(final)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(got))
	assert.NoFileExists(t, temp, "temp must be gone after a successful rename")
}

// TestFinalize_MissingTempErrorsAndLeavesNoFinal pins the atomic guarantee: when
// the rename fails (here, the temp does not exist), Finalize returns an error and
// does not create the final clip.
func TestFinalize_MissingTempErrorsAndLeavesNoFinal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	final := filepath.Join(dir, "clip.wav")
	missing := audiotemp.UniquePath(final) // never created

	err := audiotemp.Finalize(missing, final)
	require.Error(t, err, "renaming a nonexistent temp must fail")
	assert.NoFileExists(t, final, "a failed finalize must not create the final clip")
}

// TestFinalize_ConcurrentSameTargetAllSucceed verifies that many exports
// finalizing onto the same path all succeed (last writer wins) and leave no temp
// behind, on every platform (the Windows path additionally exercises the retry).
func TestFinalize_ConcurrentSameTargetAllSucceed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	final := filepath.Join(dir, "clip.wav")
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			temp := audiotemp.UniquePath(final)
			if writeErr := os.WriteFile(temp, []byte{byte(i)}, 0o600); writeErr != nil {
				errs[i] = writeErr
				return
			}
			errs[i] = audiotemp.Finalize(temp, final)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "finalize %d must not fail", i)
	}
	require.FileExists(t, final, "exactly one final clip must remain")
	leftover, err := filepath.Glob(filepath.Join(dir, "*"+audiotemp.Ext))
	require.NoError(t, err)
	assert.Empty(t, leftover, "no temp files should remain after concurrent finalize")
}
