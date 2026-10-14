package transcode

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
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

	for _, p := range []string{"", "light", "medium", "heavy"} {
		assert.Truef(t, IsValidDenoisePreset(p), "%q", p)
	}
	for _, p := range []string{"none", "Light", "extreme", " "} {
		assert.Falsef(t, IsValidDenoisePreset(p), "%q", p)
	}
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
