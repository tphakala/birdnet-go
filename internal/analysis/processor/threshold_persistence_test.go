// threshold_persistence_test.go: Unit tests for dynamic threshold persistence functionality
package processor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
)

// MockDatastore implements datastore.Interface for testing
type MockDatastore struct {
	thresholds          map[string]*datastore.DynamicThreshold
	getAllError         error
	saveError           error
	batchSaveError      error
	deleteError         error
	deleteExpiredError  error
	updateExpiryError   error
	deletedCount        int64
	batchSaveCalled     bool
	deleteExpiredCalled bool
	getAllCalled        bool
}

// Implement all required methods from datastore.Interface

func (m *MockDatastore) Open() error                                     { return nil }
func (m *MockDatastore) Close() error                                    { return nil }
func (m *MockDatastore) Save(*datastore.Note, []datastore.Results) error { return nil }
func (m *MockDatastore) Delete(string) error                             { return nil }
func (m *MockDatastore) Get(string) (datastore.Note, error)              { return datastore.Note{}, nil }
func (m *MockDatastore) SetMetrics(*datastore.Metrics)                   {}
func (m *MockDatastore) SetSunCalcMetrics(any)                           {}
func (m *MockDatastore) Optimize(context.Context) error                  { return nil }
func (m *MockDatastore) GetAllNotes() ([]datastore.Note, error) {
	return make([]datastore.Note, 0), nil
}
func (m *MockDatastore) GetTopBirdsData(string, float64) ([]datastore.Note, error) {
	return make([]datastore.Note, 0), nil
}
func (m *MockDatastore) GetHourlyOccurrences(string, string, float64) ([24]int, error) {
	return [24]int{}, nil
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
func (m *MockDatastore) SearchNotes(string, bool, int, int) ([]datastore.Note, error) {
	return make([]datastore.Note, 0), nil
}
func (m *MockDatastore) SearchNotesAdvanced(*datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
	return make([]datastore.Note, 0), 0, nil
}
func (m *MockDatastore) GetNoteClipPath(string) (string, error) {
	return "", datastore.ErrNoteReviewNotFound
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
func (m *MockDatastore) CountSearchResults(string) (int64, error) { return 0, nil }
func (m *MockDatastore) Transaction(func(*gorm.DB) error) error   { return nil }
func (m *MockDatastore) LockNote(string) error                    { return nil }
func (m *MockDatastore) UnlockNote(string) error                  { return nil }
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
func (m *MockDatastore) GetLockedNotesClipPaths() ([]string, error)               { return make([]string, 0), nil }
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
	if m.deleteError != nil {
		return m.deleteError
	}
	delete(m.thresholds, speciesName)
	return nil
}

func (m *MockDatastore) DeleteExpiredDynamicThresholds(before time.Time) (int64, error) {
	m.deleteExpiredCalled = true
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

func (m *MockDatastore) DeleteThresholdEvents(string) error {
	return nil
}

func (m *MockDatastore) DeleteAllThresholdEvents() (int64, error) {
	return 0, nil
}

// BG-17 fix: Add notification history methods
func (m *MockDatastore) GetActiveNotificationHistory(after time.Time) ([]datastore.NotificationHistory, error) {
	return []datastore.NotificationHistory{}, nil
}

func (m *MockDatastore) GetNotificationHistory(scientificName, notificationType string) (*datastore.NotificationHistory, error) {
	return nil, errors.Newf("notification history not found").
		Component("datastore").
		Category(errors.CategoryNotFound).
		Build()
}

func (m *MockDatastore) SaveNotificationHistory(history *datastore.NotificationHistory) error {
	return nil
}

func (m *MockDatastore) DeleteExpiredNotificationHistory(before time.Time) (int64, error) {
	return 0, nil
}

func (m *MockDatastore) GetDatabaseStats() (*datastore.DatabaseStats, error) {
	return &datastore.DatabaseStats{
		Type:      "mock",
		Connected: true,
	}, nil
}

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
	}

	return p
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

		// Pre-populate mock database with valid thresholds
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

		// Verify american crow threshold
		crowThreshold := p.DynamicThresholds["american crow"]
		require.NotNil(t, crowThreshold)
		assert.Equal(t, 1, crowThreshold.Level)
		assert.InDelta(t, 0.75, crowThreshold.CurrentValue, 0.001)
		assert.Equal(t, 5, crowThreshold.HighConfCount)
		assert.Equal(t, 48, crowThreshold.ValidHours)

		// Verify blue jay threshold
		jayThreshold := p.DynamicThresholds["blue jay"]
		require.NotNil(t, jayThreshold)
		assert.Equal(t, 2, jayThreshold.Level)
		assert.InDelta(t, 0.8, jayThreshold.CurrentValue, 0.001)
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
		// Add thresholds to in-memory map
		p.DynamicThresholds["american crow"] = &DynamicThreshold{
			Level:         1,
			CurrentValue:  0.75,
			Timer:         now.Add(24 * time.Hour),
			HighConfCount: 5,
			ValidHours:    48,
		}
		p.DynamicThresholds["blue jay"] = &DynamicThreshold{
			Level:         2,
			CurrentValue:  0.8,
			Timer:         now.Add(48 * time.Hour),
			HighConfCount: 10,
			ValidHours:    48,
		}

		err := p.persistDynamicThresholds()

		require.NoError(t, err)
		assert.True(t, mockDs.batchSaveCalled)
		assert.Len(t, mockDs.thresholds, 2)

		// Verify saved thresholds
		savedCrow := mockDs.thresholds["american crow"]
		require.NotNil(t, savedCrow)
		assert.Equal(t, 1, savedCrow.Level)
		assert.InDelta(t, 0.75, savedCrow.CurrentValue, 0.001)
		assert.Equal(t, 5, savedCrow.HighConfCount)
	})

	t.Run("EmptyThresholdsMap", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		err := p.persistDynamicThresholds()

		require.NoError(t, err)
		assert.False(t, mockDs.batchSaveCalled, "Should not call batch save with no thresholds")
	})

	t.Run("RemoveExpiredFromMemory", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		now := time.Now()
		p.DynamicThresholds["american crow"] = &DynamicThreshold{
			Level:        1,
			CurrentValue: 0.75,
			Timer:        now.Add(24 * time.Hour), // Valid
		}
		p.DynamicThresholds["blue jay"] = &DynamicThreshold{
			Level:        2,
			CurrentValue: 0.8,
			Timer:        now.Add(-1 * time.Hour), // Expired
		}

		err := p.persistDynamicThresholds()

		require.NoError(t, err)
		assert.Len(t, p.DynamicThresholds, 1, "Expired threshold should be removed from memory")
		assert.Contains(t, p.DynamicThresholds, "american crow")
		assert.NotContains(t, p.DynamicThresholds, "blue jay")

		// Only valid threshold should be saved to database
		assert.Len(t, mockDs.thresholds, 1)
		assert.Contains(t, mockDs.thresholds, "american crow")
	})
}

// TestFlushDynamicThresholds tests immediate flush operation
func TestFlushDynamicThresholds(t *testing.T) {
	t.Run("SuccessfulFlush", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		now := time.Now()
		p.DynamicThresholds["american crow"] = &DynamicThreshold{
			Level:        1,
			CurrentValue: 0.75,
			Timer:        now.Add(24 * time.Hour),
		}

		err := p.FlushDynamicThresholds()

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

		// Start goroutines
		p.startThresholdPersistence()
		p.startThresholdCleanup()

		// Verify context was created
		assert.NotNil(t, p.thresholdsCtx)
		assert.NotNil(t, p.thresholdsCancel)

		// Wait a bit to ensure goroutines are running
		time.Sleep(100 * time.Millisecond)

		// Cancel the goroutines
		p.thresholdsCancel()

		// Wait for goroutines to stop
		time.Sleep(100 * time.Millisecond)

		// Verify context is done
		select {
		case <-p.thresholdsCtx.Done():
			// Context is properly cancelled
		default:
			assert.Fail(t, "Context should be cancelled")
		}
	})
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

// TestBatchSaveWithBaseThreshold tests that base threshold is preserved
func TestBatchSaveWithBaseThreshold(t *testing.T) {
	t.Run("PreserveBaseThreshold", func(t *testing.T) {
		p := createTestProcessor()
		mockDs := p.Ds.(*MockDatastore)

		// Set custom threshold in settings
		p.Settings.Realtime.Species.Config = map[string]conf.SpeciesConfig{
			"american crow": {
				Threshold: 0.65,
			},
		}

		now := time.Now()
		p.DynamicThresholds["american crow"] = &DynamicThreshold{
			Level:        1,
			CurrentValue: 0.75,
			Timer:        now.Add(24 * time.Hour),
		}

		err := p.persistDynamicThresholds()

		require.NoError(t, err)

		// Verify base threshold was calculated and saved
		savedThreshold := mockDs.thresholds["american crow"]
		require.NotNil(t, savedThreshold)
		assert.InDelta(t, 0.65, savedThreshold.BaseThreshold, 0.001, "Base threshold should match custom config")
	})
}
