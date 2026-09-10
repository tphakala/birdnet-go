package models

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/recommend"
)

// backendRecommendedReason builds the structured reason the recommender attaches
// to name the backend it chose for the host, used to drive host-relative
// hardware-class classification in the tests below.
func backendRecommendedReason(backend string) recommend.Reason {
	return recommend.Reason{
		Code: recommend.ReasonBackendRecommended,
		Args: map[string]string{recommend.ReasonArgBackend: backend},
	}
}

func TestChannelOrDefault(t *testing.T) {
	t.Parallel()
	assert.Equal(t, classifier.ChannelStable, channelOrDefault(""), "an empty channel defaults to stable")
	assert.Equal(t, classifier.ChannelStable, channelOrDefault(classifier.ChannelStable))
	assert.Equal(t, classifier.ChannelPreview, channelOrDefault(classifier.ChannelPreview))
}

func TestVariantHardwareClass(t *testing.T) {
	t.Parallel()

	// A GPU-oriented build: recommended on GPU backends, only supported on CPU.
	gpuBackends := map[string]classifier.BackendSupport{
		backendCUDA:        {Supported: true, Recommended: true},
		backendTensorRT:    {Supported: true, Recommended: true},
		backendOpenVINOGPU: {Supported: true, Recommended: true},
		backendONNXCPU:     {Supported: true},
	}
	// An Intel-GPU-only build.
	intelGPUBackends := map[string]classifier.BackendSupport{
		backendOpenVINOGPU: {Supported: true, Recommended: true},
	}
	// A general CPU build: recommended on CPU (and also runnable on GPU). Its plain
	// target is the CPU; the GPU-optimized build is a separate variant.
	cpuBackends := map[string]classifier.BackendSupport{
		backendONNXCPU:  {Supported: true, Recommended: true},
		backendCUDA:     {Supported: true, Recommended: true},
		backendTensorRT: {Supported: true, Recommended: true},
	}
	// A CPU build recommended ONLY on the OpenVINO CPU backend, to exercise the
	// openvino-cpu operand of intrinsicGPUBackend (the onnxruntime-cpu operand
	// short-circuits it in cpuBackends above).
	openvinoCPUBackends := map[string]classifier.BackendSupport{
		backendOpenVINOCPU: {Supported: true, Recommended: true},
	}

	tests := []struct {
		name     string
		variant  classifier.CatalogVariant
		rec      recommend.Recommendation
		hasRec   bool
		hostArch string
		want     string
	}{
		{
			name:    "built-in baseline wins over everything",
			variant: classifier.CatalogVariant{ID: "builtin", BuiltIn: true, Backends: gpuBackends},
			want:    hwClassBuiltIn,
		},
		{
			name:     "host chose CUDA -> NVIDIA GPU",
			variant:  classifier.CatalogVariant{ID: "fp16", Backends: gpuBackends},
			rec:      recommend.Recommendation{Compatible: true, Reasons: []recommend.Reason{backendRecommendedReason(backendCUDA)}},
			hasRec:   true,
			hostArch: archAMD64,
			want:     hwClassGPUNvidia,
		},
		{
			name:     "host chose OpenVINO GPU -> Intel GPU",
			variant:  classifier.CatalogVariant{ID: "fp16", Backends: gpuBackends},
			rec:      recommend.Recommendation{Compatible: true, Reasons: []recommend.Reason{backendRecommendedReason(backendOpenVINOGPU)}},
			hasRec:   true,
			hostArch: archAMD64,
			want:     hwClassGPUIntel,
		},
		{
			name:     "host chose a CPU backend on amd64 -> AMD64 CPU",
			variant:  classifier.CatalogVariant{ID: "fp32", Backends: cpuBackends},
			rec:      recommend.Recommendation{Compatible: true, Reasons: []recommend.Reason{backendRecommendedReason(backendONNXCPU)}},
			hasRec:   true,
			hostArch: archAMD64,
			want:     hwClassAMD64CPU,
		},
		{
			name:     "host chose a CPU backend on arm64 -> ARM64 CPU",
			variant:  classifier.CatalogVariant{ID: "fp32", Backends: cpuBackends},
			rec:      recommend.Recommendation{Compatible: true, Reasons: []recommend.Reason{backendRecommendedReason(backendONNXCPU)}},
			hasRec:   true,
			hostArch: archARM64,
			want:     hwClassARM64CPU,
		},
		{
			name:     "arm-only variant is ARM64 CPU regardless of host arch",
			variant:  classifier.CatalogVariant{ID: "int8-arm", Requirements: classifier.VariantRequirements{Arch: []string{"aarch64"}}, Backends: cpuBackends},
			rec:      recommend.Recommendation{Compatible: true, Reasons: []recommend.Reason{backendRecommendedReason(backendONNXCPU)}},
			hasRec:   true,
			hostArch: archAMD64,
			want:     hwClassARM64CPU,
		},
		{
			name:    "no recommendation, intrinsic GPU build -> Intel GPU",
			variant: classifier.CatalogVariant{ID: "fp16", Backends: intelGPUBackends},
			want:    hwClassGPUIntel,
		},
		{
			name:    "no recommendation, no host arch, CPU build -> generic cpu",
			variant: classifier.CatalogVariant{ID: "fp32", Backends: cpuBackends},
			want:    hwClassCPU,
		},
		{
			name:     "blocked GPU variant with no reasons falls back to intrinsic NVIDIA GPU",
			variant:  classifier.CatalogVariant{ID: "fp16", Backends: gpuBackends},
			rec:      recommend.Recommendation{Compatible: false, Blockers: []recommend.Reason{{Code: "ram.insufficient"}}},
			hasRec:   true,
			hostArch: archAMD64,
			want:     hwClassGPUNvidia,
		},
		{
			name:     "no recommendation, openvino-cpu-only build classifies as CPU (arch from host)",
			variant:  classifier.CatalogVariant{ID: "fp32", Backends: openvinoCPUBackends},
			hostArch: archAMD64,
			want:     hwClassAMD64CPU,
		},
		{
			name:     "explicit non-ARM arch requirement is not ARM-only (host arch decides)",
			variant:  classifier.CatalogVariant{ID: "fp32", Requirements: classifier.VariantRequirements{Arch: []string{archAMD64}}, Backends: cpuBackends},
			rec:      recommend.Recommendation{Compatible: true, Reasons: []recommend.Reason{backendRecommendedReason(backendONNXCPU)}},
			hasRec:   true,
			hostArch: archAMD64,
			want:     hwClassAMD64CPU,
		},
		{
			name:     "aarch64 micro-arch requirement (aarch64-a76) is still ARM64 CPU",
			variant:  classifier.CatalogVariant{ID: "fp32", Requirements: classifier.VariantRequirements{Arch: []string{"aarch64-a76"}}, Backends: cpuBackends},
			rec:      recommend.Recommendation{Compatible: true, Reasons: []recommend.Reason{backendRecommendedReason(backendONNXCPU)}},
			hasRec:   true,
			hostArch: archAMD64,
			want:     hwClassARM64CPU,
		},
		{
			name:     "32-bit ARM (armv7l) requirement is ARM CPU, not ARM64",
			variant:  classifier.CatalogVariant{ID: "int8", Requirements: classifier.VariantRequirements{Arch: []string{"armv7l"}}, Backends: cpuBackends},
			rec:      recommend.Recommendation{Compatible: true, Reasons: []recommend.Reason{backendRecommendedReason(backendONNXCPU)}},
			hasRec:   true,
			hostArch: archARM64,
			want:     hwClassARMCPU,
		},
		{
			name:     "arch-neutral CPU build on a 32-bit ARM host is ARM CPU, not ARM64",
			variant:  classifier.CatalogVariant{ID: "fp32", Backends: cpuBackends},
			rec:      recommend.Recommendation{Compatible: true, Reasons: []recommend.Reason{backendRecommendedReason(backendONNXCPU)}},
			hasRec:   true,
			hostArch: archARM,
			want:     hwClassARMCPU,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := variantHardwareClass(&tt.variant, &tt.rec, tt.hasRec, tt.hostArch)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRecommendedBackendToken(t *testing.T) {
	t.Parallel()

	// Prefers the explicit backend.recommended reason over any other backend-bearing one.
	assert.Equal(t, backendCUDA, recommendedBackendToken([]recommend.Reason{
		{Code: recommend.ReasonBackendSupported, Args: map[string]string{recommend.ReasonArgBackend: backendOpenVINOGPU}},
		{Code: recommend.ReasonBackendRecommended, Args: map[string]string{recommend.ReasonArgBackend: backendCUDA}},
	}), "the backend.recommended reason wins over a plain backend-bearing reason")

	// Falls back to the first reason carrying a backend arg when no backend.recommended
	// reason is present (the fallback accumulator path).
	assert.Equal(t, backendOpenVINOGPU, recommendedBackendToken([]recommend.Reason{
		{Code: recommend.ReasonRegionMatched, Args: map[string]string{"region": "nordic"}},
		{Code: recommend.ReasonBackendSupported, Args: map[string]string{recommend.ReasonArgBackend: backendOpenVINOGPU}},
	}), "falls back to the first backend-bearing reason")

	// No backend-bearing reason yields the empty token (the intrinsic path then decides).
	assert.Empty(t, recommendedBackendToken([]recommend.Reason{
		{Code: recommend.ReasonRegionMatched, Args: map[string]string{"region": "nordic"}},
	}))
}
