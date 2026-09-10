package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/conf"
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

func TestEffectiveOverlap_ZeroBaseIsSafe(t *testing.T) {
	t.Parallel()
	assert.Equal(t, time.Duration(0), effectiveOverlap(2*time.Second, 0, 5*time.Second))
}

func TestOverlapToBytes_Alignment(t *testing.T) {
	t.Parallel()
	frame := conf.NumChannels * conf.BytesPerSample
	overlap := effectiveOverlap(2*time.Second, 3*time.Second, 5*time.Second)
	bytes := overlapToBytes(overlap, 32000)
	assert.Equal(t, 0, bytes%frame, "must be aligned to a whole sample frame")
	// mono 16-bit: 3.3333s * 32000 * 2 = 213332 bytes
	assert.Equal(t, 213332, bytes)
}

func TestOverlapToBytes_48kHz3s(t *testing.T) {
	t.Parallel()
	overlap := effectiveOverlap(2*time.Second, 3*time.Second, 3*time.Second)
	bytes := overlapToBytes(overlap, 48000)
	assert.Equal(t, 192000, bytes)
}

func TestOverlapToBytes_NonPositive(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 0, overlapToBytes(0, 48000))
	assert.Equal(t, 0, overlapToBytes(-1, 48000))
	assert.Equal(t, 0, overlapToBytes(time.Second, 0))
}

func TestResolveModelOverlap_BatIsFixedHalf(t *testing.T) {
	t.Parallel()
	spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second, RawSampleRate: 256000}
	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.4 // must be ignored for the bat model
	assert.Equal(t, 1500*time.Millisecond, ResolveModelOverlap(RegistryIDBat, spec, s))
}

func TestResolveModelOverlap_BirdNET3sHonorsOverlap(t *testing.T) {
	t.Parallel()
	spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.4
	// base == model clip, so effective overlap equals the configured value.
	assert.Equal(t, 2400*time.Millisecond, ResolveModelOverlap("BirdNET_V2.4", spec, s))
}

func TestResolveModelOverlap_Perch5sScales(t *testing.T) {
	t.Parallel()
	spec := ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second}
	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.4
	// 2.4s on a 3s base = 80% -> 80% of 5s = 4.0s.
	assert.Equal(t, 4*time.Second, ResolveModelOverlap(RegistryIDPerchV2, spec, s))
}

func TestResolveModelOverlap_ZeroOverlap(t *testing.T) {
	t.Parallel()
	spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	s := &conf.Settings{}
	s.BirdNET.Overlap = 0
	assert.Equal(t, time.Duration(0), ResolveModelOverlap("BirdNET_V2.4", spec, s))
}

func TestResolveModelOverlap_NilSettingsIsZero(t *testing.T) {
	t.Parallel()
	spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	assert.Equal(t, time.Duration(0), ResolveModelOverlap("BirdNET_V2.4", spec, nil))
}

func TestResolveModelOverlap_ZeroClipLengthNeverNegative(t *testing.T) {
	t.Parallel()
	// A degenerate/unset spec (ClipLength 0) must resolve to a non-negative
	// overlap, not a negative clamp target. Guards TestPrimaryModelInfo.
	spec := ModelSpec{SampleRate: 48000}
	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.4
	assert.Equal(t, time.Duration(0), ResolveModelOverlap("BirdNET_V2.4", spec, s))
}

func TestResolveModelOverlap_ClampsBelowClipLength(t *testing.T) {
	t.Parallel()
	spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	s := &conf.Settings{}
	// Pathological overlap at/above the clip length must be clamped so a
	// strictly positive read size remains.
	s.BirdNET.Overlap = 3.0
	got := ResolveModelOverlap("BirdNET_V2.4", spec, s)
	assert.LessOrEqual(t, got, spec.ClipLength-minAnalysisStep)
	assert.Positive(t, spec.ClipLength-got, "read step must stay positive")
}
