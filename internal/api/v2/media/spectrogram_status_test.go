package media

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
	"github.com/tphakala/birdnet-go/internal/spectrogram/render"
)

const (
	// statusTestLowRate is a sample rate below the 8 kHz source bound.
	statusTestLowRate = 4000
	// statusTestTinyWidth is a legacy width below the supported minimum.
	statusTestTinyWidth = 50
	// sourceName is the clip the endpoint test serves.
	sourceName = "src.wav"
	// statusTestShortSamples is the sample count of the clip that must be too
	// short to render; TestProcessedSpectrogramByID_TooShortClipIs422 checks that.
	statusTestShortSamples = 2
)

// productionWrap wraps err the way the native generator and the handler do on
// the way to spectrogramHTTPError.
func productionWrap(err error) error {
	return fmt.Errorf("failed to generate spectrogram: %w",
		fmt.Errorf("native spectrogram generate_native_file: %w", err))
}

// probeError returns the error ProbeFile reports for path.
func probeError(t *testing.T, path string) error {
	t.Helper()
	_, err := transcode.ProbeFile(t.Context(), path)
	require.Error(t, err)
	return err
}

func TestSpectrogramHTTPError_SourceErrorsAre422(t *testing.T) {
	_, h, _ := setupMediaTestEnvironment(t)
	dir := t.TempDir()

	legacy := filepath.Join(dir, "legacy.aac")
	writeLegacyAAC(t, legacy)
	lowRate := filepath.Join(dir, "low.wav")
	writeToneWAV(t, lowRate, statusTestLowRate, 1, fixtureToneHz)
	corrupt := filepath.Join(dir, "corrupt.flac")
	writeCorrupt(t, corrupt)
	huge := filepath.Join(dir, "huge.wav")
	f, err := os.Create(huge)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(nativeReadyOversizeBytes))
	require.NoError(t, f.Close())

	_, decodeErr := transcode.DecodeMono(t.Context(), corrupt, 0)
	require.ErrorIs(t, decodeErr, transcode.ErrUndecodableSource)

	cut := filepath.Join(dir, "cut.wav")
	writeToneWAV(t, cut, nativeReadyRate, 1, fixtureToneHz)
	cutBytes, err := os.ReadFile(cut) //nolint:gosec // G304: test path
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cut, cutBytes[:len(cutBytes)/2], 0o600))
	_, truncatedErr := transcode.DecodeMono(t.Context(), cut, 0)
	require.ErrorIs(t, truncatedErr, transcode.ErrTruncatedSource)

	const (
		unsupportedMsg = "Source audio format is not supported for on-demand processing"
		undecodableMsg = "Source audio could not be decoded"
		tooLongMsg     = "Audio is too long for on-demand processing"
		tooShortMsg    = "Source audio is too short for a spectrogram"
		truncatedMsg   = "Source audio is shorter than its header declares"
	)
	tests := []struct {
		name string
		err  error
		msg  string
	}{
		{"unsupported extension", probeError(t, legacy), unsupportedMsg},
		{"rate below 8 kHz", probeError(t, lowRate), unsupportedMsg},
		{"undecodable at decode time", decodeErr, undecodableMsg},
		{"truncated wav", truncatedErr, truncatedMsg},
		{"too long", probeError(t, huge), tooLongMsg},
		{"no columns", errors.New(render.ErrNoColumns).Component("spectrogram").Category(errors.CategoryValidation).Build(), tooShortMsg},
		{"empty clip", errors.New(render.ErrEmptyClip).Component("spectrogram").Category(errors.CategoryValidation).Build(), tooShortMsg},
	}
	e := echo.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
			_ = h.spectrogramHTTPError(c, productionWrap(tt.err))
			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.msg)
		})
	}
}

func TestSpectrogramHTTPError_InvalidOptionsStays500(t *testing.T) {
	_, h, _ := setupMediaTestEnvironment(t)
	err := errors.New(render.ErrInvalidOptions).Component("spectrogram").Category(errors.CategoryValidation).Build()

	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
	_ = h.spectrogramHTTPError(c, productionWrap(err))

	assert.Equal(t, http.StatusInternalServerError, rec.Code,
		"after the parse-site width check no request value reaches ErrInvalidOptions, so it is a server fault")
}

func TestSpectrogramHTTPError_NotReadyWinsOverSourceStatus(t *testing.T) {
	_, h, _ := setupMediaTestEnvironment(t)
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.flac")
	writeNativeUndecodable(t, corrupt)
	legacy := filepath.Join(dir, "legacy.aac")
	require.NoError(t, os.WriteFile(legacy, make([]byte, nativeReadyUnsupportedBytes), 0o600))

	var notReadyErr, unsupportedErr error
	synctest.Test(t, func(t *testing.T) {
		_, notReadyErr = callNative(t.Context(), corrupt)
		_, unsupportedErr = callNative(t.Context(), legacy)
	})
	require.ErrorIs(t, notReadyErr, transcode.ErrUndecodableSource,
		"the probe-time cause satisfies both checks, which is why not-ready must be tested first")

	e := echo.New()
	call := func(err error) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
		_ = h.spectrogramHTTPError(c, err)
		return rec
	}

	rec := call(notReadyErr)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "2", rec.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusUnprocessableEntity, call(unsupportedErr).Code)
}

func TestSpectrogramSourceStatus_NotReadyIsNotASourceFailure(t *testing.T) {
	t.Parallel()

	notReadyAndUndecodable := fmt.Errorf("%w: %w", ffmpeg.ErrAudioFileNotReady, transcode.ErrUndecodableSource)
	tests := []struct {
		name       string
		err        error
		wantReady  bool
		wantSource bool
	}{
		{"not ready wrapping undecodable", notReadyAndUndecodable, true, false},
		{"incomplete", ffmpeg.ErrAudioFileIncomplete, true, false},
		{"source changed wrapping truncated", fmt.Errorf("%w: after render: %w", spectrogram.ErrSourceChanged, transcode.ErrTruncatedSource), true, false},
		{"truncated source", productionWrap(transcode.ErrTruncatedSource), false, true},
		{"unsupported source", productionWrap(transcode.ErrUnsupportedSource), false, true},
		{"no columns", productionWrap(render.ErrNoColumns), false, true},
		{"invalid options", productionWrap(render.ErrInvalidOptions), false, false},
		{"unknown", errors.NewStd("boom"), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantReady, isSpectrogramNotReady(tt.err))
			assert.Equal(t, tt.wantSource, isSpectrogramSourceFailure(tt.err))
		})
	}
}

func TestParseSpectrogramParameters_LegacyWidthBounds(t *testing.T) {
	_, h, _ := setupMediaTestEnvironment(t)

	tests := []struct {
		name      string
		query     string
		wantWidth int
		wantErr   bool
	}{
		{"below minimum", "width=99", 0, true},
		{"minimum", "width=100", 100, false},
		{"maximum", "width=2000", 2000, false},
		{"above maximum", "width=2001", 0, true},
		{"zero", "width=0", 0, true},
		{"negative", "width=-5", 0, true},
		{"not an integer", "width=abc", SpectrogramSizeLg, false},
		{"size wins over width", "size=md&width=50", SpectrogramSizeMd, false},
		{"bogus size is ignored", "size=bogus", SpectrogramSizeLg, false},
		{"no parameters", "", SpectrogramSizeLg, false},
	}
	e := echo.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := e.NewContext(httptest.NewRequest(http.MethodGet, "/?"+tt.query, http.NoBody), httptest.NewRecorder())
			params, err := h.parseSpectrogramParameters(c)
			if tt.wantErr {
				require.ErrorIs(t, err, errInvalidSpectrogramWidth)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantWidth, params.width)
		})
	}
}

// dirNames lists the entry names of dir.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestSpectrogramEndpoints_RejectOutOfRangeWidthBeforeWork(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	e, h, tempDir := setupMediaTestEnvironment(t)

	writeToneWAV(t, fixturePath(tempDir, sourceName), 48000, 1, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("Get", "30").Return(datastore.Note{}, errors.NewStd("not found")).Maybe()
	mockDS.On("GetNoteClipPath", "30").Return(sourceName, nil).Maybe()
	mockDS.On("GetNoteModelType", "30").Return("bird", nil).Maybe()
	h.DS = mockDS
	for range cap(h.processingSemaphore) {
		h.processingSemaphore <- struct{}{}
	}

	width := url.Values{"width": {fmt.Sprint(statusTestTinyWidth)}}.Encode()
	requests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"filename route", http.MethodGet, "/api/v2/media/spectrogram/" + sourceName, ""},
		{"by id", http.MethodGet, "/api/v2/spectrogram/30", ""},
		{"status", http.MethodGet, "/api/v2/spectrogram/30/status", ""},
		{"generate", http.MethodPost, "/api/v2/spectrogram/30/generate", ""},
		{"processed", http.MethodPost, "/api/v2/spectrogram/30/process", `{"normalize": true}`},
	}
	for _, r := range requests {
		t.Run(r.name, func(t *testing.T) {
			do := func(query string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(r.method, r.path+"?"+query, strings.NewReader(r.body))
				if r.body != "" {
					req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				}
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				return rec
			}

			before := dirNames(t, tempDir)
			assert.Equal(t, http.StatusBadRequest, do(width).Code)
			assert.Equal(t, before, dirNames(t, tempDir), "a rejected width creates no PNG and no temp dir")

			assert.NotEqual(t, http.StatusBadRequest, do("size=md").Code,
				"size keeps working: the frontend sends it on every request")
		})
	}
}

func TestProcessedSpectrogramByID_TooShortClipIs422(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	e, h, tempDir := setupMediaTestEnvironment(t)
	clearBinaryEnvironment(t, h.Settings.Load())

	writeToneWAV(t, fixturePath(tempDir, "tiny.wav"), 48000, float64(statusTestShortSamples)/48000, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "31").Return("tiny.wav", nil)
	mockDS.On("GetNoteModelType", "31").Return("bird", nil).Maybe()
	h.DS = mockDS

	rec := postJSON(e, "/api/v2/spectrogram/31/process", `{"normalize": true}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// requireLogLevel asserts that the captured output holds msg on a line of the given level.
func requireLogLevel(t *testing.T, output, msg, level string) {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, msg) {
			assert.Contains(t, line, "level="+level)
			return
		}
	}
	require.Failf(t, "log line not found", "log line %q not found in %q", msg, output)
}

func TestServeSpectrogramByID_RetainedRenderRejectsOutOfRangeWidth(t *testing.T) {
	h, e, _ := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	h.DS = noClipDS(t, retainedClipName, false)

	rec := serveSpectrogram(t, h, e, "?width="+fmt.Sprint(statusTestTinyWidth))

	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestServeSpectrogramByID_UnsupportedSourceIs422(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	e, h, tempDir := setupMediaTestEnvironment(t)
	clearBinaryEnvironment(t, h.Settings.Load())

	require.NoError(t, os.WriteFile(fixturePath(tempDir, "legacy.aac"), make([]byte, nativeReadyUnsupportedBytes), 0o600))
	before := dirNames(t, tempDir)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "32").Return("legacy.aac", nil)
	mockDS.On("GetNoteModelType", "32").Return("bird", nil).Maybe()
	h.DS = mockDS

	logs := logtest.CaptureBuffer(t)
	h.APILogger = logger.Global().Module("api")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/spectrogram/32", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	requireLogLevel(t, logs.String(), "Spectrogram generation skipped: source audio cannot be rendered", "WARN")
	assert.NotContains(t, logs.String(), `level=ERROR msg="Spectrogram generation failed"`, "a source failure is not logged as an error")
	assert.Equal(t, before, dirNames(t, tempDir), "a rejected clip leaves no PNG behind")
}

func TestSpectrogramFailureReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			"not ready wrapping a source failure is checked before it",
			fmt.Errorf("%w: %w", ffmpeg.ErrAudioFileNotReady, transcode.ErrUndecodableSource),
			spectrogramReasonSourcePending,
		},
		{
			"source that did not appear within the wait is never terminal",
			fmt.Errorf("%w: %w (path: a.wav)", ErrAudioFileNotFound, os.ErrNotExist),
			spectrogramReasonSourcePending,
		},
		{"unrenderable source", productionWrap(transcode.ErrUnsupportedSource), spectrogramReasonSourceInvalid},
		{
			"slot timeout",
			fmt.Errorf("timeout waiting for generation slot: %w", context.DeadlineExceeded),
			spectrogramReasonBusy,
		},
		{"canceled", context.Canceled, spectrogramReasonBusy},
		{"plain failure", errors.NewStd("boom"), spectrogramReasonRenderFailed},
		{
			"output PNG missing after generation is a render failure",
			fmt.Errorf("%w: spectrogram file missing after generation: %w", ErrSpectrogramGeneration, os.ErrNotExist),
			spectrogramReasonRenderFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, spectrogramFailureReason(tt.err))
		})
	}
}
