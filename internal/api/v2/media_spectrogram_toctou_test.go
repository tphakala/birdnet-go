// media_spectrogram_toctou_test.go: regression coverage for the spectrogram
// queue-key time-of-check/time-of-use bug.
//
// GenerateSpectrogramByID validates the clip path against Realtime.Audio.Export.Path
// at the handler boundary, builds the queue key from it, and returns that key to
// the client. The async worker must run under the SAME key. Before the fix the
// worker re-derived the path inside generateSpectrogram by re-reading the live
// Export.Path, so an export-path change between the handler and the worker made
// the worker run under a different key, orphaning the handler's queued entry and
// making GET /spectrogram/:id/status miss the in-flight job. The fix threads the
// handler-validated relative path into generateSpectrogramFromRel.
package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// TestGenerateSpectrogramFromRelIgnoresLiveExportPath pins that the worker entry
// point derives the spectrogram path and queue key solely from the relative
// audio path threaded in by the handler, never from the live
// Realtime.Audio.Export.Path. It proves this via the fast path: the spectrogram
// already exists at the path derived from relAudioPath, so the call returns it
// without generation even though the live Export.Path has since changed. If the
// worker re-read Export.Path it would normalize to a different path and miss the
// pre-created file.
func TestGenerateSpectrogramFromRelIgnoresLiveExportPath(t *testing.T) {
	withRestoredGlobalSettings(t)

	tmp := t.TempDir()
	sfs, err := securefs.New(tmp)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sfs.Close() })

	ctx := t.Context()

	controller := &Controller{
		SFS: sfs,
		ctx: ctx,
	}
	controller.Settings.Store(newValidTestSettings())

	const (
		width = SpectrogramSizeLg
		raw   = true
	)
	relAudioPath := "2024/06/04/Turdus_merula_80p.wav"
	_, _, _, relSpectrogramPath := buildSpectrogramPaths(relAudioPath, width, raw, "", "")

	// Pre-create the spectrogram on disk so the fast path returns without ffmpeg.
	full := filepath.Join(tmp, relSpectrogramPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte("PNG"), 0o600))

	// Publish a divergent live export path. A worker that re-read Export.Path to
	// derive its path/key (the pre-fix behaviour) would normalize the clip path
	// differently and miss the pre-created spectrogram.
	live := conf.CloneSettings(controller.Settings.Load())
	live.Realtime.Audio.Export.Path = filepath.Join(tmp, "changed-prefix")
	conftest.SetTestSettings(live)
	controller.Settings.Store(live)

	got, err := controller.generateSpectrogramFromRel(ctx, relAudioPath, "irrelevant/clip/path.wav", "", width, raw, "", "")
	require.NoError(t, err)
	assert.Equal(t, relSpectrogramPath, got,
		"generateSpectrogramFromRel must derive the spectrogram path from the threaded relAudioPath, not the live Export.Path")
}
