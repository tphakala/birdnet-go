package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// newTestEngine creates an AudioEngine with a test context for testing.
// The caller must call the returned stop function when done to avoid goroutine leaks.
// testModelID is used by tests to verify analysis buffer allocation.
const testModelID = "BirdNET_V2.4"

// Named RTSP transport values for tests.
const (
	transportTCP = "tcp"
	transportUDP = "udp"
)

// BirdNET v2.4 analysis buffer dimensions: 3s of 16-bit 48kHz mono audio.
const (
	testClipBytes    = 288000 // 48000 * 3 * 1 * 2
	testOverlapBytes = 144000
	testReadSize     = 144000
)

func newTestEngine(t *testing.T) (eng *AudioEngine, stop func()) {
	t.Helper()
	cfg := &Config{Logger: audiocore.GetLogger()}
	eng = New(t.Context(), cfg, nil)
	eng.SetPrimaryModel(testModelID, testClipBytes, testOverlapBytes, testReadSize)
	return eng, eng.Stop
}

// TestEngine_NewAndStop verifies that an engine can be created and stopped
// cleanly with all subsystems initialised.
func TestEngine_NewAndStop(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	// All subsystems should be non-nil after construction.
	assert.NotNil(t, eng.registry)
	assert.NotNil(t, eng.router)
	assert.NotNil(t, eng.streamMgr)
	assert.NotNil(t, eng.deviceMgr)
	assert.NotNil(t, eng.bufferMgr)
	assert.NotNil(t, eng.logger)
	assert.NotNil(t, eng.ctx)
	assert.NotNil(t, eng.cancel)
}

// TestEngine_Accessors verifies that all accessor methods return non-nil
// subsystem references.
func TestEngine_Accessors(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	assert.NotNil(t, eng.Registry(), "Registry() should return non-nil")
	assert.NotNil(t, eng.Router(), "Router() should return non-nil")
	assert.NotNil(t, eng.BufferManager(), "BufferManager() should return non-nil")
	assert.NotNil(t, eng.StreamManager(), "StreamManager() should return non-nil")
	assert.NotNil(t, eng.DeviceManager(), "DeviceManager() should return non-nil")
	assert.Nil(t, eng.Scheduler(), "Scheduler() should be nil when no scheduler provided")
}

// TestEngine_resolveTransport verifies that a per-stream transport wins over
// the engine-wide default, and that an unset per-stream value falls back to
// the engine default (issue #4240).
func TestEngine_resolveTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		engineDefault   string
		streamTransport string
		want            string
	}{
		{name: "per-stream wins over default", engineDefault: transportTCP, streamTransport: transportUDP, want: transportUDP},
		{name: "empty per-stream falls back to default", engineDefault: transportTCP, streamTransport: "", want: transportTCP},
		{name: "per-stream used when default empty", engineDefault: "", streamTransport: transportUDP, want: transportUDP},
		{name: "both empty stays empty (ffmpeg guard applies later)", engineDefault: "", streamTransport: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			eng := New(t.Context(), &Config{Logger: audiocore.GetLogger(), Transport: tt.engineDefault}, nil)
			t.Cleanup(eng.Stop)
			assert.Equal(t, tt.want, eng.resolveTransport(tt.streamTransport))
		})
	}
}

// TestEngine_AddSource_Stream adds an RTSP source and verifies that it is
// registered in the source registry and that buffers are allocated.
func TestEngine_AddSource_Stream(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	cfg := &audiocore.SourceConfig{
		ID:               "test_rtsp_001",
		DisplayName:      "Test RTSP Stream",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: "rtsp://192.168.1.100/stream",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	}

	err := eng.AddSource(cfg)
	require.NoError(t, err)

	// Verify source is in the registry.
	src, ok := eng.Registry().Get("test_rtsp_001")
	require.True(t, ok, "source should be registered")
	assert.Equal(t, "Test RTSP Stream", src.DisplayName)
	assert.Equal(t, audiocore.SourceTypeRTSP, src.Type)

	// Verify analysis buffer was allocated.
	ab, err := eng.BufferManager().AnalysisBuffer("test_rtsp_001", testModelID)
	require.NoError(t, err)
	assert.NotNil(t, ab, "analysis buffer should be allocated")

	// Verify capture buffer was allocated.
	cb, err := eng.BufferManager().CaptureBuffer("test_rtsp_001")
	require.NoError(t, err)
	assert.NotNil(t, cb, "capture buffer should be allocated")

	// Verify FFmpeg stream was started (it appears in AllStreamHealth).
	health := eng.StreamManager().AllStreamHealth()
	assert.Contains(t, health, "test_rtsp_001", "stream should appear in FFmpeg manager")
}

// TestEngine_AddSource_HighSampleRate verifies that a source with a sample rate
// above 48kHz gets analysis buffers sized to the primary model's native
// dimensions, not scaled by the source/model rate ratio. This is the core
// regression test for issue #575: BufferConsumer resamples audio to the model's
// target rate before writing, so the analysis buffer must match the model spec.
func TestEngine_AddSource_HighSampleRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sampleRate int
	}{
		{name: "96kHz source", sampleRate: 96000},
		{name: "256kHz source (bat detection)", sampleRate: 256000},
		{name: "48kHz source (baseline)", sampleRate: 48000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			eng, stop := newTestEngine(t)
			defer stop()

			sourceID := fmt.Sprintf("test_highrate_%d", tt.sampleRate)
			cfg := &audiocore.SourceConfig{
				ID:               sourceID,
				DisplayName:      "High Rate Test",
				Type:             audiocore.SourceTypeRTSP,
				ConnectionString: "rtsp://192.168.1.100/highrate",
				SampleRate:       tt.sampleRate,
				BitDepth:         16,
				Channels:         1,
			}

			err := eng.AddSource(cfg)
			require.NoError(t, err)

			ab, err := eng.BufferManager().AnalysisBuffer(sourceID, testModelID)
			require.NoError(t, err)
			require.NotNil(t, ab, "analysis buffer should be allocated")

			// Verify the actual buffer dimensions match the model spec, not
			// a scaled value. On the old buggy code with rateScale, a 96kHz
			// source would produce windowSize=576000 instead of 288000.
			assert.Equal(t, testClipBytes, ab.WindowSize(),
				"analysis buffer window should match model spec (overlap+readSize=%d), not be scaled by source rate", testClipBytes)
		})
	}
}

// TestEngine_ReconfigureSource_HighSampleRate verifies that reconfiguring a
// source to a higher sample rate does not scale the analysis buffer.
func TestEngine_ReconfigureSource_HighSampleRate(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	cfg := &audiocore.SourceConfig{
		ID:               "test_reconfig_highrate",
		DisplayName:      "Reconfigure Rate Test",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: "rtsp://192.168.1.50/rate",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	}
	require.NoError(t, eng.AddSource(cfg))

	// Reconfigure to 96kHz.
	newCfg := &audiocore.SourceConfig{
		ConnectionString: "rtsp://192.168.1.50/rate_v2",
		SampleRate:       96000,
		BitDepth:         16,
		Channels:         1,
	}
	require.NoError(t, eng.ReconfigureSource("test_reconfig_highrate", newCfg))

	ab, err := eng.BufferManager().AnalysisBuffer("test_reconfig_highrate", testModelID)
	require.NoError(t, err)
	require.NotNil(t, ab, "analysis buffer should be allocated after reconfigure to 96kHz")
	assert.Equal(t, testClipBytes, ab.WindowSize(),
		"analysis buffer window should match model spec after reconfigure to 96kHz")
}

// TestEngine_AddSource_Device adds an audio card source and verifies
// registration and buffer allocation. The actual device capture may fail
// without real hardware, so we handle both success and failure paths.
func TestEngine_AddSource_Device(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	cfg := &audiocore.SourceConfig{
		ID:               "test_audio_001",
		DisplayName:      "Test Audio Card",
		Type:             audiocore.SourceTypeAudioCard,
		ConnectionString: "default",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	}

	// Device capture will likely fail without hardware, but the source
	// should still be registered and buffers allocated up to that point.
	err := eng.AddSource(cfg)

	// On a machine without audio hardware, StartCapture fails, which causes
	// AddSource to clean up and return an error. On machines with audio
	// hardware, it succeeds.
	if err != nil {
		// Verify cleanup happened: buffers should be deallocated.
		_, abErr := eng.BufferManager().AnalysisBuffer("test_audio_001", testModelID)
		require.Error(t, abErr, "analysis buffer should be cleaned up on failure")
		return
	}

	// If we got here, device capture succeeded (real hardware present).
	src, ok := eng.Registry().Get("test_audio_001")
	require.True(t, ok)
	assert.Equal(t, audiocore.SourceTypeAudioCard, src.Type)

	// Clean up the capture.
	require.NoError(t, eng.RemoveSource("test_audio_001"))
}

// TestEngine_RemoveSource adds a source, then removes it, verifying that
// registry, buffers, and streams are all cleaned up.
func TestEngine_RemoveSource(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	cfg := &audiocore.SourceConfig{
		ID:               "test_remove_001",
		DisplayName:      "Source To Remove",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: "rtsp://192.168.1.200/remove",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	}

	require.NoError(t, eng.AddSource(cfg))

	// Verify present before removal.
	_, ok := eng.Registry().Get("test_remove_001")
	require.True(t, ok)

	// Remove.
	err := eng.RemoveSource("test_remove_001")
	require.NoError(t, err)

	// Verify source is gone from registry.
	_, ok = eng.Registry().Get("test_remove_001")
	assert.False(t, ok, "source should be unregistered after removal")

	// Verify buffers are deallocated.
	_, abErr := eng.BufferManager().AnalysisBuffer("test_remove_001", testModelID)
	require.Error(t, abErr, "analysis buffer should be deallocated")

	_, cbErr := eng.BufferManager().CaptureBuffer("test_remove_001")
	require.Error(t, cbErr, "capture buffer should be deallocated")

	// Verify stream is gone from FFmpeg manager.
	health := eng.StreamManager().AllStreamHealth()
	assert.NotContains(t, health, "test_remove_001", "stream should be removed from FFmpeg manager")
}

// TestEngine_RemoveSource_NotFound verifies that removing a non-existent source
// returns an appropriate error.
func TestEngine_RemoveSource_NotFound(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	err := eng.RemoveSource("nonexistent_source")
	require.Error(t, err)
	assert.ErrorIs(t, err, audiocore.ErrSourceNotFound)
}

// TestEngine_ReconfigureSource adds a source, reconfigures it with a new
// sample rate, and verifies that fresh buffers are allocated.
func TestEngine_ReconfigureSource(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	// Add initial source.
	cfg := &audiocore.SourceConfig{
		ID:               "test_reconfig_001",
		DisplayName:      "Reconfigurable Source",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: "rtsp://192.168.1.50/reconfig",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	}
	require.NoError(t, eng.AddSource(cfg))

	// Verify initial state.
	_, ok := eng.Registry().Get("test_reconfig_001")
	require.True(t, ok)

	ab1, err := eng.BufferManager().AnalysisBuffer("test_reconfig_001", testModelID)
	require.NoError(t, err)
	require.NotNil(t, ab1)

	// Reconfigure with new sample rate.
	newCfg := &audiocore.SourceConfig{
		ConnectionString: "rtsp://192.168.1.50/reconfig_v2",
		SampleRate:       32000,
		BitDepth:         16,
		Channels:         1,
	}
	err = eng.ReconfigureSource("test_reconfig_001", newCfg)
	require.NoError(t, err)

	// Verify the source is still in the registry.
	src, ok := eng.Registry().Get("test_reconfig_001")
	require.True(t, ok)
	assert.Equal(t, audiocore.SourceTypeRTSP, src.Type)

	// Verify new buffers are allocated.
	ab2, err := eng.BufferManager().AnalysisBuffer("test_reconfig_001", testModelID)
	require.NoError(t, err)
	assert.NotNil(t, ab2, "new analysis buffer should be allocated after reconfigure")

	cb2, err := eng.BufferManager().CaptureBuffer("test_reconfig_001")
	require.NoError(t, err)
	assert.NotNil(t, cb2, "new capture buffer should be allocated after reconfigure")

	// Verify the FFmpeg stream was restarted.
	health := eng.StreamManager().AllStreamHealth()
	assert.Contains(t, health, "test_reconfig_001", "stream should be restarted after reconfigure")
}

// TestEngine_ReconfigureSource_NonRTSPTransportStaysEmpty guards against the
// hot-reload restart loop for non-RTSP stream types (issue #4240). A non-RTSP
// source (HLS/HTTP/UDP) carries an empty Transport in its desired config, so the
// reconfigure write-back must store that empty value verbatim, NOT re-resolve it
// to the engine default. If it stored a concrete default, the registry entry
// ("tcp") would never equal the empty desired value and every later reload would
// restart the stream forever.
func TestEngine_ReconfigureSource_NonRTSPTransportStaysEmpty(t *testing.T) {
	t.Parallel()
	// Engine default is a concrete transport; the non-RTSP source must not pick it up.
	eng := New(t.Context(), &Config{Logger: audiocore.GetLogger(), Transport: transportTCP}, nil)
	eng.SetPrimaryModel(testModelID, testClipBytes, testOverlapBytes, testReadSize)
	t.Cleanup(eng.Stop)

	cfg := &audiocore.SourceConfig{
		ID:               "test_hls_transport",
		DisplayName:      "HLS Source",
		Type:             audiocore.SourceTypeHLS,
		ConnectionString: "https://example.com/live/playlist.m3u8",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
		// Transport intentionally empty: HLS does not use -rtsp_transport.
	}
	require.NoError(t, eng.AddSource(cfg))

	src, ok := eng.Registry().Get("test_hls_transport")
	require.True(t, ok)
	assert.Empty(t, src.Transport, "non-RTSP source must be registered with an empty transport")

	// Reconfigure an unrelated field; desired transport is still empty.
	newCfg := &audiocore.SourceConfig{
		ConnectionString: "https://example.com/live/playlist.m3u8",
		SampleRate:       32000,
		BitDepth:         16,
		Channels:         1,
	}
	require.NoError(t, eng.ReconfigureSource("test_hls_transport", newCfg))

	src, ok = eng.Registry().Get("test_hls_transport")
	require.True(t, ok)
	assert.Empty(t, src.Transport,
		"reconfigure must not re-resolve a non-RTSP transport to the engine default")
}

// TestEngine_ReconfigureSource_NotFound verifies that reconfiguring a
// non-existent source returns an appropriate error.
func TestEngine_ReconfigureSource_NotFound(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	err := eng.ReconfigureSource("nonexistent", &audiocore.SourceConfig{
		ConnectionString: "rtsp://example.com/stream",
		SampleRate:       48000,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, audiocore.ErrSourceNotFound)
}

// TestErrEngineStopped verifies that the sentinel error is set as the context
// cancellation cause when Stop is called.
func TestErrEngineStopped(t *testing.T) {
	t.Parallel()
	eng, _ := newTestEngine(t)

	// Stop the engine — this cancels the context with ErrEngineStopped.
	eng.Stop()

	// The engine's context should be done.
	require.Error(t, eng.ctx.Err(), "context should be cancelled after Stop")

	// The cancellation cause should be the sentinel error.
	cause := context.Cause(eng.ctx)
	require.Error(t, cause)
	assert.ErrorIs(t, cause, ErrEngineStopped)
}

// TestErrEngineStopped_IsSentinel verifies that ErrEngineStopped can be
// detected with errors.Is and has a stable string representation.
func TestErrEngineStopped_IsSentinel(t *testing.T) {
	t.Parallel()

	assert.True(t, errors.Is(ErrEngineStopped, ErrEngineStopped))
	assert.Contains(t, ErrEngineStopped.Error(), "stop requested")
}

// TestEngine_AddSource_ZeroAudioParams verifies that zero-value SampleRate,
// BitDepth, and Channels are defaulted before being stored in the registry
// and passed to stream/device configs.
func TestEngine_AddSource_ZeroAudioParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		sampleRate       int
		bitDepth         int
		channels         int
		expectSampleRate int
		expectBitDepth   int
		expectChannels   int
	}{
		{
			name:             "all zero defaults",
			sampleRate:       0,
			bitDepth:         0,
			channels:         0,
			expectSampleRate: defaultSampleRate,
			expectBitDepth:   defaultBitDepth,
			expectChannels:   defaultChannels,
		},
		{
			name:             "negative values default",
			sampleRate:       -1,
			bitDepth:         -1,
			channels:         -1,
			expectSampleRate: defaultSampleRate,
			expectBitDepth:   defaultBitDepth,
			expectChannels:   defaultChannels,
		},
		{
			name:             "explicit values preserved",
			sampleRate:       96000,
			bitDepth:         24,
			channels:         2,
			expectSampleRate: 96000,
			expectBitDepth:   24,
			expectChannels:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			eng, stop := newTestEngine(t)
			defer stop()

			sourceID := fmt.Sprintf("test_zero_%s", tt.name)
			cfg := &audiocore.SourceConfig{
				ID:               sourceID,
				DisplayName:      "Zero Params Test",
				Type:             audiocore.SourceTypeRTSP,
				ConnectionString: "rtsp://192.168.1.100/zero",
				SampleRate:       tt.sampleRate,
				BitDepth:         tt.bitDepth,
				Channels:         tt.channels,
			}

			err := eng.AddSource(cfg)
			require.NoError(t, err)

			src, ok := eng.Registry().Get(sourceID)
			require.True(t, ok, "source should be registered")
			assert.Equal(t, tt.expectSampleRate, src.SampleRate,
				"registry SampleRate should be defaulted")
			assert.Equal(t, tt.expectBitDepth, src.BitDepth,
				"registry BitDepth should be defaulted")
			assert.Equal(t, tt.expectChannels, src.Channels,
				"registry Channels should be defaulted")
		})
	}
}

// TestEngine_ReconfigureSource_ZeroAudioParams verifies that zero-value
// audio parameters are defaulted during reconfiguration and the registry
// is updated with the effective values.
func TestEngine_ReconfigureSource_ZeroAudioParams(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	cfg := &audiocore.SourceConfig{
		ID:               "test_reconfig_zero",
		DisplayName:      "Reconfigure Zero Test",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: "rtsp://192.168.1.100/zero",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	}
	require.NoError(t, eng.AddSource(cfg))

	newCfg := &audiocore.SourceConfig{
		ConnectionString: "rtsp://192.168.1.100/zero_v2",
		SampleRate:       0,
		BitDepth:         0,
		Channels:         0,
	}
	require.NoError(t, eng.ReconfigureSource("test_reconfig_zero", newCfg))

	src, ok := eng.Registry().Get("test_reconfig_zero")
	require.True(t, ok)
	assert.Equal(t, defaultSampleRate, src.SampleRate,
		"registry SampleRate should be defaulted after reconfigure")
	assert.Equal(t, defaultBitDepth, src.BitDepth,
		"registry BitDepth should be defaulted after reconfigure")
	assert.Equal(t, defaultChannels, src.Channels,
		"registry Channels should be defaulted after reconfigure")
}

// TestEngine_StartStream_ZeroBitDepthFallback verifies that StartStream
// applies the defaultBitDepth fallback when the registry has zero BitDepth.
// In practice this can't happen because AddSource always defaults, but
// StartStream must be independently safe.
func TestEngine_StartStream_ZeroBitDepthFallback(t *testing.T) {
	t.Parallel()
	eng, stop := newTestEngine(t)
	defer stop()

	cfg := &audiocore.SourceConfig{
		ID:               "test_startstream_bitdepth",
		DisplayName:      "StartStream BitDepth Test",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: "rtsp://192.168.1.100/stream",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	}
	require.NoError(t, eng.AddSource(cfg))

	// Stop the stream started by AddSource so we can restart it.
	require.NoError(t, eng.StreamManager().StopStream("test_startstream_bitdepth"))

	// Manually zero out BitDepth in the registry to simulate an edge case.
	eng.Registry().UpdateAudioParams("test_startstream_bitdepth", 48000, 0, 1)

	// StartStream should not fail even with zero BitDepth in registry.
	err := eng.StartStream("test_startstream_bitdepth", "rtsp://192.168.1.100/stream2", "")
	require.NoError(t, err)

	health := eng.StreamManager().AllStreamHealth()
	assert.Contains(t, health, "test_startstream_bitdepth")
}

// TestDeriveNativeReadIdle asserts the native supervisor read-idle window sits
// strictly below the liveness silence threshold for every positive threshold, so
// the supervisor's read-idle always fires before the watchdog would alarm, while
// an unknown threshold (<=0) yields 0 (let the native default apply).
func TestDeriveNativeReadIdle(t *testing.T) {
	t.Parallel()

	// A non-positive threshold means "unknown": return 0 so the native default applies.
	assert.Zero(t, deriveNativeReadIdle(0), "threshold 0 must return 0")
	assert.Zero(t, deriveNativeReadIdle(-5*time.Second), "negative threshold must return 0")

	tests := []struct {
		name      string
		threshold time.Duration
		// want is the exact expected readIdle when non-zero; a zero want means
		// "assert only the strictly-less-than-threshold invariant".
		want time.Duration
	}{
		// Sub-floor thresholds: the 5s jitter floor is bypassed (it would meet or
		// exceed the threshold), so readIdle is two thirds of the threshold.
		{name: "1s sub-floor", threshold: 1 * time.Second, want: 1 * time.Second * 2 / 3},
		{name: "3s sub-floor", threshold: 3 * time.Second, want: 3 * time.Second * 2 / 3},
		{name: "5s sub-floor", threshold: 5 * time.Second, want: 5 * time.Second * 2 / 3},
		// Floor-applied band (5s < threshold < 7.5s): two thirds is below 5s, so the
		// floor lifts readIdle to 5s while staying below the threshold.
		{name: "6s floor applied", threshold: 6 * time.Second, want: 5 * time.Second},
		{name: "7s floor applied", threshold: 7 * time.Second, want: 5 * time.Second},
		// Two-thirds band (7.5s <= threshold < 30s): floor is below two thirds.
		{name: "8s two-thirds", threshold: 8 * time.Second, want: 8 * time.Second * 2 / 3},
		{name: "15s two-thirds", threshold: 15 * time.Second, want: 10 * time.Second},
		// Capped band (threshold >= 30s): clamped at stream.DefaultReadIdle (20s).
		{name: "30s capped", threshold: 30 * time.Second, want: 20 * time.Second},
		{name: "60s capped", threshold: 60 * time.Second, want: 20 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := deriveNativeReadIdle(tt.threshold)
			// For a positive threshold the read-idle must be positive and STRICTLY
			// below the threshold, never 0 and never at/past it.
			require.Positive(t, got, "positive threshold must yield a positive read-idle")
			assert.Less(t, got, tt.threshold,
				"read-idle must be strictly less than the silence threshold")
			if tt.want != 0 {
				assert.Equal(t, tt.want, got, "read-idle must match the expected value")
			}
		})
	}
}
