package transcode

import (
	"maps"
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/go-audio-dsp/denoiser"

	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestIsSupportedClipFormat(t *testing.T) {
	t.Parallel()

	for _, f := range []string{"wav", "flac", "mp3", "aac", "opus"} {
		assert.Truef(t, IsSupportedClipFormat(f), "%q", f)
	}
	for _, f := range []string{"alac", "ALAC", "", "m4a", "ogg", "WAV", "mp4"} {
		assert.Falsef(t, IsSupportedClipFormat(f), "%q", f)
	}
}

func TestIsValidDenoisePreset(t *testing.T) {
	t.Parallel()

	for _, p := range []string{"", "light", "light-medium", "medium", "heavy"} {
		assert.Truef(t, IsValidDenoisePreset(p), "%q", p)
	}
	for _, p := range []string{"none", "Light", "extreme", " ", "light_medium", "Light-Medium", "lightmedium"} {
		assert.Falsef(t, IsValidDenoisePreset(p), "%q", p)
	}
}

// Every name in the transcode strength map must be a conf preset name and select
// its own strength, and the ordered test preset list must name the same presets as
// the map.
func TestDenoiseStrengthsMatchConfPresets(t *testing.T) {
	t.Parallel()

	want := map[string]denoiser.Strength{
		conf.DenoisePresetLight:       denoiser.Light,
		conf.DenoisePresetLightMedium: denoiser.LightMedium,
		conf.DenoisePresetMedium:      denoiser.Medium,
		conf.DenoisePresetHeavy:       denoiser.Heavy,
	}
	assert.Equal(t, want, denoiseStrengths)
	for name := range denoiseStrengths {
		assert.Truef(t, conf.IsValidDenoisePresetName(name), "%q", name)
	}
	assert.ElementsMatch(t, slices.Collect(maps.Keys(denoiseStrengths)), denoisePresetsGentlestFirst)
	assert.True(t, conf.IsValidDenoisePresetName(conf.DefaultDenoisePreset))
	assert.False(t, conf.IsValidDenoisePresetName(""))
}

func TestIsValidGainDB(t *testing.T) {
	t.Parallel()

	for _, g := range []float64{0, 6, -6, MaxGainDB, -MaxGainDB, 0.1} {
		assert.Truef(t, IsValidGainDB(g), "%v", g)
	}
	for _, g := range []float64{MaxGainDB + 0.01, -MaxGainDB - 0.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		assert.Falsef(t, IsValidGainDB(g), "%v", g)
	}
}

func TestIsValidBatExpansionFactor(t *testing.T) {
	t.Parallel()

	for _, n := range []int{5, 10, 16, 20} {
		assert.Truef(t, IsValidBatExpansionFactor(n), "%d", n)
	}
	for _, n := range []int{0, 1, 4, 6, 15, 21, -5} {
		assert.Falsef(t, IsValidBatExpansionFactor(n), "%d", n)
	}
}

// These values were carried over from the FFmpeg-based pipeline; the literals are
// deliberate so a change to a constant has to change this test too.
func TestPipelineConstantsKeepTheirFFmpegEraValues(t *testing.T) {
	t.Parallel()

	assert.InDelta(t, -23.0, normalizeTargetLUFS, 0)
	assert.InDelta(t, -2.0, normalizeTruePeakDBTP, 0)
	assert.InDelta(t, 60.0, MaxGainDB, 0)
	assert.Equal(t, map[string]int{"mp3": 128, "opus": 64, "aac": 96}, clipBitrateKbps)
	assert.Equal(t, 48000, AudibleBatsOutputSampleRate)
}
