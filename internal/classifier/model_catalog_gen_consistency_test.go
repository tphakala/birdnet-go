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
	require.GreaterOrEqualf(t, len(cases), 2, "expected at least the birdnet-v3.0 and perch-v2 regional families")

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
			}
			assert.Equalf(t, regionalTilesPerFamily, regional, "manifest %s must describe %d regional tiles", tc.manifestFile, regionalTilesPerFamily)
			assert.Lenf(t, byModelPath, regionalTilesPerFamily, "%s must expose exactly %d regional model paths", tc.entryID, regionalTilesPerFamily)
		})
	}
}

// testManifest mirrors only the manifest fields this test compares.
type testManifest struct {
	Models []struct {
		Path         string `json:"path"`
		Variant      string `json:"variant"`
		Precision    string `json:"precision"`
		Region       string `json:"region"`
		Classes      *int   `json:"classes"`
		SHA256       string `json:"sha256"`
		SizeBytes    int64  `json:"size_bytes"`
		Requirements *struct {
			MinRAMMB int      `json:"min_ram_mb"`
			Excludes []string `json:"excludes"`
		} `json:"requirements"`
	} `json:"models"`
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
