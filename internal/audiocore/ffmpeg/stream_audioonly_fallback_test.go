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
		assert.Falsef(t, stream.maybeEngageAudioOnlyFallback(),
			"should report not-engaged after %d failure(s)", i)
		assert.Falsef(t, stream.audioOnlyFallback.Load(), "should not latch after %d failure(s)", i)
		assert.True(t, hasAudioOnlyFlag(stream.buildFFmpegInputArgs(nil)),
			"expected audio-only flag to remain below threshold")
	}

	// The failure that reaches the threshold latches the fallback and reports it
	// engaged (so Run skips that iteration's backoff).
	assert.True(t, stream.maybeEngageAudioOnlyFallback(),
		"should report engaged on the call that reaches the threshold")
	assert.True(t, stream.audioOnlyFallback.Load(), "should latch after reaching threshold")

	// A further call after latching reports not-engaged (already engaged).
	assert.False(t, stream.maybeEngageAudioOnlyFallback(),
		"should report not-engaged once already latched")

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

// TestStream_BuildFFmpegInputArgs_MediaModeWinsOverLatch verifies the runtime arg
// path honors the configured media mode over the reactive fallback latch:
// full-stream never requests audio-only, audio-only always does (even if the latch
// is set), and auto follows the latch. An unset mode defaults to full-stream.
func TestStream_BuildFFmpegInputArgs_MediaModeWinsOverLatch(t *testing.T) {
	t.Parallel()

	newStream := func(mode string) *Stream {
		cfg := newTestConfig()
		cfg.MediaMode = mode
		return newTestStreamWithConfig(t, &cfg)
	}

	t.Run("full-stream ignores latch", func(t *testing.T) {
		t.Parallel()
		s := newStream("full-stream")
		assert.False(t, hasAudioOnlyFlag(s.buildFFmpegInputArgs(nil)), "full-stream must not request audio-only")
		s.audioOnlyFallback.Store(true)
		assert.False(t, hasAudioOnlyFlag(s.buildFFmpegInputArgs(nil)), "full-stream must stay full-stream even if latched")
	})

	t.Run("audio-only ignores latch", func(t *testing.T) {
		t.Parallel()
		s := newStream("audio-only")
		assert.True(t, hasAudioOnlyFlag(s.buildFFmpegInputArgs(nil)), "audio-only must request audio-only")
		s.audioOnlyFallback.Store(true)
		assert.True(t, hasAudioOnlyFlag(s.buildFFmpegInputArgs(nil)), "audio-only must not drop the restriction when latched")
	})

	t.Run("auto follows latch", func(t *testing.T) {
		t.Parallel()
		s := newStream("auto")
		assert.True(t, hasAudioOnlyFlag(s.buildFFmpegInputArgs(nil)), "auto requests audio-only before fallback")
		s.audioOnlyFallback.Store(true)
		assert.False(t, hasAudioOnlyFlag(s.buildFFmpegInputArgs(nil)), "auto drops audio-only after fallback engages")
	})

	t.Run("empty defaults to full-stream", func(t *testing.T) {
		t.Parallel()
		s := newStream("")
		assert.False(t, hasAudioOnlyFlag(s.buildFFmpegInputArgs(nil)), "empty media mode defaults to full-stream")
	})
}

// TestStream_MaybeEngageAudioOnlyFallback_ForcedModes verifies the reactive
// fallback only engages in auto mode. A stream forced to audio-only or full-stream
// must never latch the fallback, even after the failure threshold is exceeded.
func TestStream_MaybeEngageAudioOnlyFallback_ForcedModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"audio-only", "full-stream"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			cfg := newTestConfig()
			cfg.MediaMode = mode
			s := newTestStreamWithConfig(t, &cfg)

			for range audioOnlyFallbackThreshold + 2 {
				assert.Falsef(t, s.maybeEngageAudioOnlyFallback(),
					"forced %s mode must never engage the reactive fallback", mode)
			}
			assert.Falsef(t, s.audioOnlyFallback.Load(),
				"forced %s mode must not latch the fallback", mode)
		})
	}
}
