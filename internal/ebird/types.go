// Package ebird provides a client for interacting with the eBird API v2
package ebird

import "time"

// TaxonomyEntry represents a single entry from the eBird taxonomy
type TaxonomyEntry struct {
	ScientificName string   `json:"sciName"`
	CommonName     string   `json:"comName"`
	SpeciesCode    string   `json:"speciesCode"`
	Category       string   `json:"category"`      // species, spuh, slash, hybrid, etc.
	TaxonOrder     float64  `json:"taxonOrder"`    // For sorting in taxonomic order
	BandingCodes   []string `json:"bandingCodes"`  // Array of banding codes
	ComNameCodes   []string `json:"comNameCodes"`  // Array of common name codes
	SciNameCodes   []string `json:"sciNameCodes"`  // Array of scientific name codes
	Order          string   `json:"order"`         // Taxonomic order
	FamilyCode     string   `json:"familyCode"`    // Family code (added in v3.3.32.4)
	FamilyComName  string   `json:"familyComName"` // Common family name
	FamilySciName  string   `json:"familySciName"` // Scientific family name
	ReportAs       string   `json:"reportAs,omitempty"`      // Species to report as (for subspecies)
	Extinct        bool     `json:"extinct,omitempty"`       // Whether species is extinct
	ExtinctYear    int      `json:"extinctYear,omitempty"`   // Year of extinction
	FamilyTaxonOrder float64 `json:"familyTaxonOrder,omitempty"` // Order within family
}

// TaxonomyTree represents the hierarchical structure of a species' taxonomy
type TaxonomyTree struct {
	Kingdom       string    `json:"kingdom"`
	Phylum        string    `json:"phylum"`
	Class         string    `json:"class"`
	Order         string    `json:"order"`
	Family        string    `json:"family"`
	FamilyCommon  string    `json:"family_common"`
	Genus         string    `json:"genus"`
	Species       string    `json:"species"`
	SpeciesCommon string    `json:"species_common"`
	Subspecies    []string  `json:"subspecies,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// CachedTaxonomy wraps taxonomy data with cache metadata
type CachedTaxonomy struct {
	Data      []TaxonomyEntry `json:"data"`
	CachedAt  time.Time       `json:"cached_at"`
	ExpiresAt time.Time       `json:"expires_at"`
}

// Config holds configuration for the eBird client
type Config struct {
	APIKey      string        `json:"api_key"`
	BaseURL     string        `json:"base_url"`
	Timeout     time.Duration `json:"timeout"`
	CacheTTL    time.Duration `json:"cache_ttl"`
	RateLimitMS int           `json:"rate_limit_ms"` // Milliseconds between requests
}

// Error represents an eBird API error response
type Error struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func (e *Error) Error() string {
	return e.Detail
}

// DefaultConfig returns a Config with sensible defaults
func DefaultConfig() Config {
	return Config{
		BaseURL:     "https://api.ebird.org/v2",
		Timeout:     30 * time.Second,
		CacheTTL:    24 * time.Hour, // Taxonomy rarely changes
		RateLimitMS: 100,             // 10 requests per second max
	}
}