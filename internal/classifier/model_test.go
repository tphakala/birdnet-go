package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// Compile-time check that BirdNET implements ModelInstance.
var _ ModelInstance = (*BirdNET)(nil)

// Compile-time check that Bat implements ModelInstance (Perch has its own in
// perch_onnx_test.go). Keeps the three production implementers symmetric so a
// future edit to one of their methods (e.g. RuntimeInfo) fails fast at build.
var _ ModelInstance = (*Bat)(nil)

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
