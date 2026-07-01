package audiotemp_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/errors"
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

// TestFinalizeWith_UsesInjectedRename verifies FinalizeWith routes the move
// through the caller-supplied rename function (the SecureFS rename the
// spectrogram generator injects) and returns its result unchanged on success.
func TestFinalizeWith_UsesInjectedRename(t *testing.T) {
	t.Parallel()
	var gotOld, gotNew string
	calls := 0
	rename := func(oldpath, newpath string) error {
		calls++
		gotOld, gotNew = oldpath, newpath
		return nil
	}

	require.NoError(t, audiotemp.FinalizeWith("clip.png.1.2.temp", "clip.png", rename))
	assert.Equal(t, 1, calls, "rename must be called exactly once on success")
	assert.Equal(t, "clip.png.1.2.temp", gotOld)
	assert.Equal(t, "clip.png", gotNew)
}

// TestFinalizeWith_PropagatesRenameError verifies a rename failure surfaces to
// the caller. On non-Windows the rename is attempted exactly once (no retry); the
// Windows retry path is covered by TestFinalize_ConcurrentSameTargetAllSucceed.
func TestFinalizeWith_PropagatesRenameError(t *testing.T) {
	t.Parallel()
	sentinel := errors.NewStd("rename failed")
	calls := 0
	rename := func(_, _ string) error {
		calls++
		return sentinel
	}

	err := audiotemp.FinalizeWith("a.temp", "a.png", rename)
	require.ErrorIs(t, err, sentinel)
	assert.GreaterOrEqual(t, calls, 1, "rename must be attempted at least once")
	if runtime.GOOS != "windows" {
		assert.Equal(t, 1, calls, "non-Windows must not retry a failed rename")
	}
}

func TestIsTempFor(t *testing.T) {
	t.Parallel()

	const base = "clip.wav"
	tests := []struct {
		name string
		file string
		want bool
	}{
		{"legacy fixed temp", "clip.wav" + audiotemp.Ext, true},
		{"process-unique temp", "clip.wav.1234.5" + audiotemp.Ext, true},
		{"final file is not a temp", "clip.wav", false},
		{"temp for a different clip", "other.wav" + audiotemp.Ext, false},
		{"non-integer pid", "clip.wav.abc.5" + audiotemp.Ext, false},
		{"missing seq segment", "clip.wav.1234" + audiotemp.Ext, false},
		{"unrelated suffix", "clip.wav.1234.5.bak", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, audiotemp.IsTempFor(tt.file, base))
		})
	}

	// The real UniquePath output must be recognized for its own base.
	unique := audiotemp.UniquePath("clip.wav")
	assert.True(t, audiotemp.IsTempFor(filepath.Base(unique), "clip.wav"),
		"UniquePath output must match IsTempFor for the same base")
}
