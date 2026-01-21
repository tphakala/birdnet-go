// sse_action_test.go - Tests for SSEAction.Execute()
//
// These tests verify that SSEAction correctly:
// - Reads detection ID from DetectionContext
// - Broadcasts detection data via SSEBroadcaster
// - Handles AudioExportFailed flag to skip waiting
// - Silently skips when broadcaster is nil
package processor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// MockSSEBroadcaster captures broadcast calls for testing.
type MockSSEBroadcaster struct {
	mu             sync.Mutex
	broadcastCount int
	lastNote       *datastore.Note
	lastImage      *imageprovider.BirdImage
	broadcastErr   error
}

// NewMockSSEBroadcaster creates a mock broadcaster that captures calls.
func NewMockSSEBroadcaster() *MockSSEBroadcaster {
	return &MockSSEBroadcaster{}
}

// BroadcastFunc returns a function suitable for SSEAction.SSEBroadcaster.
func (m *MockSSEBroadcaster) BroadcastFunc() func(*datastore.Note, *imageprovider.BirdImage) error {
	return func(note *datastore.Note, image *imageprovider.BirdImage) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.broadcastCount++
		m.lastNote = note
		m.lastImage = image
		return m.broadcastErr
	}
}

// GetBroadcastCount returns the number of broadcast calls.
func (m *MockSSEBroadcaster) GetBroadcastCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.broadcastCount
}

// GetLastNote returns the last broadcasted note.
func (m *MockSSEBroadcaster) GetLastNote() *datastore.Note {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastNote
}

// SetBroadcastError sets an error to be returned on broadcast.
func (m *MockSSEBroadcaster) SetBroadcastError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcastErr = err
}

// TestSSEAction_Execute_UsesDetectionContextID verifies that SSEAction
// reads the detection ID from DetectionContext and includes it in the note.
func TestSSEAction_Execute_UsesDetectionContextID(t *testing.T) {
	t.Parallel()

	mockBroadcaster := NewMockSSEBroadcaster()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()

	// Create DetectionContext with pre-set ID (simulating DatabaseAction having run)
	detectionCtx := &DetectionContext{}
	expectedID := uint64(42)
	detectionCtx.NoteID.Store(expectedID)

	action := &SSEAction{
		Settings:       settings,
		Result:         det.Result,
		EventTracker:   eventTracker,
		DetectionCtx:   detectionCtx,
		SSEBroadcaster: mockBroadcaster.BroadcastFunc(),
	}

	err := action.Execute(context.Background(), nil)
	require.NoError(t, err, "SSEAction.Execute() should not return error")

	// Verify broadcast was called
	assert.Equal(t, 1, mockBroadcaster.GetBroadcastCount(), "Should broadcast once")

	// Verify note has the correct ID
	note := mockBroadcaster.GetLastNote()
	require.NotNil(t, note, "Note should be broadcasted")
	assert.Equal(t, uint(expectedID), note.ID, "Note.ID should match DetectionContext.NoteID")

	// Verify Result.ID was also updated
	assert.Equal(t, uint(expectedID), action.Result.ID, "Result.ID should be updated from context")
}

// TestSSEAction_Execute_NoBroadcaster verifies that SSEAction silently
// skips when no broadcaster is configured.
func TestSSEAction_Execute_NoBroadcaster(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)

	action := &SSEAction{
		Settings:       settings,
		Result:         det.Result,
		EventTracker:   eventTracker,
		DetectionCtx:   detectionCtx,
		SSEBroadcaster: nil, // No broadcaster
	}

	err := action.Execute(context.Background(), nil)
	require.NoError(t, err, "Should silently succeed without broadcaster")
}

// TestSSEAction_Execute_WithoutDetectionContext verifies that SSEAction
// works even without DetectionContext (backward compatibility).
func TestSSEAction_Execute_WithoutDetectionContext(t *testing.T) {
	t.Parallel()

	mockBroadcaster := NewMockSSEBroadcaster()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()

	action := &SSEAction{
		Settings:       settings,
		Result:         det.Result,
		EventTracker:   eventTracker,
		DetectionCtx:   nil, // No DetectionContext
		SSEBroadcaster: mockBroadcaster.BroadcastFunc(),
	}

	err := action.Execute(context.Background(), nil)
	require.NoError(t, err, "Should succeed without DetectionContext")

	// Verify broadcast was called
	assert.Equal(t, 1, mockBroadcaster.GetBroadcastCount(), "Should broadcast once")

	// Note.ID should be 0 when no DetectionContext
	note := mockBroadcaster.GetLastNote()
	require.NotNil(t, note)
	assert.Equal(t, uint(0), note.ID, "Note.ID should be 0 without DetectionContext")
}

// TestSSEAction_Execute_SkipsAudioWaitOnExportFailure verifies that SSEAction
// skips waiting for audio file when AudioExportFailed is set.
func TestSSEAction_Execute_SkipsAudioWaitOnExportFailure(t *testing.T) {
	t.Parallel()

	mockBroadcaster := NewMockSSEBroadcaster()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	// Set a clip name that would normally trigger audio wait
	det.Result.ClipName = "/path/to/nonexistent/clip.wav"

	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)
	// Mark audio export as failed - SSEAction should skip waiting
	detectionCtx.AudioExportFailed.Store(true)

	action := &SSEAction{
		Settings:       settings,
		Result:         det.Result,
		EventTracker:   eventTracker,
		DetectionCtx:   detectionCtx,
		SSEBroadcaster: mockBroadcaster.BroadcastFunc(),
	}

	startTime := time.Now()
	err := action.Execute(context.Background(), nil)
	duration := time.Since(startTime)

	require.NoError(t, err, "Should succeed even with nonexistent clip")

	// Verify it didn't wait (should be fast, not 5+ seconds)
	assert.Less(t, duration, 500*time.Millisecond,
		"Should skip audio wait when AudioExportFailed is set")

	// Verify broadcast was still called
	assert.Equal(t, 1, mockBroadcaster.GetBroadcastCount(), "Should still broadcast")
}

// TestSSEAction_Execute_BroadcastsCorrectData verifies that SSEAction
// broadcasts the correct detection data.
func TestSSEAction_Execute_BroadcastsCorrectData(t *testing.T) {
	t.Parallel()

	mockBroadcaster := NewMockSSEBroadcaster()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	// Create detection with specific data
	now := time.Now()
	det := Detections{
		Result: detection.Result{
			Timestamp:  now,
			SourceNode: "test-node",
			AudioSource: detection.AudioSource{
				ID:          "source-1",
				DisplayName: "Test Source",
			},
			BeginTime: now,
			EndTime:   now.Add(15 * time.Second),
			Species: detection.Species{
				ScientificName: "Turdus migratorius",
				CommonName:     "American Robin",
				Code:           "amerob",
			},
			Confidence: 0.95,
			Model:      detection.DefaultModelInfo(),
		},
	}

	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(123)

	action := &SSEAction{
		Settings:       settings,
		Result:         det.Result,
		EventTracker:   eventTracker,
		DetectionCtx:   detectionCtx,
		SSEBroadcaster: mockBroadcaster.BroadcastFunc(),
	}

	err := action.Execute(context.Background(), nil)
	require.NoError(t, err)

	note := mockBroadcaster.GetLastNote()
	require.NotNil(t, note)

	// Verify note contains correct data
	assert.Equal(t, uint(123), note.ID)
	assert.Equal(t, "American Robin", note.CommonName)
	assert.Equal(t, "Turdus migratorius", note.ScientificName)
	assert.InDelta(t, 0.95, note.Confidence, 0.001)
}

// TestSSEAction_Execute_ReturnsErrorOnBroadcastFailure verifies that
// SSEAction returns an error when broadcasting fails.
func TestSSEAction_Execute_ReturnsErrorOnBroadcastFailure(t *testing.T) {
	t.Parallel()

	mockBroadcaster := NewMockSSEBroadcaster()
	mockBroadcaster.SetBroadcastError(assert.AnError)

	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)

	action := &SSEAction{
		Settings:       settings,
		Result:         det.Result,
		EventTracker:   eventTracker,
		DetectionCtx:   detectionCtx,
		SSEBroadcaster: mockBroadcaster.BroadcastFunc(),
	}

	err := action.Execute(context.Background(), nil)
	require.Error(t, err, "Should return error on broadcast failure")
}

// TestSSEAction_Execute_NoClipNameSkipsAudioWait verifies that SSEAction
// doesn't wait for audio when ClipName is empty.
func TestSSEAction_Execute_NoClipNameSkipsAudioWait(t *testing.T) {
	t.Parallel()

	mockBroadcaster := NewMockSSEBroadcaster()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	det.Result.ClipName = "" // No clip

	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)

	action := &SSEAction{
		Settings:       settings,
		Result:         det.Result,
		EventTracker:   eventTracker,
		DetectionCtx:   detectionCtx,
		SSEBroadcaster: mockBroadcaster.BroadcastFunc(),
	}

	startTime := time.Now()
	err := action.Execute(context.Background(), nil)
	duration := time.Since(startTime)

	require.NoError(t, err)

	// Should be fast since no audio wait is needed
	assert.Less(t, duration, 500*time.Millisecond,
		"Should not wait for audio when ClipName is empty")
}
