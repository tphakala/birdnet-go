package imports_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imports"
)

// comGreatTit is the BirdNET-Pi common name reused across several audio tests as the
// clip directory component. Extracted to a constant to satisfy goconst.
const comGreatTit = "Great Tit"

// Goroutine-leak verification for these engine audio tests is provided by the
// package-wide goleak gate in zz_goleak_test.go (TestMain), which runs the entire
// imports_test package under go.uber.org/goleak. The clip-copy worker pool exercised
// by TestCopyCandidateClips_ConcurrentPool is therefore checked for leaks there; a
// per-test gate is unnecessary and a second TestMain would not compile.

// makeAudioTree creates a fake BirdNET-Pi audio tree under root and writes a
// clip file at root/Extracted/By_Date/<date>/<comName>/<fileName>.
// Returns the path of the created clip file.
func makeAudioTree(t *testing.T, root, date, comName, fileName string, content []byte) string {
	t.Helper()
	dir := filepath.Join(root, "Extracted", "By_Date", date, comName)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, fileName)
	require.NoError(t, os.WriteFile(path, content, 0o644))
	return path
}

// assertWithinDir fails the test if target is not contained within root.
// This is the test-package mirror of the package-private isWithinDir helper;
// it uses filepath.Rel because the unexported helper is not importable here.
func assertWithinDir(t *testing.T, root, target string) {
	t.Helper()
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, target)
	require.NoError(t, err, "filepath.Rel(%q, %q) must not error", root, target)
	assert.NotEqual(t, "..", rel, "%q must be within %q", target, root)
	assert.False(t, strings.HasPrefix(rel, ".."+string(filepath.Separator)),
		"%q must be within %q (rel=%q)", target, root, rel)
}

// walkFiles returns the absolute paths of every regular file under dir.
func walkFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	require.NoError(t, walkErr)
	return files
}

// --- resolveSourceClipPath (exported via test-only accessor) ---

// We test the internal helpers indirectly through the Engine/Run integration
// and through the exported ImportOptions fields. For the helpers that are
// package-private we use an integration approach.

func TestImport_WithAudio_CopiesClips(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	date := "2025-03-25"
	comName := "Great Spotted Woodpecker"
	fileName := "woodpecker.mp3"
	clipContent := []byte("fake audio content")
	makeAudioTree(t, audioSrc, date, comName, fileName, clipContent)

	rows := []birdnetPiRow{
		{
			Date: date, Time: "14:27:32",
			SciName: "Dendrocopos major", ComName: comName,
			Confidence: 0.74, Lat: 60.75, Lon: 24.77,
			Cutoff: 0.7, Sens: 1.25, FileName: fileName,
		},
	}
	dbPath := newFixtureDB(t, rows)

	src, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)

	store := newTestStore(t)
	repo := newDetectionRepo(t, store)
	engine := imports.NewEngine(repo)

	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		BatchSize:      100,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}

	stats, err := engine.Run(t.Context(), src, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Inserted)
	assert.Equal(t, 0, stats.Errors)

	// The clip must have been copied into exportDir.
	ts := time.Date(2025, 3, 25, 14, 27, 32, 0, time.UTC)
	relPath := imports.TargetClipRelPathForTest("Dendrocopos major", 0.74, ts, "mp3")
	destAbs := filepath.Join(exportDir, filepath.FromSlash(relPath))
	data, readErr := os.ReadFile(destAbs)
	require.NoError(t, readErr, "copied clip must exist at %s", destAbs)
	assert.Equal(t, clipContent, data)

	// T-F6: Load the saved detection and verify ClipName was set to the relative clip path.
	results, _, searchErr := repo.Search(t.Context(), &datastore.DetectionFilters{
		Location: []string{imports.DefaultSourceNode},
		Limit:    10,
	})
	require.NoError(t, searchErr)
	require.Len(t, results, 1)
	assert.Equal(t, relPath, results[0].ClipName, "ClipName must equal the relative clip path")
}

func TestImport_WithAudio_InsufficientDiskSpace_Aborts(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	date := "2025-03-25"
	comName := "Great Spotted Woodpecker"
	fileName := "woodpecker.mp3"
	clipContent := []byte("fake audio content that occupies some bytes")
	makeAudioTree(t, audioSrc, date, comName, fileName, clipContent)

	rows := []birdnetPiRow{
		{
			Date: date, Time: "14:27:32",
			SciName: "Dendrocopos major", ComName: comName,
			Confidence: 0.74, Lat: 60.75, Lon: 24.77,
			Cutoff: 0.7, Sens: 1.25, FileName: fileName,
		},
	}
	dbPath := newFixtureDB(t, rows)

	src, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)

	store := newTestStore(t)
	repo := newDetectionRepo(t, store)
	engine := imports.NewEngine(repo)

	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		BatchSize:      100,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
		// Report effectively no free space so the disk-space guard trips before
		// any clip is copied.
		DiskSpaceFunc: func(string) (uint64, error) { return 0, nil },
	}

	stats, err := engine.Run(t.Context(), src, &opts, nil)
	require.Error(t, err, "import must abort when the export volume lacks space")
	assert.Equal(t, 0, stats.Inserted, "no detections may be saved when the disk-space guard trips")

	// No clip may have been copied into the export tree.
	ts := time.Date(2025, 3, 25, 14, 27, 32, 0, time.UTC)
	relPath := imports.TargetClipRelPathForTest("Dendrocopos major", 0.74, ts, "mp3")
	destAbs := filepath.Join(exportDir, filepath.FromSlash(relPath))
	_, statErr := os.Stat(destAbs)
	assert.True(t, os.IsNotExist(statErr), "no clip may be written when space is insufficient")

	// T-F8: the export tree must be completely empty, not just missing the one expected clip.
	files := walkFiles(t, exportDir)
	assert.Empty(t, files, "export tree must contain no files when the disk-space guard trips")
}

func TestImport_WithAudio_FallbackComName(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	date := "2025-04-10"
	// resolveSourceClipPath fallback replaces spaces with underscores and strips apostrophes.
	// "Robin's Thrush" -> strip apostrophe -> "Robins Thrush" -> spaces to _ -> "Robins_Thrush"
	fallbackDir := "Robins_Thrush"
	fileName := "thrush.mp3"
	clipContent := []byte("thrush audio")
	// Place clip under the fallback directory name
	dir := filepath.Join(audioSrc, "Extracted", "By_Date", date, fallbackDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, fileName), clipContent, 0o644))

	comName := "Robin's Thrush"
	rows := []birdnetPiRow{
		{
			Date: date, Time: "09:00:00",
			SciName: "Turdus species", ComName: comName,
			Confidence: 0.80, Lat: 60.0, Lon: 24.0,
			Cutoff: 0.5, Sens: 1.0, FileName: fileName,
		},
	}
	dbPath := newFixtureDB(t, rows)

	src, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)

	store := newTestStore(t)
	repo := newDetectionRepo(t, store)
	engine := imports.NewEngine(repo)

	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}

	stats, err := engine.Run(t.Context(), src, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Inserted)

	ts := time.Date(2025, 4, 10, 9, 0, 0, 0, time.UTC)
	relPath := imports.TargetClipRelPathForTest("Turdus species", 0.80, ts, "mp3")
	destAbs := filepath.Join(exportDir, filepath.FromSlash(relPath))
	data, readErr := os.ReadFile(destAbs)
	require.NoError(t, readErr, "clip via fallback name must be copied to %s", destAbs)
	assert.Equal(t, clipContent, data)
}

// TestImport_WithAudio_FallbackPreference verifies that when BOTH the exact-ComName
// directory and the underscore-fallback directory exist, resolveSourceClipPath prefers
// the EXACT form. The two directories hold clips with different content so the copied
// clip can be unambiguously attributed to one source.
func TestImport_WithAudio_FallbackPreference(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	date := "2025-04-11"
	fileName := "thrush.mp3"
	comName := "Robin's Thrush"

	// Exact directory uses the raw common name (apostrophe preserved).
	exactContent := []byte("exact-comname audio")
	exactDir := filepath.Join(audioSrc, "Extracted", "By_Date", date, comName)
	require.NoError(t, os.MkdirAll(exactDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(exactDir, fileName), exactContent, 0o644))

	// Fallback directory: apostrophe stripped, spaces to underscores.
	fallbackContent := []byte("fallback-comname audio")
	fallbackDir := filepath.Join(audioSrc, "Extracted", "By_Date", date, "Robins_Thrush")
	require.NoError(t, os.MkdirAll(fallbackDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fallbackDir, fileName), fallbackContent, 0o644))

	rows := []birdnetPiRow{
		{
			Date: date, Time: "09:30:00",
			SciName: "Turdus species", ComName: comName,
			Confidence: 0.80, Lat: 60.0, Lon: 24.0,
			Cutoff: 0.5, Sens: 1.0, FileName: fileName,
		},
	}
	dbPath := newFixtureDB(t, rows)

	src, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)

	store := newTestStore(t)
	repo := newDetectionRepo(t, store)
	engine := imports.NewEngine(repo)

	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}

	stats, err := engine.Run(t.Context(), src, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Inserted)

	ts := time.Date(2025, 4, 11, 9, 30, 0, 0, time.UTC)
	relPath := imports.TargetClipRelPathForTest("Turdus species", 0.80, ts, "mp3")
	destAbs := filepath.Join(exportDir, filepath.FromSlash(relPath))
	data, readErr := os.ReadFile(destAbs)
	require.NoError(t, readErr, "clip must be copied to %s", destAbs)
	assert.Equal(t, exactContent, data, "the exact-ComName directory must be preferred over the fallback")
}

func TestImport_WithAudio_MissingClip_ImportsContinues(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	rows := []birdnetPiRow{
		{
			Date: "2025-05-01", Time: "08:00:00",
			SciName: "Parus major", ComName: comGreatTit,
			Confidence: 0.85, Lat: 60.0, Lon: 24.0,
			Cutoff: 0.5, Sens: 1.0,
			FileName: "missing.mp3",
		},
	}
	dbPath := newFixtureDB(t, rows)

	src, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)

	store := newTestStore(t)
	repo := newDetectionRepo(t, store)
	engine := imports.NewEngine(repo)

	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}

	stats, err := engine.Run(t.Context(), src, &opts, nil)
	require.NoError(t, err)
	// Detection must still be imported even though audio is missing.
	assert.Equal(t, 1, stats.Inserted)
	assert.Equal(t, 0, stats.Errors)

	// The saved detection must have an empty ClipName (graceful degradation).
	results, _, searchErr := repo.Search(t.Context(), &datastore.DetectionFilters{
		Location: []string{imports.DefaultSourceNode},
		Limit:    10,
	})
	require.NoError(t, searchErr)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].ClipName, "ClipName must be empty when source clip is missing")
}

func TestImport_WithAudio_Idempotent_ClipNotCopiedTwice(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	date := "2025-06-01"
	comName := "Common Blackbird"
	fileName := "blackbird.mp3"
	clipContent := []byte("blackbird song")
	makeAudioTree(t, audioSrc, date, comName, fileName, clipContent)

	rows := []birdnetPiRow{
		{
			Date: date, Time: "07:00:00",
			SciName: "Turdus merula", ComName: comName,
			Confidence: 0.92, Lat: 60.0, Lon: 24.0,
			Cutoff: 0.5, Sens: 1.0, FileName: fileName,
		},
	}
	dbPath := newFixtureDB(t, rows)

	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}

	store := newTestStore(t)
	repo := newDetectionRepo(t, store)

	// Expected destination of the copied clip.
	ts := time.Date(2025, 6, 1, 7, 0, 0, 0, time.UTC)
	relPath := imports.TargetClipRelPathForTest("Turdus merula", 0.92, ts, "mp3")
	destAbs := filepath.Join(exportDir, filepath.FromSlash(relPath))

	// First run.
	src1, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	eng1 := imports.NewEngine(repo)
	stats1, err := eng1.Run(t.Context(), src1, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, stats1.Inserted)

	// T-F7: the clip file must exist after run 1; record its mtime and content.
	info1, statErr := os.Stat(destAbs)
	require.NoError(t, statErr, "clip must exist after first run at %s", destAbs)
	mtime1 := info1.ModTime()
	data1, readErr := os.ReadFile(destAbs)
	require.NoError(t, readErr)
	assert.Equal(t, clipContent, data1)

	// Second run: same detection must be skipped.
	src2, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	eng2 := imports.NewEngine(repo)
	stats2, err := eng2.Run(t.Context(), src2, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Inserted)
	assert.Equal(t, 1, stats2.Skipped)

	// T-F7: the clip must not have been rewritten. Same file, same mtime, same content.
	info2, statErr := os.Stat(destAbs)
	require.NoError(t, statErr, "clip must still exist after second run")
	assert.Equal(t, mtime1, info2.ModTime(), "clip must not be rewritten on the idempotent re-run")
	data2, readErr := os.ReadFile(destAbs)
	require.NoError(t, readErr)
	assert.Equal(t, clipContent, data2, "clip content must be unchanged after the idempotent re-run")

	// Exactly one clip file may exist in the export tree across both runs.
	files := walkFiles(t, exportDir)
	assert.Len(t, files, 1, "the idempotent re-run must not create a second clip file")
}

func TestCheckDiskSpace_Sufficient(t *testing.T) {
	called := false
	freeFn := func(_ string) (uint64, error) {
		called = true
		return 1000, nil
	}
	err := imports.CheckDiskSpaceForTest("any/path", 500, freeFn)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestCheckDiskSpace_Insufficient(t *testing.T) {
	freeFn := func(_ string) (uint64, error) {
		return 100, nil
	}
	err := imports.CheckDiskSpaceForTest("any/path", 500, freeFn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient disk space")
}

func TestCheckDiskSpace_FuncError(t *testing.T) {
	freeFn := func(_ string) (uint64, error) {
		return 0, fmt.Errorf("syscall failed")
	}
	err := imports.CheckDiskSpaceForTest("any/path", 1, freeFn)
	require.Error(t, err)
}

func TestTargetClipRelPath(t *testing.T) {
	ts := time.Date(2025, 3, 25, 14, 27, 32, 0, time.UTC)
	got := imports.TargetClipRelPathForTest("Dendrocopos major", 0.74, ts, "mp3")
	// Expected: 2025/03/dendrocopos_major_74p_20250325T142732Z.mp3
	assert.Equal(t, "2025/03/dendrocopos_major_74p_20250325T142732Z.mp3", got)
}

func TestTargetClipRelPath_RoundsConfidence(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got := imports.TargetClipRelPathForTest("Parus major", 0.81, ts, "wav")
	assert.Contains(t, got, "81p")
	assert.Contains(t, got, "parus_major")
	assert.Contains(t, got, ".wav")
}

// TestTargetClipRelPath_TraversalScientificName verifies that a crafted ScientificName
// with path separators, absolute paths, Windows paths, or ".." components cannot escape
// the expected "YYYY/MM/<file>" path structure. For every case the result must be
// relative, prefixed by the four-digit year, and free of any ".." path segment.
func TestTargetClipRelPath_TraversalScientificName(t *testing.T) {
	ts := time.Date(2025, 6, 1, 8, 0, 0, 0, time.UTC)

	crafted := []struct {
		name    string
		sciName string
	}{
		{"dot-dot prefix", "../evil"},
		{"deep dot-dot", "../../etc/passwd"},
		{"slash in name", "a/b/c"},
		{"absolute unix path", "/etc/passwd"},
		{"backslash in name", "evil\\name"},
		{"empty name", ""},
		{"single dot", "."},
		{"double dot", ".."},
		{"deep traversal", "../../etc/passwd"},
	}

	for _, tc := range crafted {
		t.Run(tc.name, func(t *testing.T) {
			relPath := imports.TargetClipRelPathForTest(tc.sciName, 0.9, ts, "mp3")

			// Must be a relative path, never absolute.
			assert.False(t, filepath.IsAbs(relPath), "relPath %q must not be absolute", relPath)

			// Must be prefixed with the four-digit year of the timestamp.
			assert.True(t, strings.HasPrefix(relPath, "2025/"),
				"relPath %q must start with the YYYY/ prefix", relPath)

			// No path segment may be exactly "..".
			for seg := range strings.SplitSeq(relPath, "/") {
				assert.NotEqual(t, "..", seg, "relPath %q must contain no .. segment", relPath)
			}

			// The filename part (last slash-separated segment) must not itself contain the
			// forward-slash path separator used by the returned relative path. A literal
			// backslash is intentionally allowed: on the Linux import target it is an ordinary
			// filename byte, not a separator, so it cannot escape the export directory.
			parts := strings.Split(relPath, "/")
			require.NotEmpty(t, parts)
			last := parts[len(parts)-1]
			assert.NotContains(t, last, "/", "filename part must not contain /")
		})
	}
}

// TestResolveSourceClipPath_TraversalRejected verifies that DB-derived path components
// containing "..", path separators, absolute paths, or Windows paths cannot escape
// audioSourceDir. Such detections are treated as a clip miss and the detection is still
// imported (graceful degradation).
func TestResolveSourceClipPath_TraversalRejected(t *testing.T) {
	audioSrc := t.TempDir()

	// A legitimate clip for the non-traversal case.
	date := "2025-06-01"
	comName := comGreatTit
	fileName := "tit.mp3"
	makeAudioTree(t, audioSrc, date, comName, fileName, []byte("audio"))

	traversalCases := []struct {
		name     string
		date     string
		comName  string
		fileName string
	}{
		{"dot-dot in comName", date, "../Great Tit", fileName},
		{"dot-dot in fileName", date, comName, "../tit.mp3"},
		{"slash in comName", date, "a/b", fileName},
		{"slash in fileName", date, comName, "a/b.mp3"},
		{"absolute path in comName", date, "/etc/Great Tit", fileName},
		{"windows path in comName", date, "C:\\clip", fileName},
		{"dot-dot in both comName and fileName", date, "../Great Tit", "../tit.mp3"},
	}

	for _, tc := range traversalCases {
		t.Run(tc.name, func(t *testing.T) {
			rows := []birdnetPiRow{{
				Date: tc.date, Time: "08:00:00",
				SciName: "Parus major", ComName: tc.comName,
				Confidence: 0.85,
				Cutoff:     0.5, Sens: 1.0, FileName: tc.fileName,
			}}
			dbPath := newFixtureDB(t, rows)
			src, err := newBirdNetPiSource(t, dbPath)
			require.NoError(t, err)

			exportDir := t.TempDir()
			store := newTestStore(t)
			repo := newDetectionRepo(t, store)
			engine := imports.NewEngine(repo)

			opts := imports.ImportOptions{
				SourceNode:     imports.DefaultSourceNode,
				Location:       time.UTC,
				IncludeAudio:   true,
				AudioSourceDir: audioSrc,
				ClipExportPath: exportDir,
			}

			stats, runErr := engine.Run(t.Context(), src, &opts, nil)
			require.NoError(t, runErr, "traversal attempt must not cause engine error (treated as miss)")
			assert.Equal(t, 1, stats.Inserted, "detection must be imported even with traversal in audio path")

			// Verify that nothing was written outside exportDir.
			// The traversal component is rejected, so the clip is a miss and exportDir stays empty.
			entries, rdErr := os.ReadDir(exportDir)
			require.NoError(t, rdErr)
			assert.Empty(t, entries, "exportDir must be empty: no clip may be written for a traversal attempt")
		})
	}
}

// TestCopyCandidateClips_TraversalDestRejected verifies that a crafted ScientificName
// cannot cause a clip to be written outside ClipExportPath. With sanitization applied,
// the clip is placed at a safe path inside ClipExportPath, and the source clip is copied.
func TestCopyCandidateClips_TraversalDestRejected(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	date := "2025-07-01"
	comName := comGreatTit
	fileName := "tit.mp3"
	clipContent := []byte("audio data")
	makeAudioTree(t, audioSrc, date, comName, fileName, clipContent)

	// A detection with a crafted ScientificName that would have escaped exportDir
	// before the filepath.Base sanitization was applied.
	rows := []birdnetPiRow{{
		Date: date, Time: "08:00:00",
		SciName: "../../evil/parus major", ComName: comName,
		Confidence: 0.85,
		Cutoff:     0.5, Sens: 1.0, FileName: fileName,
	}}
	dbPath := newFixtureDB(t, rows)
	src, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)

	store := newTestStore(t)
	repo := newDetectionRepo(t, store)
	engine := imports.NewEngine(repo)

	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}

	stats, runErr := engine.Run(t.Context(), src, &opts, nil)
	require.NoError(t, runErr)
	// Detection is imported regardless.
	assert.Equal(t, 1, stats.Inserted)

	// The sanitized clip must be inside exportDir (filepath.Base strips the "../../../" prefix).
	// Verify no directory named "evil" was created outside exportDir.
	parent := filepath.Dir(exportDir)
	entries, rdErr := os.ReadDir(parent)
	require.NoError(t, rdErr)
	for _, e := range entries {
		assert.NotEqual(t, "evil", e.Name(), "traversal must not create 'evil' directory outside export root")
	}

	// T-F2: positive containment proof. The sanitized sciname
	// "../../evil/parus major" -> filepath.Base -> "parus major" -> "parus_major",
	// so exactly one clip file must be present and it must live inside exportDir.
	files := walkFiles(t, exportDir)
	require.Len(t, files, 1, "exactly one clip file must be written inside exportDir")
	for _, f := range files {
		assertWithinDir(t, exportDir, f)
	}
	// The single file must carry the sanitized name component.
	assert.Contains(t, files[0], "parus_major", "clip must be stored under the sanitized scientific name")
}

// TestCopyCandidateClips_ConcurrentPool exercises the bounded copy worker pool with more
// rows than audioWorkerLimit (4). It mixes 5 present clips with 3 missing clips and runs
// the real engine. Under -race this asserts that:
//   - all 8 detections are saved,
//   - exactly the 5 present clips are copied to their expected relative paths inside exportDir,
//   - exactly 5 saved detections have a non-empty ClipName and 3 have an empty ClipName,
//   - no clip escapes exportDir.
func TestCopyCandidateClips_ConcurrentPool(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	date := "2025-08-01"
	comName := comGreatTit
	sciName := "Parus major"

	const presentCount = 5
	const missingCount = 3
	const totalCount = presentCount + missingCount

	// Build rows with unique timestamps so none are deduplicated against each other.
	// Rows 0..4 have a backing source clip; rows 5..7 do not.
	rows := make([]birdnetPiRow, 0, totalCount)
	expectedRelPaths := make([]string, 0, presentCount)
	for i := range totalCount {
		fileName := fmt.Sprintf("clip_%d.mp3", i)
		// Unique second-of-minute keeps Date+Time distinct per row.
		timeStr := fmt.Sprintf("08:00:%02d", i)
		confidence := 0.50 + float64(i)*0.01
		row := birdnetPiRow{
			Date: date, Time: timeStr,
			SciName: sciName, ComName: comName,
			Confidence: confidence, Lat: 60.0, Lon: 24.0,
			Cutoff: 0.5, Sens: 1.0, FileName: fileName,
		}
		rows = append(rows, row)

		if i < presentCount {
			content := fmt.Appendf(nil, "audio for clip %d", i)
			makeAudioTree(t, audioSrc, date, comName, fileName, content)

			ts := time.Date(2025, 8, 1, 8, 0, i, 0, time.UTC)
			relPath := imports.TargetClipRelPathForTest(sciName, confidence, ts, "mp3")
			expectedRelPaths = append(expectedRelPaths, relPath)
		}
	}

	dbPath := newFixtureDB(t, rows)
	src, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)

	store := newTestStore(t)
	repo := newDetectionRepo(t, store)
	engine := imports.NewEngine(repo)

	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		BatchSize:      100,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}

	stats, runErr := engine.Run(t.Context(), src, &opts, nil)
	require.NoError(t, runErr)
	assert.Equal(t, totalCount, stats.Inserted, "all detections must be saved")
	assert.Equal(t, 0, stats.Errors, "missing clips are not errors")

	// Each present clip must be copied to its expected relative path inside exportDir.
	for _, relPath := range expectedRelPaths {
		destAbs := filepath.Join(exportDir, filepath.FromSlash(relPath))
		_, statErr := os.Stat(destAbs)
		require.NoError(t, statErr, "present clip must be copied to %s", destAbs)
		assertWithinDir(t, exportDir, destAbs)
	}

	// Exactly presentCount clip files may exist in the export tree, all inside exportDir.
	files := walkFiles(t, exportDir)
	assert.Len(t, files, presentCount, "exactly %d clips must be copied", presentCount)
	for _, f := range files {
		assertWithinDir(t, exportDir, f)
	}

	// Verify per-detection ClipName: exactly presentCount non-empty, missingCount empty.
	results, _, searchErr := repo.Search(t.Context(), &datastore.DetectionFilters{
		Location: []string{imports.DefaultSourceNode},
		Limit:    100,
	})
	require.NoError(t, searchErr)
	require.Len(t, results, totalCount, "all detections must be persisted")

	withClip := 0
	withoutClip := 0
	expectedSet := make(map[string]struct{}, len(expectedRelPaths))
	for _, p := range expectedRelPaths {
		expectedSet[p] = struct{}{}
	}
	for i := range results {
		if results[i].ClipName == "" {
			withoutClip++
			continue
		}
		withClip++
		_, ok := expectedSet[results[i].ClipName]
		assert.True(t, ok, "ClipName %q must be one of the expected relative paths", results[i].ClipName)
	}
	assert.Equal(t, presentCount, withClip, "exactly %d detections must carry a ClipName", presentCount)
	assert.Equal(t, missingCount, withoutClip, "exactly %d detections must have an empty ClipName", missingCount)
}
