package media

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	wavpcm "github.com/tphakala/go-wav/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
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
	assert.InDelta(t, fixtureAmp*math.MaxInt16/2, float64(wavPeak(rec.Body.Bytes())), 4)
}

func TestProcessAudioByID_Rejections(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	writeLegacyAAC(t, fixturePath(tempDir, "legacy.aac"))
	writeCorrupt(t, fixturePath(tempDir, "corrupt.flac"))
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "legacy").Return("legacy.aac", nil)
	mockDS.On("GetNoteClipPath", "corrupt").Return("corrupt.flac", nil)
	controller.DS = mockDS

	assert.Equal(t, http.StatusBadRequest, postJSON(e, "/api/v2/audio/legacy/process", `{"denoise": "extreme"}`).Code)
	assert.Equal(t, http.StatusBadRequest, postJSON(e, "/api/v2/audio/legacy/process", `{"gain_db": 61}`).Code)

	rec := postJSON(e, "/api/v2/audio/legacy/process", `{"normalize": true}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Source audio format is not supported")

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
	writeLegacyAAC(t, fixturePath(tempDir, "legacy.aac"))
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteModelType", "10").Return("bat", nil)
	mockDS.On("GetNoteModelType", "11").Return("bat", nil)
	mockDS.On("GetNoteModelType", "12").Return("bat", nil)
	mockDS.On("GetNoteModelType", "13").Return("bird", nil)
	mockDS.On("GetNoteClipPath", "10").Return("bat.wav", nil)
	mockDS.On("GetNoteClipPath", "11").Return("low.wav", nil)
	mockDS.On("GetNoteClipPath", "12").Return("legacy.aac", nil)
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
	assert.Contains(t, rec.Body.String(), fmt.Sprintf("Source audio must be at least %d Hz for audible bats mode", audioformat.MinBatSampleRate))

	rec = postJSON(e, "/api/v2/audio/12/audible-bats", `{"expansion": 10}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Source audio format is not supported")

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
	e, controller, tempDir := setupMediaTestEnvironment(t)
	clearBinaryEnvironment(t, controller.Settings.Load())

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
		{transcode.ErrUnsupportedSource, http.StatusUnprocessableEntity},
		{transcode.ErrUndecodableSource, http.StatusUnprocessableEntity},
		{transcode.ErrAudioTooLong, http.StatusUnprocessableEntity},
		{transcode.ErrEmptyRange, http.StatusUnprocessableEntity},
		{transcode.ErrBelowMinimumRate, http.StatusUnprocessableEntity},
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
}

// wavPeak returns the largest absolute 16-bit sample in a 44-byte-header WAV body.
func wavPeak(body []byte) int {
	peak := 0
	data := body[44:]
	for i := 0; i+1 < len(data); i += 2 {
		v := int(int16(uint16(data[i]) | uint16(data[i+1])<<8)) //nolint:gosec // G115: PCM bit reinterpretation in a test
		peak = max(peak, max(v, -v))
	}
	return peak
}

// Each endpoint forwards normalize, denoise and gain_db into the filter chain;
// dropping a field would leave the response unprocessed.
func TestProcessEndpoints_ForwardFilterFields(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	const quietAmp = 0.01
	var quiet bytes.Buffer
	require.NoError(t, wavpcm.EncodeInterleaved(&quiet, wavpcm.Config{SampleRate: batTestRate, BitDepth: 16, Channels: 1}, toneBytes(batTestRate, batTestSeconds, fixtureBatToneHz, quietAmp)))
	require.NoError(t, os.WriteFile(fixturePath(tempDir, "bat.wav"), quiet.Bytes(), 0o600))
	writeToneWAV(t, fixturePath(tempDir, "tone.wav"), processTestRate, processTestLength, fixtureToneHz)
	var quietTone bytes.Buffer
	require.NoError(t, wavpcm.EncodeInterleaved(&quietTone, wavpcm.Config{SampleRate: processTestRate, BitDepth: 16, Channels: 1}, toneBytes(processTestRate, processTestLength, fixtureToneHz, quietAmp)))
	require.NoError(t, os.WriteFile(fixturePath(tempDir, "quiet.wav"), quietTone.Bytes(), 0o600))

	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteModelType", "30").Return("bat", nil)
	mockDS.On("GetNoteClipPath", "30").Return("bat.wav", nil)
	mockDS.On("GetNoteClipPath", "31").Return("tone.wav", nil)
	mockDS.On("GetNoteClipPath", "32").Return("quiet.wav", nil)
	controller.DS = mockDS

	t.Run("audible bats gain_db", func(t *testing.T) {
		plain := postJSON(e, "/api/v2/audio/30/audible-bats", `{"expansion": 10}`)
		louder := postJSON(e, "/api/v2/audio/30/audible-bats", `{"expansion": 10, "gain_db": 6.0206}`)
		require.Equal(t, http.StatusOK, plain.Code)
		require.Equal(t, http.StatusOK, louder.Code)
		assert.InDelta(t, 2*wavPeak(plain.Body.Bytes()), wavPeak(louder.Body.Bytes()), 4)
	})

	t.Run("audible bats normalize", func(t *testing.T) {
		plain := postJSON(e, "/api/v2/audio/30/audible-bats", `{"expansion": 10}`)
		normalized := postJSON(e, "/api/v2/audio/30/audible-bats", `{"expansion": 10, "normalize": true}`)
		require.Equal(t, http.StatusOK, normalized.Code)
		assert.Greater(t, wavPeak(normalized.Body.Bytes()), 4*wavPeak(plain.Body.Bytes()), "a quiet clip is lifted toward the target")
	})

	t.Run("process normalize", func(t *testing.T) {
		// A quiet clip sits far below -23 LUFS, so normalizing lifts it; a dropped
		// normalize flag would leave it at the unprocessed level.
		plain := postJSON(e, "/api/v2/audio/32/process", `{"gain_db": 0}`)
		normalized := postJSON(e, "/api/v2/audio/32/process", `{"normalize": true}`)
		require.Equal(t, http.StatusOK, plain.Code, plain.Body.String())
		require.Equal(t, http.StatusOK, normalized.Code, normalized.Body.String())
		assert.Greater(t, wavPeak(normalized.Body.Bytes()), 4*wavPeak(plain.Body.Bytes()), "a quiet clip is lifted toward the target")
	})

	t.Run("process denoise", func(t *testing.T) {
		plain := postJSON(e, "/api/v2/audio/31/process", `{"gain_db": 0}`)
		denoised := postJSON(e, "/api/v2/audio/31/process", `{"denoise": "heavy"}`)
		require.Equal(t, http.StatusOK, plain.Code, plain.Body.String())
		require.Equal(t, http.StatusOK, denoised.Code, denoised.Body.String())
		assert.NotEqual(t, plain.Body.Bytes(), denoised.Body.Bytes(), "the denoise preset reaches the filter chain")
	})

	t.Run("processed spectrogram", func(t *testing.T) {
		mockDS.On("GetNoteModelType", "32").Return("bird", nil).Maybe()

		plain := postJSON(e, "/api/v2/spectrogram/32/process", `{"gain_db": 0}`)
		require.Equal(t, http.StatusOK, plain.Code, plain.Body.String())
		// Each filter on its own must change the rendered image, so dropping any
		// one of them from the handler's filter set fails here.
		for _, body := range []string{`{"normalize": true}`, `{"denoise": "heavy"}`, `{"gain_db": 12}`} {
			filtered := postJSON(e, "/api/v2/spectrogram/32/process", body)
			require.Equal(t, http.StatusOK, filtered.Code, filtered.Body.String())
			assert.NotEqualf(t, plain.Body.Bytes(), filtered.Body.Bytes(), "filter %s reaches the processed spectrogram", body)
		}
	})
}
