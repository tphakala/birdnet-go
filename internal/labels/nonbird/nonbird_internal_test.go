package nonbird

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassesGuard verifies the integrity of the static classes map and
// the derived firstTokenSet.
func TestClassesGuard(t *testing.T) {
	t.Parallel()

	const expectedClassCount = 198

	require.Len(t, classes, expectedClassCount, "classes map must contain exactly %d entries", expectedClassCount)

	// Every key must be lowercase.
	for k := range classes {
		assert.Equal(t, strings.ToLower(k), k, "classes key %q must be lowercase", k)
	}

	// Every entry in firstTokenSet must be the first token (before the first "_")
	// of at least one multi-word key in classes.
	multiWordKeys := make(map[string]bool)
	for k := range classes {
		if before, _, found := strings.Cut(k, "_"); found {
			multiWordKeys[before] = true
		}
	}
	for tok := range firstTokenSet {
		assert.True(t, multiWordKeys[tok], "firstTokenSet entry %q must be first token of at least one multi-word key in classes", tok)
	}
	// Reverse direction: every multi-word key's first token must be present in
	// firstTokenSet, so an init() regression that drops a token is caught.
	for tok := range multiWordKeys {
		_, ok := firstTokenSet[tok]
		assert.True(t, ok, "multi-word first token %q must exist in firstTokenSet", tok)
	}

	// Every category value used in classes must be one of the seven defined constants.
	validCategories := map[Category]bool{
		CategoryHuman:       true,
		CategoryAnimal:      true,
		CategoryMusic:       true,
		CategoryMechanical:  true,
		CategoryEnvironment: true,
		CategoryNoise:       true,
		CategoryDevice:      true,
	}
	for k, cat := range classes {
		assert.True(t, validCategories[cat], "classes[%q] has unknown category %q", k, cat)
	}
}

// TestMultiWordLabelNames_AllMultiWordKeys pins that every multi-word class key
// is recognised, keeps its identity, and gets a distinct display name.
func TestMultiWordLabelNames_AllMultiWordKeys(t *testing.T) {
	t.Parallel()

	displays := make(map[string]string)
	matched := 0
	for k := range classes {
		if !strings.Contains(k, "_") {
			id, disp, ok := MultiWordLabelNames(k)
			assert.False(t, ok, "single-token key %q must not match", k)
			assert.Empty(t, id)
			assert.Empty(t, disp)
			continue
		}
		matched++
		id, disp, ok := MultiWordLabelNames(k)
		require.True(t, ok, "multi-word key %q must match", k)
		assert.Equal(t, k, id)
		assert.NotContains(t, disp, "_")
		prev, dup := displays[disp]
		assert.Falsef(t, dup, "display %q shared by %q and %q", disp, prev, k)
		displays[disp] = k
	}
	assert.Len(t, displays, matched)
}
