package dto

// RangeFilterSpecies represents a single species in the range filter. It lives in
// the shared dto package because it is the response element type for two domains:
// the range filter endpoints (/api/v2/range/species/*) and the species picker
// (/api/v2/species/all). Keeping it here avoids a domain-to-domain import between
// those packages. The json tags are the wire contract and must not change.
type RangeFilterSpecies struct {
	Label               string   `json:"label"`
	ScientificName      string   `json:"scientificName"`
	CommonName          string   `json:"commonName"`
	Score               *float64 `json:"score,omitempty"`              // Nullable - only present when individual scores are available
	RangeScore          *float64 `json:"rangeScore,omitempty"`         // Native geomodel score when Score is a synthetic override sentinel
	IsManuallyIncluded  bool     `json:"isManuallyIncluded,omitempty"` // True for explicit Always Include overrides
	IsSyntheticOverride bool     `json:"-"`                            // Internal display-dedup provenance; never serialized
}
