package myaudio

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestGetAudioDuration(t *testing.T) {
	skipIfNoSox(t)

	// Create a test WAV file with known duration (1 second of silence)
	testFile := filepath.Join(t.TempDir(), "test.wav")
	err := createTestWAVFile(testFile, 1.0)
	require.NoError(t, err, "Failed to create test file")

	tests := []struct {
		name         string
		audioPath    string
		wantDuration float64
		wantErr      bool
		tolerance    float64
	}{
		{
			name:         "valid WAV file",
			audioPath:    testFile,
			wantDuration: 1.0,
			wantErr:      false,
			tolerance:    0.1,
		},
		{
			name:      "non-existent file",
			audioPath: "/non/existent/file.wav",
			wantErr:   true,
		},
		{
			name:      "empty path",
			audioPath: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			t.Cleanup(cancel)

			duration, err := GetAudioDuration(ctx, tt.audioPath)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.InDelta(t, tt.wantDuration, duration, tt.tolerance,
					"Duration should be %v (±%v)", tt.wantDuration, tt.tolerance)
			}
		})
	}
}

func TestValidateFFmpegPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty path",
			path:    "",
			wantErr: true,
			errMsg:  "FFmpeg is not available",
		},
		{
			name:    "valid absolute path",
			path:    "/usr/bin/ffmpeg",
			wantErr: false,
		},
		{
			name:    "valid absolute path in local bin",
			path:    "/usr/local/bin/ffmpeg",
			wantErr: false,
		},
		{
			name:    "relative path rejected",
			path:    "ffmpeg",
			wantErr: true,
			errMsg:  "must be absolute",
		},
		{
			name:    "HA ingress prefix contamination",
			path:    "/api/hassio_ingress/abc123token/usr/bin/ffmpeg",
			wantErr: true,
			errMsg:  "contaminated by proxy/ingress prefix",
		},
		{
			name:    "generic API prefix contamination",
			path:    "/api/v2/usr/bin/ffmpeg",
			wantErr: true,
			errMsg:  "contaminated by proxy/ingress prefix",
		},
		{
			name:    "ingress prefix without api",
			path:    "/ingress/token123/usr/bin/ffmpeg",
			wantErr: true,
			errMsg:  "contaminated by proxy/ingress prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateFFmpegPath(tt.path)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetAudioDurationTimeout(t *testing.T) {
	skipIfNoSox(t)

	// Create and immediately cancel context to trigger error
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately

	// Use any placeholder path - the context error will be returned first
	_, err := GetAudioDuration(ctx, "placeholder.wav")
	require.Error(t, err, "Expected context cancellation error")

	// Check that we get a context-related error
	assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded),
		"Expected context.Canceled or context.DeadlineExceeded, got: %v", err)
}
