package ffmpeg

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hasAudioOnlyFlag reports whether the built input args request audio-only via
// -allowed_media_types audio.
func hasAudioOnlyFlag(args []string) bool {
	idx := slices.Index(args, "-allowed_media_types")
	return idx != -1 && idx+1 < len(args) && args[idx+1] == "audio"
}

func TestStream_AudioOnlyFallback_LatchesAfterThreshold(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t) // RTSP by default

	// Before any failure the stream requests audio-only.
	require.True(t, hasAudioOnlyFlag(stream.buildFFmpegInputArgs(nil)),
		"expected audio-only flag before any failure")

	// Failures below audioOnlyFallbackThreshold must not latch the fallback.
	for i := 1; i < audioOnlyFallbackThreshold; i++ {
		stream.maybeEngageAudioOnlyFallback()
		assert.Falsef(t, stream.audioOnlyFallback.Load(), "should not latch after %d failure(s)", i)
		assert.True(t, hasAudioOnlyFlag(stream.buildFFmpegInputArgs(nil)),
			"expected audio-only flag to remain below threshold")
	}

	// The failure that reaches the threshold latches the fallback.
	stream.maybeEngageAudioOnlyFallback()
	assert.True(t, stream.audioOnlyFallback.Load(), "should latch after reaching threshold")

	args := stream.buildFFmpegInputArgs(nil)
	assert.False(t, hasAudioOnlyFlag(args), "expected audio-only flag dropped after fallback")
	// Transport must still be present; only the media-type restriction is dropped.
	assert.Contains(t, args, "-rtsp_transport")
}

func TestStream_AudioOnlyFallback_SkippedAfterAudioReceived(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)
	// Simulate audio having flowed at least once: audio-only is proven to work.
	stream.receivedAudio.Store(true)

	for range 5 {
		stream.maybeEngageAudioOnlyFallback()
	}

	assert.False(t, stream.audioOnlyFallback.Load(),
		"fallback must not engage once audio has been received")
	assert.True(t, hasAudioOnlyFlag(stream.buildFFmpegInputArgs(nil)),
		"expected audio-only flag to remain when audio was received")
}

func TestStream_AudioOnlyFallback_NonRTSPNoop(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig()
	cfg.Type = "http"
	cfg.URL = "http://host.example.com/stream"
	stream := newTestStreamWithConfig(t, &cfg)

	for range 5 {
		stream.maybeEngageAudioOnlyFallback()
	}

	assert.False(t, stream.audioOnlyFallback.Load(),
		"fallback state must never change for non-RTSP sources")
	// HTTP never carries the audio-only flag regardless of fallback state.
	assert.False(t, hasAudioOnlyFlag(stream.buildFFmpegInputArgs(nil)))
}

func TestStream_AudioOnlyFallback_ClearsFailureCounters(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Accumulate failure/circuit state as the audio-only attempts would.
	stream.setConsecutiveFailures(2)
	stream.restartCountMu.Lock()
	stream.restartCount = 3
	stream.restartCountMu.Unlock()

	// Reach the threshold to engage the fallback.
	for range audioOnlyFallbackThreshold {
		stream.maybeEngageAudioOnlyFallback()
	}
	require.True(t, stream.audioOnlyFallback.Load(), "expected fallback to latch")

	// Counters are cleared so the first full-stream attempt runs promptly.
	assert.Equal(t, 0, stream.getConsecutiveFailures(),
		"consecutive failures should reset when fallback engages")
	stream.restartCountMu.Lock()
	restart := stream.restartCount
	stream.restartCountMu.Unlock()
	assert.Equal(t, 0, restart, "restart count should reset when fallback engages")
}
