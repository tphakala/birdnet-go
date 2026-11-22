package openfauna

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCanonicalName exercises alias resolution and the identity/normalization
// contract from one table.
func TestCanonicalName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// Streptopelia senegalensis (BirdNET v2.4) -> Spilopelia senegalensis
			// (eBird). The Laughing Dove case from the discussion that motivated
			// taxonomic aliasing.
			name: "known alias resolves to canonical",
			in:   "Streptopelia senegalensis",
			want: "Spilopelia senegalensis",
		},
		{
			name: "match is case- and space-insensitive",
			in:   "  streptopelia SENEGALENSIS  ",
			want: "Spilopelia senegalensis",
		},
		{
			// A non-aliased name passes through, trimmed, so callers get the same
			// stable key form as the alias path.
			name: "unknown name trimmed and unchanged",
			in:   "  Turdus merula  ",
			want: "Turdus merula",
		},
		{
			// Renamed BirdNET+ labels resolve to the name the geomodel uses.
			name: "renamed model label resolves to geomodel name",
			in:   "Nannus troglodytes",
			want: "Troglodytes troglodytes",
		},
		{
			name: "renamed model genus resolves to geomodel name",
			in:   "Schoeniclus schoeniclus",
			want: "Emberiza schoeniclus",
		},
		{
			name: "already canonical name unchanged",
			in:   "Spilopelia senegalensis",
			want: "Spilopelia senegalensis",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, CanonicalName(tt.in))
		})
	}
}

// TestAliasCountNonZero guards against the embedded artifact going missing or
// failing to parse. The specific aliases are asserted by name in TestCanonicalName,
// so this only needs a populated map, not a brittle exact size.
func TestAliasCountNonZero(t *testing.T) {
	t.Parallel()
	require.NotZero(t, AliasCount(), "embedded alias map should be populated")
}

// TestCanonicalName_EmbeddedMapIsSingleHop guards the single-lookup contract: no
// alias target is itself an alias key, so CanonicalName is idempotent and canonical
// keys do not depend on how many times a caller resolves a name.
func TestCanonicalName_EmbeddedMapIsSingleHop(t *testing.T) {
	t.Parallel()

	aliasOnce.Do(loadAliases)
	for from, to := range aliasMap {
		_, chained := aliasMap[normalizeName(to)]
		require.Falsef(t, chained, "alias %q -> %q chains into another alias", from, to)
	}
}

func TestExpandScientificCodeMap_BidirectionalAndDirectPrecedence(t *testing.T) {
	t.Parallel()

	m := map[string]string{
		"Coccothraustes vespertinus": "evegro",     // forward -> Hesperiphona vespertina
		"Dryophytes versicolor":      "t-11124704", // reverse -> Hyla versicolor
		"Streptopelia senegalensis":  "laudov1",
		"Spilopelia senegalensis":    "direct-code", // must not be overwritten
	}

	ExpandScientificCodeMap(m)

	require.Equal(t, "evegro", m["Hesperiphona vespertina"])
	require.Equal(t, "t-11124704", m["Hyla versicolor"])
	require.Equal(t, "direct-code", m["Spilopelia senegalensis"], "forward pass must not overwrite a canonical entry")
	require.Equal(t, "laudov1", m["Streptopelia senegalensis"], "reverse pass must not overwrite a legacy entry")
}
