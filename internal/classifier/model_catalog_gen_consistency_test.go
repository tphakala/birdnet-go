package classifier

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegionalVariants_MatchManifest cross-checks every generated regional
// variant against the vendored acoustic-models manifest it was generated from.
// The CI drift gate (model-catalog-drift.yml) catches a stale generated file;
// this fast unit test additionally catches a hand-edit of the generated file that
// diverges from the manifest data (checksum, size, region, or model path), which
// a byte-level regeneration on a developer's machine might otherwise mask.
func TestRegionalVariants_MatchManifest(t *testing.T) {
	t.Parallel()

	// Derive the regional families from the catalog itself (an entry is regional
	// when any of its variants carries a Region), so a future third regional
	// family is covered without editing this test. The manifest filename follows
	// the vendoring convention: the HuggingFace repo's base name plus
	// ".models.json".
	type famCase struct {
		entryID      string
		manifestFile string
	}
	var cases []famCase
	for i := range EmbeddedCatalog {
		e := &EmbeddedCatalog[i]
		regional := false
		for j := range e.Variants {
			if e.Variants[j].Region != "" {
				regional = true
				break
			}
		}
		if !regional {
			continue
		}
		cases = append(cases, famCase{
			entryID:      e.ID,
			manifestFile: filepath.Join("gen", "manifests", path.Base(e.HuggingFaceRepo)+".models.json"),
		})
	}
	require.GreaterOrEqualf(t, len(cases), 3, "expected at least the birdnet-v3.0, perch-v2 and birdnet-plus-v3.0 regional families")

	for _, tc := range cases {
		t.Run(tc.entryID, func(t *testing.T) {
			t.Parallel()

			entry, ok := GetCatalogEntry(tc.entryID)
			require.Truef(t, ok, "catalog entry %q", tc.entryID)

			// Index generated regional variants by their model-file RemotePath.
			byModelPath := make(map[string]*CatalogVariant)
			for i := range entry.Variants {
				v := &entry.Variants[i]
				if v.Region == "" {
					continue
				}
				for j := range v.Files {
					if v.Files[j].Role == RoleModel {
						byModelPath[v.Files[j].RemotePath] = v
					}
				}
			}

			manifest := loadTestManifest(t, tc.manifestFile)
			regional := 0
			for i := range manifest.Models {
				e := &manifest.Models[i]
				if e.Region == "" {
					continue
				}
				regional++

				v, found := byModelPath[e.Path]
				require.Truef(t, found, "no generated variant carries model path %q", e.Path)
				assert.Equalf(t, e.Region, v.Region, "variant for %q must carry the manifest region slug", e.Path)

				var model *CatalogFile
				for j := range v.Files {
					if v.Files[j].Role == RoleModel {
						model = &v.Files[j]
					}
				}
				require.NotNil(t, model)
				assert.Equalf(t, e.SHA256, model.SHA256, "model checksum for %q", e.Path)
				assert.Equalf(t, e.SizeBytes, model.SizeBytes, "model size for %q", e.Path)
				require.NotNilf(t, e.Requirements, "manifest entry %q must carry requirements", e.Path)
				assert.Equalf(t, e.Requirements.MinRAMMB, v.Requirements.MinRAMMB, "RAM floor for %q", e.Path)

				// The generated variant ID is the manifest variant token joined to
				// the region slug (generator: e.Variant + "@" + e.Region).
				assert.Equalf(t, e.Variant+"@"+e.Region, v.ID, "variant ID for %q", e.Path)

				// Precision, normalized the same way the generator does.
				assert.Equalf(t, normalizeManifestPrecision(e.Precision), v.Precision, "precision for %q", e.Path)

				// SpeciesCount: v3.0 supplies classes in the manifest; Perch omits it
				// and the region table fills it in, so there we only assert it is set.
				if e.Classes != nil {
					assert.Equalf(t, *e.Classes, v.SpeciesCount, "species count for %q", e.Path)
				} else {
					assert.Positivef(t, v.SpeciesCount, "species count for %q must be populated", e.Path)
				}

				// Arch is derived by the generator: the int8-arm build is aarch64-only.
				var wantArch []string
				if e.Variant == "int8-arm" {
					wantArch = []string{"aarch64"}
				}
				assert.ElementsMatchf(t, wantArch, v.Requirements.Arch, "arch for %q", e.Path)

				// Excludes pass through verbatim from the manifest requirements.
				assert.ElementsMatchf(t, e.Requirements.Excludes, v.Requirements.Excludes, "excludes for %q", e.Path)

				// Score calibration: every Perch regional entry carries the manifest's
				// value verbatim; the sigmoid-scored BirdNET v3.0 entries carry none.
				assertGeneratedScoreCalibration(t, entry.RegistryID == RegistryIDPerchV2, e.Path, e.ScoreCalibration, v.ScoreCalibration)
			}
			assert.Equalf(t, regionalTilesFor(tc.entryID), regional, "manifest %s must describe %d regional tiles", tc.manifestFile, regionalTilesFor(tc.entryID))
			assert.Lenf(t, byModelPath, regionalTilesFor(tc.entryID), "%s must expose exactly %d regional model paths", tc.entryID, regionalTilesFor(tc.entryID))
		})
	}
}

// TestGlobalVariants_MatchManifest cross-checks the hand-written global variants
// of every family with a vendored manifest against that manifest: each manifest
// model without a region must match a catalog variant by model path on checksum,
// size, RAM floor, backend support, benchmarks and labels file, and the family metadata (license, commercial
// use, species count and, when the manifest states one, channel) must equal the
// entry's. It catches a mistyped hash or size in a hand-written entry, which the
// generator cannot see.
func TestGlobalVariants_MatchManifest(t *testing.T) {
	t.Parallel()

	checked := 0
	for i := range EmbeddedCatalog {
		entry := &EmbeddedCatalog[i]
		manifestFile := filepath.Join("gen", "manifests", path.Base(entry.HuggingFaceRepo)+".models.json")
		if _, err := os.Stat(manifestFile); err != nil {
			continue // no vendored manifest for this entry
		}
		checked++
		t.Run(entry.ID, func(t *testing.T) {
			t.Parallel()

			manifest := loadTestManifest(t, manifestFile)
			assert.Equal(t, entry.License, manifest.FamilyMeta.License, "license")
			assert.Equal(t, entry.CommercialUse, manifest.FamilyMeta.CommercialUse, "commercial use")
			assert.Equal(t, entry.SpeciesCount, manifest.FamilyMeta.SpeciesCount, "species count")
			if manifest.FamilyMeta.Channel != nil {
				wantChannel := entry.Channel
				if wantChannel == "" {
					wantChannel = ChannelStable
				}
				assert.Equal(t, wantChannel, *manifest.FamilyMeta.Channel, "channel")
			}

			byModelPath := make(map[string]*CatalogVariant)
			for j := range entry.Variants {
				v := &entry.Variants[j]
				for k := range v.Files {
					if v.Files[k].Role == RoleModel {
						byModelPath[v.Files[k].RemotePath] = v
					}
				}
			}

			global := 0
			for j := range manifest.Models {
				e := &manifest.Models[j]
				if e.Region != "" {
					continue
				}
				global++
				v, found := byModelPath[e.Path]
				if !assert.Truef(t, found, "no catalog variant carries global model path %q", e.Path) {
					continue
				}
				assertGlobalVariantMatchesManifest(t, v, e)
			}
			assert.Positivef(t, global, "manifest %s must describe at least one global model", manifestFile)
		})
	}
	require.GreaterOrEqual(t, checked, 3, "expected at least the birdnet-v3.0, perch-v2 and birdnet-plus-v3.0 manifests")
}

// assertGlobalVariantMatchesManifest compares one hand-written global variant
// with its manifest model: RAM floor, backend support (both directions: a
// backend the manifest omits must be unsupported), benchmarks, and the model
// and labels files.
func assertGlobalVariantMatchesManifest(t *testing.T, v *CatalogVariant, e *testManifestModel) {
	t.Helper()
	require.NotNilf(t, e.Requirements, "manifest entry %q must carry requirements", e.Path)
	assert.Equalf(t, e.Requirements.MinRAMMB, v.Requirements.MinRAMMB, "RAM floor for %q", e.Path)
	for name, b := range e.Backends {
		assert.Equalf(t, BackendSupport{Supported: b.Supported, Recommended: b.Recommended}, v.Backends[name], "backend %s for %q", name, e.Path)
	}
	for name, got := range v.Backends {
		if _, listed := e.Backends[name]; !listed {
			assert.Equalf(t, BackendSupport{}, got, "backend %s of %q is not in the manifest", name, e.Path)
		}
	}
	wantBench := make([]Benchmark, 0, len(e.Benchmarks))
	for _, b := range e.Benchmarks {
		wantBench = append(wantBench, Benchmark{Device: b.Device, Backend: b.Backend, LatencyMs: b.LatencyMs, RSSMB: b.RSSMB})
	}
	assert.ElementsMatchf(t, wantBench, v.Benchmarks, "benchmarks for %q", e.Path)
	for k := range v.Files {
		f := &v.Files[k]
		switch {
		case f.Role == RoleModel && f.RemotePath == e.Path:
			assert.Equalf(t, e.SHA256, f.SHA256, "model checksum for %q", e.Path)
			assert.Equalf(t, e.SizeBytes, f.SizeBytes, "model size for %q", e.Path)
		case f.Role == RoleLabels:
			assert.Equalf(t, e.Labels, f.RemotePath, "labels path for %q", e.Path)
			assert.Equalf(t, e.LabelsSHA256, f.SHA256, "labels checksum for %q", e.Path)
			assert.Equalf(t, e.LabelsSize, f.SizeBytes, "labels size for %q", e.Path)
		}
	}
}

// assertGeneratedScoreCalibration checks a generated variant's calibration against
// the manifest's block for the same entry: Perch entries must carry a usable copy,
// every other family must carry none.
func assertGeneratedScoreCalibration(t *testing.T, wantPresent bool, modelPath string, manifest, generated *ScoreCalibration) {
	t.Helper()
	if !wantPresent {
		assert.Nilf(t, manifest, "manifest entry %q must not carry score_calibration", modelPath)
		assert.Nilf(t, generated, "generated variant for %q must not carry a score calibration", modelPath)
		return
	}
	require.NotNilf(t, manifest, "manifest entry %q must carry score_calibration", modelPath)
	require.NotNilf(t, generated, "generated variant for %q must carry a score calibration", modelPath)
	assert.Equalf(t, manifest.Method, generated.Method, "calibration method for %q", modelPath)
	assert.InDeltaf(t, manifest.RestLogit, generated.RestLogit, 0, "rest logit for %q", modelPath)
	assert.Truef(t, validScoreCalibration(generated), "calibration for %q must be usable", modelPath)
}

// testManifest mirrors only the manifest fields this test compares.
// testManifestModel mirrors only the manifest model fields this test compares.
type testManifestModel struct {
	Path         string `json:"path"`
	Variant      string `json:"variant"`
	Precision    string `json:"precision"`
	Region       string `json:"region"`
	Classes      *int   `json:"classes"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
	Labels       string `json:"labels"`
	LabelsSHA256 string `json:"labels_sha256"`
	LabelsSize   int64  `json:"labels_size_bytes"`
	Requirements *struct {
		MinRAMMB int      `json:"min_ram_mb"`
		Excludes []string `json:"excludes"`
	} `json:"requirements"`
	Backends map[string]struct {
		Supported   bool `json:"supported"`
		Recommended bool `json:"recommended"`
	} `json:"backends"`
	Benchmarks []struct {
		Device    string `json:"device"`
		Backend   string `json:"backend"`
		LatencyMs int    `json:"latency_ms"`
		RSSMB     int    `json:"rss_mb"`
	} `json:"benchmarks"`
	ScoreCalibration *ScoreCalibration `json:"score_calibration"`
}

type testManifest struct {
	Models     []testManifestModel `json:"models"`
	FamilyMeta struct {
		License       string  `json:"license"`
		CommercialUse bool    `json:"commercial_use"`
		SpeciesCount  int     `json:"species_count"`
		Channel       *string `json:"channel"`
	} `json:"family_meta"`
}

// normalizeManifestPrecision mirrors the generator's normalizePrecision, kept as
// an independent restatement so a change to that mapping is caught here rather
// than silently agreeing with the generator.
func normalizeManifestPrecision(p string) string {
	if p == "int8-arm" {
		return "int8"
	}
	return p
}

func loadTestManifest(t *testing.T, pathname string) *testManifest {
	t.Helper()
	data, err := os.ReadFile(pathname)
	require.NoErrorf(t, err, "read vendored manifest %q", pathname)
	var m testManifest
	require.NoErrorf(t, json.Unmarshal(data, &m), "parse vendored manifest %q", pathname)
	return &m
}
