package diskmanager

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// skipIfDirPermissionsNotEnforced skips a test that relies on chmod 0 making a
// directory unreadable: root ignores directory permissions, and on Windows chmod
// only toggles the read-only attribute and never blocks enumeration.
func skipIfDirPermissionsNotEnforced(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions are not enforced here")
	}
}

// writeFileIn creates baseDir/rel with the given content and parent directories.
func writeFileIn(t *testing.T, baseDir, rel, content string) string {
	t.Helper()
	full := filepath.Join(baseDir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	return full
}

func TestReleaseDeletedClipPaths_RetainsOnlyClipsWithKeptRender(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	// a.wav and b.wav are already deleted; only a has a surviving render.
	writeFileIn(t, baseDir, "2026/01/a_514px.png", "png")
	deleted := []string{
		filepath.Join(baseDir, "2026", "01", "a.wav"),
		filepath.Join(baseDir, "2026", "01", "b.wav"),
	}
	db := &MockDB{}

	releaseDeletedClipPaths(db, deleted, baseDir, "age", true)

	assert.Equal(t, [][]string{{"2026/01/a.wav"}}, db.retained)
	assert.Equal(t, [][]string{{"2026/01/b.wav"}}, db.cleared)
}

func TestReleaseDeletedClipPaths_ZeroByteAndDirectoryRendersDoNotCount(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	writeFileIn(t, baseDir, "2026/01/a_514px.png", "")
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "2026", "01", "b_514px.png"), 0o750))
	deleted := []string{
		filepath.Join(baseDir, "2026", "01", "a.wav"),
		filepath.Join(baseDir, "2026", "01", "b.wav"),
	}
	db := &MockDB{}

	releaseDeletedClipPaths(db, deleted, baseDir, "usage", true)

	assert.Empty(t, db.retained)
	assert.Equal(t, [][]string{{"2026/01/a.wav", "2026/01/b.wav"}}, db.cleared)
}

func TestReleaseDeletedClipPaths_KeepOffClearsAll(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	writeFileIn(t, baseDir, "2026/01/a_514px.png", "png")
	deleted := []string{filepath.Join(baseDir, "2026", "01", "a.wav")}
	db := &MockDB{}

	releaseDeletedClipPaths(db, deleted, baseDir, "age", false)

	assert.Empty(t, db.retained, "retain must never be called when keepSpectrograms is off")
	assert.Equal(t, [][]string{{"2026/01/a.wav"}}, db.cleared)
}

func TestReleaseDeletedClipPaths_RetainErrorLeavesRowsUntouched(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	writeFileIn(t, baseDir, "2026/01/a_514px.png", "png")
	deleted := []string{
		filepath.Join(baseDir, "2026", "01", "a.wav"),
		filepath.Join(baseDir, "2026", "01", "b.wav"), // no render: still cleared
	}
	db := &MockDB{retainErr: errors.NewStd("db down")}

	releaseDeletedClipPaths(db, deleted, baseDir, "age", true)

	assert.Equal(t, [][]string{{"2026/01/a.wav"}}, db.retained)
	assert.Equal(t, [][]string{{"2026/01/b.wav"}}, db.cleared,
		"a row whose retain failed keeps clip_name so the reconcile pass can decide; only clips without a render are cleared")
}

func TestReleaseDeletedClipPaths_UnreadableDirectoryLeavesRowUntouched(t *testing.T) {
	t.Parallel()

	skipIfDirPermissionsNotEnforced(t)
	baseDir := t.TempDir()
	writeFileIn(t, baseDir, "2026/01/a_514px.png", "png")
	dir := filepath.Join(baseDir, "2026", "01")
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
	deleted := []string{filepath.Join(dir, "a.wav")}
	db := &MockDB{}

	releaseDeletedClipPaths(db, deleted, baseDir, "age", true)

	assert.Empty(t, db.retained)
	assert.Empty(t, db.cleared, "a listing that failed is not evidence that no render exists")
}

// errEntry is a directory entry that cannot be inspected.
type errEntry struct{ name string }

func (e errEntry) Name() string               { return e.name }
func (e errEntry) IsDir() bool                { return false }
func (e errEntry) Type() os.FileMode          { return 0 }
func (e errEntry) Info() (os.FileInfo, error) { return nil, errors.NewStd("stat failed") }

func TestHasKeptSpectrogram_UninspectableEntryIsIndeterminate(t *testing.T) {
	t.Parallel()

	audioPath := filepath.Join(t.TempDir(), "a.wav")
	dirCache := map[string][]os.DirEntry{filepath.Dir(audioPath): {errEntry{name: "a_514px.png"}}}

	assert.Equal(t, renderIndeterminate, hasKeptSpectrogram(audioPath, dirCache))
}

func TestHasKeptSpectrogram_FailedListingIsNotCached(t *testing.T) {
	t.Parallel()

	skipIfDirPermissionsNotEnforced(t)
	baseDir := t.TempDir()
	writeFileIn(t, baseDir, "2026/01/a_514px.png", "png")
	dir := filepath.Join(baseDir, "2026", "01")
	audioPath := filepath.Join(dir, "a.wav")
	dirCache := map[string][]os.DirEntry{}

	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
	assert.Equal(t, renderIndeterminate, hasKeptSpectrogram(audioPath, dirCache))
	assert.NotContains(t, dirCache, dir, "a failed listing must not be remembered as an empty directory")

	require.NoError(t, os.Chmod(dir, 0o750))
	assert.Equal(t, renderPresent, hasKeptSpectrogram(audioPath, dirCache), "the next clip in the directory gets a fresh listing")
}

func TestHasKeptSpectrogram_MissingDirectoryHasNoRender(t *testing.T) {
	t.Parallel()

	audioPath := filepath.Join(t.TempDir(), "missing", "a.wav")

	assert.Equal(t, renderAbsent, hasKeptSpectrogram(audioPath, map[string][]os.DirEntry{}))
}

// ageRun walks baseDir, runs the age deletion loop with keep=true and releases the
// deleted clips, returning what this single run handed to the database.
func ageRun(t *testing.T, baseDir string, db *MockDB) {
	t.Helper()
	files, err := GetAudioFiles(baseDir, allowedFileTypes, db)
	require.NoError(t, err)
	cutoff := time.Now().Add(-time.Hour).Unix()
	run, _ := newTestRun(t)
	_, deleted, _, loopErr := processAgeBasedDeletionLoop(files, buildSpeciesTotalCountMap(files),
		0, true, run, cutoff)
	require.NoError(t, loopErr)
	releaseDeletedClipPaths(db, deleted, baseDir, "age", true)
}

func TestAgeBasedCleanup_RetainTransitions(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	const (
		nameA = "bubo_bubo_80p_20200101T000000Z"
		nameB = "bubo_bubo_81p_20200102T000000Z"
		nameC = "bubo_bubo_82p_20200103T000000Z"
	)
	writeFileIn(t, baseDir, "2020/01/"+nameA+".wav", "audio")
	writeFileIn(t, baseDir, "2020/01/"+nameB+".wav", "audio")
	writeFileIn(t, baseDir, "2020/01/"+nameA+"_514px.png", "png")

	// Run 1: a is retained, b is cleared.
	db := &MockDB{}
	ageRun(t, baseDir, db)
	assert.Equal(t, [][]string{{"2020/01/" + nameA + ".wav"}}, db.retained)
	assert.Equal(t, [][]string{{"2020/01/" + nameB + ".wav"}}, db.cleared)

	// Run 2: nothing left to process.
	db = &MockDB{}
	ageRun(t, baseDir, db)
	assert.Empty(t, db.retained)
	assert.Empty(t, db.cleared)

	// Run 3: a new clip with a legend render is retained; a is untouched.
	writeFileIn(t, baseDir, "2020/01/"+nameC+".wav", "audio")
	writeFileIn(t, baseDir, "2020/01/"+nameC+"_1026px-legend.png", "png")
	db = &MockDB{}
	ageRun(t, baseDir, db)
	assert.Equal(t, [][]string{{"2020/01/" + nameC + ".wav"}}, db.retained)
	assert.Empty(t, db.cleared)

	// Run 4: a's render is deleted by hand; there is no audio left, so no change.
	require.NoError(t, os.Remove(filepath.Join(baseDir, "2020", "01", nameA+"_514px.png")))
	db = &MockDB{}
	ageRun(t, baseDir, db)
	assert.Empty(t, db.retained)
	assert.Empty(t, db.cleared)
}

func TestEvaluateClipChunk_OrphanWithRenderIsRetained(t *testing.T) {
	t.Parallel()

	now := time.Now()
	baseDir := t.TempDir()
	writeFileIn(t, baseDir, "2026/01/a_514px.png", "png")
	writeFileIn(t, baseDir, "2026/01/zero_514px.png", "")
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "2026", "01", "dir_514px.png"), 0o750))
	writeFileIn(t, baseDir, "2026/01/present.wav", "audio")

	refs := []ClipReference{
		{ID: 1, ClipName: "2026/01/a.wav", CompletionTime: now.Add(testOld)},
		{ID: 2, ClipName: "2026/01/zero.wav", CompletionTime: now.Add(testOld)},
		{ID: 3, ClipName: "2026/01/dir.wav", CompletionTime: now.Add(testOld)},
		{ID: 4, ClipName: "2026/01/none.wav", CompletionTime: now.Add(testOld)},
	}
	root, err := os.OpenRoot(baseDir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()

	res := evaluateClipChunk(root, refs, now)

	assert.Equal(t, []string{"2026/01/a.wav"}, res.retained)
	assert.Equal(t, []string{"2026/01/zero.wav", "2026/01/dir.wav", "2026/01/none.wav"}, res.orphans)
	assert.Equal(t, 1, res.positiveCount, "a render on disk counts as storage evidence")
}

func TestEvaluateClipChunk_OnlyRetainedRowsDoNotTripDetachedGuard(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	writeFileIn(t, baseDir, "2026/01/a_514px.png", "png")
	// The ghost is a true orphan candidate: without the retained row's evidence the
	// chunk has candidates and no positive evidence, and the guard would abort.
	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "2026/01/a.wav", CompletionTime: time.Now().Add(testOld)},
		{ID: 2, ClipName: "2026/01/ghost.wav", CompletionTime: time.Now().Add(testOld)},
	}}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir)

	assert.False(t, result.Aborted, "a surviving render is evidence that storage is attached")
	assert.Equal(t, int64(1), result.Retained)
	assert.Equal(t, []string{"2026/01/a.wav"}, store.retained)
	assert.Equal(t, []string{"2026/01/ghost.wav"}, store.cleared)
}

func TestReconcileClipOrphansPass_RetainsThenStable(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	writeClip(t, baseDir, "2026/01/present.wav")
	writeFileIn(t, baseDir, "2026/01/a_514px.png", "png")
	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "2026/01/present.wav", CompletionTime: time.Now().Add(testOld)},
		{ID: 2, ClipName: "2026/01/a.wav", CompletionTime: time.Now().Add(testOld)},
		{ID: 3, ClipName: "2026/01/ghost.wav", CompletionTime: time.Now().Add(testOld)},
	}}

	first := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir)
	assert.False(t, first.Aborted)
	assert.Equal(t, int64(1), first.Retained)
	assert.Equal(t, int64(1), first.Cleared)
	assert.Equal(t, []string{"2026/01/a.wav"}, store.retained)
	assert.Equal(t, []string{"2026/01/ghost.wav"}, store.cleared)

	second := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir)
	assert.False(t, second.Aborted)
	assert.Equal(t, int64(0), second.Retained)
	assert.Equal(t, []string{"2026/01/a.wav"}, store.retained, "second pass must not retain again")
}

func TestReconcileClipOrphansPass_RetainErrorLeavesRowsUntouched(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	writeClip(t, baseDir, "2026/01/present.wav")
	writeFileIn(t, baseDir, "2026/01/a_514px.png", "png")
	store := &fakeReconcileStore{
		retainErr: errors.NewStd("db down"),
		refs: []ClipReference{
			{ID: 1, ClipName: "2026/01/present.wav", CompletionTime: time.Now().Add(testOld)},
			{ID: 2, ClipName: "2026/01/a.wav", CompletionTime: time.Now().Add(testOld)},
		},
	}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir)

	assert.Equal(t, int64(0), result.Retained)
	assert.Empty(t, store.retained)
	assert.Empty(t, store.cleared, "rows that failed to retain must not be cleared")
}

// failingClearDB is a MockDB whose clear update fails.
type failingClearDB struct {
	MockDB
}

func (f *failingClearDB) ClearNoteClipPathsByNames(clipNames []string) (int64, error) {
	_, _ = f.MockDB.ClearNoteClipPathsByNames(clipNames)
	return 0, errors.NewStd("db down")
}

func TestReleaseDeletedClipPaths_ClearErrorDoesNotPanicOrRetain(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	// The directory of the deleted clip does not exist: a missing directory counts as "no render"
	// (only fs.ErrNotExist is absent; other read errors are indeterminate).
	deleted := []string{filepath.Join(baseDir, "missing", "a.wav")}
	db := &failingClearDB{}

	releaseDeletedClipPaths(db, deleted, baseDir, "usage", true)

	assert.Empty(t, db.retained)
	assert.Equal(t, [][]string{{"missing/a.wav"}}, db.cleared)
}

// runRealCleanup publishes retention settings, writes two expired clips (one with a
// kept render, one without) and runs the real policy entry point, so the keep flag
// is read from the settings snapshot rather than passed by the test. It returns
// the database mock and the run result. Not parallel-safe: it replaces the global
// settings snapshot.
func runRealCleanup(t *testing.T, policy string, keep bool, run func(<-chan struct{}, Interface) CleanupResult) (*MockDB, CleanupResult) {
	t.Helper()
	baseDir := t.TempDir()
	settings := conftest.NewTestSettings().
		WithAudioExport(baseDir, "wav", "96k").
		Apply()
	retention := &settings.Realtime.Audio.Export.Retention
	retention.Policy = policy
	retention.MaxAge = "1d"
	retention.MaxUsage = "0%" // utilization is never below 0%, so the usage policy always proceeds
	retention.MinClips = 0
	retention.KeepSpectrograms = keep
	t.Cleanup(func() { conftest.NewTestSettings().Apply() })

	const (
		withRender    = "bubo_bubo_80p_20200101T000000Z"
		withoutRender = "bubo_bubo_81p_20200102T000000Z"
	)
	writeFileIn(t, baseDir, "2020/01/"+withRender+".wav", "audio")
	writeFileIn(t, baseDir, "2020/01/"+withoutRender+".wav", "audio")
	writeFileIn(t, baseDir, "2020/01/"+withRender+"_514px.png", "png")

	db := &MockDB{}
	result := run(make(chan struct{}), db)
	require.NoError(t, result.Err)
	require.Equal(t, 2, result.ClipsRemoved, "the run must reach deletion for the keep flag to matter")
	return db, result
}

func TestRetentionPolicies_ReadKeepSpectrogramsFromSettings(t *testing.T) {
	policies := []struct {
		name string
		run  func(<-chan struct{}, Interface) CleanupResult
	}{
		{"age", AgeBasedCleanup},
		{"usage", UsageBasedCleanup},
	}
	for _, p := range policies {
		t.Run(p.name+" keep on retains the clip with a render", func(t *testing.T) {
			db, _ := runRealCleanup(t, p.name, true, p.run)

			assert.Equal(t, [][]string{{"2020/01/bubo_bubo_80p_20200101T000000Z.wav"}}, db.retained)
			assert.Equal(t, [][]string{{"2020/01/bubo_bubo_81p_20200102T000000Z.wav"}}, db.cleared)
		})
		t.Run(p.name+" keep off clears every clip and never retains", func(t *testing.T) {
			db, _ := runRealCleanup(t, p.name, false, p.run)

			assert.Empty(t, db.retained)
			require.Len(t, db.cleared, 1)
			assert.ElementsMatch(t, []string{
				"2020/01/bubo_bubo_80p_20200101T000000Z.wav",
				"2020/01/bubo_bubo_81p_20200102T000000Z.wav",
			}, db.cleared[0])
		})
	}
}

// symlinkRender creates baseDir/rel as a symlink to a real non-empty file elsewhere.
func symlinkRender(t *testing.T, baseDir, rel string) {
	t.Helper()
	target := writeFileIn(t, t.TempDir(), "target.png", "png")
	link := filepath.Join(baseDir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o750))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestReleaseDeletedClipPaths_SymlinkNamedLikeARenderIsNeverRetained(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	symlinkRender(t, baseDir, "2026/01/a_514px.png")
	deleted := []string{filepath.Join(baseDir, "2026", "01", "a.wav")}
	db := &MockDB{}

	releaseDeletedClipPaths(db, deleted, baseDir, "age", true)

	assert.Empty(t, db.retained, "a symlink is not a kept render")
	assert.Equal(t, [][]string{{"2026/01/a.wav"}}, db.cleared)
}

func TestReconcileClipOrphansPass_SymlinkNamedLikeARenderIsNotRelinked(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	writeClip(t, baseDir, "2026/01/present.wav")
	symlinkRender(t, baseDir, "2026/01/a_514px.png")
	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "2026/01/present.wav", CompletionTime: time.Now().Add(testOld)},
		{ID: 2, ClipName: "2026/01/a.wav", CompletionTime: time.Now().Add(testOld)},
	}}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir)

	assert.Equal(t, int64(0), result.Retained)
	assert.Empty(t, store.retained)
	assert.Equal(t, []string{"2026/01/a.wav"}, store.cleared)
}
