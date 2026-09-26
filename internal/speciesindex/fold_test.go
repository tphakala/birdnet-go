package speciesindex

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/text/unicode/norm"
)

func TestFold(t *testing.T) {
	t.Parallel()

	// Composed "é" (U+00E9) vs decomposed "e"+U+0301 must fold to the same
	// key. decomposed is derived via NFD so it is genuinely "e"+combining-acute
	// regardless of how this source file is normalized on disk.
	const composed = "éire" // precomposed e-acute + "ire"
	decomposed := norm.NFD.String(composed)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"mixed case", "Tawny Owl", "tawny owl"},
		{"already folded", "great tit", "great tit"},
		{"composed accent", composed, "éire"},
		{"decomposed accent normalizes", decomposed, "éire"},
		{"leading trailing space preserved", " Owl ", " owl "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, Fold(tt.in))
		})
	}

	// Composed and decomposed forms fold identically.
	assert.Equal(t, Fold(composed), Fold(decomposed))
	// Sanity: the two inputs are genuinely different byte sequences.
	assert.NotEqual(t, composed, decomposed)
}

// TestFold_MatchesReferenceOverCorpus round-trips Fold against the exact
// expression it replaces, over every common name in the v2.4 corpus.
func TestFold_MatchesReferenceOverCorpus(t *testing.T) {
	t.Parallel()

	for _, label := range loadCorpus(t) {
		_, common, ok := strings.Cut(label, "_")
		if !ok {
			continue
		}
		common = strings.TrimSpace(common)
		want := strings.ToLower(norm.NFC.String(common))
		assert.Equal(t, want, Fold(common), "label %q", label)
	}
}
