package testutil

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// LegacySeeder provides direct SQL-based seeding for legacy database tables.
// This bypasses GORM for maximum efficiency during test setup.
type LegacySeeder struct {
	db *sql.DB
}

// NewLegacySeeder creates a new seeder with the given SQL database connection.
func NewLegacySeeder(db *sql.DB) *LegacySeeder {
	return &LegacySeeder{db: db}
}

// batchSize defines the number of records to insert per transaction.
const batchSize = 500

// SeedDetections inserts detection notes into the legacy notes table.
// Uses batch inserts with transactions for efficiency.
func (s *LegacySeeder) SeedDetections(notes []datastore.Note) error {
	if len(notes) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO notes (
		id, source_node, date, time, begin_time, end_time,
		species_code, scientific_name, common_name, confidence,
		latitude, longitude, threshold, sensitivity, clip_name, processing_time
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// Process in batches
	for i := 0; i < len(notes); i += batchSize {
		end := min(i+batchSize, len(notes))
		batch := notes[i:end]

		if err := s.seedDetectionBatch(batch, insertSQL); err != nil {
			return fmt.Errorf("batch %d: %w", i/batchSize, err)
		}
	}

	return nil
}

// seedDetectionBatch inserts a batch of notes in a single transaction.
func (s *LegacySeeder) seedDetectionBatch(notes []datastore.Note, insertSQL string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is a no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range notes {
		note := &notes[i]
		// Convert time.Duration to nanoseconds for storage (GORM default)
		processingTimeNs := note.ProcessingTime.Nanoseconds()

		_, err := stmt.Exec(
			note.ID,
			note.SourceNode,
			note.Date,
			note.Time,
			note.BeginTime,
			note.EndTime,
			note.SpeciesCode,
			note.ScientificName,
			note.CommonName,
			note.Confidence,
			note.Latitude,
			note.Longitude,
			note.Threshold,
			note.Sensitivity,
			note.ClipName,
			processingTimeNs,
		)
		if err != nil {
			return fmt.Errorf("insert note %d: %w", note.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedResults inserts secondary prediction results into the legacy results table.
func (s *LegacySeeder) SeedResults(results []datastore.Results) error {
	if len(results) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO results (id, note_id, species, confidence) VALUES (?, ?, ?, ?)`

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, result := range results {
		_, err := stmt.Exec(result.ID, result.NoteID, result.Species, result.Confidence)
		if err != nil {
			return fmt.Errorf("insert result %d: %w", result.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedReviews inserts review records into the legacy note_reviews table.
//
//nolint:dupl // Similar structure to other seed methods is intentional for readability
func (s *LegacySeeder) SeedReviews(reviews []datastore.NoteReview) error {
	if len(reviews) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO note_reviews (id, note_id, verified, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, review := range reviews {
		_, err := stmt.Exec(
			review.ID,
			review.NoteID,
			review.Verified,
			review.CreatedAt,
			review.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("insert review %d: %w", review.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedComments inserts comment records into the legacy note_comments table.
//
//nolint:dupl // Similar structure to other seed methods is intentional for readability
func (s *LegacySeeder) SeedComments(comments []datastore.NoteComment) error {
	if len(comments) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO note_comments (id, note_id, entry, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, comment := range comments {
		_, err := stmt.Exec(
			comment.ID,
			comment.NoteID,
			comment.Entry,
			comment.CreatedAt,
			comment.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("insert comment %d: %w", comment.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedLocks inserts lock records into the legacy note_locks table.
func (s *LegacySeeder) SeedLocks(locks []datastore.NoteLock) error {
	if len(locks) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO note_locks (id, note_id, locked_at) VALUES (?, ?, ?)`

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, lock := range locks {
		_, err := stmt.Exec(lock.ID, lock.NoteID, lock.LockedAt)
		if err != nil {
			return fmt.Errorf("insert lock %d: %w", lock.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedDailyEvents inserts daily event records into the legacy daily_events table.
func (s *LegacySeeder) SeedDailyEvents(events []datastore.DailyEvents) error {
	if len(events) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO daily_events (id, date, sunrise, sunset, country, city_name) VALUES (?, ?, ?, ?, ?, ?)`

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, event := range events {
		_, err := stmt.Exec(
			event.ID,
			event.Date,
			event.Sunrise,
			event.Sunset,
			event.Country,
			event.CityName,
		)
		if err != nil {
			return fmt.Errorf("insert daily event %d: %w", event.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedHourlyWeather inserts hourly weather records into the legacy hourly_weathers table.
func (s *LegacySeeder) SeedHourlyWeather(weather []datastore.HourlyWeather) error {
	if len(weather) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO hourly_weathers (
		id, daily_events_id, time, temperature, feels_like,
		temp_min, temp_max, pressure, humidity, visibility,
		wind_speed, wind_deg, wind_gust, clouds,
		weather_main, weather_desc, weather_icon
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// Process in batches
	for i := 0; i < len(weather); i += batchSize {
		end := min(i+batchSize, len(weather))
		batch := weather[i:end]

		if err := s.seedWeatherBatch(batch, insertSQL); err != nil {
			return fmt.Errorf("batch %d: %w", i/batchSize, err)
		}
	}

	return nil
}

// seedWeatherBatch inserts a batch of hourly weather in a single transaction.
func (s *LegacySeeder) seedWeatherBatch(weather []datastore.HourlyWeather, insertSQL string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range weather {
		w := &weather[i]
		_, err := stmt.Exec(
			w.ID,
			w.DailyEventsID,
			w.Time,
			w.Temperature,
			w.FeelsLike,
			w.TempMin,
			w.TempMax,
			w.Pressure,
			w.Humidity,
			w.Visibility,
			w.WindSpeed,
			w.WindDeg,
			w.WindGust,
			w.Clouds,
			w.WeatherMain,
			w.WeatherDesc,
			w.WeatherIcon,
		)
		if err != nil {
			return fmt.Errorf("insert hourly weather %d: %w", w.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedWeather is a convenience method that seeds both daily events and hourly weather.
func (s *LegacySeeder) SeedWeather(dailyEvents []datastore.DailyEvents, hourlyWeather []datastore.HourlyWeather) error {
	if err := s.SeedDailyEvents(dailyEvents); err != nil {
		return fmt.Errorf("seed daily events: %w", err)
	}
	if err := s.SeedHourlyWeather(hourlyWeather); err != nil {
		return fmt.Errorf("seed hourly weather: %w", err)
	}
	return nil
}

// SeedDynamicThresholds inserts dynamic threshold records.
func (s *LegacySeeder) SeedDynamicThresholds(thresholds []datastore.DynamicThreshold) error {
	if len(thresholds) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO dynamic_thresholds (
		id, species_name, scientific_name, level, current_value,
		base_threshold, high_conf_count, valid_hours, expires_at,
		last_triggered, first_created, updated_at, trigger_count
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range thresholds {
		th := &thresholds[i]
		_, err := stmt.Exec(
			th.ID,
			th.SpeciesName,
			th.ScientificName,
			th.Level,
			th.CurrentValue,
			th.BaseThreshold,
			th.HighConfCount,
			th.ValidHours,
			th.ExpiresAt,
			th.LastTriggered,
			th.FirstCreated,
			th.UpdatedAt,
			th.TriggerCount,
		)
		if err != nil {
			return fmt.Errorf("insert threshold %d: %w", th.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedThresholdEvents inserts threshold event records.
func (s *LegacySeeder) SeedThresholdEvents(events []datastore.ThresholdEvent) error {
	if len(events) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO threshold_events (
		id, species_name, previous_level, new_level,
		previous_value, new_value, change_reason, confidence, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, e := range events {
		_, err := stmt.Exec(
			e.ID,
			e.SpeciesName,
			e.PreviousLevel,
			e.NewLevel,
			e.PreviousValue,
			e.NewValue,
			e.ChangeReason,
			e.Confidence,
			e.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("insert threshold event %d: %w", e.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedImageCaches inserts image cache records.
func (s *LegacySeeder) SeedImageCaches(caches []datastore.ImageCache) error {
	if len(caches) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO image_caches (
		id, provider_name, scientific_name, source_provider,
		url, license_name, license_url, author_name, author_url, cached_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range caches {
		c := &caches[i]
		_, err := stmt.Exec(
			c.ID,
			c.ProviderName,
			c.ScientificName,
			c.SourceProvider,
			c.URL,
			c.LicenseName,
			c.LicenseURL,
			c.AuthorName,
			c.AuthorURL,
			c.CachedAt,
		)
		if err != nil {
			return fmt.Errorf("insert image cache %d: %w", c.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedNotificationHistory inserts notification history records.
func (s *LegacySeeder) SeedNotificationHistory(history []datastore.NotificationHistory) error {
	if len(history) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO notification_histories (
		id, scientific_name, notification_type, last_sent, expires_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is no-op if already committed

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range history {
		h := &history[i]
		_, err := stmt.Exec(
			h.ID,
			h.ScientificName,
			h.NotificationType,
			h.LastSent,
			h.ExpiresAt,
			h.CreatedAt,
			h.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("insert notification history %d: %w", h.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// SeedAll is a convenience method that seeds all data types at once.
// This is useful for setting up complete test scenarios.
func (s *LegacySeeder) SeedAll(data *SeedData) error {
	if data.Notes != nil {
		if err := s.SeedDetections(data.Notes); err != nil {
			return fmt.Errorf("seed detections: %w", err)
		}
	}

	if data.Results != nil {
		if err := s.SeedResults(data.Results); err != nil {
			return fmt.Errorf("seed results: %w", err)
		}
	}

	if data.Reviews != nil {
		if err := s.SeedReviews(data.Reviews); err != nil {
			return fmt.Errorf("seed reviews: %w", err)
		}
	}

	if data.Comments != nil {
		if err := s.SeedComments(data.Comments); err != nil {
			return fmt.Errorf("seed comments: %w", err)
		}
	}

	if data.Locks != nil {
		if err := s.SeedLocks(data.Locks); err != nil {
			return fmt.Errorf("seed locks: %w", err)
		}
	}

	if data.DailyEvents != nil {
		if err := s.SeedDailyEvents(data.DailyEvents); err != nil {
			return fmt.Errorf("seed daily events: %w", err)
		}
	}

	if data.HourlyWeather != nil {
		if err := s.SeedHourlyWeather(data.HourlyWeather); err != nil {
			return fmt.Errorf("seed hourly weather: %w", err)
		}
	}

	if data.DynamicThresholds != nil {
		if err := s.SeedDynamicThresholds(data.DynamicThresholds); err != nil {
			return fmt.Errorf("seed dynamic thresholds: %w", err)
		}
	}

	if data.ThresholdEvents != nil {
		if err := s.SeedThresholdEvents(data.ThresholdEvents); err != nil {
			return fmt.Errorf("seed threshold events: %w", err)
		}
	}

	if data.ImageCaches != nil {
		if err := s.SeedImageCaches(data.ImageCaches); err != nil {
			return fmt.Errorf("seed image caches: %w", err)
		}
	}

	if data.NotificationHistory != nil {
		if err := s.SeedNotificationHistory(data.NotificationHistory); err != nil {
			return fmt.Errorf("seed notification history: %w", err)
		}
	}

	return nil
}

// SeedData contains all data types that can be seeded.
type SeedData struct {
	Notes               []datastore.Note
	Results             []datastore.Results
	Reviews             []datastore.NoteReview
	Comments            []datastore.NoteComment
	Locks               []datastore.NoteLock
	DailyEvents         []datastore.DailyEvents
	HourlyWeather       []datastore.HourlyWeather
	DynamicThresholds   []datastore.DynamicThreshold
	ThresholdEvents     []datastore.ThresholdEvent
	ImageCaches         []datastore.ImageCache
	NotificationHistory []datastore.NotificationHistory
}

// GenerateRelatedData generates related data (results, reviews, comments, locks) for notes.
// This is useful for creating complete test scenarios.
func GenerateRelatedData(notes []datastore.Note, config *RelatedDataConfig) *SeedData {
	if config == nil {
		config = &RelatedDataConfig{
			ResultsPerNote:       2,
			ReviewedNoteRatio:    0.5,
			CommentedNoteRatio:   0.3,
			CommentsPerNote:      2,
			LockedNoteRatio:      0.1,
		}
	}

	data := &SeedData{
		Notes: notes,
	}

	var resultID, reviewID, commentID, lockID uint = 1, 1, 1, 1
	baseTime := time.Now()

	for i := range notes {
		note := &notes[i]
		// Generate secondary results
		for j := range config.ResultsPerNote {
			speciesIdx := (i + j + 1) % len(TestSpecies)
			species := TestSpecies[speciesIdx]

			result := NewResultsBuilder().
				WithID(resultID).
				WithNoteID(note.ID).
				WithSpecies(species.Scientific).
				WithConfidence(float32(0.3 + float64(j)*0.1)). // Decreasing confidence
				Build()
			data.Results = append(data.Results, result)
			resultID++
		}

		// Generate reviews for some notes
		if float64(i)/float64(len(notes)) < config.ReviewedNoteRatio {
			verified := "correct"
			if i%3 == 0 {
				verified = "false_positive"
			}
			review := NewReviewBuilder().
				WithID(reviewID).
				WithNoteID(note.ID).
				WithVerified(verified).
				WithCreatedAt(baseTime.Add(time.Duration(i) * time.Minute)).
				Build()
			data.Reviews = append(data.Reviews, review)
			reviewID++
		}

		// Generate comments for some notes
		if float64(i)/float64(len(notes)) < config.CommentedNoteRatio {
			for j := range config.CommentsPerNote {
				comment := NewCommentBuilder().
					WithID(commentID).
					WithNoteID(note.ID).
					WithEntry(fmt.Sprintf("Comment %d on note %d", j+1, note.ID)).
					WithCreatedAt(baseTime.Add(time.Duration(i*config.CommentsPerNote+j) * time.Minute)).
					Build()
				data.Comments = append(data.Comments, comment)
				commentID++
			}
		}

		// Generate locks for some notes
		if float64(i)/float64(len(notes)) < config.LockedNoteRatio {
			lock := NewLockBuilder().
				WithID(lockID).
				WithNoteID(note.ID).
				WithLockedAt(baseTime.Add(time.Duration(i) * time.Minute)).
				Build()
			data.Locks = append(data.Locks, lock)
			lockID++
		}
	}

	return data
}

// RelatedDataConfig configures how related data is generated.
type RelatedDataConfig struct {
	ResultsPerNote       int     // Number of secondary results per note
	ReviewedNoteRatio    float64 // Fraction of notes with reviews (0-1)
	CommentedNoteRatio   float64 // Fraction of notes with comments (0-1)
	CommentsPerNote      int     // Number of comments per commented note
	LockedNoteRatio      float64 // Fraction of notes that are locked (0-1)
}
