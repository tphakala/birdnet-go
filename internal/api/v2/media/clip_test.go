package media

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	wavpcm "github.com/tphakala/go-wav/pcm"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

const (
	clipTestSourceRate    = 48000
	clipTestSourceSeconds = 2.0
)

// clipRequest posts body to the clip endpoint for noteID.
func clipRequest(t *testing.T, e http.Handler, noteID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/audio/"+noteID+"/clip", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestExtractAudioClipByID(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	testFilename := "test_clip.wav"
	writeToneWAV(t, fixturePath(tempDir, testFilename), clipTestSourceRate, clipTestSourceSeconds, fixtureToneHz)

	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "clip-test-1").Return(testFilename, nil)
	mockDS.On("GetNoteClipPath", "nonexistent-999").Return("", gorm.ErrRecordNotFound)
	controller.DS = mockDS

	testCases := []struct {
		name           string
		noteID         string
		body           string
		expectedStatus int
	}{
		{name: "valid extraction WAV", noteID: "clip-test-1", body: `{"start": 0.0, "end": 0.05, "format": "wav"}`, expectedStatus: http.StatusOK},
		{name: "missing note ID", noteID: "", body: `{"start": 0.0, "end": 1.0, "format": "wav"}`, expectedStatus: http.StatusBadRequest},
		{name: "invalid start > end", noteID: "clip-test-1", body: `{"start": 2.0, "end": 1.0, "format": "wav"}`, expectedStatus: http.StatusBadRequest},
		{name: "negative start", noteID: "clip-test-1", body: `{"start": -1.0, "end": 1.0, "format": "wav"}`, expectedStatus: http.StatusBadRequest},
		{name: "unsupported format", noteID: "clip-test-1", body: `{"start": 0.0, "end": 1.0, "format": "invalid"}`, expectedStatus: http.StatusBadRequest},
		{name: "alac is no longer a format", noteID: "clip-test-1", body: `{"start": 0.0, "end": 1.0, "format": "alac"}`, expectedStatus: http.StatusBadRequest},
		{name: "malformed JSON", noteID: "clip-test-1", body: `{bad json}`, expectedStatus: http.StatusBadRequest},
		{name: "nonexistent note", noteID: "nonexistent-999", body: `{"start": 0.0, "end": 1.0, "format": "wav"}`, expectedStatus: http.StatusNotFound},
		{name: "invalid denoise preset", noteID: "clip-test-1", body: `{"start": 0, "end": 1, "format": "wav", "denoise": "extreme"}`, expectedStatus: http.StatusBadRequest},
		{name: "gain above the limit", noteID: "clip-test-1", body: `{"start": 0, "end": 1, "format": "wav", "gain_db": 61}`, expectedStatus: http.StatusBadRequest},
		{name: "duration above the limit", noteID: "clip-test-1", body: `{"start": 0, "end": 301, "format": "wav"}`, expectedStatus: http.StatusBadRequest},
		{name: "start past the end of the audio", noteID: "clip-test-1", body: `{"start": 30, "end": 31, "format": "wav"}`, expectedStatus: http.StatusUnprocessableEntity},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := clipRequest(t, e, tc.noteID, tc.body)
			assert.Equal(t, tc.expectedStatus, rec.Code)
			if tc.expectedStatus == http.StatusOK {
				assert.NotEmpty(t, rec.Header().Get("Content-Type"))
				assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
				assert.Positive(t, rec.Body.Len())
			}
		})
	}
}

func TestExtractAudioClipByID_FormatsAndHeaders(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	writeToneWAV(t, fixturePath(tempDir, "src.wav"), clipTestSourceRate, clipTestSourceSeconds, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "1").Return("src.wav", nil)
	controller.DS = mockDS

	tests := []struct {
		format    string
		mime      string
		extension string
		magic     func([]byte) bool
	}{
		{"wav", MimeTypeWAV, "wav", func(b []byte) bool { return bytes.HasPrefix(b, []byte("RIFF")) }},
		{"flac", MimeTypeFLAC, "flac", func(b []byte) bool { return bytes.HasPrefix(b, []byte("fLaC")) }},
		{"mp3", MimeTypeMP3, "mp3", func(b []byte) bool { return len(b) > 1 && (bytes.HasPrefix(b, []byte("ID3")) || b[0] == 0xFF) }},
		{"aac", MimeTypeM4A, "m4a", func(b []byte) bool { return len(b) > 8 && bytes.Equal(b[4:8], []byte("ftyp")) }},
		{"opus", MimeTypeOGG, "ogg", func(b []byte) bool { return bytes.HasPrefix(b, []byte("OggS")) }},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			rec := clipRequest(t, e, "1", `{"start": 0.5, "end": 1.5, "format": "`+tt.format+`"}`)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, tt.mime, rec.Header().Get("Content-Type"))
			assert.Contains(t, rec.Header().Get("Content-Disposition"), "clip_0.5-1.5."+tt.extension)
			assert.True(t, tt.magic(rec.Body.Bytes()), "unexpected file header")
		})
	}

	t.Run("denoise, normalize and gain together", func(t *testing.T) {
		rec := clipRequest(t, e, "1", `{"start": 0, "end": 1.5, "format": "flac", "denoise": "medium", "normalize": true, "gain_db": 3}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.True(t, bytes.HasPrefix(rec.Body.Bytes(), []byte("fLaC")))
	})
}

// A source that is quiet for its first second and loud for its second one lets a
// test tell which part of the audio a clip request returned.
func TestExtractAudioClipByID_ForwardsStartAndFilters(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	const (
		quietAmp   = 0.01
		loudAmp    = 0.3
		halfSecond = clipTestSourceRate
	)
	pcm := append(toneBytes(clipTestSourceRate, 1, fixtureToneHz, quietAmp), toneBytes(clipTestSourceRate, 1, fixtureToneHz, loudAmp)...)
	var src bytes.Buffer
	require.NoError(t, wavpcm.EncodeInterleaved(&src, wavpcm.Config{SampleRate: clipTestSourceRate, BitDepth: 16, Channels: 1}, pcm))
	require.NoError(t, os.WriteFile(fixturePath(tempDir, "split.wav"), src.Bytes(), 0o600))
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "1").Return("split.wav", nil)
	controller.DS = mockDS

	early := clipRequest(t, e, "1", `{"start": 0, "end": 1, "format": "wav"}`)
	late := clipRequest(t, e, "1", `{"start": 1, "end": 2, "format": "wav"}`)
	require.Equal(t, http.StatusOK, early.Code, early.Body.String())
	require.Equal(t, http.StatusOK, late.Code, late.Body.String())
	_, earlyLen := wavRateAndLength(t, early.Body.Bytes())
	_, lateLen := wavRateAndLength(t, late.Body.Bytes())
	assert.Equal(t, halfSecond*2, earlyLen, "one second of 16-bit mono")
	assert.Equal(t, halfSecond*2, lateLen, "one second of 16-bit mono")
	assert.Greater(t, wavPeak(late.Body.Bytes()), 10*wavPeak(early.Body.Bytes()), "the clip starts at the requested start")

	t.Run("normalize", func(t *testing.T) {
		normalized := clipRequest(t, e, "1", `{"start": 0, "end": 1, "format": "wav", "normalize": true}`)
		require.Equal(t, http.StatusOK, normalized.Code, normalized.Body.String())
		assert.Greater(t, wavPeak(normalized.Body.Bytes()), 4*wavPeak(early.Body.Bytes()), "a quiet clip is lifted toward the target")
	})

	t.Run("gain_db", func(t *testing.T) {
		louder := clipRequest(t, e, "1", `{"start": 0, "end": 1, "format": "wav", "gain_db": 6.0206}`)
		require.Equal(t, http.StatusOK, louder.Code, louder.Body.String())
		assert.InDelta(t, 2*wavPeak(early.Body.Bytes()), wavPeak(louder.Body.Bytes()), 4)
	})

	t.Run("negative_gain_db", func(t *testing.T) {
		quieter := clipRequest(t, e, "1", `{"start": 1, "end": 2, "format": "wav", "gain_db": -6.0206}`)
		require.Equal(t, http.StatusOK, quieter.Code, quieter.Body.String())
		assert.InDelta(t, wavPeak(late.Body.Bytes())/2, wavPeak(quieter.Body.Bytes()), 4)
	})

	t.Run("denoise", func(t *testing.T) {
		denoised := clipRequest(t, e, "1", `{"start": 0, "end": 1, "format": "wav", "denoise": "heavy"}`)
		require.Equal(t, http.StatusOK, denoised.Code, denoised.Body.String())
		assert.NotEqual(t, early.Body.Bytes(), denoised.Body.Bytes(), "the denoise preset reaches the filter chain")
	})
}

func TestExtractAudioClipByID_SourceProblemsAre422(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	writeLegacyAAC(t, fixturePath(tempDir, "legacy.aac"))
	writeCorrupt(t, fixturePath(tempDir, "corrupt.flac"))
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "legacy").Return("legacy.aac", nil)
	mockDS.On("GetNoteClipPath", "corrupt").Return("corrupt.flac", nil)
	controller.DS = mockDS

	rec := clipRequest(t, e, "legacy", `{"start": 0, "end": 0.2, "format": "wav"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Source audio format is not supported")

	rec = clipRequest(t, e, "corrupt", `{"start": 0, "end": 0.2, "format": "wav"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "could not be decoded")
}

// This test fills the process-wide clip semaphore, so it must not run in
// parallel with the other clip tests.
func TestExtractAudioClipByID_SemaphoreFullIs503(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	writeToneWAV(t, fixturePath(tempDir, "src.wav"), clipTestSourceRate, 0.5, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "1").Return("src.wav", nil)
	controller.DS = mockDS

	for range maxConcurrentClipExtractions {
		clipExtractionSemaphore <- struct{}{}
	}
	t.Cleanup(func() {
		for range maxConcurrentClipExtractions {
			<-clipExtractionSemaphore
		}
	})

	rec := clipRequest(t, e, "1", `{"start": 0, "end": 0.2, "format": "wav"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
