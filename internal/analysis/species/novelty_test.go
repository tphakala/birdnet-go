package species

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func testNoveltyTracker(windowDays int) *SpeciesTracker {
	return NewTrackerFromSettings(nil, &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: windowDays,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	})
}

func TestCheckAndUpdateSpeciesWithNovelty_FirstEverEpisode(t *testing.T) {
	t.Parallel()

	tracker := testNoveltyTracker(7)
	detectionTime := time.Date(2026, 5, 23, 8, 0, 0, 0, time.UTC)

	isNew, daysSinceFirst, novelty := tracker.CheckAndUpdateSpeciesWithNovelty("Setophaga castanea", detectionTime)

	assert.True(t, isNew)
	assert.Equal(t, 0, daysSinceFirst)
	assert.True(t, novelty.NoveltyEpisodeActive)
	assert.Equal(t, inactiveNoveltyValue, novelty.DaysSinceLastSeen)
	assert.Equal(t, firstEverNoveltyEpisodeDays, novelty.NoveltyEpisodeDays)
	assert.Equal(t, detectionTime, novelty.NoveltyEpisodeStart)
}

func TestCheckAndUpdateSpeciesWithNovelty_ReturnAfterAbsenceEpisode(t *testing.T) {
	t.Parallel()

	tracker := testNoveltyTracker(7)
	firstTime := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	returnTime := firstTime.AddDate(0, 0, 12)

	_, _, _ = tracker.CheckAndUpdateSpeciesWithNovelty("Setophaga castanea", firstTime)
	_, daysSinceFirst, novelty := tracker.CheckAndUpdateSpeciesWithNovelty("Setophaga castanea", returnTime)

	assert.Equal(t, 12, daysSinceFirst)
	assert.True(t, novelty.NoveltyEpisodeActive)
	assert.Equal(t, 12, novelty.DaysSinceLastSeen)
	assert.Equal(t, 12, novelty.NoveltyEpisodeDays)
	assert.Equal(t, returnTime, novelty.NoveltyEpisodeStart)
}

func TestCheckAndUpdateSpeciesWithNovelty_EpisodePersistsForWindow(t *testing.T) {
	t.Parallel()

	tracker := testNoveltyTracker(7)
	firstTime := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	returnTime := firstTime.AddDate(0, 0, 12)
	nextDay := returnTime.AddDate(0, 0, 1)

	_, _, _ = tracker.CheckAndUpdateSpeciesWithNovelty("Setophaga castanea", firstTime)
	_, _, _ = tracker.CheckAndUpdateSpeciesWithNovelty("Setophaga castanea", returnTime)
	_, _, novelty := tracker.CheckAndUpdateSpeciesWithNovelty("Setophaga castanea", nextDay)

	assert.True(t, novelty.NoveltyEpisodeActive)
	assert.Equal(t, 1, novelty.DaysSinceLastSeen)
	assert.Equal(t, 12, novelty.NoveltyEpisodeDays)
	assert.Equal(t, returnTime, novelty.NoveltyEpisodeStart)
}

func TestCheckAndUpdateSpeciesWithNovelty_NoEpisodeForSameDayDetection(t *testing.T) {
	t.Parallel()

	tracker := testNoveltyTracker(7)
	detectionTime := time.Date(2026, 5, 23, 8, 0, 0, 0, time.UTC)
	const scientificName = "Setophaga castanea"

	tracker.speciesFirstSeen[scientificName] = detectionTime.AddDate(0, 0, -30)
	tracker.speciesLastSeen[scientificName] = detectionTime

	_, _, novelty := tracker.CheckAndUpdateSpeciesWithNovelty(scientificName, detectionTime.Add(2*time.Hour))

	assert.False(t, novelty.NoveltyEpisodeActive)
	assert.Equal(t, 0, novelty.DaysSinceLastSeen)
	assert.Equal(t, inactiveNoveltyValue, novelty.NoveltyEpisodeDays)
}

func TestLoadNoveltyEpisodesFromDatabase_RestoresActiveEpisode(t *testing.T) {
	t.Parallel()

	const scientificName = "Setophaga castanea"
	now := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
	runStart := trackerDateOnly(now)
	previousDate := runStart.AddDate(0, 0, -12)

	ds := &noveltyHistoryDatastore{
		lifetime: []datastore.NewSpeciesData{
			{
				ScientificName: scientificName,
				CommonName:     "Bay-breasted Warbler",
				FirstSeenDate:  previousDate.Format(time.DateOnly),
				LastSeenDate:   runStart.Format(time.DateOnly),
			},
		},
		detectionDates: []datastore.SpeciesDetectionDate{
			{ScientificName: scientificName, Date: runStart.Format(time.DateOnly)},
		},
		previousDates: map[string]string{
			scientificName + "|" + runStart.Format(time.DateOnly): previousDate.Format(time.DateOnly),
		},
	}

	tracker := testNoveltyTracker(7)
	tracker.ds = ds
	require.NoError(t, tracker.loadLifetimeDataFromDatabase(t.Context(), now))
	require.NoError(t, tracker.loadNoveltyEpisodesFromDatabase(t.Context(), now))

	_, _, novelty := tracker.CheckAndUpdateSpeciesWithNovelty(scientificName, now.Add(2*time.Hour))

	assert.True(t, novelty.NoveltyEpisodeActive)
	assert.Equal(t, 0, novelty.DaysSinceLastSeen)
	assert.Equal(t, 12, novelty.NoveltyEpisodeDays)
	assert.Equal(t, runStart.Format(time.DateOnly), novelty.NoveltyEpisodeStart.Format(time.DateOnly))
}

func TestLoadNoveltyEpisodesFromDatabase(t *testing.T) {
	t.Parallel()

	const scientificName = "Setophaga castanea"
	now := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
	runStart := trackerDateOnly(now)
	previousDate := runStart.AddDate(0, 0, -12)

	tests := []struct {
		name                   string
		ds                     *noveltyHistoryDatastore
		wantDaysSinceLastSeen  int
		wantNoveltyEpisodeDays int
	}{
		{
			// The restored absence gap must match the value the live path records
			// at episode creation (12), not days-since-latest-detection (0).
			name: "restores absence gap",
			ds: &noveltyHistoryDatastore{
				lifetime: []datastore.NewSpeciesData{
					{
						ScientificName: scientificName,
						CommonName:     "Bay-breasted Warbler",
						FirstSeenDate:  previousDate.Format(time.DateOnly),
						LastSeenDate:   runStart.Format(time.DateOnly),
					},
				},
				detectionDates: []datastore.SpeciesDetectionDate{
					{ScientificName: scientificName, Date: runStart.Format(time.DateOnly)},
				},
				previousDates: map[string]string{
					scientificName + "|" + runStart.Format(time.DateOnly): previousDate.Format(time.DateOnly),
				},
			},
			wantDaysSinceLastSeen:  12,
			wantNoveltyEpisodeDays: 12,
		},
		{
			// A first-ever species has no prior sighting, so the restored episode
			// must use the inactive sentinel for DaysSinceLastSeen rather than the
			// multi-decade firstEver sentinel, which the API would surface as a gap.
			name: "first ever has no absence gap",
			ds: &noveltyHistoryDatastore{
				lifetime: []datastore.NewSpeciesData{
					{
						ScientificName: scientificName,
						CommonName:     "Bay-breasted Warbler",
						FirstSeenDate:  runStart.Format(time.DateOnly),
						LastSeenDate:   runStart.Format(time.DateOnly),
					},
				},
				detectionDates: []datastore.SpeciesDetectionDate{
					{ScientificName: scientificName, Date: runStart.Format(time.DateOnly)},
				},
			},
			wantDaysSinceLastSeen:  inactiveNoveltyValue,
			wantNoveltyEpisodeDays: firstEverNoveltyEpisodeDays,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tracker := testNoveltyTracker(7)
			tracker.ds = tt.ds
			require.NoError(t, tracker.loadLifetimeDataFromDatabase(t.Context(), now))
			require.NoError(t, tracker.loadNoveltyEpisodesFromDatabase(t.Context(), now))

			// Inspect the restored episode directly, before any new detection
			// re-runs the live path and overwrites the restored value.
			episode, ok := tracker.noveltyEpisodes[scientificName]
			require.True(t, ok)
			assert.True(t, episode.NoveltyEpisodeActive)
			assert.Equal(t, tt.wantDaysSinceLastSeen, episode.DaysSinceLastSeen)
			assert.Equal(t, tt.wantNoveltyEpisodeDays, episode.NoveltyEpisodeDays)
		})
	}
}

type noveltyHistoryDatastore struct {
	lifetime       []datastore.NewSpeciesData
	detectionDates []datastore.SpeciesDetectionDate
	previousDates  map[string]string
}

func (d *noveltyHistoryDatastore) GetNewSpeciesDetections(context.Context, string, string, int, int) ([]datastore.NewSpeciesData, error) {
	return d.lifetime, nil
}

func (d *noveltyHistoryDatastore) GetSpeciesFirstDetectionInPeriod(context.Context, string, string, int, int) ([]datastore.NewSpeciesData, error) {
	return nil, nil
}

func (d *noveltyHistoryDatastore) GetActiveNotificationHistory(time.Time) ([]datastore.NotificationHistory, error) {
	return nil, nil
}

func (d *noveltyHistoryDatastore) SaveNotificationHistory(*datastore.NotificationHistory) error {
	return nil
}

func (d *noveltyHistoryDatastore) DeleteExpiredNotificationHistory(time.Time) (int64, error) {
	return 0, nil
}

func (d *noveltyHistoryDatastore) GetSpeciesDetectionDatesInPeriod(context.Context, string, string, int, int) ([]datastore.SpeciesDetectionDate, error) {
	return d.detectionDates, nil
}

func (d *noveltyHistoryDatastore) GetSpeciesLastDetectionDateBefore(_ context.Context, scientificName, beforeDate string) (string, error) {
	return d.previousDates[scientificName+"|"+beforeDate], nil
}
