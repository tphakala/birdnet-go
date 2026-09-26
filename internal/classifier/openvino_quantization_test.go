package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
			wantPlan: openVINOPlan{}, wantOK: false, wantReason: ovReasonINT8Model,
		},
		{
			name: "INT8 on empty backend pref is auto and declines", plan: cpuF16, ok: true,
			backendPref: "", quant: QuantizationINT8,
			wantPlan: openVINOPlan{}, wantOK: false, wantReason: ovReasonINT8Model,
		},
		{
			name: "INT8 on the GPU under auto also declines", plan: gpuF32, ok: true,
			backendPref: conf.BackendPrefAuto, quant: QuantizationINT8,
			wantPlan: openVINOPlan{}, wantOK: false, wantReason: ovReasonINT8Model,
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
			name: "unknown weights keep the plan unchanged", plan: cpuF16, ok: true,
			backendPref: conf.BackendPrefAuto, quant: QuantizationUnknown,
			wantPlan: cpuF16, wantOK: true,
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

// TestDetectQuantization_BirdNETV24INT8Files pins that both INT8 BirdNET v2.4
// builds resolve to QuantizationINT8 from their filenames, since the load gate
// (primaryVariantUsable) derives the quantization for the INT8 rule from the path.
func TestDetectQuantization_BirdNETV24INT8Files(t *testing.T) {
	t.Parallel()
	assert.Equal(t, QuantizationINT8, detectQuantization("/models/"+DefaultBirdNETINT8ONNXModelName))
	assert.Equal(t, QuantizationINT8, detectQuantization("/config/models/birdnet-v2.4/BirdNET_v2.4_int8_arm_dfttrunc.onnx"))
	assert.Equal(t, QuantizationFP32, detectQuantization("/config/models/birdnet-v2.4/BirdNET_v2.4_fp32_dfttrunc.onnx"))
}
