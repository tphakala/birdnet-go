// threshold_persistence.go: Handles persistence of dynamic thresholds to database
package processor

import (
	"log"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// loadDynamicThresholdsFromDB loads persisted dynamic thresholds from the database
// This is called during processor initialization to restore learned thresholds across restarts
func (p *Processor) loadDynamicThresholdsFromDB() error {
	GetLogger().Info("Loading dynamic thresholds from database",
		"operation", "load_dynamic_thresholds")

	thresholds, err := p.Ds.GetAllDynamicThresholds()
	if err != nil {
		GetLogger().Error("Failed to load dynamic thresholds from database",
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

	log.Printf("Loaded %d dynamic thresholds from database (%d expired)", loadedCount, expiredCount)

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
	now := time.Now()

	// Convert in-memory thresholds to database models
	for speciesName, threshold := range p.DynamicThresholds {
		// Skip expired thresholds
		if now.After(threshold.Timer) {
			if p.Settings.Realtime.DynamicThreshold.Debug {
				GetLogger().Debug("Skipping expired threshold during persistence",
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

	// Nothing to persist after filtering expired thresholds
	if len(dbThresholds) == 0 {
		return nil
	}

	// Use batch save for efficiency
	if err := p.Ds.BatchSaveDynamicThresholds(dbThresholds); err != nil {
		GetLogger().Error("Failed to persist dynamic thresholds",
			"error", err,
			"threshold_count", len(dbThresholds),
			"operation", "persist_dynamic_thresholds")
		return err
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
func (p *Processor) startThresholdPersistence() {
	// Default persistence interval: 30 seconds
	persistInterval := 30 * time.Second

	// Start periodic persistence
	go func() {
		ticker := time.NewTicker(persistInterval)
		defer ticker.Stop()

		GetLogger().Info("Starting dynamic threshold persistence",
			"persist_interval_seconds", int(persistInterval.Seconds()),
			"operation", "threshold_persistence_startup")

		for range ticker.C {
			if err := p.persistDynamicThresholds(); err != nil {
				GetLogger().Error("Failed to persist dynamic thresholds",
					"error", err,
					"operation", "persist_dynamic_thresholds")
			}
		}

		GetLogger().Info("Dynamic threshold persistence stopped",
			"operation", "threshold_persistence_shutdown")
	}()
}

// startThresholdCleanup starts a goroutine that periodically cleans up expired thresholds
// This prevents the database from accumulating stale threshold data
func (p *Processor) startThresholdCleanup() {
	// Default cleanup interval: 24 hours
	cleanupInterval := 24 * time.Hour

	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		GetLogger().Info("Starting dynamic threshold cleanup",
			"cleanup_interval_hours", int(cleanupInterval.Hours()),
			"operation", "threshold_cleanup_startup")

		for range ticker.C {
			deleted, err := p.Ds.DeleteExpiredDynamicThresholds(time.Now())
			if err != nil {
				GetLogger().Error("Failed to clean expired thresholds",
					"error", err,
					"operation", "cleanup_dynamic_thresholds")
			} else if deleted > 0 {
				GetLogger().Info("Cleaned expired dynamic thresholds",
					"count", deleted,
					"operation", "cleanup_dynamic_thresholds")
				log.Printf("Cleaned %d expired dynamic thresholds from database", deleted)
			}
		}

		GetLogger().Info("Dynamic threshold cleanup stopped",
			"operation", "threshold_cleanup_shutdown")
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
