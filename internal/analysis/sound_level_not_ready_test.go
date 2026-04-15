// sound_level_not_ready_test.go: tests that the sound-level MQTT publisher
// silently skips when the processor reports MQTT as not ready.
//
// Without the fix, every sound-level interval would emit a telemetry-tagged
// "MQTT client not available" error and flood Sentry (several hundred events
// over weeks observed in production from faulty broker configurations).
package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// makeValidSoundLevelData returns a well-formed SoundLevelData value suitable
// for driving publishSoundLevelToMQTT past validation.
func makeValidSoundLevelData() soundlevel.SoundLevelData {
	return soundlevel.SoundLevelData{
		Timestamp: time.Now(),
		Source:    "test-source",
		Name:      "test-device",
		Duration:  10,
		OctaveBands: map[string]soundlevel.OctaveBandData{
			"1000_Hz": {
				CenterFreq:  1000,
				Min:         -60.5,
				Max:         -40.2,
				Mean:        -50.3,
				SampleCount: 100,
			},
		},
	}
}

// TestPublishSoundLevelToMQTT_ClientNotReady_Silent verifies that when the
// processor has no MQTT client (initializeMQTT failed or reconfiguration is
// in progress), the sound-level publisher:
//
//  1. Returns nil (no error escalation to the caller's log.Error path)
//  2. Does NOT wrap the sentinel with CategorySoundLevel (which would hit
//     Sentry via the errors package telemetry integration)
func TestPublishSoundLevelToMQTT_ClientNotReady_Silent(t *testing.T) {
	// Not parallel: conf.SetTestSettings mutates package-global state.
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = "birdnet/test"
	conf.SetTestSettings(settings)

	// Build a processor whose MqttClient reference is nil (the exact startup
	// failure condition reported by Sentry when the broker connect fails).
	proc := &processor.Processor{
		Settings: settings,
	}

	soundData := makeValidSoundLevelData()

	err := publishSoundLevelToMQTT(soundData, proc)
	require.NoError(t, err,
		"publishSoundLevelToMQTT must return nil when client is not ready, "+
			"not a category-tagged error that would flood Sentry")
}

// TestPublishSoundLevelToMQTT_ClientNotReady_Idempotent verifies that repeated
// calls with a nil client remain silent and do not leak an error up to the
// caller. This is the scenario that generates high-volume telemetry floods
// when the broker stays unreachable.
func TestPublishSoundLevelToMQTT_ClientNotReady_Idempotent(t *testing.T) {
	// Not parallel: conf.SetTestSettings mutates package-global state.
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = "birdnet/test"
	conf.SetTestSettings(settings)

	proc := &processor.Processor{
		Settings: settings,
	}

	// Simulate many sound-level intervals — each call must remain a silent
	// no-op at the publisher level regardless of how many have come before.
	for i := range 500 {
		err := publishSoundLevelToMQTT(makeValidSoundLevelData(), proc)
		assert.NoError(t, err, "call %d: must remain silent after first suppression", i)
	}
}

// TestPublishSoundLevelToMQTT_MQTTDisabled_StillNoError verifies the
// pre-existing "MQTT disabled" early return is not affected by the fix.
func TestPublishSoundLevelToMQTT_MQTTDisabled_StillNoError(t *testing.T) {
	// Not parallel: conf.SetTestSettings mutates package-global state.
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = false // disabled
	conf.SetTestSettings(settings)

	proc := &processor.Processor{
		Settings: settings,
	}

	err := publishSoundLevelToMQTT(makeValidSoundLevelData(), proc)
	require.NoError(t, err,
		"when MQTT is disabled the publisher returns nil (pre-existing behavior)")
}
