package openfauna

import (
	"encoding/json"
	"strconv"
	"strings"
)

// expectedSchemaMajor is the OpenFauna compiled-data schema major version this
// package consumes. Breaking change 2.0.0 moved metadata from a flat CSV to the
// nested metadata.jsonl + sources.json registry; this package parses that 2.x
// shape, so a mismatch means the vendored data and parser are out of step.
const expectedSchemaMajor = 2

// manifest mirrors the fields of build/manifest.json this package reads.
type manifest struct {
	SchemaVersion string `json:"schema_version"`
	SpeciesCount  int    `json:"species_count"`
}

// parseSchemaMajor extracts the integer major version from a manifest.json blob.
// It accepts "2", "2.1", "2.1.0". Returns ok=false on parse failure or a missing
// schema_version so callers can fail loud.
func parseSchemaMajor(raw []byte) (int, bool) {
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, false
	}
	v := strings.TrimSpace(m.SchemaVersion)
	if v == "" {
		return 0, false
	}
	majorStr, _, _ := strings.Cut(v, ".")
	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return 0, false
	}
	return major, true
}

// embeddedSchemaMajor returns the major schema version of the embedded manifest.
func embeddedSchemaMajor() (int, bool) {
	return parseSchemaMajor(manifestJSON)
}
