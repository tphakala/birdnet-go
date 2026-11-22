package classifier

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier/region"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
)

// liveRegionalTokens are the variant tokens each region must ship exactly one
// live build of, per catalog entry (family.liveVariants in gen/main.go). The
// token is the variant id up to its release or build suffix.
var liveRegionalTokens = map[string][]string{
	"birdnet-v3.0":      {"fp16", "fp32"},
	"perch-v2":          {"no-dft-fp32"},
	"birdnet-plus-v3.0": {"fp32"},
}

// allowedRegionalPrecisions is the set of normalized precisions a regional
// variant may carry. "int8-arm" is normalized to "int8" at generation time so
// the recommender's precision comparisons match.
var allowedRegionalPrecisions = map[string]bool{"fp32": true, "fp16": true, "int8": true}

// TestEmbeddedCatalog_RegionalVariants guards the generated regional catalog data
// against the failure modes that would silently break region-aware recommendation
// and updates: a Region set to a display name instead of a slug (region.matched
// never fires), a tile marked Default (wrong preselection), a region without its
// live builds (one per live token), a Legacy marking that does not match
// SupersededBy, a successor that is not a live build of the same region, a
// successor of a lower build, a newer build that is not offered as an update, a
// missing checksum, or a species count of a
// live build that has drifted from the region table. Each entry is checked
// against its own family's table, since families may ship different tile sets.
func TestEmbeddedCatalog_RegionalVariants(t *testing.T) {
	t.Parallel()

	tables, err := region.Tables()
	require.NoError(t, err, "load embedded region tables")

	for entryID, tokens := range liveRegionalTokens {
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

			liveIDs := make(map[string]string)              // live variant id -> region
			liveTokensByRegion := make(map[string][]string) // region -> live tokens
			regionSawLegacy := make(map[string]bool)
			var legacy []*CatalogVariant
			seenID := make(map[string]bool, len(entry.Variants))
			seenModelLocal := make(map[string]bool, len(entry.Variants))
			for i := range entry.Variants {
				v := &entry.Variants[i]
				if v.Region == "" {
					continue // global variant, covered by TestEmbeddedCatalog_GlobalVariantResolution
				}

				// The join guardrail: Region MUST be a region-table slug, never a
				// display name. If it is a display name the recommender's +100
				// region.matched term never fires and the tile is never recommended.
				reg, inTable := tbl.Regions[v.Region]
				assert.Truef(t, inTable, "variant %q Region %q must be a region-table slug", v.ID, v.Region)
				assert.Falsef(t, displayNames[v.Region], "variant %q Region %q must be a slug, not a display name", v.ID, v.Region)

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

				assert.Falsef(t, v.Default, "regional variant %q must not be Default", v.ID)
				// A variant is Legacy exactly when it names its successor.
				assert.Equalf(t, v.SupersededBy != "", v.Legacy, "regional variant %q is Legacy exactly when it names a SupersededBy", v.ID)
				if v.Legacy {
					regionSawLegacy[v.Region] = true
					legacy = append(legacy, v)
					// A withdrawn int8 build recommends no backend, so it is never
					// picked for a fresh install.
					if v.Precision == "int8" {
						assert.Falsef(t, backendRecommended(v), "withdrawn variant %q must recommend no backend", v.ID)
					}
				} else {
					liveIDs[v.ID] = v.Region
					liveTokensByRegion[v.Region] = append(liveTokensByRegion[v.Region], variantToken(v.ID))
					// A region lists its live builds before its Legacy ones, as the
					// generator sorts them.
					assert.Falsef(t, regionSawLegacy[v.Region], "live variant %q must precede its region's Legacy variants", v.ID)
					// The region table describes the live build.
					assert.Equalf(t, reg.Classes, v.SpeciesCount, "live variant %q species count must match the region table", v.ID)
				}
				assert.Positivef(t, v.SpeciesCount, "variant %q species count", v.ID)
				assert.Truef(t, allowedRegionalPrecisions[v.Precision], "variant %q precision %q unexpected", v.ID, v.Precision)
				assert.Positivef(t, v.Requirements.MinRAMMB, "variant %q must carry a RAM floor", v.ID)

				assert.Falsef(t, seenID[v.ID], "duplicate variant id %q", v.ID)
				seenID[v.ID] = true

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

			// Every table slug ships exactly one live build of each live token, and
			// the entry ships nothing outside its own table.
			want := slices.Sorted(slices.Values(tokens))
			for slug := range tbl.Regions {
				got := slices.Sorted(slices.Values(liveTokensByRegion[slug]))
				assert.Equalf(t, want, got, "%s region %q must ship exactly one live build of each live token", entryID, slug)
			}
			assert.Lenf(t, liveTokensByRegion, len(tbl.Regions), "%s ships live builds for exactly its own tile set", entryID)

			assertLegacyVariantsUpdate(t, &entry, legacy, liveIDs)
		})
	}
}

// assertLegacyVariantsUpdate checks each Legacy variant is superseded by a live
// variant of its own region, and that it is offered as an update exactly when the
// successor is another release or a higher build; an update shares no file name
// with the installed build.
func assertLegacyVariantsUpdate(t *testing.T, entry *CatalogEntry, legacy []*CatalogVariant, liveIDs map[string]string) {
	t.Helper()
	for _, v := range legacy {
		succ := resolveVariant(entry, v.SupersededBy)
		if assert.NotNilf(t, succ, "Legacy variant %q names a variant of the entry", v.ID) {
			assert.Equalf(t, liveIDs[v.SupersededBy], v.Region, "Legacy variant %q must be superseded by a live variant of its own region", v.ID)
			assert.Falsef(t, succ.Legacy, "successor %q of %q must be live", succ.ID, v.ID)
		}
		got, ok := VariantUpdate(entry, v.ID)
		if succ != nil && variantRelease(entry, succ) == variantRelease(entry, v) {
			// Within one release a successor never has a lower build. An equal build
			// is only a withdrawn variant token pointing at another token of the same
			// build, which is not an update.
			require.GreaterOrEqualf(t, variantBuild(succ), variantBuild(v), "successor %q of %q has a lower build", succ.ID, v.ID)
			if variantBuild(succ) == variantBuild(v) {
				assert.NotEqualf(t, variantToken(v.ID), variantToken(succ.ID), "Legacy variant %q is superseded by its own token at the same build", v.ID)
				assert.Falsef(t, ok, "Legacy variant %q has a successor of the same release and build, which is no update", v.ID)
				continue
			}
		}
		if assert.Truef(t, ok, "VariantUpdate of Legacy variant %q must offer an update", v.ID) {
			assert.Equalf(t, v.SupersededBy, got.ID, "update target of %q", v.ID)
			// Different builds never share a file, so the update downloads next to
			// the installed build instead of overwriting the running model.
			assert.Empty(t, sharedLocalNames(v, got), "%q and its successor %q must not share a file name", v.ID, got.ID)
		}
	}
}

// sharedLocalNames returns the file names two variants have in common.
func sharedLocalNames(a, b *CatalogVariant) []string {
	var shared []string
	for i := range a.Files {
		for j := range b.Files {
			if a.Files[i].LocalName == b.Files[j].LocalName && a.Files[i].Role != RoleTaxonomy {
				shared = append(shared, a.Files[i].LocalName)
			}
		}
	}
	return shared
}

// variantToken is the variant token of a regional variant id: the part before
// "@", without a release ("-3.0.1") or build ("-b2") suffix.
func variantToken(id string) string {
	token, _, _ := strings.Cut(id, "@")
	token = buildIDSuffix.ReplaceAllString(token, "")
	return releaseIDSuffix.ReplaceAllString(token, "")
}

var (
	buildIDSuffix   = regexp.MustCompile(`-b\d+$`)
	releaseIDSuffix = regexp.MustCompile(`-\d+(\.\d+)*$`)
)

// backendRecommended reports whether any backend row of v is Recommended.
func backendRecommended(v *CatalogVariant) bool {
	for _, b := range v.Backends {
		if b.Recommended {
			return true
		}
	}
	return false
}

// TestEmbeddedCatalog_LaterBuildsHaveReleaseNotes checks that every live
// regional variant above build 1 of the families that publish release notes
// has a release-notes record, and that a build 1 variant (whose
// manifest record carries no notes) resolves none. Catches a hand-written entry
// that does not wire its generated builds function.
func TestEmbeddedCatalog_LaterBuildsHaveReleaseNotes(t *testing.T) {
	t.Parallel()

	for _, entryID := range []string{"perch-v2", "birdnet-plus-v3.0"} {
		t.Run(entryID, func(t *testing.T) {
			t.Parallel()

			entry, ok := GetCatalogEntry(entryID)
			require.Truef(t, ok, "catalog entry %q", entryID)
			laterBuilds := 0
			for i := range entry.Variants {
				v := &entry.Variants[i]
				if v.Region == "" {
					continue
				}
				i := slices.IndexFunc(entry.Builds, func(b CatalogBuild) bool {
					return b.Version == VariantVersion(&entry, v.ID) && b.Build == VariantBuild(&entry, v.ID)
				})
				has := i >= 0
				if v.Build <= 1 {
					assert.Falsef(t, has, "build 1 variant %q must have no release notes", v.ID)
					continue
				}
				if v.Legacy {
					continue
				}
				laterBuilds++
				require.Truef(t, has, "live variant %q (build %d) must have release notes", v.ID, v.Build)
				assert.NotEmptyf(t, entry.Builds[i].Notes, "variant %q release notes", v.ID)
			}
			assert.Positivef(t, laterBuilds, "%s must ship live builds above 1", entryID)
		})
	}
}
