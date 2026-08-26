package classifier

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier/region"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
)

// regionalTilesPerFamily is how many region-sliced variants the generator emits
// per family: 40 region slugs, each with two precision builds.
const regionalTilesPerFamily = 80

// allowedRegionalPrecisions is the set of normalized precisions a regional
// variant may carry. "int8-arm" is normalized to "int8" at generation time so
// the recommender's precision comparisons match.
var allowedRegionalPrecisions = map[string]bool{"fp32": true, "fp16": true, "int8": true}

// TestEmbeddedCatalog_RegionalVariants guards the generated regional catalog data
// against the failure modes that would silently break region-aware recommendation:
// a Region set to a display name instead of a slug (region.matched never fires), a
// tile marked Default or Legacy (wrong preselection / hidden), a missing checksum,
// or a species count that has drifted from the region table.
func TestEmbeddedCatalog_RegionalVariants(t *testing.T) {
	t.Parallel()

	tables, err := region.Tables()
	require.NoError(t, err, "load embedded region tables")

	for _, entryID := range []string{"birdnet-v3.0", "perch-v2"} {
		t.Run(entryID, func(t *testing.T) {
			t.Parallel()

			entry, ok := GetCatalogEntry(entryID)
			require.Truef(t, ok, "catalog entry %q", entryID)
			assert.Emptyf(t, entry.Region, "%s entry-level Region must stay empty: region slugs live on variants, not the entry", entryID)

			tbl, ok := tables[entry.HuggingFaceRepo]
			require.Truef(t, ok, "region table for repo %q", entry.HuggingFaceRepo)

			displayNames := make(map[string]bool, len(tbl.Regions))
			for _, r := range tbl.Regions {
				displayNames[r.Name] = true
			}

			regional := 0
			seenID := make(map[string]bool, len(entry.Variants))
			seenModelLocal := make(map[string]bool, len(entry.Variants))
			for i := range entry.Variants {
				v := &entry.Variants[i]
				if v.Region == "" {
					continue // global variant, covered by TestEmbeddedCatalog_GlobalVariantResolution
				}
				regional++

				// The join guardrail: Region MUST be a region-table slug, never a
				// display name. If it is a display name the recommender's +100
				// region.matched term never fires and the tile is never recommended.
				reg, inTable := tbl.Regions[v.Region]
				assert.Truef(t, inTable, "variant %q Region %q must be a region-table slug", v.ID, v.Region)
				assert.Falsef(t, displayNames[v.Region], "variant %q Region %q must be a slug, not a display name", v.ID, v.Region)

				assert.Falsef(t, v.Default, "regional variant %q must not be Default", v.ID)
				assert.Falsef(t, v.Legacy, "regional variant %q must not be Legacy", v.ID)
				assert.Truef(t, allowedRegionalPrecisions[v.Precision], "variant %q precision %q unexpected", v.ID, v.Precision)
				assert.Equalf(t, reg.Classes, v.SpeciesCount, "variant %q species count must match the region table", v.ID)
				assert.Positivef(t, v.Requirements.MinRAMMB, "variant %q must carry a RAM floor", v.ID)

				assert.Falsef(t, seenID[v.ID], "duplicate variant id %q", v.ID)
				seenID[v.ID] = true

				var model, labels *CatalogFile
				for j := range v.Files {
					switch v.Files[j].Role {
					case RoleModel:
						model = &v.Files[j]
					case RoleLabels:
						labels = &v.Files[j]
					}
				}
				require.NotNilf(t, model, "variant %q must carry a model file", v.ID)
				require.NotNilf(t, labels, "variant %q must carry a labels file", v.ID)

				assert.Falsef(t, seenModelLocal[model.LocalName], "duplicate model LocalName %q within %s", model.LocalName, entryID)
				seenModelLocal[model.LocalName] = true
				assert.NotEmptyf(t, model.SHA256, "variant %q model checksum", v.ID)
				assert.Positivef(t, model.SizeBytes, "variant %q model size", v.ID)
				assert.NotEmptyf(t, labels.SHA256, "variant %q labels checksum", v.ID)
				assert.Positivef(t, labels.SizeBytes, "variant %q labels size", v.ID)

				// Hardware-shaped invariants mirrored verbatim from the manifest.
				if v.Precision == "fp16" {
					assert.Containsf(t, v.Requirements.Excludes, "openvino-gpu-intel-gen12", "fp16 variant %q must exclude the Iris Xe gen12 miscompile", v.ID)
				}
				if strings.HasPrefix(v.ID, "int8-arm@") {
					assert.Equalf(t, []string{hwprofile.CapAArch64}, v.Requirements.Arch, "int8-arm variant %q must require aarch64", v.ID)
				}
			}
			assert.Equalf(t, regionalTilesPerFamily, regional, "%s must expose %d regional tiles", entryID, regionalTilesPerFamily)
		})
	}
}
