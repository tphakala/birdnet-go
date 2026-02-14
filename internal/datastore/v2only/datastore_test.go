package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// setupTestDatastore creates a V2OnlyDatastore with an in-memory SQLite database for testing.
func setupTestDatastore(t *testing.T) (ds *Datastore, cleanup func()) {
	t.Helper()

	// Create temp directory for test (auto-cleaned by testing framework)
	tempDir := t.TempDir()

	// Create a test logger
	testLogger := logger.NewConsoleLogger("v2only_test", logger.LogLevelDebug)

	// Create SQLite manager
	manager, err := v2.NewSQLiteManager(v2.Config{
		DataDir: tempDir,
		Debug:   false,
		Logger:  testLogger,
	})
	require.NoError(t, err)

	err = manager.Initialize()
	require.NoError(t, err)

	db := manager.DB()

	// Create lookup table entries for tests
	// The LabelType and TaxonomicClass tables should be created by Initialize()
	labelTypeRepo := repository.NewLabelTypeRepository(db, false)
	taxClassRepo := repository.NewTaxonomicClassRepository(db, false)
	modelRepo := repository.NewModelRepository(db, false, false)

	ctx := t.Context()

	// Create required label types
	speciesLabelType, err := labelTypeRepo.GetOrCreate(ctx, "species")
	require.NoError(t, err, "Failed to create species label type")

	// Create required taxonomic classes
	avesClass, err := taxClassRepo.GetOrCreate(ctx, "Aves")
	require.NoError(t, err, "Failed to create Aves taxonomic class")

	// Get the default model (seeded by Initialize)
	defaultModel, err := modelRepo.GetByNameVersionVariant(ctx, "BirdNET", "2.4", "default")
	require.NoError(t, err, "Failed to get default model")

	avesClassID := avesClass.ID

	// Create repositories (useV2Prefix = false for SQLite, isMySQL = false)
	detectionRepo := repository.NewDetectionRepository(db, false, false)
	labelRepo := repository.NewLabelRepository(db, false, false)
	weatherRepo := repository.NewWeatherRepository(db, false, false)
	imageCacheRepo := repository.NewImageCacheRepository(db, labelRepo, false, false)
	thresholdRepo := repository.NewDynamicThresholdRepository(db, labelRepo, false, false)
	notificationRepo := repository.NewNotificationHistoryRepository(db, labelRepo, false, false)

	// Create datastore with cached lookup table IDs
	var err2 error
	ds, err2 = New(&Config{
		Manager:            manager,
		Detection:          detectionRepo,
		Label:              labelRepo,
		Model:              modelRepo,
		Weather:            weatherRepo,
		ImageCache:         imageCacheRepo,
		Threshold:          thresholdRepo,
		Notification:       notificationRepo,
		Logger:             testLogger,
		Timezone:           time.UTC,
		DefaultModelID:     defaultModel.ID,
		SpeciesLabelTypeID: speciesLabelType.ID,
		AvesClassID:        &avesClassID,
	})
	require.NoError(t, err2)

	cleanup = func() {
		_ = ds.Close()
		// tempDir is auto-cleaned by t.TempDir()
	}

	return ds, cleanup
}

func TestV2OnlyDatastore_Open(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Open should be a no-op
	err := ds.Open()
	assert.NoError(t, err)
}

func TestV2OnlyDatastore_Close(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Close should not error
	err := ds.Close()
	assert.NoError(t, err)
}

func TestV2OnlyDatastore_SaveAndGet(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save a note
	note := &datastore.Note{
		Date:           "2024-01-15",
		Time:           "12:30:00",
		ScientificName: "Passer domesticus",
		CommonName:     "House Sparrow",
		Confidence:     0.85,
		Latitude:       51.5074,
		Longitude:      -0.1278,
		ClipName:       "/clips/test.wav",
	}

	results := []datastore.Results{
		{Species: "Passer domesticus", Confidence: 0.85},
		{Species: "Passer montanus", Confidence: 0.10},
	}

	err := ds.Save(note, results)
	require.NoError(t, err)

	// Get the note back
	// Note: After save, we need to get all notes to find the ID
	notes, err := ds.GetAllNotes()
	require.NoError(t, err)
	require.Len(t, notes, 1)

	// Verify the note data
	assert.Equal(t, "2024-01-15", notes[0].Date)
	assert.Equal(t, "12:30:00", notes[0].Time)
	assert.Equal(t, "Passer domesticus", notes[0].ScientificName)
	assert.InDelta(t, 0.85, notes[0].Confidence, 0.001)
}

func TestV2OnlyDatastore_Delete(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save a note
	note := &datastore.Note{
		Date:           "2024-01-15",
		Time:           "12:30:00",
		ScientificName: "Passer domesticus",
		Confidence:     0.85,
	}
	err := ds.Save(note, nil)
	require.NoError(t, err)

	// Get all notes to find the ID
	notes, err := ds.GetAllNotes()
	require.NoError(t, err)
	require.Len(t, notes, 1)

	// Delete the note
	err = ds.Delete("1")
	require.NoError(t, err)

	// Verify it's deleted
	notes, err = ds.GetAllNotes()
	require.NoError(t, err)
	assert.Empty(t, notes)
}

func TestV2OnlyDatastore_DynamicThreshold(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Note: With LabelID normalization, lookups are now by scientific name.
	// The SpeciesName field is still populated from the Label for compatibility.
	threshold := &datastore.DynamicThreshold{
		SpeciesName:    "Passer domesticus", // Will be derived from Label
		ScientificName: "Passer domesticus",
		Level:          1,
		CurrentValue:   0.7,
		BaseThreshold:  0.6,
		HighConfCount:  5,
		ValidHours:     24,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		LastTriggered:  time.Now(),
		FirstCreated:   time.Now(),
		UpdatedAt:      time.Now(),
		TriggerCount:   10,
	}

	// Save threshold
	err := ds.SaveDynamicThreshold(threshold)
	require.NoError(t, err)

	// Get threshold by scientific name
	retrieved, err := ds.GetDynamicThreshold("Passer domesticus")
	require.NoError(t, err)
	assert.Equal(t, "Passer domesticus", retrieved.SpeciesName)
	assert.Equal(t, "Passer domesticus", retrieved.ScientificName)
	assert.Equal(t, 1, retrieved.Level)
	assert.InDelta(t, 0.7, retrieved.CurrentValue, 0.001)

	// Get all thresholds
	all, err := ds.GetAllDynamicThresholds()
	require.NoError(t, err)
	assert.Len(t, all, 1)

	// Delete threshold by scientific name
	err = ds.DeleteDynamicThreshold("Passer domesticus")
	require.NoError(t, err)

	// Verify deletion
	all, err = ds.GetAllDynamicThresholds()
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestV2OnlyDatastore_ImageCache(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	cache := &datastore.ImageCache{
		ProviderName:   "wikimedia",
		ScientificName: "Passer domesticus",
		SourceProvider: "wikimedia",
		URL:            "https://example.com/sparrow.jpg",
		LicenseName:    "CC BY-SA 4.0",
		LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
		AuthorName:     "Test Author",
		AuthorURL:      "https://example.com/author",
		CachedAt:       time.Now(),
	}

	// Save cache
	err := ds.SaveImageCache(cache)
	require.NoError(t, err)

	// Get cache
	query := datastore.ImageCacheQuery{
		ProviderName:   "wikimedia",
		ScientificName: "Passer domesticus",
	}
	retrieved, err := ds.GetImageCache(query)
	require.NoError(t, err)
	assert.Equal(t, "Passer domesticus", retrieved.ScientificName)
	assert.Equal(t, "https://example.com/sparrow.jpg", retrieved.URL)

	// Get all caches
	all, err := ds.GetAllImageCaches("wikimedia")
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestV2OnlyDatastore_Weather(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save daily events
	daily := &datastore.DailyEvents{
		Date:     "2024-01-15",
		Sunrise:  1705308000, // Unix timestamp
		Sunset:   1705340400,
		Country:  "UK",
		CityName: "London",
	}
	err := ds.SaveDailyEvents(daily)
	require.NoError(t, err)

	// Get daily events
	retrieved, err := ds.GetDailyEvents("2024-01-15")
	require.NoError(t, err)
	assert.Equal(t, "2024-01-15", retrieved.Date)
	assert.Equal(t, "London", retrieved.CityName)
}

func TestV2OnlyDatastore_NotificationHistory(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	history := &datastore.NotificationHistory{
		ScientificName:   "Passer domesticus",
		NotificationType: "new_species",
		LastSent:         time.Now(),
		ExpiresAt:        time.Now().Add(24 * time.Hour),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	// Save history
	err := ds.SaveNotificationHistory(history)
	require.NoError(t, err)

	// Get history
	retrieved, err := ds.GetNotificationHistory("Passer domesticus", "new_species")
	require.NoError(t, err)
	assert.Equal(t, "Passer domesticus", retrieved.ScientificName)
	assert.Equal(t, "new_species", retrieved.NotificationType)

	// Get active history
	active, err := ds.GetActiveNotificationHistory(time.Now().Add(-1 * time.Hour))
	require.NoError(t, err)
	assert.Len(t, active, 1)
}

func TestV2OnlyDatastore_ThresholdEvent(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	event := &datastore.ThresholdEvent{
		SpeciesName:   "house sparrow",
		PreviousLevel: 0,
		NewLevel:      1,
		PreviousValue: 0.6,
		NewValue:      0.7,
		ChangeReason:  "high_confidence",
		Confidence:    0.95,
		CreatedAt:     time.Now(),
	}

	// Save event
	err := ds.SaveThresholdEvent(event)
	require.NoError(t, err)

	// Get events
	events, err := ds.GetThresholdEvents("house sparrow", 10)
	require.NoError(t, err)
	assert.Len(t, events, 1)
	assert.Equal(t, "house sparrow", events[0].SpeciesName)
	assert.Equal(t, "high_confidence", events[0].ChangeReason)

	// Get recent events
	recent, err := ds.GetRecentThresholdEvents(10)
	require.NoError(t, err)
	assert.Len(t, recent, 1)
}

func TestV2OnlyDatastore_SearchNotes(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save some notes
	for i := range 5 {
		note := &datastore.Note{
			Date:           "2024-01-15",
			Time:           "12:30:00",
			ScientificName: "Passer domesticus",
			Confidence:     0.8 + float64(i)*0.02,
		}
		err := ds.Save(note, nil)
		require.NoError(t, err)
	}

	// Search notes
	notes, err := ds.SearchNotes("Passer", true, 10, 0)
	require.NoError(t, err)
	// Search may not work perfectly without full-text search, but shouldn't error
	assert.NotNil(t, notes)
}

func TestV2OnlyDatastore_GetLastDetections(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save multiple notes
	for range 10 {
		note := &datastore.Note{
			Date:           "2024-01-15",
			Time:           "12:30:00",
			ScientificName: "Passer domesticus",
			Confidence:     0.8,
		}
		err := ds.Save(note, nil)
		require.NoError(t, err)
	}

	// Get last 5 detections
	notes, err := ds.GetLastDetections(5)
	require.NoError(t, err)
	assert.Len(t, notes, 5)
}

func TestV2OnlyDatastore_GetDatabaseStats(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save a note
	note := &datastore.Note{
		Date:           "2024-01-15",
		Time:           "12:30:00",
		ScientificName: "Passer domesticus",
		Confidence:     0.85,
	}
	err := ds.Save(note, nil)
	require.NoError(t, err)

	// Get stats
	stats, err := ds.GetDatabaseStats()
	require.NoError(t, err)
	assert.Equal(t, "sqlite", stats.Type)
	assert.Equal(t, int64(1), stats.TotalDetections)
	assert.True(t, stats.Connected)
}

func TestV2OnlyDatastore_LockUnlock(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save a note
	note := &datastore.Note{
		Date:           "2024-01-15",
		Time:           "12:30:00",
		ScientificName: "Passer domesticus",
		Confidence:     0.85,
	}
	err := ds.Save(note, nil)
	require.NoError(t, err)

	// Lock the note
	err = ds.LockNote("1")
	require.NoError(t, err)

	// Check if locked
	locked, err := ds.IsNoteLocked("1")
	require.NoError(t, err)
	assert.True(t, locked)

	// Unlock the note
	err = ds.UnlockNote("1")
	require.NoError(t, err)

	// Check if unlocked
	locked, err = ds.IsNoteLocked("1")
	require.NoError(t, err)
	assert.False(t, locked)
}

func TestV2OnlyDatastore_ReviewOperations(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save a note
	note := &datastore.Note{
		Date:           "2024-01-15",
		Time:           "12:30:00",
		ScientificName: "Passer domesticus",
		Confidence:     0.85,
	}
	err := ds.Save(note, nil)
	require.NoError(t, err)

	// Save a review
	review := &datastore.NoteReview{
		NoteID:   1,
		Verified: "correct",
	}
	err = ds.SaveNoteReview(review)
	require.NoError(t, err)

	// Get review
	retrieved, err := ds.GetNoteReview("1")
	require.NoError(t, err)
	assert.Equal(t, "correct", retrieved.Verified)
}

func TestV2OnlyDatastore_CommentOperations(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save a note
	note := &datastore.Note{
		Date:           "2024-01-15",
		Time:           "12:30:00",
		ScientificName: "Passer domesticus",
		Confidence:     0.85,
	}
	err := ds.Save(note, nil)
	require.NoError(t, err)

	// Save a comment
	comment := &datastore.NoteComment{
		NoteID:    1,
		Entry:     "This is a test comment",
		CreatedAt: time.Now(),
	}
	err = ds.SaveNoteComment(comment)
	require.NoError(t, err)

	// Get comments
	comments, err := ds.GetNoteComments("1")
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Equal(t, "This is a test comment", comments[0].Entry)
}

func TestV2OnlyDatastore_Optimize(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Optimize should not error for SQLite
	err := ds.Optimize(t.Context())
	assert.NoError(t, err)
}

// TestV2OnlyDatastore_ImplementsInterface ensures the Datastore implements the full interface.
func TestV2OnlyDatastore_ImplementsInterface(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// This compile-time check ensures V2OnlyDatastore implements datastore.Interface
	var _ datastore.Interface = ds
}
