package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Compile-time check that BirdNET implements ModelInstance.
// This will fail to compile until BirdNET gets all the methods.
// Uncomment after Task 3:
// var _ ModelInstance = (*BirdNET)(nil)

func TestModelSpecDefaults(t *testing.T) {
	t.Parallel()

	spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	assert.Equal(t, 48000, spec.SampleRate)
	assert.Equal(t, 3*time.Second, spec.ClipLength)
}
