package imports_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imports"
)

// duplicateKeyRows are two source rows with the same dedup key (wall time,
// species and confidence) but different clip files. The first row's clip is
// missing from the audio tree; the second row's clip exists.
func duplicateKeyRows() []birdnetPiRow {
	row := birdnetPiRow{
		Date: "2025-04-02", Time: "06:15:00",
		SciName: "Parus major", ComName: comGreatTit,
		Confidence: 0.77, Lat: 60.75, Lon: 24.77, Cutoff: 0.5, Sens: 1.0,
		FileName: "missing.mp3",
	}
	second := row
	second.FileName = "present.mp3"
	return []birdnetPiRow{row, second}
}

// TestImport_ReattachRetriesLaterCandidateAfterMiss verifies that a clip miss
// does not use up the stored clip-less detection: a later source row with the
// same key that has its clip still fills it, whether that row is in the same
// batch (it is tried as an alternate) or in a later batch (the miss hands the
// detection back).
func TestImport_ReattachRetriesLaterCandidateAfterMiss(t *testing.T) {
	for _, batchSize := range []int{0, 1} {
		name := "same batch"
		if batchSize == 1 {
			name = "next batch"
		}
		t.Run(name, func(t *testing.T) {
			audioSrc := t.TempDir()
			exportDir := t.TempDir()
			clip := []byte("present tit audio")
			makeAudioTree(t, audioSrc, "2025-04-02", comGreatTit, "present.mp3", clip)

			dbPath := newFixtureDB(t, duplicateKeyRows())
			repo, _ := newV2DetectionRepo(t, time.UTC)

			// db-only first run: the duplicate source row is deduplicated, so
			// one clip-less detection is stored.
			src1, err := newBirdNetPiSource(t, dbPath)
			require.NoError(t, err)
			stats1, err := imports.NewEngine(repo).Run(t.Context(), src1,
				&imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: time.UTC}, nil)
			require.NoError(t, err)
			require.Equal(t, 1, stats1.Inserted)

			src2, err := newBirdNetPiSource(t, dbPath)
			require.NoError(t, err)
			opts := imports.ImportOptions{
				SourceNode:     imports.DefaultSourceNode,
				Location:       time.UTC,
				BatchSize:      batchSize,
				IncludeAudio:   true,
				AudioSourceDir: audioSrc,
				ClipExportPath: exportDir,
			}
			stats2, err := imports.NewEngine(repo).Run(t.Context(), src2, &opts, nil)
			require.NoError(t, err)
			assert.Equal(t, 0, stats2.Inserted)
			assert.Equal(t, 1, stats2.Reattached, "the second candidate must fill the detection after the first missed")

			ts := time.Date(2025, 4, 2, 6, 15, 0, 0, time.UTC)
			rel := reattachClipRel("Parus major", 0.77, ts)
			assert.Equal(t, rel, clipNamesBySci(t, repo)["Parus major"])
			data, err := os.ReadFile(filepath.Join(exportDir, filepath.FromSlash(rel)))
			require.NoError(t, err)
			assert.Equal(t, clip, data, "the clip of the candidate that has one is attached")
		})
	}
}

// noopClipNameRepo wraps a detection repository with a SetClipName that never
// changes a row, as when another writer fills the clip name between the
// engine's preload and its update.
type noopClipNameRepo struct {
	datastore.DetectionRepository
}

func (noopClipNameRepo) SetClipName(_ context.Context, _ uint, _ string) (bool, error) {
	return false, nil
}

// TestImport_ReattachNoOpUpdateNeverDeletesClip verifies that when the
// guarded update takes no row (another writer set the clip name between the
// preload and the update), the copied clip file is left in place: the other
// writer may have set the clip name to that very file, so removing it could
// break a reference. A file that was already in place is kept as well.
func TestImport_ReattachNoOpUpdateNeverDeletesClip(t *testing.T) {
	for _, preexisting := range []bool{false, true} {
		name := "fresh copy is kept"
		if preexisting {
			name = "file already in place is kept"
		}
		t.Run(name, func(t *testing.T) {
			audioSrc := t.TempDir()
			exportDir := t.TempDir()
			rows := reattachRows()[:1]
			srcClip := []byte("woodpecker audio")
			makeAudioTree(t, audioSrc, "2025-03-25", "Great Spotted Woodpecker", "woodpecker.mp3", srcClip)

			dbPath := newFixtureDB(t, rows)
			v2repo, _ := newV2DetectionRepo(t, time.UTC)

			src1, err := newBirdNetPiSource(t, dbPath)
			require.NoError(t, err)
			_, err = imports.NewEngine(v2repo).Run(t.Context(), src1,
				&imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: time.UTC}, nil)
			require.NoError(t, err)

			ts := time.Date(2025, 3, 25, 14, 27, 32, 0, time.UTC)
			dest := filepath.Join(exportDir, filepath.FromSlash(reattachClipRel("Dendrocopos major", 0.74, ts)))
			want := srcClip
			if preexisting {
				want = []byte("clip that was already there")
				require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o755))
				require.NoError(t, os.WriteFile(dest, want, 0o644))
			}

			src2, err := newBirdNetPiSource(t, dbPath)
			require.NoError(t, err)
			opts := imports.ImportOptions{
				SourceNode:     imports.DefaultSourceNode,
				Location:       time.UTC,
				IncludeAudio:   true,
				AudioSourceDir: audioSrc,
				ClipExportPath: exportDir,
			}
			stats, err := imports.NewEngine(noopClipNameRepo{v2repo}).Run(t.Context(), src2, &opts, nil)
			require.NoError(t, err)
			assert.Zero(t, stats.Reattached)

			data, readErr := os.ReadFile(dest)
			require.NoError(t, readErr, "a no-op update must never remove the clip file")
			assert.Equal(t, want, data)
		})
	}
}

// TestImport_ReattachMissReleasedUnderBothAliasKeys verifies that when a
// stored detection is claimed under one of its two dedup keys, a row matching
// the other key in the same batch does not drop it: after the claim misses and
// is released, a row in a later batch matching that other key still fills it.
func TestImport_ReattachMissReleasedUnderBothAliasKeys(t *testing.T) {
	stationZone := time.FixedZone("station", 2*3600)
	importZone := time.UTC
	// Stored 08:00 UTC: reachable under the keys 08:00 (import zone) and
	// 10:00 (station wall time).
	stored := birdnetPiRow{
		Date: "2025-06-01", Time: "08:00:00",
		SciName: "Parus major", ComName: comGreatTit,
		Confidence: 0.85, Lat: 60.0, Lon: 24.0, Cutoff: 0.5, Sens: 1.0,
		FileName: "missing.mp3",
	}
	alias := stored
	alias.Time = "10:00:00"
	alias.FileName = "tit.mp3"
	// Batch 1 (size 2): the 08:00 row claims the detection and misses; the
	// 10:00 row finds it claimed. Batch 2: the 10:00 row again, which must
	// still be able to fill it.
	rows := []birdnetPiRow{stored, alias, alias}

	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	titClip := []byte("tit audio")
	makeAudioTree(t, audioSrc, "2025-06-01", comGreatTit, "tit.mp3", titClip)

	repo, _ := newV2DetectionRepo(t, stationZone)
	src1, err := newBirdNetPiSource(t, newFixtureDB(t, []birdnetPiRow{stored}))
	require.NoError(t, err)
	_, err = imports.NewEngine(repo).Run(t.Context(), src1,
		&imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: importZone}, nil)
	require.NoError(t, err)

	src2, err := newBirdNetPiSource(t, newFixtureDB(t, rows))
	require.NoError(t, err)
	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       importZone,
		BatchSize:      2,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
	}
	stats, err := imports.NewEngine(repo).Run(t.Context(), src2, &opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Inserted)
	assert.Equal(t, 1, stats.Reattached, "the later row under the other alias key must fill the detection")
	assert.NotEmpty(t, clipNamesBySci(t, repo)["Parus major"])
}
