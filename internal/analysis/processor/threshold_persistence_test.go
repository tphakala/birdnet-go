// threshold_persistence_test.go: Unit tests for dynamic threshold persistence functionality
package processor

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	"gorm.io/gorm"
)

// MockDatastore implements datastore.Interface for testing
type MockDatastore struct {
	thresholds                   map[string]*datastore.DynamicThreshold
	getAllError                  error
	saveError                    error
	batchSaveError               error
	deleteError                  error
	deleteExpiredError           error
	updateExpiryError            error
	deletePerSpeciesErrors       map[string]error // per-species errors for DeleteDynamicThreshold
	deleteEventsPerSpeciesErrors map[string]error // per-species errors for DeleteThresholdEvents
	deleteAllThresholdsError     error            // error for DeleteAllDynamicThresholds
	deleteAllEventsError         error            // error for DeleteAllThresholdEvents
	deletedCount                 int64
	batchSaveCalled              bool
	batchSaveCalls               int                  // number of BatchSaveDynamicThresholds calls
	batchSaveHook                func(call int) error // per-call error injection; call is 1-based
	// deleteExpiredCalls counts DeleteExpiredDynamicThresholds calls; atomic because
	// the cleanup goroutine calls it while the test goroutine reads it.
	deleteExpiredCalls atomic.Int64
	getAllCalled       bool
	// Call counters to verify both delete paths are invoked even on error
	deleteDynamicThresholdCalls int
	deleteThresholdEventsCalls  int
	deleteAllThresholdsCalls    int
	deleteAllEventsCalls        int
}

// Implement all required methods from datastore.Interface

func (m *MockDatastore) Open() error                                       { return nil }
func (m *MockDatastore) Close() error                                      { return nil }
func (m *MockDatastore) Save(*datastore.Note, []datastore.Results) error   { return nil }
func (m *MockDatastore) EnsureModelRegistered(_ detection.ModelInfo) error { return nil }
func (m *MockDatastore) Delete(string) error                               { return nil }
func (m *MockDatastore) Get(string) (datastore.Note, error)                { return datastore.Note{}, nil }
func (m *MockDatastore) SetMetrics(*datastore.Metrics)                     {}
func (m *MockDatastore) SetSunCalcMetrics(any)                             {}
func (m *MockDatastore) Optimize(context.Context) error                    { return nil }
func (m *MockDatastore) GetAllNotes() ([]datastore.Note, error) {
	return make([]datastore.Note, 0), nil
}
func (m *MockDatastore) GetTopBirdsData(context.Context, string, float64, int) ([]datastore.Note, error) {
	return make([]datastore.Note, 0), nil
}
func (m *MockDatastore) GetBatchHourlyOccurrences(context.Context, string, string, []string, float64) (map[string][24]int, error) {
	return make(map[string][24]int), nil
}
func (m *MockDatastore) SpeciesDetections(string, string, string, int, bool, int, int) ([]datastore.Note, error) {
	return make([]datastore.Note, 0), nil
}
func (m *MockDatastore) GetLastDetections(int) ([]datastore.Note, error) {
	return make([]datastore.Note, 0), nil
}
func (m *MockDatastore) GetAllDetectedSpecies() ([]datastore.Note, error) {
	return make([]datastore.Note, 0), nil
}
func (m *MockDatastore) SearchNotes(string, bool, int, int) ([]datastore.Note, int64, error) {
	return make([]datastore.Note, 0), 0, nil
}
func (m *MockDatastore) SearchNotesAdvanced(*datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
	return make([]datastore.Note, 0), 0, nil
}
func (m *MockDatastore) GetNoteClipPath(string) (string, error) {
	return "", datastore.ErrNoteReviewNotFound
}
func (m *MockDatastore) GetNoteModelType(_ string) (string, error) { return "bird", nil }
func (m *MockDatastore) GetNoteKeptSpectrogram(_ string) (clipName, modelType string, err error) {
	return "", "bird", nil
}

func (m *MockDatastore) RecordClipSampleRate(_ context.Context, _ uint, _ string, _ int) error {
	return nil
}

func (m *MockDatastore) FillClipSampleRate(_ context.Context, _ uint, _ string, _ int) (bool, error) {
	return false, nil
}
func (m *MockDatastore) DeleteNoteClipPath(string) error { return nil }
func (m *MockDatastore) GetNoteReview(string) (*datastore.NoteReview, error) {
	return nil, datastore.ErrNoteReviewNotFound
}
func (m *MockDatastore) SaveNoteReview(*datastore.NoteReview) error { return nil }
func (m *MockDatastore) GetNoteComments(string) ([]datastore.NoteComment, error) {
	return make([]datastore.NoteComment, 0), nil
}
func (m *MockDatastore) GetNoteResults(string) ([]datastore.Results, error) {
	return nil, nil
}
func (m *MockDatastore) SaveNoteComment(*datastore.NoteComment) error { return nil }
func (m *MockDatastore) UpdateNoteComment(string, string) error       { return nil }
func (m *MockDatastore) DeleteNoteComment(string) error               { return nil }
func (m *MockDatastore) SaveDailyEvents(*datastore.DailyEvents) error { return nil }
func (m *MockDatastore) GetDailyEvents(string) (datastore.DailyEvents, error) {
	return datastore.DailyEvents{}, nil
}
func (m *MockDatastore) GetAllDailyEvents() ([]datastore.DailyEvents, error) {
	return nil, nil
}
func (m *MockDatastore) GetAllHourlyWeather() ([]datastore.HourlyWeather, error) {
	return nil, nil
}
func (m *MockDatastore) SaveHourlyWeather(*datastore.HourlyWeather) error { return nil }
func (m *MockDatastore) GetHourlyWeather(string) ([]datastore.HourlyWeather, error) {
	return make([]datastore.HourlyWeather, 0), nil
}
func (m *MockDatastore) LatestHourlyWeather() (*datastore.HourlyWeather, error) {
	return nil, datastore.ErrNoteReviewNotFound
}
func (m *MockDatastore) GetHourlyDetections(string, string, int, int, int) ([]datastore.Note, error) {
	return make([]datastore.Note, 0), nil
}
func (m *MockDatastore) CountSpeciesDetections(string, string, string, int) (int64, error) {
	return 0, nil
}
func (m *MockDatastore) Transaction(func(*gorm.DB) error) error { return nil }
func (m *MockDatastore) LockNote(string) error                  { return nil }
func (m *MockDatastore) UnlockNote(string) error                { return nil }
func (m *MockDatastore) GetNoteLock(string) (*datastore.NoteLock, error) {
	return nil, datastore.ErrNoteLockNotFound
}
func (m *MockDatastore) IsNoteLocked(string) (bool, error) { return false, nil }
func (m *MockDatastore) GetImageCache(datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	return nil, datastore.ErrImageCacheNotFound
}
func (m *MockDatastore) GetImageCacheBatch(string, []string) (map[string]*datastore.ImageCache, error) {
	return make(map[string]*datastore.ImageCache), nil
}
func (m *MockDatastore) SaveImageCache(*datastore.ImageCache) error { return nil }
func (m *MockDatastore) GetAllImageCaches(string) ([]datastore.ImageCache, error) {
	return make([]datastore.ImageCache, 0), nil
}
func (m *MockDatastore) GetLockedNotesClipPaths() ([]string, error)          { return make([]string, 0), nil }
func (m *MockDatastore) ClearNoteClipPathsByNames(_ []string) (int64, error) { return 0, nil }
func (m *MockDatastore) RetainNoteSpectrogramsByClipNames(_ []string) (int64, error) {
	return 0, nil
}
func (m *MockDatastore) GetNoteClipReferences(_ uint, _ int) ([]diskmanager.ClipReference, error) {
	return nil, nil
}
func (m *MockDatastore) CountHourlyDetections(string, string, int) (int64, error) { return 0, nil }
func (m *MockDatastore) GetSpeciesSummaryData(context.Context, string, string) ([]datastore.SpeciesSummaryData, error) {
	return make([]datastore.SpeciesSummaryData, 0), nil
}
func (m *MockDatastore) GetHourlyAnalyticsData(context.Context, string, string) ([]datastore.HourlyAnalyticsData, error) {
	return make([]datastore.HourlyAnalyticsData, 0), nil
}
func (m *MockDatastore) GetDailyAnalyticsData(context.Context, string, string, string) ([]datastore.DailyAnalyticsData, error) {
	return make([]datastore.DailyAnalyticsData, 0), nil
}
func (m *MockDatastore) GetDetectionTrends(context.Context, string, int) ([]datastore.DailyAnalyticsData, error) {
	return make([]datastore.DailyAnalyticsData, 0), nil
}
func (m *MockDatastore) GetHourlyDistribution(context.Context, string, string, string) ([]datastore.HourlyDistributionData, error) {
	return make([]datastore.HourlyDistributionData, 0), nil
}
func (m *MockDatastore) GetNewSpeciesDetections(context.Context, string, string, int, int) ([]datastore.NewSpeciesData, error) {
	return make([]datastore.NewSpeciesData, 0), nil
}
func (m *MockDatastore) GetSpeciesFirstDetectionInPeriod(context.Context, string, string, int, int) ([]datastore.NewSpeciesData, error) {
	return make([]datastore.NewSpeciesData, 0), nil
}
func (m *MockDatastore) GetSpeciesDiversityData(context.Context, string, string) ([]datastore.DailyAnalyticsData, error) {
	return nil, nil
}
func (m *MockDatastore) GetActivityHeatmap(context.Context, string, string, string) (datastore.ActivityHeatmapData, error) {
	return datastore.ActivityHeatmapData{}, nil
}
func (m *MockDatastore) GetHourlyDistributionBySpecies(context.Context, string, string, []string, int) ([]datastore.SpeciesHourlyDistribution, error) {
	return []datastore.SpeciesHourlyDistribution{}, nil
}
func (m *MockDatastore) GetDailyActivityOnset(context.Context, string, string, string) ([]datastore.DailyActivityOnset, error) {
	return []datastore.DailyActivityOnset{}, nil
}

func (m *MockDatastore) GetConfidenceHistogram(context.Context, string, string, string, int, int) ([]datastore.SpeciesConfidenceHistogram, error) {
	return []datastore.SpeciesConfidenceHistogram{}, nil
}
func (m *MockDatastore) GetSpeciesAccumulation(context.Context, string, string) ([]datastore.SpeciesAccumulationPoint, error) {
	return []datastore.SpeciesAccumulationPoint{}, nil
}
func (m *MockDatastore) GetAudioSources(context.Context, string, string) ([]datastore.AudioSourceSummary, error) {
	return []datastore.AudioSourceSummary{}, nil
}
func (m *MockDatastore) GetYearOverYear(_ context.Context, _ string) (datastore.YearOverYearResult, error) {
	return datastore.YearOverYearResult{Points: []datastore.YearOverYearPoint{}}, nil
}
func (m *MockDatastore) GetSpeciesPhenology(context.Context, string, string, int) ([]datastore.SpeciesPhenologyPoint, error) {
	return []datastore.SpeciesPhenologyPoint{}, nil
}
func (m *MockDatastore) GetAcousticSuccession(context.Context, string, string, []string, int) ([]datastore.SpeciesHourlyCounts, error) {
	return []datastore.SpeciesHourlyCounts{}, nil
}
func (m *MockDatastore) SearchDetections(*datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
	return make([]datastore.DetectionRecord, 0), 0, nil
}

// Dynamic threshold methods
func (m *MockDatastore) SaveDynamicThreshold(threshold *datastore.DynamicThreshold) error {
	if m.saveError != nil {
		return m.saveError
	}
	if m.thresholds == nil {
		m.thresholds = make(map[string]*datastore.DynamicThreshold)
	}
	m.thresholds[threshold.SpeciesName] = threshold
	return nil
}

func (m *MockDatastore) GetDynamicThreshold(speciesName string) (*datastore.DynamicThreshold, error) {
	if threshold, exists := m.thresholds[speciesName]; exists {
		return threshold, nil
	}
	return nil, datastore.ErrNoteReviewNotFound
}

func (m *MockDatastore) GetAllDynamicThresholds(limit ...int) ([]datastore.DynamicThreshold, error) {
	m.getAllCalled = true
	if m.getAllError != nil {
		return nil, m.getAllError
	}

	thresholds := make([]datastore.DynamicThreshold, 0, len(m.thresholds))
	for _, t := range m.thresholds {
		thresholds = append(thresholds, *t)
	}

	// Apply limit if provided
	if len(limit) > 0 && limit[0] > 0 && limit[0] < len(thresholds) {
		thresholds = thresholds[:limit[0]]
	}

	return thresholds, nil
}

func (m *MockDatastore) DeleteDynamicThreshold(speciesName string) error {
	m.deleteDynamicThresholdCalls++
	// Check per-species errors first
	if m.deletePerSpeciesErrors != nil {
		if err, ok := m.deletePerSpeciesErrors[speciesName]; ok {
			return err
		}
	}
	if m.deleteError != nil {
		return m.deleteError
	}
	delete(m.thresholds, speciesName)
	return nil
}

func (m *MockDatastore) DeleteExpiredDynamicThresholds(before time.Time) (int64, error) {
	m.deleteExpiredCalls.Add(1)
	if m.deleteExpiredError != nil {
		return 0, m.deleteExpiredError
	}

	deleted := int64(0)
	for name, threshold := range m.thresholds {
		if before.After(threshold.ExpiresAt) {
			delete(m.thresholds, name)
			deleted++
		}
	}
	m.deletedCount = deleted
	return deleted, nil
}

func (m *MockDatastore) UpdateDynamicThresholdExpiry(speciesName string, expiresAt time.Time) error {
	if m.updateExpiryError != nil {
		return m.updateExpiryError
	}
	if threshold, exists := m.thresholds[speciesName]; exists {
		threshold.ExpiresAt = expiresAt
		return nil
	}
	return datastore.ErrNoteReviewNotFound
}

func (m *MockDatastore) BatchSaveDynamicThresholds(thresholds []datastore.DynamicThreshold) error {
	m.batchSaveCalled = true
	m.batchSaveCalls++
	if m.batchSaveHook != nil {
		if err := m.batchSaveHook(m.batchSaveCalls); err != nil {
			return err
		}
	}
	if m.batchSaveError != nil {
		return m.batchSaveError
	}

	if m.thresholds == nil {
		m.thresholds = make(map[string]*datastore.DynamicThreshold)
	}

	for i := range thresholds {
		threshold := thresholds[i]
		m.thresholds[threshold.SpeciesName] = &threshold
	}
	return nil
}

// BG-59: Add new dynamic threshold methods
func (m *MockDatastore) DeleteAllDynamicThresholds() (int64, error) {
	m.deleteAllThresholdsCalls++
	if m.deleteAllThresholdsError != nil {
		return 0, m.deleteAllThresholdsError
	}
	count := int64(len(m.thresholds))
	m.thresholds = make(map[string]*datastore.DynamicThreshold)
	return count, nil
}

func (m *MockDatastore) GetDynamicThresholdStats() (totalCount, activeCount, atMinimumCount int64, levelDistribution map[int]int64, err error) {
	levelDistribution = make(map[int]int64)
	now := time.Now()
	for _, t := range m.thresholds {
		totalCount++
		if t.ExpiresAt.After(now) {
			activeCount++
			if t.Level == 3 {
				atMinimumCount++
			}
			levelDistribution[t.Level]++
		}
	}
	return totalCount, activeCount, atMinimumCount, levelDistribution, nil
}

func (m *MockDatastore) SaveThresholdEvent(*datastore.ThresholdEvent) error {
	return nil
}

func (m *MockDatastore) GetThresholdEvents(string, int) ([]datastore.ThresholdEvent, error) {
	return []datastore.ThresholdEvent{}, nil
}

func (m *MockDatastore) GetRecentThresholdEvents(int) ([]datastore.ThresholdEvent, error) {
	return []datastore.ThresholdEvent{}, nil
}

func (m *MockDatastore) DeleteThresholdEvents(speciesName string) error {
	m.deleteThresholdEventsCalls++
	if m.deleteEventsPerSpeciesErrors != nil {
		if err, ok := m.deleteEventsPerSpeciesErrors[speciesName]; ok {
			return err
		}
	}
	return nil
}

func (m *MockDatastore) DeleteAllThresholdEvents() (int64, error) {
	m.deleteAllEventsCalls++
	if m.deleteAllEventsError != nil {
		return 0, m.deleteAllEventsError
	}
	return 0, nil
}

// BG-17 fix: Add notification history methods
func (m *MockDatastore) GetActiveNotificationHistory(_ context.Context, after time.Time) ([]datastore.NotificationHistory, error) {
	return []datastore.NotificationHistory{}, nil
}

func (m *MockDatastore) GetNotificationHistory(_ context.Context, scientificName, notificationType string) (*datastore.NotificationHistory, error) {
	return nil, errors.Newf("notification history not found").
		Component("datastore").
		Category(errors.CategoryNotFound).
		Build()
}

func (m *MockDatastore) SaveNotificationHistory(_ context.Context, history *datastore.NotificationHistory) error {
	return nil
}

func (m *MockDatastore) DeleteExpiredNotificationHistory(_ context.Context, before time.Time) (int64, error) {
	return 0, nil
}

func (m *MockDatastore) SchemaVersion() string { return datastore.SchemaVersionLegacy }
func (m *MockDatastore) GetDatabaseStats(_ context.Context) (*datastore.DatabaseStats, error) {
	return &datastore.DatabaseStats{
		Type:      "mock",
		Connected: true,
	}, nil
}
func (m *MockDatastore) PingWithLatency(_ context.Context) (time.Duration, error) { return 0, nil }
func (m *MockDatastore) CountDetectionsSince(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}
func (m *MockDatastore) SaveAppEvent(_ context.Context, _, _, _ string, _ map[string]any) error {
	return nil
}
func (m *MockDatastore) GetRecentAppEvents(_ context.Context, _ int) ([]datastore.AppEvent, error) {
	return nil, nil
}
func (m *MockDatastore) GetAppEventsSince(_ context.Context, _ time.Time, _ int) ([]datastore.AppEvent, error) {
	return nil, nil
}
func (m *MockDatastore) PruneAppEvents(_ context.Context, _ int) (int64, error) { return 0, nil }

// Migration bulk fetch methods
func (m *MockDatastore) GetAllReviews() ([]datastore.NoteReview, error) {
	return nil, nil
}

func (m *MockDatastore) GetAllComments() ([]datastore.NoteComment, error) {
	return nil, nil
}

func (m *MockDatastore) GetAllLocks() ([]datastore.NoteLock, error) {
	return nil, nil
}

func (m *MockDatastore) GetAllResults() ([]datastore.Results, error) {
	return nil, nil
}

// Batched migration methods
func (m *MockDatastore) GetReviewsBatch(afterID uint, batchSize int) ([]datastore.NoteReview, error) {
	return nil, nil
}

func (m *MockDatastore) GetCommentsBatch(afterID uint, batchSize int) ([]datastore.NoteComment, error) {
	return nil, nil
}

func (m *MockDatastore) GetLocksBatch(afterID uint, batchSize int) ([]datastore.NoteLock, error) {
	return nil, nil
}

func (m *MockDatastore) GetResultsBatch(afterNoteID, afterResultID uint, batchSize int) ([]datastore.Results, error) {
	return nil, nil
}

func (m *MockDatastore) CountResults() (int64, error) {
	return 0, nil
}

// createTestProcessor creates a processor with mock datastore for testing
func createTestProcessor() *Processor {
	settings := &conf.Settings{}
	settings.Realtime.DynamicThreshold.Enabled = true
	settings.Realtime.DynamicThreshold.Debug = false
	settings.BirdNET.Threshold = 0.7
	settings.Realtime.Species.Config = make(map[string]conf.SpeciesConfig)

	mockDs := &MockDatastore{
		thresholds: make(map[string]*datastore.DynamicThreshold),
	}

	p := &Processor{
		Settings:          settings,
		Ds:                mockDs,
		DynamicThresholds: make(map[string]*DynamicThreshold),
		pendingResets:     make(map[string]struct{}),
	}

	return p
}

const (
	persistTestSpeciesKept  = "american crow"
	persistTestSpeciesReset = "blue jay"
	persistTestBaseValue    = 0.75
	persistTestValidHours   = 48
	persistTestLevel        = 1
	persistTestRelearnLevel = 2
	// persistTestShortDeadline is a flush deadline far shorter than any real cycle.
	persistTestShortDeadline = 5 * time.Second
	// persistTestDoneCtxAttempts repeats the done-context lock attempt so a lock
	// that picks randomly between a free slot and a done context fails reliably.
	persistTestDoneCtxAttempts = 64
)

// Delete operations passed to concurrentThresholdStore.deleteHook.
const (
	opDeleteThreshold     = "DeleteDynamicThreshold"
	opDeleteEvents        = "DeleteThresholdEvents"
	opDeleteAllThresholds = "DeleteAllDynamicThresholds"
	opDeleteAllEvents     = "DeleteAllThresholdEvents"
)

// concurrentThresholdStore is a goroutine-safe datastore fake for tests where a
// persistence cycle and a reset run in different goroutines. The mutex-protected
// row map and event list record what a database would hold. saveHook runs inside
// BatchSaveDynamicThresholds with no lock held; it may block or start goroutines,
// and a returned error fails that save attempt. deleteHook runs at the entry of
// each of the four delete methods with no lock held and may block.
type concurrentThresholdStore struct {
	*MockDatastore
	mu         sync.Mutex
	rows       map[string]datastore.DynamicThreshold
	events     []datastore.ThresholdEvent
	saveCalls  int
	saveHook   func(call int) error
	deleteHook func(op string)
}

func (s *concurrentThresholdStore) BatchSaveDynamicThresholds(ts []datastore.DynamicThreshold) error {
	s.mu.Lock()
	s.saveCalls++
	call := s.saveCalls
	s.mu.Unlock()
	if s.saveHook != nil {
		if err := s.saveHook(call); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range ts {
		s.rows[ts[i].SpeciesName] = ts[i]
	}
	return nil
}

func (s *concurrentThresholdStore) runDeleteHook(op string) {
	if s.deleteHook != nil {
		s.deleteHook(op)
	}
}

func (s *concurrentThresholdStore) DeleteDynamicThreshold(name string) error {
	s.runDeleteHook(opDeleteThreshold)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, name)
	return nil
}

func (s *concurrentThresholdStore) DeleteThresholdEvents(name string) error {
	s.runDeleteHook(opDeleteEvents)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = slices.DeleteFunc(s.events, func(e datastore.ThresholdEvent) bool {
		return e.SpeciesName == name
	})
	return nil
}

func (s *concurrentThresholdStore) DeleteAllDynamicThresholds() (int64, error) {
	s.runDeleteHook(opDeleteAllThresholds)
	s.mu.Lock()
	defer s.mu.Unlock()
	n := int64(len(s.rows))
	clear(s.rows)
	return n, nil
}

func (s *concurrentThresholdStore) DeleteAllThresholdEvents() (int64, error) {
	s.runDeleteHook(opDeleteAllEvents)
	s.mu.Lock()
	defer s.mu.Unlock()
	n := int64(len(s.events))
	s.events = nil
	return n, nil
}

func (s *concurrentThresholdStore) SaveThresholdEvent(e *datastore.ThresholdEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, *e)
	return nil
}

func (s *concurrentThresholdStore) eventsFor(species string) []datastore.ThresholdEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(s.events), func(e datastore.ThresholdEvent) bool {
		return e.SpeciesName != species
	})
}

func (s *concurrentThresholdStore) hasRow(species string) (datastore.DynamicThreshold, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[species]
	return row, ok
}

func (s *concurrentThresholdStore) saveCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveCalls
}

func (s *concurrentThresholdStore) rowNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Collect(maps.Keys(s.rows))
}

// newConcurrentThresholdProcessor returns a processor holding two learned
// thresholds (persistTestSpeciesKept and persistTestSpeciesReset), both already
// present as database rows, backed by a concurrentThresholdStore.
func newConcurrentThresholdProcessor() (*Processor, *concurrentThresholdStore) {
	p := createTestProcessor()
	store := &concurrentThresholdStore{
		MockDatastore: &MockDatastore{},
		rows:          make(map[string]datastore.DynamicThreshold),
	}
	p.Ds = store
	now := time.Now()
	for _, name := range []string{persistTestSpeciesKept, persistTestSpeciesReset} {
		p.DynamicThresholds[name] = &DynamicThreshold{
			Level:         persistTestLevel,
			BaseThreshold: persistTestBaseValue,
			Timer:         now.Add(persistTestValidHours * time.Hour),
			ValidHours:    persistTestValidHours,
		}
		store.rows[name] = datastore.DynamicThreshold{SpeciesName: name}
	}
	return p, store
}

// TestPersistDynamicThresholds_ResetDuringInFlightCycleIsNotResurrected pins the
// interleaving where cycle A is inside its save, a reset is requested, a second
// cycle B (a flush) runs, and A then finishes: the reset species must stay
// deleted, because the reset waits for A and B waits for the reset.
func TestPersistDynamicThresholds_ResetDuringInFlightCycleIsNotResurrected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p, store := newConcurrentThresholdProcessor()
		release := make(chan struct{})
		releaseCycle := sync.OnceFunc(func() { close(release) })
		defer releaseCycle()
		store.saveHook = func(call int) error {
			if call == 1 {
				<-release
			}
			return nil
		}

		cycleDone := make(chan error, 1)
		go func() { cycleDone <- p.persistDynamicThresholds(t.Context()) }()
		synctest.Wait() // cycle A is blocked inside its first save

		resetDone := make(chan error, 1)
		go func() { resetDone <- p.ResetDynamicThreshold(persistTestSpeciesReset) }()
		synctest.Wait()

		flushDone := make(chan error, 1)
		go func() { flushDone <- p.FlushDynamicThresholds(t.Context()) }()
		synctest.Wait()

		releaseCycle()
		require.NoError(t, <-cycleDone)
		require.NoError(t, <-resetDone)
		require.NoError(t, <-flushDone)

		assert.ElementsMatch(t, []string{persistTestSpeciesKept}, store.rowNames(),
			"a stale snapshot re-inserted the reset species")
		assert.Empty(t, p.pendingResets, "successful deletes must not queue a retry")
	})
}

// TestFlushDynamicThresholds_GivesUpWhenLockHeldPastDeadline verifies a flush
// stuck behind a running cycle returns at its deadline instead of waiting for the
// cycle, so shutdown is not held up by a hung database call.
func TestFlushDynamicThresholds_GivesUpWhenLockHeldPastDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p, store := newConcurrentThresholdProcessor()
		release := make(chan struct{})
		releaseCycle := sync.OnceFunc(func() { close(release) })
		defer releaseCycle()
		store.saveHook = func(call int) error {
			if call == 1 {
				<-release
			}
			return nil
		}

		cycleDone := make(chan error, 1)
		go func() { cycleDone <- p.persistDynamicThresholds(t.Context()) }()
		synctest.Wait()

		ctx, cancel := context.WithTimeout(t.Context(), persistTestShortDeadline)
		defer cancel()
		start := time.Now()
		err := p.FlushDynamicThresholds(ctx)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, persistTestShortDeadline, time.Since(start))
		assert.Equal(t, 1, store.saveCallCount(), "the flush must not reach the database")

		releaseCycle()
		require.NoError(t, <-cycleDone)
	})
}

// TestPersistDynamicThresholds_FailedResetRetriedBeforeSave verifies that a reset
// queues a retry only when its own delete failed, that the retry runs before the
// snapshot so a species re-learned meanwhile is rewritten by the same cycle, and
// that a later successful reset drops an older queued retry.
func TestPersistDynamicThresholds_FailedResetRetriedBeforeSave(t *testing.T) {
	dbErr := errors.Newf("database locked").Build()
	relearn := func(p *Processor) {
		p.thresholdsMutex.Lock()
		defer p.thresholdsMutex.Unlock()
		p.DynamicThresholds[persistTestSpeciesReset] = &DynamicThreshold{
			Level:         persistTestRelearnLevel,
			BaseThreshold: persistTestBaseValue,
			Timer:         time.Now().Add(persistTestValidHours * time.Hour),
			ValidHours:    persistTestValidHours,
		}
	}

	cases := []struct {
		name    string
		setErr  func(m *MockDatastore, err error)
		reset   func(t *testing.T, p *Processor)
		pending func(p *Processor) bool
	}{
		{
			name: "single reset",
			setErr: func(m *MockDatastore, err error) {
				m.deletePerSpeciesErrors = map[string]error{persistTestSpeciesReset: err}
			},
			reset: func(t *testing.T, p *Processor) {
				t.Helper()
				require.NoError(t, p.ResetDynamicThreshold(persistTestSpeciesReset))
			},
			pending: func(p *Processor) bool {
				_, queued := p.pendingResets[persistTestSpeciesReset]
				return queued
			},
		},
		{
			name: "single reset, events delete fails",
			setErr: func(m *MockDatastore, err error) {
				m.deleteEventsPerSpeciesErrors = map[string]error{persistTestSpeciesReset: err}
			},
			reset: func(t *testing.T, p *Processor) {
				t.Helper()
				require.NoError(t, p.ResetDynamicThreshold(persistTestSpeciesReset))
			},
			pending: func(p *Processor) bool {
				_, queued := p.pendingResets[persistTestSpeciesReset]
				return queued
			},
		},
		{
			name:   "reset all, events delete fails",
			setErr: func(m *MockDatastore, err error) { m.deleteAllEventsError = err },
			reset: func(t *testing.T, p *Processor) {
				t.Helper()
				_, err := p.ResetAllDynamicThresholds()
				require.NoError(t, err)
			},
			pending: func(p *Processor) bool { return p.pendingResetAll },
		},
		{
			name:   "reset all",
			setErr: func(m *MockDatastore, err error) { m.deleteAllThresholdsError = err },
			reset: func(t *testing.T, p *Processor) {
				t.Helper()
				_, err := p.ResetAllDynamicThresholds()
				require.NoError(t, err)
			},
			pending: func(p *Processor) bool { return p.pendingResetAll },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := createTestProcessor()
			mockDs := p.Ds.(*MockDatastore)

			// A reset whose delete fails queues a retry.
			tc.setErr(mockDs, dbErr)
			tc.reset(t, p)
			require.True(t, tc.pending(p), "a failed reset must queue a retry")

			// The database recovers and the species is re-learned before the next cycle.
			tc.setErr(mockDs, nil)
			relearn(p)
			require.NoError(t, p.persistDynamicThresholds(t.Context()))

			saved := mockDs.thresholds[persistTestSpeciesReset]
			require.NotNil(t, saved, "the retry deleted the row the same cycle's save wrote")
			assert.Equal(t, persistTestRelearnLevel, saved.Level)
			assert.False(t, tc.pending(p), "the retry succeeded, nothing may stay queued")

			// A failed reset followed by a successful one leaves nothing queued.
			tc.setErr(mockDs, dbErr)
			tc.reset(t, p)
			require.True(t, tc.pending(p))
			tc.setErr(mockDs, nil)
			tc.reset(t, p)
			assert.False(t, tc.pending(p), "a successful reset must drop the older queued retry")
		})
	}
}

// TestLockThresholdPersistence_FreeLockTakenWithDoneContext verifies a free lock is
// taken even when ctx is already done (shutdown relies on one last flush attempt),
// and that a held lock with a done ctx gives up with the ctx error.
func TestLockThresholdPersistence_FreeLockTakenWithDoneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for range persistTestDoneCtxAttempts {
		p := &Processor{}
		require.NoError(t, p.lockThresholdPersistence(ctx), "a free lock must be taken with a done ctx")
		require.ErrorIs(t, p.lockThresholdPersistence(ctx), context.Canceled, "a held lock must give up with a done ctx")
		p.unlockThresholdPersistence()
	}
}

// TestResetDynamicThreshold_SuccessQueuesNothing verifies a reset whose deletes
// succeed leaves no retry behind.
func TestResetDynamicThreshold_SuccessQueuesNothing(t *testing.T) {
	p := createTestProcessor()
	require.NoError(t, p.ResetDynamicThreshold(persistTestSpeciesReset))
	_, err := p.ResetAllDynamicThresholds()
	require.NoError(t, err)

	assert.Empty(t, p.pendingResets)
	assert.False(t, p.pendingResetAll)
}

// TestLoadDynamicThresholdsFromDB tests loading thresholds from database
func TestLoadDynamicThresholdsFromDB(t *testing.T) {
	t.Run("FirstRun_NoThresholds", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)
		mockDs.getAllError = nil // No error, just empty

		err := p.loadDynamicThresholdsFromDB()

		require.NoError(t, err)
		assert.Empty(t, p.DynamicThresholds)
		assert.True(t, mockDs.getAllCalled)
	})

	t.Run("LoadValidThresholds", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		// Pre-populate mock database with valid thresholds (keyed per species)
		now := time.Now()
		mockDs.thresholds = map[string]*datastore.DynamicThreshold{
			"american crow": {
				SpeciesName:   "american crow",
				Level:         1,
				CurrentValue:  0.75,
				BaseThreshold: 0.7,
				HighConfCount: 5,
				ValidHours:    48,
				ExpiresAt:     now.Add(24 * time.Hour),
				FirstCreated:  now.Add(-24 * time.Hour),
				UpdatedAt:     now,
			},
			"blue jay": {
				SpeciesName:   "blue jay",
				Level:         2,
				CurrentValue:  0.8,
				BaseThreshold: 0.7,
				HighConfCount: 10,
				ValidHours:    48,
				ExpiresAt:     now.Add(48 * time.Hour),
				FirstCreated:  now.Add(-48 * time.Hour),
				UpdatedAt:     now,
			},
		}

		err := p.loadDynamicThresholdsFromDB()

		require.NoError(t, err)
		assert.Len(t, p.DynamicThresholds, 2)

		// Verify american crow threshold (keyed by species name)
		crowThreshold := p.DynamicThresholds["american crow"]
		require.NotNil(t, crowThreshold)
		assert.Equal(t, 1, crowThreshold.Level)
		assert.InDelta(t, 0.7, crowThreshold.BaseThreshold, 0.001, "BaseThreshold loads from DB")
		assert.Equal(t, 5, crowThreshold.HighConfCount)
		assert.Equal(t, 48, crowThreshold.ValidHours)

		// Verify blue jay threshold (keyed by species name)
		jayThreshold := p.DynamicThresholds["blue jay"]
		require.NotNil(t, jayThreshold)
		assert.Equal(t, 2, jayThreshold.Level)
		assert.InDelta(t, 0.7, jayThreshold.BaseThreshold, 0.001, "BaseThreshold loads from DB")
		assert.Equal(t, 10, jayThreshold.HighConfCount)
	})

	t.Run("SkipExpiredThresholds", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		now := time.Now()
		mockDs.thresholds = map[string]*datastore.DynamicThreshold{
			"american crow": {
				SpeciesName:  "american crow",
				Level:        1,
				CurrentValue: 0.75,
				ExpiresAt:    now.Add(24 * time.Hour), // Valid
			},
			"blue jay": {
				SpeciesName:  "blue jay",
				Level:        2,
				CurrentValue: 0.8,
				ExpiresAt:    now.Add(-1 * time.Hour), // Expired
			},
		}

		err := p.loadDynamicThresholdsFromDB()

		require.NoError(t, err)
		assert.Len(t, p.DynamicThresholds, 1, "Should only load non-expired threshold")
		assert.Contains(t, p.DynamicThresholds, "american crow")
		assert.NotContains(t, p.DynamicThresholds, "blue jay")
	})
}

// TestPersistDynamicThresholds tests saving thresholds to database
func TestPersistDynamicThresholds(t *testing.T) {
	t.Run("PersistValidThresholds", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		now := time.Now()
		// Add thresholds to in-memory map (keyed per species)
		p.DynamicThresholds["american crow"] = &DynamicThreshold{
			Level:         1,
			BaseThreshold: 0.75,
			Timer:         now.Add(24 * time.Hour),
			HighConfCount: 5,
			ValidHours:    48,
		}
		p.DynamicThresholds["blue jay"] = &DynamicThreshold{
			Level:         2,
			BaseThreshold: 0.8,
			Timer:         now.Add(48 * time.Hour),
			HighConfCount: 10,
			ValidHours:    48,
		}

		err := p.persistDynamicThresholds(t.Context())

		require.NoError(t, err)
		assert.True(t, mockDs.batchSaveCalled)
		assert.Len(t, mockDs.thresholds, 2)

		// Verify saved thresholds (BatchSave stores by species name)
		savedCrow := mockDs.thresholds["american crow"]
		require.NotNil(t, savedCrow)
		assert.Equal(t, 1, savedCrow.Level)
		assert.InDelta(t, 0.75, savedCrow.BaseThreshold, 0.001, "BaseThreshold should be persisted")
		assert.Equal(t, 5, savedCrow.HighConfCount)
	})

	t.Run("EmptyThresholdsMap", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		err := p.persistDynamicThresholds(t.Context())

		require.NoError(t, err)
		assert.False(t, mockDs.batchSaveCalled, "Should not call batch save with no thresholds")
	})

	t.Run("RemoveExpiredFromMemory", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		now := time.Now()
		p.DynamicThresholds["american crow"] = &DynamicThreshold{
			Level:         1,
			BaseThreshold: 0.75,
			Timer:         now.Add(24 * time.Hour), // Valid
		}
		p.DynamicThresholds["blue jay"] = &DynamicThreshold{
			Level:         2,
			BaseThreshold: 0.8,
			Timer:         now.Add(-1 * time.Hour), // Expired
		}

		err := p.persistDynamicThresholds(t.Context())

		require.NoError(t, err)
		assert.Len(t, p.DynamicThresholds, 1, "Expired threshold should be removed from memory")
		assert.Contains(t, p.DynamicThresholds, "american crow")
		assert.NotContains(t, p.DynamicThresholds, "blue jay")

		// Only valid threshold should be saved to database
		assert.Len(t, mockDs.thresholds, 1)
		assert.Contains(t, mockDs.thresholds, "american crow")
	})
}

// gormJoinedInvalidConn builds the error GORM returns when a statement fails and
// the rollback then fails with the driver's invalid connection error: both are
// joined with "%v; %w", so the statement error is text only.
func gormJoinedInvalidConn(stmtErr error) error {
	return fmt.Errorf("%v; %w", stmtErr, mysql.ErrInvalidConn) //nolint:errorlint // mirrors GORM, which keeps only the rollback error wrapped
}

// TestSaveThresholdsWithRetry_RetriesLockAndConnectivityErrorsOnly verifies which
// batch save errors the processor repeats.
func TestSaveThresholdsWithRetry_RetriesLockAndConnectivityErrorsOnly(t *testing.T) {
	// The v2 shape of a connection that dies mid-transaction: GORM joins the
	// failed statement and the rollback error with %v and %w.
	invalidConn := gormJoinedInvalidConn(mysql.ErrInvalidConn)
	serverErrWithInvalidConn := gormJoinedInvalidConn(errors.NewStd("Error 1366 (HY000): Incorrect string value"))

	tests := []struct {
		name      string
		hook      func(call int) error
		wantCalls int
		wantErr   bool
		wantWarns int // retry records; a recovered save must not log an Error
		wantErrs  int // final failure records
	}{
		{
			name: "invalid connection then success",
			hook: func(call int) error {
				if call == 1 {
					return invalidConn
				}
				return nil
			},
			wantCalls: 2,
			wantWarns: 1,
		},
		{
			name: "database locked then success",
			hook: func(call int) error {
				if call == 1 {
					return errors.NewStd("database is locked")
				}
				return nil
			},
			wantCalls: 2,
			wantWarns: 1,
		},
		{
			name:      "server error joined with invalid connection is not retried",
			hook:      func(int) error { return serverErrWithInvalidConn },
			wantCalls: 1,
			wantErr:   true,
			wantErrs:  1,
		},
		{
			name:      "connectivity error on every attempt is bounded",
			hook:      func(int) error { return invalidConn },
			wantCalls: thresholdSaveMaxAttempts,
			wantErr:   true,
			wantWarns: thresholdSaveMaxAttempts - 1,
			wantErrs:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				logs := logtest.CaptureBuffer(t)
				p := createTestProcessor()
				mockDs := p.Ds.(*MockDatastore)
				mockDs.batchSaveHook = tt.hook
				p.DynamicThresholds["american crow"] = &DynamicThreshold{
					Level:         1,
					BaseThreshold: 0.75,
					Timer:         time.Now().Add(24 * time.Hour),
					ValidHours:    48,
				}

				err := p.persistDynamicThresholds(t.Context())

				assert.Equal(t, tt.wantCalls, mockDs.batchSaveCalls)
				assert.Equal(t, tt.wantWarns, strings.Count(logs.String(), "level=WARN"), "retry records")
				assert.Equal(t, tt.wantErrs, strings.Count(logs.String(), "level=ERROR"), "final failure records")
				if tt.wantErr {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				assert.Contains(t, mockDs.thresholds, "american crow")
			})
		})
	}
}

// TestPersistDynamicThresholds_ResetDuringSaveRetryWaitsForCycle verifies that a
// reset requested while the save is being retried waits for the whole cycle, so
// the retried attempt cannot write a pre-reset snapshot after the reset deleted.
func TestPersistDynamicThresholds_ResetDuringSaveRetryWaitsForCycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p, store := newConcurrentThresholdProcessor()
		resetDone := make(chan error, 1)
		store.saveHook = func(call int) error {
			if call == 1 {
				go func() { resetDone <- p.ResetDynamicThreshold(persistTestSpeciesReset) }()
				return gormJoinedInvalidConn(mysql.ErrInvalidConn)
			}
			return nil
		}

		require.NoError(t, p.persistDynamicThresholds(t.Context()))
		require.NoError(t, <-resetDone)

		assert.Equal(t, 2, store.saveCallCount())
		assert.Contains(t, store.rowNames(), persistTestSpeciesKept)
		assert.NotContains(t, store.rowNames(), persistTestSpeciesReset, "the retried attempt re-inserted the reset species")
	})
}

// TestFlushDynamicThresholds tests immediate flush operation
func TestFlushDynamicThresholds(t *testing.T) {
	t.Run("SuccessfulFlush", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		now := time.Now()
		p.DynamicThresholds["american crow"] = &DynamicThreshold{
			Level:         1,
			BaseThreshold: 0.75,
			Timer:         now.Add(24 * time.Hour),
		}

		err := p.FlushDynamicThresholds(t.Context())

		require.NoError(t, err)
		assert.True(t, mockDs.batchSaveCalled)
	})
}

// TestConstants verifies the persistence constants
func TestPersistenceConstants(t *testing.T) {
	t.Run("VerifyDefaultValues", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, DefaultPersistInterval, "Persist interval should be 30 seconds")
		assert.Equal(t, 24*time.Hour, DefaultCleanupInterval, "Cleanup interval should be 24 hours")
		assert.Equal(t, 15*time.Second, DefaultFlushTimeout, "Flush timeout should be 15 seconds")
	})
}

// TestThresholdGoroutineLifecycle tests the lifecycle management of persistence goroutines
func TestThresholdGoroutineLifecycle(t *testing.T) {
	t.Run("StartAndStopGoroutines", func(t *testing.T) {
		p := createTestProcessor()

		// Start goroutines (creates the lifecycle context under the lock)
		p.startThresholdGoroutines()

		// Capture the context before stopping (snapshot under the lock). A non-nil
		// snapshot proves the goroutines (and their cancel func) were installed.
		ctx := p.snapshotThresholdsCtx()
		require.NotNil(t, ctx)

		// Wait a bit to ensure goroutines are running
		time.Sleep(100 * time.Millisecond)

		// Stop tears the goroutines down and cancels the context.
		p.StopDynamicThresholds()

		// Wait for goroutines to stop
		time.Sleep(100 * time.Millisecond)

		// Verify the captured context is done
		select {
		case <-ctx.Done():
			// Context is properly cancelled
		default:
			assert.Fail(t, "Context should be cancelled")
		}
	})
}

// TestStopDynamicThresholds_ConcurrentNoRace verifies that p.thresholdsCtx is never
// accessed without synchronization. The feature can be toggled off at runtime via the
// settings UI, so StopDynamicThresholds can be invoked concurrently; the nil-check read
// must not race with the locked nil-assignment. Run with -race to catch regressions.
func TestStopDynamicThresholds_ConcurrentNoRace(t *testing.T) {
	p := createTestProcessor()
	// Start launches the persistence/cleanup goroutines and sets thresholdsCtx. No
	// in-memory thresholds, so the concurrent Stop flush path performs no datastore
	// calls (keeping the unsynchronized MockDatastore fields out of the race window).
	p.StartDynamicThresholds()

	const goroutines = 50
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range goroutines {
		wg.Go(func() {
			<-start
			p.StopDynamicThresholds()
		})
	}
	close(start) // release all goroutines simultaneously
	wg.Wait()
}

// TestLoadWithDatabaseErrors tests error handling during load
func TestLoadWithDatabaseErrors(t *testing.T) {
	t.Run("HandleNoSuchTableError", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)
		mockDs.getAllError = NoSuchTableError{TableName: "dynamic_thresholds"}

		err := p.loadDynamicThresholdsFromDB()

		// Should not return error for "no such table" (normal on first run)
		require.NoError(t, err)
		assert.Empty(t, p.DynamicThresholds)
	})
}

// NoSuchTableError is a mock error type for testing
type NoSuchTableError struct {
	TableName string
}

func (e NoSuchTableError) Error() string {
	return "no such table: " + e.TableName
}

// TestBatchSaveWithBaseThreshold verifies the in-memory BaseThreshold (the model
// base recorded at last learn) round-trips through persistence, and that the saved
// CurrentValue is derived from it and the level.
func TestBatchSaveWithBaseThreshold(t *testing.T) {
	t.Run("PreserveBaseThreshold", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		now := time.Now()
		p.DynamicThresholds["american crow"] = &DynamicThreshold{
			Level:         1,
			BaseThreshold: 0.65,
			Timer:         now.Add(24 * time.Hour),
		}

		err := p.persistDynamicThresholds(t.Context())

		require.NoError(t, err)

		// Verify base threshold round-trips and CurrentValue is derived (75% of 0.65).
		savedThreshold := mockDs.thresholds["american crow"]
		require.NotNil(t, savedThreshold)
		assert.InDelta(t, 0.65, savedThreshold.BaseThreshold, 0.001, "BaseThreshold should round-trip")
		assert.InDelta(t, 0.65*0.75, savedThreshold.CurrentValue, 0.001, "CurrentValue derived from base and level")
	})
}

// TestDrainPendingResetsRequeuesOnFailure tests that failed DB deletes during
// drainPendingResets are requeued into pendingResets for the next cycle.
func TestDrainPendingResetsRequeuesOnFailure(t *testing.T) {
	t.Run("PerSpecies_FailedDeleteRequeues", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		// Set up per-species delete error for "blue jay" only
		dbErr := errors.Newf("database locked").Build()
		mockDs.deletePerSpeciesErrors = map[string]error{
			"blue jay": dbErr,
		}

		// Queue two species for reset
		p.pendingResets["american crow"] = struct{}{}
		p.pendingResets["blue jay"] = struct{}{}

		p.drainPendingResets()

		// "american crow" should have been deleted successfully (not requeued)
		assert.NotContains(t, p.pendingResets, "american crow",
			"Successfully deleted species should not be requeued")

		// "blue jay" should be requeued because its delete failed
		assert.Contains(t, p.pendingResets, "blue jay",
			"Failed species should be requeued for retry")

		// Both delete paths must be invoked for each species
		assert.Equal(t, 2, mockDs.deleteDynamicThresholdCalls,
			"DeleteDynamicThreshold should be called once per species")
		assert.Equal(t, 2, mockDs.deleteThresholdEventsCalls,
			"DeleteThresholdEvents should be called once per species")
	})

	t.Run("PerSpecies_EventsDeleteFailureAlsoRequeues", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		// Threshold delete succeeds, but events delete fails
		dbErr := errors.Newf("database locked").Build()
		mockDs.deleteEventsPerSpeciesErrors = map[string]error{
			"blue jay": dbErr,
		}

		p.pendingResets["blue jay"] = struct{}{}

		p.drainPendingResets()

		// "blue jay" should be requeued because events delete failed
		assert.Contains(t, p.pendingResets, "blue jay",
			"Species with failed events delete should be requeued")

		// Both delete paths must be invoked even when only events fail
		assert.Equal(t, 1, mockDs.deleteDynamicThresholdCalls,
			"DeleteDynamicThreshold should be called even when events fail")
		assert.Equal(t, 1, mockDs.deleteThresholdEventsCalls,
			"DeleteThresholdEvents should be called even when threshold delete succeeds")
	})

	t.Run("PerSpecies_AllSucceed_NothingRequeued", func(t *testing.T) {
		p := createTestProcessor()

		// Queue species for reset, no errors configured
		p.pendingResets["american crow"] = struct{}{}
		p.pendingResets["blue jay"] = struct{}{}

		p.drainPendingResets()

		assert.Empty(t, p.pendingResets,
			"No species should be requeued when all deletes succeed")
	})

	t.Run("ResetAll_FailedDeleteRequeues", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		// Make delete-all-thresholds fail
		dbErr := errors.Newf("database locked").Build()
		mockDs.deleteAllThresholdsError = dbErr

		p.pendingResetAll = true

		p.drainPendingResets()

		// pendingResetAll should be re-set because the delete-all failed
		assert.True(t, p.pendingResetAll,
			"pendingResetAll should be requeued when delete-all fails")

		// Both delete-all paths must be invoked even when thresholds fail
		assert.Equal(t, 1, mockDs.deleteAllThresholdsCalls,
			"DeleteAllDynamicThresholds should be called")
		assert.Equal(t, 1, mockDs.deleteAllEventsCalls,
			"DeleteAllThresholdEvents should be called even when thresholds delete fails")
	})

	t.Run("ResetAll_EventsDeleteFailRequeues", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		// Thresholds delete succeeds, events delete fails
		dbErr := errors.Newf("database locked").Build()
		mockDs.deleteAllEventsError = dbErr

		p.pendingResetAll = true

		p.drainPendingResets()

		// pendingResetAll should be re-set because the events delete-all failed
		assert.True(t, p.pendingResetAll,
			"pendingResetAll should be requeued when events delete-all fails")

		// Both delete-all paths must be invoked even when only events fail
		assert.Equal(t, 1, mockDs.deleteAllThresholdsCalls,
			"DeleteAllDynamicThresholds should be called even when events fail")
		assert.Equal(t, 1, mockDs.deleteAllEventsCalls,
			"DeleteAllThresholdEvents should be called even when thresholds delete succeeds")
	})

	t.Run("ResetAll_AllSucceed_NotRequeued", func(t *testing.T) {
		p := createTestProcessor()

		p.pendingResetAll = true

		p.drainPendingResets()

		assert.False(t, p.pendingResetAll,
			"pendingResetAll should not be requeued when all deletes succeed")
		assert.Empty(t, p.pendingResets,
			"pendingResets should remain empty after successful reset-all")
	})
}

// TestConvertThresholdsForPersistence_PreservesTimestamps verifies the #4195 timestamp
// fix at the processor layer: convertThresholdsForPersistence copies the real per-entry
// FirstCreated/LastTriggered from the in-memory struct instead of stamping the flush time,
// falls back to now for zero values, and skips empty-species keys without aborting.
func TestConvertThresholdsForPersistence_PreservesTimestamps(t *testing.T) {
	t.Parallel()
	p := createTestProcessor()
	settings := p.currentSettings()

	firstCreated := time.Now().Add(-72 * time.Hour)
	lastTriggered := time.Now().Add(-90 * time.Minute)
	future := time.Now().Add(24 * time.Hour)

	p.DynamicThresholds["american crow"] = &DynamicThreshold{
		Level:          2,
		BaseThreshold:  0.7,
		Timer:          future,
		HighConfCount:  3,
		ValidHours:     48,
		ScientificName: "Corvus brachyrhynchos",
		FirstCreated:   firstCreated,
		LastTriggered:  lastTriggered,
	}
	// An empty-key entry must not be persisted, must not abort the batch, and must be
	// routed into the eviction path so it cannot linger in memory forever.
	p.DynamicThresholds[""] = &DynamicThreshold{
		Level: 1, Timer: future, ValidHours: 48, FirstCreated: firstCreated, LastTriggered: lastTriggered,
	}

	dbThresholds, expired := p.convertThresholdsForPersistence(settings)

	require.Len(t, dbThresholds, 1, "empty-key entry must not be persisted")
	assert.Equal(t, []string{""}, expired, "empty-key entry must be routed to eviction")
	got := dbThresholds[0]
	assert.Equal(t, "american crow", got.SpeciesName)
	// The flush time is now; the real values are hours in the past, so a regression that
	// stamped now (the #4195 bug) would fail these tight bounds.
	assert.WithinDuration(t, firstCreated, got.FirstCreated, time.Second, "FirstCreated must be the real per-entry value, not the flush time")
	assert.WithinDuration(t, lastTriggered, got.LastTriggered, time.Second, "LastTriggered must be the real per-entry value, not the flush time")
}

// TestConvertThresholdsForPersistence_ZeroTimestampFallback verifies that an in-memory
// entry with zero FirstCreated/LastTriggered falls back to now (never persisted as zero).
func TestConvertThresholdsForPersistence_ZeroTimestampFallback(t *testing.T) {
	t.Parallel()
	p := createTestProcessor()
	p.DynamicThresholds["blue jay"] = &DynamicThreshold{
		Level: 1, BaseThreshold: 0.7, Timer: time.Now().Add(24 * time.Hour), ValidHours: 48,
	}

	dbThresholds, _ := p.convertThresholdsForPersistence(p.currentSettings())
	require.Len(t, dbThresholds, 1)
	got := dbThresholds[0]
	assert.False(t, got.FirstCreated.IsZero(), "zero FirstCreated must fall back to now")
	assert.WithinDuration(t, time.Now(), got.FirstCreated, 5*time.Second)
	assert.Equal(t, got.FirstCreated, got.LastTriggered, "zero LastTriggered falls back to FirstCreated")
}

// TestCleanUpDynamicThresholds_PrunesMemoryWithoutTouchingDatabase verifies the
// flusher-tick cleanup prunes stale in-memory entries and never calls the database, so a
// slow database cannot delay the flusher.
func TestCleanUpDynamicThresholds_PrunesMemoryWithoutTouchingDatabase(t *testing.T) {
	p := createTestProcessor()
	mockDs, ok := p.Ds.(*MockDatastore)
	require.True(t, ok)

	p.DynamicThresholds["fresh"] = &DynamicThreshold{Timer: time.Now().Add(time.Hour)}
	p.DynamicThresholds["stale"] = &DynamicThreshold{Timer: time.Now().AddDate(-10, 0, 0)}
	for range 5 {
		p.cleanUpDynamicThresholds()
	}

	p.thresholdsMutex.RLock()
	_, staleKept := p.DynamicThresholds["stale"]
	_, freshKept := p.DynamicThresholds["fresh"]
	p.thresholdsMutex.RUnlock()

	assert.False(t, staleKept, "stale in-memory entry should be pruned")
	assert.True(t, freshKept, "fresh in-memory entry should be kept")
	assert.Zero(t, mockDs.deleteExpiredCalls.Load(), "the flusher tick must not touch the database")
}

// TestPersistDynamicThresholds_DeletesExpiredRowsOnlyWhenDroppingAnEntry verifies the
// persistence tick deletes the expired rows once when it drops an expired entry from
// memory, and stays off the delete path while nothing expires.
func TestPersistDynamicThresholds_DeletesExpiredRowsOnlyWhenDroppingAnEntry(t *testing.T) {
	p := createTestProcessor()
	mockDs, ok := p.Ds.(*MockDatastore)
	require.True(t, ok)

	p.DynamicThresholds["fresh"] = &DynamicThreshold{
		Level: 1, BaseThreshold: 0.7, Timer: time.Now().Add(time.Hour), ValidHours: 24,
	}
	for range 5 {
		require.NoError(t, p.persistDynamicThresholds(t.Context()))
	}
	assert.Zero(t, mockDs.deleteExpiredCalls.Load(), "an idle persistence tick must not delete")

	// Expired by ExpiresAt but not yet stale for the flusher prune.
	p.DynamicThresholds["expired"] = &DynamicThreshold{
		Level: 1, BaseThreshold: 0.7, Timer: time.Now().Add(-time.Minute), ValidHours: 24,
	}
	for range 5 {
		require.NoError(t, p.persistDynamicThresholds(t.Context()))
	}

	p.thresholdsMutex.RLock()
	_, expiredKept := p.DynamicThresholds["expired"]
	_, freshKept := p.DynamicThresholds["fresh"]
	p.thresholdsMutex.RUnlock()

	assert.False(t, expiredKept, "expired entry should be dropped from memory")
	assert.True(t, freshKept, "fresh entry should be kept")
	assert.EqualValues(t, 1, mockDs.deleteExpiredCalls.Load(), "dropping an entry deletes the expired rows once")
}

// lockProbeDatastore records whether thresholdsMutex was held (for reading or writing)
// while DeleteExpiredDynamicThresholds ran.
type lockProbeDatastore struct {
	*MockDatastore
	p        *Processor
	lockHeld atomic.Bool
}

func (d *lockProbeDatastore) DeleteExpiredDynamicThresholds(before time.Time) (int64, error) {
	if d.p.thresholdsMutex.TryLock() {
		d.p.thresholdsMutex.Unlock()
	} else {
		d.lockHeld.Store(true)
	}
	return d.MockDatastore.DeleteExpiredDynamicThresholds(before)
}

// TestPersistDynamicThresholds_ExpiredRowDeleteRunsWithoutLock verifies the database
// delete after the persistence prune does not run under thresholdsMutex.
func TestPersistDynamicThresholds_ExpiredRowDeleteRunsWithoutLock(t *testing.T) {
	p := createTestProcessor()
	probe := &lockProbeDatastore{MockDatastore: p.Ds.(*MockDatastore), p: p}
	p.Ds = probe

	p.DynamicThresholds["expired"] = &DynamicThreshold{
		Level: 1, BaseThreshold: 0.7, Timer: time.Now().Add(-time.Minute), ValidHours: 24,
	}
	require.NoError(t, p.persistDynamicThresholds(t.Context()))

	assert.EqualValues(t, 1, probe.deleteExpiredCalls.Load(), "the expired rows should be deleted")
	assert.False(t, probe.lockHeld.Load(), "the database delete must not run under thresholdsMutex")
}

// TestPersistDynamicThresholds_DropsExpiredEntryWithoutDatastore verifies the persistence
// prune still drops an expired entry, and does not panic, when no datastore is set.
func TestPersistDynamicThresholds_DropsExpiredEntryWithoutDatastore(t *testing.T) {
	p := createTestProcessor()
	p.Ds = nil
	p.DynamicThresholds["expired"] = &DynamicThreshold{
		Level: 1, BaseThreshold: 0.7, Timer: time.Now().Add(-time.Minute), ValidHours: 24,
	}

	p.cleanupExpiredThresholds([]string{"expired"})

	p.thresholdsMutex.RLock()
	defer p.thresholdsMutex.RUnlock()
	assert.Empty(t, p.DynamicThresholds, "expired entry should be dropped from memory")
}

// TestThresholdCleanup_SkipsStartCleanupWhenAlreadyCancelled verifies a lifecycle that was
// stopped before the cleanup goroutine ran does not delete anything.
func TestThresholdCleanup_SkipsStartCleanupWhenAlreadyCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := createTestProcessor()
		mockDs, ok := p.Ds.(*MockDatastore)
		require.True(t, ok)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		p.startThresholdCleanup(ctx)
		synctest.Wait()

		assert.Zero(t, mockDs.deleteExpiredCalls.Load(), "a cancelled lifecycle must not clean up")
	})
}

// TestThresholdCleanup_RunsAtStartThenDailyAndFollowsToggle verifies the database
// cleanup runs once when the goroutine starts, then once per DefaultCleanupInterval,
// and starts and stops with the runtime enable/disable of dynamic thresholds.
func TestThresholdCleanup_RunsAtStartThenDailyAndFollowsToggle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := createTestProcessor()
		mockDs, ok := p.Ds.(*MockDatastore)
		require.True(t, ok)

		p.StartDynamicThresholds()
		synctest.Wait()
		assert.EqualValues(t, 1, mockDs.deleteExpiredCalls.Load(), "cleanup should run once at start")

		time.Sleep(DefaultCleanupInterval - time.Minute)
		synctest.Wait()
		assert.EqualValues(t, 1, mockDs.deleteExpiredCalls.Load(), "no cleanup before the interval elapses")

		time.Sleep(2 * time.Minute)
		synctest.Wait()
		assert.EqualValues(t, 2, mockDs.deleteExpiredCalls.Load(), "cleanup should run after the interval")

		p.StopDynamicThresholds()
		synctest.Wait()
		time.Sleep(DefaultCleanupInterval + time.Minute)
		synctest.Wait()
		assert.EqualValues(t, 2, mockDs.deleteExpiredCalls.Load(), "no cleanup while disabled")

		p.StartDynamicThresholds()
		synctest.Wait()
		assert.EqualValues(t, 3, mockDs.deleteExpiredCalls.Load(), "re-enable should clean immediately")

		p.StopDynamicThresholds()
		synctest.Wait()
	})
}

const (
	// stallTestTimeout bounds each call that must not wait for a hung reset.
	stallTestTimeout = 10 * time.Second
	// stallTestShutdownDeadline is the shutdown context of the stall test.
	stallTestShutdownDeadline = 100 * time.Millisecond
	// resetTestTrigger is the dynamic threshold trigger of the reset tests; the
	// test confidence is above it.
	resetTestTrigger = 0.5
	// resetTestConfidence is the confidence of the approved detection a reset test learns from.
	resetTestConfidence = 0.95
)

// enableResetTestThresholds turns on dynamic thresholds with a trigger and an
// expiry window on the processor's own settings.
func enableResetTestThresholds(p *Processor) {
	p.Settings.Realtime.DynamicThreshold.Enabled = true
	p.Settings.Realtime.DynamicThreshold.Trigger = resetTestTrigger
	p.Settings.Realtime.DynamicThreshold.ValidHours = persistTestValidHours
}

// requireReturnsWithin fails the test unless fn returns within stallTestTimeout.
func requireReturnsWithin(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(stallTestTimeout):
		require.Failf(t, "call stalled behind a reset", "%s did not return within %s", what, stallTestTimeout)
	}
}

// TestResetDynamicThresholds_BlockedDeleteDoesNotStallDetectionOrShutdown pins
// that no database delete of a reset runs under thresholdsMutex: while a delete
// hangs, the detection filter, learning, the API read and shutdown all return.
//
// Real time, not synctest: on the old code the waiters block on a sync mutex,
// which synctest does not treat as durably blocked. Not parallel: the settings
// come from the processor, and currentSettings() prefers the global snapshot.
func TestResetDynamicThresholds_BlockedDeleteDoesNotStallDetectionOrShutdown(t *testing.T) {
	tests := []struct {
		name  string
		op    string
		reset func(p *Processor) error
	}{
		{"single reset hung in threshold delete", opDeleteThreshold, func(p *Processor) error {
			return p.ResetDynamicThreshold(persistTestSpeciesReset)
		}},
		{"single reset hung in events delete", opDeleteEvents, func(p *Processor) error {
			return p.ResetDynamicThreshold(persistTestSpeciesReset)
		}},
		{"reset-all hung in threshold delete", opDeleteAllThresholds, func(p *Processor) error {
			_, err := p.ResetAllDynamicThresholds()
			return err
		}},
		{"reset-all hung in events delete", opDeleteAllEvents, func(p *Processor) error {
			_, err := p.ResetAllDynamicThresholds()
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, store := newConcurrentThresholdProcessor()
			p.JobQueue = jobqueue.NewJobQueue() // not started: stopping it returns at once
			enableResetTestThresholds(p)

			entered := make(chan struct{})
			release := make(chan struct{})
			releaseDelete := sync.OnceFunc(func() { close(release) })
			store.deleteHook = func(op string) {
				if op == tt.op {
					close(entered)
					<-release
				}
			}

			resetDone := make(chan error, 1)
			// Registered first so it runs last: release the delete, then wait for the reset.
			defer func() { assert.NoError(t, <-resetDone) }()
			defer releaseDelete()
			go func() { resetDone <- tt.reset(p) }()

			select {
			case <-entered:
			case <-time.After(stallTestTimeout):
				require.Fail(t, "the reset never reached the blocked delete")
			}

			requireReturnsWithin(t, "getAdjustedConfidenceThreshold", func() {
				p.getAdjustedConfidenceThreshold(persistTestSpeciesKept, persistTestBaseValue, false)
			})
			requireReturnsWithin(t, "LearnFromApprovedDetection", func() {
				p.LearnFromApprovedDetection(persistTestSpeciesReset, "", resetTestConfidence, persistTestBaseValue)
			})
			requireReturnsWithin(t, "GetDynamicThresholdData", func() {
				_ = p.GetDynamicThresholdData()
			})

			ctx, cancel := context.WithTimeout(t.Context(), stallTestShutdownDeadline)
			defer cancel()
			requireReturnsWithin(t, "ShutdownWithContext", func() {
				_ = p.ShutdownWithContext(ctx)
			})

			releaseDelete()
			select {
			case err := <-resetDone:
				resetDone <- err // the deferred wait receives it again
				require.NoError(t, err)
			case <-time.After(stallTestTimeout):
				require.Fail(t, "the reset did not return after the delete was released")
			}
		})
	}
}

// resetTestOtherSpecies is a species with no threshold, learned during a reset.
const resetTestOtherSpecies = "northern cardinal"

// TestResetDynamicThresholds_RelearnDuringDeleteKeepsRowAndEvent pins that a
// species re-learned while a reset's deletes are running keeps its event (saved
// only after the deletes return) and its row (written by a persistence cycle that
// waits for the reset). Rows block the reset in the row delete, where a cycle
// that ignored the reset would write the re-learned row just before it is
// deleted, and in the events delete. A single reset holds back only its own
// species' events; reset-all holds back every species'. Not parallel: the
// settings come from the processor.
func TestResetDynamicThresholds_RelearnDuringDeleteKeepsRowAndEvent(t *testing.T) {
	resetOne := func(p *Processor) error { return p.ResetDynamicThreshold(persistTestSpeciesReset) }
	resetAll := func(p *Processor) error { _, err := p.ResetAllDynamicThresholds(); return err }
	tests := []struct {
		name    string
		blockOp string
		reset   func(p *Processor) error
		all     bool
	}{
		{"single reset, row delete", opDeleteThreshold, resetOne, false},
		{"single reset, events delete", opDeleteEvents, resetOne, false},
		{"reset-all, row delete", opDeleteAllThresholds, resetAll, true},
		{"reset-all, events delete", opDeleteAllEvents, resetAll, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p, store := newConcurrentThresholdProcessor()
				enableResetTestThresholds(p)

				release := make(chan struct{})
				releaseDelete := sync.OnceFunc(func() { close(release) })
				defer releaseDelete()
				store.deleteHook = func(op string) {
					if op == tt.blockOp {
						<-release
					}
				}

				resetDone := make(chan error, 1)
				go func() { resetDone <- tt.reset(p) }()
				synctest.Wait() // the reset is blocked in its delete

				p.LearnFromApprovedDetection(persistTestSpeciesReset, "", resetTestConfidence, persistTestBaseValue)
				p.LearnFromApprovedDetection(resetTestOtherSpecies, "", resetTestConfidence, persistTestBaseValue)
				cycleDone := make(chan error, 1)
				go func() { cycleDone <- p.persistDynamicThresholds(t.Context()) }()
				synctest.Wait() // the cycle waits for the reset

				assert.Empty(t, store.eventsFor(persistTestSpeciesReset),
					"the event save must wait until the reset's deletes return")
				if tt.all {
					assert.Empty(t, store.eventsFor(resetTestOtherSpecies),
						"reset-all holds back every species' events")
				} else {
					assert.Len(t, store.eventsFor(resetTestOtherSpecies), 1,
						"a single reset must not hold back another species' event")
				}

				releaseDelete()
				require.NoError(t, <-resetDone)
				require.NoError(t, <-cycleDone)
				synctest.Wait()

				events := store.eventsFor(persistTestSpeciesReset)
				require.Len(t, events, 1, "the event recorded after the reset must survive it")
				assert.Equal(t, 1, events[0].NewLevel)
				assert.Len(t, store.eventsFor(resetTestOtherSpecies), 1)

				row, ok := store.hasRow(persistTestSpeciesReset)
				require.True(t, ok, "the re-learned threshold must be persisted")
				assert.Equal(t, 1, row.Level)
				if tt.all {
					_, kept := store.hasRow(persistTestSpeciesKept)
					assert.False(t, kept, "reset-all must remove the other species' row")
				}
				assert.Empty(t, p.pendingResets)
				assert.False(t, p.pendingResetAll)
			})
		})
	}
}

// TestResetDynamicThresholds_NilDatastoreClearsMemoryOnly pins that without a
// datastore a reset clears memory, sets no barrier and queues no retry.
func TestResetDynamicThresholds_NilDatastoreClearsMemoryOnly(t *testing.T) {
	tests := []struct {
		name           string
		reset          func(p *Processor)
		wantKeptRetry  bool
		wantMemoryKept bool
	}{
		{"single reset", func(p *Processor) { _ = p.ResetDynamicThreshold(persistTestSpeciesReset) }, true, true},
		{"reset-all", func(p *Processor) { _, _ = p.ResetAllDynamicThresholds() }, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := createTestProcessor()
			p.Ds = nil
			p.DynamicThresholds[persistTestSpeciesReset] = &DynamicThreshold{Level: persistTestLevel}
			p.DynamicThresholds[persistTestSpeciesKept] = &DynamicThreshold{Level: persistTestLevel}
			p.pendingResets[persistTestSpeciesKept] = struct{}{}

			require.NotPanics(t, func() { tt.reset(p) })

			assert.NotContains(t, p.DynamicThresholds, persistTestSpeciesReset)
			assert.Equal(t, tt.wantMemoryKept, p.DynamicThresholds[persistTestSpeciesKept] != nil)
			_, retry := p.pendingResets[persistTestSpeciesKept]
			assert.Equal(t, tt.wantKeptRetry, retry)
			assert.NotContains(t, p.pendingResets, persistTestSpeciesReset)
			assert.False(t, p.pendingResetAll)
			assert.Nil(t, p.thresholdResetBarrier)
		})
	}
}

// TestResetDynamicThresholds_PanicInDeleteQueuesRetry pins that a datastore call
// that panics inside a reset still queues the retry, clears the reset barrier and
// lets later persistence cycles run. Not parallel: the settings come from the processor.
func TestResetDynamicThresholds_PanicInDeleteQueuesRetry(t *testing.T) {
	tests := []struct {
		name    string
		panicOp string
		reset   func(p *Processor)
		queued  func(p *Processor) bool
	}{
		{"single reset, threshold delete panics", opDeleteThreshold,
			func(p *Processor) { _ = p.ResetDynamicThreshold(persistTestSpeciesReset) },
			func(p *Processor) bool { _, ok := p.pendingResets[persistTestSpeciesReset]; return ok }},
		{"single reset, events delete panics", opDeleteEvents,
			func(p *Processor) { _ = p.ResetDynamicThreshold(persistTestSpeciesReset) },
			func(p *Processor) bool { _, ok := p.pendingResets[persistTestSpeciesReset]; return ok }},
		{"reset-all, threshold delete panics", opDeleteAllThresholds,
			func(p *Processor) { _, _ = p.ResetAllDynamicThresholds() },
			func(p *Processor) bool { return p.pendingResetAll }},
		{"reset-all, events delete panics", opDeleteAllEvents,
			func(p *Processor) { _, _ = p.ResetAllDynamicThresholds() },
			func(p *Processor) bool { return p.pendingResetAll }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, store := newConcurrentThresholdProcessor()
			enableResetTestThresholds(p)
			var barrier *thresholdResetBarrier
			store.deleteHook = func(op string) {
				if op == tt.panicOp {
					p.thresholdsMutex.RLock()
					barrier = p.thresholdResetBarrier
					p.thresholdsMutex.RUnlock()
					panic("simulated datastore panic")
				}
			}

			require.Panics(t, func() { tt.reset(p) })
			require.NotNil(t, barrier, "the reset must have published its barrier before the delete")
			select {
			case <-barrier.done:
			default:
				assert.Fail(t, "the barrier channel must be closed so held-back event saves are released")
			}

			p.thresholdsMutex.RLock()
			remaining := p.thresholdResetBarrier
			queued := tt.queued(p)
			p.thresholdsMutex.RUnlock()
			assert.Nil(t, remaining, "the reset barrier must be cleared")
			assert.True(t, queued, "a reset that did not finish its deletes must queue a retry")

			// The persistence lock was released: a later cycle runs and drains the retry.
			store.deleteHook = nil
			require.NoError(t, p.persistDynamicThresholds(t.Context()))
			p.thresholdsMutex.RLock()
			defer p.thresholdsMutex.RUnlock()
			assert.Empty(t, p.pendingResets)
			assert.False(t, p.pendingResetAll)
		})
	}
}
