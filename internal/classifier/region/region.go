// Package region turns a user's latitude/longitude into the regional model tile
// that best covers it, for the hardware- and region-aware model gallery.
//
// It is a pure leaf: it imports only the standard library, embed, and
// internal/errors. It deliberately does NOT import internal/classifier or
// internal/conf, so the catalog layer, the settings layer, and both API layers
// can depend on it without any import cycle. The mode vocabulary (auto / global)
// is owned here and aliased by internal/conf, keeping a single source of truth.
//
// The region geometry is generated upstream (acoustic-models) and published as a
// regions.json per model family. A build-time snapshot of each family's table is
// embedded (see embed.go and data/), so region resolution works fully offline;
// a later phase overrides the snapshot with a fetched copy.
package region

import (
	"encoding/json"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// snapshotSchema is the regions.json schema version this package understands. A
// snapshot declaring any other value is rejected at load time.
const snapshotSchema = 1

// Tier bands classify a tile by geographic specificity. Higher wins: a point
// inside both a continental and a regional tile resolves to the regional one.
// The values are assigned upstream from bbox area and are read from the
// snapshot, never recomputed here; they are named so the validator and tests
// can reason about them without magic numbers.
const (
	// TierContinental covers a continent-scale tile (upstream: bbox area above
	// 1500 deg^2).
	TierContinental = 10
	// TierRegional covers a regional tile (upstream: 150 to 1500 deg^2).
	TierRegional = 50
	// TierLocal covers a local tile (upstream: below 150 deg^2).
	TierLocal = 90
)

// bboxFields is the number of elements in a bbox JSON array:
// [lat_min, lat_max, lon_min, lon_max].
const bboxFields = 4

// Coordinate bounds used to sanity-check a snapshot at load time.
const (
	minLat = -90.0
	maxLat = 90.0
	minLon = -180.0
	maxLon = 180.0
)

// Table is the parsed region snapshot for one model family (one HuggingFace
// repo). Its Regions map is keyed by region slug (e.g. "iberia").
type Table struct {
	Schema  int               `json:"schema"`
	Repo    string            `json:"repo"`    // HuggingFace repo id this table describes
	Updated string            `json:"updated"` // upstream generation date (YYYY-MM-DD)
	Regions map[string]Region `json:"regions"`
}

// Region is one tile of a family's region table.
type Region struct {
	Name         string    `json:"name"`          // display name (e.g. "Iberia")
	Group        string    `json:"group"`         // continental bucket slug (e.g. "europe")
	GroupDisplay string    `json:"group_display"` // continental bucket display name (e.g. "Europe")
	Realm        string    `json:"realm"`         // biogeographic realm
	Tier         int       `json:"tier"`          // specificity band; see the Tier constants
	BBoxes       []BBox    `json:"bboxes"`        // disjoint axis-aligned boxes covering the tile
	Centroid     []float64 `json:"centroid"`      // [lat, lon] of the tile centroid
	Countries    Countries `json:"countries"`     // ISO 3166-1 alpha-2 country codes
	Classes      int       `json:"classes"`       // species this tile's model can identify
}

// Countries lists the ISO 3166-1 alpha-2 codes a tile covers, split into fully
// covered (core) and partially covered (partial) countries.
type Countries struct {
	Core    []string `json:"core"`
	Partial []string `json:"partial"`
}

// BBox is a latitude/longitude-aligned box. In regions.json it is encoded as a
// 4-element array [lat_min, lat_max, lon_min, lon_max]. Upstream guarantees no
// box crosses the antimeridian (lon_min <= lon_max always), which the load-time
// validator re-asserts and on which Contains relies.
type BBox struct {
	LatMin float64
	LatMax float64
	LonMin float64
	LonMax float64
}

// UnmarshalJSON decodes the [lat_min, lat_max, lon_min, lon_max] array form.
func (b *BBox) UnmarshalJSON(data []byte) error {
	var raw []float64
	if err := json.Unmarshal(data, &raw); err != nil {
		return errors.New(err).
			Component("classifier.region").
			Category(errors.CategoryValidation).
			Context("operation", "unmarshal_bbox").
			Build()
	}
	if len(raw) != bboxFields {
		return errors.Newf("bbox must have %d elements, got %d", bboxFields, len(raw)).
			Component("classifier.region").
			Category(errors.CategoryValidation).
			Context("operation", "unmarshal_bbox").
			Build()
	}
	b.LatMin, b.LatMax, b.LonMin, b.LonMax = raw[0], raw[1], raw[2], raw[3]
	return nil
}

// Contains reports whether (lat, lon) falls inside the box, inclusive on all
// four edges. A point exactly on a border belongs to the box (and to both boxes
// that share that border), which the resolver's tier and depth logic then
// disambiguates.
func (b BBox) Contains(lat, lon float64) bool {
	return lat >= b.LatMin && lat <= b.LatMax && lon >= b.LonMin && lon <= b.LonMax
}

// validate checks a freshly parsed snapshot for the invariants the resolver
// relies on. It is called at load time; a failure means the embedded snapshot is
// malformed, which is a build defect surfaced by the package tests. The returned
// error names the offending slug so a bad refresh is easy to locate.
func (t *Table) validate() error {
	fail := func(msg, slug string) error {
		return errors.Newf("region snapshot %q: %s", t.Repo, msg).
			Component("classifier.region").
			Category(errors.CategoryValidation).
			Context("operation", "validate_snapshot").
			Context("slug", slug).
			Build()
	}
	if t.Schema != snapshotSchema {
		return fail("unsupported schema version", "")
	}
	if t.Repo == "" {
		return fail("empty repo", "")
	}
	if len(t.Regions) == 0 {
		return fail("no regions", "")
	}
	for slug := range t.Regions {
		r := t.Regions[slug]
		if len(r.BBoxes) == 0 {
			return fail("region has no bboxes", slug)
		}
		switch r.Tier {
		case TierContinental, TierRegional, TierLocal:
		default:
			return fail("region has an unknown tier", slug)
		}
		for _, b := range r.BBoxes {
			if b.LatMin > b.LatMax {
				return fail("bbox has lat_min > lat_max", slug)
			}
			// Re-assert the upstream antimeridian guarantee: a box that wrapped
			// past 180 would report lon_min > lon_max and break Contains.
			if b.LonMin > b.LonMax {
				return fail("bbox has lon_min > lon_max (antimeridian crossing)", slug)
			}
			if b.LatMin < minLat || b.LatMax > maxLat || b.LonMin < minLon || b.LonMax > maxLon {
				return fail("bbox coordinate out of range", slug)
			}
		}
		if len(r.Centroid) != centroidLen {
			return fail("centroid must have 2 elements", slug)
		}
	}
	return nil
}
