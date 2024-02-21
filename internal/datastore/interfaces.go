package datastore

import (
	"fmt"
	"log"
	"strconv"

	"github.com/tphakala/birdnet-go/internal/conf"
	"gorm.io/gorm"
)

// StoreInterface abstracts the underlying database implementation and defines the interface for database operations.
type Interface interface {
	Open() error
	Save(note Note) error
	Delete(id string) error
	Close() error
	GetAllNotes() ([]Note, error)
	GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]Note, error)
	GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error)
	SpeciesDetections(species, date, hour string, sortAscending bool) ([]Note, error)
	GetLastDetections(numDetections int) ([]Note, error)
	SearchNotes(query string, sortAscending bool, limit int, offset int) ([]Note, error)
	GetNoteClipPath(noteID string) (string, error)
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

// Delete removes a note from the database by its ID.
func (ds *DataStore) Delete(id string) error {
	// Convert the id from string to integer
	var noteID int
	var err error
	if noteID, err = strconv.Atoi(id); err != nil {
		return fmt.Errorf("error converting ID to integer: %s", err)
	}

	// Perform the deletion using the converted integer ID
	result := ds.DB.Delete(&Note{}, noteID)
	if result.Error != nil {
		return fmt.Errorf("error deleting note with ID %d: %w", noteID, result.Error)
	}
	return nil
}

// GetNoteClipPath retrieves the path to the audio clip associated with a note.
func (ds *DataStore) GetNoteClipPath(noteID string) (string, error) {
	var clipPath struct {
		ClipName string
	}

	err := ds.DB.Model(&Note{}).
		Select("clip_name").
		Where("id = ?", noteID).
		First(&clipPath).Error // Use First to retrieve a single record

	if err != nil {
		return "", fmt.Errorf("failed to retrieve clip path: %w", err)
	}

	return clipPath.ClipName, nil
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
		Select("common_name, COUNT(*) as count").
		Where("date = ? AND confidence >= ?", selectedDate, minConfidenceNormalized).
		Group("common_name").
		//Having("COUNT(*) > ?", 1).
		Order("count DESC").
		Limit(reportCount).
		Scan(&results).Error

	return results, err
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
func (ds *DataStore) SpeciesDetections(species, date, hour string, sortAscending bool) ([]Note, error) {
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

	query = query.Order("id " + sortOrder)

	var detections []Note
	err := query.Find(&detections).Error
	return detections, err
}

// GetLastDetections retrieves the most recent bird detections.
func (ds *DataStore) GetLastDetections(numDetections int) ([]Note, error) {
	var notes []Note
	if result := ds.DB.Order("date DESC, time DESC").Limit(numDetections).Find(&notes); result.Error != nil {
		return nil, fmt.Errorf("error getting last detections: %w", result.Error)
	}
	return notes, nil
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
	if err := db.AutoMigrate(&Note{}); err != nil {
		return fmt.Errorf("failed to auto-migrate %s database: %v", dbType, err)
	}

	if debug {
		log.Printf("%s database connection initialized: %s", dbType, connectionInfo)
	}

	return nil
}

// sortOrderAscendingString returns "ASC" or "DESC" based on the boolean input.
func sortAscendingString(asc bool) string {
	if asc {
		return "ASC"
	}
	return "DESC"
}
