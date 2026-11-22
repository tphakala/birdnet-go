package region

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTablesLoad confirms both embedded snapshots parse, validate, and describe
// the three expected families.
func TestTablesLoad(t *testing.T) {
	t.Parallel()
	tables, err := Tables()
	require.NoError(t, err, "embedded snapshots must load")
	require.Len(t, tables, 3, "three families are embedded")

	for _, repo := range []string{perchRepo, birdnetRepo, birdnetPlusRepo} {
		tbl, ok := tables[repo]
		require.True(t, ok, "table for %s must be embedded", repo)
		assert.Equal(t, snapshotSchema, tbl.Schema, "schema version")
		assert.Equal(t, repo, tbl.Repo, "self-describing repo id")
		assert.Len(t, tbl.Regions, len(goldenTiers)-len(familyOmits[repo]), "tile count for %s matches the golden tier snapshot minus its acknowledged omissions", repo)
	}
}

// TestCrossFamilyGeometryIdentity_SharedSlugs checks that a slug two families
// both ship has identical geometry, tier, display fields and countries in both.
// The gallery dropdown dedupes a shared slug by taking one family's display
// fields, so a divergence would make the label depend on family order. The tile
// sets themselves may differ (familyOmits). The resolver never assumes identity
// (it resolves per family), but a divergence should be a conscious change that
// trips this test.
func TestCrossFamilyGeometryIdentity_SharedSlugs(t *testing.T) {
	t.Parallel()
	repos := []string{perchRepo, birdnetRepo, birdnetPlusRepo}
	for i, repoA := range repos {
		for _, repoB := range repos[i+1:] {
			t.Run(repoA+" vs "+repoB, func(t *testing.T) {
				t.Parallel()
				a, ok := TableForRepo(repoA)
				require.True(t, ok)
				b, ok := TableForRepo(repoB)
				require.True(t, ok)

				shared := 0
				for slug, ra := range a.Regions {
					rb, ok := b.Regions[slug]
					if !ok {
						continue
					}
					shared++
					assert.Equal(t, ra.Tier, rb.Tier, "tier for %s", slug)
					assert.Equal(t, ra.BBoxes, rb.BBoxes, "bboxes for %s", slug)
					assert.Equal(t, ra.Centroid, rb.Centroid, "centroid for %s", slug)
					assert.Equal(t, ra.Name, rb.Name, "name for %s", slug)
					assert.Equal(t, ra.Group, rb.Group, "group for %s", slug)
					assert.Equal(t, ra.GroupDisplay, rb.GroupDisplay, "group display for %s", slug)
					assert.Equal(t, ra.Countries, rb.Countries, "countries for %s", slug)
				}
				assert.NotZero(t, shared, "two families must share tiles")
			})
		}
	}
}

// TestBBoxUnmarshalArity confirms the 4-element array decodes and any other
// arity is rejected.
func TestBBoxUnmarshalArity(t *testing.T) {
	t.Parallel()
	var b BBox
	require.NoError(t, json.Unmarshal([]byte(`[10, 20, 30, 40]`), &b))
	assert.Equal(t, BBox{LatMin: 10, LatMax: 20, LonMin: 30, LonMax: 40}, b)

	for _, bad := range []string{`[10, 20, 30]`, `[10, 20, 30, 40, 50]`, `[]`} {
		assert.Error(t, json.Unmarshal([]byte(bad), &b), "arity %s must error", bad)
	}
}

// TestValidateRejectsBadSnapshot spot-checks the load-time validator on the
// invariants the resolver depends on.
func TestValidateRejectsBadSnapshot(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tbl  Table
	}{
		{"bad schema", Table{Schema: 99, Repo: "r", Regions: map[string]Region{"a": okRegion()}}},
		{"empty repo", Table{Schema: snapshotSchema, Repo: "", Regions: map[string]Region{"a": okRegion()}}},
		{"no regions", Table{Schema: snapshotSchema, Repo: "r", Regions: map[string]Region{}}},
		{"no bboxes", Table{Schema: snapshotSchema, Repo: "r", Regions: map[string]Region{
			"a": {Tier: TierRegional, Centroid: []float64{0, 0}},
		}}},
		{"unknown tier", Table{Schema: snapshotSchema, Repo: "r", Regions: map[string]Region{
			"a": {Tier: 42, BBoxes: []BBox{{}}, Centroid: []float64{0, 0}},
		}}},
		{"antimeridian crossing", Table{Schema: snapshotSchema, Repo: "r", Regions: map[string]Region{
			"a": {Tier: TierRegional, BBoxes: []BBox{{LatMin: 0, LatMax: 1, LonMin: 170, LonMax: -170}}, Centroid: []float64{0, 0}},
		}}},
		{"lat inverted", Table{Schema: snapshotSchema, Repo: "r", Regions: map[string]Region{
			"a": {Tier: TierRegional, BBoxes: []BBox{{LatMin: 10, LatMax: 0, LonMin: 0, LonMax: 1}}, Centroid: []float64{0, 0}},
		}}},
		{"bad centroid", Table{Schema: snapshotSchema, Repo: "r", Regions: map[string]Region{
			"a": {Tier: TierRegional, BBoxes: []BBox{{}}, Centroid: []float64{0}},
		}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Error(t, tc.tbl.validate())
		})
	}

	valid := Table{Schema: snapshotSchema, Repo: "r", Regions: map[string]Region{"a": okRegion()}}
	assert.NoError(t, valid.validate(), "a well-formed snapshot validates")
}

// okRegion returns a minimal valid region for validator tests.
func okRegion() Region {
	return Region{
		Tier:     TierRegional,
		BBoxes:   []BBox{{LatMin: 0, LatMax: 1, LonMin: 0, LonMax: 1}},
		Centroid: []float64{0.5, 0.5},
	}
}
