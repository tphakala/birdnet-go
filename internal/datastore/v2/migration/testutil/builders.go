package testutil

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// DetectionBuilder provides a fluent API for building test Note (legacy detection) data.
type DetectionBuilder struct {
	note datastore.Note
}

// NewDetectionBuilder creates a new DetectionBuilder with sensible defaults.
func NewDetectionBuilder() *DetectionBuilder {
	now := time.Now()
	return &DetectionBuilder{
		note: datastore.Note{
			ID:             1,
			SourceNode:     "test-node",
			Date:           now.Format("2006-01-02"),
			Time:           now.Format("15:04:05"),
			BeginTime:      now,
			EndTime:        now.Add(3 * time.Second),
			SpeciesCode:    "turmig",
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			Confidence:     0.85,
			Latitude:       42.3601,
			Longitude:      -71.0589,
			Threshold:      0.65,
			Sensitivity:    1.0,
			ClipName:       "test_clip.wav",
			ProcessingTime: 150 * time.Millisecond,
		},
	}
}

// WithID sets the ID.
func (b *DetectionBuilder) WithID(id uint) *DetectionBuilder {
	b.note.ID = id
	return b
}

// WithSourceNode sets the source node.
func (b *DetectionBuilder) WithSourceNode(node string) *DetectionBuilder {
	b.note.SourceNode = node
	return b
}

// WithDate sets the date (YYYY-MM-DD format).
func (b *DetectionBuilder) WithDate(date string) *DetectionBuilder {
	b.note.Date = date
	return b
}

// WithTime sets the time (HH:MM:SS format).
func (b *DetectionBuilder) WithTime(t string) *DetectionBuilder {
	b.note.Time = t
	return b
}

// WithTimestamp sets both date and time from a time.Time value.
func (b *DetectionBuilder) WithTimestamp(ts time.Time) *DetectionBuilder {
	b.note.Date = ts.Format("2006-01-02")
	b.note.Time = ts.Format("15:04:05")
	b.note.BeginTime = ts
	b.note.EndTime = ts.Add(3 * time.Second)
	return b
}

// WithBeginTime sets the begin time.
func (b *DetectionBuilder) WithBeginTime(t time.Time) *DetectionBuilder {
	b.note.BeginTime = t
	return b
}

// WithEndTime sets the end time.
func (b *DetectionBuilder) WithEndTime(t time.Time) *DetectionBuilder {
	b.note.EndTime = t
	return b
}

// WithSpeciesCode sets the species code.
func (b *DetectionBuilder) WithSpeciesCode(code string) *DetectionBuilder {
	b.note.SpeciesCode = code
	return b
}

// WithScientificName sets the scientific name.
func (b *DetectionBuilder) WithScientificName(name string) *DetectionBuilder {
	b.note.ScientificName = name
	return b
}

// WithCommonName sets the common name.
func (b *DetectionBuilder) WithCommonName(name string) *DetectionBuilder {
	b.note.CommonName = name
	return b
}

// WithSpecies sets all species fields at once.
func (b *DetectionBuilder) WithSpecies(code, scientific, common string) *DetectionBuilder {
	b.note.SpeciesCode = code
	b.note.ScientificName = scientific
	b.note.CommonName = common
	return b
}

// WithConfidence sets the confidence value.
func (b *DetectionBuilder) WithConfidence(conf float64) *DetectionBuilder {
	b.note.Confidence = conf
	return b
}

// WithLatitude sets the latitude.
func (b *DetectionBuilder) WithLatitude(lat float64) *DetectionBuilder {
	b.note.Latitude = lat
	return b
}

// WithLongitude sets the longitude.
func (b *DetectionBuilder) WithLongitude(lon float64) *DetectionBuilder {
	b.note.Longitude = lon
	return b
}

// WithLocation sets latitude and longitude.
func (b *DetectionBuilder) WithLocation(lat, lon float64) *DetectionBuilder {
	b.note.Latitude = lat
	b.note.Longitude = lon
	return b
}

// WithThreshold sets the threshold value.
func (b *DetectionBuilder) WithThreshold(threshold float64) *DetectionBuilder {
	b.note.Threshold = threshold
	return b
}

// WithSensitivity sets the sensitivity value.
func (b *DetectionBuilder) WithSensitivity(sensitivity float64) *DetectionBuilder {
	b.note.Sensitivity = sensitivity
	return b
}

// WithClipName sets the clip name.
func (b *DetectionBuilder) WithClipName(name string) *DetectionBuilder {
	b.note.ClipName = name
	return b
}

// WithProcessingTime sets the processing time.
func (b *DetectionBuilder) WithProcessingTime(d time.Duration) *DetectionBuilder {
	b.note.ProcessingTime = d
	return b
}

// WithMinimalData removes optional fields, keeping only required data.
func (b *DetectionBuilder) WithMinimalData() *DetectionBuilder {
	b.note.Latitude = 0
	b.note.Longitude = 0
	b.note.Threshold = 0
	b.note.Sensitivity = 0
	b.note.ClipName = ""
	b.note.ProcessingTime = 0
	return b
}

// Build returns the constructed Note.
func (b *DetectionBuilder) Build() datastore.Note {
	return b.note
}

// BuildPtr returns a pointer to the constructed Note.
func (b *DetectionBuilder) BuildPtr() *datastore.Note {
	note := b.note
	return &note
}

// ResultsBuilder provides a fluent API for building test Results (secondary predictions) data.
type ResultsBuilder struct {
	result datastore.Results
}

// NewResultsBuilder creates a new ResultsBuilder with sensible defaults.
func NewResultsBuilder() *ResultsBuilder {
	return &ResultsBuilder{
		result: datastore.Results{
			ID:         1,
			NoteID:     1,
			Species:    "Corvus brachyrhynchos",
			Confidence: 0.65,
		},
	}
}

// WithID sets the ID.
func (b *ResultsBuilder) WithID(id uint) *ResultsBuilder {
	b.result.ID = id
	return b
}

// WithNoteID sets the parent note ID.
func (b *ResultsBuilder) WithNoteID(noteID uint) *ResultsBuilder {
	b.result.NoteID = noteID
	return b
}

// WithSpecies sets the species (scientific name).
func (b *ResultsBuilder) WithSpecies(species string) *ResultsBuilder {
	b.result.Species = species
	return b
}

// WithConfidence sets the confidence value.
func (b *ResultsBuilder) WithConfidence(conf float32) *ResultsBuilder {
	b.result.Confidence = conf
	return b
}

// Build returns the constructed Results.
func (b *ResultsBuilder) Build() datastore.Results {
	return b.result
}

// BuildPtr returns a pointer to the constructed Results.
func (b *ResultsBuilder) BuildPtr() *datastore.Results {
	result := b.result
	return &result
}

// ReviewBuilder provides a fluent API for building test NoteReview data.
type ReviewBuilder struct {
	review datastore.NoteReview
}

// NewReviewBuilder creates a new ReviewBuilder with sensible defaults.
func NewReviewBuilder() *ReviewBuilder {
	now := time.Now()
	return &ReviewBuilder{
		review: datastore.NoteReview{
			ID:        1,
			NoteID:    1,
			Verified:  "correct",
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
}

// WithID sets the ID.
func (b *ReviewBuilder) WithID(id uint) *ReviewBuilder {
	b.review.ID = id
	return b
}

// WithNoteID sets the parent note ID.
func (b *ReviewBuilder) WithNoteID(noteID uint) *ReviewBuilder {
	b.review.NoteID = noteID
	return b
}

// WithVerified sets the verification status ("correct" or "false_positive").
func (b *ReviewBuilder) WithVerified(status string) *ReviewBuilder {
	b.review.Verified = status
	return b
}

// AsCorrect sets the verification status to "correct".
func (b *ReviewBuilder) AsCorrect() *ReviewBuilder {
	b.review.Verified = "correct"
	return b
}

// AsFalsePositive sets the verification status to "false_positive".
func (b *ReviewBuilder) AsFalsePositive() *ReviewBuilder {
	b.review.Verified = "false_positive"
	return b
}

// WithCreatedAt sets the creation timestamp.
func (b *ReviewBuilder) WithCreatedAt(t time.Time) *ReviewBuilder {
	b.review.CreatedAt = t
	return b
}

// WithUpdatedAt sets the update timestamp.
func (b *ReviewBuilder) WithUpdatedAt(t time.Time) *ReviewBuilder {
	b.review.UpdatedAt = t
	return b
}

// Build returns the constructed NoteReview.
func (b *ReviewBuilder) Build() datastore.NoteReview {
	return b.review
}

// BuildPtr returns a pointer to the constructed NoteReview.
func (b *ReviewBuilder) BuildPtr() *datastore.NoteReview {
	review := b.review
	return &review
}

// CommentBuilder provides a fluent API for building test NoteComment data.
type CommentBuilder struct {
	comment datastore.NoteComment
}

// NewCommentBuilder creates a new CommentBuilder with sensible defaults.
func NewCommentBuilder() *CommentBuilder {
	now := time.Now()
	return &CommentBuilder{
		comment: datastore.NoteComment{
			ID:        1,
			NoteID:    1,
			Entry:     "Test comment entry",
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
}

// WithID sets the ID.
func (b *CommentBuilder) WithID(id uint) *CommentBuilder {
	b.comment.ID = id
	return b
}

// WithNoteID sets the parent note ID.
func (b *CommentBuilder) WithNoteID(noteID uint) *CommentBuilder {
	b.comment.NoteID = noteID
	return b
}

// WithEntry sets the comment text.
func (b *CommentBuilder) WithEntry(entry string) *CommentBuilder {
	b.comment.Entry = entry
	return b
}

// WithCreatedAt sets the creation timestamp.
func (b *CommentBuilder) WithCreatedAt(t time.Time) *CommentBuilder {
	b.comment.CreatedAt = t
	return b
}

// WithUpdatedAt sets the update timestamp.
func (b *CommentBuilder) WithUpdatedAt(t time.Time) *CommentBuilder {
	b.comment.UpdatedAt = t
	return b
}

// Build returns the constructed NoteComment.
func (b *CommentBuilder) Build() datastore.NoteComment {
	return b.comment
}

// BuildPtr returns a pointer to the constructed NoteComment.
func (b *CommentBuilder) BuildPtr() *datastore.NoteComment {
	comment := b.comment
	return &comment
}

// LockBuilder provides a fluent API for building test NoteLock data.
type LockBuilder struct {
	lock datastore.NoteLock
}

// NewLockBuilder creates a new LockBuilder with sensible defaults.
func NewLockBuilder() *LockBuilder {
	return &LockBuilder{
		lock: datastore.NoteLock{
			ID:       1,
			NoteID:   1,
			LockedAt: time.Now(),
		},
	}
}

// WithID sets the ID.
func (b *LockBuilder) WithID(id uint) *LockBuilder {
	b.lock.ID = id
	return b
}

// WithNoteID sets the parent note ID.
func (b *LockBuilder) WithNoteID(noteID uint) *LockBuilder {
	b.lock.NoteID = noteID
	return b
}

// WithLockedAt sets the lock timestamp.
func (b *LockBuilder) WithLockedAt(t time.Time) *LockBuilder {
	b.lock.LockedAt = t
	return b
}

// Build returns the constructed NoteLock.
func (b *LockBuilder) Build() datastore.NoteLock {
	return b.lock
}

// BuildPtr returns a pointer to the constructed NoteLock.
func (b *LockBuilder) BuildPtr() *datastore.NoteLock {
	lock := b.lock
	return &lock
}

// DailyEventsBuilder provides a fluent API for building test DailyEvents data.
type DailyEventsBuilder struct {
	event   datastore.DailyEvents
	hourly  []datastore.HourlyWeather
}

// NewDailyEventsBuilder creates a new DailyEventsBuilder with sensible defaults.
func NewDailyEventsBuilder() *DailyEventsBuilder {
	today := time.Now().Format("2006-01-02")
	return &DailyEventsBuilder{
		event: datastore.DailyEvents{
			ID:       1,
			Date:     today,
			Sunrise:  1640000000, // ~6 AM
			Sunset:   1640043600, // ~6 PM
			Country:  "US",
			CityName: "Boston",
		},
		hourly: nil,
	}
}

// WithID sets the ID.
func (b *DailyEventsBuilder) WithID(id uint) *DailyEventsBuilder {
	b.event.ID = id
	return b
}

// WithDate sets the date.
func (b *DailyEventsBuilder) WithDate(date string) *DailyEventsBuilder {
	b.event.Date = date
	return b
}

// WithSunrise sets the sunrise timestamp (Unix seconds).
func (b *DailyEventsBuilder) WithSunrise(sunrise int64) *DailyEventsBuilder {
	b.event.Sunrise = sunrise
	return b
}

// WithSunset sets the sunset timestamp (Unix seconds).
func (b *DailyEventsBuilder) WithSunset(sunset int64) *DailyEventsBuilder {
	b.event.Sunset = sunset
	return b
}

// WithCountry sets the country.
func (b *DailyEventsBuilder) WithCountry(country string) *DailyEventsBuilder {
	b.event.Country = country
	return b
}

// WithCityName sets the city name.
func (b *DailyEventsBuilder) WithCityName(city string) *DailyEventsBuilder {
	b.event.CityName = city
	return b
}

// WithHourlyWeather adds hourly weather records for this day.
func (b *DailyEventsBuilder) WithHourlyWeather(hourly []datastore.HourlyWeather) *DailyEventsBuilder {
	b.hourly = hourly
	return b
}

// Build returns the constructed DailyEvents.
func (b *DailyEventsBuilder) Build() datastore.DailyEvents {
	return b.event
}

// BuildPtr returns a pointer to the constructed DailyEvents.
func (b *DailyEventsBuilder) BuildPtr() *datastore.DailyEvents {
	event := b.event
	return &event
}

// BuildWithHourly returns the DailyEvents and its associated hourly weather.
func (b *DailyEventsBuilder) BuildWithHourly() (datastore.DailyEvents, []datastore.HourlyWeather) {
	// Update the DailyEventsID for all hourly records
	for i := range b.hourly {
		b.hourly[i].DailyEventsID = b.event.ID
	}
	return b.event, b.hourly
}

// HourlyWeatherBuilder provides a fluent API for building test HourlyWeather data.
type HourlyWeatherBuilder struct {
	weather datastore.HourlyWeather
}

// NewHourlyWeatherBuilder creates a new HourlyWeatherBuilder with sensible defaults.
func NewHourlyWeatherBuilder() *HourlyWeatherBuilder {
	return &HourlyWeatherBuilder{
		weather: datastore.HourlyWeather{
			ID:            1,
			DailyEventsID: 1,
			Time:          time.Now(),
			Temperature:   20.5,
			FeelsLike:     19.0,
			TempMin:       18.0,
			TempMax:       23.0,
			Pressure:      1013,
			Humidity:      65,
			Visibility:    10000,
			WindSpeed:     5.5,
			WindDeg:       180,
			WindGust:      8.0,
			Clouds:        40,
			WeatherMain:   "Clouds",
			WeatherDesc:   "scattered clouds",
			WeatherIcon:   "03d",
		},
	}
}

// WithID sets the ID.
func (b *HourlyWeatherBuilder) WithID(id uint) *HourlyWeatherBuilder {
	b.weather.ID = id
	return b
}

// WithDailyEventsID sets the parent daily events ID.
func (b *HourlyWeatherBuilder) WithDailyEventsID(id uint) *HourlyWeatherBuilder {
	b.weather.DailyEventsID = id
	return b
}

// WithTime sets the time.
func (b *HourlyWeatherBuilder) WithTime(t time.Time) *HourlyWeatherBuilder {
	b.weather.Time = t
	return b
}

// WithTemperature sets the temperature.
func (b *HourlyWeatherBuilder) WithTemperature(temp float64) *HourlyWeatherBuilder {
	b.weather.Temperature = temp
	return b
}

// WithFeelsLike sets the feels-like temperature.
func (b *HourlyWeatherBuilder) WithFeelsLike(temp float64) *HourlyWeatherBuilder {
	b.weather.FeelsLike = temp
	return b
}

// WithTempMinMax sets the min and max temperatures.
func (b *HourlyWeatherBuilder) WithTempMinMax(minTemp, maxTemp float64) *HourlyWeatherBuilder {
	b.weather.TempMin = minTemp
	b.weather.TempMax = maxTemp
	return b
}

// WithPressure sets the pressure.
func (b *HourlyWeatherBuilder) WithPressure(pressure int) *HourlyWeatherBuilder {
	b.weather.Pressure = pressure
	return b
}

// WithHumidity sets the humidity.
func (b *HourlyWeatherBuilder) WithHumidity(humidity int) *HourlyWeatherBuilder {
	b.weather.Humidity = humidity
	return b
}

// WithVisibility sets the visibility.
func (b *HourlyWeatherBuilder) WithVisibility(visibility int) *HourlyWeatherBuilder {
	b.weather.Visibility = visibility
	return b
}

// WithWind sets wind speed, direction, and gust.
func (b *HourlyWeatherBuilder) WithWind(speed float64, deg int, gust float64) *HourlyWeatherBuilder {
	b.weather.WindSpeed = speed
	b.weather.WindDeg = deg
	b.weather.WindGust = gust
	return b
}

// WithClouds sets the cloud coverage percentage.
func (b *HourlyWeatherBuilder) WithClouds(clouds int) *HourlyWeatherBuilder {
	b.weather.Clouds = clouds
	return b
}

// WithWeather sets weather main, description, and icon.
func (b *HourlyWeatherBuilder) WithWeather(main, desc, icon string) *HourlyWeatherBuilder {
	b.weather.WeatherMain = main
	b.weather.WeatherDesc = desc
	b.weather.WeatherIcon = icon
	return b
}

// Build returns the constructed HourlyWeather.
func (b *HourlyWeatherBuilder) Build() datastore.HourlyWeather {
	return b.weather
}

// BuildPtr returns a pointer to the constructed HourlyWeather.
func (b *HourlyWeatherBuilder) BuildPtr() *datastore.HourlyWeather {
	weather := b.weather
	return &weather
}

// DynamicThresholdBuilder provides a fluent API for building test DynamicThreshold data.
type DynamicThresholdBuilder struct {
	threshold datastore.DynamicThreshold
}

// NewDynamicThresholdBuilder creates a new DynamicThresholdBuilder with sensible defaults.
func NewDynamicThresholdBuilder() *DynamicThresholdBuilder {
	now := time.Now()
	return &DynamicThresholdBuilder{
		threshold: datastore.DynamicThreshold{
			ID:             1,
			SpeciesName:    "american robin",
			ScientificName: "Turdus migratorius",
			Level:          1,
			CurrentValue:   0.75,
			BaseThreshold:  0.65,
			HighConfCount:  5,
			ValidHours:     24,
			ExpiresAt:      now.Add(24 * time.Hour),
			LastTriggered:  now,
			FirstCreated:   now.Add(-7 * 24 * time.Hour),
			UpdatedAt:      now,
			TriggerCount:   10,
		},
	}
}

// WithID sets the ID.
func (b *DynamicThresholdBuilder) WithID(id uint) *DynamicThresholdBuilder {
	b.threshold.ID = id
	return b
}

// WithSpeciesName sets the species name (common name, lowercase).
func (b *DynamicThresholdBuilder) WithSpeciesName(name string) *DynamicThresholdBuilder {
	b.threshold.SpeciesName = name
	return b
}

// WithScientificName sets the scientific name.
func (b *DynamicThresholdBuilder) WithScientificName(name string) *DynamicThresholdBuilder {
	b.threshold.ScientificName = name
	return b
}

// WithLevel sets the threshold level (0-3).
func (b *DynamicThresholdBuilder) WithLevel(level int) *DynamicThresholdBuilder {
	b.threshold.Level = level
	return b
}

// WithCurrentValue sets the current threshold value.
func (b *DynamicThresholdBuilder) WithCurrentValue(value float64) *DynamicThresholdBuilder {
	b.threshold.CurrentValue = value
	return b
}

// WithBaseThreshold sets the base threshold value.
func (b *DynamicThresholdBuilder) WithBaseThreshold(value float64) *DynamicThresholdBuilder {
	b.threshold.BaseThreshold = value
	return b
}

// WithHighConfCount sets the high confidence detection count.
func (b *DynamicThresholdBuilder) WithHighConfCount(count int) *DynamicThresholdBuilder {
	b.threshold.HighConfCount = count
	return b
}

// WithValidHours sets the valid hours.
func (b *DynamicThresholdBuilder) WithValidHours(hours int) *DynamicThresholdBuilder {
	b.threshold.ValidHours = hours
	return b
}

// WithExpiresAt sets the expiration time.
func (b *DynamicThresholdBuilder) WithExpiresAt(t time.Time) *DynamicThresholdBuilder {
	b.threshold.ExpiresAt = t
	return b
}

// WithLastTriggered sets the last triggered time.
func (b *DynamicThresholdBuilder) WithLastTriggered(t time.Time) *DynamicThresholdBuilder {
	b.threshold.LastTriggered = t
	return b
}

// WithFirstCreated sets the first created time.
func (b *DynamicThresholdBuilder) WithFirstCreated(t time.Time) *DynamicThresholdBuilder {
	b.threshold.FirstCreated = t
	return b
}

// WithUpdatedAt sets the updated at time.
func (b *DynamicThresholdBuilder) WithUpdatedAt(t time.Time) *DynamicThresholdBuilder {
	b.threshold.UpdatedAt = t
	return b
}

// WithTriggerCount sets the trigger count.
func (b *DynamicThresholdBuilder) WithTriggerCount(count int) *DynamicThresholdBuilder {
	b.threshold.TriggerCount = count
	return b
}

// Build returns the constructed DynamicThreshold.
func (b *DynamicThresholdBuilder) Build() datastore.DynamicThreshold {
	return b.threshold
}

// BuildPtr returns a pointer to the constructed DynamicThreshold.
func (b *DynamicThresholdBuilder) BuildPtr() *datastore.DynamicThreshold {
	threshold := b.threshold
	return &threshold
}

// ThresholdEventBuilder provides a fluent API for building test ThresholdEvent data.
type ThresholdEventBuilder struct {
	event datastore.ThresholdEvent
}

// NewThresholdEventBuilder creates a new ThresholdEventBuilder with sensible defaults.
func NewThresholdEventBuilder() *ThresholdEventBuilder {
	return &ThresholdEventBuilder{
		event: datastore.ThresholdEvent{
			ID:            1,
			SpeciesName:   "american robin",
			PreviousLevel: 0,
			NewLevel:      1,
			PreviousValue: 0.65,
			NewValue:      0.75,
			ChangeReason:  "high_confidence",
			Confidence:    0.92,
			CreatedAt:     time.Now(),
		},
	}
}

// WithID sets the ID.
func (b *ThresholdEventBuilder) WithID(id uint) *ThresholdEventBuilder {
	b.event.ID = id
	return b
}

// WithSpeciesName sets the species name.
func (b *ThresholdEventBuilder) WithSpeciesName(name string) *ThresholdEventBuilder {
	b.event.SpeciesName = name
	return b
}

// WithLevelChange sets the previous and new levels.
func (b *ThresholdEventBuilder) WithLevelChange(prev, newLevel int) *ThresholdEventBuilder {
	b.event.PreviousLevel = prev
	b.event.NewLevel = newLevel
	return b
}

// WithValueChange sets the previous and new threshold values.
func (b *ThresholdEventBuilder) WithValueChange(prev, newVal float64) *ThresholdEventBuilder {
	b.event.PreviousValue = prev
	b.event.NewValue = newVal
	return b
}

// WithChangeReason sets the change reason.
func (b *ThresholdEventBuilder) WithChangeReason(reason string) *ThresholdEventBuilder {
	b.event.ChangeReason = reason
	return b
}

// WithConfidence sets the detection confidence that triggered the change.
func (b *ThresholdEventBuilder) WithConfidence(conf float64) *ThresholdEventBuilder {
	b.event.Confidence = conf
	return b
}

// WithCreatedAt sets the created at time.
func (b *ThresholdEventBuilder) WithCreatedAt(t time.Time) *ThresholdEventBuilder {
	b.event.CreatedAt = t
	return b
}

// Build returns the constructed ThresholdEvent.
func (b *ThresholdEventBuilder) Build() datastore.ThresholdEvent {
	return b.event
}

// BuildPtr returns a pointer to the constructed ThresholdEvent.
func (b *ThresholdEventBuilder) BuildPtr() *datastore.ThresholdEvent {
	event := b.event
	return &event
}

// ImageCacheBuilder provides a fluent API for building test ImageCache data.
type ImageCacheBuilder struct {
	cache datastore.ImageCache
}

// NewImageCacheBuilder creates a new ImageCacheBuilder with sensible defaults.
func NewImageCacheBuilder() *ImageCacheBuilder {
	return &ImageCacheBuilder{
		cache: datastore.ImageCache{
			ID:             1,
			ProviderName:   "wikimedia",
			ScientificName: "Turdus migratorius",
			SourceProvider: "wikimedia",
			URL:            "https://example.com/image.jpg",
			LicenseName:    "CC BY-SA 4.0",
			LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
			AuthorName:     "Test Author",
			AuthorURL:      "https://example.com/author",
			CachedAt:       time.Now(),
		},
	}
}

// WithID sets the ID.
func (b *ImageCacheBuilder) WithID(id uint) *ImageCacheBuilder {
	b.cache.ID = id
	return b
}

// WithProviderName sets the provider name.
func (b *ImageCacheBuilder) WithProviderName(name string) *ImageCacheBuilder {
	b.cache.ProviderName = name
	return b
}

// WithScientificName sets the scientific name.
func (b *ImageCacheBuilder) WithScientificName(name string) *ImageCacheBuilder {
	b.cache.ScientificName = name
	return b
}

// WithSourceProvider sets the source provider.
func (b *ImageCacheBuilder) WithSourceProvider(provider string) *ImageCacheBuilder {
	b.cache.SourceProvider = provider
	return b
}

// WithURL sets the image URL.
func (b *ImageCacheBuilder) WithURL(url string) *ImageCacheBuilder {
	b.cache.URL = url
	return b
}

// WithLicense sets the license name and URL.
func (b *ImageCacheBuilder) WithLicense(name, url string) *ImageCacheBuilder {
	b.cache.LicenseName = name
	b.cache.LicenseURL = url
	return b
}

// WithAuthor sets the author name and URL.
func (b *ImageCacheBuilder) WithAuthor(name, url string) *ImageCacheBuilder {
	b.cache.AuthorName = name
	b.cache.AuthorURL = url
	return b
}

// WithCachedAt sets the cached at time.
func (b *ImageCacheBuilder) WithCachedAt(t time.Time) *ImageCacheBuilder {
	b.cache.CachedAt = t
	return b
}

// Build returns the constructed ImageCache.
func (b *ImageCacheBuilder) Build() datastore.ImageCache {
	return b.cache
}

// BuildPtr returns a pointer to the constructed ImageCache.
func (b *ImageCacheBuilder) BuildPtr() *datastore.ImageCache {
	cache := b.cache
	return &cache
}

// NotificationHistoryBuilder provides a fluent API for building test NotificationHistory data.
type NotificationHistoryBuilder struct {
	history datastore.NotificationHistory
}

// NewNotificationHistoryBuilder creates a new NotificationHistoryBuilder with sensible defaults.
func NewNotificationHistoryBuilder() *NotificationHistoryBuilder {
	now := time.Now()
	return &NotificationHistoryBuilder{
		history: datastore.NotificationHistory{
			ID:               1,
			ScientificName:   "Turdus migratorius",
			NotificationType: "new_species",
			LastSent:         now,
			ExpiresAt:        now.Add(24 * time.Hour),
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}
}

// WithID sets the ID.
func (b *NotificationHistoryBuilder) WithID(id uint) *NotificationHistoryBuilder {
	b.history.ID = id
	return b
}

// WithScientificName sets the scientific name.
func (b *NotificationHistoryBuilder) WithScientificName(name string) *NotificationHistoryBuilder {
	b.history.ScientificName = name
	return b
}

// WithNotificationType sets the notification type.
func (b *NotificationHistoryBuilder) WithNotificationType(notifType string) *NotificationHistoryBuilder {
	b.history.NotificationType = notifType
	return b
}

// WithLastSent sets the last sent time.
func (b *NotificationHistoryBuilder) WithLastSent(t time.Time) *NotificationHistoryBuilder {
	b.history.LastSent = t
	return b
}

// WithExpiresAt sets the expiration time.
func (b *NotificationHistoryBuilder) WithExpiresAt(t time.Time) *NotificationHistoryBuilder {
	b.history.ExpiresAt = t
	return b
}

// WithCreatedAt sets the created at time.
func (b *NotificationHistoryBuilder) WithCreatedAt(t time.Time) *NotificationHistoryBuilder {
	b.history.CreatedAt = t
	return b
}

// WithUpdatedAt sets the updated at time.
func (b *NotificationHistoryBuilder) WithUpdatedAt(t time.Time) *NotificationHistoryBuilder {
	b.history.UpdatedAt = t
	return b
}

// Build returns the constructed NotificationHistory.
func (b *NotificationHistoryBuilder) Build() datastore.NotificationHistory {
	return b.history
}

// BuildPtr returns a pointer to the constructed NotificationHistory.
func (b *NotificationHistoryBuilder) BuildPtr() *datastore.NotificationHistory {
	history := b.history
	return &history
}

// TestSpecies defines common species used in tests for consistent test data.
var TestSpecies = []struct {
	Code       string
	Scientific string
	Common     string
}{
	{"turmig", "Turdus migratorius", "American Robin"},
	{"norcra", "Corvus brachyrhynchos", "American Crow"},
	{"blujay", "Cyanocitta cristata", "Blue Jay"},
	{"houspa", "Passer domesticus", "House Sparrow"},
	{"carwre", "Thryothorus ludovicianus", "Carolina Wren"},
	{"norcar", "Cardinalis cardinalis", "Northern Cardinal"}, //nolint:misspell // Cardinalis is a valid scientific genus name
	{"amerob", "Turdus migratorius", "American Robin"},
	{"eunsta", "Sturnus vulgaris", "European Starling"},
	{"rebnut", "Sitta canadensis", "Red-breasted Nuthatch"},
	{"dowwoo", "Dryobates pubescens", "Downy Woodpecker"},
}

// GenerateDetections creates a slice of detection Notes for testing.
func GenerateDetections(count int) []datastore.Note {
	notes := make([]datastore.Note, count)
	baseTime := time.Now().Add(-time.Duration(count) * time.Hour)

	for i := range count {
		speciesIdx := i % len(TestSpecies)
		species := TestSpecies[speciesIdx]

		ts := baseTime.Add(time.Duration(i) * time.Hour)
		confidence := 0.5 + (float64(i%50) / 100.0) // Vary between 0.50 and 0.99

		notes[i] = NewDetectionBuilder().
			WithID(uint(i + 1)). //nolint:gosec // G115: test data uses small values that cannot overflow
			WithTimestamp(ts).
			WithSpecies(species.Code, species.Scientific, species.Common).
			WithConfidence(confidence).
			WithClipName(fmt.Sprintf("clip_%d.wav", i+1)).
			Build()
	}

	return notes
}

// GenerateWeatherData creates DailyEvents and HourlyWeather for testing.
func GenerateWeatherData(days int) ([]datastore.DailyEvents, []datastore.HourlyWeather) {
	dailyEvents := make([]datastore.DailyEvents, days)
	hourlyWeather := make([]datastore.HourlyWeather, 0, days*24)

	baseDate := time.Now().AddDate(0, 0, -days)

	for d := range days {
		date := baseDate.AddDate(0, 0, d)
		dayID := uint(d + 1) //nolint:gosec // G115: test data uses small values

		// Create daily events
		dailyEvents[d] = NewDailyEventsBuilder().
			WithID(dayID).
			WithDate(date.Format("2006-01-02")).
			WithSunrise(date.Add(6 * time.Hour).Unix()).
			WithSunset(date.Add(18 * time.Hour).Unix()).
			Build()

		// Create 24 hourly weather records for this day
		for h := range 24 {
			hourTime := date.Add(time.Duration(h) * time.Hour)
			weatherID := uint(d*24 + h + 1) //nolint:gosec // G115: test data uses small values

			// Vary temperature based on hour (cooler at night)
			baseTemp := 15.0
			if h >= 6 && h <= 18 {
				baseTemp = 20.0 + float64(h-6)/12.0*10.0 // Warm up during day
			}

			hw := NewHourlyWeatherBuilder().
				WithID(weatherID).
				WithDailyEventsID(dayID).
				WithTime(hourTime).
				WithTemperature(baseTemp).
				WithFeelsLike(baseTemp - 1).
				WithTempMinMax(baseTemp-5, baseTemp+5).
				WithHumidity(50 + h%30).
				Build()

			hourlyWeather = append(hourlyWeather, hw)
		}
	}

	return dailyEvents, hourlyWeather
}
