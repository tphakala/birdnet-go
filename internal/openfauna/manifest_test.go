package openfauna

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedSchemaMajorIsTwo(t *testing.T) {
	major, ok := embeddedSchemaMajor()
	require.True(t, ok, "embedded manifest schema_version did not parse")
	assert.Equal(t, 2, major, "embedded OpenFauna schema major must be 2 (re-vendor with refresh-data.sh)")
}

func TestParseSchemaMajor(t *testing.T) {
	cases := map[string]struct {
		in    string
		major int
		ok    bool
	}{
		"normal":     {`{"schema_version":"2.1.0"}`, 2, true},
		"major only": {`{"schema_version":"3"}`, 3, true},
		"missing":    {`{"species_count":1}`, 0, false},
		"garbage":    {`not json`, 0, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			major, ok := parseSchemaMajor([]byte(tc.in))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.major, major)
		})
	}
}

func TestEmbeddedSpeciesCountIsPositive(t *testing.T) {
	count, ok := embeddedSpeciesCount()
	require.True(t, ok, "embedded manifest species_count did not parse")
	assert.Positive(t, count, "embedded OpenFauna species_count must be positive (drives the cache-cap sanity check)")
}

func TestParseSpeciesCount(t *testing.T) {
	cases := map[string]struct {
		in    string
		count int
		ok    bool
	}{
		"normal":   {`{"species_count":15000}`, 15000, true},
		"zero":     {`{"species_count":0}`, 0, false}, // non-positive is treated as absent
		"negative": {`{"species_count":-5}`, 0, false},
		"missing":  {`{"schema_version":"2.1.0"}`, 0, false},
		"garbage":  {`not json`, 0, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			count, ok := parseSpeciesCount([]byte(tc.in))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.count, count)
		})
	}
}
