// interfaces.go: this code defines the interface for the database operations
package datastore

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/conf"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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
	GetClipsQualifyingForRemoval(minHours int, minClips int) ([]ClipForRemoval, error)
	// weather data
	SaveDailyEvents(dailyEvents *DailyEvents) error
	GetDailyEvents(date string) (DailyEvents, error)
	SaveHourlyWeather(hourlyWeather *HourlyWeather) error
	GetHourlyWeather(date string) ([]HourlyWeather, error)
	LatestHourlyWeather() (*HourlyWeather, error)
	GetHourlyDetections(date, hour string, duration int) ([]Note, error)
	CountSpeciesDetections(species, date, hour string, duration int) (int64, error)
	CountSearchResults(query string) (int64, error)
}

// DataStore implements StoreInterface using a GORM database.
type DataStore struct {
	DB *gorm.DB // GORM database instance
}

// NewDataStore creates a new DataStore instance based on the provided configuration context.
func New(settings *conf.Settings) Interface {
	switch {
	case settings.Output.SQLite.Enabled:
		return &SQLiteStore{
			Settings: settings,
		}
	case settings.Output.MySQL.Enabled:
		return &MySQLStore{
			Settings: settings,
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
			lastErr = fmt.Errorf("starting transaction: %w", tx.Error)
			continue
		}

		// Roll back the transaction if a panic occurs
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()

		// Save the note and its associated results within the transaction
		if err := tx.Create(note).Error; err != nil {
			tx.Rollback()
			if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				delay := baseDelay * time.Duration(attempt+1)
				log.Printf("[%s] Database locked, retrying in %v (attempt %d/%d)", txID, delay, attempt+1, maxRetries)
				time.Sleep(delay)
				continue
			}
			return fmt.Errorf("saving note: %w", err)
		}

		// Assign the note ID to each result and save them
		for _, result := range results {
			result.NoteID = note.ID
			if err := tx.Create(&result).Error; err != nil {
				tx.Rollback()
				if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
					delay := baseDelay * time.Duration(attempt+1)
					log.Printf("[%s] Database locked, retrying in %v (attempt %d/%d)", txID, delay, attempt+1, maxRetries)
					time.Sleep(delay)
					continue
				}
				lastErr = fmt.Errorf("saving result: %w", err)
				return lastErr
			}
		}

		// Commit the transaction
		if err := tx.Commit().Error; err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				delay := baseDelay * time.Duration(attempt+1)
				log.Printf("[%s] Database locked, retrying in %v (attempt %d/%d)", txID, delay, attempt+1, maxRetries)
				time.Sleep(delay)
				continue
			}
			return fmt.Errorf("committing transaction: %w", err)
		}

		// Log if retry count is not 0 and transaction was successful
		if attempt > 0 {
			log.Printf("[%s] Database transaction successful after %d attempts", txID, attempt+1)
		}

		// If we get here, the transaction was successful
		return nil
	}

	// If we've exhausted all retries
	return fmt.Errorf("[%s] failed after %d attempts: %w", txID, maxRetries, lastErr)
}

// Get retrieves a note by its ID from the database.
func (ds *DataStore) Get(id string) (Note, error) {
	// Convert the id from string to integer
	noteID, err := strconv.Atoi(id)
	if err != nil {
		return Note{}, fmt.Errorf("converting ID to integer: %w", err)
	}

	var note Note
	// Retrieve the note by its ID
	if err := ds.DB.First(&note, noteID).Error; err != nil {
		return Note{}, fmt.Errorf("getting note with ID %d: %w", noteID, err)
	}
	return note, nil
}

// Delete removes a note and its associated results from the database.
func (ds *DataStore) Delete(id string) error {
	// Convert the id from string to unsigned integer
	noteID, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return fmt.Errorf("converting ID to integer: %w", err)
	}

	// Perform the deletion within a transaction
	return ds.DB.Transaction(func(tx *gorm.DB) error {
		// Delete the full results entry associated with the note
		if err := tx.Where("note_id = ?", noteID).Delete(&Results{}).Error; err != nil {
			return fmt.Errorf("deleting results for note ID %d: %w", noteID, err)
		}
		// Delete the note itself
		if err := tx.Delete(&Note{}, noteID).Error; err != nil {
			return fmt.Errorf("deleting note with ID %d: %w", noteID, err)
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
		return "", fmt.Errorf("failed to retrieve clip path: %w", err)
	}

	return clipPath.ClipName, nil
}

// DeleteNoteClipPath deletes the field representing the path to the audio clip associated with a note.
func (ds *DataStore) DeleteNoteClipPath(noteID string) error {
	// Validate the input parameter
	if noteID == "" {
		return fmt.Errorf("invalid note ID: must not be empty")
	}

	// Update the clip_name field to an empty string for the specified note ID
	err := ds.DB.Model(&Note{}).Where("id = ?", noteID).Update("clip_name", "").Error
	if err != nil {
		return fmt.Errorf("failed to delete clip path for note ID %s: %w", noteID, err)
	}

	// Return nil if no errors occurred, indicating successful execution
	return nil
}

// GetAllNotes retrieves all notes from the database.
func (ds *DataStore) GetAllNotes() ([]Note, error) {
	var notes []Note
	if result := ds.DB.Find(&notes); result.Error != nil {
		return nil, fmt.Errorf("error getting all notes: %w", result.Error)
	}
	return notes, nil
}

// GetTopBirdsData retrieves the top bird sightings based on a selected date and minimum confidence threshold.
func (ds *DataStore) GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]Note, error) {
	var results []Note

	// Get the number of species to report from the dashboard settings
	reportCount := conf.Setting().Realtime.Dashboard.SummaryLimit

	// First, get the count and common names
	query := ds.DB.Table("notes").
		Select("common_name, MAX(scientific_name) as scientific_name, COUNT(*) as count").
		Where("date = ? AND confidence >= ?", selectedDate, minConfidenceNormalized).
		Group("common_name").
		Order("count DESC").
		Limit(reportCount)

	err := query.Scan(&results).Error
	return results, err
}

type ClipForRemoval struct {
	ID             string
	ScientificName string
	ClipName       string
	NumRecordings  int
}

// GetClipsQualifyingForRemoval returns the list of clips that qualify for removal based on retention policy.
// It checks each clip's age and count of recordings per scientific name, filtering out clips based on provided minimum hours and clip count criteria.
func (ds *DataStore) GetClipsQualifyingForRemoval(minHours int, minClips int) ([]ClipForRemoval, error) {
	// Validate input parameters
	if minHours <= 0 || minClips <= 0 {
		return nil, fmt.Errorf("invalid parameters: minHours and minClips must be greater than 0")
	}

	var results []ClipForRemoval

	// Define a subquery to count the number of recordings per scientific name
	subquery := ds.DB.Model(&Note{}).Select("ID, scientific_name, ROW_NUMBER() OVER (PARTITION BY scientific_name) as num_recordings").
		Where("clip_name != ''")
	if err := subquery.Error; err != nil {
		return nil, fmt.Errorf("error creating subquery: %w", err)
	}

	// Main query to find clips qualifying for removal based on retention policy
	err := ds.DB.Table("(?) AS n", ds.DB.Model(&Note{})).
		Select("n.ID, n.scientific_name, n.clip_name, sub.num_recordings").
		Joins("INNER JOIN (?) AS sub ON n.ID = sub.ID", subquery).
		Where("strftime('%s', 'now') - strftime('%s', begin_time) > ?", minHours*3600). // Convert hours to seconds for comparison
		Where("sub.num_recordings > ?", minClips).
		Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("error executing main query: %w", err)
	}

	return results, nil
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
		return hourlyCounts, err
	}

	for _, result := range results {
		if result.Hour >= 0 && result.Hour < 24 {
			hourlyCounts[result.Hour] = result.Count
		}
	}

	return hourlyCounts, nil
}

// SpeciesDetections retrieves bird species detections for a specific date and time period.
func (ds *DataStore) SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit int, offset int) ([]Note, error) {
	sortOrder := sortAscendingString(sortAscending)

	query := ds.DB.Where("common_name = ? AND date = ?", species, date)
	if hour != "" {
		startTime, endTime := getHourRange(hour, duration)
		query = query.Where("time >= ? AND time < ?", startTime, endTime)
	}

	query = query.Order("id " + sortOrder).
		Limit(limit).
		Offset(offset)

	var detections []Note
	err := query.Find(&detections).Error
	return detections, err
}

// GetLastDetections retrieves the most recent bird detections.
func (ds *DataStore) GetLastDetections(numDetections int) ([]Note, error) {
	var notes []Note
	now := time.Now()

	// Retrieve the most recent detections based on the ID in descending order
	if result := ds.DB.Order("id DESC").Limit(numDetections).Find(&notes); result.Error != nil {
		return nil, fmt.Errorf("error getting last detections: %w", result.Error)
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

	return results, err
}

// SearchNotes performs a search on notes with optional sorting, pagination, and limits.
func (ds *DataStore) SearchNotes(query string, sortAscending bool, limit int, offset int) ([]Note, error) {
	var notes []Note
	sortOrder := sortAscendingString(sortAscending)

	err := ds.DB.Where("common_name LIKE ? OR scientific_name LIKE ?", "%"+query+"%", "%"+query+"%").
		Order("id " + sortOrder).
		Limit(limit).
		Offset(offset).
		Find(&notes).Error

	if err != nil {
		return nil, fmt.Errorf("error searching notes: %w", err)
	}
	return notes, nil
}

// performAutoMigration automates database migrations with error handling.
func performAutoMigration(db *gorm.DB, debug bool, dbType, connectionInfo string) error {
	if err := db.AutoMigrate(&Note{}, &Results{}, &DailyEvents{}, &HourlyWeather{}); err != nil {
		return fmt.Errorf("failed to auto-migrate %s database: %w", dbType, err)
	}

	if debug {
		log.Printf("%s database connection initialized: %s", dbType, connectionInfo)
	}

	return nil
}

// SaveDailyEvents saves daily events data to the database.
func (ds *DataStore) SaveDailyEvents(dailyEvents *DailyEvents) error {
	// Use upsert to handle the unique date constraint
	result := ds.DB.Where("date = ?", dailyEvents.Date).
		Assign(*dailyEvents).
		FirstOrCreate(dailyEvents)

	if result.Error != nil {
		return fmt.Errorf("failed to save daily events: %w", result.Error)
	}

	return nil
}

// GetDailyEvents retrieves daily events data by date from the database.
func (ds *DataStore) GetDailyEvents(date string) (DailyEvents, error) {
	var dailyEvents DailyEvents
	if err := ds.DB.Where("date = ?", date).First(&dailyEvents).Error; err != nil {
		return dailyEvents, err
	}
	return dailyEvents, nil
}

// SaveHourlyWeather saves hourly weather data to the database.
func (ds *DataStore) SaveHourlyWeather(hourlyWeather *HourlyWeather) error {
	// Basic validation
	if hourlyWeather.Time.IsZero() {
		return fmt.Errorf("invalid time value in hourly weather data")
	}

	// Use upsert to avoid duplicates for the same timestamp
	result := ds.DB.Where("time = ?", hourlyWeather.Time).
		Assign(*hourlyWeather).
		FirstOrCreate(hourlyWeather)

	if result.Error != nil {
		return fmt.Errorf("failed to save hourly weather: %w", result.Error)
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
		return nil, fmt.Errorf("failed to get hourly weather for date %s: %w", date, err)
	}

	return hourlyWeather, nil
}

// LatestHourlyWeather retrieves the latest hourly weather entry from the database.
func (ds *DataStore) LatestHourlyWeather() (*HourlyWeather, error) {
	var weather HourlyWeather

	err := ds.DB.Order("time DESC").First(&weather).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no weather data found")
		}
		return nil, fmt.Errorf("failed to get latest weather: %w", err)
	}

	return &weather, nil
}

// sortOrderAscendingString returns "ASC" or "DESC" based on the boolean input.
func sortAscendingString(asc bool) string {
	if asc {
		return "ASC"
	}
	return "DESC"
}

// createGormLogger configures and returns a new GORM logger instance.
func createGormLogger() logger.Interface {
	return logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold: 200 * time.Millisecond,
			LogLevel:      logger.Warn,
			Colorful:      true,
		},
	)
}

// GetHourlyDetections retrieves bird detections for a specific date and hour.
func (ds *DataStore) GetHourlyDetections(date string, hour string, duration int) ([]Note, error) {
	var detections []Note

	startTime, endTime := getHourRange(hour, duration)
	err := ds.DB.Where("date = ? AND time >= ? AND time < ?", date, startTime, endTime).
		Order("time ASC").
		Find(&detections).Error

	return detections, err
}

// CountSpeciesDetections counts the number of detections for a specific species, date, and hour.
func (ds *DataStore) CountSpeciesDetections(species, date, hour string, duration int) (int64, error) {
	var count int64
	query := ds.DB.Model(&Note{}).Where("common_name = ? AND date = ?", species, date)

	if hour != "" {
		startTime, endTime := getHourRange(hour, duration)
		query = query.Where("time >= ? AND time < ?", startTime, endTime)
	}

	err := query.Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("error counting species detections: %w", err)
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
		return 0, fmt.Errorf("error counting search results: %w", err)
	}

	return count, nil
}

func getHourRange(hour string, duration int) (string, string) {
	startHour, _ := strconv.Atoi(hour)
	endHour := (startHour + duration) % 24
	startTime := fmt.Sprintf("%02d:00:00", startHour)
	endTime := fmt.Sprintf("%02d:00:00", endHour)
	return startTime, endTime
}
