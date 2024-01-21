package model

import (
	"fmt"
	"log"

	"github.com/tphakala/birdnet-go/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// DataAccess defines the interface for database operations.
// It abstracts the underlying database implementation.
type StoreInterface interface {
	Open(ctx *config.Context) error
	Save(ctx *config.Context, note Note) error
	Close() error
	GetAllNotes() ([]Note, error)
	GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]Note, error)
	GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error)
	SpeciesDetections(species, date, hour string, sortAscending bool) ([]Note, error)
	GetLastDetections(numDetections int) ([]Note, error)
	SearchNotes(query string, sortAscending bool, limit int, offset int) ([]Note, error)
	// Add other methods as needed.
}

// ObservationDB implements the DataStore interface using a GORM database.
type DataStore struct {
	DB *gorm.DB
}

// GetAllNotes retrieves all notes from the database.
func (ds *DataStore) GetAllNotes() ([]Note, error) {
	var notes []Note
	if result := ds.DB.Find(&notes); result.Error != nil {
		return nil, fmt.Errorf("error getting all notes: %w", result.Error)
	}
	return notes, nil
}

// GetTopBirdsData retrieves data of top bird sightings based on date and confidence.
func (ds *DataStore) GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]Note, error) {
	var results []Note

	// temporary assignment, this will be selectable in web ui
	var reportCount = 30

	err := ds.DB.Table("notes").
		Select("common_name, COUNT(*) as count").
		Where("date = ? AND confidence >= ?", selectedDate, minConfidenceNormalized).
		Group("common_name").
		Order("count DESC").
		Limit(reportCount).
		Scan(&results).Error
	return results, err
}

// GetHourFormat returns the database-specific SQL fragment for formatting a time column as hour.
func (ds *DataStore) GetHourFormat() string {
	switch ds.DB.Dialector.Name() {
	case "sqlite":
		return "strftime('%H', time)"
	case "mysql":
		return "DATE_FORMAT(time, '%H')"
	default:
		// SQLite and MySQL are the only supported databases at the moment
		return ""
	}
}

// GetHourlyOccurrences retrieves the hourly occurrences of a species.
func (ds *DataStore) GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
	var hourlyCounts [24]int
	var results []struct {
		Hour  int
		Count int
	}

	// Get the database-specific SQL fragment for formatting a time column as hour
	hourFormat := ds.GetHourFormat()

	// Start measuring time
	//startTime := time.Now()

	err := ds.DB.Model(&Note{}).
		Select(fmt.Sprintf("%s as hour, COUNT(*) as count", hourFormat)).
		Where("date = ? AND common_name = ? AND confidence >= ?", date, commonName, minConfidenceNormalized).
		Group(hourFormat).
		Scan(&results).Error

	//log.Printf("Time to query hourly occurrences for note %s: %v", commonName, time.Since(startTime)) // Print the time taken for this part

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

// SpeciesDetections retrieves detections of a specific species on a given date.
func (ds *DataStore) SpeciesDetections(species, date, hour string, sortAscending bool) ([]Note, error) {
	sortOrder := "DESC"
	if sortAscending {
		sortOrder = "ASC"
	}

	query := ds.DB.Where("common_name = ? AND date = ?", species, date)
	if hour != "" {
		// Pad hour with 0 if it's a single digit
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

// GetLastDetections retrieves the last set of detections.
func (ds *DataStore) GetLastDetections(numDetections int) ([]Note, error) {
	var notes []Note
	if result := ds.DB.Order("date DESC, time DESC").Limit(numDetections).Find(&notes); result.Error != nil {
		return nil, fmt.Errorf("error getting last detections: %w", result.Error)
	}
	return notes, nil
}

// SearchNotes searches notes by common or scientific name with an option to sort the results by date and time.
// Pagination is implemented with limit and offset.
func (ds *DataStore) SearchNotes(query string, sortAscending bool, limit int, offset int) ([]Note, error) {
	var notes []Note
	sortOrder := "DESC"
	if sortAscending {
		sortOrder = "ASC"
	}

	// Use LIMIT and OFFSET for pagination
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

// NewObservationDB creates a new ObservationDB instance.
func NewDataStore(db *gorm.DB) *DataStore {
	return &DataStore{DB: db}
}

// InitializeDatabase initializes and migrates the database.
func InitializeDatabase(databasePath string) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("error opening database: %w", err)
	}

	if err = db.AutoMigrate(&Note{}); err != nil {
		return nil, fmt.Errorf("error migrating database: %w", err)
	}

	return db, nil
}

// performAutoMigration performs the database migration for the given GORM DB instance.
func performAutoMigration(db *gorm.DB, debug bool, dbType, connectionInfo string) error {
	if err := db.AutoMigrate(&Note{}); err != nil {
		return fmt.Errorf("failed to auto-migrate %s database: %v", dbType, err)
	}

	if debug {
		log.Printf("%s database connection initialized: %s", dbType, connectionInfo)
	}

	return nil
}
