package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// buildTestConfig constructs the shared repositories and Config for in-memory test datastores.
func buildTestConfig(t *testing.T, labels []string) (cfg *Config, cleanup func()) {
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
	sourceRepo := repository.NewAudioSourceRepository(db, false, false)
	weatherRepo := repository.NewWeatherRepository(db, false, false)
	imageCacheRepo := repository.NewImageCacheRepository(db, labelRepo, false, false)
	thresholdRepo := repository.NewDynamicThresholdRepository(db, labelRepo, false, false)
	notificationRepo := repository.NewNotificationHistoryRepository(db, labelRepo, false, false)

	cfg = &Config{
		Manager:            manager,
		Detection:          detectionRepo,
		Label:              labelRepo,
		Model:              modelRepo,
		Source:             sourceRepo,
		Weather:            weatherRepo,
		ImageCache:         imageCacheRepo,
		Threshold:          thresholdRepo,
		Notification:       notificationRepo,
		Logger:             testLogger,
		Timezone:           time.UTC,
		DefaultModelID:     defaultModel.ID,
		SpeciesLabelTypeID: speciesLabelType.ID,
		AvesClassID:        &avesClassID,
		Labels:             labels,
	}

	// tempDir is auto-cleaned by t.TempDir(); no additional cleanup needed
	return cfg, func() {}
}

// setupTestDatastore creates a V2OnlyDatastore with an in-memory SQLite database for testing.
func setupTestDatastore(t *testing.T) (ds *Datastore, cleanup func()) {
	t.Helper()
	cfg, cfgCleanup := buildTestConfig(t, nil)
	ds, err := New(cfg)
	require.NoError(t, err)
	return ds, func() { _ = ds.Close(); cfgCleanup() }
}

// setupTestDatastoreWithLabels creates a V2OnlyDatastore with species label mappings for testing.
// Labels provide the species map (common name → scientific name) and
// common name map (scientific name → common name) used by dynamic threshold methods.
func setupTestDatastoreWithLabels(t *testing.T, labels []string) (ds *Datastore, cleanup func()) {
	t.Helper()
	cfg, cfgCleanup := buildTestConfig(t, labels)
	ds, err := New(cfg)
	require.NoError(t, err)
	return ds, func() { _ = ds.Close(); cfgCleanup() }
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

// TestV2OnlyDatastore_DynamicThreshold_CommonNameDisplay verifies that
// GetDynamicThreshold and GetAllDynamicThresholds return common names
// in SpeciesName when a label mapping exists (Bug 1 fix).
func TestV2OnlyDatastore_DynamicThreshold_CommonNameDisplay(t *testing.T) {
	labels := []string{
		"Parus major_Great Tit",
		"Turdus merula_Eurasian Blackbird",
	}
	ds, cleanup := setupTestDatastoreWithLabels(t, labels)
	defer cleanup()

	// Save threshold using scientific name (as the processor does)
	threshold := &datastore.DynamicThreshold{
		SpeciesName:    "Parus major",
		ScientificName: "Parus major",
		Level:          2,
		CurrentValue:   0.5,
		BaseThreshold:  0.8,
		HighConfCount:  3,
		ValidHours:     12,
		ExpiresAt:      time.Now().Add(12 * time.Hour),
		LastTriggered:  time.Now(),
		FirstCreated:   time.Now(),
		UpdatedAt:      time.Now(),
		TriggerCount:   5,
	}
	err := ds.SaveDynamicThreshold(threshold)
	require.NoError(t, err)

	// GetDynamicThreshold should return common name in SpeciesName
	retrieved, err := ds.GetDynamicThreshold("Parus major")
	require.NoError(t, err)
	assert.Equal(t, "Great Tit", retrieved.SpeciesName, "SpeciesName should be common name")
	assert.Equal(t, "Parus major", retrieved.ScientificName, "ScientificName should stay scientific")

	// GetAllDynamicThresholds should also return common name
	all, err := ds.GetAllDynamicThresholds()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "Great Tit", all[0].SpeciesName, "SpeciesName should be common name in list")
	assert.Equal(t, "Parus major", all[0].ScientificName, "ScientificName should stay scientific in list")
}

// TestV2OnlyDatastore_DynamicThreshold_DeleteByCommonName verifies that
// DeleteDynamicThreshold works when called with a common name (Bug 2 fix).
// The processor uses lowercase common names as map keys and passes them
// to the datastore's delete method.
func TestV2OnlyDatastore_DynamicThreshold_DeleteByCommonName(t *testing.T) {
	labels := []string{
		"Parus major_Great Tit",
	}
	ds, cleanup := setupTestDatastoreWithLabels(t, labels)
	defer cleanup()

	// Save threshold with scientific name
	threshold := &datastore.DynamicThreshold{
		SpeciesName:    "Parus major",
		ScientificName: "Parus major",
		Level:          1,
		CurrentValue:   0.6,
		BaseThreshold:  0.8,
		ValidHours:     12,
		ExpiresAt:      time.Now().Add(12 * time.Hour),
		LastTriggered:  time.Now(),
		FirstCreated:   time.Now(),
		UpdatedAt:      time.Now(),
	}
	err := ds.SaveDynamicThreshold(threshold)
	require.NoError(t, err)

	// Verify it exists
	all, err := ds.GetAllDynamicThresholds()
	require.NoError(t, err)
	require.Len(t, all, 1)

	// Delete using lowercase common name (what the processor sends after Bug 1 fix)
	err = ds.DeleteDynamicThreshold("great tit")
	require.NoError(t, err)

	// Verify deletion
	all, err = ds.GetAllDynamicThresholds()
	require.NoError(t, err)
	assert.Empty(t, all, "threshold should be deleted when using common name")
}

// TestV2OnlyDatastore_DynamicThreshold_GetByCommonName verifies that
// GetDynamicThreshold works when called with a common name.
func TestV2OnlyDatastore_DynamicThreshold_GetByCommonName(t *testing.T) {
	labels := []string{
		"Parus major_Great Tit",
	}
	ds, cleanup := setupTestDatastoreWithLabels(t, labels)
	defer cleanup()

	// Save threshold with scientific name
	threshold := &datastore.DynamicThreshold{
		SpeciesName:    "Parus major",
		ScientificName: "Parus major",
		Level:          1,
		CurrentValue:   0.6,
		BaseThreshold:  0.8,
		ValidHours:     12,
		ExpiresAt:      time.Now().Add(12 * time.Hour),
		LastTriggered:  time.Now(),
		FirstCreated:   time.Now(),
		UpdatedAt:      time.Now(),
	}
	err := ds.SaveDynamicThreshold(threshold)
	require.NoError(t, err)

	// Retrieve using lowercase common name
	retrieved, err := ds.GetDynamicThreshold("great tit")
	require.NoError(t, err)
	assert.Equal(t, "Great Tit", retrieved.SpeciesName)
	assert.Equal(t, "Parus major", retrieved.ScientificName)
}

// TestV2OnlyDatastore_DynamicThreshold_FallbackWithoutMapping verifies that
// when no label mapping exists, SpeciesName falls back to scientific name
// (existing behavior preserved).
func TestV2OnlyDatastore_DynamicThreshold_FallbackWithoutMapping(t *testing.T) {
	// No labels - empty maps
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	threshold := &datastore.DynamicThreshold{
		SpeciesName:    "Passer domesticus",
		ScientificName: "Passer domesticus",
		Level:          1,
		CurrentValue:   0.7,
		BaseThreshold:  0.6,
		ValidHours:     24,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		LastTriggered:  time.Now(),
		FirstCreated:   time.Now(),
		UpdatedAt:      time.Now(),
	}
	err := ds.SaveDynamicThreshold(threshold)
	require.NoError(t, err)

	// Without label mapping, should fall back to scientific name
	retrieved, err := ds.GetDynamicThreshold("Passer domesticus")
	require.NoError(t, err)
	assert.Equal(t, "Passer domesticus", retrieved.SpeciesName, "should fallback to scientific name")
	assert.Equal(t, "Passer domesticus", retrieved.ScientificName)
}

// TestV2OnlyDatastore_DynamicThreshold_UpdateExpiryByCommonName verifies that
// UpdateDynamicThresholdExpiry works when called with a common name.
func TestV2OnlyDatastore_DynamicThreshold_UpdateExpiryByCommonName(t *testing.T) {
	labels := []string{"Parus major_Great Tit"}
	ds, cleanup := setupTestDatastoreWithLabels(t, labels)
	defer cleanup()

	threshold := &datastore.DynamicThreshold{
		SpeciesName:    "Parus major",
		ScientificName: "Parus major",
		Level:          1,
		CurrentValue:   0.6,
		BaseThreshold:  0.8,
		ValidHours:     12,
		ExpiresAt:      time.Now().Add(12 * time.Hour),
		LastTriggered:  time.Now(),
		FirstCreated:   time.Now(),
		UpdatedAt:      time.Now(),
	}
	err := ds.SaveDynamicThreshold(threshold)
	require.NoError(t, err)

	// Update expiry using lowercase common name
	newExpiry := time.Now().Add(48 * time.Hour)
	err = ds.UpdateDynamicThresholdExpiry("great tit", newExpiry)
	require.NoError(t, err, "UpdateDynamicThresholdExpiry should work with common name")

	// Verify the expiry was updated
	retrieved, err := ds.GetDynamicThreshold("Parus major")
	require.NoError(t, err)
	assert.WithinDuration(t, newExpiry, retrieved.ExpiresAt, time.Second, "expiry should be updated")
}

// TestV2OnlyDatastore_DynamicThreshold_DeleteEventsByCommonName verifies that
// DeleteThresholdEvents works when called with a common name.
func TestV2OnlyDatastore_DynamicThreshold_DeleteEventsByCommonName(t *testing.T) {
	labels := []string{"Parus major_Great Tit"}
	ds, cleanup := setupTestDatastoreWithLabels(t, labels)
	defer cleanup()

	// Save a threshold first (needed for event's label resolution)
	threshold := &datastore.DynamicThreshold{
		SpeciesName:    "Parus major",
		ScientificName: "Parus major",
		Level:          1,
		CurrentValue:   0.6,
		BaseThreshold:  0.8,
		ValidHours:     12,
		ExpiresAt:      time.Now().Add(12 * time.Hour),
		LastTriggered:  time.Now(),
		FirstCreated:   time.Now(),
		UpdatedAt:      time.Now(),
	}
	err := ds.SaveDynamicThreshold(threshold)
	require.NoError(t, err)

	// Save an event using scientific name (as the processor does after #1907)
	event := &datastore.ThresholdEvent{
		SpeciesName:    "great tit",
		ScientificName: "Parus major",
		PreviousLevel:  0,
		NewLevel:       1,
		PreviousValue:  0.8,
		NewValue:       0.6,
		ChangeReason:   "high_confidence",
		Confidence:     0.95,
		CreatedAt:      time.Now(),
	}
	err = ds.SaveThresholdEvent(event)
	require.NoError(t, err)

	// Verify event exists
	events, err := ds.GetThresholdEvents("Parus major", 10)
	require.NoError(t, err)
	require.NotEmpty(t, events, "event should exist before delete")

	// Delete events using lowercase common name
	err = ds.DeleteThresholdEvents("great tit")
	require.NoError(t, err, "DeleteThresholdEvents should work with common name")

	// Verify events are deleted
	events, err = ds.GetThresholdEvents("Parus major", 10)
	require.NoError(t, err)
	assert.Empty(t, events, "events should be deleted when using common name")
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

// === Data gap regression tests ===
// These tests verify bugs identified in the v2only datastore audit (2026-02-21).
// See docs/plans/2026-02-21-v2only-datastore-data-gaps.md for full findings.

// saveTestNote is a helper that saves a note and returns its ID.
func saveTestNote(t *testing.T, ds *Datastore, date, timeStr, species string, confidence float64) {
	t.Helper()
	note := &datastore.Note{
		Date:           date,
		Time:           timeStr,
		ScientificName: species,
		CommonName:     species, // Use scientific name as common name for tests
		Confidence:     confidence,
		Latitude:       51.5074,
		Longitude:      -0.1278,
		ClipName:       "/clips/test.wav",
	}
	err := ds.Save(note, nil)
	require.NoError(t, err)
}

// TestV2OnlyDatastore_SpeciesDetections_LoadsRelations verifies that SpeciesDetections
// returns notes with all relations loaded (label, review, lock).
func TestV2OnlyDatastore_SpeciesDetections_LoadsRelations(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save a note
	saveTestNote(t, ds, "2024-01-15", "12:30:00", "Passer domesticus", 0.85)

	// Add review and lock
	review := &datastore.NoteReview{NoteID: 1, Verified: "correct"}
	err := ds.SaveNoteReview(review)
	require.NoError(t, err)

	err = ds.LockNote("1")
	require.NoError(t, err)

	// Query via SpeciesDetections
	notes, err := ds.SpeciesDetections("Passer domesticus", "2024-01-15", "", 0, true, 10, 0)
	require.NoError(t, err)
	require.Len(t, notes, 1)

	// These require label loading (Search doesn't load relations)
	assert.Equal(t, "Passer domesticus", notes[0].ScientificName, "ScientificName should be populated from label")
	assert.InDelta(t, 0.85, notes[0].Confidence, 0.001)

	// These require review/lock loading
	assert.Equal(t, "correct", notes[0].Verified, "Verified should be populated from review")
	assert.True(t, notes[0].Locked, "Locked should be true when lock exists")
}

// TestV2OnlyDatastore_GetHourlyDetections_LoadsRelations verifies that GetHourlyDetections
// returns notes with all relations loaded.
func TestV2OnlyDatastore_GetHourlyDetections_LoadsRelations(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save a note at 12:30
	saveTestNote(t, ds, "2024-01-15", "12:30:00", "Passer domesticus", 0.85)

	// Add review and lock
	review := &datastore.NoteReview{NoteID: 1, Verified: "correct"}
	err := ds.SaveNoteReview(review)
	require.NoError(t, err)

	err = ds.LockNote("1")
	require.NoError(t, err)

	// Query via GetHourlyDetections (hour 12, duration 1 hour)
	notes, err := ds.GetHourlyDetections("2024-01-15", "12", 1, 10, 0)
	require.NoError(t, err)
	require.Len(t, notes, 1)

	assert.Equal(t, "Passer domesticus", notes[0].ScientificName, "ScientificName should be populated")
	assert.Equal(t, "correct", notes[0].Verified, "Verified should be populated from review")
	assert.True(t, notes[0].Locked, "Locked should be true when lock exists")
}

// TestV2OnlyDatastore_SavePersistsAllFields verifies that Save() persists
// BeginTime, EndTime, and ProcessingTime fields.
func TestV2OnlyDatastore_SavePersistsAllFields(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	beginTime := time.Date(2024, 1, 15, 12, 30, 0, 0, time.UTC)
	endTime := time.Date(2024, 1, 15, 12, 30, 3, 0, time.UTC)

	note := &datastore.Note{
		Date:           "2024-01-15",
		Time:           "12:30:00",
		ScientificName: "Passer domesticus",
		Confidence:     0.85,
		BeginTime:      beginTime,
		EndTime:        endTime,
		ProcessingTime: 150 * time.Millisecond,
		ClipName:       "/clips/test.wav",
	}
	err := ds.Save(note, nil)
	require.NoError(t, err)

	// Retrieve via Get (uses GetWithRelations)
	got, err := ds.Get("1")
	require.NoError(t, err)

	assert.False(t, got.BeginTime.IsZero(), "BeginTime should not be zero")
	assert.False(t, got.EndTime.IsZero(), "EndTime should not be zero")
	assert.Equal(t, beginTime.Unix(), got.BeginTime.Unix(), "BeginTime should match saved value")
	assert.Equal(t, endTime.Unix(), got.EndTime.Unix(), "EndTime should match saved value")
	assert.Equal(t, 150*time.Millisecond, got.ProcessingTime, "ProcessingTime should match saved value")
}

// TestV2OnlyDatastore_DetectionToNote_MapsSourceAndComments verifies that
// detectionToNote populates Source and Comments fields used by the API.
func TestV2OnlyDatastore_DetectionToNote_MapsSourceAndComments(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Save a note with source info
	note := &datastore.Note{
		Date:           "2024-01-15",
		Time:           "12:30:00",
		ScientificName: "Passer domesticus",
		Confidence:     0.85,
		Source: datastore.AudioSource{
			SafeString: "rtsp_test_source",
		},
	}
	err := ds.Save(note, nil)
	require.NoError(t, err)

	// Add a comment
	comment := &datastore.NoteComment{
		NoteID:    1,
		Entry:     "Confirmed by ear",
		CreatedAt: time.Now(),
	}
	err = ds.SaveNoteComment(comment)
	require.NoError(t, err)

	// Retrieve via Get
	got, err := ds.Get("1")
	require.NoError(t, err)

	// Source should be loaded
	assert.NotEmpty(t, got.Source.SafeString, "Source SafeString should be populated")
	assert.Equal(t, "rtsp_test_source", got.Source.SafeString, "Source SafeString should match saved value")

	// Comments should be loaded
	assert.Len(t, got.Comments, 1, "Comments should be loaded with the note")
	if len(got.Comments) > 0 {
		assert.Equal(t, "Confirmed by ear", got.Comments[0].Entry)
	}
}

// TestV2OnlyDatastore_ReviewAndLockVisibleInGetAllNotes is a regression guard
// for the fix in PR #2016. Verifies review/lock are visible in GetAllNotes.
func TestV2OnlyDatastore_ReviewAndLockVisibleInGetAllNotes(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	saveTestNote(t, ds, "2024-01-15", "12:30:00", "Passer domesticus", 0.85)

	// Add review and lock
	review := &datastore.NoteReview{NoteID: 1, Verified: "correct"}
	err := ds.SaveNoteReview(review)
	require.NoError(t, err)

	err = ds.LockNote("1")
	require.NoError(t, err)

	// Retrieve via GetAllNotes
	notes, err := ds.GetAllNotes()
	require.NoError(t, err)
	require.Len(t, notes, 1)

	assert.Equal(t, "correct", notes[0].Verified, "Verified should be populated in GetAllNotes")
	assert.True(t, notes[0].Locked, "Locked should be true in GetAllNotes")
}

// TestV2OnlyDatastore_ConcatenatedLabelExtraction verifies that all read paths
// properly extract scientific names from legacy concatenated labels stored as
// "ScientificName_CommonName" in the labels table.
func TestV2OnlyDatastore_ConcatenatedLabelExtraction(t *testing.T) {
	t.Parallel()

	// Provide label mapping so commonNameMap resolves "Strix aluco" → "lehtopöllö"
	labels := []string{
		"Strix aluco_lehtopöllö",
	}
	ds, cleanup := setupTestDatastoreWithLabels(t, labels)
	defer cleanup()

	ctx := t.Context()

	// Simulate legacy data: directly create a label with concatenated scientific name
	// (this is what happened before the extractScientificName fix was applied to writes)
	concatenatedName := "Strix aluco_lehtopöllö"
	legacyLabel, err := ds.label.GetOrCreate(ctx, concatenatedName, ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
	require.NoError(t, err)

	// Insert a detection referencing this concatenated label
	now := time.Now().In(ds.timezone)
	clipName := "/clips/test-owl.wav"
	det := &entities.Detection{
		LabelID:    legacyLabel.ID,
		ModelID:    ds.defaultModelID,
		DetectedAt: now.Unix(),
		Confidence: 0.92,
		ClipName:   &clipName,
	}
	err = ds.manager.DB().Create(det).Error
	require.NoError(t, err)

	t.Run("detectionToNote extracts scientific name from concatenated label", func(t *testing.T) {
		notes, err := ds.GetAllNotes()
		require.NoError(t, err)
		require.Len(t, notes, 1)

		assert.Equal(t, "Strix aluco", notes[0].ScientificName,
			"ScientificName should be extracted from concatenated label")
		assert.Equal(t, "lehtopöllö", notes[0].CommonName,
			"CommonName should be resolved from commonNameMap")
	})

	t.Run("GetAllDetectedSpecies extracts scientific name from concatenated label", func(t *testing.T) {
		species, err := ds.GetAllDetectedSpecies()
		require.NoError(t, err)
		require.NotEmpty(t, species)

		// Find our species in the list
		found := false
		for _, s := range species {
			if s.ScientificName == "Strix aluco" {
				found = true
				break
			}
		}
		assert.True(t, found, "GetAllDetectedSpecies should return extracted scientific name 'Strix aluco', not concatenated form")

		// Verify no concatenated names leaked through
		for _, s := range species {
			assert.NotContains(t, s.ScientificName, "_",
				"ScientificName should not contain underscore separator")
		}
	})

	t.Run("GetTopBirdsData extracts scientific name from concatenated label", func(t *testing.T) {
		dateStr := now.Format(time.DateOnly)
		topBirds, err := ds.GetTopBirdsData(dateStr, 0.0, 10)
		require.NoError(t, err)
		require.Len(t, topBirds, 1)

		assert.Equal(t, "Strix aluco", topBirds[0].ScientificName,
			"ScientificName should be extracted from concatenated label")
		assert.Equal(t, "lehtopöllö", topBirds[0].CommonName,
			"CommonName should be resolved from commonNameMap")
	})

	t.Run("Save with concatenated ScientificName extracts properly", func(t *testing.T) {
		// Simulate saving a detection where ScientificName is accidentally concatenated
		note := &datastore.Note{
			Date:           now.Format(time.DateOnly),
			Time:           now.Format(time.TimeOnly),
			ScientificName: "Picus viridis_vihertikka",
			CommonName:     "vihertikka",
			Confidence:     0.88,
		}
		err := ds.Save(note, nil)
		require.NoError(t, err)

		// Verify no concatenated label was stored: scan all species labels for viridis
		var allLabels []entities.Label
		err = ds.manager.DB().
			Where("label_type_id = ? AND scientific_name LIKE ?", ds.speciesLabelTypeID, "%viridis%").
			Find(&allLabels).Error
		require.NoError(t, err)
		require.Len(t, allLabels, 1, "exactly one label should exist for Picus viridis")
		assert.Equal(t, "Picus viridis", allLabels[0].ScientificName,
			"Save should store only the extracted scientific name, not the concatenated form")
	})
}
