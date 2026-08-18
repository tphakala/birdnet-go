package recommend

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
)

// realEntry returns a copy of a real catalog entry by ID, so the matrix tests
// assert against the actual shipped Backends maps rather than a hand-copy that
// could drift from them.
func realEntry(t *testing.T, id string) classifier.CatalogEntry {
	t.Helper()
	for i := range classifier.EmbeddedCatalog {
		if classifier.EmbeddedCatalog[i].ID == id {
			return classifier.EmbeddedCatalog[i]
		}
	}
	t.Fatalf("catalog entry %q not found", id)
	return classifier.CatalogEntry{}
}

// recommendedVariant returns the variant marked Recommended for a catalog ID,
// or "" when none is (no compatible variant).
func recommendedVariant(recs []Recommendation, catalogID string) string {
	for i := range recs {
		if recs[i].CatalogID == catalogID && recs[i].Recommended {
			return recs[i].VariantID
		}
	}
	return ""
}

// variantRec returns the verdict for one variant, and whether it was found.
func variantRec(recs []Recommendation, catalogID, variantID string) (Recommendation, bool) {
	for i := range recs {
		if recs[i].CatalogID == catalogID && recs[i].VariantID == variantID {
			return recs[i], true
		}
	}
	return Recommendation{}, false
}

const (
	ramHigh = 16 * 1024 * 1024 * 1024
	ramLow  = 1536 * 1024 * 1024 // below the 2 GiB low-ram threshold
)

func TestRank_HardwareMatrix(t *testing.T) {
	t.Parallel()

	perch := realEntry(t, "perch-v2")
	v3 := realEntry(t, "birdnet-v3.0")

	tests := []struct {
		name        string
		caps        []string
		ram         int64
		deviceClass string
		entries     []classifier.CatalogEntry
		// wantRecommended maps catalogID -> expected recommended variant ID.
		wantRecommended map[string]string
		// wantBlocked lists (catalogID, variantID) pairs expected incompatible.
		wantBlocked [][2]string
	}{
		{
			name:        "amd64 onnx only",
			caps:        []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU},
			ram:         ramHigh,
			deviceClass: deviceClassX86,
			entries:     []classifier.CatalogEntry{perch, v3},
			wantRecommended: map[string]string{
				"perch-v2":     "fp32",
				"birdnet-v3.0": "fp32",
			},
			wantBlocked: [][2]string{{"perch-v2", "int8-arm"}},
		},
		{
			name:        "amd64 openvino cpu",
			caps:        []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapOpenVINOCPU},
			ram:         ramHigh,
			deviceClass: deviceClassX86,
			entries:     []classifier.CatalogEntry{perch, v3},
			wantRecommended: map[string]string{
				"perch-v2":     "no-dft-fp32",
				"birdnet-v3.0": "fp32",
			},
		},
		{
			name:        "amd64 intel igpu",
			caps:        []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapOpenVINOCPU, hwprofile.CapOpenVINOGPU},
			ram:         ramHigh,
			deviceClass: deviceClassX86,
			entries:     []classifier.CatalogEntry{perch, v3},
			wantRecommended: map[string]string{
				"perch-v2":     "no-dft-fp32",
				"birdnet-v3.0": "fp16",
			},
		},
		{
			// Pi5: no low-ram bonus, so int8-arm ties fp32 on score and wins on
			// smaller size. This is the documented empty-data behavior; a catalog
			// benchmark pass will rank fp32 higher on the A76.
			name:        "aarch64 pi5",
			caps:        []string{hwprofile.CapAArch64, hwprofile.CapAArch64A76, hwprofile.CapONNXRuntimeCPU, hwprofile.CapFP16Native},
			ram:         8 * 1024 * 1024 * 1024,
			deviceClass: deviceClassRPi5,
			entries:     []classifier.CatalogEntry{perch},
			wantRecommended: map[string]string{
				"perch-v2": "int8-arm",
			},
		},
		{
			name:        "aarch64 pi4 low ram",
			caps:        []string{hwprofile.CapAArch64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapLowRAM},
			ram:         ramLow,
			deviceClass: deviceClassRPi4,
			entries:     []classifier.CatalogEntry{perch},
			wantRecommended: map[string]string{
				"perch-v2": "int8-arm",
			},
		},
		{
			name:        "armv7l 32bit blocks aarch64 variant",
			caps:        []string{"armv7l", hwprofile.CapONNXRuntimeCPU},
			ram:         ramLow,
			deviceClass: "",
			entries:     []classifier.CatalogEntry{perch},
			wantRecommended: map[string]string{
				"perch-v2": "fp32",
			},
			wantBlocked: [][2]string{{"perch-v2", "int8-arm"}},
		},
		{
			name:        "no backend tokens blocks everything",
			caps:        []string{hwprofile.CapX86_64},
			ram:         ramHigh,
			deviceClass: deviceClassX86,
			entries:     []classifier.CatalogEntry{perch},
			wantRecommended: map[string]string{
				"perch-v2": "", // no compatible variant
			},
			wantBlocked: [][2]string{{"perch-v2", "fp32"}, {"perch-v2", "no-dft-fp32"}, {"perch-v2", "int8-arm"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			recs := Rank(&Input{
				Capabilities:  tt.caps,
				TotalRAMBytes: tt.ram,
				DeviceClass:   tt.deviceClass,
				Entries:       tt.entries,
			})
			for catalogID, want := range tt.wantRecommended {
				assert.Equalf(t, want, recommendedVariant(recs, catalogID), "recommended variant for %s", catalogID)
			}
			for _, pair := range tt.wantBlocked {
				rec, ok := variantRec(recs, pair[0], pair[1])
				require.Truef(t, ok, "variant %s/%s should be present", pair[0], pair[1])
				assert.Falsef(t, rec.Compatible, "variant %s/%s should be blocked", pair[0], pair[1])
				assert.NotEmptyf(t, rec.Blockers, "blocked variant %s/%s should carry blockers", pair[0], pair[1])
			}
		})
	}
}

func TestDeviceMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		benchDevice string
		deviceClass string
		want        bool
	}{
		{"x86-i7-1260P", deviceClassX86, true},
		{deviceClassX86, deviceClassX86, true},
		{"x86-n100", deviceClassX86, true},
		{"x8600", deviceClassX86, false}, // no separating hyphen: not an x86 device
		{deviceClassRPi5, deviceClassRPi5, true},
		{deviceClassRPi5, deviceClassRPi4, false},
		{deviceClassRPi5, deviceClassX86, false},
		{"x86-i7-1260P", deviceClassRPi5, false},
	}
	for _, tt := range tests {
		t.Run(tt.benchDevice+"/"+tt.deviceClass, func(t *testing.T) {
			t.Parallel()
			assert.Equalf(t, tt.want, deviceMatches(tt.benchDevice, tt.deviceClass),
				"deviceMatches(%q, %q)", tt.benchDevice, tt.deviceClass)
		})
	}
}

// TestIsGPUBackend pins the gpuBackendTokens membership that gates the fp16 GPU
// size lever. The lever is otherwise exercised only through openvino-gpu, so
// cuda and tensorrt membership is untested (no current build emits those
// tokens); this direct table catches a future edit that drops one of the three
// GPU tokens from the set, or adds a CPU token to it.
func TestIsGPUBackend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		backend string
		want    bool
	}{
		{backendCUDA, true},
		{backendTensorRT, true},
		{hwprofile.CapOpenVINOGPU, true},
		{hwprofile.CapONNXRuntimeCPU, false},
		{hwprofile.CapOpenVINOCPU, false},
		{hwprofile.CapTFLite, false},
	}
	for _, tt := range tests {
		t.Run(tt.backend, func(t *testing.T) {
			t.Parallel()
			assert.Equalf(t, tt.want, isGPUBackend(tt.backend), "isGPUBackend(%q)", tt.backend)
		})
	}
}

func TestRank_X86BenchmarkPrefixMatch(t *testing.T) {
	t.Parallel()

	// Two variants on the same recommended CPU backend so their scores tie, with
	// x86-i7-1260P benchmark rows the amd64 host must now consume via the x86
	// device-class prefix match. Before the fix these rows were ignored on x86 and
	// the tie fell through to size/id; now the faster measured one must win.
	backend := map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}}
	// IDs are chosen so the lexical id tie-break (ascending) would pick the
	// SLOWER variant: if the x86 rows were not consumed the two scores tie and
	// "a-slow" would win, so asserting "z-fast" wins makes the winner assertion
	// itself catch a regression of the prefix match, not just the reason assertion.
	entry := classifier.CatalogEntry{
		ID: "x86-bench",
		Variants: []classifier.CatalogVariant{
			{ID: "z-fast", Backends: backend, Benchmarks: []classifier.Benchmark{{Device: "x86-i7-1260P", Backend: hwprofile.CapONNXRuntimeCPU, LatencyMs: 50}}},
			{ID: "a-slow", Backends: backend, Benchmarks: []classifier.Benchmark{{Device: "x86-i7-1260P", Backend: hwprofile.CapONNXRuntimeCPU, LatencyMs: 150}}},
		},
	}
	recs := Rank(&Input{
		Capabilities: []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU},
		DeviceClass:  deviceClassX86,
		Entries:      []classifier.CatalogEntry{entry},
	})
	assert.Equal(t, "z-fast", recommendedVariant(recs, "x86-bench"), "faster x86 benchmark wins after prefix match, beating the lexical id tie-break")
	fastRec, ok := variantRec(recs, "x86-bench", "z-fast")
	require.True(t, ok)
	assert.True(t, hasReason(&fastRec, ReasonBenchmarkMeasured), "x86 benchmark row is consumed on an amd64 host")
}

func TestRank_GPUFP16PreferredOverFasterCPUFP32(t *testing.T) {
	t.Parallel()

	// Uses the real birdnet-v3.0 entry so the assertion tracks the shipped
	// benchmark rows: fp32 measures 70 ms on CPU, fp16 81 ms on the iGPU, so the
	// raw latency term favors fp32 on a GPU host. The size lever must keep fp16.
	v3 := realEntry(t, "birdnet-v3.0")

	tests := []struct {
		name            string
		caps            []string
		resolvedRegion  string
		wantRecommended string
		// wantFP16Lever is the variant expected to carry the fp16 GPU-preferred
		// reason, or "" when no variant should.
		wantFP16Lever string
	}{
		{
			name:            "igpu keeps fp16 over faster cpu fp32",
			caps:            []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapOpenVINOCPU, hwprofile.CapOpenVINOGPU},
			wantRecommended: "fp16",
			wantFP16Lever:   "fp16",
		},
		{
			// No GPU backend reachable: fp16's recommended path is not a GPU, so the
			// lever stays inert and the faster fp32 CPU build is the correct pick.
			name:            "no gpu falls back to fp32",
			caps:            []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapOpenVINOCPU},
			wantRecommended: "fp32",
			wantFP16Lever:   "",
		},
		{
			// The lever applies at the regional level too: with nordic resolved,
			// fp16@nordic must stay ahead of the faster-on-CPU fp32@nordic.
			name:            "igpu keeps fp16 regional slice",
			caps:            []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapOpenVINOCPU, hwprofile.CapOpenVINOGPU},
			resolvedRegion:  "nordic",
			wantRecommended: "fp16@nordic",
			wantFP16Lever:   "fp16@nordic",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			recs := Rank(&Input{
				Capabilities:   tt.caps,
				TotalRAMBytes:  ramHigh,
				DeviceClass:    deviceClassX86,
				ResolvedRegion: tt.resolvedRegion,
				Entries:        []classifier.CatalogEntry{v3},
			})
			assert.Equalf(t, tt.wantRecommended, recommendedVariant(recs, "birdnet-v3.0"), "recommended variant")

			// The fp32 sibling must carry a measured benchmark, proving the x86 rows
			// are actually consumed; otherwise the test could pass vacuously if the
			// prefix match regressed.
			fp32ID := "fp32"
			if tt.resolvedRegion != "" {
				fp32ID = "fp32@" + tt.resolvedRegion
			}
			fp32Rec, ok := variantRec(recs, "birdnet-v3.0", fp32ID)
			require.Truef(t, ok, "variant %s present", fp32ID)
			assert.Truef(t, hasReason(&fp32Rec, ReasonBenchmarkMeasured), "%s consumes the x86 benchmark row", fp32ID)

			if tt.wantFP16Lever != "" {
				leverRec, ok := variantRec(recs, "birdnet-v3.0", tt.wantFP16Lever)
				require.Truef(t, ok, "variant %s present", tt.wantFP16Lever)
				assert.Truef(t, hasReason(&leverRec, ReasonPrecisionFP16GPUPreferred), "%s carries the fp16 GPU-preferred reason", tt.wantFP16Lever)
			} else {
				fp16Rec, ok := variantRec(recs, "birdnet-v3.0", "fp16")
				require.True(t, ok)
				assert.False(t, hasReason(&fp16Rec, ReasonPrecisionFP16GPUPreferred), "lever must not fire without a recommended GPU path")
			}
		})
	}
}

func TestRank_FP16GPULeverIsX86Only(t *testing.T) {
	t.Parallel()

	// A hypothetical non-x86 host that still reports a recommended GPU backend
	// (CapOpenVINOGPU is not architecture-gated in hwprofile, so this is possible
	// in principle). The fp16 GPU size lever must NOT fire there: the x86
	// CPU-vs-iGPU benchmark flip it counteracts cannot arise off x86, and its
	// calibration is unvalidated on other architectures.
	backend := map[string]classifier.BackendSupport{
		hwprofile.CapOpenVINOGPU: {Supported: true, Recommended: true},
	}
	entry := classifier.CatalogEntry{
		ID: "fp16-gpu",
		Variants: []classifier.CatalogVariant{
			{ID: "fp16", Precision: "fp16", Backends: backend},
			{ID: "fp32", Precision: "fp32", Backends: backend},
		},
	}
	arm := Rank(&Input{
		Capabilities: []string{hwprofile.CapAArch64, hwprofile.CapOpenVINOGPU},
		DeviceClass:  deviceClassRPi5,
		Entries:      []classifier.CatalogEntry{entry},
	})
	armFP16, ok := variantRec(arm, "fp16-gpu", "fp16")
	require.True(t, ok)
	assert.False(t, hasReason(&armFP16, ReasonPrecisionFP16GPUPreferred),
		"the fp16 GPU lever must not fire on a non-x86 host")

	// Sanity: the same entry on an x86 host with a recommended GPU DOES get the
	// lever, so this is not vacuously passing because the lever is dead everywhere.
	x86 := Rank(&Input{
		Capabilities: []string{hwprofile.CapX86_64, hwprofile.CapOpenVINOGPU},
		DeviceClass:  deviceClassX86,
		Entries:      []classifier.CatalogEntry{entry},
	})
	x86FP16, ok := variantRec(x86, "fp16-gpu", "fp16")
	require.True(t, ok)
	assert.True(t, hasReason(&x86FP16, ReasonPrecisionFP16GPUPreferred),
		"the fp16 GPU lever fires on an x86 host with a recommended GPU backend")
}

func TestRank_Blockers(t *testing.T) {
	t.Parallel()

	entry := classifier.CatalogEntry{
		ID: "synthetic",
		Variants: []classifier.CatalogVariant{
			{
				ID:           "arch-only",
				Requirements: classifier.VariantRequirements{Arch: []string{hwprofile.CapAArch64}},
				Backends:     map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}},
			},
			{
				ID:           "backend-required",
				Requirements: classifier.VariantRequirements{Backends: []string{hwprofile.CapOpenVINOGPU}},
				Backends:     map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}},
			},
			{
				ID:           "ram-hungry",
				Requirements: classifier.VariantRequirements{MinRAMMB: 4096},
				Backends:     map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}},
			},
			{
				ID:           "excluded",
				Requirements: classifier.VariantRequirements{Excludes: []string{"openvino-gpu-intel-gen12"}},
				Backends:     map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}},
			},
			{
				ID:       "no-available-backend",
				Backends: map[string]classifier.BackendSupport{hwprofile.CapOpenVINOGPU: {Supported: true, Recommended: true}},
			},
		},
	}

	recs := Rank(&Input{
		Capabilities:  []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, "openvino-gpu-intel-gen12"},
		TotalRAMBytes: ramLow, // 1.5 GiB, below the 4 GiB floor
		DeviceClass:   deviceClassX86,
		Entries:       []classifier.CatalogEntry{entry},
	})

	wantCode := map[string]string{
		"arch-only":            BlockerArchUnsupported,
		"backend-required":     BlockerBackendMissing,
		"ram-hungry":           BlockerRAMInsufficient,
		"excluded":             BlockerHardwareExcluded,
		"no-available-backend": BlockerBackendMissing,
	}
	for variantID, code := range wantCode {
		rec, ok := variantRec(recs, "synthetic", variantID)
		require.Truef(t, ok, "variant %s present", variantID)
		assert.Falsef(t, rec.Compatible, "variant %s incompatible", variantID)
		assert.Truef(t, hasBlocker(&rec, code), "variant %s carries blocker %s (got %+v)", variantID, code, rec.Blockers)
	}

	// The RAM blocker carries the required floor as an arg.
	ramRec, _ := variantRec(recs, "synthetic", "ram-hungry")
	assert.Equal(t, "4096", blockerArg(&ramRec, BlockerRAMInsufficient, "requiredMb"))
}

func TestRank_BenchmarkScaling(t *testing.T) {
	t.Parallel()

	// Three variants on the same recommended backend so score ties, letting the
	// benchmark term decide. Latencies 100 / 300 / 500 on rpi5-a76.
	backend := map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}}
	entry := classifier.CatalogEntry{
		ID: "bench",
		Variants: []classifier.CatalogVariant{
			{ID: "fast", Backends: backend, Benchmarks: []classifier.Benchmark{{Device: deviceClassRPi5, Backend: hwprofile.CapONNXRuntimeCPU, LatencyMs: 100}}},
			{ID: "mid", Backends: backend, Benchmarks: []classifier.Benchmark{{Device: deviceClassRPi5, Backend: hwprofile.CapONNXRuntimeCPU, LatencyMs: 300}}},
			{ID: "slow", Backends: backend, Benchmarks: []classifier.Benchmark{{Device: deviceClassRPi5, Backend: hwprofile.CapONNXRuntimeCPU, LatencyMs: 500}}},
		},
	}
	recs := Rank(&Input{
		Capabilities: []string{hwprofile.CapAArch64, hwprofile.CapONNXRuntimeCPU},
		DeviceClass:  deviceClassRPi5,
		Entries:      []classifier.CatalogEntry{entry},
	})
	assert.Equal(t, "fast", recommendedVariant(recs, "bench"), "fastest measured variant wins")

	// Fewer than two comparable benchmarks: term is zero for all, so the tie
	// falls through to size then id.
	single := classifier.CatalogEntry{
		ID: "bench-single",
		Variants: []classifier.CatalogVariant{
			{ID: "a", Backends: backend, Benchmarks: []classifier.Benchmark{{Device: deviceClassRPi5, Backend: hwprofile.CapONNXRuntimeCPU, LatencyMs: 999}}},
			{ID: "b", Backends: backend},
		},
	}
	recsSingle := Rank(&Input{
		Capabilities: []string{hwprofile.CapAArch64, hwprofile.CapONNXRuntimeCPU},
		DeviceClass:  deviceClassRPi5,
		Entries:      []classifier.CatalogEntry{single},
	})
	// Equal size (no files), equal score, tie broken lexically -> "a".
	assert.Equal(t, "a", recommendedVariant(recsSingle, "bench-single"))
	recA, _ := variantRec(recsSingle, "bench-single", "a")
	assert.False(t, hasReason(&recA, ReasonBenchmarkMeasured), "single benchmark contributes no measured reason")
}

func TestRank_TieBreaks(t *testing.T) {
	t.Parallel()

	// Equal score and equal backend rank: smaller download wins.
	backend := map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}}
	entry := classifier.CatalogEntry{
		ID: "tie",
		Variants: []classifier.CatalogVariant{
			{ID: "big", Backends: backend, Files: []classifier.CatalogFile{{SizeBytes: 900}}},
			{ID: "small", Backends: backend, Files: []classifier.CatalogFile{{SizeBytes: 100}}},
		},
	}
	recs := Rank(&Input{
		Capabilities: []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU},
		Entries:      []classifier.CatalogEntry{entry},
	})
	assert.Equal(t, "small", recommendedVariant(recs, "tie"))
}

func TestRank_BestBackendFromRecommendedSet(t *testing.T) {
	t.Parallel()

	// A variant Supported (not Recommended) on the high-preference openvino-gpu,
	// but Recommended on the lower openvino-cpu, must be scored via openvino-cpu.
	gpuSupportedCPURecommended := classifier.CatalogVariant{
		ID: "cpu-rec",
		Backends: map[string]classifier.BackendSupport{
			hwprofile.CapOpenVINOGPU: {Supported: true},
			hwprofile.CapOpenVINOCPU: {Supported: true, Recommended: true},
		},
	}
	// A sibling Recommended on openvino-gpu should outrank it on backend rank.
	gpuRecommended := classifier.CatalogVariant{
		ID: "gpu-rec",
		Backends: map[string]classifier.BackendSupport{
			hwprofile.CapOpenVINOGPU: {Supported: true, Recommended: true},
			hwprofile.CapOpenVINOCPU: {Supported: true},
		},
	}
	entry := classifier.CatalogEntry{ID: "backend-choice", Variants: []classifier.CatalogVariant{gpuSupportedCPURecommended, gpuRecommended}}
	recs := Rank(&Input{
		Capabilities: []string{hwprofile.CapX86_64, hwprofile.CapOpenVINOCPU, hwprofile.CapOpenVINOGPU},
		Entries:      []classifier.CatalogEntry{entry},
	})
	assert.Equal(t, "gpu-rec", recommendedVariant(recs, "backend-choice"))

	cpuRec, _ := variantRec(recs, "backend-choice", "cpu-rec")
	assert.Equal(t, hwprofile.CapOpenVINOCPU, reasonArg(&cpuRec, ReasonBackendRecommended, "backend"), "scored via its recommended backend, not the higher-preference supported one")
}

func TestRank_LegacyDemoted(t *testing.T) {
	t.Parallel()

	backend := map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}}
	entry := classifier.CatalogEntry{
		ID: "legacy-test",
		Variants: []classifier.CatalogVariant{
			{ID: "current", Backends: backend},
			{ID: "old", Legacy: true, Backends: backend},
		},
	}
	recs := Rank(&Input{
		Capabilities: []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU},
		Entries:      []classifier.CatalogEntry{entry},
	})
	assert.Equal(t, "current", recommendedVariant(recs, "legacy-test"))
	oldRec, _ := variantRec(recs, "legacy-test", "old")
	assert.True(t, hasReason(&oldRec, ReasonVariantLegacy))
	assert.Negative(t, oldRec.Score, "legacy penalty drives the score negative")
}

func TestRank_EmptyVariantsProducesNothing(t *testing.T) {
	t.Parallel()

	flat := classifier.CatalogEntry{ID: "flat", Files: []classifier.CatalogFile{{SizeBytes: 10}}}
	recs := Rank(&Input{
		Capabilities: []string{hwprofile.CapX86_64},
		Entries:      []classifier.CatalogEntry{flat},
	})
	assert.Empty(t, recs)
}

func TestRank_EmptyBackendsMapNotBlocked(t *testing.T) {
	t.Parallel()

	// A user-edited entry with no backend metadata must not be filtered out.
	entry := classifier.CatalogEntry{
		ID:       "user-catalog",
		Variants: []classifier.CatalogVariant{{ID: "plain"}},
	}
	recs := Rank(&Input{
		Capabilities: []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU},
		Entries:      []classifier.CatalogEntry{entry},
	})
	rec, ok := variantRec(recs, "user-catalog", "plain")
	require.True(t, ok)
	assert.True(t, rec.Compatible, "no backend info means no blocker")
	assert.False(t, hasReason(&rec, ReasonBackendRecommended), "no backend info means no backend term")
	assert.False(t, hasReason(&rec, ReasonBackendSupported), "no backend info means no backend term")
	// A global variant with no region resolved still earns the global fallback,
	// which is the only reason present here.
	assert.True(t, hasReason(&rec, ReasonRegionGlobalFallback), "global variant, no region resolved")
	assert.True(t, rec.Recommended, "the only compatible variant is recommended")
}

// regionEntry builds a synthetic entry with one global variant plus one variant
// per given region slug, for the region-axis tests. Variants carry no hardware
// requirements, so they are all compatible and the region term is isolated. The
// global variant's download is the largest and the regional ones are 1 byte, so
// a bare hardware tie-break (which prefers the smaller download) would favor a
// regional variant; the region term must override that.
func regionEntry(globalSize int64, regions ...string) classifier.CatalogEntry {
	e := classifier.CatalogEntry{
		ID: "regional-model",
		Variants: []classifier.CatalogVariant{
			{ID: "global", Files: []classifier.CatalogFile{{SizeBytes: globalSize}}},
		},
	}
	for _, r := range regions {
		e.Variants = append(e.Variants, classifier.CatalogVariant{
			ID:     "slice-" + r,
			Region: r,
			Files:  []classifier.CatalogFile{{SizeBytes: 1}},
		})
	}
	return e
}

func TestRank_RegionMatched(t *testing.T) {
	t.Parallel()

	entry := regionEntry(100, "iberia", "amazonia")
	recs := Rank(&Input{
		Capabilities:   []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU},
		ResolvedRegion: "iberia",
		Entries:        []classifier.CatalogEntry{entry},
	})

	assert.Equal(t, "slice-iberia", recommendedVariant(recs, "regional-model"), "the matching regional slice wins")

	iberia, ok := variantRec(recs, "regional-model", "slice-iberia")
	require.True(t, ok)
	assert.True(t, hasReason(&iberia, ReasonRegionMatched))
	assert.Equal(t, "iberia", reasonArg(&iberia, ReasonRegionMatched, "region"))

	// A matching regional slice suppresses the global fallback: the global variant
	// earns no region reason when the entry ships the resolved region.
	global, ok := variantRec(recs, "regional-model", "global")
	require.True(t, ok)
	assert.False(t, hasReason(&global, ReasonRegionGlobalFallback), "global fallback suppressed when a slice matches")
	assert.False(t, hasReason(&global, ReasonRegionMatched))

	// A non-matching slice earns nothing on the region axis.
	amazonia, ok := variantRec(recs, "regional-model", "slice-amazonia")
	require.True(t, ok)
	assert.False(t, hasReason(&amazonia, ReasonRegionMatched))
	assert.False(t, hasReason(&amazonia, ReasonRegionGlobalFallback))
}

func TestRank_RegionGlobalFallback(t *testing.T) {
	t.Parallel()

	entry := regionEntry(100, "iberia")
	recs := Rank(&Input{
		Capabilities:   []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU},
		ResolvedRegion: "", // no region resolved (global mode, or no coordinates)
		Entries:        []classifier.CatalogEntry{entry},
	})

	assert.Equal(t, "global", recommendedVariant(recs, "regional-model"), "global wins the +50 fallback when no region resolves")

	global, ok := variantRec(recs, "regional-model", "global")
	require.True(t, ok)
	assert.True(t, hasReason(&global, ReasonRegionGlobalFallback))

	slice, ok := variantRec(recs, "regional-model", "slice-iberia")
	require.True(t, ok)
	assert.False(t, hasReason(&slice, ReasonRegionMatched), "no region resolved means no regional match")
}

func TestRank_RegionResolvedButNoMatchingSlice(t *testing.T) {
	t.Parallel()

	// The resolved region (amazonia) has no slice in this entry. The global model
	// must still beat the wrong-region iberia slice, even though iberia is the
	// smaller download and would win a bare hardware tie-break. This is the
	// mis-recommendation hazard the global fallback is designed to close.
	entry := regionEntry(100, "iberia")
	recs := Rank(&Input{
		Capabilities:   []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU},
		ResolvedRegion: "amazonia",
		Entries:        []classifier.CatalogEntry{entry},
	})

	assert.Equal(t, "global", recommendedVariant(recs, "regional-model"), "global fallback beats a wrong-region slice")

	global, ok := variantRec(recs, "regional-model", "global")
	require.True(t, ok)
	assert.True(t, hasReason(&global, ReasonRegionGlobalFallback))

	slice, ok := variantRec(recs, "regional-model", "slice-iberia")
	require.True(t, ok)
	assert.False(t, hasReason(&slice, ReasonRegionMatched))
	assert.False(t, hasReason(&slice, ReasonRegionGlobalFallback))
}

func TestRank_PureInputNotMutated(t *testing.T) {
	t.Parallel()

	perch := realEntry(t, "perch-v2")
	in := Input{
		Capabilities:  []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU},
		TotalRAMBytes: ramHigh,
		DeviceClass:   deviceClassX86,
		Entries:       []classifier.CatalogEntry{perch},
	}
	before := len(in.Entries[0].Variants)
	beforeCaps := slices.Clone(in.Capabilities)

	_ = Rank(&in)

	assert.Len(t, in.Entries[0].Variants, before, "entry variants unchanged")
	assert.Equal(t, beforeCaps, in.Capabilities, "capabilities unchanged")
}

func TestRank_Deterministic(t *testing.T) {
	t.Parallel()

	perch := realEntry(t, "perch-v2")
	v3 := realEntry(t, "birdnet-v3.0")
	in := Input{
		Capabilities:  []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapOpenVINOCPU, hwprofile.CapOpenVINOGPU},
		TotalRAMBytes: ramHigh,
		DeviceClass:   deviceClassX86,
		Entries:       []classifier.CatalogEntry{perch, v3},
	}
	first := Rank(&in)
	for range 20 {
		assert.Equal(t, first, Rank(&in), "Rank is deterministic across repeated calls")
	}
}

func TestDeviceClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		boardTier string
		goArch    string
		want      string
	}{
		{"pi5", "arm64", deviceClassRPi5},
		{"pi4", "arm64", deviceClassRPi4},
		{"pi3", "arm64", deviceClassRPi3},
		{"", "amd64", deviceClassX86},
		{"", "arm64", ""},
		{"", "arm", ""},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, DeviceClass(tt.boardTier, tt.goArch), "DeviceClass(%q, %q)", tt.boardTier, tt.goArch)
	}
}

func TestRank_FP16NativeBonus(t *testing.T) {
	t.Parallel()

	// birdnet-v3.0 has an fp16 variant; on an fp16-native ARM host it earns the
	// precision bonus. This is the case the matrix test could not reach (perch-v2
	// has no fp16 variant).
	v3 := realEntry(t, "birdnet-v3.0")
	recs := Rank(&Input{
		Capabilities: []string{hwprofile.CapAArch64, hwprofile.CapAArch64A76, hwprofile.CapONNXRuntimeCPU, hwprofile.CapFP16Native},
		DeviceClass:  deviceClassRPi5,
		Entries:      []classifier.CatalogEntry{v3},
	})
	fp16, ok := variantRec(recs, "birdnet-v3.0", "fp16")
	require.True(t, ok)
	assert.True(t, fp16.Compatible)
	assert.True(t, hasReason(&fp16, ReasonPrecisionFP16Native), "fp16 variant on an fp16-native host gets the precision bonus (reasons: %+v)", fp16.Reasons)
}

func TestRank_LowRAMInt8Bonus(t *testing.T) {
	t.Parallel()

	backend := map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}}
	// int8 is the LARGER file, so absent the low-RAM bonus fp32 wins on the size
	// tie-break. The bonus is what must flip the pick to int8, isolating it from
	// the size effect that confounds the real-catalog case.
	entry := classifier.CatalogEntry{
		ID: "ram-test",
		Variants: []classifier.CatalogVariant{
			{ID: "fp32", Precision: "fp32", Backends: backend, Files: []classifier.CatalogFile{{SizeBytes: 100}}},
			{ID: "int8", Precision: precisionINT8, Backends: backend, Files: []classifier.CatalogFile{{SizeBytes: 200}}},
		},
	}

	lowRAM := Rank(&Input{
		Capabilities:  []string{hwprofile.CapAArch64, hwprofile.CapONNXRuntimeCPU, hwprofile.CapLowRAM},
		TotalRAMBytes: ramLow,
		Entries:       []classifier.CatalogEntry{entry},
	})
	assert.Equal(t, "int8", recommendedVariant(lowRAM, "ram-test"), "low-RAM host prefers the int8 build despite its larger size")
	int8Rec, _ := variantRec(lowRAM, "ram-test", "int8")
	assert.True(t, hasReason(&int8Rec, ReasonRAMConstrainedFit))

	fullRAM := Rank(&Input{
		Capabilities:  []string{hwprofile.CapAArch64, hwprofile.CapONNXRuntimeCPU},
		TotalRAMBytes: ramHigh,
		Entries:       []classifier.CatalogEntry{entry},
	})
	assert.Equal(t, "fp32", recommendedVariant(fullRAM, "ram-test"), "without the low-RAM bonus the smaller fp32 wins")
}

func TestRank_LegacyNotRecommended(t *testing.T) {
	t.Parallel()

	backend := map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}}
	// The only compatible variant is Legacy; because the gallery hides legacy
	// variants, the recommender must not point at it, so the entry gets no
	// recommendation rather than one the client cannot see.
	entry := classifier.CatalogEntry{
		ID: "legacy-only",
		Variants: []classifier.CatalogVariant{
			{ID: "old", Legacy: true, Backends: backend},
			{ID: "new", Requirements: classifier.VariantRequirements{Arch: []string{hwprofile.CapAArch64}}, Backends: backend},
		},
	}
	recs := Rank(&Input{
		Capabilities: []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU}, // amd64: "new" (aarch64-only) is blocked
		Entries:      []classifier.CatalogEntry{entry},
	})
	assert.Empty(t, recommendedVariant(recs, "legacy-only"), "a Legacy-only entry gets no recommendation")
	oldRec, ok := variantRec(recs, "legacy-only", "old")
	require.True(t, ok)
	assert.True(t, oldRec.Compatible, "the legacy variant is still compatible, just not recommended")
	assert.False(t, oldRec.Recommended)
}

func TestRank_BackendMissingNotDuplicated(t *testing.T) {
	t.Parallel()

	// A variant that both requires a backend the host lacks (Requirements.Backends)
	// AND whose Backends map has no host-available candidate trips both the hard
	// filter and the backend-term missing path. It must still yield exactly one
	// backend.missing blocker, not two.
	entry := classifier.CatalogEntry{
		ID: "dup",
		Variants: []classifier.CatalogVariant{
			{
				ID:           "needs-gpu",
				Requirements: classifier.VariantRequirements{Backends: []string{hwprofile.CapOpenVINOGPU}},
				Backends:     map[string]classifier.BackendSupport{hwprofile.CapOpenVINOGPU: {Supported: true, Recommended: true}},
			},
		},
	}
	recs := Rank(&Input{
		Capabilities: []string{hwprofile.CapX86_64, hwprofile.CapONNXRuntimeCPU}, // no openvino-gpu
		Entries:      []classifier.CatalogEntry{entry},
	})
	rec, ok := variantRec(recs, "dup", "needs-gpu")
	require.True(t, ok)
	assert.False(t, rec.Compatible)

	count := 0
	for _, b := range rec.Blockers {
		if b.Code == BlockerBackendMissing {
			count++
		}
	}
	assert.Equal(t, 1, count, "backend.missing must appear once, not duplicated (blockers: %+v)", rec.Blockers)
}

// --- test helpers ---

func hasBlocker(rec *Recommendation, code string) bool {
	for _, b := range rec.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}

func hasReason(rec *Recommendation, code string) bool {
	for _, r := range rec.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

func reasonArg(rec *Recommendation, code, arg string) string {
	for _, r := range rec.Reasons {
		if r.Code == code {
			return r.Args[arg]
		}
	}
	return ""
}

func blockerArg(rec *Recommendation, code, arg string) string {
	for _, b := range rec.Blockers {
		if b.Code == code {
			return b.Args[arg]
		}
	}
	return ""
}
