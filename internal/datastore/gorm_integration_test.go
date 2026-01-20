// gorm_integration_test.go: Integration tests for GORM persistence behavior.
//
// These tests verify that the Omit("Results") fix prevents GORM from auto-saving
// Results associations, which was causing UNIQUE constraint failures.
//
// These tests use real SQLite databases (not mocks) to exercise actual GORM behavior.
package datastore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// countResultsInDatabase returns the number of Results rows for a given NoteID.
// This directly queries the database to verify persistence behavior.
func countResultsInDatabase(t *testing.T, ds Interface, noteID uint) int {
	t.Helper()

	// Type assert to SQLiteStore to access the embedded DataStore.DB
	sqliteStore, ok := ds.(*SQLiteStore)
	require.True(t, ok, "Interface must be *SQLiteStore for this test")

	var count int64
	err := sqliteStore.DB.Model(&Results{}).Where("note_id = ?", noteID).Count(&count).Error
	require.NoError(t, err, "Failed to count Results")

	return int(count)
}

// getAllResultsForNote retrieves all Results for a given NoteID.
func getAllResultsForNote(t *testing.T, ds Interface, noteID uint) []Results {
	t.Helper()

	// Type assert to SQLiteStore to access the embedded DataStore.DB
	sqliteStore, ok := ds.(*SQLiteStore)
	require.True(t, ok, "Interface must be *SQLiteStore for this test")

	var results []Results
	err := sqliteStore.DB.Where("note_id = ?", noteID).Find(&results).Error
	require.NoError(t, err, "Failed to get Results")

	return results
}

// TestSave_OmitResultsPreventsAutoSave verifies that Omit("Results") prevents
// GORM from auto-saving the Results association when note.Results is populated.
//
// This is a regression test for the UNIQUE constraint bug fixed in PR #1846.
// Without Omit("Results"), GORM would:
// 1. Auto-save Results via association when Create(note) is called
// 2. Then saveResultsInTransaction would try to create them again
// 3. Result: "UNIQUE constraint failed: results.id"
func TestSave_OmitResultsPreventsAutoSave(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	// Create Note with Results assigned (simulating the bug condition)
	note := Note{
		SourceNode:     "test-node",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Turdus migratorius",
		CommonName:     "American Robin",
		Confidence:     0.95,
		ClipName:       "clip_001.wav",
		// Assign Results to note - this was the bug trigger
		Results: []Results{
			{Species: "Turdus migratorius_American Robin", Confidence: 0.95},
			{Species: "Turdus merula_Common Blackbird", Confidence: 0.75},
			{Species: "Turdus philomelos_Song Thrush", Confidence: 0.60},
		},
	}

	// Create separate Results slice for Save() call
	resultsToSave := []Results{
		{Species: "Turdus migratorius_American Robin", Confidence: 0.95},
		{Species: "Turdus merula_Common Blackbird", Confidence: 0.75},
		{Species: "Turdus philomelos_Song Thrush", Confidence: 0.60},
	}

	// Save should succeed without UNIQUE constraint error
	// Before the fix, this would fail with: "UNIQUE constraint failed: results.id"
	err := ds.Save(&note, resultsToSave)
	require.NoError(t, err, "Save should succeed - Omit('Results') prevents auto-save")

	// Verify Note was saved
	require.NotZero(t, note.ID, "Note ID should be assigned after save")

	// Verify exactly 3 Results were saved (not 6 from double-insertion)
	count := countResultsInDatabase(t, ds, note.ID)
	assert.Equal(t, 3, count, "Expected exactly 3 Results, got %d (double-insertion bug?)", count)

	// Verify Results have correct NoteID
	results := getAllResultsForNote(t, ds, note.ID)
	for i, r := range results {
		assert.Equal(t, note.ID, r.NoteID, "Result[%d] has wrong NoteID", i)
		assert.NotZero(t, r.ID, "Result[%d] should have assigned ID", i)
	}
}

// TestSave_ResultsActuallyPersisted verifies Results rows exist in database
// after Save() completes. This complements TestDatabaseContract_ResultsRelationship
// by directly querying the Results table.
func TestSave_ResultsActuallyPersisted(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	note := Note{
		SourceNode:     "test-node",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.85,
		ClipName:       "clip_001.wav",
	}

	results := []Results{
		{Species: "Parus major_Great Tit_gretit1", Confidence: 0.85},
		{Species: "Cyanistes caeruleus_Eurasian Blue Tit_eurblu", Confidence: 0.65},
	}

	err := ds.Save(&note, results)
	require.NoError(t, err)

	// Verify Results are in database
	count := countResultsInDatabase(t, ds, note.ID)
	assert.Equal(t, 2, count, "Expected 2 Results in database")

	// Verify Results content
	savedResults := getAllResultsForNote(t, ds, note.ID)
	require.Len(t, savedResults, 2)

	// Results may not be in order, so check both exist
	species := []string{savedResults[0].Species, savedResults[1].Species}
	assert.Contains(t, species, "Parus major_Great Tit_gretit1")
	assert.Contains(t, species, "Cyanistes caeruleus_Eurasian Blue Tit_eurblu")
}

// TestSave_ConcurrentDetections verifies multiple goroutines can save
// detections simultaneously without errors. Tests transaction isolation
// and the retry logic for database lock handling.
func TestSave_ConcurrentDetections(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)
	noteIDs := make(chan uint, numGoroutines)

	for i := range numGoroutines {
		wg.Go(func() {
			note := Note{
				SourceNode:     fmt.Sprintf("node-%d", i),
				Date:           "2024-01-15",
				Time:           fmt.Sprintf("14:30:%02d", i),
				ScientificName: fmt.Sprintf("Species %d", i),
				CommonName:     fmt.Sprintf("Bird %d", i),
				Confidence:     0.85,
				ClipName:       fmt.Sprintf("clip_%03d.wav", i),
			}

			results := []Results{
				{Species: fmt.Sprintf("Species %d_Bird %d", i, i), Confidence: 0.85},
				{Species: fmt.Sprintf("Species %d alt_Bird %d alt", i, i), Confidence: 0.65},
			}

			if err := ds.Save(&note, results); err != nil {
				errors <- fmt.Errorf("goroutine %d: %w", i, err)
				return
			}
			noteIDs <- note.ID
		})
	}

	wg.Wait()
	close(errors)
	close(noteIDs)

	// Check for errors
	allErrors := make([]error, 0, numGoroutines)
	for err := range errors {
		allErrors = append(allErrors, err)
	}
	require.Empty(t, allErrors, "Concurrent saves should not fail: %v", allErrors)

	// Verify all notes were saved with unique IDs
	ids := make(map[uint]bool)
	for id := range noteIDs {
		require.NotZero(t, id, "Note ID should not be zero")
		require.False(t, ids[id], "Duplicate Note ID: %d", id)
		ids[id] = true
	}
	assert.Len(t, ids, numGoroutines, "All %d notes should have unique IDs", numGoroutines)

	// Verify each note has exactly 2 Results
	for id := range ids {
		count := countResultsInDatabase(t, ds, id)
		assert.Equal(t, 2, count, "Note %d should have exactly 2 Results", id)
	}
}

// TestDetectionRepository_RoundTrip verifies that detection.Result data survives
// a save/load roundtrip through DetectionRepository.
// This test uses DetectionRepository (the domain interface) rather than the
// low-level datastore.Interface to test the full conversion pipeline.
func TestDetectionRepository_RoundTrip(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	store := createDatabase(t, settings)

	// Create DetectionRepository wrapping the store
	repo := NewDetectionRepository(store, time.UTC)

	// Create a detection.Result with all fields populated
	now := time.Date(2024, 6, 15, 14, 30, 45, 0, time.UTC)
	original := detection.Result{
		Timestamp:  now,
		SourceNode: "test-node",
		AudioSource: detection.AudioSource{
			ID:          "rtsp_camera_1",
			SafeString:  "rtsp://***@192.168.1.100/stream",
			DisplayName: "Backyard Camera",
		},
		BeginTime: now,
		EndTime:   now.Add(3 * time.Second),
		Species: detection.Species{
			Code:           "gretit1",
			ScientificName: "Parus major",
			CommonName:     "Great Tit",
		},
		Confidence:     0.85,
		Latitude:       60.1699,
		Longitude:      24.9384,
		Threshold:      0.7,
		Sensitivity:    1.0,
		ClipName:       "clip_001.wav",
		ProcessingTime: 150 * time.Millisecond,
		Occurrence:     0.75, // Runtime-only field
		Model: detection.ModelInfo{
			Name:    "CustomModel",
			Version: "1.0",
			Custom:  true,
		},
	}

	ctx := context.Background()

	// Save the detection
	err := repo.Save(ctx, &original, nil)
	require.NoError(t, err, "Failed to save detection")
	require.NotZero(t, original.ID, "Detection ID should be assigned after save")

	// Load it back
	loaded, err := repo.Get(ctx, fmt.Sprintf("%d", original.ID))
	require.NoError(t, err, "Failed to load detection")

	// ==========================================================================
	// ROUNDTRIP ASSERTIONS - Fields that SHOULD survive
	// ==========================================================================

	assert.Equal(t, original.ID, loaded.ID, "ID mismatch")
	assert.Equal(t, original.SourceNode, loaded.SourceNode, "SourceNode mismatch")
	assert.Equal(t, original.Species.Code, loaded.Species.Code, "Species.Code mismatch")
	assert.Equal(t, original.Species.ScientificName, loaded.Species.ScientificName, "ScientificName mismatch")
	assert.Equal(t, original.Species.CommonName, loaded.Species.CommonName, "CommonName mismatch")
	assert.InDelta(t, original.Confidence, loaded.Confidence, 0.0001, "Confidence mismatch")
	assert.InDelta(t, original.Latitude, loaded.Latitude, 0.0001, "Latitude mismatch")
	assert.InDelta(t, original.Longitude, loaded.Longitude, 0.0001, "Longitude mismatch")
	assert.InDelta(t, original.Threshold, loaded.Threshold, 0.0001, "Threshold mismatch")
	assert.InDelta(t, original.Sensitivity, loaded.Sensitivity, 0.0001, "Sensitivity mismatch")
	assert.Equal(t, original.ClipName, loaded.ClipName, "ClipName mismatch")
	assert.Equal(t, original.ProcessingTime, loaded.ProcessingTime, "ProcessingTime mismatch")

	// Timestamp is reconstructed from Date+Time strings
	assert.True(t, original.Timestamp.Equal(loaded.Timestamp),
		"Timestamp mismatch: got %v, want %v", loaded.Timestamp, original.Timestamp)

	// BeginTime/EndTime should survive
	assert.True(t, original.BeginTime.Equal(loaded.BeginTime),
		"BeginTime mismatch: got %v, want %v", loaded.BeginTime, original.BeginTime)
	assert.True(t, original.EndTime.Equal(loaded.EndTime),
		"EndTime mismatch: got %v, want %v", loaded.EndTime, original.EndTime)

	// ==========================================================================
	// KNOWN LIMITATIONS - Fields that are NOT persisted (documented behavior)
	// ==========================================================================

	// AudioSource is NOT persisted (gorm:"-" on Note.Source)
	// This is a known limitation - AudioSource must be populated from config at runtime
	assert.Empty(t, loaded.AudioSource.ID, "KNOWN LIMITATION: AudioSource.ID not persisted")
	assert.Empty(t, loaded.AudioSource.SafeString, "KNOWN LIMITATION: AudioSource.SafeString not persisted")
	assert.Empty(t, loaded.AudioSource.DisplayName, "KNOWN LIMITATION: AudioSource.DisplayName not persisted")

	// Occurrence is runtime-only (gorm:"-" on Note.Occurrence)
	assert.InDelta(t, 0.0, loaded.Occurrence, 0.0001, "KNOWN LIMITATION: Occurrence not persisted")

	// Model info uses default on load (noteToResult creates DefaultModelInfo)
	// This is acceptable - Model info is typically static per deployment
	assert.NotEqual(t, original.Model.Name, loaded.Model.Name,
		"KNOWN LIMITATION: Model.Name not persisted (uses default)")
}

// TestSave_SourceFieldNotPersisted documents that the Source (AudioSource) field
// has gorm:"-" tag and is NOT persisted to the database.
// This test documents current behavior - Source data is lost on roundtrip.
//
// NOTE: This is a known limitation. If you need AudioSource data after loading
// from the database, you must populate it from another source.
func TestSave_SourceFieldNotPersisted(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	// Create Note with Source data populated
	note := Note{
		SourceNode:     "test-node",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.85,
		ClipName:       "clip_001.wav",
		// Source has gorm:"-" tag - it should NOT be persisted
		Source: AudioSource{
			ID:          "rtsp_camera_1",
			SafeString:  "rtsp://***@192.168.1.100/stream",
			DisplayName: "Backyard Camera",
		},
	}

	err := ds.Save(&note, nil)
	require.NoError(t, err)
	require.NotZero(t, note.ID)

	// Load the note back
	loaded, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)

	// DOCUMENT: Source field is NOT persisted (gorm:"-" tag)
	// This test verifies the CURRENT behavior - Source is lost on roundtrip
	assert.Empty(t, loaded.Source.ID, "KNOWN LIMITATION: Source.ID is not persisted")
	assert.Empty(t, loaded.Source.SafeString, "KNOWN LIMITATION: Source.SafeString is not persisted")
	assert.Empty(t, loaded.Source.DisplayName, "KNOWN LIMITATION: Source.DisplayName is not persisted")
}

// TestSave_EmptyResults verifies Save() handles nil and empty Results correctly.
func TestSave_EmptyResults(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	t.Run("nil results", func(t *testing.T) {
		note := Note{
			SourceNode:     "test-node",
			Date:           "2024-01-15",
			Time:           "14:30:45",
			ScientificName: "Parus major",
			CommonName:     "Great Tit",
			Confidence:     0.85,
		}

		err := ds.Save(&note, nil)
		require.NoError(t, err)
		require.NotZero(t, note.ID)

		count := countResultsInDatabase(t, ds, note.ID)
		assert.Equal(t, 0, count, "No Results should be saved for nil input")
	})

	t.Run("empty slice results", func(t *testing.T) {
		note := Note{
			SourceNode:     "test-node",
			Date:           "2024-01-15",
			Time:           "14:31:45",
			ScientificName: "Cyanistes caeruleus",
			CommonName:     "Eurasian Blue Tit",
			Confidence:     0.80,
		}

		err := ds.Save(&note, []Results{})
		require.NoError(t, err)
		require.NotZero(t, note.ID)

		count := countResultsInDatabase(t, ds, note.ID)
		assert.Equal(t, 0, count, "No Results should be saved for empty slice")
	})
}
