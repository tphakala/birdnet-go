package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

// These tests cover the variadic sourceIDs filter added to the v2 detection-repository
// analytics methods. They verify the contract that:
//
//  1. Omitting sourceIDs returns results aggregated across all audio sources (back-compat).
//  2. Passing one sourceID restricts results to that source.
//  3. Passing multiple sourceIDs unions across the listed sources (IN semantics).
//  4. Passing a sourceID that has no detections returns an empty result rather than erroring.
//
// The fixture seeds two audio sources with distinct species and one shared species so the
// difference between filtered and unfiltered queries is observable in a single assertion.

const (
	srcVoordeurID uint = 11
	srcPoortID    uint = 22
	srcGhostID    uint = 99 // no detections — used to exercise "valid filter, empty result"
	labelRobinID  uint = 1
	labelMerelID  uint = 2
	labelKraaiID  uint = 3
	modelID       uint = 1
)

// setupAnalyticsSourceFixture builds a small DB with detections split across two audio sources.
//
// Layout:
//   - Voordeur (id 11): 3 Robin detections + 1 Merel detection
//   - Poort    (id 22): 2 Merel detections + 1 Kraai detection
//
// All detections are inside the [start, end) window the tests pass (1000 .. 2000).
// detected_at values are spaced so that hourly and daily groupings are exercisable.
func setupAnalyticsSourceFixture(t *testing.T) (db *gorm.DB, repo *detectionRepository) {
	t.Helper()

	db = setupDetectionTestDB(t)

	// Migrate label table too (the detection_impl analytics queries JOIN labels).
	require.NoError(t, db.AutoMigrate(&entities.Label{}, &entities.AudioSource{}))

	// Seed labels.
	require.NoError(t, db.Table(tableLabels).Create(&entities.Label{
		ID: labelRobinID, ScientificName: "Erithacus rubecula", ModelID: modelID, LabelTypeID: 1,
	}).Error)
	require.NoError(t, db.Table(tableLabels).Create(&entities.Label{
		ID: labelMerelID, ScientificName: "Turdus merula", ModelID: modelID, LabelTypeID: 1,
	}).Error)
	require.NoError(t, db.Table(tableLabels).Create(&entities.Label{
		ID: labelKraaiID, ScientificName: "Corvus corone", ModelID: modelID, LabelTypeID: 1,
	}).Error)

	// Seed audio sources.
	voordeurName := "Voordeur"
	poortName := "Poort"
	require.NoError(t, db.Table(tableAudioSources).Create(&entities.AudioSource{
		ID: srcVoordeurID, SourceURI: "rtsp://10.0.0.1/voordeur", NodeName: "host-a",
		SourceType: entities.SourceTypeRTSP, DisplayName: &voordeurName,
	}).Error)
	require.NoError(t, db.Table(tableAudioSources).Create(&entities.AudioSource{
		ID: srcPoortID, SourceURI: "rtsp://10.0.0.1/poort", NodeName: "host-a",
		SourceType: entities.SourceTypeRTSP, DisplayName: &poortName,
	}).Error)

	type seedRow struct {
		labelID    uint
		sourceID   uint
		confidence float64
		detectedAt int64
	}
	seeds := []seedRow{
		{labelRobinID, srcVoordeurID, 0.91, 1100},
		{labelRobinID, srcVoordeurID, 0.95, 1200},
		{labelRobinID, srcVoordeurID, 0.88, 1300},
		{labelMerelID, srcVoordeurID, 0.80, 1400},
		{labelMerelID, srcPoortID, 0.82, 1500},
		{labelMerelID, srcPoortID, 0.79, 1600},
		{labelKraaiID, srcPoortID, 0.75, 1700},
	}
	for _, s := range seeds {
		sid := s.sourceID // copy to allow pointer-to-loop-var-safe assignment
		det := &entities.Detection{
			LabelID:    s.labelID,
			ModelID:    modelID,
			SourceID:   &sid,
			Confidence: s.confidence,
			DetectedAt: s.detectedAt,
		}
		require.NoError(t, db.Table(tableDetections).Create(det).Error)
	}

	repo = &detectionRepository{db: db, useV2Prefix: false}
	return db, repo
}

func TestGetSpeciesSummary_SourceFilter(t *testing.T) {
	// No t.Parallel(): the shared-cache in-memory SQLite used by setupDetectionTestDB collides
	// when multiple tests in this package run concurrently.
	_, repo := setupAnalyticsSourceFixture(t)
	ctx := context.Background()

	// Count rows by scientific name to make assertions readable.
	countsBySpecies := func(rows []SpeciesSummaryData) map[string]int64 {
		m := make(map[string]int64, len(rows))
		for _, r := range rows {
			m[r.ScientificName] = r.TotalDetections
		}
		return m
	}

	t.Run("no filter returns all sources combined", func(t *testing.T) {
		rows, err := repo.GetSpeciesSummary(ctx, 1000, 2000, nil)
		require.NoError(t, err)
		got := countsBySpecies(rows)
		assert.Equal(t, int64(3), got["Erithacus rubecula"], "robin only on Voordeur")
		assert.Equal(t, int64(3), got["Turdus merula"], "merel split across both sources")
		assert.Equal(t, int64(1), got["Corvus corone"], "kraai only on Poort")
	})

	t.Run("single source restricts to that source", func(t *testing.T) {
		rows, err := repo.GetSpeciesSummary(ctx, 1000, 2000, nil, srcVoordeurID)
		require.NoError(t, err)
		got := countsBySpecies(rows)
		assert.Equal(t, int64(3), got["Erithacus rubecula"])
		assert.Equal(t, int64(1), got["Turdus merula"], "only the one Voordeur merel detection")
		_, hasKraai := got["Corvus corone"]
		assert.False(t, hasKraai, "kraai is Poort-only and should be absent")
	})

	t.Run("multiple sources union via IN", func(t *testing.T) {
		// Listing both sources should match the unfiltered case.
		rows, err := repo.GetSpeciesSummary(ctx, 1000, 2000, nil, srcVoordeurID, srcPoortID)
		require.NoError(t, err)
		got := countsBySpecies(rows)
		assert.Equal(t, int64(3), got["Erithacus rubecula"])
		assert.Equal(t, int64(3), got["Turdus merula"])
		assert.Equal(t, int64(1), got["Corvus corone"])
	})

	t.Run("nonexistent source returns empty without error", func(t *testing.T) {
		rows, err := repo.GetSpeciesSummary(ctx, 1000, 2000, nil, srcGhostID)
		require.NoError(t, err)
		assert.Empty(t, rows)
	})
}

func TestGetHourlyDistribution_SourceFilter(t *testing.T) {
	// No t.Parallel(): the shared-cache in-memory SQLite used by setupDetectionTestDB collides
	// when multiple tests in this package run concurrently.
	_, repo := setupAnalyticsSourceFixture(t)
	ctx := context.Background()

	// Without filter, all 7 seeded detections should be counted across hours.
	t.Run("no filter aggregates all sources", func(t *testing.T) {
		rows, err := repo.GetHourlyDistribution(ctx, 1000, 2000, nil, nil)
		require.NoError(t, err)
		var total int64
		for _, r := range rows {
			total += r.Count
		}
		assert.Equal(t, int64(7), total)
	})

	// With a single source filter, only that source's 4 detections appear.
	t.Run("single source restricts count", func(t *testing.T) {
		rows, err := repo.GetHourlyDistribution(ctx, 1000, 2000, nil, nil, srcVoordeurID)
		require.NoError(t, err)
		var total int64
		for _, r := range rows {
			total += r.Count
		}
		assert.Equal(t, int64(4), total, "Voordeur has 3 robin + 1 merel")
	})
}

func TestGetDailyAnalytics_SourceFilter(t *testing.T) {
	// No t.Parallel(): the shared-cache in-memory SQLite used by setupDetectionTestDB collides
	// when multiple tests in this package run concurrently.
	_, repo := setupAnalyticsSourceFixture(t)
	ctx := context.Background()

	all, err := repo.GetDailyAnalytics(ctx, 1000, 2000, nil, nil)
	require.NoError(t, err)
	var allTotal int64
	for _, r := range all {
		allTotal += r.TotalDetections
	}

	filtered, err := repo.GetDailyAnalytics(ctx, 1000, 2000, nil, nil, srcPoortID)
	require.NoError(t, err)
	var filteredTotal int64
	for _, r := range filtered {
		filteredTotal += r.TotalDetections
	}

	assert.Equal(t, int64(7), allTotal)
	assert.Equal(t, int64(3), filteredTotal, "Poort has 2 merel + 1 kraai")
}

// TestGetSpeciesFirstDetectionInPeriod_SourceFilter is a regression test for a SQL bug
// where the source filter was spliced into the inner subquery using a fresh `WHERE` clause
// on top of the existing `WHERE d.detected_at ...` clause, producing invalid SQL. The fix
// is to use the AND-style fragment from buildSourceFilterClauses. This test would have
// caught the bug because it runs the actual query against SQLite.
func TestGetSpeciesFirstDetectionInPeriod_SourceFilter(t *testing.T) {
	_, repo := setupAnalyticsSourceFixture(t)
	ctx := context.Background()

	// Without filter, all three species are first-detected in the window.
	all, err := repo.GetSpeciesFirstDetectionInPeriod(ctx, 1000, 2000, 50, 0)
	require.NoError(t, err)
	assert.Len(t, all, 3, "fixture has 3 species in the window")

	// With Poort filter, only species whose first detection on Poort lies in the window
	// should surface. The merel was first heard on Voordeur (t=1400) and then on Poort
	// (t=1500); when scoped to Poort, t=1500 is its first-detection-on-Poort.
	filtered, err := repo.GetSpeciesFirstDetectionInPeriod(ctx, 1000, 2000, 50, 0, srcPoortID)
	require.NoError(t, err)
	names := make([]string, 0, len(filtered))
	for _, r := range filtered {
		names = append(names, r.ScientificName)
	}
	assert.ElementsMatch(t, []string{"Turdus merula", "Corvus corone"}, names,
		"robin is Voordeur-only and must be excluded; merel and kraai are present on Poort")

	// Multi-source filter exercises the IN-clause expansion in the AND fragment.
	bothSources, err := repo.GetSpeciesFirstDetectionInPeriod(ctx, 1000, 2000, 50, 0, srcVoordeurID, srcPoortID)
	require.NoError(t, err)
	assert.Len(t, bothSources, 3, "listing both sources should match the unfiltered result")

	// Nonexistent source: must not return data or produce a SQL error.
	empty, err := repo.GetSpeciesFirstDetectionInPeriod(ctx, 1000, 2000, 50, 0, srcGhostID)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestGetNewSpecies_SourceFilter verifies that "first ever" semantics are scoped to the
// selected sources when a filter is applied. The merel was detected on Voordeur (t=1400)
// before Poort (t=1500); when filtering by Poort only, its first detection should still
// surface within the queried window.
func TestGetNewSpecies_SourceFilter(t *testing.T) {
	// No t.Parallel(): the shared-cache in-memory SQLite used by setupDetectionTestDB collides
	// when multiple tests in this package run concurrently.
	_, repo := setupAnalyticsSourceFixture(t)
	ctx := context.Background()

	t.Run("unfiltered finds all 3 species as new in the window", func(t *testing.T) {
		rows, err := repo.GetNewSpecies(ctx, 1000, 2000, 50, 0)
		require.NoError(t, err)
		names := make([]string, 0, len(rows))
		for _, r := range rows {
			names = append(names, r.ScientificName)
		}
		assert.ElementsMatch(t, []string{"Erithacus rubecula", "Turdus merula", "Corvus corone"}, names)
	})

	t.Run("filtered by Poort surfaces merel and kraai, not robin", func(t *testing.T) {
		rows, err := repo.GetNewSpecies(ctx, 1000, 2000, 50, 0, srcPoortID)
		require.NoError(t, err)
		names := make([]string, 0, len(rows))
		for _, r := range rows {
			names = append(names, r.ScientificName)
		}
		assert.ElementsMatch(t, []string{"Turdus merula", "Corvus corone"}, names,
			"robin only on Voordeur should be excluded; merel/kraai are first-seen for Poort")
	})
}
