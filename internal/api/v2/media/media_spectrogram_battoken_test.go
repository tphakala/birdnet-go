// media_spectrogram_battoken_test.go: coverage for the bat-v3 cache token and the
// exact-only serving of overlay (sr) requests.

package media

import (
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	batQuery        = "?size=md&raw=true"
	batQueryWithSR  = "?size=md&raw=true&sr=192000"
	batClipRate     = 192000
	batClipSeconds  = 0.5
	batToneHz       = 80000.0
	batRowTolerance = 2.0
	batBird514      = "2026/01/a_514px.png"
	batV2File       = "2026/01/a_514px-bat-v2.png"
	batLegacyFile   = "2026/01/a_514px-bat.png"
	batV3File       = "2026/01/a_514px-bat-v3.png"
)

// batDS returns a mock for a bat detection whose clip is still set.
func batDS(t *testing.T) *mocks.MockInterface {
	t.Helper()
	ds := mocks.NewMockInterface(t)
	ds.EXPECT().GetNoteClipPath(retainedNoteID).Return(retainedClipName, nil)
	ds.EXPECT().Get(retainedNoteID).Return(datastore.Note{ClipName: retainedClipName}, nil).Maybe()
	ds.EXPECT().GetNoteModelType(retainedNoteID).Return("bat", nil).Maybe()
	return ds
}

// batNoClipDS returns a mock for a bat detection whose audio was removed.
func batNoClipDS(t *testing.T) *mocks.MockInterface {
	t.Helper()
	ds := mocks.NewMockInterface(t)
	ds.EXPECT().GetNoteClipPath(retainedNoteID).Return("", repository.ErrNoClipPath)
	ds.EXPECT().GetNoteKeptSpectrogram(retainedNoteID).Return(retainedClipName, "bat", nil)
	return ds
}

func fileExists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// requireToneRow asserts the brightest row of the PNG at root/rel sits where a tone
// at toneHz belongs for a spectrogram that spans 0 to nyquistHz.
func requireToneRow(t *testing.T, root, rel string, toneHz, nyquistHz float64) {
	t.Helper()
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel))) //nolint:gosec // G304: test path
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	require.NoError(t, err)
	pal, ok := img.(*image.Paletted)
	require.True(t, ok, "native output is a paletted PNG")
	height := pal.Bounds().Dy()
	x := pal.Bounds().Dx() / 2
	best, row := -1, -1
	for y := range height {
		if v := int(pal.ColorIndexAt(x, y)); v > best {
			best, row = v, y
		}
	}
	want := (1 - toneHz/nyquistHz) * float64(height-1)
	assert.InDelta(t, want, float64(row), batRowTolerance)
}

func TestServeSpectrogramByID_BatTokenTransition(t *testing.T) {

	// a, b: a fresh native-rate render supersedes the older files, then is served.
	t.Run("generates bat-v3 and removes superseded files, then serves the fast path", func(t *testing.T) {
		h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
		require.NoError(t, os.MkdirAll(filepath.Join(root, "2026", "01"), 0o750))
		writeToneWAV(t, filepath.Join(root, "2026", "01", "a.wav"), batClipRate, batClipSeconds, batToneHz)
		writeRetainedFile(t, root, batBird514, "bird")
		writeRetainedFile(t, root, batV2File, "old 0-128 kHz render")
		writeRetainedFile(t, root, batLegacyFile, "older render")
		h.DS = batDS(t)

		rec := serveSpectrogram(t, h, e, batQueryWithSR)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		requireToneRow(t, root, batV3File, batToneHz, batClipRate/2)
		assert.False(t, fileExists(root, batBird514), "bird sibling removed")
		assert.False(t, fileExists(root, batV2File), "bat-v2 sibling removed")
		assert.False(t, fileExists(root, batLegacyFile), "legacy bat sibling removed")

		// b: the second request serves the existing file without redrawing it.
		writeRetainedFile(t, root, batV3File, "marker")
		h.DS = batDS(t)
		rec = serveSpectrogram(t, h, e, batQueryWithSR)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "marker", rec.Body.String())
	})

	t.Run("spectrogram-only without sr serves the nearest older render", func(t *testing.T) {
		h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
		writeRetainedFile(t, root, batV2File, "bat-v2 render")
		h.DS = batNoClipDS(t)

		rec := serveSpectrogram(t, h, e, batQuery)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "bat-v2 render", rec.Body.String())
	})

	t.Run("spectrogram-only with sr answers 404 instead of a substitute", func(t *testing.T) {
		h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
		writeRetainedFile(t, root, batV2File, "bat-v2 render")
		h.DS = batNoClipDS(t)

		rec := serveSpectrogram(t, h, e, batQueryWithSR)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Empty(t, rec.Header().Get("Cache-Control"))
	})

	t.Run("spectrogram-only with sr serves the exact bat-v3 render", func(t *testing.T) {
		h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
		writeRetainedFile(t, root, batV3File, "bat-v3 render")
		writeRetainedFile(t, root, batV2File, "bat-v2 render")
		h.DS = batNoClipDS(t)

		rec := serveSpectrogram(t, h, e, batQueryWithSR)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "bat-v3 render", rec.Body.String())
		assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable")
	})

	t.Run("clip vanished with only bat-v2 and sr answers 404", func(t *testing.T) {
		h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
		writeRetainedFile(t, root, batV2File, "bat-v2 render")
		h.DS = batDS(t)

		rec := serveSpectrogram(t, h, e, batQueryWithSR)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.NotEqual(t, "bat-v2 render", rec.Body.String())
		assert.Empty(t, rec.Header().Get("Cache-Control"))
	})
}

// newModelLookupFailsHandler builds a handler for the retained bat clip whose
// model type lookup always fails. GetNoteClipPath and Get are incidental (the
// serve and generate paths call the first, status the second); the failing
// GetNoteModelType call is mandatory.
func newModelLookupFailsHandler(t *testing.T, mode string) (*Handler, *echo.Echo, string) {
	t.Helper()
	h, e, root := newRetainedTestHandler(t, mode)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "2026", "01"), 0o750))
	writeToneWAV(t, filepath.Join(root, "2026", "01", "a.wav"), batClipRate, batClipSeconds, batToneHz)
	ds := mocks.NewMockInterface(t)
	ds.EXPECT().GetNoteClipPath(retainedNoteID).Return(retainedClipName, nil).Maybe()
	ds.EXPECT().Get(retainedNoteID).Return(datastore.Note{ClipName: retainedClipName}, nil).Maybe()
	ds.EXPECT().GetNoteModelType(retainedNoteID).Return("", errors.NewStd("model lookup failed"))
	h.DS = ds
	return h, e, root
}

// TestServeSpectrogramByID_OverlayRateRefusesBirdFallback verifies that a request
// carrying sr never gets a bird-profile render when the model type lookup fails:
// it answers 503 with Retry-After and generates and serves nothing. A request
// without sr keeps the bird fallback.
func TestServeSpectrogramByID_OverlayRateRefusesBirdFallback(t *testing.T) {

	t.Run("sr request answers 503 and generates nothing", func(t *testing.T) {
		h, e, root := newModelLookupFailsHandler(t, conf.SpectrogramModeAuto)

		rec := serveSpectrogram(t, h, e, batQueryWithSR)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
		assert.NotEmpty(t, rec.Header().Get("Retry-After"))
		assert.False(t, fileExists(root, batBird514), "no bird-profile render written")
		assert.False(t, fileExists(root, batV3File), "no bat render written")
	})

	t.Run("request without sr falls back to the bird profile", func(t *testing.T) {
		h, e, root := newModelLookupFailsHandler(t, conf.SpectrogramModeAuto)

		rec := serveSpectrogram(t, h, e, batQuery)

		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.True(t, fileExists(root, batBird514), "bird-profile render written")
	})
}

// TestSpectrogramByID_ModelTypeLookupFailureIs503 verifies that generate and
// status answer a retryable 503 instead of acting on a guessed bird profile when
// the model type lookup fails, and that nothing is queued or written.
func TestSpectrogramByID_ModelTypeLookupFailureIs503(t *testing.T) {

	// queueKey returns the queue key the handlers derive for batQuery, cleared
	// before the test and again on cleanup (the queue is package-global).
	queueKey := func(t *testing.T, h *Handler) string {
		t.Helper()
		spec := h.CurrentSettings().Realtime.Dashboard.Spectrogram
		key := buildSpectrogramQueueKey(retainedNoteID, SpectrogramSizeMd, true, spec.Style, spec.DynamicRange)
		spectrogramQueue.Delete(key)
		t.Cleanup(func() { spectrogramQueue.Delete(key) })
		return key
	}

	t.Run("generate answers 503 and queues and writes nothing", func(t *testing.T) {
		h, e, root := newModelLookupFailsHandler(t, conf.SpectrogramModeUserRequested)
		key := queueKey(t, h)

		req := httptest.NewRequest(http.MethodPost, "/api/v2/spectrogram/"+retainedNoteID+"/generate"+batQuery, http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(retainedNoteID)
		_ = h.GenerateSpectrogramByID(c)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
		assert.NotEmpty(t, rec.Header().Get("Retry-After"))
		_, queued := spectrogramQueue.Load(key)
		assert.False(t, queued, "no queue entry")
		// The queue entry is written synchronously before any worker is spawned, so
		// its absence shows no worker started. The file checks are best effort.
		assert.False(t, fileExists(root, batBird514), "no bird-profile render written")
		assert.False(t, fileExists(root, batV3File), "no bat render written")
	})

	t.Run("status answers 503 instead of reporting a file under a guessed profile", func(t *testing.T) {
		h, e, root := newModelLookupFailsHandler(t, conf.SpectrogramModeUserRequested)
		writeRetainedFile(t, root, batBird514, "bird")

		req := httptest.NewRequest(http.MethodGet, "/api/v2/spectrogram/"+retainedNoteID+"/status"+batQuery, http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(retainedNoteID)
		_ = h.GetSpectrogramStatus(c)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
		assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	})

	t.Run("status reports the file under the resolved profile token", func(t *testing.T) {
		h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeUserRequested)
		ds := mocks.NewMockInterface(t)
		ds.EXPECT().Get(retainedNoteID).Return(datastore.Note{ClipName: retainedClipName}, nil)
		ds.EXPECT().GetNoteModelType(retainedNoteID).Return("bat", nil)
		h.DS = ds
		writeRetainedFile(t, root, batV3File, "marker")

		req := httptest.NewRequest(http.MethodGet, "/api/v2/spectrogram/"+retainedNoteID+"/status"+batQuery, http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(retainedNoteID)
		_ = h.GetSpectrogramStatus(c)

		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "exists")
	})
}

func TestParseSpectrogramParameters_OverlayRate(t *testing.T) {
	tests := []struct {
		query string
		want  int
	}{
		{"", 0},
		{"?sr=384000", 384000},
		{"?sr=abc", 0},
		{"?sr=", 0},
		{"?sr=-5", 0},
		{"?sr=1.5", 0},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			h, e, _ := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
			req := httptest.NewRequest(http.MethodGet, "/api/v2/spectrogram/1"+tt.query, http.NoBody)
			c := e.NewContext(req, nil)
			params, err := h.parseSpectrogramParameters(c)
			require.NoError(t, err)
			assert.Equal(t, tt.want, params.overlayRate)
		})
	}
}
