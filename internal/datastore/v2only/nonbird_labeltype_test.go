package v2only

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

// TestNew_CachesNonBirdLabelTypeIDs verifies that after construction all seven
// nonbird categories have a non-zero label_type_id in the cached map.
func TestNew_CachesNonBirdLabelTypeIDs(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	cats := nonbird.Categories()
	require.Len(t, cats, 7, "expected 7 non-bird categories")

	for _, cat := range cats {
		id, ok := ds.nonBirdLabelTypeIDs[cat]
		assert.True(t, ok, "nonBirdLabelTypeIDs missing category %q", cat)
		assert.NotZero(t, id, "nonBirdLabelTypeIDs[%q] must be non-zero", cat)
	}
}

// TestNew_NonBirdLabelTypeIDsMapSize ensures the map has exactly as many entries
// as there are categories (no extra, no missing).
func TestNew_NonBirdLabelTypeIDsMapSize(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	assert.Len(t, ds.nonBirdLabelTypeIDs, len(nonbird.Categories()),
		"nonBirdLabelTypeIDs should have one entry per category")
}
