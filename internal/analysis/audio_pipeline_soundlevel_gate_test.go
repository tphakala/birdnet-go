// audio_pipeline_soundlevel_gate_test.go verifies that the sound level
// pipeline (router route + Processor + bridge goroutine) is only created when
// Realtime.Audio.SoundLevel.Enabled is true. Before the gate existed, every
// audio source got a full biquad filter bank and accumulator even when sound
// level monitoring was off, costing ~16 MB of live heap and ~13 MB/h of
// allocation churn per source on a Pi.
package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestRegisterSoundLevelConsumers_DisabledIsNoOp confirms the guard added to
// registerSoundLevelConsumers short-circuits before touching p.engine when
// sound level monitoring is disabled. Passing a nil engine proves the early
// return is taken: if the body executed it would panic on the first
// p.engine.Registry() call.
func TestRegisterSoundLevelConsumers_DisabledIsNoOp(t *testing.T) {
	// Not parallel: conf.SetTestSettings mutates package-global state.
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.Audio.SoundLevel.Enabled = false
	settings.Realtime.Audio.SoundLevel.Interval = 10
	conf.SetTestSettings(settings)

	// engine is intentionally nil: the guard must return before dereferencing
	// it. If this panics, the gate was not taken.
	p := &AudioPipelineService{}
	assert.NotPanics(t, func() {
		p.registerSoundLevelConsumers([]string{"src-1", "src-2"}, "unit_test_disabled")
	}, "registerSoundLevelConsumers must early-return when SoundLevel.Enabled is false")
	assert.Empty(t, p.soundLevelConsumers,
		"no consumers should be tracked when sound level is disabled")
}

// TestRegisterSoundLevelConsumers_DisabledRepeatedCallsStayQuiet re-runs the
// disabled-path check to catch any state that might accumulate across calls
// (idempotency regression).
func TestRegisterSoundLevelConsumers_DisabledRepeatedCallsStayQuiet(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.Audio.SoundLevel.Enabled = false
	conf.SetTestSettings(settings)

	p := &AudioPipelineService{}
	for range 10 {
		p.registerSoundLevelConsumers([]string{"src-1"}, "unit_test_repeat")
	}
	assert.Empty(t, p.soundLevelConsumers)
}

// TestReconfigureSoundLevel_DisableWithNoRoutes verifies the hot-reload
// disable path is safe when no routes were ever created. The teardown helper
// must not touch the engine in that case since p.engine would be nil in a
// unit-test context.
func TestReconfigureSoundLevel_DisableWithNoRoutes(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.Audio.SoundLevel.Enabled = false
	conf.SetTestSettings(settings)

	p := &AudioPipelineService{}
	// Nil engine is acceptable because the disable path with an empty
	// consumer map short-circuits before calling router.
	assert.NotPanics(t, func() { p.ReconfigureSoundLevel() })
}

// TestRemoveAllSoundLevelConsumers_EmptyMapIsNoOp confirms the teardown helper
// does not access p.engine when there is nothing tracked. Guards against a
// regression where the helper would unconditionally dereference p.engine.
func TestRemoveAllSoundLevelConsumers_EmptyMapIsNoOp(t *testing.T) {
	p := &AudioPipelineService{}
	assert.NotPanics(t, func() { p.removeAllSoundLevelConsumers("unit_test_empty") })
	assert.Empty(t, p.soundLevelConsumers)
}

// TestRemoveAllSoundLevelConsumers_ClearsMapBeforeCalls confirms the helper
// drains the tracking map atomically so a concurrent call sees no tracked
// consumers while the first call is still issuing RemoveRoute.
//
// We simulate this by injecting a map entry and then verifying the map is
// emptied even though the RemoveRoute call will panic on a nil engine (the
// recover below swallows it). The post-panic state check confirms the drain
// happened before the router call.
func TestRemoveAllSoundLevelConsumers_DrainsMapBeforeRouterCalls(t *testing.T) {
	p := &AudioPipelineService{
		soundLevelConsumers: map[string]string{"src-1": "soundlevel_src-1"},
	}

	// Engine is nil, so RemoveRoute will panic. The drain must happen first.
	func() {
		defer func() { _ = recover() }()
		p.removeAllSoundLevelConsumers("unit_test_drain")
	}()

	require.Empty(t, p.soundLevelConsumers,
		"tracking map must be cleared atomically before router.RemoveRoute is called")
}
