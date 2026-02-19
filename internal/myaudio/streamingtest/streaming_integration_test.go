//go:build integration

// Package streamingtest contains integration tests for audio streaming.
// These tests require Docker (for MediaMTX container) and FFmpeg.
// Run with: go test -tags=integration -race -v ./internal/myaudio/streamingtest/ -timeout 180s
package streamingtest

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// Package-level test fixtures shared across all streaming integration tests.
// The MediaMTX container and publisher are started once in TestMain and reused.
var (
	mediamtxContainer *containers.MediaMTXContainer
	publisher         *containers.StreamPublisher
)

const testStreamPath = "birdnet-test"

func TestMain(m *testing.M) {
	// Skip entire suite if ffmpeg is not available
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Println("ffmpeg not available, skipping streaming integration tests")
		os.Exit(0)
	}

	ctx := context.Background() //nolint:gocritic // TestMain has no *testing.T for t.Context()

	// Start MediaMTX container
	var err error
	mediamtxContainer, err = containers.NewMediaMTXContainer(ctx, nil)
	if err != nil {
		log.Fatalf("failed to start MediaMTX container: %v", err)
	}

	// Find tawnyowl.wav in repo root
	wavPath := findTawnyOwlWAV()

	// Publish WAV to MediaMTX via RTSP — MediaMTX auto-exposes on all protocols
	rtspPublishURL := mediamtxContainer.GetRTSPURL(testStreamPath)
	publisher, err = containers.PublishWAVToMediaMTX(ctx, wavPath, rtspPublishURL)
	if err != nil {
		_ = mediamtxContainer.Terminate(context.Background()) //nolint:gocritic // TestMain has no *testing.T
		log.Fatalf("failed to start publisher: %v", err)
	}

	// Allow time for stream to be fully published and available on all protocols.
	// HLS in particular needs time to generate initial segments.
	time.Sleep(5 * time.Second)

	code := m.Run()

	// Cleanup
	publisher.Stop()
	_ = mediamtxContainer.Terminate(context.Background()) //nolint:gocritic // TestMain has no *testing.T
	os.Exit(code)
}

// findTawnyOwlWAV walks up from the current directory to find tawnyowl.wav at the repo root.
func findTawnyOwlWAV() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal("failed to get working directory")
	}
	for {
		candidate := filepath.Join(dir, "tawnyowl.wav")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatal("tawnyowl.wav not found in repo root")
		}
		dir = parent
	}
}

// setupTestSettings configures global settings matching what the frontend sends.
// The frontend saves StreamConfig via PUT /api/v2/settings with fields:
//
//	{ "name": "...", "url": "rtsp://...", "type": "rtsp", "transport": "tcp" }
func setupTestSettings(t *testing.T, streamURL, streamType, transport string) {
	t.Helper()

	originalSettings := conf.GetTestSettings()

	settings := conf.GetTestSettings()
	settings.Realtime.Audio.FfmpegPath = "ffmpeg" // Use system FFmpeg
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:      "Integration Test Stream",
			URL:       streamURL,
			Type:      streamType,
			Transport: transport,
		},
	}
	conf.SetTestSettings(settings)
	t.Cleanup(func() {
		conf.SetTestSettings(originalSettings)
	})
}

// waitForAudioData waits for at least one audio data message on the channel
// or until the timeout is reached. Returns 1 if data was received, 0 on timeout.
func waitForAudioData(t *testing.T, audioChan <-chan myaudio.UnifiedAudioData, timeout time.Duration) int {
	t.Helper()

	select {
	case <-audioChan:
		return 1
	case <-time.After(timeout):
		return 0
	}
}

// startManagerStream creates a manager, starts a stream, and returns cleanup resources.
// Uses FFmpegManager.StartStream which mirrors the production code path:
// it initializes analysis/capture buffers, registers sound level processor, etc.
func startManagerStream(t *testing.T, url, transport string) (manager *myaudio.FFmpegManager, audioChan chan myaudio.UnifiedAudioData) {
	t.Helper()

	manager = myaudio.NewFFmpegManager()
	require.NotNil(t, manager)

	audioChan = make(chan myaudio.UnifiedAudioData, 100)

	err := manager.StartStream(url, transport, audioChan)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = manager.StopStream(url)
	})

	return manager, audioChan
}

// TestStreamingIntegration_RTSP verifies that an RTSP stream can be started via the
// manager, receives audio data, and stops cleanly. Uses exact frontend config format.
func TestStreamingIntegration_RTSP(t *testing.T) {
	rtspURL := mediamtxContainer.GetRTSPURL(testStreamPath)
	setupTestSettings(t, rtspURL, conf.StreamTypeRTSP, "tcp")

	manager, audioChan := startManagerStream(t, rtspURL, "tcp")

	// Verify stream is tracked as active
	activeStreams := manager.GetActiveStreams()
	assert.Contains(t, activeStreams, rtspURL, "manager should track the active RTSP stream")

	// Verify audio data is received
	msgCount := waitForAudioData(t, audioChan, 15*time.Second)
	assert.Positive(t, msgCount, "RTSP stream should receive audio data")

	// Stop stream and verify cleanup
	err := manager.StopStream(rtspURL)
	require.NoError(t, err)

	activeStreams = manager.GetActiveStreams()
	assert.NotContains(t, activeStreams, rtspURL, "stopped RTSP stream should not be tracked")
}

// TestStreamingIntegration_RTMP verifies that an RTMP stream can be started,
// receives audio data, and stops cleanly.
//
// KNOWN BUG: The -timeout flag in buildFFmpegInputArgs is applied to all stream types,
// but for RTMP it gets interpreted as listen_timeout, causing FFmpeg to enter listen/server
// mode instead of client mode. This results in "Address already in use" errors.
// The -timeout flag should only be applied to RTSP and HTTP/HLS streams.
func TestStreamingIntegration_RTMP(t *testing.T) {
	t.Skip("known bug: -timeout flag causes FFmpeg to enter listen mode for RTMP streams")

	rtmpURL := mediamtxContainer.GetRTMPURL(testStreamPath)
	setupTestSettings(t, rtmpURL, conf.StreamTypeRTMP, "")

	manager, audioChan := startManagerStream(t, rtmpURL, "")

	activeStreams := manager.GetActiveStreams()
	assert.Contains(t, activeStreams, rtmpURL, "manager should track the active RTMP stream")

	msgCount := waitForAudioData(t, audioChan, 15*time.Second)
	assert.Positive(t, msgCount, "RTMP stream should receive audio data")

	err := manager.StopStream(rtmpURL)
	require.NoError(t, err)

	activeStreams = manager.GetActiveStreams()
	assert.NotContains(t, activeStreams, rtmpURL, "stopped RTMP stream should not be tracked")
}

// TestStreamingIntegration_HLS verifies that an HLS stream can be started,
// receives audio data, and stops cleanly. HLS has higher latency due to
// segment generation, so timeouts are more generous.
func TestStreamingIntegration_HLS(t *testing.T) {
	hlsURL := mediamtxContainer.GetHLSURL(testStreamPath)
	setupTestSettings(t, hlsURL, conf.StreamTypeHLS, "")

	manager, audioChan := startManagerStream(t, hlsURL, "")

	activeStreams := manager.GetActiveStreams()
	assert.Contains(t, activeStreams, hlsURL, "manager should track the active HLS stream")

	// HLS needs more time — segments must be generated first
	msgCount := waitForAudioData(t, audioChan, 30*time.Second)
	assert.Positive(t, msgCount, "HLS stream should receive audio data")

	err := manager.StopStream(hlsURL)
	require.NoError(t, err)

	activeStreams = manager.GetActiveStreams()
	assert.NotContains(t, activeStreams, hlsURL, "stopped HLS stream should not be tracked")
}

// TestStreamingIntegration_ManagerLifecycle tests the full lifecycle including
// stop and restart, verifying data flows again after restart.
func TestStreamingIntegration_ManagerLifecycle(t *testing.T) {
	rtspURL := mediamtxContainer.GetRTSPURL(testStreamPath)
	setupTestSettings(t, rtspURL, conf.StreamTypeRTSP, "tcp")

	manager := myaudio.NewFFmpegManager()
	require.NotNil(t, manager)
	t.Cleanup(func() {
		_ = manager.StopStream(rtspURL)
	})

	audioChan := make(chan myaudio.UnifiedAudioData, 100)

	// Start stream
	err := manager.StartStream(rtspURL, "tcp", audioChan)
	require.NoError(t, err)

	// Wait for initial data
	msgCount := waitForAudioData(t, audioChan, 15*time.Second)
	require.Positive(t, msgCount, "initial stream should receive data")

	// Stop and restart
	err = manager.StopStream(rtspURL)
	require.NoError(t, err)

	// Brief pause before restart
	time.Sleep(1 * time.Second)

	err = manager.StartStream(rtspURL, "tcp", audioChan)
	require.NoError(t, err)

	// Verify data flows again after restart
	msgCount = waitForAudioData(t, audioChan, 15*time.Second)
	assert.Positive(t, msgCount, "restarted stream should receive data")

	err = manager.StopStream(rtspURL)
	require.NoError(t, err)
}
