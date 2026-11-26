//go:build openvino

package classifier

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// perchInputSampleCount is the Perch v2 input length (32 kHz * 5 s).
const perchInputSampleCount = 160000

// TestPerchOpenVINO_Functional is a hardware/lib/model-gated test that builds a
// Perch v2 classifier on the OpenVINO backend through the same tryPerchOpenVINO
// path NewPerch uses, then runs one inference. Set OV_TEST_PERCH_MODEL (a
// perch_v2_no_dft ONNX) and OV_TEST_PERCH_LABELS to run it; OV_TEST_DEVICE picks
// "auto" (default), "cpu", "gpu", or a concrete "GPU.<n>", and OV_TEST_LIB optionally points at
// libopenvino_c. It is skipped otherwise so normal CI stays green.
//
// It proves the end-to-end Perch OV path: the no_dft gate, device selection, the
// index-3 logits output, and the #1112 fix (NumSpecies reflects the real model
// output dimension, so it equals the label count rather than echoing it).
func TestPerchOpenVINO_Functional(t *testing.T) {
	model := os.Getenv("OV_TEST_PERCH_MODEL")
	labelPath := os.Getenv("OV_TEST_PERCH_LABELS")
	if model == "" || labelPath == "" {
		t.Skip("set OV_TEST_PERCH_MODEL and OV_TEST_PERCH_LABELS to run the Perch OpenVINO functional test")
	}

	labelData, err := os.ReadFile(labelPath)
	require.NoError(t, err)
	labels, err := ParsePerchLabels(labelData)
	require.NoError(t, err)
	require.NotEmpty(t, labels)

	device := os.Getenv("OV_TEST_DEVICE")
	if device == "" {
		device = conf.OVDeviceAuto
	}

	cfg := PerchConfig{
		ModelPath:      model,
		LabelPath:      labelPath,
		OpenVINOPath:   os.Getenv("OV_TEST_LIB"),
		Backend:        conf.BackendPrefOpenVINO,
		OpenVINODevice: device,
	}
	t.Cleanup(func() { _ = inference.DestroyOpenVINO() })

	c, boundDevice, precisionHint, err := tryPerchOpenVINO(&cfg, labels)
	requireNotRefused(t, err, os.Getenv("OV_TEST_DEVICE"))
	require.NotNil(t, c, "Perch OpenVINO classifier must be created for the no_dft model")
	if inference.OpenVINOIsGPUDevice(boundDevice) {
		assert.Equal(t, inference.OVPrecisionF32, precisionHint,
			"Perch on the GPU must compile at f32 (f16 returns NaN logits on Intel Arc)")
	}
	t.Cleanup(func() { c.Close() })

	// #1112: NumSpecies must reflect the real model output dimension (== label
	// count), not merely echo len(labels).
	require.Equal(t, len(labels), c.NumSpecies())

	logits, err := c.Predict(make([]float32, perchInputSampleCount))
	require.NoError(t, err)
	require.Len(t, logits, len(labels), "Perch OV must return the index-3 logits, one per label")
}

// requireNotRefused is the functional-test rule for an explicit OpenVINO selection,
// which never falls back to ONNX Runtime. A refusal that only says this host cannot
// run OpenVINO (no library in the build, no device, an unsupported CPU) is a skip when
// no device was requested and a failure when one was, since OpenVINO was then expected
// to run. Every other refusal (a failed init or load, a model OpenVINO cannot run) and
// any other error fails: it is a defect of the model, the library or the OpenVINO path.
func requireNotRefused(t *testing.T, err error, requestedDevice string) {
	t.Helper()
	if refusal, ok := errors.AsType[*OpenVINORefusalError](err); ok {
		switch refusal.Reason {
		case ovReasonNotBuilt, ovReasonGPUUnavailable, ovReasonGPUDeviceMissing, ovReasonCPUUnsupported, ovReasonNoDevice:
			if requestedDevice == "" {
				t.Skipf("this host cannot run OpenVINO (%s); set a device to require it", refusal.Reason)
			}
		default:
		}
		t.Fatalf("OpenVINO refused the model (device %q requested): %v", requestedDevice, err)
	}
	require.NoError(t, err)
}
