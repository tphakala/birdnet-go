package testutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

func TestAssertDetectionMatches_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2024, 6, 15, 10, 30, 45, 0, time.UTC)

	legacy := &datastore.Note{
		ID:             42,
		Date:           "2024-06-15",
		Time:           "10:30:45",
		BeginTime:      now,
		EndTime:        now.Add(3 * time.Second),
		ScientificName: "Turdus migratorius",
		Confidence:     0.85,
		Latitude:       42.3601,
		Longitude:      -71.0589,
		Threshold:      0.65,
		Sensitivity:    1.0,
		ClipName:       "test_clip.wav",
		ProcessingTime: 150 * time.Millisecond,
	}

	legacyID := uint(42)
	beginTime := now.UnixMilli()
	endTime := now.Add(3 * time.Second).UnixMilli()
	lat := 42.3601
	lon := -71.0589
	threshold := 0.65
	sensitivity := 1.0
	clipName := "test_clip.wav"
	processingTimeMs := int64(150)

	v2 := &entities.Detection{
		ID:               1, // V2 ID can differ
		LegacyID:         &legacyID,
		DetectedAt:       now.Unix(),
		BeginTime:        &beginTime,
		EndTime:          &endTime,
		Confidence:       0.85,
		Latitude:         &lat,
		Longitude:        &lon,
		Threshold:        &threshold,
		Sensitivity:      &sensitivity,
		ClipName:         &clipName,
		ProcessingTimeMs: &processingTimeMs,
	}

	// Should not panic or fail
	AssertDetectionMatches(t, legacy, v2)
}

func TestAssertDetectionMatches_MinimalFields(t *testing.T) {
	t.Parallel()

	now := time.Date(2024, 6, 15, 10, 30, 45, 0, time.UTC)

	// Legacy with only required fields
	legacy := &datastore.Note{
		ID:             1,
		Date:           "2024-06-15",
		Time:           "10:30:45",
		ScientificName: "Turdus migratorius",
		Confidence:     0.85,
		// All optional fields are zero/empty
	}

	v2 := &entities.Detection{
		ID:         1,
		DetectedAt: now.Unix(),
		Confidence: 0.85,
		// All optional fields are nil
	}

	// Should not panic or fail
	AssertDetectionMatches(t, legacy, v2)
}

func TestAssertDetectionLabelMatches_Success(t *testing.T) {
	t.Parallel()

	legacy := &datastore.Note{
		ScientificName: "Turdus migratorius",
	}

	scientificName := "Turdus migratorius"
	v2 := &entities.Detection{
		Label: &entities.Label{
			ScientificName: &scientificName,
		},
	}

	AssertDetectionLabelMatches(t, legacy, v2)
}

func TestAssertReviewMatches_Correct(t *testing.T) {
	t.Parallel()

	legacy := &datastore.NoteReview{
		ID:       1,
		NoteID:   1,
		Verified: "correct",
	}

	v2 := &entities.DetectionReview{
		ID:          1,
		DetectionID: 1,
		Verified:    entities.VerificationCorrect,
	}

	AssertReviewMatches(t, legacy, v2)
}

func TestAssertReviewMatches_FalsePositive(t *testing.T) {
	t.Parallel()

	legacy := &datastore.NoteReview{
		ID:       1,
		NoteID:   1,
		Verified: "false_positive",
	}

	v2 := &entities.DetectionReview{
		ID:          1,
		DetectionID: 1,
		Verified:    entities.VerificationFalsePositive,
	}

	AssertReviewMatches(t, legacy, v2)
}

func TestAssertCommentMatches_Success(t *testing.T) {
	t.Parallel()

	legacy := &datastore.NoteComment{
		ID:     1,
		NoteID: 1,
		Entry:  "This is a test comment",
	}

	v2 := &entities.DetectionComment{
		ID:          1,
		DetectionID: 1,
		Entry:       "This is a test comment",
	}

	AssertCommentMatches(t, legacy, v2)
}

func TestAssertLockMatches_Success(t *testing.T) {
	t.Parallel()

	lockTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	legacy := &datastore.NoteLock{
		ID:       1,
		NoteID:   1,
		LockedAt: lockTime,
	}

	v2 := &entities.DetectionLock{
		ID:          1,
		DetectionID: 1,
		LockedAt:    lockTime,
	}

	AssertLockMatches(t, legacy, v2)
}

func TestAssertDailyEventsMatches_Success(t *testing.T) {
	t.Parallel()

	legacy := &datastore.DailyEvents{
		ID:       1,
		Date:     "2024-06-15",
		Sunrise:  1718431200,
		Sunset:   1718484000,
		Country:  "US",
		CityName: "Boston",
	}

	v2 := &entities.DailyEvents{
		ID:       100, // V2 ID can differ
		Date:     "2024-06-15",
		Sunrise:  1718431200,
		Sunset:   1718484000,
		Country:  "US",
		CityName: "Boston",
	}

	AssertDailyEventsMatches(t, legacy, v2)
}

func TestAssertHourlyWeatherMatches_Success(t *testing.T) {
	t.Parallel()

	weatherTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	legacy := &datastore.HourlyWeather{
		ID:            1,
		DailyEventsID: 1,
		Time:          weatherTime,
		Temperature:   25.5,
		FeelsLike:     24.0,
		TempMin:       20.0,
		TempMax:       28.0,
		Pressure:      1015,
		Humidity:      55,
		Visibility:    10000,
		WindSpeed:     10.5,
		WindDeg:       270,
		WindGust:      15.0,
		Clouds:        30,
		WeatherMain:   "Clear",
		WeatherDesc:   "clear sky",
		WeatherIcon:   "01d",
	}

	v2 := &entities.HourlyWeather{
		ID:            100, // V2 ID can differ
		DailyEventsID: 50,  // DailyEventsID can differ due to remapping
		Time:          weatherTime,
		Temperature:   25.5,
		FeelsLike:     24.0,
		TempMin:       20.0,
		TempMax:       28.0,
		Pressure:      1015,
		Humidity:      55,
		Visibility:    10000,
		WindSpeed:     10.5,
		WindDeg:       270,
		WindGust:      15.0,
		Clouds:        30,
		WeatherMain:   "Clear",
		WeatherDesc:   "clear sky",
		WeatherIcon:   "01d",
	}

	AssertHourlyWeatherMatches(t, legacy, v2)
}

func TestAssertDynamicThresholdMatches_Success(t *testing.T) {
	t.Parallel()

	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)

	legacy := &datastore.DynamicThreshold{
		ID:             1,
		SpeciesName:    "american robin",
		ScientificName: "Turdus migratorius",
		Level:          1,
		CurrentValue:   0.75,
		BaseThreshold:  0.65,
		HighConfCount:  5,
		ValidHours:     24,
		ExpiresAt:      expiresAt,
		LastTriggered:  now,
		FirstCreated:   now.Add(-7 * 24 * time.Hour),
		TriggerCount:   10,
	}

	v2 := &entities.DynamicThreshold{
		ID:             100, // V2 ID can differ
		SpeciesName:    "american robin",
		ScientificName: "Turdus migratorius",
		Level:          1,
		CurrentValue:   0.75,
		BaseThreshold:  0.65,
		HighConfCount:  5,
		ValidHours:     24,
		ExpiresAt:      expiresAt,
		LastTriggered:  now,
		FirstCreated:   now.Add(-7 * 24 * time.Hour),
		TriggerCount:   10,
	}

	AssertDynamicThresholdMatches(t, legacy, v2)
}

func TestAssertImageCacheMatches_Success(t *testing.T) {
	t.Parallel()

	cachedAt := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	legacy := &datastore.ImageCache{
		ID:             1,
		ProviderName:   "wikimedia",
		ScientificName: "Turdus migratorius",
		SourceProvider: "wikimedia",
		URL:            "https://example.com/image.jpg",
		LicenseName:    "CC BY-SA 4.0",
		LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
		AuthorName:     "Test Author",
		AuthorURL:      "https://example.com/author",
		CachedAt:       cachedAt,
	}

	v2 := &entities.ImageCache{
		ID:             100, // V2 ID can differ
		ProviderName:   "wikimedia",
		ScientificName: "Turdus migratorius",
		SourceProvider: "wikimedia",
		URL:            "https://example.com/image.jpg",
		LicenseName:    "CC BY-SA 4.0",
		LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
		AuthorName:     "Test Author",
		AuthorURL:      "https://example.com/author",
		CachedAt:       cachedAt,
	}

	AssertImageCacheMatches(t, legacy, v2)
}

func TestAssertNotificationHistoryMatches_Success(t *testing.T) {
	t.Parallel()

	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)

	legacy := &datastore.NotificationHistory{
		ID:               1,
		ScientificName:   "Turdus migratorius",
		NotificationType: "new_species",
		LastSent:         now,
		ExpiresAt:        expiresAt,
	}

	v2 := &entities.NotificationHistory{
		ID:               100, // V2 ID can differ
		ScientificName:   "Turdus migratorius",
		NotificationType: "new_species",
		LastSent:         now,
		ExpiresAt:        expiresAt,
	}

	AssertNotificationHistoryMatches(t, legacy, v2)
}

func TestAssertMigrationCountsMatch_Success(t *testing.T) {
	t.Parallel()

	legacy := MigrationCounts{
		Detections:          100,
		Predictions:         200,
		Reviews:             50,
		Comments:            30,
		Locks:               10,
		DailyEvents:         7,
		HourlyWeather:       168,
		DynamicThresholds:   5,
		ThresholdEvents:     15,
		ImageCaches:         10,
		NotificationHistory: 5,
	}

	v2 := MigrationCounts{
		Detections:          100,
		Predictions:         200,
		Reviews:             50,
		Comments:            30,
		Locks:               10,
		DailyEvents:         7,
		HourlyWeather:       168,
		DynamicThresholds:   5,
		ThresholdEvents:     15,
		ImageCaches:         10,
		NotificationHistory: 5,
	}

	AssertMigrationCountsMatch(t, legacy, v2)
}

func TestAssertResultsMatch_Success(t *testing.T) {
	t.Parallel()

	legacyResults := []datastore.Results{
		{ID: 1, NoteID: 1, Species: "Corvus brachyrhynchos", Confidence: 0.7},
		{ID: 2, NoteID: 1, Species: "Passer domesticus", Confidence: 0.5},
	}

	crow := "Corvus brachyrhynchos"
	sparrow := "Passer domesticus"

	v2Predictions := []*entities.DetectionPrediction{
		{ID: 1, DetectionID: 1, LabelID: 1, Confidence: 0.7, Label: &entities.Label{ScientificName: &crow}},
		{ID: 2, DetectionID: 1, LabelID: 2, Confidence: 0.5, Label: &entities.Label{ScientificName: &sparrow}},
	}

	AssertResultsMatch(t, legacyResults, v2Predictions)
}

func TestAssertAllDataMigrated_Success(t *testing.T) {
	t.Parallel()

	legacy := MigrationCounts{
		Detections:    100,
		Predictions:   200,
		Reviews:       50,
		Comments:      30,
		Locks:         10,
		DailyEvents:   7,
		HourlyWeather: 168,
	}

	v2 := MigrationCounts{
		Detections:    100,
		Predictions:   200,
		Reviews:       50,
		Comments:      30,
		Locks:         10,
		DailyEvents:   7,
		HourlyWeather: 168,
	}

	AssertAllDataMigrated(t, legacy, v2, true)
}

// Helper tests to ensure assertion failures are caught
func TestAssertDetectionMatches_ConfidenceMismatch(t *testing.T) {
	t.Parallel()

	mockT := &testing.T{}

	legacy := &datastore.Note{
		ID:         1,
		Date:       "2024-06-15",
		Time:       "10:30:45",
		Confidence: 0.85,
	}

	v2 := &entities.Detection{
		ID:         1,
		DetectedAt: time.Date(2024, 6, 15, 10, 30, 45, 0, time.UTC).Unix(),
		Confidence: 0.50, // Mismatch!
	}

	AssertDetectionMatches(mockT, legacy, v2)
	assert.True(t, mockT.Failed(), "should fail on confidence mismatch")
}
