package hwprofile

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProfileCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile Profile
		want    []string
	}{
		{
			name: "amd64 with only the built-in backend",
			profile: Profile{
				Arch:     archAMD64,
				Backends: Backends{TFLite: BackendStatus{Available: true}},
			},
			want: []string{CapX86_64, CapTFLite},
		},
		{
			name: "raspberry pi 5 class arm64 emits the a76 and fp16 tokens",
			profile: Profile{
				Arch:         archARM64,
				HasNativeF16: true,
				Backends:     Backends{TFLite: BackendStatus{Available: true}},
			},
			want: []string{CapAArch64, CapAArch64A76, CapTFLite, CapFP16Native},
		},
		{
			name: "raspberry pi 4 class arm64 emits neither",
			profile: Profile{
				Arch:     archARM64,
				Backends: Backends{TFLite: BackendStatus{Available: true}},
			},
			want: []string{CapAArch64, CapTFLite},
		},
		{
			name: "32-bit arm reports the variant resolved from cpuinfo",
			profile: Profile{
				Arch:     archARM,
				CPUArch:  "armv7l",
				Backends: Backends{TFLite: BackendStatus{Available: true}},
			},
			want: []string{"armv7l", CapTFLite},
		},
		{
			name: "32-bit arm with an unresolved variant emits no arch token",
			profile: Profile{
				Arch:     archARM,
				CPUArch:  archARM,
				Backends: Backends{TFLite: BackendStatus{Available: true}},
			},
			want: []string{CapTFLite},
		},
		{
			name: "onnx runtime present",
			profile: Profile{
				Arch: archAMD64,
				Backends: Backends{
					TFLite: BackendStatus{Available: true},
					ONNX:   BackendStatus{Available: true, Version: "1.25.1"},
				},
			},
			want: []string{CapX86_64, CapTFLite, CapONNXRuntimeCPU},
		},
		{
			name: "openvino build without a gpu device",
			profile: Profile{
				Arch: archAMD64,
				Backends: Backends{
					TFLite:   BackendStatus{Available: true},
					OpenVINO: OpenVINOStatus{Supported: true, Devices: []string{deviceCPU}},
				},
			},
			want: []string{CapX86_64, CapTFLite, CapOpenVINOCPU},
		},
		{
			name: "openvino build with a gpu device",
			profile: Profile{
				Arch: archAMD64,
				Backends: Backends{
					TFLite:   BackendStatus{Available: true},
					OpenVINO: OpenVINOStatus{Supported: true, Devices: []string{deviceCPU, deviceGPU}},
				},
			},
			want: []string{CapX86_64, CapTFLite, CapOpenVINOCPU, CapOpenVINOGPU},
		},
		{
			name: "memory below the threshold",
			profile: Profile{
				Arch:          archARM64,
				TotalRAMBytes: 1024 * 1024 * 1024,
				Backends:      Backends{TFLite: BackendStatus{Available: true}},
			},
			want: []string{CapAArch64, CapTFLite, CapLowRAM},
		},
		{
			name: "memory exactly at the threshold is not low",
			profile: Profile{
				Arch:          archARM64,
				TotalRAMBytes: lowRAMThresholdBytes,
				Backends:      Backends{TFLite: BackendStatus{Available: true}},
			},
			want: []string{CapAArch64, CapTFLite},
		},
		{
			name: "unknown memory does not claim low memory",
			profile: Profile{
				Arch:     archARM64,
				Backends: Backends{TFLite: BackendStatus{Available: true}},
			},
			want: []string{CapAArch64, CapTFLite},
		},
		{
			name: "intel gpu generation token is emitted even when the gpu is unusable",
			profile: Profile{
				Arch:     archAMD64,
				Backends: Backends{TFLite: BackendStatus{Available: true}},
				Accelerators: []Accelerator{
					{Vendor: VendorIntel, Generation: 12, Reasons: []string{ReasonRenderNodeUnavailable}},
				},
			},
			want: []string{CapX86_64, CapTFLite, "openvino-gpu-intel-gen12"},
		},
		{
			name: "two intel gpus of the same generation yield one token",
			profile: Profile{
				Arch:     archAMD64,
				Backends: Backends{TFLite: BackendStatus{Available: true}},
				Accelerators: []Accelerator{
					{Vendor: VendorIntel, Generation: 12},
					{Vendor: VendorIntel, Generation: 12},
				},
			},
			want: []string{CapX86_64, CapTFLite, "openvino-gpu-intel-gen12"},
		},
		{
			name: "non-intel accelerators contribute no generation token",
			profile: Profile{
				Arch:     archAMD64,
				Backends: Backends{TFLite: BackendStatus{Available: true}},
				Accelerators: []Accelerator{
					{Vendor: VendorNVIDIA, Reasons: []string{ReasonNoRuntime}},
				},
			},
			want: []string{CapX86_64, CapTFLite},
		},
		{
			name:    "an empty profile yields no tokens rather than a wrong one",
			profile: Profile{},
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.profile.Capabilities())
		})
	}
}

func TestProfileCapabilitiesIsPure(t *testing.T) {
	t.Parallel()

	profile := Profile{
		Arch:         archARM64,
		HasNativeF16: true,
		Backends:     Backends{TFLite: BackendStatus{Available: true}},
		Accelerators: []Accelerator{{Vendor: VendorIntel, Generation: 12}},
	}

	first := profile.Capabilities()
	second := profile.Capabilities()

	// Capabilities is the join key against manifest selection rules, so a second
	// call has to produce the same tokens. The receiver is a value and dedupe
	// allocates, so neither can leave residue in the profile between calls.
	assert.Equal(t, first, second)
}
