package openfauna

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeMetaRecord is the single decode step shared by lookupMetaShared and primeMeta.
// Both used to return a decode failure as a scan error, which the scan-error paths
// deliberately refuse to memoize so a *transient* failure isn't cached. But the
// embedded dataset is immutable, so a decode failure is permanent: the effect was that
// one malformed record made every subsequent lookup of that name re-scan the whole
// ~15k-record dataset, and in primeMeta abandoned the entire batch. These tests pin the
// decode contract that lets both callers memoize the failure as "no usable metadata".
//
// They exercise the helper directly rather than swapping the embedded blob: metadataGz
// is a package global and much of this package's suite runs with t.Parallel().

func TestDecodeMetaRecord_DecodesAWellFormedRecord(t *testing.T) {
	t.Parallel()

	line := []byte(`{"scientific_name":"Turdus merula","taxonomy":{"class":"Aves","order":"Passeriformes","family":"Turdidae","family_common":"Thrushes"}}`)

	m, ok := decodeMetaRecord(line)

	require.True(t, ok, "a well-formed record must decode")
	assert.Equal(t, "Aves", m.Class)
	assert.Equal(t, "Passeriformes", m.Order)
	assert.Equal(t, "Turdidae", m.Family)
	assert.Equal(t, "Thrushes", m.FamilyCommon)
}

// A malformed body must report ok=false rather than propagating an error, so the
// callers memoize it instead of re-scanning the dataset on every future lookup.
func TestDecodeMetaRecord_ReportsNotOkOnMalformedBody(t *testing.T) {
	t.Parallel()

	// streamMetadataNames already drops lines whose scientific_name won't decode, so
	// the reachable corruption is a usable name with a malformed body.
	for name, line := range map[string]string{
		"taxonomy is not an object": `{"scientific_name":"Turdus merula","taxonomy":"nope"}`,
		"links is not an object":    `{"scientific_name":"Turdus merula","links":42}`,
		"truncated json":            `{"scientific_name":"Turdus merula","taxonomy":{`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Guard the premise: the name itself still decodes, so streamMetadataNames
			// would hand this line to the callback rather than skipping it.
			var nameOnly metadataNameRecord
			if err := json.Unmarshal([]byte(line), &nameOnly); err == nil {
				require.Equal(t, "Turdus merula", nameOnly.ScientificName)
			}

			m, ok := decodeMetaRecord([]byte(line))

			assert.False(t, ok, "a malformed body must report ok=false, not an error")
			assert.Equal(t, Meta{}, m, "a failed decode must not return partial data")
		})
	}
}
