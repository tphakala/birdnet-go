package classifier

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// TestCanonicalSpeciesKeyMatchesSpeciesindex pins the equality between the
// classifier's canonicalSpeciesKey (mapped_range_filter.go) and the canonical key
// the shared speciesindex builds. Phase 2b substitutes the memoized speciesindex
// map for the local helper, so this guards that the substitution is behavior-
// preserving over the full v2.4 label corpus (memo-hit path) plus a few
// taxonomic-alias legacy names (compute path). Classifier tests may import the
// leaf speciesindex package; speciesindex never imports classifier, so there is
// no cycle.
func TestCanonicalSpeciesKeyMatchesSpeciesindex(t *testing.T) {
	t.Parallel()

	const corpusPath = "data/labels/V2.4/BirdNET_GLOBAL_6K_V2.4_Labels_en_uk.txt"
	data, err := os.ReadFile(corpusPath)
	require.NoError(t, err, "v2.4 corpus must be present")

	var corpus []string
	for line := range strings.Lines(string(data)) {
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		corpus = append(corpus, line)
	}
	require.NotEmpty(t, corpus)

	snap := speciesindex.Build(corpus, nil, "")

	// Memo-hit path: every corpus label.
	for _, label := range corpus {
		assert.Equalf(t, canonicalSpeciesKey(label), snap.CanonicalKey(label),
			"canonical key mismatch (memo) for %q", label)
	}

	// Compute path: taxonomic-alias legacy names and formatted variants not
	// necessarily in the corpus.
	for _, label := range []string{
		"Accipiter badius",
		"Accipiter badius_Shikra",
		"Tachyspiza badia",
		"Turdus merula_Blackbird",
	} {
		assert.Equalf(t, canonicalSpeciesKey(label), snap.CanonicalKey(label),
			"canonical key mismatch (compute) for %q", label)
	}
}
