package region

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goldenTiers is the authoritative slug-to-tier banding of every shipped tile. It is a
// literal snapshot of the shipped geometry: any reband, rename, addition, or
// removal in a refreshed regions.json changes this comparison and fails CI, so a
// tier change is never silent. When a refresh legitimately changes the geometry,
// update this map in the same commit and say why.
var goldenTiers = map[string]int{
	"amazonia":              TierRegional,
	"andes":                 TierRegional,
	"australia-east":        TierRegional,
	"azores":                TierLocal,
	"baltics":               TierRegional,
	"british-isles":         TierRegional,
	"canada-alaska":         TierContinental,
	"canary-islands":        TierLocal,
	"cape-verde":            TierLocal,
	"central-africa":        TierRegional,
	"central-europe":        TierRegional,
	"central-india":         TierLocal,
	"china-north-central":   TierRegional,
	"china-northeast":       TierRegional,
	"china-southeast":       TierRegional,
	"china-southwest":       TierRegional,
	"east-africa":           TierRegional,
	"eastern-brazil":        TierRegional,
	"eastern-europe":        TierRegional,
	"galapagos":             TierLocal,
	"hawaii":                TierLocal,
	"himalaya":              TierRegional,
	"iberia":                TierRegional,
	"iceland":               TierLocal,
	"indo-gangetic":         TierRegional,
	"japan":                 TierRegional,
	"madeira":               TierLocal,
	"mauritius":             TierLocal,
	"new-caledonia":         TierLocal,
	"new-zealand":           TierRegional,
	"nordic":                TierRegional,
	"north-america-east":    TierRegional,
	"north-america-west":    TierRegional,
	"reunion":               TierLocal,
	"sao-tome-principe":     TierLocal,
	"seychelles":            TierLocal,
	"south-asia-peninsular": TierRegional,
	"southern-africa":       TierRegional,
	"southern-cone":         TierRegional,
	"southern-europe":       TierRegional,
	"svalbard":              TierLocal,
	"tibet":                 TierRegional,
	"west-africa":           TierRegional,
	"western-palearctic":    TierContinental,
}

// familyOmits lists, per repo, the slugs of goldenTiers that the family does not
// ship. A family may lag the shared tile set (the superseded BirdNET v3.0 preview
// is not rebuilt for tiles added later); the omission must be acknowledged here,
// so a family that silently loses a tile still fails TestGoldenTierTable.
var familyOmits = map[string][]string{
	// The superseded BirdNET v3.0 preview is not rebuilt for tiles added later.
	"tphakala/BirdNET-v3.0-Models": {"central-india"},
}

// goldenTiersFor returns goldenTiers without the slugs the repo's family omits.
func goldenTiersFor(repo string) map[string]int {
	want := make(map[string]int, len(goldenTiers))
	maps.Copy(want, goldenTiers)
	for _, slug := range familyOmits[repo] {
		delete(want, slug)
	}
	return want
}

// TestGoldenTierTable asserts every embedded family matches the golden banding
// exactly, tile for tile, apart from the slugs it acknowledges in familyOmits. A
// family that changes a tier, drops a tile or gains an unlisted one fails here.
func TestGoldenTierTable(t *testing.T) {
	t.Parallel()
	tables, err := Tables()
	require.NoError(t, err)

	for repo, tbl := range tables {
		t.Run(repo, func(t *testing.T) {
			t.Parallel()
			got := make(map[string]int, len(tbl.Regions))
			for slug, r := range tbl.Regions {
				got[slug] = r.Tier
			}
			assert.Equal(t, goldenTiersFor(repo), got, "tier banding for %s drifted from golden", repo)
		})
	}
}
