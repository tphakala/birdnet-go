//go:build openvino

package classifier

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// perchInputSampleCount is the Perch v2 input length (32 kHz * 5 s).
const perchInputSampleCount = 160000

// TestPerchOpenVINO_Functional is a hardware/lib/model-gated test that builds a
// Perch v2 classifier on the OpenVINO backend through the same tryPerchOpenVINO
// path NewPerch uses, then runs one inference. Set OV_TEST_PERCH_MODEL (a
// perch_v2_no_dft ONNX) and OV_TEST_PERCH_LABELS to run it; OV_TEST_DEVICE picks
// "auto" (default), "cpu", or "gpu", and OV_TEST_LIB optionally points at
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

	c, _, ok := tryPerchOpenVINO(&cfg, labels)
	require.True(t, ok, "Perch OpenVINO classifier must be created for the no_dft model")
	require.NotNil(t, c)
	t.Cleanup(func() { c.Close() })

	// #1112: NumSpecies must reflect the real model output dimension (== label
	// count), not merely echo len(labels).
	require.Equal(t, len(labels), c.NumSpecies())

	logits, err := c.Predict(make([]float32, perchInputSampleCount))
	require.NoError(t, err)
	require.Len(t, logits, len(labels), "Perch OV must return the index-3 logits, one per label")
}
