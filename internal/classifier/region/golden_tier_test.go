package region

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goldenTiers is the authoritative slug-to-tier banding of all 40 tiles. It is a
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
	"central-europe":        TierRegional,
	"china-north-central":   TierRegional,
	"china-northeast":       TierRegional,
	"china-southeast":       TierRegional,
	"china-southwest":       TierRegional,
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
	"western-palearctic":    TierContinental,
}

// TestGoldenTierTable asserts every embedded family matches the golden banding
// exactly, tile for tile. It runs against all families because D8 assumes their
// geometry stays in step; a family that changes a tier or drops a tile fails here.
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
			assert.Equal(t, goldenTiers, got, "tier banding for %s drifted from golden", repo)
		})
	}
}
