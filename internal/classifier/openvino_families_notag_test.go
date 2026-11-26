//go:build !openvino

package classifier

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TestOpenVINOFamilies_RefuseWithoutORT drives each OpenVINO-capable family
// through its real constructor on a build without OpenVINO: an explicit selection
// is refused with the not-built code before any ONNX Runtime build, and auto still
// takes the ONNX Runtime path.
func TestOpenVINOFamilies_RefuseWithoutORT(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing_no_dft.onnx")

	families := []struct {
		name    string
		modelID string
		build   func(t *testing.T, backend, device string) error
	}{
		{"BirdNET v2.4", DefaultModelVersion, func(t *testing.T, backend, device string) error {
			t.Helper()
			settings := &conf.Settings{}
			settings.BirdNET.Backend, settings.BirdNET.OpenVINODevice = backend, device
			bn := &BirdNET{Settings: settings}
			bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX, CustomPath: missing}
			return bn.initializeModel()
		}},
		{"Perch v2", RegistryIDPerchV2, func(t *testing.T, backend, device string) error {
			t.Helper()
			_, err := NewPerch(&PerchConfig{
				ModelPath: missing, LabelPath: writeLabelFile(t), Backend: backend, OpenVINODevice: device,
			})
			return err
		}},
		{"BirdNET+ v3.0", RegistryIDBirdNETPlusV3, func(t *testing.T, backend, device string) error {
			t.Helper()
			if !inference.CheckORTAvailability("").Available {
				t.Skip("ONNX Runtime is not installed: NewBirdNETV3 initializes it before the OpenVINO plan")
			}
			_, err := NewBirdNETV3(&BirdNETV3Config{
				RegistryID: RegistryIDBirdNETPlusV3, ModelPath: missing, LabelPath: writeLabelFile(t),
				Backend: backend, OpenVINODevice: device,
			})
			return err
		}},
		{"bats", RegistryIDBat, func(t *testing.T, backend, device string) error {
			t.Helper()
			_, _, _, err := tryBatOpenVINO(&BatModelConfig{Backend: backend, OpenVINODevice: device}, 1024)
			return err
		}},
	}
	for _, fam := range families {
		for _, form := range explicitOpenVINOForms {
			t.Run(fam.name+"/"+form.name+" is refused", func(t *testing.T) {
				requireNoORTFallback(t, fam.build(t, form.backend, form.device), fam.modelID)
			})
		}
		t.Run(fam.name+"/auto keeps the fallback", func(t *testing.T) {
			if err := fam.build(t, conf.BackendPrefAuto, conf.OVDeviceAuto); err != nil {
				requireNotRefusal(t, err)
			}
		})
	}
}

// TestTryOpenVINO_NoTagDeclineIsNilForAuto pins the (nil, nil) "use ONNX Runtime"
// result of the family try functions under auto without the openvino tag.
func TestTryOpenVINO_NoTagDeclineIsNilForAuto(t *testing.T) {
	t.Parallel()
	ext, device, precision, err := tryBatOpenVINO(&BatModelConfig{Backend: conf.BackendPrefAuto, OpenVINODevice: conf.OVDeviceAuto}, 1024)
	require.NoError(t, err)
	assert.Nil(t, ext, "a declined OV path must return a nil extractor (no typed-nil trap)")
	assert.Empty(t, device)
	assert.Empty(t, precision)
}
