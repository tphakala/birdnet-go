package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetNewSpecies_ExcludesFalsePositives guards the "New Species Detected" chart against counting
// species whose detections were all reviewed as false positives, and against a species' lifetime
// first-seen being pinned to a false-positive detection.
func TestGetNewSpecies_ExcludesFalsePositives(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	withFP := seedLabel(t, db, "Parus major")
	allFP := seedLabel(t, db, "Turdus merula")

	// Parus major's earliest detection is a false positive: first-seen must skip it for the next real one.
	fpID := seedDetection(t, db, withFP, 1000, 0.6)
	seedFalsePositiveReview(t, db, fpID)
	seedDetection(t, db, withFP, 1800, 0.9)

	// Turdus merula has only a false-positive detection: it is not a newly detected species.
	allFPID := seedDetection(t, db, allFP, 1200, 0.5)
	seedFalsePositiveReview(t, db, allFPID)

	got, err := repo.GetNewSpecies(ctx, 500, 5000, 100, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Parus major", got[0].ScientificName)
	assert.Equal(t, int64(1800), got[0].FirstDetected)
}

// TestGetNewSpecies_FalsePositiveBeforeWindowDoesNotHideNewSpecies checks the exclusion is applied
// when computing the lifetime first-seen, not only within the window. A false positive predating the
// window must not count as prior history: the species is genuinely new and must still be reported.
// This is the inverse failure of the reported bug — the old query hid real new species this way.
func TestGetNewSpecies_FalsePositiveBeforeWindowDoesNotHideNewSpecies(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	label := seedLabel(t, db, "Parus major")
	fpID := seedDetection(t, db, label, 100, 0.6) // false positive before the window
	seedFalsePositiveReview(t, db, fpID)
	seedDetection(t, db, label, 1000, 0.8) // first real detection, inside the window

	got, err := repo.GetNewSpecies(ctx, 500, 5000, 100, 0)
	require.NoError(t, err)
	require.Len(t, got, 1, "a species whose only prior detection was a false positive is still new")
	assert.Equal(t, "Parus major", got[0].ScientificName)
	assert.Equal(t, int64(1000), got[0].FirstDetected)
}

// TestGetNewSpecies_RealDetectionBeforeWindowIsNotNew guards the window bound itself while the
// false-positive filter moves around it: genuine prior history still disqualifies a species.
func TestGetNewSpecies_RealDetectionBeforeWindowIsNotNew(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	label := seedLabel(t, db, "Parus major")
	seedDetection(t, db, label, 100, 0.9) // real detection before the window
	seedDetection(t, db, label, 1000, 0.8)

	got, err := repo.GetNewSpecies(ctx, 500, 5000, 100, 0)
	require.NoError(t, err)
	assert.Empty(t, got, "species first detected before the window is not new")
}
