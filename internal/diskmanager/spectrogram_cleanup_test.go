package diskmanager

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/spectrogram/specfile"
)

const (
	cleanupClipA = "bubo_bubo_80p_20200101T000000Z"
	cleanupClipB = "bubo_bubo_81p_20200102T000000Z"
)

// cleanupFixture lays out the month directory used by the keep on/off tests and
// returns the base directory, the paths that belong to the two expired clips,
// and the paths that must survive a cleanup of those clips.
func cleanupFixture(t *testing.T, baseDir string) (removable, survivors []string) {
	t.Helper()
	const month = "2020/01/"
	removable = []string{
		month + cleanupClipA + ".png",
		month + cleanupClipA + "_514px.png",
		month + cleanupClipA + "_514px-raw.png",
		month + cleanupClipA + "_1026px-scientific_dark-legend.png",
		month + cleanupClipA + "_2050px.png", // empty: an interrupted render
		month + cleanupClipB + "_514px.png",
	}
	for _, rel := range removable {
		content := "png"
		if rel == month+cleanupClipA+"_2050px.png" {
			content = ""
		}
		writeFileIn(t, baseDir, rel, content)
	}
	writeFileIn(t, baseDir, month+cleanupClipA+".wav", "audio")
	writeFileIn(t, baseDir, month+cleanupClipB+".wav", "audio")

	survivors = make([]string, 0, 6)
	survivors = append(survivors,
		month+cleanupClipA+"_15s_514px.png",
		month+cleanupClipA+"2_514px.png",
		month+"bubo_bubo_99p_20200105T000000Z_514px.png",
		month+cleanupClipA+".png.123.4.temp",
	)
	for _, rel := range survivors {
		writeFileIn(t, baseDir, rel, "png")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "2020", "01", cleanupClipA+"_4100px.png"), 0o750))
	symlinkRender(t, baseDir, month+cleanupClipA+"_1026px.png")
	survivors = append(survivors, month+cleanupClipA+"_4100px.png", month+cleanupClipA+"_1026px.png")
	return removable, survivors
}

// runCleanupFixture publishes retention settings, builds the fixture and runs
// the real policy entry point.
func runCleanupFixture(t *testing.T, policy string, keep bool, run func(<-chan struct{}, Interface) CleanupResult) (db *MockDB, baseDir string, removable, survivors []string) {
	t.Helper()
	baseDir = t.TempDir()
	settings := conftest.NewTestSettings().
		WithAudioExport(baseDir, "wav", "96k").
		Apply()
	retention := &settings.Realtime.Audio.Export.Retention
	retention.Policy = policy
	retention.MaxAge = "1d"
	retention.MaxUsage = "0%"
	retention.MinClips = 0
	retention.KeepSpectrograms = keep
	t.Cleanup(func() { conftest.NewTestSettings().Apply() })

	removable, survivors = cleanupFixture(t, baseDir)
	db = &MockDB{}
	result := run(make(chan struct{}), db)
	require.NoError(t, result.Err)
	require.Equal(t, 2, result.ClipsRemoved)
	return db, baseDir, removable, survivors
}

var cleanupPolicies = []struct {
	name string
	run  func(<-chan struct{}, Interface) CleanupResult
}{
	{"age", AgeBasedCleanup},
	{"usage", UsageBasedCleanup},
}

func TestRetentionPolicies_KeepOffDeletesEveryRenderOfDeletedClips(t *testing.T) {
	for _, p := range cleanupPolicies {
		t.Run(p.name, func(t *testing.T) {
			_, baseDir, removable, survivors := runCleanupFixture(t, p.name, false, p.run)

			assert.NoFileExists(t, filepath.Join(baseDir, "2020", "01", cleanupClipA+".wav"))
			assert.NoFileExists(t, filepath.Join(baseDir, "2020", "01", cleanupClipB+".wav"))
			for _, rel := range removable {
				assert.NoFileExists(t, filepath.Join(baseDir, filepath.FromSlash(rel)), "render should be removed")
			}
			for _, rel := range survivors {
				_, err := os.Lstat(filepath.Join(baseDir, filepath.FromSlash(rel)))
				require.NoError(t, err, "%s should survive", rel)
			}
		})
	}
}

func TestRetentionPolicies_KeepOnDeletesNoRender(t *testing.T) {
	for _, p := range cleanupPolicies {
		t.Run(p.name, func(t *testing.T) {
			db, baseDir, removable, survivors := runCleanupFixture(t, p.name, true, p.run)

			for _, rel := range append(removable, survivors...) {
				_, err := os.Lstat(filepath.Join(baseDir, filepath.FromSlash(rel)))
				require.NoError(t, err, "%s should be kept", rel)
			}
			require.Len(t, db.retained, 1)
			assert.ElementsMatch(t, []string{
				"2020/01/" + cleanupClipA + ".wav",
				"2020/01/" + cleanupClipB + ".wav",
			}, db.retained[0])
		})
	}
}

// expiredClip writes dir/name.wav and returns its FileInfo, old enough for age cleanup.
func expiredClip(t *testing.T, dir, name string) FileInfo {
	t.Helper()
	path := filepath.Join(dir, name+".wav")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(path, []byte("audio"), 0o600))
	return FileInfo{Path: path, Species: "sp_" + name, Timestamp: time.Now().Add(-500 * time.Hour), Size: 5}
}

// countingReadDir wraps os.ReadDir, counting calls per directory and running
// after (when set) with the directory and its 1-based call number.
func countingReadDir(counts map[string]int, after func(dir string, call int)) func(string) ([]os.DirEntry, error) {
	return func(dir string) ([]os.DirEntry, error) {
		entries, err := os.ReadDir(dir)
		counts[dir]++
		if after != nil {
			after(dir, counts[dir])
		}
		return entries, err
	}
}

// runAgeLoop runs the age deletion loop over files with keep off and no minimum.
func runAgeLoop(t *testing.T, files []FileInfo, run *deletionRun) cleanupStats {
	t.Helper()
	cutoff := time.Now().Add(-168 * time.Hour).Unix()
	_, _, stats, err := processAgeBasedDeletionLoop(files, buildSpeciesTotalCountMap(files), 0, false, run, cutoff)
	require.NoError(t, err)
	return stats
}

func TestDeletionRun_ListsEachDirectoryOncePerBatch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir1, dir2 := filepath.Join(root, "d1"), filepath.Join(root, "d2")
	files := []FileInfo{
		expiredClip(t, dir1, "c1"), expiredClip(t, dir1, "c2"),
		expiredClip(t, dir1, "c3"), expiredClip(t, dir2, "c4"),
	}
	run, _ := newTestRun(t)
	counts := map[string]int{}
	run.renderDirs.readDir = countingReadDir(counts, nil)

	runAgeLoop(t, files, run)

	// Batch 1 (c1, c2) shares one listing; batch 2 lists dir1 again for c3 and dir2 for c4.
	assert.Equal(t, map[string]int{dir1: 2, dir2: 1}, counts)
}

func TestDeletionRun_NewBatchSeesRendersCreatedAfterPreviousListing(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "d1")
	files := []FileInfo{expiredClip(t, dir, "c1"), expiredClip(t, dir, "c2"), expiredClip(t, dir, "c3")}
	late := filepath.Join(dir, "c3_514px.png")
	run, _ := newTestRun(t)
	run.renderDirs.readDir = countingReadDir(map[string]int{}, func(d string, call int) {
		if call == 1 {
			require.NoError(t, os.WriteFile(late, []byte("png"), 0o600))
		}
	})

	runAgeLoop(t, files, run)

	assert.NoFileExists(t, late, "the second batch lists the directory again")
}

func TestReleaseBatch_ResetsRenderListingsEvenWhenBatchIsEmpty(t *testing.T) {
	t.Parallel()

	run, _ := newTestRun(t)
	_, err := run.renderDirs.list(t.TempDir(), "age")
	require.NoError(t, err)
	require.NotEmpty(t, run.renderDirs.dirs)

	run.releaseBatch()

	assert.Empty(t, run.renderDirs.dirs)
}

func TestDeleteClipRenders_UnreadableDirectoryFallsBackToUnsizedNames(t *testing.T) {
	skipIfDirPermissionsNotEnforced(t)

	dir := filepath.Join(t.TempDir(), "d1")
	files := []FileInfo{expiredClip(t, dir, "c1"), expiredClip(t, dir, "c2")}
	for _, name := range []string{"c1", "c2"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".png"), []byte("png"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+"_514px.png"), []byte("png"), 0o600))
	}
	// Write and search permission without read: the listing fails, by-name removal works.
	require.NoError(t, os.Chmod(dir, 0o300))       //nolint:gosec // G302: the test needs an unreadable directory
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) }) //nolint:gosec // G302: restore access so TempDir can clean up
	run, _ := newTestRun(t)
	counts := map[string]int{}
	run.renderDirs.readDir = countingReadDir(counts, nil)

	stats := runAgeLoop(t, files, run)

	require.NoError(t, os.Chmod(dir, 0o750)) //nolint:gosec // G302: read back the directory
	assert.NoFileExists(t, filepath.Join(dir, "c1.wav"))
	assert.NoFileExists(t, filepath.Join(dir, "c2.wav"))
	assert.NoFileExists(t, filepath.Join(dir, "c1.png"))
	assert.NoFileExists(t, filepath.Join(dir, "c2.png"))
	assert.FileExists(t, filepath.Join(dir, "c1_514px.png"), "sized renders leak when the directory cannot be listed")
	assert.FileExists(t, filepath.Join(dir, "c2_514px.png"))
	assert.Equal(t, 1, counts[dir], "the failed listing is cached for the batch")
	assert.Equal(t, 0, stats.Errors)
}

func TestDeleteFileAndOptionalSpectrogram_AlreadyGoneAudioKeepsRenders(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	render := filepath.Join(dir, "a_514px.png")
	require.NoError(t, os.WriteFile(render, []byte("png"), 0o600))
	counts := map[string]int{}
	dirs := &renderDirCache{readDir: countingReadDir(counts, nil)}

	err := deleteFileAndOptionalSpectrogram(&FileInfo{Path: filepath.Join(dir, "a.wav")}, "test", false, "age", dirs)

	require.ErrorIs(t, err, errFileAlreadyGone)
	assert.FileExists(t, render)
	assert.Empty(t, counts, "no directory is listed for a clip whose audio is already gone")
}

func TestEntriesWithPrefix_MatchesFullScan(t *testing.T) {
	t.Parallel()

	names := []string{
		cleanupClipA + ".png", cleanupClipA + "_514px.png", cleanupClipA + "_514px-raw.png",
		cleanupClipA + "_15s_514px.png", cleanupClipA + "2_514px.png", cleanupClipB + "_514px.png",
		"b.PNG", "b.png", "b_15s.png", "b_15s_1026px.png", "bx_1026px.png", "a.png", "c.png",
	}
	fsys := fstest.MapFS{}
	for _, n := range names {
		fsys[n] = &fstest.MapFile{Data: []byte("png")}
	}
	entries, err := fs.ReadDir(fsys, ".")
	require.NoError(t, err)

	for _, base := range []string{cleanupClipA, "b", "b_15s", "bx", "zzz", ""} {
		t.Run("base "+base, func(t *testing.T) {
			t.Parallel()
			var want, got []string
			for _, e := range specfile.Matching(entries, base) {
				want = append(want, e.Name())
			}
			for _, e := range specfile.Matching(entriesWithPrefix(entries, base), base) {
				got = append(got, e.Name())
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestRenderDirCache_ListHandlesNilReceiverAndMissingDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFileIn(t, dir, "a.png", "png")

	var nilCache *renderDirCache
	entries, err := nilCache.list(dir, "age")
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	cache := &renderDirCache{}
	entries, err = cache.list(filepath.Join(dir, "missing"), "age")
	require.NoError(t, err, "a missing directory is an empty listing")
	assert.Empty(t, entries)
}

func TestDeleteClipRenders_RemovesUppercaseUnsizedRender(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	upper := writeFileIn(t, dir, "a.PNG", "png")

	deleted := deleteClipRenders(filepath.Join(dir, "a.wav"), "age", &renderDirCache{}, GetLogger())

	assert.Equal(t, 1, deleted)
	assert.NoFileExists(t, upper)
}

func TestDeleteUnsizedSpectrograms_RemovesBothCasesWhenDistinctFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lower := writeFileIn(t, dir, "a.png", "png")
	upper := filepath.Join(dir, "a.PNG")
	if err := os.WriteFile(upper, []byte("png"), 0o600); err != nil {
		t.Skipf("cannot create the uppercase name: %v", err)
	}
	if _, err := os.Stat(lower); err != nil {
		t.Skip("case-insensitive filesystem: the two names are one file")
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	if len(entries) != 2 {
		t.Skip("case-insensitive filesystem: the two names are one file")
	}

	deleted := deleteUnsizedSpectrograms(filepath.Join(dir, "a"), "age", GetLogger())

	assert.Equal(t, 2, deleted)
	assert.NoFileExists(t, lower)
	assert.NoFileExists(t, upper)
}

func TestDeletionRun_UsagePolicyListsEachDirectoryOncePerBatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := makeDrainFiles(t, dir, 3)
	run, _ := newTestRun(t)
	counts := map[string]int{}
	run.renderDirs.readDir = countingReadDir(counts, nil)
	params := newUsageLoopTestParams(1000, 900, 80, 0)
	params.keepSpectrograms = false

	deletedCount, _, _, _, err := processUsageDeletionLoop(files, buildSpeciesSubDirCountMap(files), params, dir, run)

	require.NoError(t, err)
	require.Equal(t, 3, deletedCount)
	// Batch 1 (two clips) shares one listing; batch 2 lists the directory again.
	assert.Equal(t, map[string]int{dir: 2}, counts)
}

// regularEntry reports a directory entry as a regular file whatever it is on disk.
type regularEntry struct{ os.DirEntry }

func (regularEntry) IsDir() bool       { return false }
func (regularEntry) Type() fs.FileMode { return 0 }

func TestDeleteFileAndOptionalSpectrogram_RenderRemovalFailureDoesNotFailTheClip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	audio := filepath.Join(dir, "a.wav")
	require.NoError(t, os.WriteFile(audio, []byte("audio"), 0o600))
	// A non-empty directory named like a render: listed as a regular file, but
	// os.Remove refuses it.
	stuck := filepath.Join(dir, "a_514px.png")
	require.NoError(t, os.MkdirAll(filepath.Join(stuck, "inner"), 0o750))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	listed := make([]os.DirEntry, 0, len(entries))
	for _, e := range entries {
		if e.Name() == "a_514px.png" {
			e = regularEntry{e}
		}
		listed = append(listed, e)
	}
	dirs := &renderDirCache{readDir: func(string) ([]os.DirEntry, error) { return listed, nil }}

	err = deleteFileAndOptionalSpectrogram(&FileInfo{Path: audio, Size: 5}, "test", false, "age", dirs)

	require.NoError(t, err, "a render that cannot be removed must not fail the clip")
	assert.NoFileExists(t, audio)
	assert.DirExists(t, stuck)
}
