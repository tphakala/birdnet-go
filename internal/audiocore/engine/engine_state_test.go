package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Named values for source state tests.
const (
	stateTestRTSPURL     = "rtsp://192.168.1.60/state"
	stateTestFileURL     = "/tmp/state-test.wav"
	stateTestMissingCard = "no-such-audio-device-for-state-test"

	stateTestSampleRate = 48000
	stateTestBitDepth   = 16
	stateTestChannels   = 1
)

// hookStreamManager wraps the engine's real StreamManager and lets a test run
// code inside StartStream, or make it fail.
type hookStreamManager struct {
	audiocore.StreamManager
	onStart func(spec *audiocore.StreamSpec) error
	// afterStart runs after a successful delegated start, before it returns.
	afterStart func(spec *audiocore.StreamSpec)
}

// StartStream runs the hook, returns its error if any, and otherwise delegates.
func (h *hookStreamManager) StartStream(spec *audiocore.StreamSpec) error {
	if h.onStart != nil {
		if err := h.onStart(spec); err != nil {
			return err
		}
	}
	if err := h.StreamManager.StartStream(spec); err != nil {
		return err
	}
	if h.afterStart != nil {
		h.afterStart(spec)
	}
	return nil
}

// installStartHook replaces the engine's stream manager with a hooked wrapper.
func installStartHook(eng *AudioEngine, onStart func(spec *audiocore.StreamSpec) error) *hookStreamManager {
	h := &hookStreamManager{StreamManager: eng.streamMgr, onStart: onStart}
	eng.streamMgr = h
	return h
}

func stateTestRTSPConfig() *audiocore.SourceConfig {
	return &audiocore.SourceConfig{
		DisplayName:      "State Test RTSP",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: stateTestRTSPURL,
		SampleRate:       stateTestSampleRate,
		BitDepth:         stateTestBitDepth,
		Channels:         stateTestChannels,
	}
}

func requireState(t *testing.T, eng *AudioEngine, id string, want audiocore.SourceState) {
	t.Helper()
	src, ok := eng.Registry().Get(id)
	require.True(t, ok, "source should be registered")
	assert.Equal(t, want, src.State)
}

func TestEngine_AddSource_StreamStateRunning(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)

	id, err := eng.AddSource(stateTestRTSPConfig())
	require.NoError(t, err)
	requireState(t, eng, id, audiocore.SourceRunning)
}

func TestEngine_AddSource_StreamStateStartingDuringStart(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)

	var during audiocore.SourceState
	installStartHook(eng, func(spec *audiocore.StreamSpec) error {
		src, ok := eng.Registry().Get(spec.SourceID)
		require.True(t, ok)
		during = src.State
		return nil
	})

	id, err := eng.AddSource(stateTestRTSPConfig())
	require.NoError(t, err)
	assert.Equal(t, audiocore.SourceStarting, during)
	requireState(t, eng, id, audiocore.SourceRunning)
}

func TestEngine_AddSource_StopDuringStartKeepsStopped(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)

	// Stop the stream once it has started but before AddSource finishes, as a
	// quiet-hours stop would.
	installStartHook(eng, nil).afterStart = func(spec *audiocore.StreamSpec) {
		assert.NoError(t, eng.StopStream(spec.SourceID))
	}

	id, err := eng.AddSource(stateTestRTSPConfig())
	require.NoError(t, err)
	requireState(t, eng, id, audiocore.SourceStopped)
}

func TestEngine_AddSource_ExistingEntryStaleStateReset(t *testing.T) {
	t.Parallel()

	for _, stale := range []audiocore.SourceState{
		audiocore.SourceError, audiocore.SourceStopped, audiocore.SourceStarting,
	} {
		t.Run(stale.String(), func(t *testing.T) {
			t.Parallel()
			eng := newTestEngine(t)
			cfg := stateTestRTSPConfig()

			pre, err := eng.Registry().Register(cfg)
			require.NoError(t, err)
			require.NoError(t, eng.Registry().UpdateState(pre.ID, stale))

			id, err := eng.AddSource(cfg)
			require.NoError(t, err)
			assert.Equal(t, pre.ID, id, "the existing entry should be reused")
			requireState(t, eng, id, audiocore.SourceRunning)
		})
	}
}

func TestEngine_AddSource_FileSourceStaysInactive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stale *audiocore.SourceState
	}{
		{name: "fresh entry"},
		{name: "pre-registered with stale error", stale: new(audiocore.SourceError)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			eng := newTestEngine(t)
			cfg := &audiocore.SourceConfig{
				DisplayName:      "State Test File",
				Type:             audiocore.SourceTypeFile,
				ConnectionString: stateTestFileURL,
				SampleRate:       stateTestSampleRate,
				BitDepth:         stateTestBitDepth,
				Channels:         stateTestChannels,
			}
			if tt.stale != nil {
				pre, err := eng.Registry().Register(cfg)
				require.NoError(t, err)
				require.NoError(t, eng.Registry().UpdateState(pre.ID, *tt.stale))
			}

			id, err := eng.AddSource(cfg)
			require.NoError(t, err)
			requireState(t, eng, id, audiocore.SourceInactive)
		})
	}
}

func TestEngine_SourceStateTransitions(t *testing.T) {
	t.Parallel()

	failStart := func(*audiocore.StreamSpec) error { return errors.NewStd("start refused") }

	t.Run("quiet hours stop and start", func(t *testing.T) {
		t.Parallel()
		eng := newTestEngine(t)
		id, err := eng.AddSource(stateTestRTSPConfig())
		require.NoError(t, err)
		requireState(t, eng, id, audiocore.SourceRunning)

		require.NoError(t, eng.StopStream(id))
		requireState(t, eng, id, audiocore.SourceStopped)

		require.NoError(t, eng.StartStream(id, stateTestRTSPURL, ""))
		requireState(t, eng, id, audiocore.SourceRunning)

		require.NoError(t, eng.StopStream(id))
		requireState(t, eng, id, audiocore.SourceStopped)
	})

	t.Run("failed reconfigure then successful reconfigure", func(t *testing.T) {
		t.Parallel()
		eng := newTestEngine(t)
		h := installStartHook(eng, nil)
		id, err := eng.AddSource(stateTestRTSPConfig())
		require.NoError(t, err)
		requireState(t, eng, id, audiocore.SourceRunning)

		h.onStart = failStart
		require.Error(t, eng.ReconfigureSource(id, stateTestRTSPConfig()))
		requireState(t, eng, id, audiocore.SourceError)

		h.onStart = nil
		require.NoError(t, eng.ReconfigureSource(id, stateTestRTSPConfig()))
		requireState(t, eng, id, audiocore.SourceRunning)
	})

	t.Run("remove and re-add", func(t *testing.T) {
		t.Parallel()
		eng := newTestEngine(t)
		id, err := eng.AddSource(stateTestRTSPConfig())
		require.NoError(t, err)
		requireState(t, eng, id, audiocore.SourceRunning)

		require.NoError(t, eng.RemoveSource(id))
		id, err = eng.AddSource(stateTestRTSPConfig())
		require.NoError(t, err)
		requireState(t, eng, id, audiocore.SourceRunning)
	})

	t.Run("reconfigure a stopped stream", func(t *testing.T) {
		t.Parallel()
		eng := newTestEngine(t)
		id, err := eng.AddSource(stateTestRTSPConfig())
		require.NoError(t, err)
		require.NoError(t, eng.StopStream(id))
		requireState(t, eng, id, audiocore.SourceStopped)

		require.NoError(t, eng.ReconfigureSource(id, stateTestRTSPConfig()))
		requireState(t, eng, id, audiocore.SourceRunning)
	})
}

func TestEngine_ReconfigureSource_AudioCardFailureSetsError(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)

	cfg := &audiocore.SourceConfig{
		DisplayName:      "State Test Card",
		Type:             audiocore.SourceTypeAudioCard,
		ConnectionString: stateTestMissingCard,
		SampleRate:       stateTestSampleRate,
		BitDepth:         stateTestBitDepth,
		Channels:         stateTestChannels,
	}
	src, err := eng.Registry().Register(cfg)
	require.NoError(t, err)
	require.NoError(t, eng.Registry().UpdateState(src.ID, audiocore.SourceRunning))

	require.Error(t, eng.ReconfigureSource(src.ID, cfg))
	requireState(t, eng, src.ID, audiocore.SourceError)
}

func TestEngine_StartStream_StateLifecycle(t *testing.T) {
	t.Parallel()

	newStopped := func(t *testing.T) (*AudioEngine, *hookStreamManager, string) {
		t.Helper()
		eng := newTestEngine(t)
		h := installStartHook(eng, nil)
		id, err := eng.AddSource(stateTestRTSPConfig())
		require.NoError(t, err)
		require.NoError(t, eng.StopStream(id))
		return eng, h, id
	}

	t.Run("starting while the stream starts", func(t *testing.T) {
		t.Parallel()
		eng, h, id := newStopped(t)
		var during audiocore.SourceState
		h.onStart = func(spec *audiocore.StreamSpec) error {
			src, ok := eng.Registry().Get(spec.SourceID)
			require.True(t, ok)
			during = src.State
			return nil
		}
		require.NoError(t, eng.StartStream(id, stateTestRTSPURL, ""))
		assert.Equal(t, audiocore.SourceStarting, during)
		requireState(t, eng, id, audiocore.SourceRunning)
	})

	t.Run("failed start sets error", func(t *testing.T) {
		t.Parallel()
		eng, h, id := newStopped(t)
		h.onStart = func(*audiocore.StreamSpec) error { return errors.NewStd("start refused") }
		require.Error(t, eng.StartStream(id, stateTestRTSPURL, ""))
		requireState(t, eng, id, audiocore.SourceError)
	})

	t.Run("stop during failed start keeps stopped", func(t *testing.T) {
		t.Parallel()
		eng, h, id := newStopped(t)
		// A quiet-hours stop records stopped while the start is in flight,
		// then the start fails.
		h.onStart = func(spec *audiocore.StreamSpec) error {
			require.NoError(t, eng.Registry().UpdateState(spec.SourceID, audiocore.SourceStopped))
			return errors.NewStd("start refused")
		}
		require.Error(t, eng.StartStream(id, stateTestRTSPURL, ""))
		requireState(t, eng, id, audiocore.SourceStopped)
	})

	t.Run("stop during start keeps stopped", func(t *testing.T) {
		t.Parallel()
		eng, h, id := newStopped(t)
		h.afterStart = func(spec *audiocore.StreamSpec) {
			assert.NoError(t, eng.StopStream(spec.SourceID))
		}
		require.NoError(t, eng.StartStream(id, stateTestRTSPURL, ""))
		requireState(t, eng, id, audiocore.SourceStopped)
	})
}

func TestEngine_ReconfigureSource_StopDuringStartKeepsStopped(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)
	h := installStartHook(eng, nil)
	id, err := eng.AddSource(stateTestRTSPConfig())
	require.NoError(t, err)

	h.afterStart = func(spec *audiocore.StreamSpec) {
		assert.NoError(t, eng.StopStream(spec.SourceID))
	}
	require.NoError(t, eng.ReconfigureSource(id, stateTestRTSPConfig()))
	requireState(t, eng, id, audiocore.SourceStopped)
}

func TestEngine_ReconfigureSource_StopDuringFailedStartKeepsStopped(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)
	h := installStartHook(eng, nil)
	id, err := eng.AddSource(stateTestRTSPConfig())
	require.NoError(t, err)

	h.onStart = func(spec *audiocore.StreamSpec) error {
		require.NoError(t, eng.Registry().UpdateState(spec.SourceID, audiocore.SourceStopped))
		return errors.NewStd("start refused")
	}
	require.Error(t, eng.ReconfigureSource(id, stateTestRTSPConfig()))
	requireState(t, eng, id, audiocore.SourceStopped)
}

func TestEngine_ReconfigureSource_TypeResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		newType   audiocore.SourceType
		wantState audiocore.SourceState
	}{
		{"empty type keeps the stream type", "", audiocore.SourceRunning},
		{"unknown type keeps the stream type", audiocore.SourceTypeUnknown, audiocore.SourceRunning},
		{"stream to file is inactive", audiocore.SourceTypeFile, audiocore.SourceInactive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			eng := newTestEngine(t)
			id, err := eng.AddSource(stateTestRTSPConfig())
			require.NoError(t, err)

			newCfg := stateTestRTSPConfig()
			newCfg.Type = tt.newType
			require.NoError(t, eng.ReconfigureSource(id, newCfg))
			requireState(t, eng, id, tt.wantState)
		})
	}
}
