package imports_test

import (
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

// This file is the behaviour baseline for the re-attach half of issue #4313. It
// deliberately uses only the API that exists at the pin: the engine repository
// is the production v2-only adapter (v2only.NewDetectionRepository, returned by
// newV2DetectionRepo), and every assertion is on copied bytes, persisted
// ClipName, row counts or the pinned ImportStats fields. On the pin the engine
// never attaches a clip to an already stored detection, so these tests build
// there and fail on an assertion (missing clip file, empty ClipName); with the
// fix they pass. Tests that need the new SetClipName method or the new
// ImportStats.Reattached counter live in the fix commit and cannot build at the
// pin.

// clipNamesBySci searches the store and keys the results by scientific name.
func clipNamesBySci(t *testing.T, repo datastore.DetectionRepository) map[string]string {
	t.Helper()
	results, _, err := repo.Search(t.Context(), datastore.NewDetectionFilters().WithLimit(datastore.DefaultDetectionLimit))
	require.NoError(t, err)
	bySci := make(map[string]string, len(results))
	for _, r := range results {
		bySci[r.Species.ScientificName] = r.ClipName
	}
	return bySci
}

// reattachRows is the pair of source rows reused by several re-attach tests.
func reattachRows() []birdnetPiRow {
	return []birdnetPiRow{
		{
			Date: "2025-03-25", Time: "14:27:32",
			SciName: "Dendrocopos major", ComName: "Great Spotted Woodpecker",
			Confidence: 0.74, Lat: 60.75, Lon: 24.77, Cutoff: 0.7, Sens: 1.25,
			FileName: "woodpecker.mp3",
		},
		{
			Date: "2025-03-25", Time: "15:00:00",
			SciName: "Parus major", ComName: comGreatTit,
			Confidence: 0.81, Lat: 60.75, Lon: 24.77, Cutoff: 0.5, Sens: 1.0,
			FileName: "tit.mp3",
		},
	}
}

// reattachAudioTree writes the source clips of reattachRows under audioSrc.
func reattachAudioTree(t *testing.T, audioSrc string) (clipA, clipB []byte) {
	t.Helper()
	clipA = []byte("woodpecker audio")
	clipB = []byte("tit audio")
	makeAudioTree(t, audioSrc, "2025-03-25", "Great Spotted Woodpecker", "woodpecker.mp3", clipA)
	makeAudioTree(t, audioSrc, "2025-03-25", comGreatTit, "tit.mp3", clipB)
	return clipA, clipB
}

// reattachClipRel is the export-relative clip path of one reattachRows entry.
func reattachClipRel(sciName string, confidence float64, ts time.Time) string {
	return imports.TargetClipRelPathForTest(sciName, confidence, ts, "mp3")
}

// TestImport_ReattachClipsToExistingDetections verifies the re-attach half of
// issue #4313: a db-audio import run after a db-only import copies the source
// clips and sets ClipName on the detections that already exist, without
// inserting any new row. The engine repository is the production v2-only
// adapter, exactly what a v2 install passes to imports.NewEngine. At the pin
// the engine never sets ClipName on an existing row, so this fails there with
// the clip files missing and ClipName empty.
func TestImport_ReattachClipsToExistingDetections(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()

	rows := reattachRows()
	clipA, clipB := reattachAudioTree(t, audioSrc)

	dbPath := newFixtureDB(t, rows)
	repo, _ := newV2DetectionRepo(t, time.UTC)

	// First run: db-only, stores both detections without clips.
	src1, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	dbOnlyOpts := imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: time.UTC}
	stats1, err := imports.NewEngine(repo).Run(t.Context(), src1, &dbOnlyOpts, nil)
	require.NoError(t, err)
	require.Equal(t, 2, stats1.Inserted)
	for _, clip := range clipNamesBySci(t, repo) {
		assert.Empty(t, clip, "db-only import must leave ClipName empty")
	}

	// Second run: db-audio over the same source re-attaches the clips.
	src2, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	audioOpts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}
	stats2, err := imports.NewEngine(repo).Run(t.Context(), src2, &audioOpts, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Inserted, "no new rows may be inserted on re-import")
	assert.Equal(t, 2, stats2.Skipped)
	assert.Equal(t, 0, stats2.Errors)

	// The clips were copied through the same path as for new rows.
	tsA := time.Date(2025, 3, 25, 14, 27, 32, 0, time.UTC)
	relA := reattachClipRel("Dendrocopos major", 0.74, tsA)
	data, err := os.ReadFile(filepath.Join(exportDir, filepath.FromSlash(relA)))
	require.NoError(t, err, "re-attached clip must exist at %s", relA)
	assert.Equal(t, clipA, data)

	tsB := time.Date(2025, 3, 25, 15, 0, 0, 0, time.UTC)
	relB := reattachClipRel("Parus major", 0.81, tsB)
	data, err = os.ReadFile(filepath.Join(exportDir, filepath.FromSlash(relB)))
	require.NoError(t, err, "re-attached clip must exist at %s", relB)
	assert.Equal(t, clipB, data)

	// ClipName is set on the existing rows and the row count is unchanged.
	_, total, err := repo.Search(t.Context(), datastore.NewDetectionFilters().WithLimit(datastore.DefaultDetectionLimit))
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "re-attach must not add detection rows")
	bySci := clipNamesBySci(t, repo)
	assert.Equal(t, relA, bySci["Dendrocopos major"])
	assert.Equal(t, relB, bySci["Parus major"])
}

// TestImport_ReattachLeavesExistingClipNameUntouched verifies that a stored
// detection that already has a ClipName is never overwritten by a re-attach,
// and that its source clip is not copied again.
func TestImport_ReattachLeavesExistingClipNameUntouched(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()

	tsA := time.Date(2025, 3, 25, 14, 27, 32, 0, time.UTC)
	tsB := time.Date(2025, 3, 25, 15, 0, 0, 0, time.UTC)
	rows := reattachRows()
	reattachAudioTree(t, audioSrc)

	dbPath := newFixtureDB(t, rows)
	repo, db := newV2DetectionRepo(t, time.UTC)

	src1, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	stats1, err := imports.NewEngine(repo).Run(t.Context(), src1,
		&imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: time.UTC}, nil)
	require.NoError(t, err)
	require.Equal(t, 2, stats1.Inserted)

	// Give the woodpecker detection a clip name directly in the database, the
	// way an earlier db-audio import would have.
	const keptClip = "2025/03/kept_74p_20250325T142732Z.mp3"
	res := db.Exec("UPDATE detections SET clip_name = ? WHERE detected_at = ?", keptClip, tsA.Unix())
	require.NoError(t, res.Error)
	require.EqualValues(t, 1, res.RowsAffected)

	src2, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}
	stats2, err := imports.NewEngine(repo).Run(t.Context(), src2, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Inserted)
	assert.Equal(t, 2, stats2.Skipped)
	assert.Equal(t, 0, stats2.Errors)

	// Only the clip-less row's clip was copied; the kept-clip row's source clip
	// is not a re-attach candidate.
	files := walkFiles(t, exportDir)
	require.Len(t, files, 1, "only the re-attached row's clip may be written")
	assertWithinDir(t, exportDir, files[0])

	bySci := clipNamesBySci(t, repo)
	assert.Equal(t, keptClip, bySci["Dendrocopos major"], "an existing ClipName must be kept unchanged")
	assert.Equal(t, reattachClipRel("Parus major", 0.81, tsB), bySci["Parus major"])
}

// TestImport_DBOnlyRunDoesNotTouchExistingRows verifies db-only behaviour is
// unchanged: a second db-only run skips the stored rows, attaches nothing and
// copies no audio even when audio paths are configured.
func TestImport_DBOnlyRunDoesNotTouchExistingRows(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()

	rows := []birdnetPiRow{
		{
			Date: "2025-03-25", Time: "15:00:00",
			SciName: "Parus major", ComName: comGreatTit,
			Confidence: 0.81, Lat: 60.75, Lon: 24.77, Cutoff: 0.5, Sens: 1.0,
			FileName: "tit.mp3",
		},
	}
	makeAudioTree(t, audioSrc, "2025-03-25", comGreatTit, "tit.mp3", []byte("tit audio"))
	dbPath := newFixtureDB(t, rows)
	repo, _ := newV2DetectionRepo(t, time.UTC)

	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}
	src1, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	stats1, err := imports.NewEngine(repo).Run(t.Context(), src1, &opts, nil)
	require.NoError(t, err)
	require.Equal(t, 1, stats1.Inserted, "first db-only run inserts the row")

	src2, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	stats2, err := imports.NewEngine(repo).Run(t.Context(), src2, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Inserted)
	assert.Equal(t, 1, stats2.Skipped)
	assert.Equal(t, 0, stats2.Errors)

	assert.Empty(t, clipNamesBySci(t, repo)["Parus major"], "ClipName must stay empty on a db-only run")
	if entries, err := os.ReadDir(exportDir); err == nil {
		assert.Empty(t, entries, "no clip may be copied on a db-only run")
	}
}

// TestImport_ReattachTraversalIsMissNotEscape verifies the re-attach half goes
// through the same containment as new rows: a crafted DB file name is a clip
// miss (no attach, nothing written outside the export root), and a crafted
// scientific name still lands inside it. The traversal targets are planted as
// real files at the locations an unsafe join would resolve to, so a broken
// resolver would copy them and the assertions below would see it.
func TestImport_ReattachTraversalIsMissNotEscape(t *testing.T) {
	// One common temporary parent holds the audio tree, the export root and the
	// planted traversal targets, so any write outside the export root is
	// visible when walking the parent.
	parent := t.TempDir()
	audioSrc := filepath.Join(parent, "audio")
	exportDir := filepath.Join(parent, "export")
	require.NoError(t, os.MkdirAll(audioSrc, 0o755))

	rows := []birdnetPiRow{
		{
			Date: "2025-06-01", Time: "08:00:00",
			SciName: "Parus major", ComName: comGreatTit,
			Confidence: 0.85, Lat: 60.0, Lon: 24.0, Cutoff: 0.5, Sens: 1.0,
			FileName: "../evil.mp3",
		},
		{
			Date: "2025-06-01", Time: "09:00:00",
			SciName: "../../evil/parus major", ComName: comGreatTit,
			Confidence: 0.86, Lat: 60.0, Lon: 24.0, Cutoff: 0.5, Sens: 1.0,
			FileName: "tit.mp3",
		},
	}
	titClip := []byte("tit audio")
	makeAudioTree(t, audioSrc, "2025-06-01", comGreatTit, "tit.mp3", titClip)

	// Plant the traversal targets where an unsafe join of "../evil.mp3" would
	// resolve: one level above the common-name directory inside the audio tree,
	// and directly next to the audio source root.
	evilClip := []byte("evil audio")
	evilInTree := filepath.Join(audioSrc, "Extracted", "By_Date", "2025-06-01", "evil.mp3")
	require.NoError(t, os.WriteFile(evilInTree, evilClip, 0o644))
	evilNextToRoot := filepath.Join(parent, "evil.mp3")
	require.NoError(t, os.WriteFile(evilNextToRoot, evilClip, 0o644))

	dbPath := newFixtureDB(t, rows)
	repo, _ := newV2DetectionRepo(t, time.UTC)

	// db-only first run: both rows are stored clip-less.
	src1, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	_, err = imports.NewEngine(repo).Run(t.Context(), src1,
		&imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: time.UTC}, nil)
	require.NoError(t, err)

	src2, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}
	stats, err := imports.NewEngine(repo).Run(t.Context(), src2, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Inserted)
	assert.Equal(t, 2, stats.Skipped)
	assert.Equal(t, 0, stats.Errors)

	// Nothing was written outside the audio source and export roots.
	within := func(root, target string) bool {
		rel, err := filepath.Rel(root, target)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	for _, f := range walkFiles(t, parent) {
		if f == evilNextToRoot || f == evilInTree {
			continue // the planted traversal targets must stay where they are
		}
		assert.True(t, within(audioSrc, f) || within(exportDir, f),
			"no file may be written outside the audio source and export roots: %s", f)
	}

	// Exactly one clip (the crafted-name row's) was exported, inside the export
	// root, and it never carries the traversal target's bytes.
	files := walkFiles(t, exportDir)
	require.Len(t, files, 1, "exactly one clip (the crafted-name row's) may be written")
	for _, f := range files {
		assertWithinDir(t, exportDir, f)
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		assert.NotEqual(t, evilClip, data, "the traversal target must never be attached")
	}
	for _, planted := range []string{evilInTree, evilNextToRoot} {
		data, err := os.ReadFile(planted)
		require.NoError(t, err)
		assert.Equal(t, evilClip, data, "planted traversal target %s must be untouched", planted)
	}

	// The traversal file name was a miss: one detection keeps an empty ClipName;
	// the crafted scientific name was sanitized into the export root.
	results, _, err := repo.Search(t.Context(), datastore.NewDetectionFilters().WithLimit(datastore.DefaultDetectionLimit))
	require.NoError(t, err)
	require.Len(t, results, 2)
	emptyClips, attachedClip := 0, ""
	for _, r := range results {
		if r.ClipName == "" {
			emptyClips++
		} else {
			attachedClip = r.ClipName
		}
	}
	assert.Equal(t, 1, emptyClips, "traversal file name must be a miss, not an attach")
	require.NotEmpty(t, attachedClip)
	assertWithinDir(t, exportDir, filepath.Join(exportDir, filepath.FromSlash(attachedClip)))
}

// TestImport_ReattachUnsupportedRepoSkipsGracefully verifies that a repository
// without the SetClipName capability (the legacy-store bridge) does not break a
// db-audio re-import: the run succeeds, existing rows keep their empty ClipName
// and no clip is copied.
func TestImport_ReattachUnsupportedRepoSkipsGracefully(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()

	rows := []birdnetPiRow{
		{
			Date: "2025-03-25", Time: "15:00:00",
			SciName: "Parus major", ComName: comGreatTit,
			Confidence: 0.81, Lat: 60.75, Lon: 24.77, Cutoff: 0.5, Sens: 1.0,
			FileName: "tit.mp3",
		},
	}
	makeAudioTree(t, audioSrc, "2025-03-25", comGreatTit, "tit.mp3", []byte("tit audio"))
	dbPath := newFixtureDB(t, rows)

	store := newTestStore(t)
	repo := newDetectionRepo(t, store) // legacy bridge: no SetClipName

	src1, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	_, err = imports.NewEngine(repo).Run(t.Context(), src1,
		&imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: time.UTC}, nil)
	require.NoError(t, err)

	src2, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}
	stats, err := imports.NewEngine(repo).Run(t.Context(), src2, &opts, nil)
	require.NoError(t, err, "a repo without SetClipName must not fail the run")
	assert.Equal(t, 0, stats.Inserted)
	assert.Equal(t, 1, stats.Skipped)
	assert.Equal(t, 0, stats.Errors)

	results, _, err := repo.Search(t.Context(), &datastore.DetectionFilters{
		Location: []string{imports.DefaultSourceNode},
		Limit:    datastore.DefaultDetectionLimit,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].ClipName, "legacy store keeps no clip on re-import")

	entries, err := os.ReadDir(exportDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "no clip may be copied when the repository cannot set clip names")
}
