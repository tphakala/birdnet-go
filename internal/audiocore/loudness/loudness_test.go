package loudness

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/go-audio-dsp/loudnorm"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestMaxClipGainDBIsSixty pins the clip gain clamp literally, so a policy
// change has to change this test.
func TestMaxClipGainDBIsSixty(t *testing.T) {
	t.Parallel()
	assert.InDelta(t, 60.0, float64(MaxClipGainDB), 0)
	assert.InDelta(t, float64(conf.MaxTargetLUFS-loudnorm.AbsoluteGateLUFS), float64(MaxClipGainDB), 0)
}

// TestClipPlanOptions pins the clip policy: arguments pass through, the clamp is
// 60 dB and the gate fallback is on.
func TestClipPlanOptions(t *testing.T) {
	t.Parallel()
	got := ClipPlanOptions(44100, 2, -18.5, -1.5)
	assert.Equal(t, 44100, got.SampleRate)
	assert.Equal(t, 2, got.Channels)
	assert.InDelta(t, -18.5, got.TargetLUFS, 0)
	assert.InDelta(t, -1.5, got.TruePeakDBTP, 0)
	assert.InDelta(t, 60.0, got.MaxGainDB, 0)
	assert.True(t, got.GateFallback)
}

// TestSoundscapePlanOptions pins the BirdWeather policy: capture format from
// conf, -23 LUFS, -1 dBTP, a 30 dB clamp and no gate fallback.
func TestSoundscapePlanOptions(t *testing.T) {
	t.Parallel()
	got := SoundscapePlanOptions()
	assert.Equal(t, conf.SampleRate, got.SampleRate)
	assert.Equal(t, conf.NumChannels, got.Channels)
	assert.InDelta(t, -23.0, got.TargetLUFS, 0)
	assert.InDelta(t, -1.0, got.TruePeakDBTP, 0)
	assert.InDelta(t, 30.0, got.MaxGainDB, 0)
	assert.False(t, got.GateFallback)
	assert.InDelta(t, -23.0, float64(SoundscapeTargetLUFS), 0)
}
