package api

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"gorm.io/gorm"
)

func TestExtractAudioClipByID(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a test WAV file (needs to be a real WAV for FFmpeg to process)
	testFilename := "test_clip.wav"
	audioFilePath := filepath.Join(tempDir, testFilename)
	err := createTestAudioFile(t, audioFilePath)
	require.NoError(t, err)

	// Detect FFmpeg so the valid extraction test can set the path
	ffmpegPath, ffmpegErr := exec.LookPath("ffmpeg")

	// Configure mock datastore
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "clip-test-1").Return(testFilename, nil)
	mockDS.On("GetNoteClipPath", "nonexistent-999").Return("", gorm.ErrRecordNotFound)
	controller.DS = mockDS

	testCases := []struct {
		name           string
		noteID         string
		body           string
		expectedStatus int
		requiresFFmpeg bool
	}{
		{
			name:           "valid extraction WAV",
			noteID:         "clip-test-1",
			body:           `{"start": 0.0, "end": 0.05, "format": "wav"}`,
			expectedStatus: http.StatusOK,
			requiresFFmpeg: true,
		},
		{
			name:           "missing note ID",
			noteID:         "",
			body:           `{"start": 0.0, "end": 1.0, "format": "wav"}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid start > end",
			noteID:         "clip-test-1",
			body:           `{"start": 2.0, "end": 1.0, "format": "wav"}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "negative start",
			noteID:         "clip-test-1",
			body:           `{"start": -1.0, "end": 1.0, "format": "wav"}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "unsupported format",
			noteID:         "clip-test-1",
			body:           `{"start": 0.0, "end": 1.0, "format": "invalid"}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "malformed JSON",
			noteID:         "clip-test-1",
			body:           `{bad json}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "nonexistent note",
			noteID:         "nonexistent-999",
			body:           `{"start": 0.0, "end": 1.0, "format": "wav"}`,
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.requiresFFmpeg {
				if ffmpegErr != nil {
					t.Skip("FFmpeg not available, skipping extraction test")
				}
				controller.Settings.Load().Realtime.Audio.FfmpegPath = ffmpegPath
			}

			path := "/api/v2/audio/" + tc.noteID + "/clip"
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			assert.Equal(t, tc.expectedStatus, rec.Code)

			if tc.expectedStatus == http.StatusOK {
				contentType := rec.Header().Get("Content-Type")
				assert.NotEmpty(t, contentType)
				contentDisp := rec.Header().Get("Content-Disposition")
				assert.Contains(t, contentDisp, "attachment")
				assert.Positive(t, rec.Body.Len())
			}
		})
	}
}

func TestExportAudioByID(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	testFilename := "test_export.wav"
	audioFilePath := filepath.Join(tempDir, testFilename)
	err := createTestAudioFile(t, audioFilePath)
	require.NoError(t, err)

	ffmpegPath, ffmpegErr := exec.LookPath("ffmpeg")

	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "123").Return(testFilename, nil)
	mockDS.On("GetNoteClipPath", "999").Return("", gorm.ErrRecordNotFound)
	controller.DS = mockDS

	testCases := []struct {
		name           string
		noteID         string
		body           string
		expectedStatus int
		requiresFFmpeg bool
		expectedType   string
		expectedExt    string
	}{
		{
			name:           "valid export WAV",
			noteID:         "123",
			body:           `{"format": "wav"}`,
			expectedStatus: http.StatusOK,
			requiresFFmpeg: true,
			expectedType:   MimeTypeWAV,
			expectedExt:    ".wav",
		},
		{
			name:           "valid export MP3",
			noteID:         "123",
			body:           `{"format": "mp3"}`,
			expectedStatus: http.StatusOK,
			requiresFFmpeg: true,
			expectedType:   MimeTypeMP3,
			expectedExt:    ".mp3",
		},
		{
			name:           "unsupported format",
			noteID:         "123",
			body:           `{"format": "invalid"}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "malformed JSON",
			noteID:         "123",
			body:           `{bad json}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "nonexistent note",
			noteID:         "999",
			body:           `{"format": "wav"}`,
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.requiresFFmpeg {
				if ffmpegErr != nil {
					t.Skip("FFmpeg not available, skipping export test")
				}
				controller.Settings.Realtime.Audio.FfmpegPath = ffmpegPath
			}

			path := "/api/v2/audio/" + tc.noteID + "/export"
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			assert.Equal(t, tc.expectedStatus, rec.Code)

			if tc.expectedStatus == http.StatusOK {
				assert.Equal(t, tc.expectedType, rec.Header().Get("Content-Type"))
				assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
				assert.Contains(t, rec.Header().Get("Content-Disposition"), tc.expectedExt)
				assert.Positive(t, rec.Body.Len())
			}
		})
	}
}
