//go:build openvino

package classifier

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TestOpenVINOFamilies_PostPlanFailureRefusesWhenExplicit drives Perch, v3.0 and the bat
// extractor past an accepted plan to a failing step (a model file that does not exist, so
// the compile, output detection or library init fails): an explicit selection refuses with
// init_failed or load_failed and builds no ONNX Runtime instance, the result the
// BirdNET v2.4 test pins for its own path. A host whose plan declines (no OpenVINO device
// or CPU support) skips: the failing step is not reached there.
func TestOpenVINOFamilies_PostPlanFailureRefusesWhenExplicit(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing_no_dft.onnx")
	labels := []string{"a", "b"}
	families := []struct {
		name    string
		modelID string
		try     func() error
	}{
		{"Perch v2", RegistryIDPerchV2, func() error {
			_, _, _, err := tryPerchOpenVINO(&PerchConfig{ModelPath: missing, Backend: conf.BackendPrefOpenVINO, OpenVINODevice: conf.OVDeviceCPU}, labels)
			return err
		}},
		{"BirdNET v3.0 preview", RegistryIDBirdNETV3, func() error {
			_, _, _, err := tryBirdNETV3OpenVINO(&BirdNETV3Config{RegistryID: RegistryIDBirdNETV3, ModelPath: missing, Backend: conf.BackendPrefOpenVINO, OpenVINODevice: conf.OVDeviceCPU}, labels)
			return err
		}},
		{"BirdNET+ v3.0", RegistryIDBirdNETPlusV3, func() error {
			_, _, _, err := tryBirdNETV3OpenVINO(&BirdNETV3Config{RegistryID: RegistryIDBirdNETPlusV3, ModelPath: missing, Backend: conf.BackendPrefOpenVINO, OpenVINODevice: conf.OVDeviceCPU}, labels)
			return err
		}},
		{"bats", RegistryIDBat, func() error {
			_, _, _, err := tryBatOpenVINO(&BatModelConfig{EmbeddingModelPath: missing, Backend: conf.BackendPrefOpenVINO, OpenVINODevice: conf.OVDeviceCPU}, 1024)
			return err
		}},
	}
	for _, fam := range families {
		t.Run(fam.name, func(t *testing.T) {
			t.Parallel()
			err := fam.try()
			refusal, ok := errors.AsType[*OpenVINORefusalError](err)
			require.Truef(t, ok, "an explicit selection must refuse, got %v", err)
			assert.Equal(t, fam.modelID, refusal.ModelID)
			switch refusal.Reason {
			case ovReasonInitFailed, ovReasonLoadFailed:
				assert.Equal(t, inference.OVDeviceCPU, refusal.Device, "a post-plan refusal names the planned device")
			default:
				t.Skipf("the plan declined on this host (%s), so the failing step is not reached", refusal.Reason)
			}
		})
	}
}
