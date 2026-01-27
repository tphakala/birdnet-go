package testutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectionBuilder_Defaults(t *testing.T) {
	t.Parallel()

	note := NewDetectionBuilder().Build()

	assert.Equal(t, uint(1), note.ID)
	assert.NotEmpty(t, note.SourceNode)
	assert.NotEmpty(t, note.Date)
	assert.NotEmpty(t, note.Time)
	assert.NotEmpty(t, note.ScientificName)
	assert.NotEmpty(t, note.CommonName)
	assert.NotZero(t, note.Confidence)
	assert.NotZero(t, note.Latitude)
	assert.NotZero(t, note.Longitude)
}

func TestDetectionBuilder_Chainable(t *testing.T) {
	t.Parallel()

	testTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

	note := NewDetectionBuilder().
		WithID(42).
		WithSourceNode("custom-node").
		WithTimestamp(testTime).
		WithSpecies("blujay", "Cyanocitta cristata", "Blue Jay").
		WithConfidence(0.95).
		WithLocation(40.7128, -74.0060).
		WithThreshold(0.70).
		WithSensitivity(1.25).
		WithClipName("custom_clip.mp3").
		WithProcessingTime(200 * time.Millisecond).
		Build()

	assert.Equal(t, uint(42), note.ID)
	assert.Equal(t, "custom-node", note.SourceNode)
	assert.Equal(t, "2024-06-15", note.Date)
	assert.Equal(t, "10:30:00", note.Time)
	assert.Equal(t, testTime, note.BeginTime)
	assert.Equal(t, "blujay", note.SpeciesCode)
	assert.Equal(t, "Cyanocitta cristata", note.ScientificName)
	assert.Equal(t, "Blue Jay", note.CommonName)
	assert.InDelta(t, 0.95, note.Confidence, 0.001)
	assert.InDelta(t, 40.7128, note.Latitude, 0.0001)
	assert.InDelta(t, -74.0060, note.Longitude, 0.0001)
	assert.InDelta(t, 0.70, note.Threshold, 0.001)
	assert.InDelta(t, 1.25, note.Sensitivity, 0.001)
	assert.Equal(t, "custom_clip.mp3", note.ClipName)
	assert.Equal(t, 200*time.Millisecond, note.ProcessingTime)
}

func TestDetectionBuilder_MinimalData(t *testing.T) {
	t.Parallel()

	note := NewDetectionBuilder().
		WithMinimalData().
		Build()

	assert.Zero(t, note.Latitude)
	assert.Zero(t, note.Longitude)
	assert.Zero(t, note.Threshold)
	assert.Zero(t, note.Sensitivity)
	assert.Empty(t, note.ClipName)
	assert.Zero(t, note.ProcessingTime)
}

func TestDetectionBuilder_BuildPtr(t *testing.T) {
	t.Parallel()

	notePtr := NewDetectionBuilder().WithID(99).BuildPtr()

	require.NotNil(t, notePtr)
	assert.Equal(t, uint(99), notePtr.ID)
}

func TestResultsBuilder_Defaults(t *testing.T) {
	t.Parallel()

	result := NewResultsBuilder().Build()

	assert.Equal(t, uint(1), result.ID)
	assert.Equal(t, uint(1), result.NoteID)
	assert.NotEmpty(t, result.Species)
	assert.NotZero(t, result.Confidence)
}

func TestResultsBuilder_Chainable(t *testing.T) {
	t.Parallel()

	result := NewResultsBuilder().
		WithID(5).
		WithNoteID(10).
		WithSpecies("Passer domesticus").
		WithConfidence(0.82).
		Build()

	assert.Equal(t, uint(5), result.ID)
	assert.Equal(t, uint(10), result.NoteID)
	assert.Equal(t, "Passer domesticus", result.Species)
	assert.InDelta(t, float32(0.82), result.Confidence, 0.001)
}

func TestReviewBuilder_Defaults(t *testing.T) {
	t.Parallel()

	review := NewReviewBuilder().Build()

	assert.Equal(t, uint(1), review.ID)
	assert.Equal(t, uint(1), review.NoteID)
	assert.Equal(t, "correct", review.Verified)
	assert.False(t, review.CreatedAt.IsZero())
}

func TestReviewBuilder_StatusMethods(t *testing.T) {
	t.Parallel()

	correctReview := NewReviewBuilder().AsCorrect().Build()
	assert.Equal(t, "correct", correctReview.Verified)

	fpReview := NewReviewBuilder().AsFalsePositive().Build()
	assert.Equal(t, "false_positive", fpReview.Verified)
}

func TestCommentBuilder_Defaults(t *testing.T) {
	t.Parallel()

	comment := NewCommentBuilder().Build()

	assert.Equal(t, uint(1), comment.ID)
	assert.Equal(t, uint(1), comment.NoteID)
	assert.NotEmpty(t, comment.Entry)
	assert.False(t, comment.CreatedAt.IsZero())
}

func TestCommentBuilder_Chainable(t *testing.T) {
	t.Parallel()

	comment := NewCommentBuilder().
		WithID(7).
		WithNoteID(15).
		WithEntry("This is a test comment").
		Build()

	assert.Equal(t, uint(7), comment.ID)
	assert.Equal(t, uint(15), comment.NoteID)
	assert.Equal(t, "This is a test comment", comment.Entry)
}

func TestLockBuilder_Defaults(t *testing.T) {
	t.Parallel()

	lock := NewLockBuilder().Build()

	assert.Equal(t, uint(1), lock.ID)
	assert.Equal(t, uint(1), lock.NoteID)
	assert.False(t, lock.LockedAt.IsZero())
}

func TestLockBuilder_Chainable(t *testing.T) {
	t.Parallel()

	lockTime := time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)

	lock := NewLockBuilder().
		WithID(3).
		WithNoteID(20).
		WithLockedAt(lockTime).
		Build()

	assert.Equal(t, uint(3), lock.ID)
	assert.Equal(t, uint(20), lock.NoteID)
	assert.Equal(t, lockTime, lock.LockedAt)
}

func TestDailyEventsBuilder_Defaults(t *testing.T) {
	t.Parallel()

	event := NewDailyEventsBuilder().Build()

	assert.Equal(t, uint(1), event.ID)
	assert.NotEmpty(t, event.Date)
	assert.NotZero(t, event.Sunrise)
	assert.NotZero(t, event.Sunset)
	assert.NotEmpty(t, event.Country)
	assert.NotEmpty(t, event.CityName)
}

func TestDailyEventsBuilder_Chainable(t *testing.T) {
	t.Parallel()

	event := NewDailyEventsBuilder().
		WithID(5).
		WithDate("2024-03-20").
		WithSunrise(1710918000).
		WithSunset(1710961200).
		WithCountry("FI").
		WithCityName("Helsinki").
		Build()

	assert.Equal(t, uint(5), event.ID)
	assert.Equal(t, "2024-03-20", event.Date)
	assert.Equal(t, int64(1710918000), event.Sunrise)
	assert.Equal(t, int64(1710961200), event.Sunset)
	assert.Equal(t, "FI", event.Country)
	assert.Equal(t, "Helsinki", event.CityName)
}

func TestHourlyWeatherBuilder_Defaults(t *testing.T) {
	t.Parallel()

	weather := NewHourlyWeatherBuilder().Build()

	assert.Equal(t, uint(1), weather.ID)
	assert.Equal(t, uint(1), weather.DailyEventsID)
	assert.NotZero(t, weather.Temperature)
	assert.NotZero(t, weather.Pressure)
	assert.NotZero(t, weather.Humidity)
	assert.NotEmpty(t, weather.WeatherMain)
}

func TestHourlyWeatherBuilder_AllFields(t *testing.T) {
	t.Parallel()

	testTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	weather := NewHourlyWeatherBuilder().
		WithID(24).
		WithDailyEventsID(5).
		WithTime(testTime).
		WithTemperature(25.5).
		WithFeelsLike(24.0).
		WithTempMinMax(20.0, 28.0).
		WithPressure(1015).
		WithHumidity(55).
		WithVisibility(15000).
		WithWind(10.0, 270, 15.0).
		WithClouds(30).
		WithWeather("Clear", "clear sky", "01d").
		Build()

	assert.Equal(t, uint(24), weather.ID)
	assert.Equal(t, uint(5), weather.DailyEventsID)
	assert.Equal(t, testTime, weather.Time)
	assert.InDelta(t, 25.5, weather.Temperature, 0.01)
	assert.InDelta(t, 24.0, weather.FeelsLike, 0.01)
	assert.InDelta(t, 20.0, weather.TempMin, 0.01)
	assert.InDelta(t, 28.0, weather.TempMax, 0.01)
	assert.Equal(t, 1015, weather.Pressure)
	assert.Equal(t, 55, weather.Humidity)
	assert.Equal(t, 15000, weather.Visibility)
	assert.InDelta(t, 10.0, weather.WindSpeed, 0.01)
	assert.Equal(t, 270, weather.WindDeg)
	assert.InDelta(t, 15.0, weather.WindGust, 0.01)
	assert.Equal(t, 30, weather.Clouds)
	assert.Equal(t, "Clear", weather.WeatherMain)
	assert.Equal(t, "clear sky", weather.WeatherDesc)
	assert.Equal(t, "01d", weather.WeatherIcon)
}

func TestDynamicThresholdBuilder_Defaults(t *testing.T) {
	t.Parallel()

	threshold := NewDynamicThresholdBuilder().Build()

	assert.Equal(t, uint(1), threshold.ID)
	assert.NotEmpty(t, threshold.SpeciesName)
	assert.NotEmpty(t, threshold.ScientificName)
	assert.NotZero(t, threshold.Level)
	assert.NotZero(t, threshold.CurrentValue)
	assert.NotZero(t, threshold.BaseThreshold)
}

func TestDynamicThresholdBuilder_Chainable(t *testing.T) {
	t.Parallel()

	now := time.Now()
	expiresAt := now.Add(48 * time.Hour)

	threshold := NewDynamicThresholdBuilder().
		WithID(10).
		WithSpeciesName("blue jay").
		WithScientificName("Cyanocitta cristata").
		WithLevel(2).
		WithCurrentValue(0.80).
		WithBaseThreshold(0.65).
		WithHighConfCount(15).
		WithValidHours(48).
		WithExpiresAt(expiresAt).
		WithLastTriggered(now).
		WithTriggerCount(25).
		Build()

	assert.Equal(t, uint(10), threshold.ID)
	assert.Equal(t, "blue jay", threshold.SpeciesName)
	assert.Equal(t, "Cyanocitta cristata", threshold.ScientificName)
	assert.Equal(t, 2, threshold.Level)
	assert.InDelta(t, 0.80, threshold.CurrentValue, 0.001)
	assert.InDelta(t, 0.65, threshold.BaseThreshold, 0.001)
	assert.Equal(t, 15, threshold.HighConfCount)
	assert.Equal(t, 48, threshold.ValidHours)
	assert.Equal(t, expiresAt.Unix(), threshold.ExpiresAt.Unix())
	assert.Equal(t, 25, threshold.TriggerCount)
}

func TestThresholdEventBuilder_Defaults(t *testing.T) {
	t.Parallel()

	event := NewThresholdEventBuilder().Build()

	assert.Equal(t, uint(1), event.ID)
	assert.NotEmpty(t, event.SpeciesName)
	assert.NotEmpty(t, event.ChangeReason)
	assert.False(t, event.CreatedAt.IsZero())
}

func TestThresholdEventBuilder_Chainable(t *testing.T) {
	t.Parallel()

	event := NewThresholdEventBuilder().
		WithID(5).
		WithSpeciesName("house sparrow").
		WithLevelChange(1, 2).
		WithValueChange(0.70, 0.80).
		WithChangeReason("expiry").
		WithConfidence(0.88).
		Build()

	assert.Equal(t, uint(5), event.ID)
	assert.Equal(t, "house sparrow", event.SpeciesName)
	assert.Equal(t, 1, event.PreviousLevel)
	assert.Equal(t, 2, event.NewLevel)
	assert.InDelta(t, 0.70, event.PreviousValue, 0.001)
	assert.InDelta(t, 0.80, event.NewValue, 0.001)
	assert.Equal(t, "expiry", event.ChangeReason)
	assert.InDelta(t, 0.88, event.Confidence, 0.001)
}

func TestImageCacheBuilder_Defaults(t *testing.T) {
	t.Parallel()

	cache := NewImageCacheBuilder().Build()

	assert.Equal(t, uint(1), cache.ID)
	assert.Equal(t, "wikimedia", cache.ProviderName)
	assert.NotEmpty(t, cache.ScientificName)
	assert.NotEmpty(t, cache.URL)
	assert.NotEmpty(t, cache.LicenseName)
}

func TestImageCacheBuilder_Chainable(t *testing.T) {
	t.Parallel()

	cache := NewImageCacheBuilder().
		WithID(8).
		WithProviderName("flickr").
		WithScientificName("Passer domesticus").
		WithSourceProvider("flickr").
		WithURL("https://flickr.com/photo.jpg").
		WithLicense("CC BY 2.0", "https://creativecommons.org/licenses/by/2.0/").
		WithAuthor("John Doe", "https://flickr.com/johndoe").
		Build()

	assert.Equal(t, uint(8), cache.ID)
	assert.Equal(t, "flickr", cache.ProviderName)
	assert.Equal(t, "Passer domesticus", cache.ScientificName)
	assert.Equal(t, "flickr", cache.SourceProvider)
	assert.Equal(t, "https://flickr.com/photo.jpg", cache.URL)
	assert.Equal(t, "CC BY 2.0", cache.LicenseName)
	assert.Equal(t, "https://creativecommons.org/licenses/by/2.0/", cache.LicenseURL)
	assert.Equal(t, "John Doe", cache.AuthorName)
	assert.Equal(t, "https://flickr.com/johndoe", cache.AuthorURL)
}

func TestNotificationHistoryBuilder_Defaults(t *testing.T) {
	t.Parallel()

	history := NewNotificationHistoryBuilder().Build()

	assert.Equal(t, uint(1), history.ID)
	assert.NotEmpty(t, history.ScientificName)
	assert.Equal(t, "new_species", history.NotificationType)
	assert.False(t, history.LastSent.IsZero())
	assert.False(t, history.ExpiresAt.IsZero())
}

func TestNotificationHistoryBuilder_Chainable(t *testing.T) {
	t.Parallel()

	now := time.Now()
	expiresAt := now.Add(12 * time.Hour)

	history := NewNotificationHistoryBuilder().
		WithID(3).
		WithScientificName("Corvus brachyrhynchos").
		WithNotificationType("yearly").
		WithLastSent(now).
		WithExpiresAt(expiresAt).
		Build()

	assert.Equal(t, uint(3), history.ID)
	assert.Equal(t, "Corvus brachyrhynchos", history.ScientificName)
	assert.Equal(t, "yearly", history.NotificationType)
	assert.Equal(t, now.Unix(), history.LastSent.Unix())
	assert.Equal(t, expiresAt.Unix(), history.ExpiresAt.Unix())
}

func TestGenerateDetections(t *testing.T) {
	t.Parallel()

	notes := GenerateDetections(25)

	require.Len(t, notes, 25)

	// Check IDs are sequential
	for i, note := range notes {
		assert.Equal(t, uint(i+1), note.ID)
	}

	// Check species variety (we have 10 test species, so with 25 notes we should see repeats)
	speciesSet := make(map[string]bool)
	for _, note := range notes {
		speciesSet[note.ScientificName] = true
	}
	assert.GreaterOrEqual(t, len(speciesSet), 5, "should have variety in species")

	// Check confidence variation
	var minConf, maxConf float64 = 1.0, 0.0
	for _, note := range notes {
		if note.Confidence < minConf {
			minConf = note.Confidence
		}
		if note.Confidence > maxConf {
			maxConf = note.Confidence
		}
	}
	assert.Less(t, minConf, maxConf, "confidence should vary")
}

func TestGenerateWeatherData(t *testing.T) {
	t.Parallel()

	dailyEvents, hourlyWeather := GenerateWeatherData(3)

	require.Len(t, dailyEvents, 3)
	require.Len(t, hourlyWeather, 3*24) // 24 hours per day

	// Check daily events have sequential IDs
	for i, event := range dailyEvents {
		assert.Equal(t, uint(i+1), event.ID)
	}

	// Check hourly weather references correct daily events
	for _, hw := range hourlyWeather {
		assert.LessOrEqual(t, hw.DailyEventsID, uint(3))
		assert.GreaterOrEqual(t, hw.DailyEventsID, uint(1))
	}

	// Count hourly records per day
	countsPerDay := make(map[uint]int)
	for _, hw := range hourlyWeather {
		countsPerDay[hw.DailyEventsID]++
	}
	for dayID := uint(1); dayID <= 3; dayID++ {
		assert.Equal(t, 24, countsPerDay[dayID], "each day should have 24 hourly records")
	}
}

func TestTestSpecies_Variety(t *testing.T) {
	t.Parallel()

	assert.GreaterOrEqual(t, len(TestSpecies), 5, "should have at least 5 test species")

	// Check all species have required fields
	for _, species := range TestSpecies {
		assert.NotEmpty(t, species.Code, "species code should not be empty")
		assert.NotEmpty(t, species.Scientific, "scientific name should not be empty")
		assert.NotEmpty(t, species.Common, "common name should not be empty")
	}
}
