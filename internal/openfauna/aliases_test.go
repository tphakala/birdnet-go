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
