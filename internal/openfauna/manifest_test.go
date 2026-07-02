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
