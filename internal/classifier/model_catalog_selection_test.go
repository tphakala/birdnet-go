package classifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/recommend"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
)

// mib is one mebibyte, matching the recommender's MinRAMMB-to-bytes conversion.
const mib = int64(1) << 20

// recommendedVariantID runs the recommender for a single entry under a synthetic
// host profile and returns the id of the variant it marks Recommended (the one
// the gallery preselects), or "" when none is recommended.
func recommendedVariantID(entry *classifier.CatalogEntry, caps []string, ramBytes int64, resolvedRegion string) string {
	in := &recommend.Input{
		Capabilities:   caps,
		TotalRAMBytes:  ramBytes,
		ResolvedRegion: resolvedRegion,
		Entries:        []classifier.CatalogEntry{*entry},
	}
	for _, rec := range recommend.Rank(in) {
		if rec.Recommended {
			return rec.VariantID
		}
	}
	return ""
}

// modelRemotePath returns the RemotePath of the model-role file of the named
// variant, so a recommendation can be checked against a manifest selection path.
func modelRemotePath(t *testing.T, entry *classifier.CatalogEntry, variantID string) string {
	t.Helper()
	for i := range entry.Variants {
		if entry.Variants[i].ID != variantID {
			continue
		}
		for j := range entry.Variants[i].Files {
			if entry.Variants[i].Files[j].Role == classifier.RoleModel {
				return entry.Variants[i].Files[j].RemotePath
			}
		}
	}
	t.Fatalf("variant %q model file not found in entry %q", variantID, entry.ID)
	return ""
}

// TestRecommend_RegionalTiles is the golden test for the #1439 deliverable: that
// the region-sliced variants win recommendation on the hosts they were sliced
// for, and never hijack the global case. The expectations mirror the manifest
// selection intent (Perch's "low-ram" template maps to the regional int8-arm
// slice; a region-resolved CPU host prefers the region's slice over the global
// model on the +100 region.matched term).
func TestRecommend_RegionalTiles(t *testing.T) {
	t.Parallel()

	// A spread of slugs across tiers and both families' regions.json.
	slugs := []string{"nordic", "iberia", "andes", "amazonia", "southern-africa"}

	perch, ok := classifier.GetCatalogEntry("perch-v2")
	require.True(t, ok)
	v30, ok := classifier.GetCatalogEntry("birdnet-v3.0")
	require.True(t, ok)

	for _, slug := range slugs {
		t.Run(slug, func(t *testing.T) {
			t.Parallel()

			// Perch "low-ram" selection: a low-RAM aarch64 host in a region gets the
			// region's int8-arm slice. 300 MiB is below every GLOBAL perch floor
			// (int8-arm 350, fp32 700, no-dft-fp32 750), so all globals are blocked
			// and a regional tile must win. Among the region's two tiles, int8-arm@slug
			// outscores no-dft-fp32@slug regardless of their per-region RAM floors:
			// region +100, backend.recommended +40, and low-ram+int8 ram.constrained_fit
			// +25 = 165, versus no-dft-fp32's region +100 + backend.supported +10 = 110.
			lowRAM := 300 * mib
			gotPerch := recommendedVariantID(&perch, []string{hwprofile.CapAArch64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapLowRAM}, lowRAM, slug)
			assert.Equalf(t, "int8-arm@"+slug, gotPerch, "low-RAM aarch64 host in %q must get the regional int8-arm slice", slug)
			assert.Equalf(t, "regional/"+slug+"/perch_v2_"+slug+"_int8_arm.onnx", modelRemotePath(t, &perch, gotPerch),
				"recommended perch variant must own the manifest low-ram selection path")

			// BirdNET v3.0: a plain CPU host in a region prefers the region's fp32
			// slice (region.matched +100 + backend.recommended +40 = 140) over the
			// region's fp16 slice (+100 + backend.supported +10 = 110) and over the
			// global model (which loses the fallback bonus once a slice matches).
			gotV30 := recommendedVariantID(&v30, []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU}, 8*1024*mib, slug)
			assert.Equalf(t, "fp32@"+slug, gotV30, "CPU host in %q must get the regional fp32 slice", slug)
		})
	}

	// Guard: with no region resolved, a regional tile must never be recommended;
	// the global variant wins on its fallback bonus.
	t.Run("global-host-picks-global", func(t *testing.T) {
		t.Parallel()
		caps := []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU}
		high := 8 * 1024 * mib

		gotV30 := recommendedVariantID(&v30, caps, high, "")
		assert.Equal(t, "fp32", gotV30, "no region resolved must recommend the global v3.0 variant")

		gotPerch := recommendedVariantID(&perch, caps, high, "")
		assert.Equal(t, "fp32", gotPerch, "no region resolved must recommend a global perch variant")
	})
}

// TestRecommend_V30GlobalSelection is a regression guard that the recommender
// still reproduces every BirdNET v3.0 manifest selection key on the global
// variants. (Perch's global selection map is intentionally not asserted here: the
// manifest prefers no-dft-fp32 on x86 ORT/CUDA to sidestep the in-graph DFT op,
// while the catalog marks the global fp32 as ORT/CUDA-recommended, so the
// recommender prefers fp32 there. That predates this change and is out of scope.)
func TestRecommend_V30GlobalSelection(t *testing.T) {
	t.Parallel()

	v30, ok := classifier.GetCatalogEntry("birdnet-v3.0")
	require.True(t, ok)
	high := 8 * 1024 * mib

	cases := []struct {
		name     string
		caps     []string
		wantPath string // manifest selection value
	}{
		{"x86-64/onnxruntime", []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU}, "full/birdnet-v3.0-preview3.1-fp32-b1.onnx"},
		{"x86-64/openvino-cpu", []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapOpenVINOCPU}, "full/birdnet-v3.0-preview3.1-fp32-b1.onnx"},
		{"x86-64/openvino-gpu", []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapOpenVINOCPU, hwprofile.CapOpenVINOGPU}, "full/birdnet-v3.0-preview3.1-fp16-b1.onnx"},
		{"aarch64-a76/openvino", []string{hwprofile.CapAArch64, hwprofile.CapAArch64A76, hwprofile.CapONNXRuntimeCPU, hwprofile.CapOpenVINOCPU}, "full/birdnet-v3.0-preview3.1-fp32-b1.onnx"},
		{"aarch64/onnxruntime", []string{hwprofile.CapAArch64, hwprofile.CapONNXRuntimeCPU}, "full/birdnet-v3.0-preview3.1-fp32-b1.onnx"},
		{"cuda", []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, "cuda"}, "full/birdnet-v3.0-preview3.1-fp16-b1.onnx"},
		{"tensorrt", []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, "cuda", "tensorrt"}, "full/birdnet-v3.0-preview3.1-fp16-b1.onnx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := recommendedVariantID(&v30, tc.caps, high, "")
			require.NotEmpty(t, got, "a global variant must be recommended")
			assert.Equal(t, tc.wantPath, modelRemotePath(t, &v30, got), "recommended variant must own the manifest selection path")
		})
	}
}
