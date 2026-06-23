package imports_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/imports"
)

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

func TestImport_WithAudio_MissingClip_ImportsContinues(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	rows := []birdnetPiRow{
		{
			Date: "2025-05-01", Time: "08:00:00",
			SciName: "Parus major", ComName: "Great Tit",
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

	// First run.
	src1, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	eng1 := imports.NewEngine(repo)
	stats1, err := eng1.Run(t.Context(), src1, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, stats1.Inserted)

	// Second run: same detection must be skipped.
	src2, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	eng2 := imports.NewEngine(repo)
	stats2, err := eng2.Run(t.Context(), src2, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Inserted)
	assert.Equal(t, 1, stats2.Skipped)
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
