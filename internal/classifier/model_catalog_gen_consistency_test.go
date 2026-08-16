package classifier

import (
	"encoding/json"
	"os"
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

	cases := []struct {
		entryID      string
		manifestFile string
	}{
		{"birdnet-v3.0", "gen/manifests/BirdNET-v3.0-Models.models.json"},
		{"perch-v2", "gen/manifests/Perch-v2-Models.models.json"},
	}

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
		Region       string `json:"region"`
		SHA256       string `json:"sha256"`
		SizeBytes    int64  `json:"size_bytes"`
		Requirements *struct {
			MinRAMMB int `json:"min_ram_mb"`
		} `json:"requirements"`
	} `json:"models"`
}

func loadTestManifest(t *testing.T, pathname string) *testManifest {
	t.Helper()
	data, err := os.ReadFile(pathname)
	require.NoErrorf(t, err, "read vendored manifest %q", pathname)
	var m testManifest
	require.NoErrorf(t, json.Unmarshal(data, &m), "parse vendored manifest %q", pathname)
	return &m
}
