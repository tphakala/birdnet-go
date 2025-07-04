package ffmpeg

import (
	"context"
	"testing"
	"time"
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
		FFmpegPath:   "/usr/bin/ffmpeg",
	}

	process := NewProcess(config)

	if process.ID() != config.ID {
		t.Errorf("Expected process ID %s, got %s", config.ID, process.ID())
	}

	if process.IsRunning() {
		t.Error("Process should not be running initially")
	}
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
		FFmpegPath:   "/usr/bin/ffmpeg",
	}

	process := NewProcess(config)
	metrics := process.Metrics()

	if metrics.RestartCount != 0 {
		t.Errorf("Expected restart count 0, got %d", metrics.RestartCount)
	}

	if !metrics.StartTime.IsZero() {
		t.Error("Start time should be zero for unstarted process")
	}
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
		FFmpegPath:   "/usr/bin/ffmpeg",
	}

	process := NewProcess(config)

	// Check that channels are created
	audioOutput := process.AudioOutput()
	errorOutput := process.ErrorOutput()

	if audioOutput == nil {
		t.Error("Audio output channel should not be nil")
	}

	if errorOutput == nil {
		t.Error("Error output channel should not be nil")
	}
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
		FFmpegPath:   "/usr/bin/ffmpeg",
	}

	process := NewProcess(config)

	// Should be able to stop a process that was never started
	err := process.Stop()
	if err != nil {
		t.Errorf("Stop should not error for unstarted process: %v", err)
	}
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
		FFmpegPath:   "/usr/bin/ffmpeg",
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

	if !hasInput {
		t.Error("Args should contain input URL")
	}

	if !hasOutput {
		t.Error("Args should contain pipe:1 output")
	}

	if !hasRTSPTransport {
		t.Error("Args should contain RTSP transport for RTSP URLs")
	}

	if !hasExtraArgs {
		t.Error("Args should contain extra arguments")
	}
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
		if result != test.expected {
			t.Errorf("isRTSPURL(%q) = %v, expected %v", test.url, result, test.expected)
		}
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
	if err == nil {
		t.Error("Expected error when starting with invalid FFmpeg path")
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
		FFmpegPath:   "/usr/bin/ffmpeg",
	}

	process := NewProcess(config)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// First start (may fail due to invalid input, but that's OK)
	err1 := process.Start(ctx)
	
	// Second start should return the same error (sync.Once behavior)
	err2 := process.Start(ctx)
	
	if (err1 == nil) != (err2 == nil) {
		t.Error("Multiple Start() calls should return consistent error state")
	}
	
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
		FFmpegPath:   "/usr/bin/ffmpeg",
	}

	process := NewProcess(config)

	// First stop
	err1 := process.Stop()
	if err1 != nil {
		t.Errorf("First stop failed: %v", err1)
	}

	// Second stop should also succeed
	err2 := process.Stop()
	if err2 != nil {
		t.Errorf("Second stop failed: %v", err2)
	}
}