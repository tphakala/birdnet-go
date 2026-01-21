// mock_datastore_test.go - Mock datastore for action execution tests
//
// This mock provides a minimal implementation of datastore.Interface focused on
// testing DatabaseAction, MqttAction, and SSEAction execution. It simulates
// database ID assignment and captures saved notes for verification.
package processor

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"gorm.io/gorm"
)

// ActionMockDatastore implements datastore.Interface for testing action execution.
// It captures saved notes and simulates ID assignment.
type ActionMockDatastore struct {
	mu           sync.Mutex
	nextID       uint // Simple counter, mutex provides synchronization
	savedNotes   []*datastore.Note
	savedResults [][]datastore.Results
	saveErr      error
	getErr       error
	notes        map[uint]*datastore.Note // For Get() lookups
}

// NewActionMockDatastore creates a new mock datastore starting with ID 1.
func NewActionMockDatastore() *ActionMockDatastore {
	return &ActionMockDatastore{
		nextID: 1,
		notes:  make(map[uint]*datastore.Note),
	}
}

// Save implements datastore.Interface.Save.
// Assigns an auto-incrementing ID and stores the note.
func (m *ActionMockDatastore) Save(note *datastore.Note, results []datastore.Results) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.saveErr != nil {
		return m.saveErr
	}

	// Simulate database ID assignment
	note.ID = m.nextID
	m.nextID++

	// Store copies for verification (prevent mutation of internal state)
	noteCopy := *note
	m.savedNotes = append(m.savedNotes, &noteCopy)
	var resultsCopy []datastore.Results
	if results != nil {
		resultsCopy = append([]datastore.Results(nil), results...)
	}
	m.savedResults = append(m.savedResults, resultsCopy)
	m.notes[note.ID] = &noteCopy

	return nil
}

// Get implements datastore.Interface.Get.
func (m *ActionMockDatastore) Get(id string) (datastore.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getErr != nil {
		return datastore.Note{}, m.getErr
	}

	// Parse ID and look up
	noteID, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return datastore.Note{}, fmt.Errorf("invalid ID: %s", id)
	}

	if note, ok := m.notes[uint(noteID)]; ok {
		return *note, nil
	}

	return datastore.Note{}, fmt.Errorf("note not found: %s", id)
}

// GetSavedNotes returns deep copies of all notes that were saved.
// Returns copies to prevent callers from mutating internal state.
func (m *ActionMockDatastore) GetSavedNotes() []*datastore.Note {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*datastore.Note, len(m.savedNotes))
	for i, n := range m.savedNotes {
		if n == nil {
			continue
		}
		noteCopy := *n
		result[i] = &noteCopy
	}
	return result
}

// GetLastSavedNote returns a copy of the most recently saved note.
func (m *ActionMockDatastore) GetLastSavedNote() *datastore.Note {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.savedNotes) == 0 {
		return nil
	}
	n := m.savedNotes[len(m.savedNotes)-1]
	if n == nil {
		return nil
	}
	noteCopy := *n
	return &noteCopy
}

// GetLastSavedResults returns a copy of the most recently saved results.
func (m *ActionMockDatastore) GetLastSavedResults() []datastore.Results {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.savedResults) == 0 {
		return nil
	}
	last := m.savedResults[len(m.savedResults)-1]
	return append([]datastore.Results(nil), last...)
}

// SetSaveError sets an error to be returned on next Save call.
func (m *ActionMockDatastore) SetSaveError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveErr = err
}

// Implement remaining datastore.Interface methods as no-ops or stubs.
// These are not needed for action execution tests.

func (m *ActionMockDatastore) Open() error  { return nil }
func (m *ActionMockDatastore) Close() error { return nil }
func (m *ActionMockDatastore) Delete(_ string) error {
	return nil
}
func (m *ActionMockDatastore) SetMetrics(_ *datastore.Metrics)  {}
func (m *ActionMockDatastore) SetSunCalcMetrics(_ any)          {}
func (m *ActionMockDatastore) Optimize(_ context.Context) error { return nil }
func (m *ActionMockDatastore) GetAllNotes() ([]datastore.Note, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetTopBirdsData(_ string, _ float64) ([]datastore.Note, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetHourlyOccurrences(_, _ string, _ float64) ([24]int, error) {
	return [24]int{}, nil
}
func (m *ActionMockDatastore) SpeciesDetections(_, _, _ string, _ int, _ bool, _, _ int) ([]datastore.Note, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetLastDetections(_ int) ([]datastore.Note, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetAllDetectedSpecies() ([]datastore.Note, error) {
	return nil, nil
}
func (m *ActionMockDatastore) SearchNotes(_ string, _ bool, _, _ int) ([]datastore.Note, error) {
	return nil, nil
}
func (m *ActionMockDatastore) SearchNotesAdvanced(_ *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
	return nil, 0, nil
}
func (m *ActionMockDatastore) GetNoteClipPath(_ string) (string, error) {
	return "", nil
}
func (m *ActionMockDatastore) DeleteNoteClipPath(_ string) error {
	return nil
}
func (m *ActionMockDatastore) GetNoteReview(_ string) (*datastore.NoteReview, error) {
	return nil, datastore.ErrNoteReviewNotFound
}
func (m *ActionMockDatastore) SaveNoteReview(_ *datastore.NoteReview) error {
	return nil
}
func (m *ActionMockDatastore) GetNoteComments(_ string) ([]datastore.NoteComment, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetNoteResults(_ string) ([]datastore.Results, error) {
	return nil, nil
}
func (m *ActionMockDatastore) SaveNoteComment(_ *datastore.NoteComment) error {
	return nil
}
func (m *ActionMockDatastore) UpdateNoteComment(_, _ string) error {
	return nil
}
func (m *ActionMockDatastore) DeleteNoteComment(_ string) error {
	return nil
}
func (m *ActionMockDatastore) SaveDailyEvents(_ *datastore.DailyEvents) error {
	return nil
}
func (m *ActionMockDatastore) GetDailyEvents(_ string) (datastore.DailyEvents, error) {
	return datastore.DailyEvents{}, nil
}
func (m *ActionMockDatastore) SaveHourlyWeather(_ *datastore.HourlyWeather) error {
	return nil
}
func (m *ActionMockDatastore) GetHourlyWeather(_ string) ([]datastore.HourlyWeather, error) {
	return nil, nil
}
func (m *ActionMockDatastore) LatestHourlyWeather() (*datastore.HourlyWeather, error) {
	// Returns ErrNoteReviewNotFound as a generic "not found" sentinel.
	// This stub method is not exercised by action execution tests.
	return nil, datastore.ErrNoteReviewNotFound
}
func (m *ActionMockDatastore) GetHourlyDetections(_, _ string, _, _, _ int) ([]datastore.Note, error) {
	return nil, nil
}
func (m *ActionMockDatastore) CountSpeciesDetections(_, _, _ string, _ int) (int64, error) {
	return 0, nil
}
func (m *ActionMockDatastore) CountSearchResults(_ string) (int64, error) {
	return 0, nil
}
func (m *ActionMockDatastore) Transaction(_ func(tx *gorm.DB) error) error {
	return nil
}
func (m *ActionMockDatastore) LockNote(_ string) error {
	return nil
}
func (m *ActionMockDatastore) UnlockNote(_ string) error {
	return nil
}
func (m *ActionMockDatastore) GetNoteLock(_ string) (*datastore.NoteLock, error) {
	return nil, datastore.ErrNoteLockNotFound
}
func (m *ActionMockDatastore) IsNoteLocked(_ string) (bool, error) {
	return false, nil
}
func (m *ActionMockDatastore) GetImageCache(_ datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	return nil, datastore.ErrImageCacheNotFound
}
func (m *ActionMockDatastore) GetImageCacheBatch(_ string, _ []string) (map[string]*datastore.ImageCache, error) {
	return make(map[string]*datastore.ImageCache), nil
}
func (m *ActionMockDatastore) SaveImageCache(_ *datastore.ImageCache) error {
	return nil
}
func (m *ActionMockDatastore) GetAllImageCaches(_ string) ([]datastore.ImageCache, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetLockedNotesClipPaths() ([]string, error) {
	return nil, nil
}
func (m *ActionMockDatastore) CountHourlyDetections(_, _ string, _ int) (int64, error) {
	return 0, nil
}
func (m *ActionMockDatastore) GetSpeciesSummaryData(_ context.Context, _, _ string) ([]datastore.SpeciesSummaryData, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetHourlyAnalyticsData(_ context.Context, _, _ string) ([]datastore.HourlyAnalyticsData, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetDailyAnalyticsData(_ context.Context, _, _, _ string) ([]datastore.DailyAnalyticsData, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetDetectionTrends(_ context.Context, _ string, _ int) ([]datastore.DailyAnalyticsData, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetHourlyDistribution(_ context.Context, _, _, _ string) ([]datastore.HourlyDistributionData, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetNewSpeciesDetections(_ context.Context, _, _ string, _, _ int) ([]datastore.NewSpeciesData, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetSpeciesFirstDetectionInPeriod(_ context.Context, _, _ string, _, _ int) ([]datastore.NewSpeciesData, error) {
	return nil, nil
}
func (m *ActionMockDatastore) SearchDetections(_ *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
	return nil, 0, nil
}
func (m *ActionMockDatastore) SaveDynamicThreshold(_ *datastore.DynamicThreshold) error {
	return nil
}
func (m *ActionMockDatastore) GetDynamicThreshold(_ string) (*datastore.DynamicThreshold, error) {
	// Returns ErrNoteReviewNotFound as a generic "not found" sentinel.
	// This stub method is not exercised by action execution tests.
	return nil, datastore.ErrNoteReviewNotFound
}
func (m *ActionMockDatastore) GetAllDynamicThresholds(_ ...int) ([]datastore.DynamicThreshold, error) {
	return nil, nil
}
func (m *ActionMockDatastore) DeleteDynamicThreshold(_ string) error {
	return nil
}
func (m *ActionMockDatastore) DeleteExpiredDynamicThresholds(_ time.Time) (int64, error) {
	return 0, nil
}
func (m *ActionMockDatastore) UpdateDynamicThresholdExpiry(_ string, _ time.Time) error {
	return nil
}
func (m *ActionMockDatastore) BatchSaveDynamicThresholds(_ []datastore.DynamicThreshold) error {
	return nil
}
func (m *ActionMockDatastore) DeleteAllDynamicThresholds() (int64, error) {
	return 0, nil
}
func (m *ActionMockDatastore) GetDynamicThresholdStats() (totalCount, activeCount, atMinimumCount int64, levelDistribution map[int]int64, err error) {
	return 0, 0, 0, nil, nil
}
func (m *ActionMockDatastore) SaveThresholdEvent(_ *datastore.ThresholdEvent) error {
	return nil
}
func (m *ActionMockDatastore) GetThresholdEvents(_ string, _ int) ([]datastore.ThresholdEvent, error) {
	return nil, nil
}
func (m *ActionMockDatastore) GetRecentThresholdEvents(_ int) ([]datastore.ThresholdEvent, error) {
	return nil, nil
}
func (m *ActionMockDatastore) DeleteThresholdEvents(_ string) error {
	return nil
}
func (m *ActionMockDatastore) DeleteAllThresholdEvents() (int64, error) {
	return 0, nil
}
func (m *ActionMockDatastore) SaveNotificationHistory(_ *datastore.NotificationHistory) error {
	return nil
}
func (m *ActionMockDatastore) GetNotificationHistory(_, _ string) (*datastore.NotificationHistory, error) {
	return nil, datastore.ErrNotificationHistoryNotFound
}
func (m *ActionMockDatastore) GetActiveNotificationHistory(_ time.Time) ([]datastore.NotificationHistory, error) {
	return nil, nil
}
func (m *ActionMockDatastore) DeleteExpiredNotificationHistory(_ time.Time) (int64, error) {
	return 0, nil
}
func (m *ActionMockDatastore) GetDatabaseStats() (*datastore.DatabaseStats, error) {
	return &datastore.DatabaseStats{Type: "mock", Connected: true}, nil
}

// Compile-time check that ActionMockDatastore implements datastore.Interface
var _ datastore.Interface = (*ActionMockDatastore)(nil)

// MockDetectionRepository implements datastore.DetectionRepository for testing.
// It simulates database ID assignment and captures saved detections for verification.
type MockDetectionRepository struct {
	mu                     sync.Mutex
	nextID                 uint
	savedCount             int
	saveErr                error
	savedResult            *detection.Result
	savedAdditionalResults []detection.AdditionalResult // Captures additional results for verification
}

// NewMockDetectionRepository creates a new mock repository starting with ID 1.
func NewMockDetectionRepository() *MockDetectionRepository {
	return &MockDetectionRepository{
		nextID: 1,
	}
}

// Save implements datastore.DetectionRepository.Save.
func (m *MockDetectionRepository) Save(_ context.Context, result *detection.Result, additionalResults []detection.AdditionalResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.saveErr != nil {
		return m.saveErr
	}

	// Simulate database ID assignment
	result.ID = m.nextID
	m.nextID++
	m.savedCount++
	m.savedResult = result
	m.savedAdditionalResults = additionalResults // Capture for verification

	return nil
}

// SetSaveError sets an error to be returned on next Save call.
func (m *MockDetectionRepository) SetSaveError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveErr = err
}

// GetSavedCount returns the number of times Save was called successfully.
func (m *MockDetectionRepository) GetSavedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.savedCount
}

// GetLastSavedResult returns the last saved detection result.
func (m *MockDetectionRepository) GetLastSavedResult() *detection.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.savedResult
}

// GetLastSavedAdditionalResults returns the additional results from the last Save call.
func (m *MockDetectionRepository) GetLastSavedAdditionalResults() []detection.AdditionalResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.savedAdditionalResults
}

// Stub implementations for remaining DetectionRepository methods
// These are intentionally stubbed to return nil - not used in tests
//
//nolint:nilnil // Stub method for mock
func (m *MockDetectionRepository) Get(_ context.Context, _ string) (*detection.Result, error) {
	return nil, nil
}
func (m *MockDetectionRepository) Delete(_ context.Context, _ string) error { return nil }
func (m *MockDetectionRepository) GetRecent(_ context.Context, _ int) ([]*detection.Result, error) {
	return nil, nil
}
func (m *MockDetectionRepository) Search(_ context.Context, _ *datastore.DetectionFilters) ([]*detection.Result, int64, error) {
	return nil, 0, nil
}
func (m *MockDetectionRepository) GetBySpecies(_ context.Context, _ string, _ *datastore.DetectionFilters) ([]*detection.Result, int64, error) {
	return nil, 0, nil
}
func (m *MockDetectionRepository) GetByDateRange(_ context.Context, _, _ string, _, _ int) ([]*detection.Result, int64, error) {
	return nil, 0, nil
}
func (m *MockDetectionRepository) GetHourly(_ context.Context, _, _ string, _, _, _ int) ([]*detection.Result, int64, error) {
	return nil, 0, nil
}
func (m *MockDetectionRepository) Lock(_ context.Context, _ string) error   { return nil }
func (m *MockDetectionRepository) Unlock(_ context.Context, _ string) error { return nil }
func (m *MockDetectionRepository) IsLocked(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (m *MockDetectionRepository) SetReview(_ context.Context, _, _ string) error { return nil }
func (m *MockDetectionRepository) GetReview(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (m *MockDetectionRepository) AddComment(_ context.Context, _, _ string) error { return nil }
func (m *MockDetectionRepository) GetComments(_ context.Context, _ string) ([]detection.Comment, error) {
	return nil, nil
}
func (m *MockDetectionRepository) UpdateComment(_ context.Context, _ uint, _ string) error {
	return nil
}
func (m *MockDetectionRepository) DeleteComment(_ context.Context, _ uint) error { return nil }
func (m *MockDetectionRepository) GetClipPath(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (m *MockDetectionRepository) GetAdditionalResults(_ context.Context, _ string) ([]detection.AdditionalResult, error) {
	return nil, nil
}

// Compile-time check that MockDetectionRepository implements datastore.DetectionRepository
var _ datastore.DetectionRepository = (*MockDetectionRepository)(nil)
