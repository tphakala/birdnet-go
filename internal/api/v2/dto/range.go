package dto

// RangeFilterSpecies represents a single species in the range filter. It lives in
// the shared dto package because it is the response element type for two domains:
// the range filter endpoints (/api/v2/range/species/*) and the species picker
// (/api/v2/species/all). Keeping it here avoids a domain-to-domain import between
// those packages. The json tags are the wire contract and must not change.
type RangeFilterSpecies struct {
	Label          string   `json:"label"`
	ScientificName string   `json:"scientificName"`
	CommonName     string   `json:"commonName"`
	Score          *float64 `json:"score,omitempty"` // Nullable - only present when individual scores are available
	// HasCustomConfig and IsManuallyIncluded report why a species is in the list:
	// it is keyed in realtime.species.config, and/or listed in
	// realtime.species.include. Both are absent-or-true, never explicitly false:
	// the server resolves the user's key (which may be an alias) to a canonical
	// model label, so the client cannot recover this by matching the displayed
	// names against the settings, but a client that gets no field at all must
	// still be able to fall back to that matching rather than read the absence as
	// a definitive "no".
	HasCustomConfig    *bool `json:"hasCustomConfig,omitempty"`
	IsManuallyIncluded *bool `json:"isManuallyIncluded,omitempty"`
}
