package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

const (
	statsSpeciesA      = "Parus major"
	statsSpeciesB      = "Turdus merula"
	statsLegacyA       = "Parus major_Great Tit"
	statsCaptureName   = "test:capture_species_stats_statement"
	statsDetectionsTag = "SEARCH d "
	statsReviewsTag    = "SEARCH dr "
	statsReviewsIndex  = "idx_detection_reviews_detection_id"
	statsScanPrefix    = "SCAN"
	statsTempBTree     = "TEMP B-TREE"
	statsJoinMarker    = "LEFT JOIN"
	statsThirdModelID  = 3
)

// statsOracleRow is the expected state of one seeded detection.
type statsOracleRow struct {
	species string
	at      int64
	conf    float64
	review  entities.VerificationStatus
}

// statsOracle mirrors the database in Go so every expectation is computed, not typed.
type statsOracle map[uint]*statsOracleRow

func (o statsOracle) expected(species string) SpeciesStats {
	var want SpeciesStats
	var sum float64
	for _, row := range o {
		if row.species != species {
			continue
		}
		switch row.review {
		case entities.VerificationFalsePositive:
			want.FalsePositiveReviews++
			continue
		case entities.VerificationCorrect:
			want.CorrectReviews++
		}
		if want.Detections == 0 || row.at < want.FirstDetectedAt {
			want.FirstDetectedAt = row.at
		}
		want.LastDetectedAt = max(want.LastDetectedAt, row.at)
		want.MaxConfidence = max(want.MaxConfidence, row.conf)
		want.Detections++
		sum += row.conf
	}
	if want.Detections > 0 {
		want.AvgConfidence = sum / float64(want.Detections)
	}
	return want
}

func assertStatsEqual(t *testing.T, want, got *SpeciesStats, msg string) {
	t.Helper()
	require.NotNil(t, got, msg)
	assert.Equal(t, want.Detections, got.Detections, "%s: detections", msg)
	assert.Equal(t, want.FirstDetectedAt, got.FirstDetectedAt, "%s: first", msg)
	assert.Equal(t, want.LastDetectedAt, got.LastDetectedAt, "%s: last", msg)
	assert.InDelta(t, want.AvgConfidence, got.AvgConfidence, confidenceTolerance, "%s: average", msg)
	assert.InDelta(t, want.MaxConfidence, got.MaxConfidence, confidenceTolerance, "%s: maximum", msg)
	assert.Equal(t, want.CorrectReviews, got.CorrectReviews, "%s: correct reviews", msg)
	assert.Equal(t, want.FalsePositiveReviews, got.FalsePositiveReviews, "%s: false positive reviews", msg)
}

func TestGetSpeciesStats_FollowsDetectionsThroughChanges(t *testing.T) {
	db := setupInsightsTestDBMultiModel(t)
	require.NoError(t, db.Create(&entities.AIModel{ID: statsThirdModelID, Name: "Third", Version: "1.0"}).Error)
	repo := NewDetectionRepository(db, nil, false, false)

	labelA1 := seedLabelForModel(t, db, statsSpeciesA, 1)
	labelA2 := seedLabelForModel(t, db, statsSpeciesA, 2)
	labelB := seedLabelForModel(t, db, statsSpeciesB, 1)

	oracle := statsOracle{}
	add := func(label uint, species string, at int64, conf float64) uint {
		id := seedDetection(t, db, label, at, conf)
		oracle[id] = &statsOracleRow{species: species, at: at, conf: conf}
		return id
	}
	review := func(id uint, status entities.VerificationStatus) {
		require.NoError(t, db.Create(&entities.DetectionReview{DetectionID: id, Verified: status}).Error)
		oracle[id].review = status
	}

	best := add(labelA1, statsSpeciesA, 1000, 0.95)
	second := add(labelA2, statsSpeciesA, 2000, 0.60)
	add(labelA1, statsSpeciesA, 3000, 0.70)
	moved := add(labelA2, statsSpeciesA, 4000, 0.50)
	add(labelB, statsSpeciesB, 1500, 0.80)
	add(labelB, statsSpeciesB, 2500, 0.40)

	steps := []struct {
		name string
		do   func()
	}{
		{"seeded", func() {}},
		{"earliest and most confident marked false positive", func() { review(best, entities.VerificationFalsePositive) }},
		{"another detection marked correct", func() { review(second, entities.VerificationCorrect) }},
		{"false positive review removed", func() {
			require.NoError(t, db.Delete(&entities.DetectionReview{}, "detection_id = ?", best).Error)
			oracle[best].review = ""
		}},
		{"a detection deleted", func() {
			require.NoError(t, db.Delete(&entities.Detection{}, moved).Error)
			delete(oracle, moved)
		}},
		{"a detection moved to the other species", func() {
			require.NoError(t, db.Model(&entities.Detection{}).Where("id = ?", second).Update("label_id", labelB).Error)
			oracle[second].species = statsSpeciesB
		}},
		{"a label of a third model added", func() {
			label := seedLabelForModel(t, db, statsSpeciesA, statsThirdModelID)
			id := seedDetectionForModel(t, db, label, statsThirdModelID, 5000, 0.99)
			oracle[id] = &statsOracleRow{species: statsSpeciesA, at: 5000, conf: 0.99}
		}},
		{"a legacy form label added", func() {
			label := seedLabel(t, db, statsLegacyA)
			id := seedDetection(t, db, label, 500, 0.30)
			oracle[id] = &statsOracleRow{species: statsSpeciesA, at: 500, conf: 0.30}
			review(id, entities.VerificationFalsePositive)
			id = seedDetection(t, db, label, 6000, 0.45)
			oracle[id] = &statsOracleRow{species: statsSpeciesA, at: 6000, conf: 0.45}
		}},
	}
	for _, step := range steps {
		step.do()
		for _, species := range []string{statsSpeciesA, statsSpeciesB} {
			want := oracle.expected(species)
			got, err := repo.GetSpeciesStats(t.Context(), species)
			require.NoError(t, err, step.name)
			assertStatsEqual(t, &want, got, step.name+": "+species)
		}
	}
}

func TestGetSpeciesStats_Edges(t *testing.T) {
	t.Run("no label carries the name", func(t *testing.T) {
		db := setupInsightsTestDB(t)
		repo := NewDetectionRepository(db, nil, false, false)
		got, err := repo.GetSpeciesStats(t.Context(), "Nonexistent species")
		require.NoError(t, err)
		assert.Equal(t, &SpeciesStats{}, got)
	})

	t.Run("a label with no detections", func(t *testing.T) {
		db := setupInsightsTestDB(t)
		repo := NewDetectionRepository(db, nil, false, false)
		seedLabel(t, db, statsSpeciesA)
		got, err := repo.GetSpeciesStats(t.Context(), statsSpeciesA)
		require.NoError(t, err)
		assert.Equal(t, &SpeciesStats{}, got)
	})

	t.Run("only false positives", func(t *testing.T) {
		db := setupInsightsTestDB(t)
		repo := NewDetectionRepository(db, nil, false, false)
		label := seedLabel(t, db, statsSpeciesA)
		seedFalsePositiveReview(t, db, seedDetection(t, db, label, 1000, 0.9))
		seedFalsePositiveReview(t, db, seedDetection(t, db, label, 2000, 0.8))
		got, err := repo.GetSpeciesStats(t.Context(), statsSpeciesA)
		require.NoError(t, err)
		assert.Equal(t, &SpeciesStats{FalsePositiveReviews: 2}, got)
	})

	t.Run("a name with no species part selects no labels", func(t *testing.T) {
		db := setupInsightsTestDB(t)
		repo := NewDetectionRepository(db, nil, false, false)
		odd := seedLabel(t, db, "_Odd")
		seedDetection(t, db, odd, 1000, 0.9)
		for _, name := range []string{"", "   ", "_Odd"} {
			got, err := repo.GetSpeciesStats(t.Context(), name)
			require.NoError(t, err, "name %q", name)
			assert.Equal(t, &SpeciesStats{}, got, "name %q", name)
		}
	})

	t.Run("database errors are returned", func(t *testing.T) {
		db := setupInsightsTestDB(t)
		repo := NewDetectionRepository(db, nil, false, false)
		seedDetection(t, db, seedLabel(t, db, statsSpeciesA), 1000, 0.5)

		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := repo.GetSpeciesStats(canceled, statsSpeciesA)
		require.ErrorIs(t, err, context.Canceled, "the label lookup")

		require.NoError(t, db.Migrator().DropTable(&entities.DetectionReview{}))
		_, err = repo.GetSpeciesStats(t.Context(), statsSpeciesA)
		require.Error(t, err, "the aggregate")
	})

	t.Run("the legacy form as input is the same species", func(t *testing.T) {
		db := setupInsightsTestDB(t)
		repo := NewDetectionRepository(db, nil, false, false)
		seedDetection(t, db, seedLabel(t, db, statsSpeciesA), 1000, 0.5)
		seedDetection(t, db, seedLabel(t, db, statsLegacyA), 2000, 0.7)
		bare, err := repo.GetSpeciesStats(t.Context(), statsSpeciesA)
		require.NoError(t, err)
		legacy, err := repo.GetSpeciesStats(t.Context(), statsLegacyA)
		require.NoError(t, err)
		assert.Equal(t, int64(2), bare.Detections)
		assert.Equal(t, bare, legacy)
	})
}

func TestGetSpeciesStats_MatchesSpeciesSummary(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	label := seedLabel(t, db, statsSpeciesA)
	seedFalsePositiveReview(t, db, seedDetection(t, db, label, 1000, 0.95))
	seedDetection(t, db, label, 2000, 0.6)
	seedCorrectReview(t, db, seedDetection(t, db, label, 3000, 0.7))
	seedDetection(t, db, label, 4000, 0.5)

	summary, err := repo.GetSpeciesSummary(t.Context(), 0, 10000, nil)
	require.NoError(t, err)
	require.Len(t, summary, 1)
	got, err := repo.GetSpeciesStats(t.Context(), statsSpeciesA)
	require.NoError(t, err)

	assert.Equal(t, summary[0].TotalDetections, got.Detections)
	assert.Equal(t, summary[0].FirstDetection, got.FirstDetectedAt)
	assert.Equal(t, summary[0].LastDetection, got.LastDetectedAt)
	assert.InDelta(t, summary[0].AvgConfidence, got.AvgConfidence, confidenceTolerance)
	assert.InDelta(t, summary[0].MaxConfidence, got.MaxConfidence, confidenceTolerance)
}

// TestGetSpeciesStats_UsesIndexSearches pins that the aggregate seeks an index on
// the detections table and on the reviews table. A form that joins through the
// labels or scans returns the same numbers, so only the plan tells them apart.
func TestGetSpeciesStats_UsesIndexSearches(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	label := seedLabel(t, db, statsSpeciesA)
	seedCorrectReview(t, db, seedDetection(t, db, label, 1000, 0.9))
	seedDetection(t, db, label, 2000, 0.8)

	var statement string
	require.NoError(t, db.Callback().Row().After("gorm:row").Register(statsCaptureName, func(tx *gorm.DB) {
		if sql := tx.Statement.SQL.String(); strings.Contains(sql, statsJoinMarker) {
			statement = tx.Explain(sql, tx.Statement.Vars...)
		}
	}))
	_, err := repo.GetSpeciesStats(t.Context(), statsSpeciesA)
	// Remove the capture before the EXPLAIN, which goes through the same chain.
	require.NoError(t, db.Callback().Row().Remove(statsCaptureName))
	require.NoError(t, err)
	require.NotEmpty(t, statement, "the aggregate statement was not captured")

	var plan []struct{ Detail string }
	require.NoError(t, db.Raw("EXPLAIN QUERY PLAN "+statement).Scan(&plan).Error, statement)
	detectionSearches, reviewSearches := 0, 0
	for _, row := range plan {
		assert.False(t, strings.HasPrefix(row.Detail, statsScanPrefix), "table scan: %s", row.Detail)
		assert.NotContains(t, row.Detail, statsTempBTree, statement)
		switch {
		case strings.HasPrefix(row.Detail, statsDetectionsTag):
			detectionSearches++
			assert.Contains(t, row.Detail, "USING INDEX", row.Detail)
		case strings.HasPrefix(row.Detail, statsReviewsTag):
			reviewSearches++
			assert.Contains(t, row.Detail, statsReviewsIndex)
		}
	}
	assert.Equal(t, 1, detectionSearches, "one index search on the detections table: %v", plan)
	assert.Equal(t, 1, reviewSearches, "one index search on the reviews table: %v", plan)
}
