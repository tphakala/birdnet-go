package testutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// floatDelta is the tolerance for float comparisons.
const floatDelta = 0.0001

// AssertDetectionMatches verifies that a V2 Detection matches a legacy Note.
// This performs field-by-field comparison with appropriate type conversions.
func AssertDetectionMatches(t *testing.T, legacy *datastore.Note, v2 *entities.Detection) {
	t.Helper()

	require.NotNil(t, legacy, "legacy note should not be nil")
	require.NotNil(t, v2, "v2 detection should not be nil")

	// Core identification
	if v2.LegacyID != nil {
		assert.Equal(t, legacy.ID, *v2.LegacyID, "legacy ID should be preserved")
	}

	// Timestamp comparison (legacy Date+Time -> v2 DetectedAt Unix timestamp)
	// The V2 DetectedAt is Unix seconds, so compare within a reasonable window
	legacyTime, err := time.Parse("2006-01-02 15:04:05", legacy.Date+" "+legacy.Time)
	if err == nil {
		assert.InDelta(t, legacyTime.Unix(), v2.DetectedAt, 1, "detected_at should match legacy date+time")
	}

	// Begin/End times (time.Time -> milliseconds)
	if !legacy.BeginTime.IsZero() {
		require.NotNil(t, v2.BeginTime, "v2 begin_time should not be nil when legacy has value")
		assert.Equal(t, legacy.BeginTime.UnixMilli(), *v2.BeginTime, "begin_time should match")
	}
	if !legacy.EndTime.IsZero() {
		require.NotNil(t, v2.EndTime, "v2 end_time should not be nil when legacy has value")
		assert.Equal(t, legacy.EndTime.UnixMilli(), *v2.EndTime, "end_time should match")
	}

	// Confidence
	assert.InDelta(t, legacy.Confidence, v2.Confidence, floatDelta, "confidence should match")

	// Location (optional)
	if legacy.Latitude != 0 {
		require.NotNil(t, v2.Latitude, "v2 latitude should not be nil when legacy has value")
		assert.InDelta(t, legacy.Latitude, *v2.Latitude, floatDelta, "latitude should match")
	}
	if legacy.Longitude != 0 {
		require.NotNil(t, v2.Longitude, "v2 longitude should not be nil when legacy has value")
		assert.InDelta(t, legacy.Longitude, *v2.Longitude, floatDelta, "longitude should match")
	}

	// Threshold and Sensitivity (optional)
	if legacy.Threshold != 0 {
		require.NotNil(t, v2.Threshold, "v2 threshold should not be nil when legacy has value")
		assert.InDelta(t, legacy.Threshold, *v2.Threshold, floatDelta, "threshold should match")
	}
	if legacy.Sensitivity != 0 {
		require.NotNil(t, v2.Sensitivity, "v2 sensitivity should not be nil when legacy has value")
		assert.InDelta(t, legacy.Sensitivity, *v2.Sensitivity, floatDelta, "sensitivity should match")
	}

	// Clip name (optional)
	if legacy.ClipName != "" {
		require.NotNil(t, v2.ClipName, "v2 clip_name should not be nil when legacy has value")
		assert.Equal(t, legacy.ClipName, *v2.ClipName, "clip_name should match")
	}

	// Processing time (time.Duration -> milliseconds)
	if legacy.ProcessingTime > 0 {
		require.NotNil(t, v2.ProcessingTimeMs, "v2 processing_time_ms should not be nil when legacy has value")
		assert.Equal(t, legacy.ProcessingTime.Milliseconds(), *v2.ProcessingTimeMs, "processing_time should match")
	}
}

// AssertDetectionLabelMatches verifies that V2 Detection's Label matches legacy species.
// Requires the Label relation to be preloaded.
func AssertDetectionLabelMatches(t *testing.T, legacy *datastore.Note, v2 *entities.Detection) {
	t.Helper()

	require.NotNil(t, v2.Label, "v2 detection should have label preloaded")
	require.NotNil(t, v2.Label.ScientificName, "label should have scientific name")
	assert.Equal(t, legacy.ScientificName, *v2.Label.ScientificName, "scientific name should match")
}

// AssertDetectionModelMatches verifies that V2 Detection's Model is set.
// Requires the Model relation to be preloaded.
func AssertDetectionModelMatches(t *testing.T, v2 *entities.Detection) {
	t.Helper()

	require.NotNil(t, v2.Model, "v2 detection should have model preloaded")
	assert.NotEmpty(t, v2.Model.Name, "model should have name")
}

// AssertDetectionSourceMatches verifies that V2 Detection's Source matches legacy.
// Requires the Source relation to be preloaded.
func AssertDetectionSourceMatches(t *testing.T, legacy *datastore.Note, v2 *entities.Detection) {
	t.Helper()

	if legacy.SourceNode != "" {
		require.NotNil(t, v2.Source, "v2 detection should have source when legacy has source_node")
		assert.Equal(t, legacy.SourceNode, v2.Source.NodeName, "source node name should match")
	}
}

// AssertResultsMatch verifies that V2 DetectionPredictions match legacy Results.
func AssertResultsMatch(t *testing.T, legacyResults []datastore.Results, v2Predictions []*entities.DetectionPrediction) {
	t.Helper()

	// Note: legacy Results may have different IDs than V2 predictions.
	// We compare by species and confidence.
	assert.Len(t, v2Predictions, len(legacyResults),
		"prediction count should match results count")

	// Build a map of legacy species -> confidence for comparison
	legacyMap := make(map[string]float32, len(legacyResults))
	for _, r := range legacyResults {
		legacyMap[r.Species] = r.Confidence
	}

	// Verify each V2 prediction has a matching legacy result
	for _, pred := range v2Predictions {
		require.NotNil(t, pred.Label, "prediction should have label preloaded")
		require.NotNil(t, pred.Label.ScientificName, "prediction label should have scientific name")

		scientificName := *pred.Label.ScientificName
		legacyConf, found := legacyMap[scientificName]
		if assert.True(t, found, "v2 prediction species %q should exist in legacy results", scientificName) {
			assert.InDelta(t, float64(legacyConf), pred.Confidence, floatDelta,
				"confidence should match for species %s", scientificName)
		}
	}
}

// AssertReviewMatches verifies that a V2 DetectionReview matches a legacy NoteReview.
func AssertReviewMatches(t *testing.T, legacy *datastore.NoteReview, v2 *entities.DetectionReview) {
	t.Helper()

	require.NotNil(t, legacy, "legacy review should not be nil")
	require.NotNil(t, v2, "v2 review should not be nil")

	// Verified status
	assert.Equal(t, legacy.Verified, string(v2.Verified), "verified status should match")
}

// AssertCommentMatches verifies that a V2 DetectionComment matches a legacy NoteComment.
func AssertCommentMatches(t *testing.T, legacy *datastore.NoteComment, v2 *entities.DetectionComment) {
	t.Helper()

	require.NotNil(t, legacy, "legacy comment should not be nil")
	require.NotNil(t, v2, "v2 comment should not be nil")

	// Entry text
	assert.Equal(t, legacy.Entry, v2.Entry, "comment entry should match")
}

// AssertLockMatches verifies that a V2 DetectionLock matches a legacy NoteLock.
func AssertLockMatches(t *testing.T, legacy *datastore.NoteLock, v2 *entities.DetectionLock) {
	t.Helper()

	require.NotNil(t, legacy, "legacy lock should not be nil")
	require.NotNil(t, v2, "v2 lock should not be nil")

	// Lock timestamp (within a second tolerance due to conversion)
	assert.InDelta(t, legacy.LockedAt.Unix(), v2.LockedAt.Unix(), 1, "locked_at should match within a second")
}

// AssertDailyEventsMatches verifies that a V2 DailyEvents matches a legacy DailyEvents.
func AssertDailyEventsMatches(t *testing.T, legacy *datastore.DailyEvents, v2 *entities.DailyEvents) {
	t.Helper()

	require.NotNil(t, legacy, "legacy daily events should not be nil")
	require.NotNil(t, v2, "v2 daily events should not be nil")

	// Note: IDs may differ due to remapping
	assert.Equal(t, legacy.Date, v2.Date, "date should match")
	assert.Equal(t, legacy.Sunrise, v2.Sunrise, "sunrise should match")
	assert.Equal(t, legacy.Sunset, v2.Sunset, "sunset should match")
	assert.Equal(t, legacy.Country, v2.Country, "country should match")
	assert.Equal(t, legacy.CityName, v2.CityName, "city_name should match")
}

// AssertHourlyWeatherMatches verifies that a V2 HourlyWeather matches a legacy HourlyWeather.
func AssertHourlyWeatherMatches(t *testing.T, legacy *datastore.HourlyWeather, v2 *entities.HourlyWeather) {
	t.Helper()

	require.NotNil(t, legacy, "legacy hourly weather should not be nil")
	require.NotNil(t, v2, "v2 hourly weather should not be nil")

	// Note: IDs and DailyEventsID may differ due to remapping
	assert.Equal(t, legacy.Time.Unix(), v2.Time.Unix(), "time should match")
	assert.InDelta(t, legacy.Temperature, v2.Temperature, floatDelta, "temperature should match")
	assert.InDelta(t, legacy.FeelsLike, v2.FeelsLike, floatDelta, "feels_like should match")
	assert.InDelta(t, legacy.TempMin, v2.TempMin, floatDelta, "temp_min should match")
	assert.InDelta(t, legacy.TempMax, v2.TempMax, floatDelta, "temp_max should match")
	assert.Equal(t, legacy.Pressure, v2.Pressure, "pressure should match")
	assert.Equal(t, legacy.Humidity, v2.Humidity, "humidity should match")
	assert.Equal(t, legacy.Visibility, v2.Visibility, "visibility should match")
	assert.InDelta(t, legacy.WindSpeed, v2.WindSpeed, floatDelta, "wind_speed should match")
	assert.Equal(t, legacy.WindDeg, v2.WindDeg, "wind_deg should match")
	assert.InDelta(t, legacy.WindGust, v2.WindGust, floatDelta, "wind_gust should match")
	assert.Equal(t, legacy.Clouds, v2.Clouds, "clouds should match")
	assert.Equal(t, legacy.WeatherMain, v2.WeatherMain, "weather_main should match")
	assert.Equal(t, legacy.WeatherDesc, v2.WeatherDesc, "weather_desc should match")
	assert.Equal(t, legacy.WeatherIcon, v2.WeatherIcon, "weather_icon should match")
}

// AssertDynamicThresholdMatches verifies that a V2 DynamicThreshold matches a legacy DynamicThreshold.
// The v2 DynamicThreshold should have its Label preloaded for species name comparison.
func AssertDynamicThresholdMatches(t *testing.T, legacy *datastore.DynamicThreshold, v2 *entities.DynamicThreshold) {
	t.Helper()

	require.NotNil(t, legacy, "legacy threshold should not be nil")
	require.NotNil(t, v2, "v2 threshold should not be nil")

	// Compare scientific name via label relationship
	if v2.Label != nil && v2.Label.ScientificName != nil {
		assert.Equal(t, legacy.ScientificName, *v2.Label.ScientificName, "scientific_name should match (via label)")
	} else {
		assert.NotZero(t, v2.LabelID, "label_id should be set if Label not preloaded")
	}
	assert.Equal(t, legacy.Level, v2.Level, "level should match")
	assert.InDelta(t, legacy.CurrentValue, v2.CurrentValue, floatDelta, "current_value should match")
	assert.InDelta(t, legacy.BaseThreshold, v2.BaseThreshold, floatDelta, "base_threshold should match")
	assert.Equal(t, legacy.HighConfCount, v2.HighConfCount, "high_conf_count should match")
	assert.Equal(t, legacy.ValidHours, v2.ValidHours, "valid_hours should match")
	assert.Equal(t, legacy.TriggerCount, v2.TriggerCount, "trigger_count should match")

	// Times may have slight variations, compare Unix timestamps
	assert.InDelta(t, legacy.ExpiresAt.Unix(), v2.ExpiresAt.Unix(), 1, "expires_at should match")
	assert.InDelta(t, legacy.LastTriggered.Unix(), v2.LastTriggered.Unix(), 1, "last_triggered should match")
	assert.InDelta(t, legacy.FirstCreated.Unix(), v2.FirstCreated.Unix(), 1, "first_created should match")
}

// AssertThresholdEventMatches verifies that a V2 ThresholdEvent matches a legacy ThresholdEvent.
// Note: The legacy ThresholdEvent.SpeciesName is the common name (lowercase), while the V2 event
// is linked to a label containing the scientific name. These are different by design - the species
// relationship is verified implicitly through the parent DynamicThreshold migration.
func AssertThresholdEventMatches(t *testing.T, legacy *datastore.ThresholdEvent, v2 *entities.ThresholdEvent) {
	t.Helper()

	require.NotNil(t, legacy, "legacy threshold event should not be nil")
	require.NotNil(t, v2, "v2 threshold event should not be nil")

	// Verify label relationship exists (species matching is verified at parent threshold level)
	assert.NotZero(t, v2.LabelID, "label_id should be set")
	assert.Equal(t, legacy.PreviousLevel, v2.PreviousLevel, "previous_level should match")
	assert.Equal(t, legacy.NewLevel, v2.NewLevel, "new_level should match")
	assert.InDelta(t, legacy.PreviousValue, v2.PreviousValue, floatDelta, "previous_value should match")
	assert.InDelta(t, legacy.NewValue, v2.NewValue, floatDelta, "new_value should match")
	assert.Equal(t, legacy.ChangeReason, v2.ChangeReason, "change_reason should match")
	assert.InDelta(t, legacy.Confidence, v2.Confidence, floatDelta, "confidence should match")
	assert.InDelta(t, legacy.CreatedAt.Unix(), v2.CreatedAt.Unix(), 1, "created_at should match")
}

// AssertImageCacheMatches verifies that a V2 ImageCache matches a legacy ImageCache.
// The v2 ImageCache should have its Label preloaded for scientific name comparison.
func AssertImageCacheMatches(t *testing.T, legacy *datastore.ImageCache, v2 *entities.ImageCache) {
	t.Helper()

	require.NotNil(t, legacy, "legacy image cache should not be nil")
	require.NotNil(t, v2, "v2 image cache should not be nil")

	assert.Equal(t, legacy.ProviderName, v2.ProviderName, "provider_name should match")
	// Compare scientific name via label relationship
	if v2.Label != nil && v2.Label.ScientificName != nil {
		assert.Equal(t, legacy.ScientificName, *v2.Label.ScientificName, "scientific_name should match (via label)")
	} else {
		assert.NotZero(t, v2.LabelID, "label_id should be set if Label not preloaded")
	}
	assert.Equal(t, legacy.SourceProvider, v2.SourceProvider, "source_provider should match")
	assert.Equal(t, legacy.URL, v2.URL, "url should match")
	assert.Equal(t, legacy.LicenseName, v2.LicenseName, "license_name should match")
	assert.Equal(t, legacy.LicenseURL, v2.LicenseURL, "license_url should match")
	assert.Equal(t, legacy.AuthorName, v2.AuthorName, "author_name should match")
	assert.Equal(t, legacy.AuthorURL, v2.AuthorURL, "author_url should match")
	assert.InDelta(t, legacy.CachedAt.Unix(), v2.CachedAt.Unix(), 1, "cached_at should match")
}

// AssertNotificationHistoryMatches verifies that a V2 NotificationHistory matches a legacy NotificationHistory.
// The v2 NotificationHistory should have its Label preloaded for scientific name comparison.
func AssertNotificationHistoryMatches(t *testing.T, legacy *datastore.NotificationHistory, v2 *entities.NotificationHistory) {
	t.Helper()

	require.NotNil(t, legacy, "legacy notification history should not be nil")
	require.NotNil(t, v2, "v2 notification history should not be nil")

	// Compare scientific name via label relationship
	if v2.Label != nil && v2.Label.ScientificName != nil {
		assert.Equal(t, legacy.ScientificName, *v2.Label.ScientificName, "scientific_name should match (via label)")
	} else {
		assert.NotZero(t, v2.LabelID, "label_id should be set if Label not preloaded")
	}
	assert.Equal(t, legacy.NotificationType, v2.NotificationType, "notification_type should match")
	assert.InDelta(t, legacy.LastSent.Unix(), v2.LastSent.Unix(), 1, "last_sent should match")
	assert.InDelta(t, legacy.ExpiresAt.Unix(), v2.ExpiresAt.Unix(), 1, "expires_at should match")
}

// MigrationCounts holds counts for all migrated tables.
type MigrationCounts struct {
	Detections          int
	Predictions         int
	Reviews             int
	Comments            int
	Locks               int
	DailyEvents         int
	HourlyWeather       int
	DynamicThresholds   int
	ThresholdEvents     int
	ImageCaches         int
	NotificationHistory int
}

// AssertMigrationCountsMatch verifies that V2 counts match legacy counts.
//
//nolint:gocritic // hugeParam: pass by value is simpler for test utilities
func AssertMigrationCountsMatch(t *testing.T, legacy, v2 MigrationCounts) {
	t.Helper()

	assert.Equal(t, legacy.Detections, v2.Detections, "detection count should match")
	assert.Equal(t, legacy.Predictions, v2.Predictions, "prediction count should match")
	assert.Equal(t, legacy.Reviews, v2.Reviews, "review count should match")
	assert.Equal(t, legacy.Comments, v2.Comments, "comment count should match")
	assert.Equal(t, legacy.Locks, v2.Locks, "lock count should match")
	assert.Equal(t, legacy.DailyEvents, v2.DailyEvents, "daily_events count should match")
	assert.Equal(t, legacy.HourlyWeather, v2.HourlyWeather, "hourly_weather count should match")
	assert.Equal(t, legacy.DynamicThresholds, v2.DynamicThresholds, "dynamic_threshold count should match")
	assert.Equal(t, legacy.ThresholdEvents, v2.ThresholdEvents, "threshold_event count should match")
	assert.Equal(t, legacy.ImageCaches, v2.ImageCaches, "image_cache count should match")
	assert.Equal(t, legacy.NotificationHistory, v2.NotificationHistory, "notification_history count should match")
}

// AssertAllDataMigrated is a comprehensive check that all legacy data was migrated.
// This combines count verification with sample field verification.
//
//nolint:gocritic // hugeParam: pass by value is simpler for test utilities
func AssertAllDataMigrated(t *testing.T, legacy, v2 MigrationCounts, sampleVerified bool) {
	t.Helper()

	// First verify counts
	AssertMigrationCountsMatch(t, legacy, v2)

	// Ensure at least sample verification was done
	assert.True(t, sampleVerified, "sample field verification should have been performed")
}
