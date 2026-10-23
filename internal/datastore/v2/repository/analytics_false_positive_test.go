package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

const (
	// secondsPerHour and secondsPerDay build detection timestamps relative to a day start.
	secondsPerHour = int64(3600)
	secondsPerDay  = 24 * secondsPerHour

	// analyticsDay1 is 2024-06-14T00:00:00Z.
	analyticsDay1 = int64(1718323200)
	analyticsDay2 = analyticsDay1 + secondsPerDay

	// confidenceTolerance is the allowed difference for averaged and maximum confidence values.
	confidenceTolerance = 1e-9
)

// seedCorrectReview marks a detection as reviewed correct.
func seedCorrectReview(t *testing.T, db *gorm.DB, detectionID uint) {
	t.Helper()
	review := entities.DetectionReview{
		DetectionID: detectionID,
		Verified:    entities.VerificationCorrect,
	}
	require.NoError(t, db.Create(&review).Error)
}

// TestGetSpeciesSummary_ExcludesFalsePositives pins the false-positive exclusion in the species
// summary query: a species whose detections were all reviewed as false positives is not listed,
// and a false positive does not count towards another species' totals, first detection or
// maximum confidence. A detection reviewed as correct still counts.
func TestGetSpeciesSummary_ExcludesFalsePositives(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	parus := seedLabel(t, db, "Parus major")
	turdus := seedLabel(t, db, "Turdus merula")

	// The earliest and most confident Parus major detection is a false positive.
	seedFalsePositiveReview(t, db, seedDetection(t, db, parus, 1000, 0.95))
	seedDetection(t, db, parus, 2000, 0.6)
	seedCorrectReview(t, db, seedDetection(t, db, parus, 3000, 0.7))

	// Turdus merula has only a false positive.
	seedFalsePositiveReview(t, db, seedDetection(t, db, turdus, 1500, 0.8))

	got, err := repo.GetSpeciesSummary(ctx, 500, 5000, nil)
	require.NoError(t, err)
	require.Len(t, got, 1, "a species with only false positives must not be listed")
	assert.Equal(t, "Parus major", got[0].ScientificName)
	assert.Equal(t, int64(2), got[0].TotalDetections)
	assert.Equal(t, int64(2000), got[0].FirstDetection)
	assert.InDelta(t, 0.7, got[0].MaxConfidence, confidenceTolerance)
}

// TestGetDailyAnalytics_ExcludesFalsePositives pins the false-positive exclusion in the daily
// analytics query: false positives count neither as detections nor as species on their day, and a
// day with only false positives has no row. A detection reviewed as correct still counts.
func TestGetDailyAnalytics_ExcludesFalsePositives(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	parus := seedLabel(t, db, "Parus major")
	turdus := seedLabel(t, db, "Turdus merula")

	seedDetection(t, db, parus, analyticsDay1+secondsPerHour, 0.6)
	seedCorrectReview(t, db, seedDetection(t, db, parus, analyticsDay1+2*secondsPerHour, 0.8))
	seedFalsePositiveReview(t, db, seedDetection(t, db, turdus, analyticsDay1+3*secondsPerHour, 0.9))

	// The second day has only a false positive.
	seedFalsePositiveReview(t, db, seedDetection(t, db, parus, analyticsDay2+secondsPerHour, 0.9))

	got, err := repo.GetDailyAnalytics(ctx, analyticsDay1, analyticsDay2+secondsPerDay, 0, nil, nil)
	require.NoError(t, err)
	require.Len(t, got, 1, "a day with only false positives must have no row")
	assert.Equal(t, "2024-06-14", got[0].Date)
	assert.Equal(t, int64(2), got[0].TotalDetections)
	assert.Equal(t, int64(1), got[0].UniqueSpecies)
	assert.InDelta(t, 0.7, got[0].AvgConfidence, confidenceTolerance)
}
