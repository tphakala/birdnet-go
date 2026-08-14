package region

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	perchRepo   = "tphakala/Perch-v2-Models"
	birdnetRepo = "tphakala/BirdNET-v3.0-Models"
)

// perchTable loads the embedded Perch v2 region table, the shared geometry the
// resolver tests exercise (all families carry identical geometry; see
// TestCrossFamilyGeometryIdentity).
func perchTable(t *testing.T) *Table {
	t.Helper()
	tbl, ok := TableForRepo(perchRepo)
	require.True(t, ok, "perch region snapshot must be embedded")
	return tbl
}

// TestResolve covers the geometry cases that pin the tier-then-depth algorithm,
// including the two named regression cases from issue #1468.
func TestResolve(t *testing.T) {
	t.Parallel()
	tbl := perchTable(t)

	tests := []struct {
		name      string
		lat, lon  float64
		wantTop   string // "" means no tile resolves (caller uses global)
		wantTier  int
		ambiguous bool
		runnerUp  string
	}{
		{
			// Bogota sits in both andes and amazonia (both tier 50). Centroid
			// distance and bbox area both wrongly pick amazonia; only penetration
			// depth is right, and andes is the deeper containment.
			name: "bogota resolves to andes on depth", lat: 4.7, lon: -74.1,
			wantTop: "andes", wantTier: TierRegional,
		},
		{
			// Madrid sits in both iberia (tier 50) and western-palearctic (tier
			// 10, which geometrically contains iberia). Tier picks iberia; pure
			// depth would wrongly pick western-palearctic.
			name: "madrid resolves to iberia on tier", lat: 40, lon: -3.5,
			wantTop: "iberia", wantTier: TierRegional,
		},
		{
			// A second tier-decides case: Helsinki is in nordic (50) and
			// western-palearctic (10).
			name: "helsinki resolves to nordic on tier", lat: 60.17, lon: 24.94,
			wantTop: "nordic", wantTier: TierRegional,
		},
		{
			// A single high-latitude tile. The depth here is a haversine tell:
			// an equirectangular approximation fails to shrink longitude at 78N.
			name: "svalbard single tile high latitude", lat: 78.0, lon: 20.0,
			wantTop: "svalbard", wantTier: TierLocal,
		},
		{
			// Deeper into the andes/amazonia overlap the two depths fall within
			// the border band, so the resolution is ambiguous.
			name: "border overlap is ambiguous", lat: 2.0, lon: -71.0,
			wantTop: "andes", wantTier: TierRegional, ambiguous: true, runnerUp: "amazonia",
		},
		{
			name: "null island resolves to nothing", lat: 0, lon: 0,
			wantTop: "",
		},
		{
			// No tile covers the geographic pole, so it degrades to global. This
			// also documents the cos(lat)=0 depth singularity is never reached in
			// practice.
			name: "north pole resolves to nothing", lat: 90, lon: 0,
			wantTop: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			matches := tbl.Resolve(tc.lat, tc.lon)
			if tc.wantTop == "" {
				assert.Empty(t, matches, "expected no tile to resolve")
				return
			}
			require.NotEmpty(t, matches, "expected a tile to resolve")
			assert.Equal(t, tc.wantTop, matches[0].Slug, "top match slug")
			assert.Equal(t, tc.wantTier, matches[0].Tier, "top match tier")
			assert.Equal(t, tc.ambiguous, Ambiguous(matches), "ambiguity band")
			if tc.runnerUp != "" {
				require.GreaterOrEqual(t, len(matches), 2, "expected a runner-up")
				assert.Equal(t, tc.runnerUp, matches[1].Slug, "runner-up slug")
			}
		})
	}
}

// TestResolveDepths asserts the exact penetration depths behind the regression
// cases, so an implementation drift (equirectangular distance, wrong edge)
// changes a number and fails here even if the winning slug happens to survive.
func TestResolveDepths(t *testing.T) {
	t.Parallel()
	tbl := perchTable(t)

	const tolKm = 0.5
	matches := tbl.Resolve(4.7, -74.1) // Bogota
	require.Len(t, matches, 2)
	assert.Equal(t, "andes", matches[0].Slug)
	assert.InDelta(t, 764.66, matches[0].DepthKm, tolKm, "andes depth at Bogota")
	assert.Equal(t, "amazonia", matches[1].Slug)
	assert.InDelta(t, 543.02, matches[1].DepthKm, tolKm, "amazonia depth at Bogota")

	// Svalbard: 253.93 km with haversine; an equirectangular approximation
	// yields 333.5 km via the wrong edge and fails this assertion.
	sv := tbl.Resolve(78.0, 20.0)
	require.Len(t, sv, 1)
	assert.InDelta(t, 253.93, sv[0].DepthKm, tolKm, "svalbard depth (haversine tell)")
}

// TestBBoxContainsEdgeIsInclusive confirms a point exactly on a border belongs
// to the box, with depth 0.
func TestBBoxContainsEdgeIsInclusive(t *testing.T) {
	t.Parallel()
	b := BBox{LatMin: 10, LatMax: 20, LonMin: 30, LonMax: 40}
	assert.True(t, b.Contains(10, 35), "point on the south edge is inside")
	assert.True(t, b.Contains(20, 40), "corner is inside")
	assert.False(t, b.Contains(9.9, 35), "point below the south edge is outside")
	assert.InDelta(t, 0.0, b.depthKm(10, 35), 1e-9, "depth on an edge is zero")
}

// TestSelectModes covers the three ModelRegion modes and the D8 per-family
// fallback ladder against the real Perch table.
func TestSelectModes(t *testing.T) {
	t.Parallel()
	tbl := perchTable(t)
	const bogotaLat, bogotaLon = 4.7, -74.1

	t.Run("auto and empty behave identically", func(t *testing.T) {
		t.Parallel()
		auto := Select(tbl, ModeAuto, bogotaLat, bogotaLon)
		empty := Select(tbl, "", bogotaLat, bogotaLon)
		assert.Equal(t, "andes", auto.Slug)
		assert.Equal(t, SourceAuto, auto.Source)
		assert.Equal(t, auto.Slug, empty.Slug)
		assert.Equal(t, auto.Source, empty.Source)
	})

	t.Run("global short-circuits coordinates", func(t *testing.T) {
		t.Parallel()
		sel := Select(tbl, ModeGlobal, bogotaLat, bogotaLon)
		assert.Empty(t, sel.Slug, "global uses the global model")
		assert.Equal(t, SourceGlobal, sel.Source)
	})

	t.Run("pinned slug present ignores coordinates", func(t *testing.T) {
		t.Parallel()
		sel := Select(tbl, "iberia", bogotaLat, bogotaLon)
		assert.Equal(t, "iberia", sel.Slug)
		assert.Equal(t, SourcePinned, sel.Source)
	})

	t.Run("auto with no tile falls back to global", func(t *testing.T) {
		t.Parallel()
		sel := Select(tbl, ModeAuto, 0, 0)
		assert.Empty(t, sel.Slug)
		assert.Equal(t, SourceGlobal, sel.Source)
	})
}

// TestSelectD8Fallback exercises the per-family fallback for a stored slug that
// is absent from a family's table, using a synthetic table missing andes.
func TestSelectD8Fallback(t *testing.T) {
	t.Parallel()
	tbl := perchTable(t)

	// A divergent family: same geometry but without the andes tile.
	divergent := &Table{Schema: snapshotSchema, Repo: "tphakala/Divergent", Regions: map[string]Region{}}
	for slug := range tbl.Regions {
		if slug == "andes" {
			continue
		}
		divergent.Regions[slug] = tbl.Regions[slug]
	}

	t.Run("pinned absent falls back to coordinates in this table", func(t *testing.T) {
		t.Parallel()
		// Stored slug andes is absent here; Bogota coordinates still sit inside
		// amazonia, so resolution finds it via the fallback.
		sel := Select(divergent, "andes", 4.7, -74.1)
		assert.Equal(t, "amazonia", sel.Slug)
		assert.Equal(t, SourcePinnedFallback, sel.Source)
	})

	t.Run("pinned absent with no coverage falls back to global", func(t *testing.T) {
		t.Parallel()
		sel := Select(divergent, "andes", 0, 0)
		assert.Empty(t, sel.Slug)
		assert.Equal(t, SourceGlobal, sel.Source)
	})

	t.Run("divergence does not break a family that has the slug", func(t *testing.T) {
		t.Parallel()
		// The same stored slug resolves normally against the real family, proving
		// per-family resolution is independent.
		realFamily := Select(tbl, "andes", 4.7, -74.1)
		assert.Equal(t, "andes", realFamily.Slug)
		assert.Equal(t, SourcePinned, realFamily.Source)
	})
}

// TestSelectNilTable confirms a missing family degrades to the global model
// rather than panicking.
func TestSelectNilTable(t *testing.T) {
	t.Parallel()
	assert.Equal(t, SourceGlobal, Select(nil, ModeAuto, 4.7, -74.1).Source)
	assert.Equal(t, SourceGlobal, Select(nil, "iberia", 4.7, -74.1).Source)
	assert.Equal(t, SourceGlobal, Select(nil, ModeGlobal, 4.7, -74.1).Source)
}
