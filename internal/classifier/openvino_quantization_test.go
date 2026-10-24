package classifier

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TestApplyOpenVINOQuantizationPolicy pins the INT8 rule for BirdNET v2.4 on
// OpenVINO (GitHub #4423): INT8 weights overflow under OpenVINO's default f16 on
// the A76 CPU, so the auto backend must hand them to ONNX Runtime, and an
// explicit backend=openvino must run them at f32. The policy is a pure function
// so it is testable without the openvino build tag, where openVINOPlanFor always
// declines.
func TestApplyOpenVINOQuantizationPolicy(t *testing.T) {
	t.Parallel()

	cpuF16 := openVINOPlan{device: inference.OVDeviceCPU, outputIndex: birdnetLogitsOutputIndex}
	gpuF32 := openVINOPlan{device: inference.OVDeviceGPU, outputIndex: birdnetLogitsOutputIndex, precision: inference.OVPrecisionF32}

	tests := []struct {
		name        string
		plan        openVINOPlan
		ok          bool
		reason      string
		backendPref string
		quant       Quantization
		wantPlan    openVINOPlan
		wantOK      bool
		wantReason  string
	}{
		{
			name: "INT8 on auto backend declines to ONNX Runtime", plan: cpuF16, ok: true,
			backendPref: conf.BackendPrefAuto, quant: QuantizationINT8,
			wantPlan: openVINOPlan{}, wantOK: false, wantReason: ovReasonUnverifiedWeights,
		},
		{
			name: "INT8 on empty backend pref is auto and declines", plan: cpuF16, ok: true,
			backendPref: "", quant: QuantizationINT8,
			wantPlan: openVINOPlan{}, wantOK: false, wantReason: ovReasonUnverifiedWeights,
		},
		{
			name: "INT8 on the GPU under auto also declines", plan: gpuF32, ok: true,
			backendPref: conf.BackendPrefAuto, quant: QuantizationINT8,
			wantPlan: openVINOPlan{}, wantOK: false, wantReason: ovReasonUnverifiedWeights,
		},
		{
			name: "INT8 with explicit openvino backend is forced to f32", plan: cpuF16, ok: true,
			backendPref: conf.BackendPrefOpenVINO, quant: QuantizationINT8,
			wantPlan: openVINOPlan{device: inference.OVDeviceCPU, outputIndex: birdnetLogitsOutputIndex, precision: inference.OVPrecisionF32},
			wantOK:   true,
		},
		{
			name: "FP32 weights keep the f16 CPU default", plan: cpuF16, ok: true,
			backendPref: conf.BackendPrefAuto, quant: QuantizationFP32,
			wantPlan: cpuF16, wantOK: true,
		},
		{
			name: "unrecognized weights on auto decline like INT8", plan: cpuF16, ok: true,
			backendPref: conf.BackendPrefAuto, quant: QuantizationUnknown,
			wantPlan: openVINOPlan{}, wantOK: false, wantReason: ovReasonUnverifiedWeights,
		},
		{
			name: "unrecognized weights on explicit openvino run at f32", plan: cpuF16, ok: true,
			backendPref: conf.BackendPrefOpenVINO, quant: QuantizationUnknown,
			wantPlan: openVINOPlan{device: inference.OVDeviceCPU, outputIndex: birdnetLogitsOutputIndex, precision: inference.OVPrecisionF32},
			wantOK:   true,
		},
		{
			name: "FP16 weights keep the plan unchanged", plan: cpuF16, ok: true,
			backendPref: conf.BackendPrefAuto, quant: QuantizationFP16,
			wantPlan: cpuF16, wantOK: true,
		},
		{
			name: "a declined plan stays declined for INT8 on explicit openvino", plan: openVINOPlan{}, ok: false, reason: ovReasonNoDevice,
			backendPref: conf.BackendPrefOpenVINO, quant: QuantizationINT8,
			wantPlan: openVINOPlan{}, wantOK: false, wantReason: ovReasonNoDevice,
		},
		{
			name: "a declined plan keeps its original reason for INT8", plan: openVINOPlan{}, ok: false, reason: ovReasonBackendONNX,
			backendPref: conf.BackendPrefONNX, quant: QuantizationINT8,
			wantPlan: openVINOPlan{}, wantOK: false, wantReason: ovReasonBackendONNX,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan, ok, reason := applyOpenVINOQuantizationPolicy(tt.plan, tt.ok, tt.reason, tt.backendPref, tt.quant)
			assert.Equal(t, tt.wantPlan, plan)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

// TestBirdNETV24CatalogModelFilesDetectQuantization pins that every BirdNET v2.4
// gallery build's model file resolves to its declared precision from its
// filename. Model init and the load gate both take the precision from the file
// name (detectQuantization), so a gallery FP32 build renamed without its fp32
// token would silently lose OpenVINO on auto, and a build whose name misstated
// its precision could get the wrong plan (GitHub #4423).
func TestBirdNETV24CatalogModelFilesDetectQuantization(t *testing.T) {
	t.Parallel()
	entry, ok := primaryCatalogEntry()
	require.True(t, ok, "the catalog must carry the BirdNET v2.4 entry")
	want := map[string]Quantization{"int8": QuantizationINT8, "fp32": QuantizationFP32}
	checked := 0
	for i := range entry.Variants {
		v := &entry.Variants[i]
		for j := range v.Files {
			if v.Files[j].Role != RoleModel {
				continue
			}
			q, known := want[v.Precision]
			require.True(t, known, "variant %s declares precision %q with no expected quantization", v.ID, v.Precision)
			assert.Equal(t, q, detectQuantization(v.Files[j].LocalName), "variant %s file %s", v.ID, v.Files[j].LocalName)
			checked++
		}
	}
	assert.Positive(t, checked, "the BirdNET v2.4 entry must declare at least one model file")
}

// stubBirdNETV24BasePlan replaces the base OpenVINO plan with an accepted plan
// for the rest of the test, so the quantization policy is reachable through the
// production entry points in a build without the openvino tag. Callers must NOT
// run in parallel: it mutates package state.
func stubBirdNETV24BasePlan(t *testing.T, plan openVINOPlan) {
	t.Helper()
	orig := birdnetV24BasePlan
	birdnetV24BasePlan = func(*conf.BirdNETConfig) (openVINOPlan, bool, string) {
		return plan, true, ""
	}
	t.Cleanup(func() { birdnetV24BasePlan = orig })
}

// TestBirdNETOpenVINOPlan_QuantizationPolicyWired drives the weight-precision
// rule through (*BirdNET).openVINOPlan, the model-init entry point, so dropping
// the policy call or the quantization it is given fails here and not only in the
// pure policy test. The precision must come from the model file path, as in the
// load gate, not from ModelInfo.Quantization: the legacy birdnet.version path
// keeps the registry's FP32 for any file. Not parallel: stubs package state.
func TestBirdNETOpenVINOPlan_QuantizationPolicyWired(t *testing.T) {
	cpuF16 := openVINOPlan{device: inference.OVDeviceCPU, outputIndex: birdnetLogitsOutputIndex}
	cpuF32 := openVINOPlan{device: inference.OVDeviceCPU, outputIndex: birdnetLogitsOutputIndex, precision: inference.OVPrecisionF32}
	stubBirdNETV24BasePlan(t, cpuF16)

	entry, ok := primaryCatalogEntry()
	require.True(t, ok, "the catalog must carry the BirdNET v2.4 entry")
	int8File := filepath.Join("models", "birdnet-v2.4", modelRoleLocalName(t, variantByID(t, &entry, "int8-arm-dfttrunc").Files))
	fp32File := filepath.Join("models", "birdnet-v2.4", modelRoleLocalName(t, variantByID(t, &entry, "fp32-dfttrunc").Files))
	stockFile := "/models/" + DefaultBirdNETINT8ONNXModelName
	tokenlessFile := filepath.Join("data", "model", "my_birdnet.onnx")

	tests := []struct {
		name        string
		path        string
		infoQuant   Quantization
		backendPref string
		wantOK      bool
		wantReason  string
		wantPlan    openVINOPlan
	}{
		{name: "stock INT8 file on auto declines", path: stockFile, infoQuant: QuantizationINT8,
			backendPref: conf.BackendPrefAuto, wantReason: ovReasonUnverifiedWeights},
		{name: "gallery INT8 build on explicit openvino runs at f32", path: int8File, infoQuant: QuantizationINT8,
			backendPref: conf.BackendPrefOpenVINO, wantOK: true, wantPlan: cpuF32},
		{name: "gallery FP32 build on auto keeps the f16 CPU plan", path: fp32File, infoQuant: QuantizationFP32,
			backendPref: conf.BackendPrefAuto, wantOK: true, wantPlan: cpuF16},
		{name: "file with no precision token on auto declines", path: tokenlessFile, infoQuant: QuantizationFP32,
			backendPref: conf.BackendPrefAuto, wantReason: ovReasonUnverifiedWeights},
		{name: "INT8 path wins over FP32 ModelInfo on auto", path: int8File, infoQuant: QuantizationFP32,
			backendPref: conf.BackendPrefAuto, wantReason: ovReasonUnverifiedWeights},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bn := &BirdNET{Settings: &conf.Settings{}}
			bn.Settings.BirdNET.Backend = tt.backendPref
			bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX, Quantization: tt.infoQuant, CustomPath: tt.path}
			plan, ok, reason := bn.openVINOPlan()
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
			assert.Equal(t, tt.wantPlan, plan)
		})
	}
}

// TestPrimaryVariantUsable_QuantizationPolicyWired drives the weight-precision rule through
// the installed-variant load gate on a host with a loadable OpenVINO library and
// no ONNX Runtime: the gate must agree with model init, refusing an INT8 build
// on auto (init would run it on the missing ORT) and accepting it on an explicit
// openvino backend. Not parallel: stubs package state and the process-wide
// settings snapshot, which currentSettings prefers over the Orchestrator's own.
func TestPrimaryVariantUsable_QuantizationPolicyWired(t *testing.T) {
	stubBirdNETV24BasePlan(t, openVINOPlan{device: inference.OVDeviceCPU, outputIndex: birdnetLogitsOutputIndex})
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	entry, ok := primaryCatalogEntry()
	require.True(t, ok, "the catalog must carry the BirdNET v2.4 entry")
	int8File := modelRoleLocalName(t, variantByID(t, &entry, "int8-arm-dfttrunc").Files)
	fp32File := modelRoleLocalName(t, variantByID(t, &entry, "fp32-dfttrunc").Files)

	tests := []struct {
		name        string
		file        string
		backendPref string
		want        bool
	}{
		{"INT8 build on auto is refused", int8File, conf.BackendPrefAuto, false},
		{"INT8 build on explicit openvino is accepted", int8File, conf.BackendPrefOpenVINO, true},
		{"FP32 build on auto is accepted", fp32File, conf.BackendPrefAuto, true},
		{"file with no precision token on auto is refused", "my_birdnet.onnx", conf.BackendPrefAuto, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &conf.Settings{}
			settings.BirdNET.Backend = tt.backendPref
			conf.StoreSettings(settings)
			o := &Orchestrator{
				ortAvailable: func(string) bool { return false },
				ovLoadable:   func(string) bool { return true },
			}
			o.updateSettings(settings)
			assert.Equal(t, tt.want, o.primaryVariantUsable(filepath.Join("models", "birdnet-v2.4", tt.file)))
		})
	}
}
