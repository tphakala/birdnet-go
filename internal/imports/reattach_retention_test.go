package imports_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/imports"
)

// TestImport_ReattachSkipsRetentionPrunedRows verifies that a stored
// detection whose audio retention deleted (spectrogram_clip_name set,
// clip_name empty) is not a re-attach candidate: its source clip is neither
// copied nor charged against free space, and the row keeps its retention
// state.
func TestImport_ReattachSkipsRetentionPrunedRows(t *testing.T) {
	audioSrc := t.TempDir()
	exportDir := t.TempDir()
	rows := reattachRows()[:1]
	makeAudioTree(t, audioSrc, "2025-03-25", "Great Spotted Woodpecker", "woodpecker.mp3", []byte("woodpecker audio"))

	dbPath := newFixtureDB(t, rows)
	repo, db := newV2DetectionRepo(t, time.UTC)

	src1, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	_, err = imports.NewEngine(repo).Run(t.Context(), src1,
		&imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: time.UTC}, nil)
	require.NoError(t, err)

	// Retention deleted the audio of the stored row but kept its spectrogram.
	const retained = "2025/03/dendrocopos_major_74p_retained.mp3"
	require.NoError(t, db.Exec("UPDATE detections SET spectrogram_clip_name = ?", retained).Error)

	src2, err := newBirdNetPiSource(t, dbPath)
	require.NoError(t, err)
	opts := imports.ImportOptions{
		SourceNode:     imports.DefaultSourceNode,
		Location:       time.UTC,
		IncludeAudio:   true,
		AudioSourceDir: audioSrc,
		ClipExportPath: exportDir,
		// Any disk-space check would see the clip; report no free space so a
		// queued copy would fail the run.
		DiskSpaceFunc: func(string) (uint64, error) { return 0, nil },
	}
	stats, err := imports.NewEngine(repo).Run(t.Context(), src2, &opts, nil)
	require.NoError(t, err, "a retention-pruned row must not be charged against free space")
	assert.Zero(t, stats.Reattached)
	assert.Equal(t, 1, stats.Skipped)

	entries, err := os.ReadDir(exportDir)
	if err == nil {
		assert.Empty(t, entries, "no clip may be copied for a retention-pruned row")
	} else {
		require.ErrorIs(t, err, os.ErrNotExist)
	}

	var stored struct {
		ClipName            *string
		SpectrogramClipName *string
	}
	require.NoError(t, db.Raw("SELECT clip_name, spectrogram_clip_name FROM detections").Scan(&stored).Error)
	assert.True(t, stored.ClipName == nil || *stored.ClipName == "", "clip_name stays empty")
	require.NotNil(t, stored.SpectrogramClipName)
	assert.Equal(t, retained, *stored.SpectrogramClipName)
}
