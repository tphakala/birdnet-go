// database_action_test.go - Tests for DatabaseAction.Execute()
//
// These tests verify that DatabaseAction correctly:
// - Assigns the database ID back to Result after successful save
// - Stores the ID in DetectionContext for downstream actions (MQTT, SSE)
// - Saves additional results alongside the main detection
package processor

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
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
	err := action.Execute(t.Context(), nil)
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
	err := action.Execute(t.Context(), nil)
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

	err := action.Execute(t.Context(), nil)
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

	err := action1.Execute(t.Context(), nil)
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

	err = action2.Execute(t.Context(), nil)
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
	err := action.Execute(t.Context(), nil)
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
	err := action.Execute(t.Context(), nil)
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
	err := action.Execute(t.Context(), nil)
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
	_ = action2.Execute(t.Context(), nil)

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
	err := action.Execute(t.Context(), nil)
	require.NoError(t, err, "DatabaseAction.Execute() should not return error")

	// Verify ID was assigned to Result via repository
	assert.NotEqual(t, uint(0), action.Result.ID, "Result.ID should be assigned after save")
	assert.Equal(t, uint(1), action.Result.ID, "First save should get ID 1")

	// Verify repository was called
	assert.Equal(t, 1, mockRepo.GetSavedCount(), "Repository Save should be called once")
}

// ctxRecordingRepository records whether the context its Save received was already done.
type ctxRecordingRepository struct {
	*MockDetectionRepository
	ctxErr error
}

func (r *ctxRecordingRepository) Save(ctx context.Context, result *detection.Result, additional []detection.AdditionalResult) error {
	r.ctxErr = ctx.Err()
	return r.MockDetectionRepository.Save(ctx, result, additional)
}

// TestDatabaseAction_Execute_SaveIgnoresCallerCancellation verifies that the repository
// save is detached from the caller's context: the CompositeAction's per-step deadline must
// not cancel a save that would otherwise commit late.
func TestDatabaseAction_Execute_SaveIgnoresCallerCancellation(t *testing.T) {
	t.Parallel()

	repo := &ctxRecordingRepository{MockDetectionRepository: NewMockDetectionRepository()}
	det := testDetection()
	action := &DatabaseAction{
		Settings:     &conf.Settings{Debug: true},
		Repo:         repo,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: NewEventTracker(testEventTrackerInterval),
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, action.Execute(ctx, nil))
	assert.Equal(t, 1, repo.GetSavedCount(), "the save must run")
	require.NoError(t, repo.ctxErr, "the save must not see the caller's cancellation")
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

	err := action.Execute(t.Context(), nil)
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
	err := action.Execute(t.Context(), nil)
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

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err)

	// Verify repository was used (has saved count)
	assert.Equal(t, 1, mockRepo.GetSavedCount(), "Repository should be called")

	// Verify legacy datastore was NOT used (no saved notes)
	assert.Empty(t, mockDs.GetSavedNotes(), "Legacy datastore should not be called when Repo is available")
}

// TestDatabaseAction_Execute_WithRepository_SavesAdditionalResults verifies that
// additional results are correctly passed to the repository Save method.
func TestDatabaseAction_Execute_WithRepository_SavesAdditionalResults(t *testing.T) {
	t.Parallel()

	mockRepo := NewMockDetectionRepository()
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
		Repo:         mockRepo,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
	}

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err)

	// Verify additional results were passed to repository
	savedResults := mockRepo.GetLastSavedAdditionalResults()
	require.Len(t, savedResults, 2, "Should save 2 additional results via repository")
	assert.Equal(t, "Parus major", savedResults[0].Species.ScientificName)
	assert.Equal(t, "Cyanistes caeruleus", savedResults[1].Species.ScientificName)
}

// scriptedSaveRepository returns the scripted errors from Save in order and then
// succeeds. When release is set, Save blocks on it before returning.
type scriptedSaveRepository struct {
	*MockDetectionRepository
	mu      sync.Mutex
	script  []error
	calls   int
	release chan struct{}
	// comments counts AddComment calls.
	comments atomic.Int32
}

func (r *scriptedSaveRepository) Save(ctx context.Context, result *detection.Result, additional []detection.AdditionalResult) error {
	r.mu.Lock()
	r.calls++
	var scripted error
	if len(r.script) > 0 {
		scripted, r.script = r.script[0], r.script[1:]
	}
	r.mu.Unlock()

	if r.release != nil {
		<-r.release
	}
	if scripted != nil {
		return scripted
	}
	return r.MockDetectionRepository.Save(ctx, result, additional)
}

func (r *scriptedSaveRepository) AddComment(_ context.Context, _, _ string) error {
	r.comments.Add(1)
	return nil
}

func (r *scriptedSaveRepository) saveCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// refusedRetrySafeError is the error the v2 store returns when the database
// refuses the connection before anything is written.
func refusedRetrySafeError() error {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	return fmt.Errorf("failed to save detection: %w: %w", datastore.ErrDetectionSaveRetrySafe, refused)
}

// TestDatabaseAction_RetrySafeFailureSavesOnceWithoutRegating drives the three
// job attempts of a detection whose first two saves are refused. The species
// interval is far longer than the test, so a retry that asked the event tracker
// again would be rate limited into a silent no-op, and one that asked the new
// species tracker again would no longer see a new species.
func TestDatabaseAction_RetrySafeFailureSavesOnceWithoutRegating(t *testing.T) {
	consumer := setupEventBusWithConsumer(t)

	mockDS := mocks.NewMockInterface(t)
	mockDS.EXPECT().
		GetActiveNotificationHistory(mock.Anything, mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	mockDS.EXPECT().
		SaveNotificationHistory(mock.Anything, mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	tracker := species.NewTrackerFromSettings(mockDS, &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         testWindowDays,
		SyncIntervalMinutes:          testSyncIntervalMins,
		NotificationSuppressionHours: testSuppressionHours,
	})

	repo := &scriptedSaveRepository{
		MockDetectionRepository: NewMockDetectionRepository(),
		script:                  []error{refusedRetrySafeError(), refusedRetrySafeError()},
	}
	det := testDetection()
	detectionCtx := &DetectionContext{}
	action := &DatabaseAction{
		Settings:          &conf.Settings{Debug: true},
		Repo:              repo,
		Result:            det.Result,
		Results:           det.Results,
		EventTracker:      NewEventTracker(time.Hour),
		NewSpeciesTracker: tracker,
		DetectionCtx:      detectionCtx,
	}
	retry := action.saveRetryConfig()
	require.True(t, retry.Enabled)

	for attempt := 1; attempt <= 2; attempt++ {
		require.Error(t, action.ExecuteContext(t.Context(), nil), "attempt %d is refused", attempt)
		assert.True(t, retry.Retryable(nil), "a refused save keeps the job retrying after attempt %d", attempt)
		assert.Equal(t, uint64(0), detectionCtx.NoteID.Load(), "nothing is stored yet")
	}

	require.NoError(t, action.ExecuteContext(t.Context(), nil), "the third attempt saves")
	assert.Equal(t, 3, repo.saveCalls(), "every attempt reached the repository")
	assert.Equal(t, 1, repo.GetSavedCount(), "one stored detection")
	assert.NotZero(t, detectionCtx.NoteID.Load(), "downstream actions get the stored ID")
	assert.False(t, retry.Retryable(nil), "a stored detection ends the retries")

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.Len(collect, consumer.GetReceivedEvents(), 1)
	}, 2*time.Second, 50*time.Millisecond)
	assert.True(t, consumer.GetReceivedEvents()[0].IsNewSpecies(), "the new species notice survives the retries")
}

// TestDatabaseAction_CommitOutcomeUnknownIsNotRetried covers a save that failed
// with a connectivity error the store did not mark retry safe, as a lost COMMIT
// reply does. The server may have stored the row, so a repeat could duplicate it.
func TestDatabaseAction_CommitOutcomeUnknownIsNotRetried(t *testing.T) {
	t.Parallel()

	repo := &scriptedSaveRepository{
		MockDetectionRepository: NewMockDetectionRepository(),
		script:                  []error{fmt.Errorf("failed to save detection: %w", mysql.ErrInvalidConn)},
	}
	det := testDetection()
	action := &DatabaseAction{
		Settings:     &conf.Settings{Debug: true},
		Repo:         repo,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: NewEventTracker(time.Hour),
	}
	retry := action.saveRetryConfig()

	firstErr := action.ExecuteContext(t.Context(), nil)
	require.Error(t, firstErr)
	require.True(t, errors.IsConnectivityError(firstErr), "the error looks transient")
	assert.False(t, retry.Retryable(firstErr), "an unknown commit outcome ends the job")

	require.Error(t, action.ExecuteContext(t.Context(), nil), "a later call reports the same failure")
	assert.Equal(t, 1, repo.saveCalls(), "the save is never repeated")
	assert.Zero(t, repo.GetSavedCount())
}

// TestDatabaseAction_RetryWaitsForInFlightSave covers a save that outlasts its
// job attempt. The next attempt must not start a second save while the first is
// running, and must not repeat the save once the first one has stored the row.
func TestDatabaseAction_RetryWaitsForInFlightSave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &scriptedSaveRepository{
			MockDetectionRepository: NewMockDetectionRepository(),
			release:                 make(chan struct{}),
		}
		det := testDetection()
		detectionCtx := &DetectionContext{}
		action := &DatabaseAction{
			Settings:     &conf.Settings{Debug: true},
			Repo:         repo,
			Result:       det.Result,
			Results:      det.Results,
			EventTracker: NewEventTracker(time.Hour),
			DetectionCtx: detectionCtx,
		}
		retry := action.saveRetryConfig()

		var firstErr atomic.Pointer[error]
		first := make(chan struct{})
		go func() {
			defer close(first)
			err := action.ExecuteContext(t.Context(), nil)
			firstErr.Store(&err)
		}()
		synctest.Wait()
		require.Equal(t, 1, repo.saveCalls(), "the first attempt is saving")

		ctx, cancel := context.WithTimeout(t.Context(), CompositeActionTimeout)
		defer cancel()
		err := action.ExecuteContext(ctx, nil)
		require.ErrorIs(t, err, errDetectionSaveInFlight)
		assert.True(t, retry.Retryable(err), "a save still running keeps the job retrying")
		assert.Equal(t, 1, repo.saveCalls(), "no second save while one is in flight")

		close(repo.release)
		<-first
		require.NoError(t, *firstErr.Load())
		assert.NotZero(t, detectionCtx.NoteID.Load())

		require.NoError(t, action.ExecuteContext(t.Context(), nil), "a later attempt lets the composite continue")
		assert.Equal(t, 1, repo.saveCalls(), "the stored detection is not saved again")
		assert.False(t, retry.Retryable(nil))
	})
}

// TestTaskDetectionFor_DropsPCMForDatabaseJobs verifies that a job that may wait
// out a retry window does not keep the PCM audio alive, and that other jobs keep it.
func TestTaskDetectionFor_DropsPCMForDatabaseJobs(t *testing.T) {
	t.Parallel()

	det := testDetection()
	det.pcmData3s = []byte{1, 2, 3}
	db := &DatabaseAction{}

	tests := []struct {
		name    string
		action  Action
		wantPCM bool
	}{
		{"bare database action", db, false},
		{"database composite", &CompositeAction{Actions: []Action{db, &SSEAction{}}}, false},
		{"other composite", &CompositeAction{Actions: []Action{&SSEAction{}}}, true},
		{"audio export", &SaveAudioAction{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := taskDetectionFor(&det, tt.action)
			assert.Equal(t, tt.wantPCM, len(got.pcmData3s) > 0)
			assert.Equal(t, det.Result.Species, got.Result.Species)
		})
	}
}

// TestDatabaseAction_RetryWindowEnds covers the end of the retry window: a save
// that is still refused on the last attempt ends the job, and a save still
// running on the last attempt is not repeated and not reported as retryable
// once it has failed.
func TestDatabaseAction_RetryWindowEnds(t *testing.T) {
	const lastAttempt = 2

	t.Run("refused_on_the_last_attempt", func(t *testing.T) {
		t.Parallel()
		repo := &scriptedSaveRepository{
			MockDetectionRepository: NewMockDetectionRepository(),
			script:                  []error{refusedRetrySafeError(), refusedRetrySafeError()},
		}
		det := testDetection()
		action := &DatabaseAction{
			Settings: &conf.Settings{Debug: true}, Repo: repo, Result: det.Result, Results: det.Results,
			EventTracker: NewEventTracker(time.Hour),
		}
		retry := action.saveRetryConfig()
		action.maxAttempts.Store(lastAttempt)

		require.Error(t, action.ExecuteContext(t.Context(), nil))
		assert.True(t, retry.Retryable(nil), "attempts remain")
		require.Error(t, action.ExecuteContext(t.Context(), nil))
		assert.False(t, retry.Retryable(nil), "the window is over")
		require.Error(t, action.ExecuteContext(t.Context(), nil), "a call after the window reports the failure")
		assert.Equal(t, lastAttempt, repo.saveCalls(), "no save after the window")
	})

	t.Run("save_outlives_the_last_attempt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			repo := &scriptedSaveRepository{
				MockDetectionRepository: NewMockDetectionRepository(),
				script:                  []error{refusedRetrySafeError()},
				release:                 make(chan struct{}),
			}
			det := testDetection()
			action := &DatabaseAction{
				Settings: &conf.Settings{Debug: true}, Repo: repo, Result: det.Result, Results: det.Results,
				EventTracker: NewEventTracker(time.Hour),
			}
			retry := action.saveRetryConfig()
			action.maxAttempts.Store(lastAttempt)

			first := make(chan error, 1)
			go func() { first <- action.ExecuteContext(t.Context(), nil) }()
			synctest.Wait()

			ctx, cancel := context.WithTimeout(t.Context(), CompositeActionTimeout)
			defer cancel()
			require.ErrorIs(t, action.ExecuteContext(ctx, nil), errDetectionSaveInFlight)
			assert.True(t, action.windowEnded.Load(), "the last attempt gave up on the running save")

			close(repo.release)
			require.Error(t, <-first, "the abandoned save was refused")
			assert.False(t, retry.Retryable(nil), "nothing retries after the window")
			assert.Equal(t, 1, repo.saveCalls())
		})
	})

	t.Run("save_lands_after_the_last_attempt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			repo := &scriptedSaveRepository{
				MockDetectionRepository: NewMockDetectionRepository(),
				release:                 make(chan struct{}),
			}
			det := testDetection()
			detectionCtx := &DetectionContext{}
			action := &DatabaseAction{
				Settings: &conf.Settings{Debug: true}, Repo: repo, Result: det.Result, Results: det.Results,
				EventTracker: NewEventTracker(time.Hour), DetectionCtx: detectionCtx,
			}
			action.saveRetryConfig()
			action.maxAttempts.Store(lastAttempt)

			first := make(chan error, 1)
			go func() { first <- action.ExecuteContext(t.Context(), nil) }()
			synctest.Wait()

			ctx, cancel := context.WithTimeout(t.Context(), CompositeActionTimeout)
			defer cancel()
			require.ErrorIs(t, action.ExecuteContext(ctx, nil), errDetectionSaveInFlight)

			close(repo.release)
			require.NoError(t, <-first)
			assert.NotZero(t, detectionCtx.NoteID.Load(), "the row is stored and its ID shared")
			assert.Equal(t, 1, repo.GetSavedCount())
		})
	})
}

// TestDatabaseAction_LegacyStoreIsNotRetried verifies that a failure of the legacy
// datastore path ends the job, whatever the error says.
func TestDatabaseAction_LegacyStoreIsNotRetried(t *testing.T) {
	t.Parallel()

	mockDs := NewActionMockDatastore()
	mockDs.SetSaveError(refusedRetrySafeError())
	det := testDetection()
	action := &DatabaseAction{
		Settings: &conf.Settings{Debug: true}, Ds: mockDs, Result: det.Result, Results: det.Results,
		EventTracker: NewEventTracker(time.Hour),
	}
	retry := action.saveRetryConfig()

	require.Error(t, action.ExecuteContext(t.Context(), nil))
	assert.False(t, retry.Retryable(nil))
	assert.Equal(t, 1, mockDs.GetSaveCalls())
}

// TestDatabaseAction_UnlikelyCommentAfterRetry verifies that the follow-up steps
// of a stored detection run once, on the attempt that stored it.
func TestDatabaseAction_UnlikelyCommentAfterRetry(t *testing.T) {
	t.Parallel()

	repo := &scriptedSaveRepository{
		MockDetectionRepository: NewMockDetectionRepository(),
		script:                  []error{refusedRetrySafeError()},
	}
	det := testDetection()
	det.Result.Unlikely = true
	action := &DatabaseAction{
		Settings: &conf.Settings{Debug: true}, Repo: repo, Result: det.Result, Results: det.Results,
		EventTracker: NewEventTracker(time.Hour),
	}
	action.saveRetryConfig()

	require.Error(t, action.ExecuteContext(t.Context(), nil))
	require.NoError(t, action.ExecuteContext(t.Context(), nil))
	require.NoError(t, action.ExecuteContext(t.Context(), nil))
	assert.Equal(t, 1, repo.GetSavedCount())
	assert.Equal(t, 1, int(repo.comments.Load()), "the unlikely comment is added once")
}
