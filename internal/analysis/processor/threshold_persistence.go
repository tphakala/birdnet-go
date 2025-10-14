// threshold_persistence.go: Handles persistence of dynamic thresholds to database
package processor

import (
	"context"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

const (
	// DefaultPersistInterval is the default interval for persisting dynamic thresholds to database
	DefaultPersistInterval = 30 * time.Second

	// DefaultCleanupInterval is the default interval for cleaning up expired dynamic thresholds
	DefaultCleanupInterval = 24 * time.Hour

	// DefaultFlushTimeout is the default timeout for flushing dynamic thresholds during shutdown
	// Increased to 15s to provide comfortable margin for slow storage (SD cards, network drives)
	// while still being responsive. With max ~300 species, batch insert completes quickly on
	// normal hardware, but this accounts for resource-constrained systems.
	DefaultFlushTimeout = 15 * time.Second
)

// loadDynamicThresholdsFromDB loads persisted dynamic thresholds from the database
// This is called during processor initialization to restore learned thresholds across restarts
func (p *Processor) loadDynamicThresholdsFromDB() error {
	GetLogger().Info("Loading dynamic thresholds from database",
		"operation", "load_dynamic_thresholds")

	thresholds, err := p.Ds.GetAllDynamicThresholds()
	if err != nil {
		// Check if error is "not found" (table doesn't exist, no records, etc)
		// This is normal on first run - return nil to indicate success with no data
		errStr := err.Error()
		if errStr == "record not found" ||
			strings.Contains(errStr, "no such table") ||
			strings.Contains(errStr, "doesn't exist") {
			GetLogger().Debug("No existing dynamic thresholds found (first run)",
				"reason", errStr,
				"operation", "load_dynamic_thresholds")
			return nil // Normal condition, not an error
		}

		// Actual database error - log as warning
		GetLogger().Warn("Database error loading dynamic thresholds",
			"error", err,
			"operation", "load_dynamic_thresholds")
		return err
	}

	p.thresholdsMutex.Lock()
	defer p.thresholdsMutex.Unlock()

	now := time.Now()
	loadedCount := 0
	expiredCount := 0

	for i := range thresholds {
		dbThreshold := &thresholds[i]
		// Skip expired thresholds
		if now.After(dbThreshold.ExpiresAt) {
			expiredCount++
			if p.Settings.Realtime.DynamicThreshold.Debug {
				GetLogger().Debug("Skipping expired threshold",
					"species", dbThreshold.SpeciesName,
					"expires_at", dbThreshold.ExpiresAt,
					"operation", "load_dynamic_thresholds")
			}
			continue
		}

		// Convert database model to in-memory representation
		p.DynamicThresholds[dbThreshold.SpeciesName] = &DynamicThreshold{
			Level:         dbThreshold.Level,
			CurrentValue:  dbThreshold.CurrentValue,
			Timer:         dbThreshold.ExpiresAt,
			HighConfCount: dbThreshold.HighConfCount,
			ValidHours:    dbThreshold.ValidHours,
		}
		loadedCount++

		if p.Settings.Realtime.DynamicThreshold.Debug {
			GetLogger().Debug("Loaded dynamic threshold",
				"species", dbThreshold.SpeciesName,
				"level", dbThreshold.Level,
				"current_value", dbThreshold.CurrentValue,
				"expires_at", dbThreshold.ExpiresAt,
				"operation", "load_dynamic_thresholds")
		}
	}

	GetLogger().Info("Dynamic thresholds loaded from database",
		"loaded_count", loadedCount,
		"expired_count", expiredCount,
		"total_retrieved", len(thresholds),
		"operation", "load_dynamic_thresholds")

	return nil
}

// persistDynamicThresholds saves all current dynamic thresholds to the database
// This is called periodically by the persistence goroutine
func (p *Processor) persistDynamicThresholds() error {
	p.thresholdsMutex.RLock()
	thresholdsCount := len(p.DynamicThresholds)

	// Early return if no thresholds to persist
	if thresholdsCount == 0 {
		p.thresholdsMutex.RUnlock()
		return nil
	}

	// Create a slice to hold database representations
	dbThresholds := make([]datastore.DynamicThreshold, 0, thresholdsCount)
	expiredSpecies := make([]string, 0) // Track expired species for cleanup
	now := time.Now()

	// Convert in-memory thresholds to database models
	for speciesName, threshold := range p.DynamicThresholds {
		// Track expired thresholds for cleanup
		if now.After(threshold.Timer) {
			expiredSpecies = append(expiredSpecies, speciesName)
			if p.Settings.Realtime.DynamicThreshold.Debug {
				GetLogger().Debug("Found expired threshold during persistence",
					"species", speciesName,
					"expires_at", threshold.Timer,
					"operation", "persist_dynamic_thresholds")
			}
			continue
		}

		// Reconstruct base threshold for reference
		baseThreshold := p.getBaseConfidenceThreshold(speciesName)

		dbThresholds = append(dbThresholds, datastore.DynamicThreshold{
			SpeciesName:   speciesName,
			Level:         threshold.Level,
			CurrentValue:  threshold.CurrentValue,
			BaseThreshold: float64(baseThreshold),
			HighConfCount: threshold.HighConfCount,
			ValidHours:    threshold.ValidHours,
			ExpiresAt:     threshold.Timer,
			LastTriggered: now,  // Track when we last saw activity
			FirstCreated:  now,  // Will be preserved by upsert if record exists
			UpdatedAt:     now,
			TriggerCount:  threshold.HighConfCount, // Use HighConfCount as trigger count
		})
	}
	p.thresholdsMutex.RUnlock()

	// Clean up expired thresholds from memory
	if len(expiredSpecies) > 0 {
		p.thresholdsMutex.Lock()
		for _, speciesName := range expiredSpecies {
			delete(p.DynamicThresholds, speciesName)
		}
		p.thresholdsMutex.Unlock()

		GetLogger().Info("Cleaned expired thresholds from memory",
			"count", len(expiredSpecies),
			"operation", "persist_dynamic_thresholds")
	}

	// Nothing to persist after filtering expired thresholds
	if len(dbThresholds) == 0 {
		return nil
	}

	// Use batch save for efficiency with retry logic for transient lock errors
	// Keep at 3 retries with fast backoff - we have 30s busy_timeout doing heavy lifting
	maxRetries := 3
	baseDelay := 100 * time.Millisecond

	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		err = p.Ds.BatchSaveDynamicThresholds(dbThresholds)
		if err == nil {
			break // Success
		}

		// Check if error is a database locked error (transient)
		errStr := err.Error()
		isLockError := strings.Contains(errStr, "database is locked") ||
			strings.Contains(errStr, "SQLITE_BUSY")

		if !isLockError || attempt == maxRetries-1 {
			// Not a lock error or exhausted retries
			GetLogger().Error("Failed to persist dynamic thresholds",
				"error", err,
				"threshold_count", len(dbThresholds),
				"attempt", attempt+1,
				"max_retries", maxRetries,
				"operation", "persist_dynamic_thresholds")
			return err
		}

		// Exponential backoff: 100ms, 200ms, 400ms
		backoffDuration := baseDelay * time.Duration(1<<uint(attempt))
		GetLogger().Warn("Database locked, retrying after backoff",
			"attempt", attempt+1,
			"max_retries", maxRetries,
			"backoff_ms", backoffDuration.Milliseconds(),
			"operation", "persist_dynamic_thresholds")

		// Context-aware sleep - allows early exit on shutdown
		select {
		case <-time.After(backoffDuration):
			// Continue to retry
		case <-p.thresholdsCtx.Done():
			GetLogger().Info("Retry aborted due to shutdown",
				"operation", "persist_dynamic_thresholds")
			return p.thresholdsCtx.Err()
		}
	}

	if p.Settings.Realtime.DynamicThreshold.Debug {
		GetLogger().Debug("Persisted dynamic thresholds to database",
			"count", len(dbThresholds),
			"operation", "persist_dynamic_thresholds")
	}

	return nil
}

// startThresholdPersistence starts a goroutine that periodically persists dynamic thresholds
// This ensures that learned thresholds are saved to the database and survive application restarts
// The goroutine uses a dedicated context for clean cancellation on shutdown
func (p *Processor) startThresholdPersistence() {
	// Create dedicated context for threshold goroutines
	// Both persistence and cleanup will share this context
	p.thresholdsCtx, p.thresholdsCancel = context.WithCancel(context.Background())

	// Start periodic persistence
	go func() {
		ticker := time.NewTicker(DefaultPersistInterval)
		defer ticker.Stop()

		GetLogger().Info("Starting dynamic threshold persistence",
			"persist_interval_seconds", int(DefaultPersistInterval.Seconds()),
			"operation", "threshold_persistence_startup")

		for {
			select {
			case <-ticker.C:
				if err := p.persistDynamicThresholds(); err != nil {
					GetLogger().Error("Failed to persist dynamic thresholds",
						"error", err,
						"operation", "persist_dynamic_thresholds")
				}
			case <-p.thresholdsCtx.Done():
				// Shutdown signal received via context cancellation
				GetLogger().Info("Dynamic threshold persistence stopped",
					"operation", "threshold_persistence_shutdown")
				return
			}
		}
	}()
}

// startThresholdCleanup starts a goroutine that periodically cleans up expired thresholds
// This prevents the database from accumulating stale threshold data
// The goroutine uses the same context as persistence for clean cancellation on shutdown
func (p *Processor) startThresholdCleanup() {
	// Use the same context created in startThresholdPersistence
	// This ensures both goroutines stop together when thresholdsCancel() is called
	go func() {
		ticker := time.NewTicker(DefaultCleanupInterval)
		defer ticker.Stop()

		GetLogger().Info("Starting dynamic threshold cleanup",
			"cleanup_interval_hours", int(DefaultCleanupInterval.Hours()),
			"operation", "threshold_cleanup_startup")

		for {
			select {
			case <-ticker.C:
				deleted, err := p.Ds.DeleteExpiredDynamicThresholds(time.Now())
				if err != nil {
					GetLogger().Error("Failed to clean expired thresholds",
						"error", err,
						"operation", "cleanup_dynamic_thresholds")
				} else if deleted > 0 {
					GetLogger().Info("Cleaned expired dynamic thresholds",
						"count", deleted,
						"operation", "cleanup_dynamic_thresholds")
				}
			case <-p.thresholdsCtx.Done():
				// Shutdown signal received via context cancellation
				GetLogger().Info("Dynamic threshold cleanup stopped",
					"operation", "threshold_cleanup_shutdown")
				return
			}
		}
	}()
}

// FlushDynamicThresholds immediately persists all dynamic thresholds to the database
// This is useful during graceful shutdown to ensure no data loss
func (p *Processor) FlushDynamicThresholds() error {
	GetLogger().Info("Flushing dynamic thresholds to database",
		"operation", "flush_dynamic_thresholds")

	if err := p.persistDynamicThresholds(); err != nil {
		GetLogger().Error("Failed to flush dynamic thresholds",
			"error", err,
			"operation", "flush_dynamic_thresholds")
		return err
	}

	GetLogger().Info("Dynamic thresholds flushed successfully",
		"operation", "flush_dynamic_thresholds")
	return nil
}
