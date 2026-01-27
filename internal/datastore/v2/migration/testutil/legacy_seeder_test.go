package testutil

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// setupTestDB creates a temporary SQLite database with the legacy schema.
func setupTestDB(t *testing.T) (*sql.DB, string, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database with GORM to set up schema
	gormDB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)

	// Auto-migrate the legacy schema
	err = gormDB.AutoMigrate(
		&datastore.Note{},
		&datastore.Results{},
		&datastore.NoteReview{},
		&datastore.NoteComment{},
		&datastore.NoteLock{},
		&datastore.DailyEvents{},
		&datastore.HourlyWeather{},
		&datastore.DynamicThreshold{},
		&datastore.ThresholdEvent{},
		&datastore.ImageCache{},
		&datastore.NotificationHistory{},
	)
	require.NoError(t, err)

	// Open raw SQL connection for seeder
	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	cleanup := func() {
		_ = sqlDB.Close()
		_ = os.RemoveAll(tmpDir)
	}

	return sqlDB, dbPath, cleanup
}

func TestLegacySeeder_SeedDetections_Single(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	notes := []datastore.Note{
		NewDetectionBuilder().WithID(1).Build(),
	}

	err := seeder.SeedDetections(notes)
	require.NoError(t, err)

	// Verify insertion
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM notes").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify data
	var id uint
	var scientificName string
	err = db.QueryRow("SELECT id, scientific_name FROM notes WHERE id = 1").Scan(&id, &scientificName)
	require.NoError(t, err)
	assert.Equal(t, uint(1), id)
	assert.Equal(t, "Turdus migratorius", scientificName)
}

func TestLegacySeeder_SeedDetections_Batch(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	// Generate 600 notes to test batching (batchSize is 500)
	notes := GenerateDetections(600)

	err := seeder.SeedDetections(notes)
	require.NoError(t, err)

	// Verify all inserted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM notes").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 600, count)
}

func TestLegacySeeder_SeedDetections_Empty(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	err := seeder.SeedDetections(nil)
	require.NoError(t, err)

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM notes").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestLegacySeeder_SeedResults(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	// First seed a note
	notes := []datastore.Note{
		NewDetectionBuilder().WithID(1).Build(),
	}
	err := seeder.SeedDetections(notes)
	require.NoError(t, err)

	// Seed results
	results := []datastore.Results{
		NewResultsBuilder().WithID(1).WithNoteID(1).WithSpecies("Corvus brachyrhynchos").WithConfidence(0.7).Build(),
		NewResultsBuilder().WithID(2).WithNoteID(1).WithSpecies("Passer domesticus").WithConfidence(0.5).Build(),
	}

	err = seeder.SeedResults(results)
	require.NoError(t, err)

	// Verify
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM results WHERE note_id = 1").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestLegacySeeder_SeedReviews(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	// First seed a note
	notes := []datastore.Note{
		NewDetectionBuilder().WithID(1).Build(),
	}
	err := seeder.SeedDetections(notes)
	require.NoError(t, err)

	// Seed reviews
	reviews := []datastore.NoteReview{
		NewReviewBuilder().WithID(1).WithNoteID(1).AsCorrect().Build(),
	}

	err = seeder.SeedReviews(reviews)
	require.NoError(t, err)

	// Verify
	var verified string
	err = db.QueryRow("SELECT verified FROM note_reviews WHERE note_id = 1").Scan(&verified)
	require.NoError(t, err)
	assert.Equal(t, "correct", verified)
}

func TestLegacySeeder_SeedComments(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	// First seed a note
	notes := []datastore.Note{
		NewDetectionBuilder().WithID(1).Build(),
	}
	err := seeder.SeedDetections(notes)
	require.NoError(t, err)

	// Seed comments
	comments := []datastore.NoteComment{
		NewCommentBuilder().WithID(1).WithNoteID(1).WithEntry("First comment").Build(),
		NewCommentBuilder().WithID(2).WithNoteID(1).WithEntry("Second comment").Build(),
	}

	err = seeder.SeedComments(comments)
	require.NoError(t, err)

	// Verify count
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM note_comments WHERE note_id = 1").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Verify content
	var entry string
	err = db.QueryRow("SELECT entry FROM note_comments WHERE id = 1").Scan(&entry)
	require.NoError(t, err)
	assert.Equal(t, "First comment", entry)
}

func TestLegacySeeder_SeedLocks(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	// First seed a note
	notes := []datastore.Note{
		NewDetectionBuilder().WithID(1).Build(),
	}
	err := seeder.SeedDetections(notes)
	require.NoError(t, err)

	// Seed locks
	lockTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	locks := []datastore.NoteLock{
		NewLockBuilder().WithID(1).WithNoteID(1).WithLockedAt(lockTime).Build(),
	}

	err = seeder.SeedLocks(locks)
	require.NoError(t, err)

	// Verify
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM note_locks WHERE note_id = 1").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestLegacySeeder_SeedWeather(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	// Generate weather data
	dailyEvents, hourlyWeather := GenerateWeatherData(3)

	err := seeder.SeedWeather(dailyEvents, hourlyWeather)
	require.NoError(t, err)

	// Verify daily events
	var dailyCount int
	err = db.QueryRow("SELECT COUNT(*) FROM daily_events").Scan(&dailyCount)
	require.NoError(t, err)
	assert.Equal(t, 3, dailyCount)

	// Verify hourly weather (3 days * 24 hours)
	var hourlyCount int
	err = db.QueryRow("SELECT COUNT(*) FROM hourly_weathers").Scan(&hourlyCount)
	require.NoError(t, err)
	assert.Equal(t, 72, hourlyCount)
}

func TestLegacySeeder_SeedDynamicThresholds(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	thresholds := []datastore.DynamicThreshold{
		NewDynamicThresholdBuilder().WithID(1).WithSpeciesName("american robin").Build(),
		NewDynamicThresholdBuilder().WithID(2).WithSpeciesName("blue jay").Build(),
	}

	err := seeder.SeedDynamicThresholds(thresholds)
	require.NoError(t, err)

	// Verify
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM dynamic_thresholds").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestLegacySeeder_SeedThresholdEvents(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	events := []datastore.ThresholdEvent{
		NewThresholdEventBuilder().WithID(1).WithSpeciesName("american robin").Build(),
	}

	err := seeder.SeedThresholdEvents(events)
	require.NoError(t, err)

	// Verify
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM threshold_events").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestLegacySeeder_SeedImageCaches(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	caches := []datastore.ImageCache{
		NewImageCacheBuilder().WithID(1).WithScientificName("Turdus migratorius").Build(),
		NewImageCacheBuilder().WithID(2).WithScientificName("Corvus brachyrhynchos").Build(),
	}

	err := seeder.SeedImageCaches(caches)
	require.NoError(t, err)

	// Verify
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM image_caches").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestLegacySeeder_SeedNotificationHistory(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	history := []datastore.NotificationHistory{
		NewNotificationHistoryBuilder().WithID(1).WithScientificName("Turdus migratorius").Build(),
	}

	err := seeder.SeedNotificationHistory(history)
	require.NoError(t, err)

	// Verify
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM notification_histories").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestLegacySeeder_SeedAll(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	// Generate comprehensive test data
	notes := GenerateDetections(10)
	data := GenerateRelatedData(notes, &RelatedDataConfig{
		ResultsPerNote:     2,
		ReviewedNoteRatio:  0.5,
		CommentedNoteRatio: 0.3,
		CommentsPerNote:    2,
		LockedNoteRatio:    0.1,
	})

	// Add weather data
	dailyEvents, hourlyWeather := GenerateWeatherData(2)
	data.DailyEvents = dailyEvents
	data.HourlyWeather = hourlyWeather

	// Add other auxiliary data
	data.DynamicThresholds = []datastore.DynamicThreshold{
		NewDynamicThresholdBuilder().WithID(1).Build(),
	}
	data.ImageCaches = []datastore.ImageCache{
		NewImageCacheBuilder().WithID(1).Build(),
	}

	err := seeder.SeedAll(data)
	require.NoError(t, err)

	// Verify all tables have data
	var noteCount int
	err = db.QueryRow("SELECT COUNT(*) FROM notes").Scan(&noteCount)
	require.NoError(t, err)
	assert.Equal(t, 10, noteCount)

	var resultCount int
	err = db.QueryRow("SELECT COUNT(*) FROM results").Scan(&resultCount)
	require.NoError(t, err)
	assert.Equal(t, 20, resultCount) // 10 notes * 2 results each

	var reviewCount int
	err = db.QueryRow("SELECT COUNT(*) FROM note_reviews").Scan(&reviewCount)
	require.NoError(t, err)
	assert.Equal(t, 5, reviewCount) // 50% of 10 notes

	var dailyEventCount int
	err = db.QueryRow("SELECT COUNT(*) FROM daily_events").Scan(&dailyEventCount)
	require.NoError(t, err)
	assert.Equal(t, 2, dailyEventCount)
}

func TestGenerateRelatedData(t *testing.T) {
	t.Parallel()

	notes := GenerateDetections(20)

	data := GenerateRelatedData(notes, &RelatedDataConfig{
		ResultsPerNote:     3,
		ReviewedNoteRatio:  0.5,
		CommentedNoteRatio: 0.4,
		CommentsPerNote:    2,
		LockedNoteRatio:    0.2,
	})

	// Verify notes are included
	assert.Len(t, data.Notes, 20)

	// Verify results (20 notes * 3 each = 60)
	assert.Len(t, data.Results, 60)

	// Verify reviews (50% of 20 = 10)
	assert.Len(t, data.Reviews, 10)

	// Verify comments (40% of 20 = 8 notes * 2 comments = 16)
	assert.Len(t, data.Comments, 16)

	// Verify locks (20% of 20 = 4)
	assert.Len(t, data.Locks, 4)

	// Verify foreign keys are correct
	for _, result := range data.Results {
		assert.LessOrEqual(t, result.NoteID, uint(20))
		assert.GreaterOrEqual(t, result.NoteID, uint(1))
	}

	// Verify review statuses vary
	var correctCount, fpCount int
	for _, review := range data.Reviews {
		if review.Verified == "correct" {
			correctCount++
		} else if review.Verified == "false_positive" {
			fpCount++
		}
	}
	assert.Greater(t, correctCount, 0, "should have some correct reviews")
	assert.Greater(t, fpCount, 0, "should have some false_positive reviews")
}

func TestGenerateRelatedData_DefaultConfig(t *testing.T) {
	t.Parallel()

	notes := GenerateDetections(10)

	// Pass nil config to use defaults
	data := GenerateRelatedData(notes, nil)

	assert.Len(t, data.Notes, 10)
	assert.Greater(t, len(data.Results), 0)
	assert.Greater(t, len(data.Reviews), 0)
}

func TestLegacySeeder_ProcessingTimeStorage(t *testing.T) {
	t.Parallel()

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	seeder := NewLegacySeeder(db)

	// Create note with specific processing time
	processingTime := 250 * time.Millisecond
	notes := []datastore.Note{
		NewDetectionBuilder().WithID(1).WithProcessingTime(processingTime).Build(),
	}

	err := seeder.SeedDetections(notes)
	require.NoError(t, err)

	// Verify processing time is stored as nanoseconds
	var storedValue int64
	err = db.QueryRow("SELECT processing_time FROM notes WHERE id = 1").Scan(&storedValue)
	require.NoError(t, err)

	// Processing time should be stored as nanoseconds (GORM default for time.Duration)
	assert.Equal(t, processingTime.Nanoseconds(), storedValue)
}
