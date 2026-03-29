package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// newTestEngine creates an AudioEngine with a test context for testing.
// The caller must call the returned stop function when done to avoid goroutine leaks.
// testModelID is used by tests to verify analysis buffer allocation.
const testModelID = "BirdNET_GLOBAL_6K_V2.4"

func newTestEngine(t *testing.T) (eng *AudioEngine, stop func()) {
	t.Helper()
	cfg := &Config{Logger: audiocore.GetLogger()}
	eng = New(t.Context(), cfg, nil)
	eng.SetPrimaryModelID(testModelID)
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
	assert.NotNil(t, eng.ffmpegMgr)
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
	assert.NotNil(t, eng.FFmpegManager(), "FFmpegManager() should return non-nil")
	assert.NotNil(t, eng.DeviceManager(), "DeviceManager() should return non-nil")
	assert.Nil(t, eng.Scheduler(), "Scheduler() should be nil when no scheduler provided")
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
	health := eng.FFmpegManager().AllStreamHealth()
	assert.Contains(t, health, "test_rtsp_001", "stream should appear in FFmpeg manager")
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
	health := eng.FFmpegManager().AllStreamHealth()
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
	health := eng.FFmpegManager().AllStreamHealth()
	assert.Contains(t, health, "test_reconfig_001", "stream should be restarted after reconfigure")
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
