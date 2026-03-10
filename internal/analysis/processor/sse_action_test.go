// sse_action_test.go - Tests for SSEAction.Execute()
//
// These tests verify that SSEAction correctly:
// - Reads detection ID from DetectionContext
// - Broadcasts detection data via SSEBroadcaster
// - Silently skips when broadcaster is nil
package processor

import (
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

	err := action.Execute(t.Context(), nil)
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

	err := action.Execute(t.Context(), nil)
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

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err, "Should succeed without DetectionContext")

	// Verify broadcast was called
	assert.Equal(t, 1, mockBroadcaster.GetBroadcastCount(), "Should broadcast once")

	// Note.ID should be 0 when no DetectionContext
	note := mockBroadcaster.GetLastNote()
	require.NotNil(t, note)
	assert.Equal(t, uint(0), note.ID, "Note.ID should be 0 without DetectionContext")
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

	err := action.Execute(t.Context(), nil)
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

	err := action.Execute(t.Context(), nil)
	require.Error(t, err, "Should return error on broadcast failure")
}

// TestSSEAction_Execute_NoClipNameBroadcastsQuickly verifies that SSEAction
// broadcasts without delay when ClipName is empty.
func TestSSEAction_Execute_NoClipNameBroadcastsQuickly(t *testing.T) {
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

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err)

	// Verify broadcast was called successfully
	assert.Equal(t, 1, mockBroadcaster.GetBroadcastCount(), "Should broadcast once")
	require.NotNil(t, mockBroadcaster.GetLastNote(), "Note should be broadcasted")
}
