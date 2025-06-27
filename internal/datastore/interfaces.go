// interfaces.go: this code defines the interface for the database operations
package datastore

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/suncalc" // Import suncalc
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// sunriseSetWindowMinutes defines the time window (in minutes) around sunrise and sunset
const sunriseSetWindowMinutes = 30

// StoreInterface abstracts the underlying database implementation and defines the interface for database operations.
type Interface interface {
	Open() error
	Save(note *Note, results []Results) error
	Delete(id string) error
	Get(id string) (Note, error)
	Close() error
	GetAllNotes() ([]Note, error)
	GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]Note, error)
	GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error)
	SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit int, offset int) ([]Note, error)
	GetLastDetections(numDetections int) ([]Note, error)
	GetAllDetectedSpecies() ([]Note, error)
	SearchNotes(query string, sortAscending bool, limit int, offset int) ([]Note, error)
	GetNoteClipPath(noteID string) (string, error)
	DeleteNoteClipPath(noteID string) error
	GetNoteReview(noteID string) (*NoteReview, error)
	SaveNoteReview(review *NoteReview) error
	GetNoteComments(noteID string) ([]NoteComment, error)
	SaveNoteComment(comment *NoteComment) error
	UpdateNoteComment(commentID string, entry string) error
	DeleteNoteComment(commentID string) error
	SaveDailyEvents(dailyEvents *DailyEvents) error
	GetDailyEvents(date string) (DailyEvents, error)
	SaveHourlyWeather(hourlyWeather *HourlyWeather) error
	GetHourlyWeather(date string) ([]HourlyWeather, error)
	LatestHourlyWeather() (*HourlyWeather, error)
	GetHourlyDetections(date, hour string, duration, limit, offset int) ([]Note, error)
	CountSpeciesDetections(species, date, hour string, duration int) (int64, error)
	CountSearchResults(query string) (int64, error)
	Transaction(fc func(tx *gorm.DB) error) error
	// Lock management methods
	LockNote(noteID string) error
	UnlockNote(noteID string) error
	GetNoteLock(noteID string) (*NoteLock, error)
	IsNoteLocked(noteID string) (bool, error)
	// Image cache methods
	GetImageCache(query ImageCacheQuery) (*ImageCache, error)
	GetImageCacheBatch(providerName string, scientificNames []string) (map[string]*ImageCache, error)
	SaveImageCache(cache *ImageCache) error
	GetAllImageCaches(providerName string) ([]ImageCache, error)
	GetLockedNotesClipPaths() ([]string, error)
	CountHourlyDetections(date, hour string, duration int) (int64, error)
	// Analytics methods
	GetSpeciesSummaryData(startDate, endDate string) ([]SpeciesSummaryData, error)
	GetHourlyAnalyticsData(date string, species string) ([]HourlyAnalyticsData, error)
	GetDailyAnalyticsData(startDate, endDate string, species string) ([]DailyAnalyticsData, error)
	GetDetectionTrends(period string, limit int) ([]DailyAnalyticsData, error)
	GetHourlyDistribution(startDate, endDate string, species string) ([]HourlyDistributionData, error)
	GetNewSpeciesDetections(startDate, endDate string, limit, offset int) ([]NewSpeciesData, error)
	// Search functionality
	SearchDetections(filters *SearchFilters) ([]DetectionRecord, int, error)
}

// DataStore implements StoreInterface using a GORM database.
type DataStore struct {
	DB            *gorm.DB         // GORM database instance
	SunCalc       *suncalc.SunCalc // Instance for calculating sun times (Assumed initialized)
	sunTimesCache sync.Map         // Thread-safe map for caching sun times by date
}

// NewDataStore creates a new DataStore instance based on the provided configuration context.
func New(settings *conf.Settings) Interface {
	// Create a SunCalc instance to be shared by all datastore implementations
	sunCalc := suncalc.NewSunCalc(settings.BirdNET.Latitude, settings.BirdNET.Longitude)

	switch {
	case settings.Output.SQLite.Enabled:
		return &SQLiteStore{
			Settings: settings,
			DataStore: DataStore{
				SunCalc: sunCalc,
			},
		}
	case settings.Output.MySQL.Enabled:
		return &MySQLStore{
			Settings: settings,
			DataStore: DataStore{
				SunCalc: sunCalc,
			},
		}
	default:
		// Consider handling the case where neither database is enabled
		return nil
	}
}

// Save stores a note and its associated results as a single transaction in the database.
func (ds *DataStore) Save(note *Note, results []Results) error {
	// Generate a unique transaction ID (first 8 chars of UUID)
	txID := fmt.Sprintf("tx-%s", uuid.New().String()[:8])

	// Retry configuration
	maxRetries := 5
	baseDelay := 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Begin a transaction
		tx := ds.DB.Begin()
		if tx.Error != nil {
			lastErr = errors.New(tx.Error).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "start_transaction").
				Context("attempt", fmt.Sprintf("%d", attempt+1)).
				Build()
			continue
		}

		err := func() error {
			defer func() {
				if r := recover(); r != nil {
					tx.Rollback()
				}
			}()

			// Save the note and its associated results within the transaction
			if err := tx.Create(note).Error; err != nil {
				tx.Rollback()
				if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
					return err
				}
				return errors.New(err).
					Component("datastore").
					Category(errors.CategoryDatabase).
					Context("operation", "save_note").
					Context("note_id", fmt.Sprintf("%d", note.ID)).
					Build()
			}

			// Assign the note ID to each result and save them
			for _, result := range results {
				result.NoteID = note.ID
				if err := tx.Create(&result).Error; err != nil {
					tx.Rollback()
					if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
						return err
					}
					return errors.New(err).
						Component("datastore").
						Category(errors.CategoryDatabase).
						Context("operation", "save_result").
						Context("note_id", fmt.Sprintf("%d", note.ID)).
						Build()
				}
			}

			// Commit the transaction
			if err := tx.Commit().Error; err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
					return err
				}
				return errors.New(err).
					Component("datastore").
					Category(errors.CategoryDatabase).
					Context("operation", "commit_transaction").
					Build()
			}

			return nil
		}()

		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				delay := baseDelay * time.Duration(attempt+1)
				log.Printf("[%s] Database locked, retrying in %v (attempt %d/%d)", txID, delay, attempt+1, maxRetries)
				time.Sleep(delay)
				lastErr = err
				continue
			}
			return err
		}

		// Log if retry count is not 0 and transaction was successful
		if attempt > 0 {
			log.Printf("[%s] Database transaction successful after %d attempts", txID, attempt+1)
		}

		// If we get here, the transaction was successful
		return nil
	}

	// If we've exhausted all retries
	return errors.New(lastErr).
		Component("datastore").
		Category(errors.CategoryDatabase).
		Context("operation", "save_transaction").
		Context("transaction_id", txID).
		Context("max_retries", fmt.Sprintf("%d", maxRetries)).
		Build()
}

// Get retrieves a note by its ID from the database.
func (ds *DataStore) Get(id string) (Note, error) {
	// Convert the id from string to integer
	noteID, err := strconv.Atoi(id)
	if err != nil {
		return Note{}, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "convert_note_id").
			Context("id", id).
			Build()
	}

	var note Note
	// Retrieve the note by its ID with Review, Lock, and Comments preloaded
	if err := ds.DB.Preload("Review").Preload("Lock").Preload("Comments", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC") // Order comments by creation time, newest first
	}).First(&note, noteID).Error; err != nil {
		return Note{}, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_note").
			Context("note_id", fmt.Sprintf("%d", noteID)).
			Build()
	}

	// Populate virtual Verified field
	if note.Review != nil {
		note.Verified = note.Review.Verified
	}

	// Populate virtual Locked field
	note.Locked = note.Lock != nil

	return note, nil
}

// Delete removes a note and its associated results from the database.
func (ds *DataStore) Delete(id string) error {
	// Convert the id from string to unsigned integer
	noteID, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "convert_note_id_for_delete").
			Context("id", id).
			Build()
	}

	// Check if the note is locked
	isLocked, err := ds.IsNoteLocked(id)
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "check_note_lock").
			Context("note_id", id).
			Build()
	}
	if isLocked {
		return errors.New(fmt.Errorf("cannot delete note: note is locked")).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "delete_note").
			Context("note_id", id).
			Build()
	}

	// Perform the deletion within a transaction
	return ds.DB.Transaction(func(tx *gorm.DB) error {
		// Delete the full results entry associated with the note
		if err := tx.Where("note_id = ?", noteID).Delete(&Results{}).Error; err != nil {
			return errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "delete_results").
				Context("note_id", fmt.Sprintf("%d", noteID)).
				Build()
		}
		// Delete the note itself
		if err := tx.Delete(&Note{}, noteID).Error; err != nil {
			return errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "delete_note").
				Context("note_id", fmt.Sprintf("%d", noteID)).
				Build()
		}
		return nil
	})
}

// GetNoteClipPath retrieves the path to the audio clip associated with a note.
func (ds *DataStore) GetNoteClipPath(noteID string) (string, error) {
	var clipPath struct {
		ClipName string
	}

	// Retrieve the clip path by note ID
	err := ds.DB.Model(&Note{}).
		Select("clip_name").
		Where("id = ?", noteID).
		First(&clipPath).Error // Use First to retrieve a single record

	if err != nil {
		return "", errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_clip_path").
			Context("note_id", noteID).
			Build()
	}

	return clipPath.ClipName, nil
}

// DeleteNoteClipPath deletes the field representing the path to the audio clip associated with a note.
func (ds *DataStore) DeleteNoteClipPath(noteID string) error {
	// Validate the input parameter
	if noteID == "" {
		return errors.New(fmt.Errorf("invalid note ID: must not be empty")).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "delete_clip_path").
			Build()
	}

	// Update the clip_name field to an empty string for the specified note ID
	err := ds.DB.Model(&Note{}).Where("id = ?", noteID).Update("clip_name", "").Error
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "delete_clip_path").
			Context("note_id", noteID).
			Build()
	}

	// Return nil if no errors occurred, indicating successful execution
	return nil
}

// GetAllNotes retrieves all notes from the database.
func (ds *DataStore) GetAllNotes() ([]Note, error) {
	var notes []Note
	if result := ds.DB.Find(&notes); result.Error != nil {
		return nil, errors.New(result.Error).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_all_notes").
			Build()
	}
	return notes, nil
}

// GetTopBirdsData retrieves the top bird sightings based on a selected date and minimum confidence threshold.
func (ds *DataStore) GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]Note, error) {
	// Define a temporary struct to hold the query results including the count
	type SpeciesCount struct {
		CommonName     string
		ScientificName string
		SpeciesCode    string
		Count          int
		Confidence     float64
		Date           string
		Time           string
	}

	var results []SpeciesCount

	// Get the number of species to report from the dashboard settings
	reportCount := conf.Setting().Realtime.Dashboard.SummaryLimit

	// First, get the count and common names
	query := ds.DB.Table("notes").
		Select("common_name, scientific_name, species_code, COUNT(*) as count, MAX(confidence) as confidence, date, MAX(time) as time").
		Where("date = ? AND confidence >= ?", selectedDate, minConfidenceNormalized).
		Group("common_name, scientific_name, species_code, date").
		Order("count DESC").
		Limit(reportCount)

	if err := query.Scan(&results).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_top_birds_data").
			Context("date", selectedDate).
			Build()
	}

	// Create a single note for each species with the count information
	var notes []Note
	for _, result := range results {
		// Create a representative note for this species
		note := Note{
			CommonName:     result.CommonName,
			ScientificName: result.ScientificName,
			SpeciesCode:    result.SpeciesCode,
			Confidence:     result.Confidence,
			Date:           result.Date,
			Time:           result.Time,
		}

		// Add this note to our results
		notes = append(notes, note)

		// For the web UI, we only need one note per species
		// The hourly counts will be retrieved separately via GetHourlyOccurrences
	}

	return notes, nil
}

type ClipForRemoval struct {
	ID             string
	ScientificName string
	ClipName       string
	NumRecordings  int
}

// GetHourFormat returns the database-specific SQL fragment for formatting a time column as hour.
func (ds *DataStore) GetHourFormat() string {
	// Handling for supported databases: SQLite and MySQL
	switch ds.DB.Dialector.Name() {
	case "sqlite":
		return "strftime('%H', time)"
	case "mysql":
		return "TIME_FORMAT(time, '%H')"
	default:
		// Log or handle unsupported database types
		return ""
	}
}

// GetHourlyOccurrences retrieves hourly occurrences of a specified bird species.
func (ds *DataStore) GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
	var hourlyCounts [24]int
	var results []struct {
		Hour  int
		Count int
	}

	hourFormat := ds.GetHourFormat()

	err := ds.DB.Model(&Note{}).
		Select(fmt.Sprintf("%s as hour, COUNT(*) as count", hourFormat)).
		Where("date = ? AND common_name = ? AND confidence >= ?", date, commonName, minConfidenceNormalized).
		Group(hourFormat).
		Scan(&results).Error

	if err != nil {
		return hourlyCounts, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_hourly_occurrences").
			Context("date", date).
			Context("species", commonName).
			Build()
	}

	for _, result := range results {
		if result.Hour >= 0 && result.Hour < 24 {
			hourlyCounts[result.Hour] = result.Count
		}
	}

	return hourlyCounts, nil
}

// SpeciesDetections retrieves bird species detections for a specific date and time period.
func (ds *DataStore) SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit, offset int) ([]Note, error) {
	sortOrder := sortAscendingString(sortAscending)

	query := ds.DB.Preload("Review").Preload("Lock").Preload("Comments", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC") // Order comments by creation time, newest first
	}).Where("common_name = ? AND date = ?", species, date)
	if hour != "" {
		startTime, endTime, crossesMidnight := getHourRange(hour, duration)
		if crossesMidnight {
			// For the last hour(s) of the day, use <= instead of < to include the end time
			query = query.Where("time >= ? AND time <= ?", startTime, endTime)
		} else {
			query = query.Where("time >= ? AND time < ?", startTime, endTime)
		}
	}

	query = query.Order("id " + sortOrder).
		Limit(limit).
		Offset(offset)

	var detections []Note
	err := query.Find(&detections).Error

	// Populate virtual fields
	for i := range detections {
		if detections[i].Review != nil {
			detections[i].Verified = detections[i].Review.Verified
		}
		detections[i].Locked = detections[i].Lock != nil
	}

	return detections, err
}

// GetLastDetections retrieves the most recent bird detections.
func (ds *DataStore) GetLastDetections(numDetections int) ([]Note, error) {
	var notes []Note
	now := time.Now()

	// Retrieve the most recent detections based on the ID in descending order
	if result := ds.DB.Preload("Review").Preload("Lock").Preload("Comments", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC") // Order comments by creation time, newest first
	}).Order("id DESC").Limit(numDetections).Find(&notes); result.Error != nil {
		return nil, errors.New(result.Error).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_last_detections").
			Context("limit", fmt.Sprintf("%d", numDetections)).
			Build()
	}

	// Populate virtual fields
	for i := range notes {
		if notes[i].Review != nil {
			notes[i].Verified = notes[i].Review.Verified
		}
		notes[i].Locked = notes[i].Lock != nil
	}

	elapsed := time.Since(now)
	log.Printf("Retrieved %d detections in %v", numDetections, elapsed)

	return notes, nil
}

// GetLastDetections retrieves all detected species.
func (ds *DataStore) GetAllDetectedSpecies() ([]Note, error) {
	var results []Note

	err := ds.DB.Table("notes").
		Select("scientific_name").
		Group("scientific_name").
		Scan(&results).Error

	if err != nil {
		return results, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_all_detected_species").
			Build()
	}
	return results, nil
}

// SearchNotes performs a search on notes with optional sorting, pagination, and limits.
func (ds *DataStore) SearchNotes(query string, sortAscending bool, limit, offset int) ([]Note, error) {
	var notes []Note
	sortOrder := sortAscendingString(sortAscending)

	err := ds.DB.Preload("Review").Preload("Lock").Preload("Comments", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC") // Order comments by creation time, newest first
	}).Where("common_name LIKE ? OR scientific_name LIKE ?", "%"+query+"%", "%"+query+"%").
		Order("id " + sortOrder).
		Limit(limit).
		Offset(offset).
		Find(&notes).Error

	// Populate virtual fields
	for i := range notes {
		if notes[i].Review != nil {
			notes[i].Verified = notes[i].Review.Verified
		}
		notes[i].Locked = notes[i].Lock != nil
	}

	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "search_notes").
			Context("query", query).
			Build()
	}
	return notes, nil
}

// SaveDailyEvents saves daily events data to the database.
func (ds *DataStore) SaveDailyEvents(dailyEvents *DailyEvents) error {
	// Use upsert to handle the unique date constraint
	result := ds.DB.Where("date = ?", dailyEvents.Date).
		Assign(*dailyEvents).
		FirstOrCreate(dailyEvents)

	if result.Error != nil {
		return errors.New(result.Error).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "save_daily_events").
			Context("date", dailyEvents.Date).
			Build()
	}

	return nil
}

// GetDailyEvents retrieves daily events data by date from the database.
func (ds *DataStore) GetDailyEvents(date string) (DailyEvents, error) {
	var dailyEvents DailyEvents
	if err := ds.DB.Where("date = ?", date).First(&dailyEvents).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dailyEvents, nil // Return empty struct for not found
		}
		return dailyEvents, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_daily_events").
			Context("date", date).
			Build()
	}
	return dailyEvents, nil
}

// SaveHourlyWeather saves hourly weather data to the database.
func (ds *DataStore) SaveHourlyWeather(hourlyWeather *HourlyWeather) error {
	// Basic validation
	if hourlyWeather.Time.IsZero() {
		return errors.Newf("invalid time value").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "save_hourly_weather").
			Build()
	}

	// Use upsert to avoid duplicates for the same timestamp
	result := ds.DB.Where("time = ?", hourlyWeather.Time).
		Assign(*hourlyWeather).
		FirstOrCreate(hourlyWeather)

	if result.Error != nil {
		return errors.New(result.Error).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "save_hourly_weather").
			Build()
	}

	return nil
}

// GetHourlyWeather retrieves hourly weather data by date from the database.
func (ds *DataStore) GetHourlyWeather(date string) ([]HourlyWeather, error) {
	var hourlyWeather []HourlyWeather

	err := ds.DB.Where("DATE(time) = ?", date).
		Order("time ASC").
		Find(&hourlyWeather).Error

	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_hourly_weather").
			Context("date", date).
			Build()
	}

	return hourlyWeather, nil
}

// LatestHourlyWeather retrieves the latest hourly weather entry from the database.
func (ds *DataStore) LatestHourlyWeather() (*HourlyWeather, error) {
	var weather HourlyWeather

	err := ds.DB.Order("time DESC").First(&weather).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.Newf("no weather data found").
				Component("datastore").
				Category(errors.CategoryValidation).
				Context("operation", "get_latest_weather").
				Build()
		}
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_latest_weather").
			Build()
	}

	return &weather, nil
}

// GetHourlyDetections retrieves bird detections for a specific date and hour.
func (ds *DataStore) GetHourlyDetections(date, hour string, duration, limit, offset int) ([]Note, error) {
	var detections []Note

	startTime, endTime, crossesMidnight := getHourRange(hour, duration)
	query := ds.DB.Preload("Review").Preload("Lock").Preload("Comments", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC") // Order comments by creation time, newest first
	})

	if crossesMidnight {
		// For the last hour(s) of the day, use <= instead of < to include the end time
		query = query.Where("date = ? AND time >= ? AND time <= ?", date, startTime, endTime)
	} else {
		query = query.Where("date = ? AND time >= ? AND time < ?", date, startTime, endTime)
	}

	err := query.
		Order("time ASC").
		Limit(limit).
		Offset(offset).
		Find(&detections).Error

	// Populate virtual fields
	for i := range detections {
		if detections[i].Review != nil {
			detections[i].Verified = detections[i].Review.Verified
		}
		detections[i].Locked = detections[i].Lock != nil
	}

	if err != nil {
		return detections, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_hourly_detections").
			Context("date", date).
			Context("hour", hour).
			Build()
	}
	return detections, nil
}

// CountSpeciesDetections counts the number of detections for a specific species, date, and hour.
func (ds *DataStore) CountSpeciesDetections(species, date, hour string, duration int) (int64, error) {
	var count int64
	query := ds.DB.Model(&Note{}).Where("common_name = ? AND date = ?", species, date)

	if hour != "" {
		startTime, endTime, crossesMidnight := getHourRange(hour, duration)
		if crossesMidnight {
			// For the last hour(s) of the day, use <= instead of < to include the end time
			query = query.Where("time >= ? AND time <= ?", startTime, endTime)
		} else {
			query = query.Where("time >= ? AND time < ?", startTime, endTime)
		}
	}

	err := query.Count(&count).Error
	if err != nil {
		return 0, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "count_species_detections").
			Context("species", species).
			Context("date", date).
			Build()
	}

	return count, nil
}

// CountSearchResults counts the number of search results for a given query.
func (ds *DataStore) CountSearchResults(query string) (int64, error) {
	var count int64
	err := ds.DB.Model(&Note{}).
		Where("common_name LIKE ? OR scientific_name LIKE ?", "%"+query+"%", "%"+query+"%").
		Count(&count).Error

	if err != nil {
		return 0, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "count_search_results").
			Context("query", query).
			Build()
	}

	return count, nil
}

// UpdateNote updates specific fields of a note. It validates the input parameters
// and returns appropriate errors if the note doesn't exist or if the update fails.
func (ds *DataStore) UpdateNote(id string, updates map[string]interface{}) error {
	if id == "" {
		return errors.Newf("invalid id: must not be empty").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "update_note").
			Build()
	}
	if len(updates) == 0 {
		return errors.Newf("no updates provided").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "update_note").
			Build()
	}

	result := ds.DB.Model(&Note{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return errors.New(result.Error).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "update_note").
			Context("note_id", id).
			Build()
	}
	if result.RowsAffected == 0 {
		return errors.Newf("note not found").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "update_note").
			Context("note_id", id).
			Build()
	}

	return nil
}

// GetNoteReview retrieves the review status for a note
func (ds *DataStore) GetNoteReview(noteID string) (*NoteReview, error) {
	var review NoteReview
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "get_note_review").
			Context("note_id", noteID).
			Build()
	}

	// Use Session to temporarily modify logger config for this query
	err = ds.DB.Session(&gorm.Session{
		Logger: ds.DB.Logger.LogMode(logger.Silent),
	}).Where("note_id = ?", id).First(&review).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // Return nil if no review exists
		}
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_note_review").
			Context("note_id", noteID).
			Build()
	}

	return &review, nil
}

// SaveNoteReview saves or updates a note review
func (ds *DataStore) SaveNoteReview(review *NoteReview) error {
	// Use upsert operation to either create or update the review
	result := ds.DB.Where("note_id = ?", review.NoteID).
		Assign(*review).
		FirstOrCreate(review)

	if result.Error != nil {
		return errors.New(result.Error).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "save_note_review").
			Context("note_id", fmt.Sprintf("%d", review.NoteID)).
			Build()
	}

	return nil
}

// GetNoteComments retrieves all comments for a note
func (ds *DataStore) GetNoteComments(noteID string) ([]NoteComment, error) {
	var comments []NoteComment
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "get_note_comments").
			Context("note_id", noteID).
			Build()
	}

	err = ds.DB.Where("note_id = ?", id).Order("created_at DESC").Find(&comments).Error
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_note_comments").
			Context("note_id", noteID).
			Build()
	}

	return comments, nil
}

// SaveNoteComment saves a new comment for a note
func (ds *DataStore) SaveNoteComment(comment *NoteComment) error {
	// Validate input
	if comment == nil {
		return errors.Newf("comment cannot be nil").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "save_note_comment").
			Build()
	}
	if comment.NoteID == 0 {
		return errors.Newf("note ID cannot be zero").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "save_note_comment").
			Build()
	}
	// Entry can be empty as comments are optional, but if provided, check length
	if len(comment.Entry) > 1000 {
		return errors.Newf("comment entry exceeds maximum length").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "save_note_comment").
			Context("entry_length", fmt.Sprintf("%d", len(comment.Entry))).
			Build()
	}

	if err := ds.DB.Create(comment).Error; err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "save_note_comment").
			Context("note_id", fmt.Sprintf("%d", comment.NoteID)).
			Build()
	}
	return nil
}

// DeleteNoteComment deletes a comment
func (ds *DataStore) DeleteNoteComment(commentID string) error {
	id, err := strconv.ParseUint(commentID, 10, 32)
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "delete_note_comment").
			Context("comment_id", commentID).
			Build()
	}

	if err := ds.DB.Delete(&NoteComment{}, id).Error; err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "delete_note_comment").
			Context("comment_id", commentID).
			Build()
	}
	return nil
}

// UpdateNoteComment updates an existing comment's entry
func (ds *DataStore) UpdateNoteComment(commentID, entry string) error {
	id, err := strconv.ParseUint(commentID, 10, 32)
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "update_note_comment").
			Context("comment_id", commentID).
			Build()
	}

	result := ds.DB.Model(&NoteComment{}).Where("id = ?", id).Updates(map[string]interface{}{
		"entry":      entry,
		"updated_at": time.Now(),
	})

	if result.Error != nil {
		return errors.New(result.Error).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "update_note_comment").
			Context("comment_id", commentID).
			Build()
	}

	if result.RowsAffected == 0 {
		return errors.Newf("comment not found").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "update_note_comment").
			Context("comment_id", commentID).
			Build()
	}

	return nil
}

// getHourRange returns the start and end times for a given hour and duration.
// Returns a boolean indicating whether the range crosses midnight within the same date.
func getHourRange(hour string, duration int) (startTime, endTime string, crossesMidnight bool) {
	startHour, _ := strconv.Atoi(hour)
	endHour := startHour + duration

	startTime = fmt.Sprintf("%02d:00:00", startHour)
	crossesMidnight = endHour >= 24

	if crossesMidnight {
		// For the last hour(s) of the day, set endTime to end of day
		endTime = "23:59:59"
	} else {
		endTime = fmt.Sprintf("%02d:00:00", endHour)
	}

	return startTime, endTime, crossesMidnight
}

// sortOrderAscendingString returns "ASC" or "DESC" based on the boolean input.
func sortAscendingString(asc bool) string {
	if asc {
		return "ASC"
	}
	return "DESC"
}

// Transaction executes a function within a transaction.
func (ds *DataStore) Transaction(fc func(tx *gorm.DB) error) error {
	if fc == nil {
		return errors.Newf("transaction function cannot be nil").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "transaction").
			Build()
	}
	return ds.DB.Transaction(fc)
}

// GetNoteLock retrieves the lock status for a note
func (ds *DataStore) GetNoteLock(noteID string) (*NoteLock, error) {
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "get_note_lock").
			Context("note_id", noteID).
			Build()
	}

	var lock NoteLock
	// Check if the lock exists and get its details in one query
	err = ds.DB.Where("note_id = ?", id).First(&lock).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // Return nil if no lock exists
		}
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_note_lock").
			Context("note_id", noteID).
			Build()
	}

	return &lock, nil
}

// IsNoteLocked checks if a note is locked
func (ds *DataStore) IsNoteLocked(noteID string) (bool, error) {
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return false, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "is_note_locked").
			Context("note_id", noteID).
			Build()
	}

	var count int64
	err = ds.DB.Model(&NoteLock{}).
		Where("note_id = ?", id).
		Count(&count).
		Error

	if err != nil {
		return false, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "is_note_locked").
			Context("note_id", noteID).
			Build()
	}

	return count > 0, nil
}

// LockNote creates or updates a lock for a note
func (ds *DataStore) LockNote(noteID string) error {
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "lock_note").
			Context("note_id", noteID).
			Build()
	}

	// Generate a unique transaction ID (first 8 chars of UUID)
	txID := fmt.Sprintf("tx-%s", uuid.New().String()[:8])

	// Retry configuration
	maxRetries := 5
	baseDelay := 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Use upsert operation to either create or update the lock
		lock := &NoteLock{
			NoteID:   uint(id),
			LockedAt: time.Now(),
		}

		result := ds.DB.Where("note_id = ?", id).
			Assign(*lock).
			FirstOrCreate(lock)

		if result.Error != nil {
			if strings.Contains(strings.ToLower(result.Error.Error()), "database is locked") {
				delay := baseDelay * time.Duration(attempt+1)
				log.Printf("[%s] Database locked, retrying in %v (attempt %d/%d)", txID, delay, attempt+1, maxRetries)
				time.Sleep(delay)
				lastErr = result.Error
				continue
			}
			return errors.New(result.Error).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "lock_note").
				Context("note_id", noteID).
				Build()
		}

		// If we get here, the transaction was successful
		if attempt > 0 {
			log.Printf("[%s] Database transaction successful after %d attempts", txID, attempt+1)
		}
		return nil
	}

	return errors.New(lastErr).
		Component("datastore").
		Category(errors.CategoryDatabase).
		Context("operation", "lock_note").
		Context("note_id", noteID).
		Context("transaction_id", txID).
		Context("max_retries", fmt.Sprintf("%d", maxRetries)).
		Build()
}

// UnlockNote removes a lock from a note
func (ds *DataStore) UnlockNote(noteID string) error {
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "unlock_note").
			Context("note_id", noteID).
			Build()
	}

	// Generate a unique transaction ID (first 8 chars of UUID)
	txID := fmt.Sprintf("tx-%s", uuid.New().String()[:8])

	// Retry configuration
	maxRetries := 5
	baseDelay := 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		// First check if the lock exists
		exists, err := ds.IsNoteLocked(noteID)
		if err != nil {
			return errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "unlock_note_check_existence").
				Context("note_id", noteID).
				Build()
		}
		if !exists {
			// Lock doesn't exist, nothing to unlock
			return nil
		}

		result := ds.DB.Where("note_id = ?", id).Delete(&NoteLock{})
		if result.Error != nil {
			if strings.Contains(strings.ToLower(result.Error.Error()), "database is locked") {
				delay := baseDelay * time.Duration(attempt+1)
				log.Printf("[%s] Database locked, retrying in %v (attempt %d/%d)", txID, delay, attempt+1, maxRetries)
				time.Sleep(delay)
				lastErr = result.Error
				continue
			}
			return errors.New(result.Error).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "unlock_note").
				Context("note_id", noteID).
				Build()
		}

		// If we get here, the transaction was successful
		if attempt > 0 {
			log.Printf("[%s] Database transaction successful after %d attempts", txID, attempt+1)
		}
		return nil
	}

	return errors.New(lastErr).
		Component("datastore").
		Category(errors.CategoryDatabase).
		Context("operation", "unlock_note").
		Context("note_id", noteID).
		Context("transaction_id", txID).
		Context("max_retries", fmt.Sprintf("%d", maxRetries)).
		Build()
}

// GetImageCache retrieves an image cache entry by scientific name and provider
func (ds *DataStore) GetImageCache(query ImageCacheQuery) (*ImageCache, error) {
	var cache ImageCache
	if query.ScientificName == "" || query.ProviderName == "" {
		return nil, errors.Newf("scientific name and provider name must be provided").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "get_image_cache").
			Build()
	}
	// Use Session to disable logging for this query
	if err := ds.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		Where("scientific_name = ? AND provider_name = ?", query.ScientificName, query.ProviderName).First(&cache).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // Return nil, nil when record is not found
		}
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_image_cache").
			Context("scientific_name", query.ScientificName).
			Context("provider", query.ProviderName).
			Build()
	}
	return &cache, nil
}

// SaveImageCache saves an image cache entry to the database
func (ds *DataStore) SaveImageCache(cache *ImageCache) error {
	if cache.ProviderName == "" {
		return errors.Newf("provider name cannot be empty").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "save_image_cache").
			Build()
	}
	if cache.ScientificName == "" {
		return errors.Newf("scientific name cannot be empty").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "save_image_cache").
			Build()
	}

	// Use Clauses(clause.OnConflict...) to perform an UPSERT operation
	// Update all columns except primary key on conflict
	if err := ds.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider_name"}, {Name: "scientific_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"url", "license_name", "license_url", "author_name", "author_url", "cached_at"}),
	}).Create(cache).Error; err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "save_image_cache").
			Context("scientific_name", cache.ScientificName).
			Context("provider", cache.ProviderName).
			Build()
	}
	return nil
}

// GetAllImageCaches retrieves all image cache entries for a specific provider
func (ds *DataStore) GetAllImageCaches(providerName string) ([]ImageCache, error) {
	var caches []ImageCache
	if err := ds.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		Where("provider_name = ?", providerName).Find(&caches).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_all_image_caches").
			Context("provider", providerName).
			Build()
	}
	return caches, nil
}

// GetImageCacheBatch retrieves multiple image cache entries for a provider in a single query
func (ds *DataStore) GetImageCacheBatch(providerName string, scientificNames []string) (map[string]*ImageCache, error) {
	if providerName == "" {
		return nil, errors.Newf("provider name must be provided").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "get_image_cache_batch").
			Build()
	}

	if len(scientificNames) == 0 {
		return make(map[string]*ImageCache), nil
	}

	var caches []ImageCache
	// Use Session to disable logging for this query and use IN clause for batch lookup
	if err := ds.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		Where("provider_name = ? AND scientific_name IN ?", providerName, scientificNames).
		Find(&caches).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_image_cache_batch").
			Context("provider", providerName).
			Context("batch_size", fmt.Sprintf("%d", len(scientificNames))).
			Build()
	}

	// Convert to map for easy lookup
	result := make(map[string]*ImageCache, len(caches))
	for i := range caches {
		result[caches[i].ScientificName] = &caches[i]
	}

	return result, nil
}

// GetLockedNotesClipPaths retrieves a list of clip paths from all locked notes
func (ds *DataStore) GetLockedNotesClipPaths() ([]string, error) {
	var clipPaths []string

	// Query to get clip paths from notes that have an associated lock
	err := ds.DB.Model(&Note{}).
		Joins("JOIN note_locks ON notes.id = note_locks.note_id").
		Where("notes.clip_name != ''"). // Only include notes that have a clip path
		Pluck("notes.clip_name", &clipPaths).
		Error

	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_locked_notes_clip_paths").
			Build()
	}

	return clipPaths, nil
}

// CountHourlyDetections counts the number of detections for a specific date and hour.
func (ds *DataStore) CountHourlyDetections(date, hour string, duration int) (int64, error) {
	var count int64
	startTime, endTime, crossesMidnight := getHourRange(hour, duration)
	query := ds.DB.Model(&Note{})

	if crossesMidnight {
		// For the last hour(s) of the day, use <= instead of < to include the end time
		query = query.Where("date = ? AND time >= ? AND time <= ?", date, startTime, endTime)
	} else {
		query = query.Where("date = ? AND time >= ? AND time < ?", date, startTime, endTime)
	}

	err := query.Count(&count).Error

	if err != nil {
		return 0, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "count_hourly_detections").
			Context("date", date).
			Context("hour", hour).
			Build()
	}

	return count, nil
}

// SearchFilters defines parameters for filtering detection records
type SearchFilters struct {
	Species        string
	DateStart      string
	DateEnd        string
	ConfidenceMin  float64
	ConfidenceMax  float64
	VerifiedOnly   bool
	UnverifiedOnly bool
	LockedOnly     bool
	UnlockedOnly   bool
	Device         string
	TimeOfDay      string // "any", "day", "night", "sunrise", "sunset"
	Page           int
	PerPage        int
	SortBy         string
	Ctx            context.Context // Add context for cancellation/timeout
}

// sanitise validates and normalises the search filters, returning an error for invalid combinations.
func (f *SearchFilters) sanitise() error {
	if f.Page <= 0 {
		f.Page = 1
	}
	// Default and cap PerPage
	if f.PerPage <= 0 || f.PerPage > 200 {
		f.PerPage = 20 // Default/max page size
	}
	// Default ConfidenceMax if not set or zero
	if f.ConfidenceMax == 0 {
		f.ConfidenceMax = 1.0
	}
	// Validate confidence range
	if f.ConfidenceMin > f.ConfidenceMax {
		return errors.Newf("confidence_min must be <= confidence_max").
			Component("datastore").
			Category(errors.CategoryValidation).
			Build()
	}
	// Validate mutually exclusive Verified flags
	if f.VerifiedOnly && f.UnverifiedOnly {
		return errors.Newf("verified_only and unverified_only cannot both be true").
			Component("datastore").
			Category(errors.CategoryValidation).
			Build()
	}
	// Validate mutually exclusive Locked flags
	if f.LockedOnly && f.UnlockedOnly {
		return errors.Newf("locked_only and unlocked_only cannot both be true").
			Component("datastore").
			Category(errors.CategoryValidation).
			Build()
	}
	// Validate TimeOfDay
	switch f.TimeOfDay {
	case "", "any", "day", "night", "sunrise", "sunset": // Add sunrise/sunset
		// Valid values
	default:
		return errors.Newf("invalid time_of_day value, must be 'any', 'day', 'night', 'sunrise', or 'sunset'").
			Component("datastore").
			Category(errors.CategoryValidation).
			Build()
	}
	return nil
}

// applySpeciesFilter applies the species filter to a GORM query
func applySpeciesFilter(query *gorm.DB, species string) *gorm.DB {
	if species != "" {
		likeParam := "%" + species + "%"
		return query.Where("notes.scientific_name LIKE ? OR notes.common_name LIKE ?", likeParam, likeParam)
	}
	return query
}

// applyCommonFilters applies common search filters to a GORM query
func applyCommonFilters(query *gorm.DB, filters *SearchFilters, ds *DataStore) *gorm.DB {
	query = applySpeciesFilter(query, filters.Species)

	if filters.DateStart != "" {
		query = query.Where("notes.date >= ?", filters.DateStart)
	}
	if filters.DateEnd != "" {
		query = query.Where("notes.date <= ?", filters.DateEnd)
	}

	query = query.Where("notes.confidence >= ? AND notes.confidence <= ?",
		filters.ConfidenceMin, filters.ConfidenceMax)

	if filters.VerifiedOnly {
		query = query.Where("note_reviews.verified = ?", "correct")
	} else if filters.UnverifiedOnly {
		// Handle NULL case explicitly for unverified
		query = query.Where("(note_reviews.verified IS NULL OR (note_reviews.verified != ? AND note_reviews.verified != ?))", "correct", "false_positive")
	}

	if filters.LockedOnly {
		query = query.Where("note_locks.id IS NOT NULL")
	} else if filters.UnlockedOnly {
		query = query.Where("note_locks.id IS NULL")
	}

	// Debug logging for TimeOfDay filter
	log.Printf("DEBUG: TimeOfDay filter value: '%s', SunCalc is nil: %v, DB is nil: %v",
		filters.TimeOfDay, ds.SunCalc == nil, ds.DB == nil)

	// --- Dynamic TimeOfDay Filter ---
	if (filters.TimeOfDay == "day" || filters.TimeOfDay == "night" || filters.TimeOfDay == "sunrise" || filters.TimeOfDay == "sunset") && ds.SunCalc != nil && ds.DB != nil { // Include sunrise/sunset
		dateConditions, err := buildTimeOfDayConditions(filters, ds.SunCalc, ds.DB)
		switch {
		case err != nil:
			log.Printf("WARN: Failed to build TimeOfDay conditions: %v. Skipping filter.", err)
		case len(dateConditions) > 0:
			log.Printf("DEBUG: Successfully built %d TimeOfDay conditions for filter: '%s'", len(dateConditions), filters.TimeOfDay)
			combinedCondition := ds.DB.Where(dateConditions[0])
			for i := 1; i < len(dateConditions); i++ {
				combinedCondition = combinedCondition.Or(dateConditions[i])
			}
			query = query.Where(combinedCondition)
		default:
			log.Printf("DEBUG: No TimeOfDay conditions were generated for filter: '%s'", filters.TimeOfDay)
		}
	} else {
		log.Printf("DEBUG: Skipping TimeOfDay filter: filters.TimeOfDay=%s, SunCalc==nil=%v, DB==nil=%v",
			filters.TimeOfDay, ds.SunCalc == nil, ds.DB == nil)
	} // --- End Dynamic TimeOfDay Filter ---

	if filters.Device != "" {
		query = query.Where("notes.source_node LIKE ?", "%"+filters.Device+"%")
	}

	return query
}

// buildTimeOfDayConditions generates the WHERE conditions for day/night/sunrise/sunset filtering
func buildTimeOfDayConditions(filters *SearchFilters, sc *suncalc.SunCalc, db *gorm.DB) ([]*gorm.DB, error) {
	startDateStr := filters.DateStart
	endDateStr := filters.DateEnd

	// Default to a reasonable date range if no dates are provided
	switch {
	case startDateStr == "" && endDateStr == "":
		// Use past year instead of 90 days
		today := time.Now()
		endDateStr = today.Format("2006-01-02")
		startDateStr = today.AddDate(-1, 0, 0).Format("2006-01-02") // 1 year ago
		log.Printf("INFO: TimeOfDay filter applied without date range, defaulting to last year (%s to %s)",
			startDateStr, endDateStr)
	case startDateStr == "":
		// If only end date is provided, use 1 year before that date as start
		endDate, err := time.Parse("2006-01-02", endDateStr)
		if err == nil {
			startDateStr = endDate.AddDate(-1, 0, 0).Format("2006-01-02")
		} else {
			startDateStr = endDateStr // Fallback if parsing fails
		}
	case endDateStr == "":
		// If only start date is provided, use that date +1 year or today (whichever is earlier)
		startDate, err := time.Parse("2006-01-02", startDateStr)
		if err == nil {
			endDate := startDate.AddDate(1, 0, 0)
			today := time.Now()
			if endDate.After(today) {
				endDate = today
			}
			endDateStr = endDate.Format("2006-01-02")
		} else {
			endDateStr = startDateStr // Fallback if parsing fails
		}
	}

	startDate, err := time.Parse("2006-01-02", startDateStr)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "build_time_of_day_conditions").
			Context("start_date", startDateStr).
			Build()
	}
	endDate, err := time.Parse("2006-01-02", endDateStr)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "build_time_of_day_conditions").
			Context("end_date", endDateStr).
			Build()
	}

	if endDate.Before(startDate) {
		return nil, errors.Newf("end date cannot be before start date").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "build_time_of_day_conditions").
			Context("start_date", startDateStr).
			Context("end_date", endDateStr).
			Build()
	}

	// Limit date range to avoid excessively long queries (increased to 365 days)
	if endDate.Sub(startDate).Hours() > 365*24 {
		return nil, errors.Newf("date range for TimeOfDay filter cannot exceed 365 days").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "build_time_of_day_conditions").
			Context("start_date", startDateStr).
			Context("end_date", endDateStr).
			Build()
	}

	var conditions []*gorm.DB
	window := time.Duration(sunriseSetWindowMinutes) * time.Minute // Define window for sunrise/sunset

	// Optimization: Group dates by week and calculate sun times once per week
	// Store weekly sun calculations
	type WeeklySunTimes struct {
		year      int
		week      int
		sunTimes  suncalc.SunEventTimes
		dateRange []time.Time
	}

	// Map to store our weekly calculations
	weeklySunCache := make(map[string]*WeeklySunTimes)

	// First pass: group dates by week
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		year, week := d.ISOWeek()
		key := fmt.Sprintf("%d-%d", year, week)

		if _, exists := weeklySunCache[key]; !exists {
			// Create a new entry for this week
			weeklySunCache[key] = &WeeklySunTimes{
				year:      year,
				week:      week,
				dateRange: []time.Time{},
			}
		}

		// Add this date to the week's date range
		weeklySunCache[key].dateRange = append(weeklySunCache[key].dateRange, d)
	}

	// Second pass: calculate sun times for one representative day per week
	for _, weekData := range weeklySunCache {
		// Find the middle day of the week as representative
		representativeDay := weekData.dateRange[len(weekData.dateRange)/2]
		sunTimes, err := sc.GetSunEventTimes(representativeDay)
		if err != nil {
			log.Printf("WARN: Could not get sun times for week %d-%d: %v. Skipping week for TimeOfDay filter.",
				weekData.year, weekData.week, err)
			continue
		}
		weekData.sunTimes = sunTimes
	}

	// Third pass: build conditions for each date using the weekly sun times
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		year, week := d.ISOWeek()
		key := fmt.Sprintf("%d-%d", year, week)

		// Get the sun times for this week
		weekData, exists := weeklySunCache[key]
		if !exists || weekData == nil {
			log.Printf("WARN: No sun times found for week %d-%d. Skipping date %s for TimeOfDay filter.",
				year, week, dateStr)
			continue
		}

		sunTimes := weekData.sunTimes
		sunriseStr := sunTimes.Sunrise.Format("15:04:05")
		sunsetStr := sunTimes.Sunset.Format("15:04:05")

		var condition *gorm.DB
		switch filters.TimeOfDay {
		case "day":
			// Exclude the sunrise and sunset windows (30 minutes before and after)
			sunriseEnd := sunTimes.Sunrise.Add(window).Format("15:04:05")
			sunsetStart := sunTimes.Sunset.Add(-window).Format("15:04:05")
			// Time should be after sunrise window but before sunset window
			condition = db.Where("notes.date = ? AND notes.time > ? AND notes.time < ?", dateStr, sunriseEnd, sunsetStart)
		case "night":
			condition = db.Where("notes.date = ? AND (notes.time < ? OR notes.time > ?)", dateStr, sunriseStr, sunsetStr)
		case "sunrise":
			sunriseStart := sunTimes.Sunrise.Add(-window).Format("15:04:05")
			sunriseEnd := sunTimes.Sunrise.Add(window).Format("15:04:05")
			condition = db.Where("notes.date = ? AND notes.time >= ? AND notes.time <= ?", dateStr, sunriseStart, sunriseEnd)
		case "sunset":
			sunsetStart := sunTimes.Sunset.Add(-window).Format("15:04:05")
			sunsetEnd := sunTimes.Sunset.Add(window).Format("15:04:05")
			condition = db.Where("notes.date = ? AND notes.time >= ? AND notes.time <= ?", dateStr, sunsetStart, sunsetEnd)
		default:
			// Should not happen due to sanitise, but skip if it does
			continue
		}
		conditions = append(conditions, condition)
	}

	// Log summary of how many conditions were created
	log.Printf("INFO: Created %d date-specific conditions for TimeOfDay filter over a %d day range",
		len(conditions), int(endDate.Sub(startDate).Hours()/24)+1)

	return conditions, nil
}

// SearchDetections retrieves detections based on the given filters
func (ds *DataStore) SearchDetections(filters *SearchFilters) ([]DetectionRecord, int, error) {
	// Sanitise filters first
	if err := filters.sanitise(); err != nil {
		return nil, 0, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "search_detections").
			Build()
	}

	// Build the query with GORM query builder
	query := ds.DB.Table("notes")

	// Select necessary fields, including potentially null fields from joins
	query = query.Select("notes.id, notes.date, notes.time, notes.scientific_name, notes.common_name, notes.confidence, " +
		"notes.latitude, notes.longitude, notes.clip_name, notes.source, notes.source_node, " +
		"note_reviews.verified AS review_verified, " + // Select review status
		"note_locks.id IS NOT NULL AS is_locked") // Select lock status as boolean

	// Use LEFT JOINs to fetch optional review and lock data
	query = query.Joins("LEFT JOIN note_reviews ON notes.id = note_reviews.note_id")
	query = query.Joins("LEFT JOIN note_locks ON notes.id = note_locks.note_id")

	// Apply filters - Pass ds to applyCommonFilters now
	query = applyCommonFilters(query, filters, ds)

	// --- Count Query ---
	// Create a separate query for counting to avoid issues with GROUP BY if added later
	countQuery := ds.DB.Table("notes").
		Joins("LEFT JOIN note_reviews ON notes.id = note_reviews.note_id").
		Joins("LEFT JOIN note_locks ON notes.id = note_locks.note_id")

	// Apply the *same* filters to the count query - Pass ds here too
	countQuery = applyCommonFilters(countQuery, filters, ds)

	// Get total count using the separate count query
	var total int64
	err := countQuery.WithContext(filters.Ctx).Count(&total).Error // Apply context
	if err != nil {
		return nil, 0, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "search_detections_count").
			Build()
	}
	// --- End Count Query ---

	// Apply sorting to the main query
	switch filters.SortBy {
	case "date_asc":
		query = query.Order("notes.date ASC, notes.time ASC")
	case "species_asc":
		query = query.Order("notes.common_name ASC")
	case "confidence_desc":
		query = query.Order("notes.confidence DESC")
	default:
		query = query.Order("notes.date DESC, notes.time DESC") // Default sort by date, newest first
	}

	// Apply pagination (PerPage and Page are already sanitised)
	limit := filters.PerPage
	offset := (filters.Page - 1) * limit
	query = query.Limit(limit).Offset(offset)

	// Define a struct to scan results into, including joined fields
	type ScannedResult struct {
		ID             uint
		Date           string
		Time           string
		ScientificName string
		CommonName     string
		Confidence     float64
		Latitude       float64
		Longitude      float64
		ClipName       string
		Source         string
		SourceNode     string
		ReviewVerified *string // Use pointer to handle NULL for review status
		IsLocked       bool    // Boolean result from IS NOT NULL
	}

	// Execute the query
	var scannedResults []ScannedResult
	if err := query.WithContext(filters.Ctx).Scan(&scannedResults).Error; err != nil { // Apply context
		return nil, 0, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "search_detections_query").
			Build()
	}

	// Convert ScannedResult objects to DetectionRecord objects
	results := make([]DetectionRecord, 0, len(scannedResults))
	for i := range scannedResults {
		scanned := &scannedResults[i] // Use pointer to avoid copy
		// Determine verification status
		verifiedStatus := "unverified" // Default
		if scanned.ReviewVerified != nil {
			verifiedStatus = *scanned.ReviewVerified
		}

		// Parse timestamp string to time.Time
		timestamp, err := time.Parse("2006-01-02 15:04:05", scanned.Date+" "+scanned.Time)
		if err != nil {
			log.Printf("Warning: Failed to parse timestamp '%s %s' for note ID %d: %v. Using current time.", scanned.Date, scanned.Time, scanned.ID, err)
			timestamp = time.Now() // Fallback
		}

		// Calculate week from date if needed
		week := 0
		if t, err := time.Parse("2006-01-02", scanned.Date); err == nil {
			_, week = t.ISOWeek()
		}

		// Calculate time of day
		timeOfDay := "unknown"
		if ds.SunCalc != nil {
			// Get date string for cache key
			dateStr := scanned.Date

			// Get or calculate sun times for this date
			sunEvents, err := ds.getSunEventsForDate(dateStr, timestamp)
			if err == nil {
				// Convert all times to the same format for comparison
				detTime := timestamp.Format("15:04:05")
				sunriseTime := sunEvents.Sunrise.Format("15:04:05")
				sunsetTime := sunEvents.Sunset.Format("15:04:05")

				// Define sunrise/sunset window (using constant)
				window := time.Duration(sunriseSetWindowMinutes) * time.Minute
				sunriseStart := sunEvents.Sunrise.Add(-window).Format("15:04:05")
				sunriseEnd := sunEvents.Sunrise.Add(window).Format("15:04:05")
				sunsetStart := sunEvents.Sunset.Add(-window).Format("15:04:05")
				sunsetEnd := sunEvents.Sunset.Add(window).Format("15:04:05")

				switch {
				case detTime >= sunriseStart && detTime <= sunriseEnd:
					timeOfDay = "sunrise"
				case detTime >= sunsetStart && detTime <= sunsetEnd:
					timeOfDay = "sunset"
				case detTime >= sunriseTime && detTime < sunsetTime:
					timeOfDay = "day"
				default:
					timeOfDay = "night"
				}
			}
		}

		// Create detection record
		record := DetectionRecord{
			ID:             fmt.Sprintf("%d", scanned.ID),
			Timestamp:      timestamp,
			ScientificName: scanned.ScientificName,
			CommonName:     scanned.CommonName,
			Confidence:     scanned.Confidence,
			Latitude:       scanned.Latitude,
			Longitude:      scanned.Longitude,
			Week:           week,
			AudioFilePath:  scanned.ClipName,
			Verified:       verifiedStatus,   // Use derived status
			Locked:         scanned.IsLocked, // Use derived status
			HasAudio:       scanned.ClipName != "",
			Device:         scanned.SourceNode,
			Source:         scanned.Source,
			TimeOfDay:      timeOfDay, // Include calculated time of day
		}

		results = append(results, record)
	}

	return results, int(total), nil
}

// getSunEventsForDate retrieves sun times for a given date
func (ds *DataStore) getSunEventsForDate(dateStr string, timestamp time.Time) (suncalc.SunEventTimes, error) {
	// Check if the sun times are already cached
	if cached, exists := ds.getCachedSunTimes(dateStr); exists {
		return cached, nil
	}

	// Calculate sun times for the given date
	sunTimes, err := ds.SunCalc.GetSunEventTimes(timestamp)
	if err != nil {
		return suncalc.SunEventTimes{}, errors.New(err).
			Component("datastore").
			Category(errors.CategoryNetwork).
			Context("operation", "get_sun_events_for_date").
			Context("date", dateStr).
			Build()
	}

	// Cache the calculated sun times
	ds.cacheSunTimes(dateStr, &sunTimes)

	return sunTimes, nil
}

// getCachedSunTimes retrieves sun times from the cache
func (ds *DataStore) getCachedSunTimes(dateStr string) (suncalc.SunEventTimes, bool) {
	cached, exists := ds.sunTimesCache.Load(dateStr)
	if exists {
		return cached.(suncalc.SunEventTimes), true
	}
	return suncalc.SunEventTimes{}, false
}

// cacheSunTimes caches sun times
func (ds *DataStore) cacheSunTimes(dateStr string, sunTimes *suncalc.SunEventTimes) {
	ds.sunTimesCache.Store(dateStr, *sunTimes)
}
