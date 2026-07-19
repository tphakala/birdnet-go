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

// TestGetNewSpecies_FalsePositiveDoesNotHideEarlierLifetimeFirst checks the exclusion is applied
// when computing the lifetime first-seen, not only inside the window: a species whose only real
// detection predates the window must still not be reported as new.
func TestGetNewSpecies_FalsePositiveDoesNotHideEarlierLifetimeFirst(t *testing.T) {
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
