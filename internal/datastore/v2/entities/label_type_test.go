package entities_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

// TestDefaultLabelTypesDriftGuard asserts that DefaultLabelTypes() contains an
// entry for every category exported by the nonbird package, plus the species
// entry, and has no duplicate names. This test must fail if nonbird.Categories()
// grows without a matching entry in DefaultLabelTypes().
func TestDefaultLabelTypesDriftGuard(t *testing.T) {
	t.Parallel()

	types := entities.DefaultLabelTypes()
	require.NotEmpty(t, types)

	// Build a set of names for easy lookup.
	nameSet := make(map[string]struct{}, len(types))
	for _, lt := range types {
		_, dup := nameSet[lt.Name]
		assert.False(t, dup, "duplicate name %q in DefaultLabelTypes()", lt.Name)
		nameSet[lt.Name] = struct{}{}
	}

	// Every nonbird category must have a matching label-type entry.
	for _, c := range nonbird.Categories() {
		_, ok := nameSet[string(c)]
		assert.True(t, ok, "DefaultLabelTypes() missing entry for nonbird.Category %q", c)
	}

	// The species entry must also be present.
	_, ok := nameSet[entities.LabelTypeSpecies]
	assert.True(t, ok, "DefaultLabelTypes() missing entry for LabelTypeSpecies (%q)", entities.LabelTypeSpecies)
}
