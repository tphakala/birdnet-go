package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference"
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

// TestBirdNET_RuntimeInfo_PublishAndRestore verifies the atomic runtime-triplet
// mechanism behind RuntimeInfo(): an unpublished instance reports the not-loaded
// triplet, setRuntimeInfo publishes a self-consistent triplet read lock-free, and
// storing a snapshotted pointer restores it. The store-snapshot step is exactly
// what reloadModelInternal's rollback performs on a failed reload, so this covers
// the rollback restoration without needing a native backend to drive the full
// reload path.
func TestBirdNET_RuntimeInfo_PublishAndRestore(t *testing.T) {
	t.Parallel()

	bn := &BirdNET{}

	// Before the first publish: not-loaded triplet (Unknown device, empty rest).
	device, backend, precision := bn.RuntimeInfo()
	assert.Equal(t, deviceUnknown, device)
	assert.Empty(t, backend)
	assert.Empty(t, precision)

	// Publish an initial triplet and snapshot the pointer (as the reload does).
	bn.setRuntimeInfo(deviceCPU, BackendTFLite, string(QuantizationFP32))
	snapshot := bn.runtime.Load()

	// Republish a new triplet (an OpenVINO/GPU/FP16 reload attempt).
	bn.setRuntimeInfo(inference.OVDeviceGPU, BackendOpenVINO, string(QuantizationFP16))
	device, backend, precision = bn.RuntimeInfo()
	assert.Equal(t, inference.OVDeviceGPU, device)
	assert.Equal(t, BackendOpenVINO, backend)
	assert.Equal(t, string(QuantizationFP16), precision)

	// Roll back to the snapshot, exactly as reloadModelInternal does on failure.
	bn.runtime.Store(snapshot)
	device, backend, precision = bn.RuntimeInfo()
	assert.Equal(t, deviceCPU, device, "rollback must restore the previous device")
	assert.Equal(t, BackendTFLite, backend, "rollback must restore the previous backend")
	assert.Equal(t, string(QuantizationFP32), precision, "rollback must restore the previous precision")
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
		overlap  time.Duration
		expected time.Duration
	}{
		{"3s clip, 50% overlap", 3 * time.Second, 1500 * time.Millisecond, 1500 * time.Millisecond},
		{"5s clip, 50% overlap", 5 * time.Second, 2500 * time.Millisecond, 2500 * time.Millisecond},
		{"3s clip, 2.4s overlap", 3 * time.Second, 2400 * time.Millisecond, 600 * time.Millisecond},
		{"3s clip, zero overlap", 3 * time.Second, 0, 3 * time.Second},
		{"5s clip, 4s overlap", 5 * time.Second, 4 * time.Second, 1 * time.Second},
		{"overlap == clip floored", 3 * time.Second, 3 * time.Second, minAnalysisStep},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := ModelSpec{SampleRate: 48000, ClipLength: tt.clip}
			assert.Equal(t, tt.expected, spec.BufferInterval(tt.overlap))
		})
	}
}

func TestModelSpec_BufferDimensions(t *testing.T) {
	t.Parallel()

	// BirdNET v2.4: 48kHz, 3s, mono 16-bit -> 288000 clip bytes.
	spec := ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	frame := conf.NumChannels * conf.BytesPerSample

	t.Run("50% overlap", func(t *testing.T) {
		t.Parallel()
		clip, overlap, read := spec.BufferDimensions(1500 * time.Millisecond)
		assert.Equal(t, 288000, clip)
		assert.Equal(t, 144000, overlap)
		assert.Equal(t, 144000, read)
	})

	t.Run("2.4s overlap gives 0.6s step", func(t *testing.T) {
		t.Parallel()
		clip, overlap, read := spec.BufferDimensions(2400 * time.Millisecond)
		assert.Equal(t, 288000, clip)
		assert.Equal(t, 230400, overlap) // 2.4s * 48000 * 2
		assert.Equal(t, 57600, read)     // 0.6s * 48000 * 2
	})

	t.Run("zero overlap reads whole clip", func(t *testing.T) {
		t.Parallel()
		clip, overlap, read := spec.BufferDimensions(0)
		assert.Equal(t, 288000, clip)
		assert.Equal(t, 0, overlap)
		assert.Equal(t, 288000, read)
	})

	t.Run("overlap at clip length is clamped to keep read positive", func(t *testing.T) {
		t.Parallel()
		clip, overlap, read := spec.BufferDimensions(3 * time.Second)
		assert.Equal(t, 288000, clip)
		assert.Positive(t, read, "read size must stay positive")
		assert.Equal(t, clip-overlap, read)
		assert.Equal(t, 0, read%frame, "read size must be frame-aligned")
	})

	t.Run("overlap bytes are frame-aligned", func(t *testing.T) {
		t.Parallel()
		_, overlap, _ := spec.BufferDimensions(2400 * time.Millisecond)
		assert.Equal(t, 0, overlap%frame)
	})
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
