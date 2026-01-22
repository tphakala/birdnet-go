// roundtrip_test.go: Tests for database round-trip behavior.
//
// IMPORTANT: These tests verify that Note data survives database save and load
// without modification. They serve as regression tests for the model separation
// refactor to ensure data integrity is preserved.
//
// These tests use real SQLite databases (not mocks) to ensure actual persistence
// behavior is tested.
package datastore

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// createTestSettings creates minimal settings for database tests.
// Test coordinates are Helsinki, Finland (60.1699°N, 24.9384°E).
func createTestSettings(t *testing.T) *conf.Settings {
	t.Helper()
	settings := &conf.Settings{}
	settings.BirdNET.Latitude = 60.1699
	settings.BirdNET.Longitude = 24.9384
	return settings
}

// TestDatabaseContract_NoteRoundTrip verifies all Note fields survive
// database save and load without modification.
func TestDatabaseContract_NoteRoundTrip(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	// Create Note with all fields that are persisted
	beginTime := time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC)
	endTime := beginTime.Add(3 * time.Second)

	original := Note{
		SourceNode:     "test-node",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		BeginTime:      beginTime,
		EndTime:        endTime,
		SpeciesCode:    "gretit1",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.85,
		Latitude:       60.1699,
		Longitude:      24.9384,
		Threshold:      0.7,
		Sensitivity:    1.0,
		ClipName:       "clip_001.wav",
		ProcessingTime: 150 * time.Millisecond,
	}

	// Save
	err := ds.Save(&original, nil)
	require.NoError(t, err, "Failed to save Note")
	require.NotZero(t, original.ID, "Note ID should be assigned after save")

	// Load
	loaded, err := ds.Get(fmt.Sprintf("%d", original.ID))
	require.NoError(t, err, "Failed to load Note")

	// ==========================================================================
	// CONTRACT ASSERTIONS - All fields must round-trip correctly
	// ==========================================================================

	assert.Equal(t, original.ID, loaded.ID, "ID mismatch")
	assert.Equal(t, original.SourceNode, loaded.SourceNode, "SourceNode mismatch")
	assert.Equal(t, original.Date, loaded.Date, "Date mismatch")
	assert.Equal(t, original.Time, loaded.Time, "Time mismatch")
	assert.Equal(t, original.SpeciesCode, loaded.SpeciesCode, "SpeciesCode mismatch")
	assert.Equal(t, original.ScientificName, loaded.ScientificName, "ScientificName mismatch")
	assert.Equal(t, original.CommonName, loaded.CommonName, "CommonName mismatch")
	assert.InDelta(t, original.Confidence, loaded.Confidence, 0.0001, "Confidence mismatch")
	assert.InDelta(t, original.Latitude, loaded.Latitude, 0.0001, "Latitude mismatch")
	assert.InDelta(t, original.Longitude, loaded.Longitude, 0.0001, "Longitude mismatch")
	assert.InDelta(t, original.Threshold, loaded.Threshold, 0.0001, "Threshold mismatch")
	assert.InDelta(t, original.Sensitivity, loaded.Sensitivity, 0.0001, "Sensitivity mismatch")
	assert.Equal(t, original.ClipName, loaded.ClipName, "ClipName mismatch")
	assert.Equal(t, original.ProcessingTime, loaded.ProcessingTime, "ProcessingTime mismatch")

	// Time fields (BeginTime, EndTime) need special handling due to timezone normalization
	assert.True(t, original.BeginTime.Equal(loaded.BeginTime),
		"BeginTime mismatch: got %v, want %v", loaded.BeginTime, original.BeginTime)
	assert.True(t, original.EndTime.Equal(loaded.EndTime),
		"EndTime mismatch: got %v, want %v", loaded.EndTime, original.EndTime)
}

// TestDatabaseContract_ResultsRelationship verifies Results are saved correctly.
// Note: Results are NOT preloaded by the Get() function in the current implementation.
// This test verifies that Results can be saved alongside a Note.
func TestDatabaseContract_ResultsRelationship(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	// Create Note
	note := Note{
		SourceNode:     "test-node",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.85,
		ClipName:       "clip_001.wav",
	}

	// Create additional predictions (Results)
	results := []Results{
		{Species: "Parus major", Confidence: 0.85},
		{Species: "Cyanistes caeruleus", Confidence: 0.65},
		{Species: "Poecile palustris", Confidence: 0.45},
	}

	// Save note with results - this should succeed
	err := ds.Save(&note, results)
	require.NoError(t, err, "Failed to save Note with Results")

	// Verify Note was saved
	require.NotZero(t, note.ID, "Note ID should be assigned after save")

	// Note: Results are saved in the database but Get() does not preload them.
	// This documents current behavior - Results need separate loading if needed.
	loaded, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err, "Failed to load Note")

	// Verify the Note itself was saved correctly
	assert.Equal(t, note.ScientificName, loaded.ScientificName)
	assert.Equal(t, note.CommonName, loaded.CommonName)

	// Document that Results are not preloaded by Get() - this is current behavior
	// If this test fails after refactor, it means Get() behavior changed
	assert.Nil(t, loaded.Results, "CONTRACT: Get() does not preload Results (current behavior)")
}

// TestDatabaseContract_ReviewRelationship verifies Review relationship.
func TestDatabaseContract_ReviewRelationship(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	// Create and save Note
	note := Note{
		SourceNode:     "test-node",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.85,
		ClipName:       "clip_001.wav",
	}

	err := ds.Save(&note, nil)
	require.NoError(t, err)

	// Save a review for this note
	review := &NoteReview{
		NoteID:   note.ID,
		Verified: "correct",
	}
	err = ds.SaveNoteReview(review)
	require.NoError(t, err, "Failed to save NoteReview")

	// Load note (should include Review via preload)
	loaded, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)

	// Verify Review relationship
	require.NotNil(t, loaded.Review, "Review should be preloaded")
	assert.Equal(t, "correct", loaded.Review.Verified, "Review.Verified mismatch")
	assert.Equal(t, note.ID, loaded.Review.NoteID, "Review.NoteID should match Note ID")
}

// TestDatabaseContract_CommentsRelationship verifies Comments relationship.
func TestDatabaseContract_CommentsRelationship(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	// Create and save Note
	note := Note{
		SourceNode:     "test-node",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.85,
		ClipName:       "clip_001.wav",
	}

	err := ds.Save(&note, nil)
	require.NoError(t, err)

	// Save comments for this note
	testComments := []string{
		"Beautiful song pattern",
		"Confirmed by visual observation",
	}

	for _, comment := range testComments {
		entry := NoteComment{
			NoteID: note.ID,
			Entry:  comment,
		}
		err = ds.SaveNoteComment(&entry)
		require.NoError(t, err, "Failed to save NoteComment")
	}

	// Load note (should include Comments via preload)
	loaded, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)

	// Verify Comments relationship
	// Note: Comments are ordered by created_at DESC (newest first)
	require.Len(t, loaded.Comments, 2, "Comments count mismatch")

	// Collect comment entries to verify both exist (order depends on creation time)
	commentEntries := make(map[string]bool)
	for _, c := range loaded.Comments {
		commentEntries[c.Entry] = true
	}

	for _, expectedComment := range testComments {
		assert.True(t, commentEntries[expectedComment],
			"Expected comment not found: %s", expectedComment)
	}
}

// TestDatabaseContract_LockRelationship verifies Lock relationship.
func TestDatabaseContract_LockRelationship(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	// Create and save Note
	note := Note{
		SourceNode:     "test-node",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.85,
		ClipName:       "clip_001.wav",
	}

	err := ds.Save(&note, nil)
	require.NoError(t, err)

	// Lock the note
	noteIDStr := fmt.Sprintf("%d", note.ID)
	err = ds.LockNote(noteIDStr)
	require.NoError(t, err, "Failed to lock note")

	// Load note (should include Lock via preload)
	loaded, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)

	// Verify Lock relationship
	require.NotNil(t, loaded.Lock, "Lock should be preloaded")
	assert.Equal(t, note.ID, loaded.Lock.NoteID, "Lock.NoteID should match Note ID")
	assert.True(t, loaded.Locked, "Locked virtual field should be true")

	// Unlock and verify
	err = ds.UnlockNote(noteIDStr)
	require.NoError(t, err, "Failed to unlock note")

	loadedUnlocked, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)
	assert.Nil(t, loadedUnlocked.Lock, "Lock should be nil after unlock")
	assert.False(t, loadedUnlocked.Locked, "Locked virtual field should be false")
}

// TestDatabaseContract_EmptyResults verifies behavior when no Results are saved.
func TestDatabaseContract_EmptyResults(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	// Create and save Note without Results
	note := Note{
		SourceNode:     "test-node",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.85,
		ClipName:       "clip_001.wav",
	}

	err := ds.Save(&note, nil)
	require.NoError(t, err)

	// Load note
	loaded, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)

	// Note: Get() does not preload Results, so it will be nil
	// This documents current behavior
	assert.Nil(t, loaded.Results, "CONTRACT: Results is nil when Get() doesn't preload them")
}

// TestDatabaseContract_SpecialCharacters verifies that special characters in
// string fields are handled correctly.
func TestDatabaseContract_SpecialCharacters(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	// Test with special characters that might cause issues
	note := Note{
		SourceNode:     "node with spaces & special 'chars'",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Motacilla alba alba", // Subspecies notation
		CommonName:     "White Wagtail (European)",
		Confidence:     0.85,
		ClipName:       "clip with spaces & symbols!.wav",
	}

	err := ds.Save(&note, nil)
	require.NoError(t, err)

	loaded, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)

	assert.Equal(t, note.SourceNode, loaded.SourceNode)
	assert.Equal(t, note.ScientificName, loaded.ScientificName)
	assert.Equal(t, note.CommonName, loaded.CommonName)
	assert.Equal(t, note.ClipName, loaded.ClipName)
}

// TestDatabaseContract_UnicodeCharacters verifies that unicode characters are
// handled correctly (important for species names in different languages).
func TestDatabaseContract_UnicodeCharacters(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	note := Note{
		SourceNode:     "北京鸟巢", // Chinese: Beijing Bird Nest
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Parus major",
		CommonName:     "Talitiainen", // Finnish name
		Confidence:     0.85,
		ClipName:       "havainto_päivä.wav", // Finnish with special chars
	}

	err := ds.Save(&note, nil)
	require.NoError(t, err)

	loaded, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)

	assert.Equal(t, note.SourceNode, loaded.SourceNode)
	assert.Equal(t, note.CommonName, loaded.CommonName)
	assert.Equal(t, note.ClipName, loaded.ClipName)
}

// TestDatabaseContract_ZeroValues verifies handling of zero/empty values.
func TestDatabaseContract_ZeroValues(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	// Create Note with minimal required fields, zeros for everything else
	note := Note{
		Date:           "2024-01-15",
		Time:           "00:00:00",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.0, // Zero confidence
		Latitude:       0.0, // Equator
		Longitude:      0.0, // Prime meridian
		Threshold:      0.0,
		Sensitivity:    0.0,
	}

	err := ds.Save(&note, nil)
	require.NoError(t, err)

	loaded, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)

	// Zero values should be preserved, not converted to defaults
	assert.InDelta(t, 0.0, loaded.Confidence, 0.0001, "Zero Confidence should be preserved")
	assert.InDelta(t, 0.0, loaded.Latitude, 0.0001, "Zero Latitude should be preserved")
	assert.InDelta(t, 0.0, loaded.Longitude, 0.0001, "Zero Longitude should be preserved")
	assert.Equal(t, "00:00:00", loaded.Time, "Midnight Time should be preserved")
}

// TestDatabaseContract_MaxValues verifies handling of extreme values.
func TestDatabaseContract_MaxValues(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	note := Note{
		Date:           "2024-01-15",
		Time:           "23:59:59",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     1.0,   // Max confidence
		Latitude:       90.0,  // North pole
		Longitude:      180.0, // International date line
		Threshold:      1.0,
		Sensitivity:    3.0,   // Max sensitivity
		ProcessingTime: 10 * time.Second,
	}

	err := ds.Save(&note, nil)
	require.NoError(t, err)

	loaded, err := ds.Get(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)

	assert.InDelta(t, 1.0, loaded.Confidence, 0.0001)
	assert.InDelta(t, 90.0, loaded.Latitude, 0.0001)
	assert.InDelta(t, 180.0, loaded.Longitude, 0.0001)
	assert.Equal(t, "23:59:59", loaded.Time)
	assert.Equal(t, 10*time.Second, loaded.ProcessingTime)
}
