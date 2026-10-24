package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

// contribStats holds the recorded confidence stats of a contribution row; a nil
// pointer seeds a row written before those columns existed (all four NULL).
type contribStats struct {
	minConfidence float64
	confidenceSum float64
}

// seedContribution creates one detection_model_contributions row.
func seedContribution(t *testing.T, db *gorm.DB, detectionID, modelID uint, hits int, maxConf float64, stats *contribStats) {
	t.Helper()
	row := entities.DetectionModelContribution{
		DetectionID:   detectionID,
		ModelID:       modelID,
		HitCount:      hits,
		MaxConfidence: maxConf,
	}
	if stats != nil {
		first, last := int64(1), int64(2)
		row.FirstHitAt, row.LastHitAt = &first, &last
		row.MinConfidence = &stats.minConfidence
		row.ConfidenceSum = &stats.confidenceSum
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedReview creates a review with the given verdict for a detection.
func seedReview(t *testing.T, db *gorm.DB, detectionID uint, verdict entities.VerificationStatus) {
	t.Helper()
	require.NoError(t, db.Create(&entities.DetectionReview{DetectionID: detectionID, Verified: verdict}).Error)
}

// Range used by the model contribution tests: half-open [999, 2000).
const (
	modelStatsTestStart = int64(999)
	modelStatsTestEnd   = int64(2000)
)

// seedModelContributionFixture seeds three models and seven detections:
// d1 A only with stats (correct); d2 A+B, B without stats (false_positive);
// d3 A+B+C (empty verdict); d4 B only without stats; d5 exactly at the range end
// (excluded); d6 exactly at the range start, A only without stats; d7 in range
// with no contribution rows.
func seedModelContributionFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Model(&entities.AIModel{}).Where("id = ?", 1).Update("model_type", entities.ModelTypeBird).Error)
	require.NoError(t, db.Create(&entities.AIModel{ID: 2, Name: "Perch", Version: "2", ModelType: entities.ModelTypeBird}).Error)
	require.NoError(t, db.Create(&entities.AIModel{ID: 3, Name: "BatNET", Version: "1", ModelType: entities.ModelTypeBat}).Error)

	label := seedLabel(t, db, "Turdus merula")
	d1 := seedDetection(t, db, label, 1000, 0.9)
	d2 := seedDetection(t, db, label, 1500, 0.8)
	d3 := seedDetection(t, db, label, 1800, 0.7)
	d4 := seedDetection(t, db, label, 1900, 0.6)
	d5 := seedDetection(t, db, label, modelStatsTestEnd, 0.9)
	d6 := seedDetection(t, db, label, modelStatsTestStart, 0.5)
	_ = seedDetection(t, db, label, 1200, 0.5) // d7: no contribution rows

	seedContribution(t, db, d1, 1, 3, 0.9, &contribStats{0.6, 2.1})
	seedContribution(t, db, d2, 1, 2, 0.8, &contribStats{0.7, 1.5})
	seedContribution(t, db, d2, 2, 1, 0.75, nil)
	seedContribution(t, db, d3, 1, 1, 0.7, &contribStats{0.7, 0.7})
	seedContribution(t, db, d3, 2, 2, 0.85, &contribStats{0.5, 1.2})
	seedContribution(t, db, d3, 3, 1, 0.4, &contribStats{0.4, 0.4})
	seedContribution(t, db, d4, 2, 4, 0.6, nil)
	seedContribution(t, db, d5, 1, 9, 0.99, &contribStats{0.1, 5.0})
	seedContribution(t, db, d6, 1, 1, 0.5, nil)

	seedReview(t, db, d1, entities.VerificationCorrect)
	seedReview(t, db, d2, entities.VerificationFalsePositive)
	seedReview(t, db, d3, "") // an empty verdict, which migration can carry over
}

// assertModelStatsConsistent checks the cross-field relations a client may rely on.
func assertModelStatsConsistent(t *testing.T, stats *ModelContributionStats) {
	t.Helper()
	require.NotNil(t, stats)
	var hits int64
	var share float64
	byID := make(map[uint]int64, len(stats.Models))
	for i := range stats.Models {
		m := &stats.Models[i]
		byID[m.ModelID] = m.Detections
		hits += m.Hits
		share += m.HitShare
		assert.Equal(t, m.Detections, m.Correct+m.FalsePositive+m.Unreviewed, "outcomes partition detections of model %d", m.ModelID)
		assert.LessOrEqual(t, m.SoleDetections, m.Detections)
		assert.LessOrEqual(t, m.DetectionsWithStats, m.Detections)
		assert.LessOrEqual(t, m.Detections, stats.DetectionsWithContributions)
		if m.MinConfidence != nil && m.MeanConfidence != nil {
			assert.LessOrEqual(t, *m.MinConfidence, *m.MeanConfidence)
			assert.LessOrEqual(t, *m.MeanConfidence, m.MaxConfidence)
		}
	}
	assert.Equal(t, stats.TotalHits, hits)
	if stats.TotalHits > 0 {
		assert.InDelta(t, 1.0, share, 1e-9)
	}
	assert.LessOrEqual(t, stats.DetectionsWithContributions, stats.TotalDetections)
	for _, a := range stats.Agreements {
		assert.Less(t, a.ModelAID, a.ModelBID)
		assert.LessOrEqual(t, a.SharedDetections, min(byID[a.ModelAID], byID[a.ModelBID]))
	}
}

func TestGetModelContributionStats(t *testing.T) {
	db := setupInsightsTestDB(t)
	seedModelContributionFixture(t, db)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	stats, err := repo.GetModelContributionStats(ctx, modelStatsTestStart, modelStatsTestEnd)
	require.NoError(t, err)
	assertModelStatsConsistent(t, stats)

	assert.Equal(t, int64(6), stats.TotalDetections)
	assert.Equal(t, int64(5), stats.DetectionsWithContributions)
	assert.Equal(t, int64(15), stats.TotalHits)

	require.Len(t, stats.Models, 3)
	a, b, c := &stats.Models[0], &stats.Models[1], &stats.Models[2]

	assert.Equal(t, uint(1), a.ModelID)
	assert.Equal(t, "BirdNET", a.Name)
	assert.Equal(t, "2.4", a.Version)
	assert.Equal(t, "default", a.Variant)
	assert.Equal(t, "bird", a.ModelType)
	assert.Equal(t, int64(4), a.Detections)
	assert.Equal(t, int64(7), a.Hits)
	assert.InDelta(t, 7.0/15.0, a.HitShare, 1e-9)
	assert.Equal(t, int64(2), a.SoleDetections)
	assert.Equal(t, int64(3), a.DetectionsWithStats)
	assert.InDelta(t, 0.9, a.MaxConfidence, 1e-9)
	require.NotNil(t, a.MinConfidence)
	assert.InDelta(t, 0.6, *a.MinConfidence, 1e-9)
	require.NotNil(t, a.MeanConfidence)
	assert.InDelta(t, 4.3/6.0, *a.MeanConfidence, 1e-9)
	assert.Equal(t, int64(1), a.Correct)
	assert.Equal(t, int64(1), a.FalsePositive)
	assert.Equal(t, int64(2), a.Unreviewed, "an empty verdict counts as unreviewed")

	assert.Equal(t, uint(2), b.ModelID)
	assert.Equal(t, int64(3), b.Detections)
	assert.Equal(t, int64(7), b.Hits)
	assert.Equal(t, int64(1), b.SoleDetections)
	assert.Equal(t, int64(1), b.DetectionsWithStats, "rows without stats are counted but not as stats rows")
	assert.InDelta(t, 0.85, b.MaxConfidence, 1e-9)
	require.NotNil(t, b.MinConfidence)
	assert.InDelta(t, 0.5, *b.MinConfidence, 1e-9)
	require.NotNil(t, b.MeanConfidence)
	assert.InDelta(t, 0.6, *b.MeanConfidence, 1e-9, "NULL rows must not drag the mean")
	assert.Equal(t, int64(0), b.Correct)
	assert.Equal(t, int64(1), b.FalsePositive)
	assert.Equal(t, int64(2), b.Unreviewed)

	assert.Equal(t, uint(3), c.ModelID)
	assert.Equal(t, "bat", c.ModelType)
	assert.Equal(t, int64(1), c.Detections)
	assert.Equal(t, int64(1), c.Hits)
	assert.Equal(t, int64(0), c.SoleDetections)
	assert.Equal(t, int64(1), c.Unreviewed)

	assert.Equal(t, []ModelAgreement{
		{ModelAID: 1, ModelBID: 2, SharedDetections: 2},
		{ModelAID: 1, ModelBID: 3, SharedDetections: 1},
		{ModelAID: 2, ModelBID: 3, SharedDetections: 1},
	}, stats.Agreements)

	empty, err := repo.GetModelContributionStats(ctx, 5000, 6000)
	require.NoError(t, err)
	assert.Zero(t, empty.TotalDetections)
	assert.Zero(t, empty.DetectionsWithContributions)
	assert.Zero(t, empty.TotalHits)
	assert.NotNil(t, empty.Models)
	assert.Empty(t, empty.Models)
	assert.NotNil(t, empty.Agreements)
	assert.Empty(t, empty.Agreements)
}

func TestGetModelContributionStats_NullStatsOnly(t *testing.T) {
	db := setupInsightsTestDB(t)
	label := seedLabel(t, db, "Parus major")
	d := seedDetection(t, db, label, 1000, 0.5)
	seedContribution(t, db, d, 1, 2, 0.5, nil)

	stats, err := NewInsightsRepository(db, false, false).GetModelContributionStats(t.Context(), 0, 2000)
	require.NoError(t, err)
	require.Len(t, stats.Models, 1)
	m := &stats.Models[0]
	assert.Equal(t, int64(1), m.Detections)
	assert.Nil(t, m.MinConfidence)
	assert.Nil(t, m.MeanConfidence)
	assert.Equal(t, int64(0), m.DetectionsWithStats)
	assert.InDelta(t, 0.5, m.MaxConfidence, 1e-9, "the maximum comes from every row, including rows without stats")
}

func TestGetModelContributionStats_Transitions(t *testing.T) {
	db := setupInsightsTestDB(t)
	require.NoError(t, db.Create(&entities.AIModel{ID: 2, Name: "Perch", Version: "2", ModelType: entities.ModelTypeBird}).Error)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()
	label := seedLabel(t, db, "Turdus merula")
	d1 := seedDetection(t, db, label, 1000, 0.9)
	seedContribution(t, db, d1, 1, 1, 0.9, &contribStats{0.9, 0.9})

	load := func() (*ModelContributionStats, *ModelContributionStat, *ModelContributionStat) {
		t.Helper()
		stats, err := repo.GetModelContributionStats(ctx, 0, 2000)
		require.NoError(t, err)
		assertModelStatsConsistent(t, stats)
		var a, b *ModelContributionStat
		for i := range stats.Models {
			switch stats.Models[i].ModelID {
			case 1:
				a = &stats.Models[i]
			case 2:
				b = &stats.Models[i]
			}
		}
		return stats, a, b
	}

	// (a) one model only.
	stats, a, b := load()
	require.NotNil(t, a)
	assert.Nil(t, b)
	assert.Equal(t, int64(1), a.SoleDetections)
	assert.Empty(t, stats.Agreements)

	// (b) a second model joins the detection.
	seedContribution(t, db, d1, 2, 1, 0.8, &contribStats{0.8, 0.8})
	stats, a, b = load()
	require.NotNil(t, b)
	assert.Equal(t, int64(0), a.SoleDetections)
	assert.Equal(t, int64(1), b.Detections)
	assert.Equal(t, []ModelAgreement{{ModelAID: 1, ModelBID: 2, SharedDetections: 1}}, stats.Agreements)

	// (c) a review moves the outcome of both models.
	seedReview(t, db, d1, entities.VerificationFalsePositive)
	stats, a, b = load()
	assert.Equal(t, int64(1), a.FalsePositive)
	assert.Equal(t, int64(0), a.Unreviewed)
	assert.Equal(t, int64(1), b.FalsePositive)
	assert.Equal(t, int64(1), stats.TotalDetections)

	// (d) the second model's row goes away.
	require.NoError(t, db.Where("detection_id = ? AND model_id = ?", d1, 2).Delete(&entities.DetectionModelContribution{}).Error)
	stats, a, b = load()
	assert.Nil(t, b)
	assert.Equal(t, int64(1), a.SoleDetections)
	assert.Equal(t, int64(1), a.FalsePositive)
	assert.Empty(t, stats.Agreements)
}
