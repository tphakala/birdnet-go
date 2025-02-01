// interfaces.go: this code defines the interface for the database operations
package datastore

import (
	"errors"
	"fmt"
	"log"
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
	GetHourlyDetections(date, hour string, duration int) ([]Note, error)
	CountSpeciesDetections(species, date, hour string, duration int) (int64, error)
	CountSearchResults(query string) (int64, error)
	Transaction(fc func(tx *gorm.DB) error) error
	// Lock management methods
	LockNote(noteID string) error
	UnlockNote(noteID string) error
	GetNoteLock(noteID string) (*NoteLock, error)
	IsNoteLocked(noteID string) (bool, error)
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
				return fmt.Errorf("saving note: %w", err)
			}

			// Assign the note ID to each result and save them
			for _, result := range results {
				result.NoteID = note.ID
				if err := tx.Create(&result).Error; err != nil {
					tx.Rollback()
					if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
						return err
					}
					return fmt.Errorf("saving result: %w", err)
				}
			}

			// Commit the transaction
			if err := tx.Commit().Error; err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
					return err
				}
				return fmt.Errorf("committing transaction: %w", err)
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
	// Retrieve the note by its ID with Review, Lock, and Comments preloaded
	if err := ds.DB.Preload("Review").Preload("Lock").Preload("Comments", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC") // Order comments by creation time, newest first
	}).First(&note, noteID).Error; err != nil {
		return Note{}, fmt.Errorf("getting note with ID %d: %w", noteID, err)
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
		return fmt.Errorf("converting ID to integer: %w", err)
	}

	// Check if the note is locked
	isLocked, err := ds.IsNoteLocked(id)
	if err != nil {
		return fmt.Errorf("checking note lock status: %w", err)
	}
	if isLocked {
		return fmt.Errorf("cannot delete note: note is locked")
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
func (ds *DataStore) GetClipsQualifyingForRemoval(minHours, minClips int) ([]ClipForRemoval, error) {
	// Validate input parameters
	if minHours <= 0 || minClips <= 0 {
		return nil, fmt.Errorf("invalid parameters: minHours and minClips must be greater than 0")
	}

	var results []ClipForRemoval

	// Define a subquery to count the number of recordings per scientific name
	subquery := ds.DB.Model(&Note{}).Select("ID, scientific_name, ROW_NUMBER() OVER (PARTITION BY scientific_name) as num_recordings").
		Where("clip_name != ''").
		// Exclude notes that have a lock
		Joins("LEFT JOIN note_locks ON notes.id = note_locks.note_id").
		Where("note_locks.id IS NULL")

	if err := subquery.Error; err != nil {
		return nil, fmt.Errorf("error creating subquery: %w", err)
	}

	// Main query to find clips qualifying for removal based on retention policy
	err := ds.DB.Table("(?) AS n", ds.DB.Model(&Note{})).
		Select("n.ID, n.scientific_name, n.clip_name, sub.num_recordings").
		Joins("INNER JOIN (?) AS sub ON n.ID = sub.ID", subquery).
		// Exclude notes that have a lock
		Joins("LEFT JOIN note_locks ON n.id = note_locks.note_id").
		Where("note_locks.id IS NULL").
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
func (ds *DataStore) SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit, offset int) ([]Note, error) {
	sortOrder := sortAscendingString(sortAscending)

	query := ds.DB.Preload("Review").Preload("Lock").Preload("Comments", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC") // Order comments by creation time, newest first
	}).Where("common_name = ? AND date = ?", species, date)
	if hour != "" {
		startTime, endTime := getHourRange(hour, duration)
		query = query.Where("time >= ? AND time < ?", startTime, endTime)
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
		return nil, fmt.Errorf("error getting last detections: %w", result.Error)
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

	return results, err
}

// SearchNotes performs a search on notes with optional sorting, pagination, and limits.
func (ds *DataStore) SearchNotes(query string, sortAscending bool, limit, offset int) ([]Note, error) {
	var notes []Note
	sortOrder := sortAscendingString(sortAscending)

	err := ds.DB.Preload("Review").Preload("Comments", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC") // Order comments by creation time, newest first
	}).Where("common_name LIKE ? OR scientific_name LIKE ?", "%"+query+"%", "%"+query+"%").
		Order("id " + sortOrder).
		Limit(limit).
		Offset(offset).
		Find(&notes).Error

	// Populate virtual Verified field
	for i := range notes {
		if notes[i].Review != nil {
			notes[i].Verified = notes[i].Review.Verified
		}
	}

	if err != nil {
		return nil, fmt.Errorf("error searching notes: %w", err)
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

// GetHourlyDetections retrieves bird detections for a specific date and hour.
func (ds *DataStore) GetHourlyDetections(date, hour string, duration int) ([]Note, error) {
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

// UpdateNote updates specific fields of a note. It validates the input parameters
// and returns appropriate errors if the note doesn't exist or if the update fails.
func (ds *DataStore) UpdateNote(id string, updates map[string]interface{}) error {
	if id == "" {
		return fmt.Errorf("invalid id: must not be empty")
	}
	if len(updates) == 0 {
		return fmt.Errorf("no updates provided")
	}

	result := ds.DB.Model(&Note{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("failed to update note: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("note with id %s not found", id)
	}

	return nil
}

// GetNoteReview retrieves the review status for a note
func (ds *DataStore) GetNoteReview(noteID string) (*NoteReview, error) {
	var review NoteReview
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid note ID: %w", err)
	}

	// Use Session to temporarily modify logger config for this query
	err = ds.DB.Session(&gorm.Session{
		Logger: ds.DB.Logger.LogMode(logger.Silent),
	}).Where("note_id = ?", id).First(&review).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // Return nil if no review exists
		}
		return nil, fmt.Errorf("error getting note review: %w", err)
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
		return fmt.Errorf("failed to save note review: %w", result.Error)
	}

	return nil
}

// GetNoteComments retrieves all comments for a note
func (ds *DataStore) GetNoteComments(noteID string) ([]NoteComment, error) {
	var comments []NoteComment
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid note ID: %w", err)
	}

	err = ds.DB.Where("note_id = ?", id).Order("created_at DESC").Find(&comments).Error
	if err != nil {
		return nil, fmt.Errorf("error getting note comments: %w", err)
	}

	return comments, nil
}

// SaveNoteComment saves a new comment for a note
func (ds *DataStore) SaveNoteComment(comment *NoteComment) error {
	// Validate input
	if comment == nil {
		return fmt.Errorf("comment cannot be nil")
	}
	if comment.NoteID == 0 {
		return fmt.Errorf("note ID cannot be zero")
	}
	// Entry can be empty as comments are optional, but if provided, check length
	if len(comment.Entry) > 1000 {
		return fmt.Errorf("comment entry exceeds maximum length of 1000 characters")
	}

	if err := ds.DB.Create(comment).Error; err != nil {
		return fmt.Errorf("failed to save note comment: %w", err)
	}
	return nil
}

// DeleteNoteComment deletes a comment
func (ds *DataStore) DeleteNoteComment(commentID string) error {
	id, err := strconv.ParseUint(commentID, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid comment ID: %w", err)
	}

	if err := ds.DB.Delete(&NoteComment{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete note comment: %w", err)
	}
	return nil
}

// UpdateNoteComment updates an existing comment's entry
func (ds *DataStore) UpdateNoteComment(commentID, entry string) error {
	id, err := strconv.ParseUint(commentID, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid comment ID: %w", err)
	}

	result := ds.DB.Model(&NoteComment{}).Where("id = ?", id).Updates(map[string]interface{}{
		"entry":      entry,
		"updated_at": time.Now(),
	})

	if result.Error != nil {
		return fmt.Errorf("failed to update note comment: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("comment with ID %s not found", commentID)
	}

	return nil
}

// getHourRange returns the start and end times for a given hour and duration.
func getHourRange(hour string, duration int) (startTime, endTime string) {
	startHour, _ := strconv.Atoi(hour)
	endHour := (startHour + duration) % 24
	startTime = fmt.Sprintf("%02d:00:00", startHour)
	endTime = fmt.Sprintf("%02d:00:00", endHour)
	return startTime, endTime
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
		return fmt.Errorf("transaction function cannot be nil")
	}
	return ds.DB.Transaction(fc)
}

// LockNote creates or updates a lock for a note
func (ds *DataStore) LockNote(noteID string) error {
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid note ID: %w", err)
	}

	// Generate a unique transaction ID (first 8 chars of UUID)
	txID := fmt.Sprintf("tx-%s", uuid.New().String()[:8])

	// Retry configuration
	maxRetries := 5
	baseDelay := 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		lock := &NoteLock{
			NoteID:   uint(id),
			LockedAt: time.Now(),
		}

		// Use upsert operation to either create or update the lock
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
			return fmt.Errorf("failed to lock note: %w", result.Error)
		}

		// Log if retry count is not 0 and transaction was successful
		if attempt > 0 {
			log.Printf("[%s] Database transaction successful after %d attempts", txID, attempt+1)
		}

		return nil
	}

	return fmt.Errorf("[%s] failed after %d attempts: %w", txID, maxRetries, lastErr)
}

// UnlockNote removes a lock from a note
func (ds *DataStore) UnlockNote(noteID string) error {
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid note ID: %w", err)
	}

	// Generate a unique transaction ID (first 8 chars of UUID)
	txID := fmt.Sprintf("tx-%s", uuid.New().String()[:8])

	// Retry configuration
	maxRetries := 5
	baseDelay := 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		result := ds.DB.Where("note_id = ?", id).Delete(&NoteLock{})

		if result.Error != nil {
			if strings.Contains(strings.ToLower(result.Error.Error()), "database is locked") {
				delay := baseDelay * time.Duration(attempt+1)
				log.Printf("[%s] Database locked, retrying in %v (attempt %d/%d)", txID, delay, attempt+1, maxRetries)
				time.Sleep(delay)
				lastErr = result.Error
				continue
			}
			return fmt.Errorf("failed to unlock note: %w", result.Error)
		}

		// Log if retry count is not 0 and transaction was successful
		if attempt > 0 {
			log.Printf("[%s] Database transaction successful after %d attempts", txID, attempt+1)
		}

		return nil
	}

	return fmt.Errorf("[%s] failed after %d attempts: %w", txID, maxRetries, lastErr)
}

// GetNoteLock retrieves the lock status for a note
func (ds *DataStore) GetNoteLock(noteID string) (*NoteLock, error) {
	id, err := strconv.ParseUint(noteID, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid note ID: %w", err)
	}

	var lock NoteLock
	err = ds.DB.Where("note_id = ?", id).First(&lock).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // Return nil if no lock exists
		}
		return nil, fmt.Errorf("error getting note lock: %w", err)
	}

	return &lock, nil
}

// IsNoteLocked checks if a note is locked
func (ds *DataStore) IsNoteLocked(noteID string) (bool, error) {
	lock, err := ds.GetNoteLock(noteID)
	if err != nil {
		return false, err
	}
	return lock != nil, nil
}
