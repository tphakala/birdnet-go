// interfaces.go: this code defines the interface for the database operations
package datastore

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"github.com/tphakala/birdnet-go/internal/suncalc" // Import suncalc
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// sunriseSetWindowMinutes defines the time window (in minutes) around sunrise and sunset
const sunriseSetWindowMinutes = 30

// Sentinel errors for not found cases
var (
	ErrNoteReviewNotFound = errors.Newf("note review not found").Component("datastore").Category(errors.CategoryNotFound).Build()
	ErrNoteLockNotFound   = errors.Newf("note lock not found").Component("datastore").Category(errors.CategoryNotFound).Build()
	ErrImageCacheNotFound = errors.Newf("image cache not found").Component("datastore").Category(errors.CategoryNotFound).Build()
)

// StoreInterface abstracts the underlying database implementation and defines the interface for database operations.
// Optional methods:
//   - CheckpointWAL() error - Implemented by stores that support Write-Ahead Logging (e.g., SQLite)
//     Call via type assertion: if sqliteStore, ok := store.(*SQLiteStore); ok { sqliteStore.CheckpointWAL() }
type Interface interface {
	Open() error
	Save(note *Note, results []Results) error
	Delete(id string) error
	Get(id string) (Note, error)
	Close() error
	SetMetrics(metrics *Metrics) // Set metrics instance for observability
	SetSunCalcMetrics(suncalcMetrics any) // Set metrics for SunCalc service
	Optimize(ctx context.Context) error // Perform database optimization (VACUUM, ANALYZE, etc.)
	GetAllNotes() ([]Note, error)
	GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]Note, error)
	GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error)
	SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit int, offset int) ([]Note, error)
	GetLastDetections(numDetections int) ([]Note, error)
	GetAllDetectedSpecies() ([]Note, error)
	SearchNotes(query string, sortAscending bool, limit int, offset int) ([]Note, error)
	SearchNotesAdvanced(filters *AdvancedSearchFilters) ([]Note, int64, error)
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
	GetSpeciesSummaryData(ctx context.Context, startDate, endDate string) ([]SpeciesSummaryData, error)
	GetHourlyAnalyticsData(ctx context.Context, date string, species string) ([]HourlyAnalyticsData, error)
	GetDailyAnalyticsData(ctx context.Context, startDate, endDate string, species string) ([]DailyAnalyticsData, error)
	GetDetectionTrends(ctx context.Context, period string, limit int) ([]DailyAnalyticsData, error)
	GetHourlyDistribution(ctx context.Context, startDate, endDate string, species string) ([]HourlyDistributionData, error)
	GetNewSpeciesDetections(ctx context.Context, startDate, endDate string, limit, offset int) ([]NewSpeciesData, error)
	GetSpeciesFirstDetectionInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]NewSpeciesData, error)
	// Search functionality
	SearchDetections(filters *SearchFilters) ([]DetectionRecord, int, error)
	// Dynamic Threshold methods
	SaveDynamicThreshold(threshold *DynamicThreshold) error
	GetDynamicThreshold(speciesName string) (*DynamicThreshold, error)
	GetAllDynamicThresholds() ([]DynamicThreshold, error)
	DeleteDynamicThreshold(speciesName string) error
	DeleteExpiredDynamicThresholds(before time.Time) (int64, error) // Returns count deleted
	UpdateDynamicThresholdExpiry(speciesName string, expiresAt time.Time) error
	BatchSaveDynamicThresholds(thresholds []DynamicThreshold) error
}

// DataStore implements StoreInterface using a GORM database.
type DataStore struct {
	DB            *gorm.DB         // GORM database instance
	SunCalc       *suncalc.SunCalc // Instance for calculating sun times (Assumed initialized)
	sunTimesCache sync.Map         // Thread-safe map for caching sun times by date
	metrics       *Metrics // Metrics instance for tracking operations
	metricsMu     sync.RWMutex     // Mutex to protect metrics field access
	
	// Monitoring lifecycle management
	monitoringCtx    context.Context    // Context for monitoring goroutines
	monitoringCancel context.CancelFunc // Function to cancel monitoring
	monitoringMu     sync.Mutex         // Mutex to protect monitoring state
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

// SetMetrics sets the metrics instance for the datastore
func (ds *DataStore) SetMetrics(m *Metrics) {
	ds.metricsMu.Lock()
	defer ds.metricsMu.Unlock()
	ds.metrics = m
}

// SetSunCalcMetrics sets the metrics instance for the SunCalc service
func (ds *DataStore) SetSunCalcMetrics(suncalcMetrics any) {
	ds.metricsMu.RLock()
	sunCalc := ds.SunCalc
	ds.metricsMu.RUnlock()
	
	if sunCalc != nil && suncalcMetrics != nil {
		// Type assert to the actual metrics type
		if m, ok := suncalcMetrics.(*metrics.SunCalcMetrics); ok {
			sunCalc.SetMetrics(m)
		}
	}
}

// Save stores a note and its associated results as a single transaction in the database.
func (ds *DataStore) Save(note *Note, results []Results) error {
	// Generate a unique transaction ID (first 8 chars of UUID)
	txID := fmt.Sprintf("tx-%s", uuid.New().String()[:8])
	txStart := time.Now()
	txLogger := getLogger().With("tx_id", txID, "operation", "save_note")
	
	txLogger.Debug("Starting transaction",
		"note_scientific_name", note.ScientificName,
		"results_count", len(results))

	// Retry configuration
	maxRetries := 5
	baseDelay := 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Begin a transaction
		tx := ds.DB.Begin()
		if tx.Error != nil {
			lastErr = dbError(tx.Error, "begin_transaction", errors.PriorityHigh,
				"tx_id", txID,
				"attempt", fmt.Sprintf("%d", attempt+1),
				"action", "save_detection",
				"table", "notes")
			
			txLogger.Error("Failed to begin transaction",
				"error", lastErr,
				"attempt", attempt+1)
				
			continue
		}

		// Execute transaction with rollback on error
		transactionErr := ds.executeTransaction(tx, note, results, txID, attempt+1, txLogger)
		
		if transactionErr != nil {
			lastErr = transactionErr
			if isDatabaseLocked(transactionErr) {
				ds.handleDatabaseLockError(attempt, maxRetries, baseDelay, txLogger)
				continue
			}
			// Non-retryable error
			return transactionErr
		}

		// Success - record metrics
		ds.recordTransactionSuccess(txStart, attempt+1, len(results), txLogger)
		return nil
	}

	// All retries exhausted
	return ds.handleMaxRetriesExhausted(lastErr, txID, txStart, txLogger)
}

// Get retrieves a note by its ID from the database.
func (ds *DataStore) Get(id string) (Note, error) {
	// Convert the id from string to integer
	noteID, err := strconv.Atoi(id)
	if err != nil {
		return Note{}, validationError("invalid note ID format", "id", id)
	}

	var note Note
	// Retrieve the note by its ID with Review, Lock, and Comments preloaded
	if err := ds.DB.Preload("Review").Preload("Lock").Preload("Comments", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC") // Order comments by creation time, newest first
	}).First(&note, noteID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Note{}, notFoundError("note", fmt.Sprintf("%d", noteID))
		}
		return Note{}, dbError(err, "get_note", errors.PriorityMedium,
			"note_id", fmt.Sprintf("%d", noteID),
			"action", "retrieve_detection_record")
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
		return validationError("invalid note ID format for deletion", "id", id)
	}

	// Check if the note is locked
	isLocked, err := ds.IsNoteLocked(id)
	if err != nil {
		return dbError(err, "check_note_lock", errors.PriorityMedium,
			"note_id", id,
			"action", "validate_deletion_permissions")
	}
	if isLocked {
		return conflictError(errors.NewStd("cannot delete note: note is locked"), 
			"delete_note", "note_locked",
			"note_id", id,
			"action", "delete_detection_record")
	}

	// Perform the deletion within a transaction
	return ds.DB.Transaction(func(tx *gorm.DB) error {
		// Delete the full results entry associated with the note
		if err := tx.Where("note_id = ?", noteID).Delete(&Results{}).Error; err != nil {
			return dbError(err, "delete_results", errors.PriorityMedium,
				"note_id", fmt.Sprintf("%d", noteID),
				"table", "results",
				"action", "delete_detection_results")
		}
		// Delete the note itself
		if err := tx.Delete(&Note{}, noteID).Error; err != nil {
			return dbError(err, "delete_note", errors.PriorityMedium,
				"note_id", fmt.Sprintf("%d", noteID),
				"table", "notes",
				"action", "delete_detection_record")
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
	// Pre-allocate slice with capacity for all results
	notes := make([]Note, 0, len(results))
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

// Dialector returns the database dialector
func (ds *DataStore) Dialector() gorm.Dialector {
	if ds.DB == nil {
		return nil
	}
	return ds.DB.Dialector
}

// GetHourFormat returns the database-specific SQL fragment for formatting a time column as hour.
func (ds *DataStore) GetHourFormat() string {
	dialector := ds.Dialector()
	if dialector == nil {
		return ""
	}

	// Handling for supported databases: SQLite and MySQL
	switch strings.ToLower(dialector.Name()) {
	case "sqlite":
		return "strftime('%H', time)"
	case "mysql":
		return "TIME_FORMAT(time, '%H')"
	default:
		// Log or handle unsupported database types
		return ""
	}
}

// GetDateTimeExpr returns the database-specific SQL fragment for concatenating date and time columns into a datetime.
// This generalized version accepts column names to prevent ambiguity in JOIN queries.
// Returns an empty string for unsupported database types, which should be handled by the caller.
//
// Supported formats:
//   - SQLite: datetime(dateCol || ' ' || timeCol)
//   - MySQL: STR_TO_DATE(CONCAT(dateCol, ' ', timeCol), '%Y-%m-%d %H:%i:%s')
//
// The returned fragment can be used directly in SQL SELECT statements for MIN/MAX datetime operations.
func (ds *DataStore) GetDateTimeExpr(dateCol, timeCol string) string {
	dialector := ds.Dialector()
	if dialector == nil {
		return ""
	}

	// Handling for supported databases: SQLite and MySQL
	switch strings.ToLower(dialector.Name()) {
	case "sqlite":
		return fmt.Sprintf("datetime(%s || ' ' || %s)", dateCol, timeCol)
	case "mysql":
		return fmt.Sprintf("STR_TO_DATE(CONCAT(%s, ' ', %s), '%%Y-%%m-%%d %%H:%%i:%%s')", dateCol, timeCol)
	default:
		// Log or handle unsupported database types
		return ""
	}
}

// GetDateTimeFormat returns the database-specific SQL fragment for concatenating date and time columns into a datetime.
// This is a convenience wrapper that uses the default column names "date" and "time".
// Returns an empty string for unsupported database types, which should be handled by the caller.
//
// Supported formats:
//   - SQLite: datetime(date || ' ' || time)
//   - MySQL: STR_TO_DATE(CONCAT(date, ' ', time), '%Y-%m-%d %H:%i:%s')
//
// The returned fragment can be used directly in SQL SELECT statements for MIN/MAX datetime operations.
func (ds *DataStore) GetDateTimeFormat() string {
	return ds.GetDateTimeExpr("date", "time")
}

// GetDateFormat returns the database-specific SQL fragment for extracting date from a datetime column.
// Returns an empty string for unsupported database types, which should be handled by the caller.
//
// Supported formats:
//   - SQLite: date(column_name)
//   - MySQL: DATE(column_name)
//
// The returned fragment can be used directly in SQL WHERE clauses for date comparisons.
func (ds *DataStore) GetDateFormat(columnName string) string {
	dialector := ds.Dialector()
	if dialector == nil {
		return ""
	}

	// Handling for supported databases: SQLite and MySQL
	switch strings.ToLower(dialector.Name()) {
	case "sqlite":
		return fmt.Sprintf("date(%s)", columnName)
	case "mysql":
		return fmt.Sprintf("DATE(%s)", columnName)
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

	// Get database-specific date format
	dateFormat := ds.GetDateFormat("time")
	if dateFormat == "" {
		// Safely get database type for error context
		dialectName := "unknown"
		if d := ds.Dialector(); d != nil {
			dialectName = d.Name()
		}
		return nil, errors.Newf("unsupported database type for date formatting").
			Component("datastore").
			Category(errors.CategoryConfiguration).
			Context("operation", "get_hourly_weather").
			Context("database_type", dialectName).
			Build()
	}

	err := ds.DB.Where(dateFormat+" = ?", date).
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
			return nil, ErrNoteReviewNotFound
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
		return validationError("comment cannot be nil", "comment", nil)
	}
	if comment.NoteID == 0 {
		return validationError("note ID cannot be zero", "note_id", comment.NoteID)
	}
	// Entry can be empty as comments are optional, but if provided, check length
	if len(comment.Entry) > 1000 {
		return validationError("comment entry exceeds maximum length", "entry_length", len(comment.Entry))
	}

	if err := ds.DB.Create(comment).Error; err != nil {
		return dbError(err, "save_note_comment", errors.PriorityMedium,
			"note_id", fmt.Sprintf("%d", comment.NoteID),
			"table", "note_comments",
			"action", "add_user_comment")
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
			return nil, ErrNoteLockNotFound
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
				// Calculate exponential backoff with jitter
				baseBackoff := baseDelay * time.Duration(attempt+1)
				jitter := time.Duration(rand.Float64() * 0.25 * float64(baseBackoff))
				delay := baseBackoff + jitter
				log.Printf("[%s] Database locked, retrying in %v (attempt %d/%d, jitter %v)", txID, delay, attempt+1, maxRetries, jitter)
				time.Sleep(delay)
				lastErr = result.Error
				continue
			}
			return stateError(result.Error, "lock_note", "note_lock_acquisition",
				"note_id", noteID,
				"action", "acquire_edit_lock")
		}

		// If we get here, the transaction was successful
		if attempt > 0 {
			log.Printf("[%s] Database transaction successful after %d attempts", txID, attempt+1)
		}
		return nil
	}

	return stateError(lastErr, "lock_note", "lock_retry_exhausted",
		"note_id", noteID,
		"transaction_id", txID,
		"max_retries", fmt.Sprintf("%d", maxRetries),
		"action", "acquire_edit_lock")
}

// UnlockNote removes a lock from a note
func (ds *DataStore) UnlockNote(noteID string) error {
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return validationError("invalid note ID format for unlock", "note_id", noteID)
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
				// Calculate exponential backoff with jitter
				baseBackoff := baseDelay * time.Duration(attempt+1)
				jitter := time.Duration(rand.Float64() * 0.25 * float64(baseBackoff))
				delay := baseBackoff + jitter
				log.Printf("[%s] Database locked, retrying in %v (attempt %d/%d, jitter %v)", txID, delay, attempt+1, maxRetries, jitter)
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
			return nil, ErrImageCacheNotFound
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
	start := time.Now()
	
	if cache.ProviderName == "" {
		err := validationError("provider name cannot be empty", "provider_name", "")
		getLogger().Error("Invalid image cache data: empty provider name", "error", err)
		return err
	}
	if cache.ScientificName == "" {
		err := validationError("scientific name cannot be empty", "scientific_name", "")
		getLogger().Error("Invalid image cache data: empty scientific name", "error", err)
		return err
	}

	// Use Clauses(clause.OnConflict...) to perform an UPSERT operation
	// Update all columns except primary key on conflict
	if err := ds.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider_name"}, {Name: "scientific_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"url", "license_name", "license_url", "author_name", "author_url", "cached_at"}),
	}).Create(cache).Error; err != nil {
		// Detect constraint violations
		if isConstraintViolation(err) {
			// This is expected with UPSERT, log at debug level
			getLogger().Debug("Image cache UPSERT handled constraint",
				"scientific_name", cache.ScientificName,
				"provider", cache.ProviderName)
		} else {
			enhancedErr := dbError(err, "save_image_cache", errors.PriorityMedium,
				"table", "image_caches",
				"scientific_name", cache.ScientificName,
				"provider", cache.ProviderName,
				"action", "cache_species_thumbnail")
			
			getLogger().Error("Failed to save image cache",
				"error", enhancedErr)
			
			// Record error metric
			ds.metricsMu.RLock()
			metricsInstance := ds.metrics
			ds.metricsMu.RUnlock()
			if metricsInstance != nil {
				metricsInstance.RecordImageCacheOperation("save", "error")
				metricsInstance.RecordImageCacheDuration("save", time.Since(start).Seconds())
			}
			
			return enhancedErr
		}
	}
	
	// Record success metric
	ds.metricsMu.RLock()
	metricsInstance := ds.metrics
	ds.metricsMu.RUnlock()
	if metricsInstance != nil {
		metricsInstance.RecordImageCacheOperation("save", "success")
		metricsInstance.RecordImageCacheDuration("save", time.Since(start).Seconds())
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

	// Debug logging (controlled by thumbnails debug setting)
	settings := conf.Setting()
	if settings.Realtime.Dashboard.Thumbnails.Debug {
		log.Printf("GetImageCacheBatch: Querying for provider=%s, species=%v", providerName, scientificNames)
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

	if settings.Realtime.Dashboard.Thumbnails.Debug {
		log.Printf("GetImageCacheBatch: Found %d entries for provider %s", len(caches), providerName)
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

	// Pre-allocate conditions slice based on date range
	dayCount := int(endDate.Sub(startDate).Hours()/24) + 1
	conditions := make([]*gorm.DB, 0, dayCount)
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
		"notes.latitude, notes.longitude, notes.clip_name, notes.source_node, " +
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
		// IMPORTANT: Database stores local time strings, so parse them as local time
		// Using time.Parse() assumes UTC, which causes timezone conversion bugs
		// TODO: Consider creating a helper function like ParseDatabaseTimestamp() to ensure
		// consistent timezone handling across all database time parsing operations
		timestamp, err := time.ParseInLocation("2006-01-02 15:04:05", scanned.Date+" "+scanned.Time, time.Local)
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
			Source:         "", // Source field was runtime-only, not stored in database
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

// Helper functions for Save method to reduce cognitive complexity

// saveNoteInTransaction saves a note within a transaction
func (ds *DataStore) saveNoteInTransaction(tx *gorm.DB, note *Note, txID string, attempt int, txLogger *slog.Logger) error {
	if err := tx.Create(note).Error; err != nil {
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "save_note").
			Context("table", "notes").
			Context("note_id", fmt.Sprintf("%d", note.ID)).
			Context("tx_id", txID).
			Context("attempt", fmt.Sprintf("%d", attempt)).
			Build()
		
		txLogger.Error("Failed to save note",
			"error", enhancedErr,
			"note_id", note.ID,
			"scientific_name", note.ScientificName)
		
		// Record error metric
		ds.metricsMu.RLock()
		metricsInstance := ds.metrics
		ds.metricsMu.RUnlock()
		if metricsInstance != nil {
			metricsInstance.RecordNoteOperation("save", "error")
			metricsInstance.RecordDbOperationError("create", "notes", categorizeError(err))
		}
		
		return enhancedErr
	}
	
	// Record success metric for note
	ds.metricsMu.RLock()
	metricsInstance := ds.metrics
	ds.metricsMu.RUnlock()
	if metricsInstance != nil {
		metricsInstance.RecordNoteOperation("save", "success")
	}
	
	return nil
}

// saveResultsInTransaction saves results within a transaction
func (ds *DataStore) saveResultsInTransaction(tx *gorm.DB, results []Results, noteID uint, txID string, attempt int, txLogger *slog.Logger) error {
	for i, result := range results {
		result.NoteID = noteID
		if err := tx.Create(&result).Error; err != nil {
			enhancedErr := errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "save_result").
				Context("note_id", fmt.Sprintf("%d", noteID)).
				Context("result_index", fmt.Sprintf("%d", i)).
				Context("tx_id", txID).
				Context("attempt", fmt.Sprintf("%d", attempt)).
				Build()
			
			txLogger.Error("Failed to save result",
				"error", enhancedErr,
				"note_id", noteID,
				"result_index", i)
			
			ds.metricsMu.RLock()
			metricsInstance := ds.metrics
			ds.metricsMu.RUnlock()
			if metricsInstance != nil {
				metricsInstance.RecordDbOperationError("create", "results", categorizeError(err))
			}
			
			return enhancedErr
		}
	}
	return nil
}

// commitTransactionWithMetrics commits a transaction and records metrics
func (ds *DataStore) commitTransactionWithMetrics(tx *gorm.DB, txID string, attempt int, txLogger *slog.Logger) error {
	if err := tx.Commit().Error; err != nil {
		// Commit failures are critical as they can lead to data loss
		priority := errors.PriorityHigh
		if isDatabaseCorruption(err) {
			priority = errors.PriorityCritical
		}
		
		enhancedErr := dbError(err, "commit_transaction", priority,
			"tx_id", txID,
			"attempt", fmt.Sprintf("%d", attempt),
			"action", "finalize_detection_save")
		
		txLogger.Error("Failed to commit transaction",
			"error", enhancedErr)
		
		ds.metricsMu.RLock()
		metricsInstance := ds.metrics
		ds.metricsMu.RUnlock()
		if metricsInstance != nil {
			metricsInstance.RecordTransaction("rollback")
			metricsInstance.RecordTransactionError("save_note", categorizeError(err))
		}
		
		return enhancedErr
	}
	
	// Record commit success
	ds.metricsMu.RLock()
	metricsInstance := ds.metrics
	ds.metricsMu.RUnlock()
	if metricsInstance != nil {
		metricsInstance.RecordTransaction("committed")
	}
	
	return nil
}

// handleDatabaseLockError handles database lock errors with backoff
func (ds *DataStore) handleDatabaseLockError(attempt, maxRetries int, baseDelay time.Duration, txLogger *slog.Logger) {
	// Calculate exponential backoff with jitter to avoid thundering herd
	baseBackoff := baseDelay * time.Duration(attempt+1)
	// Add 0-25% jitter to the base backoff
	jitter := time.Duration(rand.Float64() * 0.25 * float64(baseBackoff))
	delay := baseBackoff + jitter
	
	txLogger.Warn("Database locked, scheduling retry",
		"attempt", attempt+1,
		"max_attempts", maxRetries,
		"backoff_ms", delay.Milliseconds(),
		"jitter_ms", jitter.Milliseconds())
	
	// Record retry metric
	ds.metricsMu.RLock()
	metricsInstance := ds.metrics
	ds.metricsMu.RUnlock()
	if metricsInstance != nil {
		metricsInstance.RecordTransactionRetry("save_note", "database_locked")
	}
	
	time.Sleep(delay)
}

// executeTransaction executes the save operations within a transaction
func (ds *DataStore) executeTransaction(tx *gorm.DB, note *Note, results []Results, txID string, attempt int, txLogger *slog.Logger) error {
	// Set up panic recovery with rollback
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Save the note
	if err := ds.saveNoteInTransaction(tx, note, txID, attempt, txLogger); err != nil {
		tx.Rollback()
		if isDatabaseLocked(err) {
			return err
		}
		return err
	}

	// Save the results
	if err := ds.saveResultsInTransaction(tx, results, note.ID, txID, attempt, txLogger); err != nil {
		tx.Rollback()
		if isDatabaseLocked(err) {
			return err
		}
		return err
	}

	// Commit the transaction
	if err := ds.commitTransactionWithMetrics(tx, txID, attempt, txLogger); err != nil {
		if isDatabaseLocked(err) {
			return err
		}
		return err
	}

	return nil
}

// recordTransactionSuccess records success metrics for a transaction
func (ds *DataStore) recordTransactionSuccess(txStart time.Time, attempts, resultsCount int, txLogger *slog.Logger) {
	duration := time.Since(txStart)
	txLogger.Info("Transaction completed",
		"duration", duration,
		"attempts", attempts,
		"rows_affected", 1+resultsCount)
	
	// Record success metrics
	ds.metricsMu.RLock()
	metricsInstance := ds.metrics
	ds.metricsMu.RUnlock()
	if metricsInstance != nil {
		metricsInstance.RecordTransactionDuration("save_note", duration.Seconds())
		if attempts > 1 {
			metricsInstance.RecordLockContention("database", "retry_succeeded")
		}
	}
}

// handleMaxRetriesExhausted handles the case when all retries are exhausted
func (ds *DataStore) handleMaxRetriesExhausted(lastErr error, txID string, txStart time.Time, txLogger *slog.Logger) error {
	enhancedErr := stateError(lastErr, "save_transaction", "transaction_retry_exhausted",
		"tx_id", txID,
		"max_retries_exhausted", "true",
		"action", "save_detection_data",
		"total_duration_ms", time.Since(txStart).Milliseconds())
	
	txLogger.Error("Transaction failed after max retries",
		"error", enhancedErr,
		"total_duration", time.Since(txStart))
	
	// Record failure metrics
	ds.metricsMu.RLock()
	metricsInstance := ds.metrics
	ds.metricsMu.RUnlock()
	if metricsInstance != nil {
		metricsInstance.RecordTransaction("timeout")
		metricsInstance.RecordTransactionError("save_note", "max_retries_exhausted")
		metricsInstance.RecordLockContention("database", "max_retries_exhausted")
	}
	
	return enhancedErr
}
