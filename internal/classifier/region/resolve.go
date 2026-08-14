package region

import (
	"cmp"
	"math"
	"slices"
)

// Mode values for the shared BirdNET.ModelRegion setting. internal/conf aliases
// these so the setting layer and the resolver share one vocabulary. Any value
// that is not a mode is treated as a pinned region slug.
const (
	// ModeAuto resolves the region from the configured coordinates, falling back
	// to the global model when nothing resolves. The empty string is treated as
	// ModeAuto for backward compatibility with configs written before this field
	// existed.
	ModeAuto = "auto"
	// ModeGlobal always prefers the global model regardless of coordinates.
	ModeGlobal = "global"
)

const (
	// earthRadiusKm is the mean Earth radius used for great-circle distances.
	earthRadiusKm = 6371.0
	// ambiguityDepthRatio marks a resolution ambiguous when the same-tier
	// runner-up penetrates at least this fraction as deeply as the winner (the
	// "within 15 percent" border band). Being a ratio, the band scales with the
	// overlap: a large overlap yields a wide band, a sliver a tight one.
	ambiguityDepthRatio = 0.85
	// centroidLen is the number of elements in a Centroid array [lat, lon].
	centroidLen = 2
)

// Source records how a Selection was reached: the rung of the mode / D8 fallback
// ladder that produced it.
type Source string

const (
	// SourcePinned means the stored slug exists in this family's table and was
	// used directly; coordinates were not consulted.
	SourcePinned Source = "pinned"
	// SourceAuto means the region was resolved from coordinates under ModeAuto.
	SourceAuto Source = "auto"
	// SourcePinnedFallback means a stored slug was absent from this family's
	// table, so coordinates were used instead (the D8 per-family fallback).
	SourcePinnedFallback Source = "pinned-fallback"
	// SourceGlobal means the global model is used: ModeGlobal, no tile resolved,
	// or the end of the fallback ladder.
	SourceGlobal Source = "global"
)

// Match is one candidate tile that contains the resolved point, carrying the
// tier used to rank it and the penetration depth used to break ties within a
// tier.
type Match struct {
	Slug    string
	Tier    int
	DepthKm float64
}

// Selection is the outcome of applying a ModelRegion mode to one family's table.
// A zero Slug means "use this family's global model". Ambiguous is set when a
// same-tier runner-up falls inside the border band, in which case RunnerUp names
// it and the UI surfaces both for the user to pin.
type Selection struct {
	Slug      string
	Source    Source
	Ambiguous bool
	RunnerUp  string
	Matches   []Match // full candidate list from coordinate resolution, best first
}

// radians converts degrees to radians.
func radians(deg float64) float64 { return deg * math.Pi / 180 }

// haversineKm returns the great-circle distance in kilometres between two
// points given in degrees.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2 := radians(lat1), radians(lat2)
	dp := radians(lat2 - lat1)
	dl := radians(lon2 - lon1)
	sinDp := math.Sin(dp / 2)
	sinDl := math.Sin(dl / 2)
	a := sinDp*sinDp + math.Cos(p1)*math.Cos(p2)*sinDl*sinDl
	return 2 * earthRadiusKm * math.Asin(math.Min(1, math.Sqrt(a)))
}

// depthKm is the penetration depth of a contained point within one box: the
// great-circle distance to the nearest of the four edges, measured to the
// point's orthogonal projection onto each edge. Using haversine (rather than a
// flat degree count) is what makes a longitude degree shrink toward the poles,
// so a point near the top of a high-latitude tile like Svalbard is not treated
// as if it sat on the equator. The caller guarantees the box contains the point.
func (b BBox) depthKm(lat, lon float64) float64 {
	south := haversineKm(lat, lon, b.LatMin, lon)
	north := haversineKm(lat, lon, b.LatMax, lon)
	west := haversineKm(lat, lon, lat, b.LonMin)
	east := haversineKm(lat, lon, lat, b.LonMax)
	return math.Min(math.Min(south, north), math.Min(west, east))
}

// Resolve returns the tiles whose bboxes contain (lat, lon), filtered to the
// single highest tier present and sorted by penetration depth descending
// (deepest first), with slug ascending as a deterministic tiebreaker over map
// iteration order. It returns nil when no tile contains the point, which the
// caller reads as "use the global model".
//
// Tier resolves nesting (a regional tile inside a continental one wins on tier);
// depth resolves partial overlap between same-tier tiles (the point sitting
// deeper inside one of two overlapping tiles wins).
func (t *Table) Resolve(lat, lon float64) []Match {
	if t == nil {
		return nil
	}
	var candidates []Match
	for slug := range t.Regions {
		r := t.Regions[slug]
		depth := math.Inf(-1)
		for _, b := range r.BBoxes {
			// Bboxes are disjoint, so a point is contained in at most one box of a
			// tile; take the deepest containing box. Known limitation of the bbox
			// model (not polygon geometry): a point exactly on the internal seam
			// between two adjacent boxes of the same tile reads as depth 0 in each,
			// so a different same-tier tile overlapping that seam could win the
			// depth tiebreak. Tier resolves the common nesting case first, and an
			// exact-seam coincidence is negligible in practice.
			if b.Contains(lat, lon) {
				if d := b.depthKm(lat, lon); d > depth {
					depth = d
				}
			}
		}
		if math.IsInf(depth, -1) {
			continue // no box of this tile contains the point
		}
		candidates = append(candidates, Match{Slug: slug, Tier: r.Tier, DepthKm: depth})
	}
	if len(candidates) == 0 {
		return nil
	}
	bestTier := candidates[0].Tier
	for _, c := range candidates[1:] {
		if c.Tier > bestTier {
			bestTier = c.Tier
		}
	}
	candidates = slices.DeleteFunc(candidates, func(m Match) bool { return m.Tier != bestTier })
	slices.SortFunc(candidates, func(a, b Match) int {
		if c := cmp.Compare(b.DepthKm, a.DepthKm); c != 0 { // depth descending
			return c
		}
		return cmp.Compare(a.Slug, b.Slug) // slug ascending, deterministic
	})
	return candidates
}

// Ambiguous reports whether the top two matches (already filtered to one tier by
// Resolve) fall within the border band, i.e. the runner-up penetrates at least
// ambiguityDepthRatio as deeply as the winner. It compares only rank 1 and rank
// 2: a three-way border overlap is rare enough that surfacing the top two is the
// right UX, and the winner is unchanged regardless.
func Ambiguous(matches []Match) bool {
	return len(matches) >= 2 && matches[1].DepthKm >= ambiguityDepthRatio*matches[0].DepthKm
}

// Select applies a ModelRegion mode to one family's table and returns the
// resulting Selection. It implements the three modes and the D8 per-family
// fallback ladder:
//
//   - ModeGlobal (or "global"): always the global model.
//   - ModeAuto (or ""): resolve from coordinates; global when nothing resolves.
//   - a pinned slug: use it if this family has that slug; otherwise fall back to
//     coordinate resolution in this family's table; otherwise the global model.
//
// Passing a nil table degrades to the global model, matching TableForRepo's
// not-ok contract.
func Select(t *Table, modelRegion string, lat, lon float64) Selection {
	switch modelRegion {
	case ModeGlobal:
		return Selection{Source: SourceGlobal}
	case ModeAuto, "":
		return selectionFromMatches(t.Resolve(lat, lon), SourceAuto)
	default: // pinned slug
		if t != nil {
			if _, ok := t.Regions[modelRegion]; ok {
				return Selection{Slug: modelRegion, Source: SourcePinned}
			}
		}
		return selectionFromMatches(t.Resolve(lat, lon), SourcePinnedFallback)
	}
}

// selectionFromMatches turns a coordinate-resolution result into a Selection,
// attaching the ambiguity band and, when present, the runner-up. An empty
// result becomes the global model regardless of the requested source.
func selectionFromMatches(matches []Match, src Source) Selection {
	if len(matches) == 0 {
		return Selection{Source: SourceGlobal}
	}
	sel := Selection{Slug: matches[0].Slug, Source: src, Matches: matches}
	if Ambiguous(matches) {
		sel.Ambiguous = true
		sel.RunnerUp = matches[1].Slug
	}
	return sel
}
