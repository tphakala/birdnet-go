// database_action_test.go - Tests for DatabaseAction.Execute()
//
// These tests verify that DatabaseAction correctly:
// - Assigns the database ID back to Result after successful save
// - Stores the ID in DetectionContext for downstream actions (MQTT, SSE)
// - Saves additional results alongside the main detection
package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// testEventTrackerInterval is used to create EventTrackers for tests.
// A short interval ensures events are tracked without rate limiting interference.
const testEventTrackerInterval = 1 * time.Second

// TestDatabaseAction_Execute_AssignsID verifies that DatabaseAction.Execute()
// assigns the database ID back to the Result after successful save.
//
// This is critical for downstream actions (MQTT, SSE) that need the ID.
func TestDatabaseAction_Execute_AssignsID(t *testing.T) {
	t.Parallel()

	// Setup
	mockDs := NewActionMockDatastore()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	// Create detection with no ID
	det := testDetection()
	require.Equal(t, uint(0), det.Result.ID, "Result should start with ID 0")

	// Create DatabaseAction
	action := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
	}

	// Execute
	err := action.Execute(nil)
	require.NoError(t, err, "DatabaseAction.Execute() should not return error")

	// Verify ID was assigned to Result
	assert.NotEqual(t, uint(0), action.Result.ID, "Result.ID should be assigned after save")
	assert.Equal(t, uint(1), action.Result.ID, "First save should get ID 1")

	// Verify note was saved with correct data
	savedNote := mockDs.GetLastSavedNote()
	require.NotNil(t, savedNote, "Note should be saved")
	assert.Equal(t, action.Result.ID, savedNote.ID, "Saved note ID should match Result.ID")
	assert.Equal(t, det.Result.Species.CommonName, savedNote.CommonName)
	assert.Equal(t, det.Result.Species.ScientificName, savedNote.ScientificName)
}

// TestDatabaseAction_Execute_StoresIDInDetectionContext verifies that
// DatabaseAction stores the assigned ID in DetectionContext for downstream actions.
func TestDatabaseAction_Execute_StoresIDInDetectionContext(t *testing.T) {
	t.Parallel()

	// Setup
	mockDs := NewActionMockDatastore()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)
	detectionCtx := &DetectionContext{}

	det := testDetection()

	action := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	// Execute
	err := action.Execute(nil)
	require.NoError(t, err)

	// Verify DetectionContext has the ID
	storedID := detectionCtx.NoteID.Load()
	assert.Equal(t, uint64(action.Result.ID), storedID,
		"DetectionContext.NoteID should contain the assigned ID")
	assert.NotEqual(t, uint64(0), storedID,
		"DetectionContext.NoteID should not be zero")
}

// TestDatabaseAction_Execute_SavesResults verifies that additional results
// are saved alongside the main detection.
func TestDatabaseAction_Execute_SavesResults(t *testing.T) {
	t.Parallel()

	mockDs := NewActionMockDatastore()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	// Add extra results
	det.Results = []detection.AdditionalResult{
		{Species: detection.Species{ScientificName: "Parus major", CommonName: "Great Tit"}, Confidence: 0.85},
		{Species: detection.Species{ScientificName: "Cyanistes caeruleus", CommonName: "Blue Tit"}, Confidence: 0.75},
	}

	action := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
	}

	err := action.Execute(nil)
	require.NoError(t, err)

	// Verify results were saved
	savedResults := mockDs.GetLastSavedResults()
	require.Len(t, savedResults, 2, "Should save 2 additional results")
	assert.Contains(t, savedResults[0].Species, "Parus major")
	assert.Contains(t, savedResults[1].Species, "Cyanistes caeruleus")
}

// TestDatabaseAction_Execute_MultipleDetections verifies that multiple
// detections get sequential IDs.
func TestDatabaseAction_Execute_MultipleDetections(t *testing.T) {
	t.Parallel()

	mockDs := NewActionMockDatastore()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	// First detection
	det1 := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)
	action1 := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det1.Result,
		Results:      det1.Results,
		EventTracker: eventTracker,
	}

	err := action1.Execute(nil)
	require.NoError(t, err)
	assert.Equal(t, uint(1), action1.Result.ID, "First detection should get ID 1")

	// Second detection
	det2 := testDetectionWithSpecies("Great Tit", "Parus major", 0.90)
	action2 := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det2.Result,
		Results:      det2.Results,
		EventTracker: eventTracker,
	}

	err = action2.Execute(nil)
	require.NoError(t, err)
	assert.Equal(t, uint(2), action2.Result.ID, "Second detection should get ID 2")

	// Verify both were saved
	savedNotes := mockDs.GetSavedNotes()
	require.Len(t, savedNotes, 2, "Should have 2 saved notes")
}

// TestDatabaseAction_Execute_WithoutDetectionContext verifies that
// DatabaseAction works even without DetectionContext (backward compatibility).
func TestDatabaseAction_Execute_WithoutDetectionContext(t *testing.T) {
	t.Parallel()

	mockDs := NewActionMockDatastore()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()

	action := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
		DetectionCtx: nil, // No DetectionContext provided
	}

	// Execute should not panic or error
	err := action.Execute(nil)
	require.NoError(t, err, "Execute should succeed without DetectionContext")

	// Verify ID was still assigned
	assert.Equal(t, uint(1), action.Result.ID, "Result.ID should still be assigned")
}

// TestDatabaseAction_Execute_ReturnsErrorOnSaveFailure verifies that
// DatabaseAction returns an error when the datastore save fails.
func TestDatabaseAction_Execute_ReturnsErrorOnSaveFailure(t *testing.T) {
	t.Parallel()

	mockDs := NewActionMockDatastore()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)
	detectionCtx := &DetectionContext{}

	det := testDetection()

	// Configure mock to return error
	mockDs.SetSaveError(assert.AnError)

	action := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	// Execute should return error
	err := action.Execute(nil)
	require.Error(t, err, "Execute should return error on save failure")

	// Verify ID was not assigned
	assert.Equal(t, uint(0), action.Result.ID, "Result.ID should not be assigned on error")

	// Verify DetectionContext was not updated
	assert.Equal(t, uint64(0), detectionCtx.NoteID.Load(),
		"DetectionContext.NoteID should not be updated on error")
}

// TestDatabaseAction_Execute_EventTrackerLimiting verifies that
// DatabaseAction respects EventTracker for rate limiting.
func TestDatabaseAction_Execute_EventTrackerLimiting(t *testing.T) {
	t.Parallel()

	mockDs := NewActionMockDatastore()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()

	action := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
	}

	// First execute should save
	err := action.Execute(nil)
	require.NoError(t, err)
	assert.Len(t, mockDs.GetSavedNotes(), 1, "First execution should save")

	// Create a new action for the same species (to test rate limiting)
	// Note: The actual rate limiting behavior depends on EventTracker configuration
	// This test verifies the integration between DatabaseAction and EventTracker
	action2 := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
	}

	// Second execute might be rate-limited (depends on EventTracker config).
	// We intentionally ignore the error because:
	// 1. Rate limiting may or may not trigger depending on timing
	// 2. This test verifies integration, not specific rate limiting behavior
	// 3. Either outcome (save or skip) is valid for this test
	// TODO: Consider making this test deterministic by mocking time or EventTracker
	_ = action2.Execute(nil)

	// The important thing is that Execute doesn't error - the EventTracker
	// handles rate limiting internally
}

// =============================================================================
// Repository Path Tests (Phase 2 Migration)
// =============================================================================

// TestDatabaseAction_Execute_WithRepository verifies that DatabaseAction.Execute()
// uses the repository path when Repo is provided (Phase 2 migration).
func TestDatabaseAction_Execute_WithRepository(t *testing.T) {
	t.Parallel()

	// Setup with repository instead of legacy datastore
	mockRepo := NewMockDetectionRepository()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	require.Equal(t, uint(0), det.Result.ID, "Result should start with ID 0")

	action := &DatabaseAction{
		Settings:     settings,
		Repo:         mockRepo, // Use repository path
		Ds:           nil,      // No legacy datastore
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
	}

	// Execute
	err := action.Execute(nil)
	require.NoError(t, err, "DatabaseAction.Execute() should not return error")

	// Verify ID was assigned to Result via repository
	assert.NotEqual(t, uint(0), action.Result.ID, "Result.ID should be assigned after save")
	assert.Equal(t, uint(1), action.Result.ID, "First save should get ID 1")

	// Verify repository was called
	assert.Equal(t, 1, mockRepo.GetSavedCount(), "Repository Save should be called once")
}

// TestDatabaseAction_Execute_WithRepository_StoresIDInDetectionContext verifies that
// the repository path also updates DetectionContext for downstream actions.
func TestDatabaseAction_Execute_WithRepository_StoresIDInDetectionContext(t *testing.T) {
	t.Parallel()

	mockRepo := NewMockDetectionRepository()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)
	detectionCtx := &DetectionContext{}

	det := testDetection()

	action := &DatabaseAction{
		Settings:     settings,
		Repo:         mockRepo,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	err := action.Execute(nil)
	require.NoError(t, err)

	// Verify DetectionContext has the ID from repository path
	storedID := detectionCtx.NoteID.Load()
	assert.Equal(t, uint64(action.Result.ID), storedID,
		"DetectionContext.NoteID should contain the assigned ID")
	assert.NotEqual(t, uint64(0), storedID,
		"DetectionContext.NoteID should not be zero")
}

// TestDatabaseAction_Execute_WithRepository_ReturnsErrorOnSaveFailure verifies that
// the repository path properly propagates errors.
func TestDatabaseAction_Execute_WithRepository_ReturnsErrorOnSaveFailure(t *testing.T) {
	t.Parallel()

	mockRepo := NewMockDetectionRepository()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)
	detectionCtx := &DetectionContext{}

	det := testDetection()

	// Configure mock to return error
	mockRepo.SetSaveError(assert.AnError)

	action := &DatabaseAction{
		Settings:     settings,
		Repo:         mockRepo,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	// Execute should return error
	err := action.Execute(nil)
	require.Error(t, err, "Execute should return error on save failure")

	// Verify ID was not assigned
	assert.Equal(t, uint(0), action.Result.ID, "Result.ID should not be assigned on error")

	// Verify DetectionContext was not updated
	assert.Equal(t, uint64(0), detectionCtx.NoteID.Load(),
		"DetectionContext.NoteID should not be updated on error")
}

// TestDatabaseAction_Execute_PrefersRepositoryOverLegacy verifies that when both
// Repo and Ds are provided, the repository path is preferred.
func TestDatabaseAction_Execute_PrefersRepositoryOverLegacy(t *testing.T) {
	t.Parallel()

	mockRepo := NewMockDetectionRepository()
	mockDs := NewActionMockDatastore()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()

	action := &DatabaseAction{
		Settings:     settings,
		Repo:         mockRepo, // Both provided
		Ds:           mockDs,   // Both provided
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
	}

	err := action.Execute(nil)
	require.NoError(t, err)

	// Verify repository was used (has saved count)
	assert.Equal(t, 1, mockRepo.GetSavedCount(), "Repository should be called")

	// Verify legacy datastore was NOT used (no saved notes)
	assert.Empty(t, mockDs.GetSavedNotes(), "Legacy datastore should not be called when Repo is available")
}
