package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Compile-time check that BirdNET implements ModelInstance.
var _ ModelInstance = (*BirdNET)(nil)

func TestModelSpecDefaults(t *testing.T) {
	t.Parallel()

	spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	assert.Equal(t, 48000, spec.SampleRate)
	assert.Equal(t, 3*time.Second, spec.ClipLength)
}

func TestModelSpec_EffectiveSampleRate(t *testing.T) {
	t.Parallel()

	t.Run("returns SampleRate when RawSampleRate is zero", func(t *testing.T) {
		t.Parallel()
		spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
		assert.Equal(t, 48000, spec.EffectiveSampleRate())
	})

	t.Run("returns RawSampleRate when set", func(t *testing.T) {
		t.Parallel()
		spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second, RawSampleRate: 256000}
		assert.Equal(t, 256000, spec.EffectiveSampleRate())
	})
}
