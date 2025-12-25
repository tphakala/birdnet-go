package ffmpeg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProcess(t *testing.T) {
	t.Parallel()

	config := &ProcessConfig{
		ID:           "test-process",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	process := NewProcess(config)

	assert.Equal(t, config.ID, process.ID(), "Process ID should match config")
	assert.False(t, process.IsRunning(), "Process should not be running initially")
}

func TestProcessMetrics(t *testing.T) {
	t.Parallel()

	config := &ProcessConfig{
		ID:           "metrics-test",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	process := NewProcess(config)
	metrics := process.Metrics()

	assert.Equal(t, 0, metrics.RestartCount, "Expected restart count 0")
	assert.True(t, metrics.StartTime.IsZero(), "Start time should be zero for unstarted process")
}

func TestProcessChannels(t *testing.T) {
	t.Parallel()

	config := &ProcessConfig{
		ID:           "channels-test",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	process := NewProcess(config)

	// Check that channels are created
	audioOutput := process.AudioOutput()
	errorOutput := process.ErrorOutput()

	assert.NotNil(t, audioOutput, "Audio output channel should not be nil")
	assert.NotNil(t, errorOutput, "Error output channel should not be nil")
}

func TestProcessStopBeforeStart(t *testing.T) {
	t.Parallel()

	config := &ProcessConfig{
		ID:           "stop-test",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	process := NewProcess(config)

	// Should be able to stop a process that was never started
	err := process.Stop()
	assert.NoError(t, err, "Stop should not error for unstarted process")
}

func TestBuildFFmpegArgs(t *testing.T) {
	t.Parallel()

	config := &ProcessConfig{
		ID:           "args-test",
		InputURL:     "rtsp://example.com/stream",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
		ExtraArgs:    []string{"-analyzeduration", "1000000"},
	}

	p := &process{
		config: config,
	}

	args := p.buildFFmpegArgs()

	// Check for essential arguments
	hasInput := false
	hasOutput := false
	hasRTSPTransport := false
	hasExtraArgs := false

	for i, arg := range args {
		switch arg {
		case "-i":
			if i+1 < len(args) && args[i+1] == config.InputURL {
				hasInput = true
			}
		case "pipe:1":
			hasOutput = true
		case "-rtsp_transport":
			if i+1 < len(args) && args[i+1] == "tcp" {
				hasRTSPTransport = true
			}
		case "-analyzeduration":
			if i+1 < len(args) && args[i+1] == "1000000" {
				hasExtraArgs = true
			}
		}
	}

	assert.True(t, hasInput, "Args should contain input URL")
	assert.True(t, hasOutput, "Args should contain pipe:1 output")
	assert.True(t, hasRTSPTransport, "Args should contain RTSP transport for RTSP URLs")
	assert.True(t, hasExtraArgs, "Args should contain extra arguments")
}

func TestIsRTSPURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url      string
		expected bool
	}{
		{"rtsp://example.com/stream", true},
		{"rtsps://example.com/stream", true},
		{"http://example.com/stream", false},
		{"https://example.com/stream", false},
		{"file:///path/to/file.wav", false},
		{"", false},
		{"rtsp", false},
	}

	for _, test := range tests {
		result := isRTSPURL(test.url)
		assert.Equal(t, test.expected, result, "isRTSPURL(%q) should return %v", test.url, test.expected)
	}
}

func TestProcessStartInvalidCommand(t *testing.T) {
	t.Parallel()

	config := &ProcessConfig{
		ID:           "invalid-command-test",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	process := NewProcess(config)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := process.Start(ctx)
	require.Error(t, err, "Expected error when starting with invalid FFmpeg path")
	if err == nil {
		_ = process.Stop() // Ignore error for cleanup
	}
}

func TestProcessDoubleStart(t *testing.T) {
	t.Parallel()

	config := &ProcessConfig{
		ID:           "double-start-test",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	process := NewProcess(config)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// First start (may fail due to invalid input, but that's OK)
	err1 := process.Start(ctx)

	// Second start should return the same error (sync.Once behavior)
	err2 := process.Start(ctx)

	assert.Equal(t, err1 == nil, err2 == nil, "Multiple Start() calls should return consistent error state")

	_ = process.Stop() // Ignore error for cleanup
}

func TestProcessDoubleStop(t *testing.T) {
	t.Parallel()

	config := &ProcessConfig{
		ID:           "double-stop-test",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	process := NewProcess(config)

	// First stop
	err1 := process.Stop()
	require.NoError(t, err1, "First stop should not fail")

	// Second stop should also succeed
	err2 := process.Stop()
	assert.NoError(t, err2, "Second stop should not fail")
}