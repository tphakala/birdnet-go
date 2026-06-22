package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Compile-time check that BirdNET implements ModelInstance.
var _ ModelInstance = (*BirdNET)(nil)

// Compile-time check that Bat implements ModelInstance (Perch has its own in
// perch_onnx_test.go). Keeps the three production implementers symmetric so a
// future edit to one of their methods (e.g. RuntimeInfo) fails fast at build.
var _ ModelInstance = (*Bat)(nil)

// TestBirdNET_Identity_PublishAndSnapshot verifies the atomic identity snapshot
// behind ModelID/ModelName/ModelVersion/Spec: an unpublished struct-literal
// instance falls back to reading the fields directly, publishIdentity captures a
// snapshot read lock-free, and a subsequent bn.ModelInfo write WITHOUT republish
// does not change the getters. That last property is the race fix: the lock-free
// getters only ever observe a committed snapshot, never reloadModelInternal's
// in-flight write to bn.ModelInfo / bn.modelVersion. Republish (reload commit /
// rollback) then advances the snapshot.
func TestBirdNET_Identity_PublishAndSnapshot(t *testing.T) {
	t.Parallel()

	bn := &BirdNET{
		ModelInfo:    ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4", Spec: ModelSpec{SampleRate: 48000}},
		modelVersion: "v2.4-fp32",
	}

	// Unpublished struct-literal instance: getters fall back to the fields.
	assert.Equal(t, "BirdNET_V2.4", bn.ModelID())
	assert.Equal(t, "BirdNET v2.4", bn.ModelName())
	assert.Equal(t, "v2.4-fp32", bn.ModelVersion())
	assert.Equal(t, 48000, bn.Spec().SampleRate)

	// Publish the snapshot; getters now read the published value.
	bn.publishIdentity()
	assert.Equal(t, "BirdNET_V2.4", bn.ModelID())

	// A bn.ModelInfo / bn.modelVersion write WITHOUT republish must NOT change the
	// getters: this is what decouples the lock-free getters from a concurrent
	// reloadModelInternal write to those fields.
	bn.ModelInfo = ModelInfo{ID: "OTHER", Name: "Other", Spec: ModelSpec{SampleRate: 32000}}
	bn.modelVersion = "other"
	assert.Equal(t, "BirdNET_V2.4", bn.ModelID(), "getter must read the published snapshot, not the unpublished field write")
	assert.Equal(t, "BirdNET v2.4", bn.ModelName())
	assert.Equal(t, "v2.4-fp32", bn.ModelVersion())
	assert.Equal(t, 48000, bn.Spec().SampleRate)

	// Republish picks up the new values (the reload-commit / rollback step).
	bn.publishIdentity()
	assert.Equal(t, "OTHER", bn.ModelID())
	assert.Equal(t, "Other", bn.ModelName())
	assert.Equal(t, "other", bn.ModelVersion())
	assert.Equal(t, 32000, bn.Spec().SampleRate)
}

func TestModelSpecDefaults(t *testing.T) {
	t.Parallel()

	spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	assert.Equal(t, 48000, spec.SampleRate)
	assert.Equal(t, 3*time.Second, spec.ClipLength)
}

func TestModelSpec_BufferInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		clip     time.Duration
		expected time.Duration
	}{
		{"BirdNET v2.4 (3s)", 3 * time.Second, 1500 * time.Millisecond},
		{"Perch v2 (5s)", 5 * time.Second, 2500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := ModelSpec{SampleRate: 48000, ClipLength: tt.clip}
			assert.Equal(t, tt.expected, spec.BufferInterval())
		})
	}
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
