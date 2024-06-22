// interfaces.go: this code defines the interface for the database operations
package datastore

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

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
	SpeciesDetections(species, date, hour string, sortAscending bool, limit int, offset int) ([]Note, error)
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
	GetHourlyDetections(date, hour string) ([]Note, error)
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
	// Begin a transaction
	tx := ds.DB.Begin()
	if tx.Error != nil {
		return fmt.Errorf("starting transaction: %w", tx.Error)
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
		return fmt.Errorf("saving note: %w", err)
	}

	// Assign the note ID to each result and save them
	for _, result := range results {
		result.NoteID = note.ID
		if err := tx.Create(&result).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("saving result: %w", err)
		}
	}

	// Commit the transaction
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
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
	const reportCount = 30 // Consider making this a configurable parameter

	err := ds.DB.Table("notes").
		Select("common_name", "scientific_name", "COUNT(*) as count").
		Where("date = ? AND confidence >= ?", selectedDate, minConfidenceNormalized).
		Group("common_name").
		//Having("COUNT(*) > ?", 1).
		Order("count DESC").
		Limit(reportCount).
		Scan(&results).Error

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
		return "DATE_FORMAT(time, '%H')"
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
func (ds *DataStore) SpeciesDetections(species, date, hour string, sortAscending bool, limit int, offset int) ([]Note, error) {
	sortOrder := sortAscendingString(sortAscending)

	query := ds.DB.Where("common_name = ? AND date = ?", species, date)
	if hour != "" {
		if len(hour) < 2 {
			hour = "0" + hour
		}
		startTime := hour + ":00"
		endTime := hour + ":59"
		query = query.Where("time >= ? AND time <= ?", startTime, endTime)
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

	// get current time
	now := time.Now()

	if result := ds.DB.Order("date DESC, time DESC").Limit(numDetections).Find(&notes); result.Error != nil {
		return nil, fmt.Errorf("error getting last detections: %w", result.Error)
	}

	// calculate time it took to retrieve the data
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
		return fmt.Errorf("failed to auto-migrate %s database: %v", dbType, err)
	}

	if debug {
		log.Printf("%s database connection initialized: %s", dbType, connectionInfo)
	}

	return nil
}

// SaveDailyEvents saves daily events data to the database.
func (ds *DataStore) SaveDailyEvents(dailyEvents *DailyEvents) error {
	// Check if daily events data already exists for the date
	var existingDailyEvents DailyEvents
	if err := ds.DB.Where("date = ?", dailyEvents.Date).First(&existingDailyEvents).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			// Insert new daily events data
			if err := ds.DB.Create(dailyEvents).Error; err != nil {
				return err
			}
			return nil
		}
		return err
	}

	// Update existing daily events data
	existingDailyEvents.Sunrise = dailyEvents.Sunrise
	existingDailyEvents.Sunset = dailyEvents.Sunset
	existingDailyEvents.Country = dailyEvents.Country
	existingDailyEvents.CityName = dailyEvents.CityName
	if err := ds.DB.Save(&existingDailyEvents).Error; err != nil {
		return err
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
	if err := ds.DB.Create(hourlyWeather).Error; err != nil {
		return err
	}
	return nil
}

// GetHourlyWeather retrieves hourly weather data by date from the database.
func (ds *DataStore) GetHourlyWeather(date string) ([]HourlyWeather, error) {
	var hourlyWeather []HourlyWeather
	if err := ds.DB.Where("date(time) = ?", date).Find(&hourlyWeather).Error; err != nil {
		return nil, err
	}
	return hourlyWeather, nil
}

// LatestHourlyWeather retrieves the latest hourly weather entry from the database.
func (ds *DataStore) LatestHourlyWeather() (*HourlyWeather, error) {
	var weather HourlyWeather
	if err := ds.DB.Order("time DESC").First(&weather).Error; err != nil {
		return nil, err
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
func (ds *DataStore) GetHourlyDetections(date, hour string) ([]Note, error) {
	var detections []Note

	startTime := hour + ":00:00"
	endTime := hour + ":59:59"

	err := ds.DB.Where("date = ? AND time >= ? AND time <= ?", date, startTime, endTime).
		Order("time ASC").
		Find(&detections).Error

	return detections, err
}
