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
package media

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
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

	controllerCore := &apicore.Core{SFS: sfs}
	controllerCore.SetTestContext(ctx, nil)
	controller := &Handler{Core: controllerCore}
	controller.Settings.Store(apitest.NewValidTestSettings())

	const (
		width = SpectrogramSizeLg
		raw   = true
	)
	relAudioPath := "2024/06/04/Turdus_merula_80p.wav"
	_, _, _, relSpectrogramPath := buildSpectrogramPaths(relAudioPath, width, raw, "", "", "")

	// Pre-create the spectrogram on disk so the fast path returns without rendering.
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

	got, err := controller.generateSpectrogramFromRel(ctx, relAudioPath, "irrelevant/clip/path.wav", "", width, raw, "", "", "", 0)
	require.NoError(t, err)
	assert.Equal(t, relSpectrogramPath, got,
		"generateSpectrogramFromRel must derive the spectrogram path from the threaded relAudioPath, not the live Export.Path")
}

func TestFindExistingSpectrogramFallback(t *testing.T) {
	withRestoredGlobalSettings(t)

	tmp := t.TempDir()
	sfs, err := securefs.New(tmp)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sfs.Close() })

	ctx := t.Context()

	controllerCore := &apicore.Core{SFS: sfs}
	controllerCore.SetTestContext(ctx, nil)
	controller := &Handler{Core: controllerCore}
	controller.Settings.Store(apitest.NewValidTestSettings())

	relAudioPath := "2024/06/04/Turdus_merula_80p.wav"

	// Create only the 'md' spectrogram on disk
	_, _, _, mdSpectrogramPath := buildSpectrogramPaths(relAudioPath, SpectrogramSizeMd, true, "", "", "")
	full := filepath.Join(tmp, mdSpectrogramPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte("PNG_MD_DATA"), 0o600))

	// 1. Calling findExistingSpectrogram requesting sm should fallback to the existing larger md render
	foundPath, exists := controller.findExistingSpectrogram(relAudioPath, SpectrogramSizeSm, true, "", "", "", 0)
	require.True(t, exists, "findExistingSpectrogram should find the existing larger md spectrogram")
	assert.Equal(t, mdSpectrogramPath, foundPath)

	// 2. Calling findExistingSpectrogram requesting lg should NOT return the smaller md render
	_, existsSmaller := controller.findExistingSpectrogram(relAudioPath, SpectrogramSizeLg, true, "", "", "", 0)
	assert.False(t, existsSmaller, "findExistingSpectrogram should not serve a render smaller than requested")

	// 3. Positive overlayRate (sr) must skip fallback and require exact render only
	_, existsOverlay := controller.findExistingSpectrogram(relAudioPath, SpectrogramSizeSm, true, "", "", "", 48000)
	assert.False(t, existsOverlay, "findExistingSpectrogram must skip fallback when overlayRate is positive")

	// 4. generateSpectrogramFromRel with requested width=sm should return the existing larger md path without generating
	got, err := controller.generateSpectrogramFromRel(ctx, relAudioPath, "clip.wav", "", SpectrogramSizeSm, true, "", "", "", 0)
	require.NoError(t, err)
	assert.Equal(t, mdSpectrogramPath, got)

	// 5. Test legacy/pre-rendered <basename>.png fallback
	writePNG := func(path string, w, h int) {
		t.Helper()
		f, err := os.Create(path) //nolint:gosec // test TempDir path
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		require.NoError(t, png.Encode(f, image.NewNRGBA(image.Rect(0, 0, w, h))))
	}

	relAudioBare := "2024/06/04/Turdus_bare.wav"
	barePath := "2024/06/04/Turdus_bare.png"
	fullBare := filepath.Join(tmp, barePath)
	writePNG(fullBare, SpectrogramSizeSm, 129)

	specSettings := apitest.NewValidTestSettings()
	specSettings.Realtime.Dashboard.Spectrogram.Size = "sm"
	specSettings.Realtime.Dashboard.Spectrogram.Raw = true
	specSettings.Realtime.Dashboard.Spectrogram.Style = "default"
	specSettings.Realtime.Dashboard.Spectrogram.DynamicRange = "100"
	controller.Settings.Store(specSettings)

	foundBare, existsBare := controller.findExistingSpectrogram(relAudioBare, SpectrogramSizeSm, true, "", "", "", 0)
	require.True(t, existsBare, "findExistingSpectrogram should find bare .png when raw/style match and width >= requestedWidth")
	assert.Equal(t, barePath, foundBare)

	// Should NOT match larger size even when settings later increase to lg (PNG width is still 258px)
	specSettingsLg := apitest.NewValidTestSettings()
	specSettingsLg.Realtime.Dashboard.Spectrogram.Size = "lg"
	specSettingsLg.Realtime.Dashboard.Spectrogram.Raw = true
	controller.Settings.Store(specSettingsLg)
	_, existsBareMd := controller.findExistingSpectrogram(relAudioBare, SpectrogramSizeMd, true, "", "", "", 0)
	assert.False(t, existsBareMd, "findExistingSpectrogram must inspect PNG width so a 258px bare .png is never served for md/lg")

	// When bare .png actually has lg dimensions (1026px), lg/md/sm match while xl rejects
	writePNG(fullBare, SpectrogramSizeLg, 513)
	_, existsBareLg := controller.findExistingSpectrogram(relAudioBare, SpectrogramSizeLg, true, "", "", "", 0)
	assert.True(t, existsBareLg, "findExistingSpectrogram should reuse a 1026px bare .png for lg")
	_, existsBareXl := controller.findExistingSpectrogram(relAudioBare, SpectrogramSizeXl, true, "", "", "", 0)
	assert.False(t, existsBareXl, "findExistingSpectrogram must not reuse a 1026px bare .png for xl")

	// Should NOT match when requested raw or style differs from prerenderer config
	_, existsNonRaw := controller.findExistingSpectrogram(relAudioBare, SpectrogramSizeSm, false, "", "", "", 0)
	assert.False(t, existsNonRaw, "findExistingSpectrogram should not match bare .png when raw disagrees with config")
	_, existsStyle := controller.findExistingSpectrogram(relAudioBare, SpectrogramSizeSm, true, "scientific_dark", "", "", 0)
	assert.False(t, existsStyle, "findExistingSpectrogram should not match bare .png when style disagrees with config")
	_, existsFreq := controller.findExistingSpectrogram(relAudioBare, SpectrogramSizeSm, true, "", "", "-bat", 0)
	assert.False(t, existsFreq, "findExistingSpectrogram should not match bare .png when freqSuffix is set")

	// If config itself uses raw=false, raw=true requests must reject bare .png
	specNonRaw := apitest.NewValidTestSettings()
	specNonRaw.Realtime.Dashboard.Spectrogram.Size = "lg"
	specNonRaw.Realtime.Dashboard.Spectrogram.Raw = false
	controller.Settings.Store(specNonRaw)
	_, existsMismatchedRaw := controller.findExistingSpectrogram(relAudioBare, SpectrogramSizeSm, true, "", "", "", 0)
	assert.False(t, existsMismatchedRaw, "findExistingSpectrogram must reject non-raw bare .png for raw=true requests")
}
