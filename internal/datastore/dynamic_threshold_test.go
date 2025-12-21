// dynamic_threshold_test.go: Unit tests for dynamic threshold database operations
package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupDynamicThresholdTestDB creates an in-memory SQLite database for testing
func setupDynamicThresholdTestDB(t *testing.T) *DataStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err, "Failed to create test database")

	// Auto-migrate the schema
	err = db.AutoMigrate(&DynamicThreshold{})
	require.NoError(t, err, "Failed to migrate schema")

	return &DataStore{DB: db}
}

// TestSaveDynamicThreshold tests saving/updating a single threshold
func TestSaveDynamicThreshold(t *testing.T) {
	t.Run("SaveNewThreshold", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		threshold := &DynamicThreshold{
			SpeciesName:   "american crow",
			Level:         1,
			CurrentValue:  0.75,
			BaseThreshold: 0.7,
			HighConfCount: 5,
			ValidHours:    48,
			ExpiresAt:     time.Now().Add(48 * time.Hour),
			TriggerCount:  5,
		}

		err := ds.SaveDynamicThreshold(threshold)

		require.NoError(t, err)
		assert.NotZero(t, threshold.ID, "ID should be assigned after save")
		assert.NotZero(t, threshold.FirstCreated, "FirstCreated should be set")
		assert.NotZero(t, threshold.UpdatedAt, "UpdatedAt should be set")
	})

	t.Run("UpdateExistingThreshold", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		// Save initial threshold
		threshold := &DynamicThreshold{
			SpeciesName:   "blue jay",
			Level:         1,
			CurrentValue:  0.75,
			BaseThreshold: 0.7,
			HighConfCount: 5,
			ValidHours:    48,
			ExpiresAt:     time.Now().Add(48 * time.Hour),
		}

		err := ds.SaveDynamicThreshold(threshold)
		require.NoError(t, err)

		originalID := threshold.ID
		originalFirstCreated := threshold.FirstCreated

		// Update threshold
		threshold.Level = 2
		threshold.CurrentValue = 0.8
		threshold.HighConfCount = 10

		err = ds.SaveDynamicThreshold(threshold)

		require.NoError(t, err)
		assert.Equal(t, originalID, threshold.ID, "ID should remain the same")
		assert.Equal(t, originalFirstCreated, threshold.FirstCreated, "FirstCreated should not change")
		assert.Equal(t, 2, threshold.Level)
		assert.InDelta(t, 0.8, threshold.CurrentValue, 0.001)
		assert.Equal(t, 10, threshold.HighConfCount)
	})

	t.Run("RejectNilThreshold", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		err := ds.SaveDynamicThreshold(nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "threshold cannot be nil")
	})

	t.Run("RejectEmptySpeciesName", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		threshold := &DynamicThreshold{
			SpeciesName: "",
			Level:       1,
		}

		err := ds.SaveDynamicThreshold(threshold)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "species name cannot be empty")
	})
}

// TestGetDynamicThreshold tests retrieving a threshold
func TestGetDynamicThreshold(t *testing.T) {
	t.Run("GetExistingThreshold", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		// Save a threshold
		original := &DynamicThreshold{
			SpeciesName:   "cardinal",
			Level:         2,
			CurrentValue:  0.8,
			BaseThreshold: 0.7,
			HighConfCount: 8,
			ValidHours:    48,
			ExpiresAt:     time.Now().Add(48 * time.Hour),
		}
		err := ds.SaveDynamicThreshold(original)
		require.NoError(t, err)

		// Retrieve it
		retrieved, err := ds.GetDynamicThreshold("cardinal")

		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, "cardinal", retrieved.SpeciesName)
		assert.Equal(t, 2, retrieved.Level)
		assert.InDelta(t, 0.8, retrieved.CurrentValue, 0.001)
		assert.Equal(t, 8, retrieved.HighConfCount)
	})

	t.Run("GetNonExistentThreshold", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		threshold, err := ds.GetDynamicThreshold("nonexistent")

		require.Error(t, err)
		assert.Nil(t, threshold)
		// Error should either be gorm.ErrRecordNotFound or wrapped in our error type
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("RejectEmptySpeciesName", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		threshold, err := ds.GetDynamicThreshold("")

		require.Error(t, err)
		assert.Nil(t, threshold)
		assert.Contains(t, err.Error(), "species name cannot be empty")
	})
}

// TestGetAllDynamicThresholds tests bulk retrieval
func TestGetAllDynamicThresholds(t *testing.T) {
	t.Run("GetAllThresholds", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		// Save multiple thresholds
		species := []string{"american crow", "blue jay", "cardinal", "robin"}
		for i, name := range species {
			threshold := &DynamicThreshold{
				SpeciesName:   name,
				Level:         i + 1,
				CurrentValue:  0.7 + float64(i)*0.05,
				BaseThreshold: 0.7,
				HighConfCount: (i + 1) * 2,
				ValidHours:    48,
				ExpiresAt:     time.Now().Add(48 * time.Hour),
			}
			err := ds.SaveDynamicThreshold(threshold)
			require.NoError(t, err)
		}

		// Retrieve all
		thresholds, err := ds.GetAllDynamicThresholds()

		require.NoError(t, err)
		assert.Len(t, thresholds, 4)

		// Verify ordering (should be alphabetical by species name)
		assert.Equal(t, "american crow", thresholds[0].SpeciesName)
		assert.Equal(t, "blue jay", thresholds[1].SpeciesName)
		assert.Equal(t, "cardinal", thresholds[2].SpeciesName)
		assert.Equal(t, "robin", thresholds[3].SpeciesName)
	})

	t.Run("GetWithLimit", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		// Save 5 thresholds
		for i := range 5 {
			threshold := &DynamicThreshold{
				SpeciesName:   string(rune('a'+i)) + "-species",
				Level:         1,
				CurrentValue:  0.75,
				BaseThreshold: 0.7,
				ExpiresAt:     time.Now().Add(48 * time.Hour),
			}
			err := ds.SaveDynamicThreshold(threshold)
			require.NoError(t, err)
		}

		// Retrieve with limit
		thresholds, err := ds.GetAllDynamicThresholds(3)

		require.NoError(t, err)
		assert.Len(t, thresholds, 3)
	})

	t.Run("GetFromEmptyDatabase", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		thresholds, err := ds.GetAllDynamicThresholds()

		require.NoError(t, err)
		assert.Empty(t, thresholds)
	})
}

// TestDeleteDynamicThreshold tests deletion
func TestDeleteDynamicThreshold(t *testing.T) {
	t.Run("DeleteExistingThreshold", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		// Save a threshold
		threshold := &DynamicThreshold{
			SpeciesName:   "sparrow",
			Level:         1,
			CurrentValue:  0.75,
			BaseThreshold: 0.7,
			ExpiresAt:     time.Now().Add(48 * time.Hour),
		}
		err := ds.SaveDynamicThreshold(threshold)
		require.NoError(t, err)

		// Delete it
		err = ds.DeleteDynamicThreshold("sparrow")

		require.NoError(t, err)

		// Verify deletion
		retrieved, err := ds.GetDynamicThreshold("sparrow")
		require.Error(t, err)
		assert.Nil(t, retrieved)
	})

	t.Run("DeleteNonExistentThreshold", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		// Should not error even if doesn't exist
		err := ds.DeleteDynamicThreshold("nonexistent")

		require.NoError(t, err)
	})

	t.Run("RejectEmptySpeciesName", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		err := ds.DeleteDynamicThreshold("")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "species name cannot be empty")
	})
}

// TestDeleteExpiredDynamicThresholds tests cleanup of expired thresholds
func TestDeleteExpiredDynamicThresholds(t *testing.T) {
	t.Run("DeleteExpiredOnly", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		now := time.Now()

		// Save mix of expired and valid thresholds
		thresholds := []*DynamicThreshold{
			{
				SpeciesName:  "expired1",
				Level:        1,
				CurrentValue: 0.75,
				ExpiresAt:    now.Add(-2 * time.Hour), // Expired
			},
			{
				SpeciesName:  "expired2",
				Level:        1,
				CurrentValue: 0.75,
				ExpiresAt:    now.Add(-1 * time.Hour), // Expired
			},
			{
				SpeciesName:  "valid1",
				Level:        1,
				CurrentValue: 0.75,
				ExpiresAt:    now.Add(24 * time.Hour), // Valid
			},
			{
				SpeciesName:  "valid2",
				Level:        1,
				CurrentValue: 0.75,
				ExpiresAt:    now.Add(48 * time.Hour), // Valid
			},
		}

		for _, threshold := range thresholds {
			threshold.BaseThreshold = 0.7
			threshold.ValidHours = 48
			err := ds.SaveDynamicThreshold(threshold)
			require.NoError(t, err)
		}

		// Delete expired
		deletedCount, err := ds.DeleteExpiredDynamicThresholds(now)

		require.NoError(t, err)
		assert.Equal(t, int64(2), deletedCount)

		// Verify only valid ones remain
		remaining, err := ds.GetAllDynamicThresholds()
		require.NoError(t, err)
		assert.Len(t, remaining, 2)
		assert.Equal(t, "valid1", remaining[0].SpeciesName)
		assert.Equal(t, "valid2", remaining[1].SpeciesName)
	})

	t.Run("NoExpiredThresholds", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		now := time.Now()

		// Save only valid threshold
		threshold := &DynamicThreshold{
			SpeciesName:   "valid",
			Level:         1,
			CurrentValue:  0.75,
			BaseThreshold: 0.7,
			ExpiresAt:     now.Add(24 * time.Hour),
		}
		err := ds.SaveDynamicThreshold(threshold)
		require.NoError(t, err)

		// Try to delete expired
		deletedCount, err := ds.DeleteExpiredDynamicThresholds(now)

		require.NoError(t, err)
		assert.Equal(t, int64(0), deletedCount)

		// Verify threshold still exists
		remaining, err := ds.GetAllDynamicThresholds()
		require.NoError(t, err)
		assert.Len(t, remaining, 1)
	})
}

// TestUpdateDynamicThresholdExpiry tests expiry updates
func TestUpdateDynamicThresholdExpiry(t *testing.T) {
	t.Run("UpdateExistingThreshold", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		now := time.Now()
		originalExpiry := now.Add(24 * time.Hour)

		// Save threshold
		threshold := &DynamicThreshold{
			SpeciesName:   "chickadee",
			Level:         1,
			CurrentValue:  0.75,
			BaseThreshold: 0.7,
			ExpiresAt:     originalExpiry,
		}
		err := ds.SaveDynamicThreshold(threshold)
		require.NoError(t, err)

		// Update expiry
		newExpiry := now.Add(48 * time.Hour)
		err = ds.UpdateDynamicThresholdExpiry("chickadee", newExpiry)

		require.NoError(t, err)

		// Verify update
		retrieved, err := ds.GetDynamicThreshold("chickadee")
		require.NoError(t, err)
		assert.True(t, retrieved.ExpiresAt.After(originalExpiry))
		assert.WithinDuration(t, newExpiry, retrieved.ExpiresAt, time.Second)
	})

	t.Run("UpdateNonExistentThreshold", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		err := ds.UpdateDynamicThresholdExpiry("nonexistent", time.Now().Add(24*time.Hour))

		require.Error(t, err)
	})

	t.Run("RejectEmptySpeciesName", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		err := ds.UpdateDynamicThresholdExpiry("", time.Now())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "species name cannot be empty")
	})
}

// TestBatchSaveDynamicThresholds tests batch operations
func TestBatchSaveDynamicThresholds(t *testing.T) {
	t.Run("BatchSaveMultipleThresholds", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		now := time.Now()
		thresholds := []DynamicThreshold{
			{
				SpeciesName:   "crow",
				Level:         1,
				CurrentValue:  0.75,
				BaseThreshold: 0.7,
				HighConfCount: 5,
				ValidHours:    48,
				ExpiresAt:     now.Add(48 * time.Hour),
				TriggerCount:  5,
			},
			{
				SpeciesName:   "jay",
				Level:         2,
				CurrentValue:  0.8,
				BaseThreshold: 0.7,
				HighConfCount: 10,
				ValidHours:    48,
				ExpiresAt:     now.Add(48 * time.Hour),
				TriggerCount:  10,
			},
			{
				SpeciesName:   "sparrow",
				Level:         1,
				CurrentValue:  0.72,
				BaseThreshold: 0.7,
				HighConfCount: 3,
				ValidHours:    48,
				ExpiresAt:     now.Add(48 * time.Hour),
				TriggerCount:  3,
			},
		}

		err := ds.BatchSaveDynamicThresholds(thresholds)

		require.NoError(t, err)

		// Verify all were saved
		saved, err := ds.GetAllDynamicThresholds()
		require.NoError(t, err)
		assert.Len(t, saved, 3)
	})

	t.Run("BatchUpdateExisting", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		now := time.Now()

		// Initial save
		initial := []DynamicThreshold{
			{
				SpeciesName:   "robin",
				Level:         1,
				CurrentValue:  0.75,
				BaseThreshold: 0.7,
				ExpiresAt:     now.Add(24 * time.Hour),
			},
		}
		err := ds.BatchSaveDynamicThresholds(initial)
		require.NoError(t, err)

		// Update with batch save
		updated := []DynamicThreshold{
			{
				SpeciesName:   "robin",
				Level:         2,
				CurrentValue:  0.8,
				BaseThreshold: 0.7,
				ExpiresAt:     now.Add(48 * time.Hour),
			},
		}
		err = ds.BatchSaveDynamicThresholds(updated)

		require.NoError(t, err)

		// Verify update
		retrieved, err := ds.GetDynamicThreshold("robin")
		require.NoError(t, err)
		assert.Equal(t, 2, retrieved.Level)
		assert.InDelta(t, 0.8, retrieved.CurrentValue, 0.001)
	})

	t.Run("EmptyBatch", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		err := ds.BatchSaveDynamicThresholds([]DynamicThreshold{})

		require.NoError(t, err, "Empty batch should not error")
	})

	t.Run("RejectInvalidEntry", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		thresholds := []DynamicThreshold{
			{
				SpeciesName:  "valid",
				Level:        1,
				CurrentValue: 0.75,
			},
			{
				SpeciesName:  "", // Invalid
				Level:        1,
				CurrentValue: 0.75,
			},
		}

		err := ds.BatchSaveDynamicThresholds(thresholds)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "species name cannot be empty")
	})
}

// TestGetDynamicThresholdStats tests statistics generation
func TestGetDynamicThresholdStats(t *testing.T) {
	t.Run("GetStatsWithData", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		now := time.Now()

		// Create mix of active and expired thresholds at different levels
		thresholds := []DynamicThreshold{
			{SpeciesName: "active1", Level: 1, CurrentValue: 0.75, BaseThreshold: 0.7, ExpiresAt: now.Add(24 * time.Hour)},
			{SpeciesName: "active2", Level: 2, CurrentValue: 0.8, BaseThreshold: 0.7, ExpiresAt: now.Add(48 * time.Hour)},
			{SpeciesName: "active3", Level: 1, CurrentValue: 0.73, BaseThreshold: 0.7, ExpiresAt: now.Add(36 * time.Hour)},
			{SpeciesName: "expired1", Level: 1, CurrentValue: 0.75, BaseThreshold: 0.7, ExpiresAt: now.Add(-1 * time.Hour)},
			{SpeciesName: "expired2", Level: 3, CurrentValue: 0.85, BaseThreshold: 0.7, ExpiresAt: now.Add(-2 * time.Hour)},
		}

		for _, threshold := range thresholds {
			threshold.ValidHours = 48
			err := ds.SaveDynamicThreshold(&threshold)
			require.NoError(t, err)
		}

		// Get stats
		totalCount, activeCount, atMinimumCount, levelDistribution, err := ds.GetDynamicThresholdStats()

		require.NoError(t, err)

		// Verify counts
		assert.Equal(t, int64(5), totalCount)
		assert.Equal(t, int64(3), activeCount)
		// active3 has level 3, so atMinimumCount should be 1
		assert.Equal(t, int64(1), atMinimumCount)

		// Verify level distribution
		assert.NotNil(t, levelDistribution)
		// level 1: active1, level 2: active2, level 3: active3
		assert.Equal(t, int64(1), levelDistribution[1])
		assert.Equal(t, int64(1), levelDistribution[2])
		assert.Equal(t, int64(1), levelDistribution[3])
	})

	t.Run("GetStatsEmptyDatabase", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		totalCount, activeCount, atMinimumCount, levelDistribution, err := ds.GetDynamicThresholdStats()

		require.NoError(t, err)
		assert.Equal(t, int64(0), totalCount)
		assert.Equal(t, int64(0), activeCount)
		assert.Equal(t, int64(0), atMinimumCount)
		assert.NotNil(t, levelDistribution)
		assert.Empty(t, levelDistribution)
	})
}

// TestDynamicThresholdIndexes tests database indexes
func TestDynamicThresholdIndexes(t *testing.T) {
	t.Run("UniqueSpeciesNameIndex", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		threshold1 := &DynamicThreshold{
			SpeciesName:   "duplicate",
			Level:         1,
			CurrentValue:  0.75,
			BaseThreshold: 0.7,
			ExpiresAt:     time.Now().Add(24 * time.Hour),
		}
		err := ds.SaveDynamicThreshold(threshold1)
		require.NoError(t, err)

		// Attempt to save duplicate (should update, not error due to upsert)
		threshold2 := &DynamicThreshold{
			SpeciesName:   "duplicate",
			Level:         2,
			CurrentValue:  0.8,
			BaseThreshold: 0.7,
			ExpiresAt:     time.Now().Add(48 * time.Hour),
		}
		err = ds.SaveDynamicThreshold(threshold2)
		require.NoError(t, err)

		// Verify only one record exists with updated values
		all, err := ds.GetAllDynamicThresholds()
		require.NoError(t, err)
		assert.Len(t, all, 1)
		assert.Equal(t, 2, all[0].Level)
	})
}

// TestTimestampFields tests automatic timestamp management
func TestTimestampFields(t *testing.T) {
	t.Run("AutoSetTimestamps", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		threshold := &DynamicThreshold{
			SpeciesName:   "timestamp-test",
			Level:         1,
			CurrentValue:  0.75,
			BaseThreshold: 0.7,
			ExpiresAt:     time.Now().Add(24 * time.Hour),
		}

		before := time.Now()
		err := ds.SaveDynamicThreshold(threshold)
		after := time.Now()

		require.NoError(t, err)
		assert.False(t, threshold.FirstCreated.IsZero())
		assert.False(t, threshold.UpdatedAt.IsZero())
		assert.True(t, threshold.FirstCreated.After(before.Add(-time.Second)))
		assert.True(t, threshold.FirstCreated.Before(after.Add(time.Second)))
	})

	t.Run("PreserveFirstCreatedOnUpdate", func(t *testing.T) {
		ds := setupDynamicThresholdTestDB(t)

		threshold := &DynamicThreshold{
			SpeciesName:   "preserve-test",
			Level:         1,
			CurrentValue:  0.75,
			BaseThreshold: 0.7,
			ExpiresAt:     time.Now().Add(24 * time.Hour),
		}

		err := ds.SaveDynamicThreshold(threshold)
		require.NoError(t, err)
		firstCreated := threshold.FirstCreated

		// Wait a bit to ensure timestamp difference
		time.Sleep(10 * time.Millisecond)

		// Update
		threshold.Level = 2
		err = ds.SaveDynamicThreshold(threshold)
		require.NoError(t, err)

		// FirstCreated should not change
		assert.Equal(t, firstCreated.Unix(), threshold.FirstCreated.Unix())
	})
}
