package analysis

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
)

// TestSanitizeFloat64_ClampsToLevelRange pins that the publish-side clamp uses
// the processor's own level range, and that non-finite values take the default.
func TestSanitizeFloat64_ClampsToLevelRange(t *testing.T) {
	t.Parallel()

	const fallback = -100.0
	tests := []struct {
		name  string
		input float64
		want  float64
	}{
		{"below min", soundlevel.MinLevelDB - 0.1, soundlevel.MinLevelDB},
		{"at min", soundlevel.MinLevelDB, soundlevel.MinLevelDB},
		{"at max", soundlevel.MaxLevelDB, soundlevel.MaxLevelDB},
		{"above max", soundlevel.MaxLevelDB + 0.1, soundlevel.MaxLevelDB},
		{"in range", -42.5, -42.5},
		{"NaN", math.NaN(), fallback},
		{"+Inf", math.Inf(1), fallback},
		{"-Inf", math.Inf(-1), fallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, tt.want, sanitizeFloat64(tt.input, fallback), 0)
		})
	}
}
