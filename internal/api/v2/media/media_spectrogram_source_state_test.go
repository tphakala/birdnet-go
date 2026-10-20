package media

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
)

const (
	// sourceStateClipAge is how far back a test dates a clip so readiness takes it
	// as settled without the stability wait.
	sourceStateClipAge = time.Hour
	// sourceStateRelAudio is the clip the tests render, relative to the clip root.
	sourceStateRelAudio = "2024/06/04/Turdus_merula_80p.wav"
	// sourceStateWAVHeaderBytes is the size of the header EncodeInterleaved writes
	// for a mono 16-bit WAV.
	sourceStateWAVHeaderBytes = 44
)

// nativeSourceStateHandler builds a Handler that renders natively into a fresh
// clip root, with no external binaries.
func nativeSourceStateHandler(t *testing.T) (h *Handler, root string) {
	t.Helper()
	withRestoredGlobalSettings(t)
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")

	root = t.TempDir()
	sfs, err := securefs.New(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sfs.Close() })

	settings := apitest.NewValidTestSettings()
	clearBinaryEnvironment(t, settings)
	core := &apicore.Core{SFS: sfs}
	core.SetTestContext(t.Context(), nil)
	h = &Handler{Core: core}
	h.Settings.Store(settings)
	h.spectrogramGenerator = spectrogram.NewGenerator(settings, sfs, nil)
	require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(sourceStateRelAudio)), 0o750))
	return h, root
}

// ageFile dates path sourceStateClipAge in the past.
func ageFile(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-sourceStateClipAge)
	require.NoError(t, os.Chtimes(path, old, old))
}

// requestSpectrogramStatus runs err through spectrogramHTTPError and returns the
// status code and body.
func requestSpectrogramStatus(t *testing.T, h *Handler, err error) (code int, body, retryAfter string) {
	t.Helper()
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
	_ = h.spectrogramHTTPError(c, err)
	return rec.Code, rec.Body.String(), rec.Header().Get("Retry-After")
}

func TestGenerateSpectrogramFromRel_ClipChangedAfterReadinessIs503ThenRenders(t *testing.T) {
	const width = SpectrogramSizeLg
	_, _, _, relSpec := buildSpectrogramPaths(sourceStateRelAudio, width, true, "", "", "")

	synctest.Test(t, func(t *testing.T) {
		// The handler and its contexts are made inside the bubble so the waiting
		// render is durably blocked.
		h, root := nativeSourceStateHandler(t)
		absAudio := filepath.Join(root, sourceStateRelAudio)
		absSpec := filepath.Join(root, relSpec)
		writeToneWAV(t, absAudio, nativeGateClipRate, nativeGateClipSeconds, fixtureToneHz)
		ageFile(t, absAudio)

		// Hold every generation slot, in a channel made inside the bubble, to model
		// the queue wait between the readiness check and the render.
		prior := spectrogramSemaphore
		spectrogramSemaphore = make(chan struct{}, maxConcurrentSpectrograms)
		t.Cleanup(func() { spectrogramSemaphore = prior })
		for range maxConcurrentSpectrograms {
			spectrogramSemaphore <- struct{}{}
		}

		var genErr error
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, genErr = h.generateSpectrogramFromRel(t.Context(), sourceStateRelAudio, sourceStateRelAudio, "", width, true, "", "", "")
		}()
		synctest.Wait() // readiness has passed and the render waits for a slot

		// The writer is still going: the clip is replaced by a longer one.
		writeToneWAV(t, absAudio, nativeGateClipRate, 2*nativeGateClipSeconds, fixtureToneHz)
		ageFile(t, absAudio)
		<-spectrogramSemaphore // free one slot
		<-done

		require.Error(t, genErr)
		code, _, retryAfter := requestSpectrogramStatus(t, h, genErr)
		assert.Equal(t, http.StatusServiceUnavailable, code, "a clip that changed after readiness is retryable")
		assert.NotEmpty(t, retryAfter)
		assert.NoFileExists(t, absSpec, "nothing is published for the stale clip")

		// Run two sees the stable file and renders it.
		for range maxConcurrentSpectrograms - 1 {
			<-spectrogramSemaphore
		}
		got, err := h.generateSpectrogramFromRel(t.Context(), sourceStateRelAudio, sourceStateRelAudio, "", width, true, "", "", "")
		require.NoError(t, err)
		assert.Equal(t, relSpec, got)
		assert.FileExists(t, absSpec)
	})
}

func TestGenerateSpectrogramFromRel_TruncatedWAVIs422AndNotCached(t *testing.T) {
	h, root := nativeSourceStateHandler(t)
	absAudio := filepath.Join(root, sourceStateRelAudio)
	writeToneWAV(t, absAudio, nativeGateClipRate, nativeGateClipSeconds, fixtureToneHz)
	whole, err := os.ReadFile(absAudio) //nolint:gosec // G304: test path
	require.NoError(t, err)
	cut := whole[:sourceStateWAVHeaderBytes+(len(whole)-sourceStateWAVHeaderBytes)/2]

	const width = SpectrogramSizeLg
	_, _, _, relSpec := buildSpectrogramPaths(sourceStateRelAudio, width, true, "", "", "")
	absSpec := filepath.Join(root, relSpec)
	generate := func() error {
		_, err := h.generateSpectrogramFromRel(t.Context(), sourceStateRelAudio, sourceStateRelAudio, "", width, true, "", "", "")
		return err
	}

	// The header declares the whole clip and the data stops halfway; the file is
	// old, so readiness cannot tell it from a finished one.
	require.NoError(t, os.WriteFile(absAudio, cut, 0o600))
	ageFile(t, absAudio)
	for attempt := range 2 {
		genErr := generate()
		require.ErrorIs(t, genErr, transcode.ErrTruncatedSource, "attempt %d", attempt)
		code, body, _ := requestSpectrogramStatus(t, h, genErr)
		assert.Equal(t, http.StatusUnprocessableEntity, code)
		assert.Contains(t, body, "Source audio is shorter than its header declares")
		assert.NoFileExists(t, absSpec, "a truncated clip leaves nothing cached, so a rerun decides again")
	}

	// The writer finishes: the next request renders and caches, the one after is
	// served from the cache.
	require.NoError(t, os.WriteFile(absAudio, whole, 0o600))
	ageFile(t, absAudio)
	require.NoError(t, generate())
	require.FileExists(t, absSpec)

	require.NoError(t, os.WriteFile(absSpec, []byte(nativeGateMarker), 0o600))
	require.NoError(t, generate())
	again, err := os.ReadFile(absSpec) //nolint:gosec // G304: test path
	require.NoError(t, err)
	assert.Equal(t, nativeGateMarker, string(again), "the cached PNG is served, not redrawn")
}
