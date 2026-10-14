package media

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

const (
	batTestRate       = 256000
	batTestSeconds    = 0.25
	batTestExpansion  = 10
	processTestRate   = 44100
	processTestLength = 1.0
)

// postJSON posts body to path.
func postJSON(e http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// wavHeader returns the sample rate and data length declared by a WAV body.
func wavRateAndLength(t *testing.T, body []byte) (rate, dataLen int) {
	t.Helper()
	require.GreaterOrEqual(t, len(body), 44)
	require.Equal(t, "RIFF", string(body[:4]))
	rate = int(body[24]) | int(body[25])<<8 | int(body[26])<<16 | int(body[27])<<24
	dataLen = len(body) - 44
	return rate, dataLen
}

func TestProcessAudioByID(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	src := fixturePath(tempDir, "src.flac")
	writeToneFLAC(t, src, processTestRate, processTestLength, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "7").Return("src.flac", nil)
	controller.DS = mockDS

	const body = `{"normalize": true, "denoise": "light", "gain_db": 2}`
	rec := postJSON(e, "/api/v2/audio/7/process", body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, MimeTypeWAV, rec.Header().Get("Content-Type"))
	rate, dataLen := wavRateAndLength(t, rec.Body.Bytes())
	assert.Equal(t, processTestRate, rate, "the source rate is kept")
	assert.Equal(t, processTestRate*2, dataLen, "one second of 16-bit mono")
	first := bytes.Clone(rec.Body.Bytes())

	// The second identical request is served from the cache: the source now holds
	// undecodable bytes, so only a cache hit can answer 200. (The handler stats
	// the clip before consulting the cache, so the file must still exist.)
	require.NoError(t, os.WriteFile(src, []byte("not audio"), 0o600))
	rec = postJSON(e, "/api/v2/audio/7/process", body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, first, rec.Body.Bytes(), "a cache hit skips processing")
}

func TestProcessAudioByID_AppliesGain(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	writeToneWAV(t, fixturePath(tempDir, "src.wav"), processTestRate, processTestLength, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "8").Return("src.wav", nil)
	controller.DS = mockDS

	rec := postJSON(e, "/api/v2/audio/8/process", `{"gain_db": -6.0206}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// The source tone peaks at fixtureAmp of full scale; -6 dB halves it.
	var peak int
	body := rec.Body.Bytes()[44:]
	for i := 0; i+1 < len(body); i += 2 {
		v := int(int16(uint16(body[i]) | uint16(body[i+1])<<8)) //nolint:gosec // G115: PCM bit reinterpretation in a test
		peak = max(peak, max(v, -v))
	}
	assert.InDelta(t, fixtureAmp*math.MaxInt16/2, float64(peak), 4)
}

func TestProcessAudioByID_Rejections(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	writeHEAAC(t, fixturePath(tempDir, "he.m4a"))
	writeCorrupt(t, fixturePath(tempDir, "corrupt.flac"))
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "he").Return("he.m4a", nil)
	mockDS.On("GetNoteClipPath", "corrupt").Return("corrupt.flac", nil)
	controller.DS = mockDS

	assert.Equal(t, http.StatusBadRequest, postJSON(e, "/api/v2/audio/he/process", `{"denoise": "extreme"}`).Code)
	assert.Equal(t, http.StatusBadRequest, postJSON(e, "/api/v2/audio/he/process", `{"gain_db": 61}`).Code)

	rec := postJSON(e, "/api/v2/audio/he/process", `{"normalize": true}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "HE-AAC")

	rec = postJSON(e, "/api/v2/audio/corrupt/process", `{"normalize": true}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestProcessAudioByID_SemaphoreFullIs503(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	writeToneWAV(t, fixturePath(tempDir, "src.wav"), processTestRate, 0.2, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "7").Return("src.wav", nil)
	controller.DS = mockDS

	for range cap(controller.processingSemaphore) {
		controller.processingSemaphore <- struct{}{}
	}
	rec := postJSON(e, "/api/v2/audio/7/process", `{"normalize": true}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestAudibleBatsByID(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	writeToneWAV(t, fixturePath(tempDir, "bat.wav"), batTestRate, batTestSeconds, fixtureBatToneHz)
	writeToneWAV(t, fixturePath(tempDir, "low.wav"), 48000, 0.2, fixtureToneHz)
	writeHEAAC(t, fixturePath(tempDir, "he.m4a"))
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteModelType", "10").Return("bat", nil)
	mockDS.On("GetNoteModelType", "11").Return("bat", nil)
	mockDS.On("GetNoteModelType", "12").Return("bat", nil)
	mockDS.On("GetNoteModelType", "13").Return("bird", nil)
	mockDS.On("GetNoteClipPath", "10").Return("bat.wav", nil)
	mockDS.On("GetNoteClipPath", "11").Return("low.wav", nil)
	mockDS.On("GetNoteClipPath", "12").Return("he.m4a", nil)
	controller.DS = mockDS

	rec := postJSON(e, "/api/v2/audio/10/audible-bats", `{"expansion": 10, "normalize": true, "gain_db": 1}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, MimeTypeWAV, rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	rate, dataLen := wavRateAndLength(t, rec.Body.Bytes())
	assert.Equal(t, transcode.AudibleBatsOutputSampleRate, rate)
	wantBytes := batTestSeconds * batTestExpansion * float64(transcode.AudibleBatsOutputSampleRate) * 2
	assert.InDelta(t, wantBytes, float64(dataLen), 8192)

	rec = postJSON(e, "/api/v2/audio/11/audible-bats", `{"expansion": 10}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "a 48 kHz source cannot carry ultrasound")

	rec = postJSON(e, "/api/v2/audio/12/audible-bats", `{"expansion": 10}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "HE-AAC")

	rec = postJSON(e, "/api/v2/audio/13/audible-bats", `{"expansion": 10}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "only bat detections are eligible")

	assert.Equal(t, http.StatusBadRequest, postJSON(e, "/api/v2/audio/10/audible-bats", `{"expansion": 7}`).Code)
}

func TestProcessedSpectrogramByID_UndecodableSourceIs422(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	writeCorrupt(t, fixturePath(tempDir, "corrupt.flac"))
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "20").Return("corrupt.flac", nil)
	controller.DS = mockDS

	rec := postJSON(e, "/api/v2/spectrogram/20/process", `{"normalize": true}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "the source is rejected before any rendering")

	entries, err := os.ReadDir(filepath.Join(tempDir, ".tmp-processing"))
	require.NoError(t, err)
	assert.Empty(t, entries, "no temp file outlives the request")
}

func TestProcessedSpectrogramByID_Renders(t *testing.T) {
	// Rendering belongs to the spectrogram generator, which still shells out to
	// SoX and FFmpeg; only the processing step in front of it is native.
	soxPath, soxErr := exec.LookPath("sox")
	ffmpegPath, ffmpegErr := exec.LookPath("ffmpeg")
	if soxErr != nil || ffmpegErr != nil {
		t.Skip("sox and ffmpeg are required by the spectrogram renderer")
	}
	e, controller, tempDir := setupMediaTestEnvironment(t)
	controller.Settings.Load().Realtime.Audio.SoxPath = soxPath
	controller.Settings.Load().Realtime.Audio.FfmpegPath = ffmpegPath

	writeToneWAV(t, fixturePath(tempDir, "src.wav"), 48000, 1, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "21").Return("src.wav", nil)
	mockDS.On("GetNoteModelType", "21").Return("bird", nil).Maybe()
	controller.DS = mockDS

	rec := postJSON(e, "/api/v2/spectrogram/21/process", `{"normalize": true, "denoise": "light"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.True(t, bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")))

	entries, err := os.ReadDir(filepath.Join(tempDir, ".tmp-processing"))
	require.NoError(t, err)
	assert.Empty(t, entries, "the processed WAV and the PNG are removed with the request")
}

func TestTranscodeStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err    error
		status int
	}{
		{transcode.ErrHEAACSource, http.StatusUnprocessableEntity},
		{transcode.ErrUnsupportedSource, http.StatusUnprocessableEntity},
		{transcode.ErrUndecodableSource, http.StatusUnprocessableEntity},
		{transcode.ErrAudioTooLong, http.StatusUnprocessableEntity},
		{transcode.ErrEmptyRange, http.StatusUnprocessableEntity},
		{transcode.ErrInvalidRequest, http.StatusBadRequest},
	}
	for _, tt := range tests {
		status, msg, ok := transcodeStatus(tt.err)
		assert.Truef(t, ok, "%v", tt.err)
		assert.Equal(t, tt.status, status)
		assert.NotEmpty(t, msg)
	}
	_, _, ok := transcodeStatus(context.DeadlineExceeded)
	assert.False(t, ok, "unclassified errors stay 500")

	_, msg, _ := transcodeStatus(transcode.ErrHEAACSource)
	assert.Contains(t, msg, "HE-AAC", "HE-AAC gets its own message")
}
