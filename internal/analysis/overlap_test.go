package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEffectiveOverlap_SameClipLength(t *testing.T) {
	t.Parallel()
	result := effectiveOverlap(2*time.Second, 3*time.Second, 3*time.Second)
	assert.Equal(t, 2*time.Second, result)
}

func TestEffectiveOverlap_LongerClip(t *testing.T) {
	t.Parallel()
	result := effectiveOverlap(2*time.Second, 3*time.Second, 5*time.Second)
	expected := (2 * time.Second * 5) / 3
	assert.Equal(t, expected, result)
}

func TestEffectiveOverlap_ZeroOverlap(t *testing.T) {
	t.Parallel()
	result := effectiveOverlap(0, 3*time.Second, 5*time.Second)
	assert.Equal(t, time.Duration(0), result)
}

func TestOverlapBytes_Alignment(t *testing.T) {
	t.Parallel()
	const bytesPerSample = 2
	overlap := effectiveOverlap(2*time.Second, 3*time.Second, 5*time.Second)
	bytes := overlapBytes(overlap, 32000, bytesPerSample)
	assert.Equal(t, 0, bytes%bytesPerSample, "must be aligned to sample boundary")
	assert.Equal(t, 213332, bytes)
}

func TestOverlapBytes_48kHz3s(t *testing.T) {
	t.Parallel()
	const bytesPerSample = 2
	overlap := effectiveOverlap(2*time.Second, 3*time.Second, 3*time.Second)
	bytes := overlapBytes(overlap, 48000, bytesPerSample)
	assert.Equal(t, 192000, bytes)
}
