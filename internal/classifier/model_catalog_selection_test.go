package classifier_test

import (
	"slices"
	"strings"
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
// selection intent (Perch's "low-ram" template maps to the regional no-dft-fp32
// slice, the int8-arm builds being withdrawn; a region-resolved CPU host prefers the region's slice over the global
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
			// region's no-dft-fp32 slice (the withdrawn int8-arm slice is Legacy and
			// never recommended). 400 MiB is below every GLOBAL perch floor (fp32 700,
			// no-dft-fp32 750; the withdrawn int8-arm 350 is Legacy) and above the
			// highest regional no-dft-fp32 floor (350, amazonia and andes), so a
			// regional tile must win.
			lowRAM := 400 * mib
			gotPerch := recommendedVariantID(&perch, []string{hwprofile.CapAArch64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapLowRAM}, lowRAM, slug)
			assert.Equalf(t, "no-dft-fp32@"+slug, gotPerch, "low-RAM aarch64 host in %q must get the regional no-dft-fp32 slice", slug)
			assert.Equalf(t, "regional/"+slug+"/perch_v2_"+slug+"_no_dft_fp32.onnx", modelRemotePath(t, &perch, gotPerch),
				"recommended perch variant must own the manifest low-ram selection path")

			// BirdNET v3.0: a plain CPU host in a region prefers the region's fp32
			// slice (region.matched +100 + backend.recommended +40 = 140) over the
			// region's fp16 slice (+100 + backend.supported +10 = 110) and over the
			// global model (which loses the fallback bonus once a slice matches).
			gotV30 := recommendedVariantID(&v30, []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU}, 8*1024*mib, slug)
			assert.Equalf(t, "fp32@"+slug, gotV30, "CPU host in %q must get the regional fp32 slice", slug)
		})
	}

	// A tile covers only its region's species, so a host whose own region's slice
	// does not fit, or that has no resolved region, gets no recommendation rather
	// than a foreign tile. The withdrawn int8 builds raised the Perch floor, which
	// is what exposes this: amazonia's slice needs 350 MiB, and every global build
	// needs 700 MiB or more (int8-arm is Legacy).
	t.Run("no-foreign-tile-recommended", func(t *testing.T) {
		t.Parallel()
		caps := []string{hwprofile.CapAArch64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapLowRAM}

		assert.Empty(t, recommendedVariantID(&perch, caps, 300*mib, "amazonia"), "own slice blocked at 300 MiB: nothing, not another region's slice")
		assert.Equal(t, "no-dft-fp32@amazonia", recommendedVariantID(&perch, caps, 400*mib, "amazonia"), "own slice fits at 400 MiB")
	})

	// The same rule holds for every family shape: below the global floors, with
	// regional slices that would fit, a host with no region or an unmatched
	// region is recommended nothing.
	for _, id := range []string{"perch-v2", "birdnet-v3.0", "birdnet-plus-v3.0"} {
		t.Run("no-foreign-tile-"+id, func(t *testing.T) {
			t.Parallel()
			entry, ok := classifier.GetCatalogEntry(id)
			require.True(t, ok)

			// RAM just under the smallest live global floor, and a tile that fits it.
			var floorMiB int64
			for i := range entry.Variants {
				v := &entry.Variants[i]
				if v.Region == "" && !v.Legacy && (floorMiB == 0 || int64(v.Requirements.MinRAMMB) < floorMiB) {
					floorMiB = int64(v.Requirements.MinRAMMB)
				}
			}
			require.Positive(t, floorMiB)
			ram := (floorMiB - 1) * mib
			caps := []string{hwprofile.CapX86_64, hwprofile.CapAArch64, hwprofile.CapONNXRuntimeCPU}

			fitting := ""
			for _, rec := range recommend.Rank(&recommend.Input{Capabilities: caps, TotalRAMBytes: ram, Entries: []classifier.CatalogEntry{entry}}) {
				if rec.Compatible && strings.Contains(rec.VariantID, "@") {
					fitting = rec.VariantID
					break
				}
			}
			require.NotEmptyf(t, fitting, "%s needs a regional slice that fits %d MiB for this test to bite", id, ram/mib)

			assert.Emptyf(t, recommendedVariantID(&entry, caps, ram, ""), "%s: no region resolved must not recommend a tile", id)
			assert.Emptyf(t, recommendedVariantID(&entry, caps, ram, "no-such-region"), "%s: an unmatched region must not recommend a tile", id)
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

// TestRecommend_BirdNETPlusOffersTheCurrentRelease checks the recommender picks
// the 3.0.1 build of the host's tile (or the 3.0.1 global build with no region)
// and never an older release's Legacy variant, which is what makes an installed
// 3.0 differ from the recommendation the optimize rule reads.
func TestRecommend_BirdNETPlusOffersTheCurrentRelease(t *testing.T) {
	t.Parallel()

	plus, ok := classifier.GetCatalogEntry("birdnet-plus-v3.0")
	require.True(t, ok)
	caps := []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU}
	high := 8 * 1024 * mib

	for _, tc := range []struct{ name, region, want string }{
		{"region resolved to nordic", "nordic", "fp32-3.0.1@nordic"},
		{"no region", "", "fp32-3.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := recommendedVariantID(&plus, caps, high, tc.region)
			assert.Equal(t, tc.want, got)
			idx := slices.IndexFunc(plus.Variants, func(v classifier.CatalogVariant) bool { return v.ID == got })
			require.GreaterOrEqual(t, idx, 0)
			assert.False(t, plus.Variants[idx].Legacy, "a Legacy variant is never recommended")
		})
	}
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
