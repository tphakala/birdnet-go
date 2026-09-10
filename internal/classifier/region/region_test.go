package region

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectedTileCount is the number of regional tiles each family publishes.
const expectedTileCount = 40

// TestTablesLoad confirms both embedded snapshots parse, validate, and describe
// the two expected families.
func TestTablesLoad(t *testing.T) {
	t.Parallel()
	tables, err := Tables()
	require.NoError(t, err, "embedded snapshots must load")
	require.Len(t, tables, 2, "two families are embedded")

	for _, repo := range []string{perchRepo, birdnetRepo} {
		tbl, ok := tables[repo]
		require.True(t, ok, "table for %s must be embedded", repo)
		assert.Equal(t, snapshotSchema, tbl.Schema, "schema version")
		assert.Equal(t, repo, tbl.Repo, "self-describing repo id")
		assert.Len(t, tbl.Regions, expectedTileCount, "tile count for %s", repo)
	}
}

// TestCrossFamilyGeometryIdentity documents the invariant the D8 shared setting
// relies on today: both families publish identical geometry, tiers, and slug
// sets, differing only in per-tile class counts. The resolver never assumes this
// (it resolves per family), but a future divergence should be a conscious change
// that trips this test.
func TestCrossFamilyGeometryIdentity(t *testing.T) {
	t.Parallel()
	perch, ok := TableForRepo(perchRepo)
	require.True(t, ok)
	birdnet, ok := TableForRepo(birdnetRepo)
	require.True(t, ok)

	require.Len(t, birdnet.Regions, len(perch.Regions), "same tile count")
	for slug, pr := range perch.Regions {
		br, ok := birdnet.Regions[slug]
		require.True(t, ok, "slug %s present in both families", slug)
		assert.Equal(t, pr.Tier, br.Tier, "tier for %s", slug)
		assert.Equal(t, pr.BBoxes, br.BBoxes, "bboxes for %s", slug)
		assert.Equal(t, pr.Centroid, br.Centroid, "centroid for %s", slug)
		// Display fields must match too: the gallery dropdown dedupes a shared
		// slug by taking one family's Name/Group/GroupDisplay, so a divergence
		// here would make the dropdown label depend on family order.
		assert.Equal(t, pr.Name, br.Name, "name for %s", slug)
		assert.Equal(t, pr.Group, br.Group, "group for %s", slug)
		assert.Equal(t, pr.GroupDisplay, br.GroupDisplay, "group display for %s", slug)
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
