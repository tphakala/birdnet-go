// threshold_persistence.go: Handles persistence of dynamic thresholds to database
package processor

import (
	"context"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
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
	settings := p.currentSettings()
	GetLogger().Info("Loading dynamic thresholds from database",
		logger.String("operation", "load_dynamic_thresholds"))

	thresholds, err := p.Ds.GetAllDynamicThresholds()
	if err != nil {
		// Check if error is "not found" (table doesn't exist, no records, etc)
		// This is normal on first run - return nil to indicate success with no data
		errStr := err.Error()
		if errStr == "record not found" ||
			strings.Contains(errStr, "no such table") ||
			strings.Contains(errStr, "doesn't exist") {
			GetLogger().Debug("No existing dynamic thresholds found (first run)",
				logger.String("reason", errStr),
				logger.String("operation", "load_dynamic_thresholds"))
			return nil // Normal condition, not an error
		}

		// Actual database error - log as warning
		GetLogger().Warn("Database error loading dynamic thresholds",
			logger.Error(err),
			logger.String("operation", "load_dynamic_thresholds"))
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
			if settings.Realtime.DynamicThreshold.Debug {
				GetLogger().Debug("Skipping expired threshold",
					logger.String("species", dbThreshold.SpeciesName),
					logger.String("model", dbThreshold.ModelName),
					logger.Time("expires_at", dbThreshold.ExpiresAt),
					logger.String("operation", "load_dynamic_thresholds"))
			}
			continue
		}

		// Reconstruct composite key from DB fields
		key := dynamicThresholdKey(dbThreshold.ModelName, strings.ToLower(dbThreshold.SpeciesName))

		// Convert database model to in-memory representation
		p.DynamicThresholds[key] = &DynamicThreshold{
			Level:          dbThreshold.Level,
			CurrentValue:   dbThreshold.CurrentValue,
			Timer:          dbThreshold.ExpiresAt,
			HighConfCount:  dbThreshold.HighConfCount,
			ValidHours:     dbThreshold.ValidHours,
			ScientificName: dbThreshold.ScientificName,
		}
		loadedCount++

		if settings.Realtime.DynamicThreshold.Debug {
			GetLogger().Debug("Loaded dynamic threshold",
				logger.String("species", dbThreshold.SpeciesName),
				logger.String("model", dbThreshold.ModelName),
				logger.Int("threshold_level", dbThreshold.Level),
				logger.Float64("current_value", dbThreshold.CurrentValue),
				logger.Time("expires_at", dbThreshold.ExpiresAt),
				logger.String("operation", "load_dynamic_thresholds"))
		}
	}

	GetLogger().Info("Dynamic thresholds loaded from database",
		logger.Int("loaded_count", loadedCount),
		logger.Int("expired_count", expiredCount),
		logger.Int("total_retrieved", len(thresholds)),
		logger.String("operation", "load_dynamic_thresholds"))

	return nil
}

// convertThresholdsForPersistence converts in-memory thresholds to database format.
// Returns the database thresholds and a list of expired species to clean up.
func (p *Processor) convertThresholdsForPersistence(settings *conf.Settings) (dbThresholds []datastore.DynamicThreshold, expiredSpecies []string) {
	p.thresholdsMutex.RLock()
	defer p.thresholdsMutex.RUnlock()

	if len(p.DynamicThresholds) == 0 {
		return nil, nil
	}

	now := time.Now()
	dbThresholds = make([]datastore.DynamicThreshold, 0, len(p.DynamicThresholds))
	expiredSpecies = make([]string, 0)

	for compositeKey, threshold := range p.DynamicThresholds {
		if now.After(threshold.Timer) {
			expiredSpecies = append(expiredSpecies, compositeKey)
			if settings.Realtime.DynamicThreshold.Debug {
				GetLogger().Debug("Found expired threshold during persistence",
					logger.String("key", compositeKey),
					logger.Time("expires_at", threshold.Timer),
					logger.String("operation", "persist_dynamic_thresholds"))
			}
			continue
		}

		// Extract model name and species name from composite key
		modelName, speciesName := splitDynamicThresholdKey(compositeKey)

		baseThreshold := p.getBaseConfidenceThreshold(settings, speciesName, "", modelName)
		dbThresholds = append(dbThresholds, datastore.DynamicThreshold{
			SpeciesName:    speciesName,
			ModelName:      modelName,
			ScientificName: threshold.ScientificName,
			Level:          threshold.Level,
			CurrentValue:   threshold.CurrentValue,
			BaseThreshold:  float64(baseThreshold),
			HighConfCount:  threshold.HighConfCount,
			ValidHours:     threshold.ValidHours,
			ExpiresAt:      threshold.Timer,
			LastTriggered:  now,
			FirstCreated:   now,
			UpdatedAt:      now,
			TriggerCount:   threshold.HighConfCount,
		})
	}

	return dbThresholds, expiredSpecies
}

// cleanupExpiredThresholds removes expired thresholds from memory.
func (p *Processor) cleanupExpiredThresholds(expiredSpecies []string) {
	if len(expiredSpecies) == 0 {
		return
	}

	p.thresholdsMutex.Lock()
	for _, speciesName := range expiredSpecies {
		delete(p.DynamicThresholds, speciesName)
	}
	p.thresholdsMutex.Unlock()

	GetLogger().Info("Cleaned expired thresholds from memory",
		logger.Int("count", len(expiredSpecies)),
		logger.String("operation", "persist_dynamic_thresholds"))
}

// saveThresholdsWithRetry attempts to save thresholds with exponential backoff.
// The caller passes the threshold-lifecycle context so the retry loop aborts on
// cancellation without reading the shared p.thresholdsCtx field (which would race
// with StartDynamicThresholds/StopDynamicThresholds toggling the feature at runtime).
func (p *Processor) saveThresholdsWithRetry(ctx context.Context, dbThresholds []datastore.DynamicThreshold) error {
	if ctx == nil {
		ctx = context.Background()
	}
	const maxRetries = 3
	baseDelay := 100 * time.Millisecond

	var err error
	for attempt := range maxRetries {
		err = p.Ds.BatchSaveDynamicThresholds(dbThresholds)
		if err == nil {
			return nil
		}

		if !datastore.IsTransientDBError(err) || attempt == maxRetries-1 {
			GetLogger().Error("Failed to persist dynamic thresholds",
				logger.Error(err),
				logger.Int("threshold_count", len(dbThresholds)),
				logger.Int("attempt", attempt+1),
				logger.Int("max_retries", maxRetries),
				logger.String("operation", "persist_dynamic_thresholds"))
			return err
		}

		backoffDuration := baseDelay * time.Duration(1<<uint(attempt)) //nolint:gosec // G115: attempt is bounded by maxRetries (3), no overflow risk
		GetLogger().Warn("Database locked, retrying after backoff",
			logger.Int("attempt", attempt+1),
			logger.Int("max_retries", maxRetries),
			logger.Int64("backoff_ms", backoffDuration.Milliseconds()),
			logger.String("operation", "persist_dynamic_thresholds"))

		// Use an explicit timer (not time.After) so the timer is released promptly
		// when ctx is cancelled mid-backoff, instead of lingering until it fires.
		timer := time.NewTimer(backoffDuration)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			GetLogger().Info("Retry aborted due to shutdown",
				logger.String("operation", "persist_dynamic_thresholds"))
			return ctx.Err()
		}
	}
	return err
}

// persistDynamicThresholds saves all current dynamic thresholds to the database
// This is called periodically by the persistence goroutine. The ctx governs the
// retry-backoff abort and is supplied by the caller (the persistence goroutine's
// lifecycle context, or a snapshot taken under thresholdsMutex by Stop/Flush).
func (p *Processor) persistDynamicThresholds(ctx context.Context) error {
	settings := p.currentSettings()
	dbThresholds, expiredSpecies := p.convertThresholdsForPersistence(settings)

	p.cleanupExpiredThresholds(expiredSpecies)

	if len(dbThresholds) == 0 {
		// Even when there are no thresholds to persist, drain pending resets
		// to clean up species that were deleted between persistence cycles.
		p.drainPendingResets()
		return nil
	}

	if err := p.saveThresholdsWithRetry(ctx, dbThresholds); err != nil {
		return err
	}

	// After the batch save, drain pending resets. If a species was deleted
	// between the snapshot (convertThresholdsForPersistence) and the write
	// (saveThresholdsWithRetry), the batch upsert may have re-inserted it.
	// Draining pending resets re-deletes those species from the database.
	p.drainPendingResets()

	if settings.Realtime.DynamicThreshold.Debug {
		GetLogger().Debug("Persisted dynamic thresholds to database",
			logger.Int("count", len(dbThresholds)),
			logger.String("operation", "persist_dynamic_thresholds"))
	}

	return nil
}

// drainPendingResets processes any species that were reset (deleted) while a
// persistence cycle was in progress. Because the periodic batch upsert could
// re-insert a stale snapshot of a deleted species, this method re-deletes them
// from the database after the batch write completes.
//
// If any DB delete fails, the failed items are requeued into pendingResets so
// they will be retried on the next persistence cycle rather than silently lost.
func (p *Processor) drainPendingResets() {
	p.thresholdsMutex.Lock()
	resets := p.pendingResets
	resetAll := p.pendingResetAll
	// Clear the pending state under lock so new resets can accumulate while
	// we perform DB operations. Use nil-safe initialization since tests may
	// create Processor structs without initializing pendingResets.
	if p.pendingResets != nil {
		p.pendingResets = make(map[string]struct{})
	}
	p.pendingResetAll = false
	p.thresholdsMutex.Unlock()

	if p.Ds == nil || (len(resets) == 0 && !resetAll) {
		return
	}

	log := GetLogger()

	if resetAll {
		var requeue bool

		if _, err := p.Ds.DeleteAllDynamicThresholds(); err != nil {
			requeue = true
			log.Warn("failed to re-delete all dynamic thresholds after persistence, requeuing",
				logger.Error(err),
				logger.String("operation", "drain_pending_resets"))
		}
		if _, err := p.Ds.DeleteAllThresholdEvents(); err != nil {
			requeue = true
			log.Warn("failed to re-delete all threshold events after persistence, requeuing",
				logger.Error(err),
				logger.String("operation", "drain_pending_resets"))
		}

		// If either delete-all failed, requeue the resetAll flag
		if requeue {
			p.thresholdsMutex.Lock()
			p.pendingResetAll = true
			p.thresholdsMutex.Unlock()
		}
		return
	}

	// Track species names whose DB deletes failed so we can requeue them.
	var failedResets []string

	for speciesName := range resets {
		thresholdErr := p.Ds.DeleteDynamicThreshold(speciesName)
		if thresholdErr != nil {
			log.Warn("failed to re-delete dynamic threshold after persistence, requeuing",
				logger.String("species", speciesName),
				logger.Error(thresholdErr),
				logger.String("operation", "drain_pending_resets"))
		}
		eventsErr := p.Ds.DeleteThresholdEvents(speciesName)
		if eventsErr != nil {
			log.Warn("failed to re-delete threshold events after persistence, requeuing",
				logger.String("species", speciesName),
				logger.Error(eventsErr),
				logger.String("operation", "drain_pending_resets"))
		}
		if thresholdErr != nil || eventsErr != nil {
			failedResets = append(failedResets, speciesName)
		}
	}

	// Requeue any species whose deletes failed so the next cycle retries them.
	if len(failedResets) > 0 {
		p.thresholdsMutex.Lock()
		for _, speciesName := range failedResets {
			if p.pendingResets != nil {
				p.pendingResets[speciesName] = struct{}{}
			}
		}
		p.thresholdsMutex.Unlock()

		log.Warn("requeued failed pending resets for next cycle",
			logger.Int("requeued_count", len(failedResets)),
			logger.String("operation", "drain_pending_resets"))
	}
}

// startThresholdPersistence starts a goroutine that periodically persists dynamic thresholds
// This ensures that learned thresholds are saved to the database and survive application restarts
// The goroutine uses a dedicated context for clean cancellation on shutdown
func (p *Processor) startThresholdPersistence(ctx context.Context) {
	// The lifecycle context is created and stored by StartDynamicThresholds under
	// thresholdsMutex; the goroutine captures the local ctx and never reads the
	// shared p.thresholdsCtx field, so a concurrent Stop/Start cannot race it.

	// Start periodic persistence
	go func() {
		ticker := time.NewTicker(DefaultPersistInterval)
		defer ticker.Stop()

		GetLogger().Info("Starting dynamic threshold persistence",
			logger.Int("persist_interval_seconds", int(DefaultPersistInterval.Seconds())),
			logger.String("operation", "threshold_persistence_startup"))

		for {
			select {
			case <-ticker.C:
				if err := p.persistDynamicThresholds(ctx); err != nil {
					GetLogger().Error("Failed to persist dynamic thresholds",
						logger.Error(err),
						logger.String("operation", "persist_dynamic_thresholds"))
				}
			case <-ctx.Done():
				// Shutdown signal received via context cancellation
				GetLogger().Info("Dynamic threshold persistence stopped",
					logger.String("operation", "threshold_persistence_shutdown"))
				return
			}
		}
	}()
}

// startThresholdCleanup starts a goroutine that periodically cleans up expired thresholds
// This prevents the database from accumulating stale threshold data
// The goroutine uses the same context as persistence for clean cancellation on shutdown
func (p *Processor) startThresholdCleanup(ctx context.Context) {
	// Shares the same lifecycle context as persistence (passed by StartDynamicThresholds),
	// so both goroutines stop together when thresholdsCancel() is called.
	go func() {
		ticker := time.NewTicker(DefaultCleanupInterval)
		defer ticker.Stop()

		GetLogger().Info("Starting dynamic threshold cleanup",
			logger.Int("cleanup_interval_hours", int(DefaultCleanupInterval.Hours())),
			logger.String("operation", "threshold_cleanup_startup"))

		for {
			select {
			case <-ticker.C:
				deleted, err := p.Ds.DeleteExpiredDynamicThresholds(time.Now())
				if err != nil {
					GetLogger().Error("Failed to clean expired thresholds",
						logger.Error(err),
						logger.String("operation", "cleanup_dynamic_thresholds"))
				} else if deleted > 0 {
					GetLogger().Info("Cleaned expired dynamic thresholds",
						logger.Int64("count", deleted),
						logger.String("operation", "cleanup_dynamic_thresholds"))
				}
			case <-ctx.Done():
				// Shutdown signal received via context cancellation
				GetLogger().Info("Dynamic threshold cleanup stopped",
					logger.String("operation", "threshold_cleanup_shutdown"))
				return
			}
		}
	}()
}

// snapshotThresholdsCtx returns the current threshold-lifecycle context, read under
// thresholdsMutex so it never races with StartDynamicThresholds/StopDynamicThresholds
// rewriting the field. The returned context may be nil if the goroutines are not running.
func (p *Processor) snapshotThresholdsCtx() context.Context {
	p.thresholdsMutex.RLock()
	defer p.thresholdsMutex.RUnlock()
	return p.thresholdsCtx
}

// startThresholdGoroutines creates the shared lifecycle context and launches the
// persistence and cleanup goroutines. It is a no-op if they are already running. The
// "already running" check and the context creation happen atomically under
// thresholdsMutex so concurrent callers cannot double-start or leak a context.
func (p *Processor) startThresholdGoroutines() {
	p.thresholdsMutex.Lock()
	if p.thresholdsCancel != nil {
		p.thresholdsMutex.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.thresholdsCtx = ctx
	p.thresholdsCancel = cancel
	p.thresholdsMutex.Unlock()

	p.startThresholdPersistence(ctx)
	p.startThresholdCleanup(ctx)
}

// StartDynamicThresholds loads thresholds from the database and starts the persistence
// and cleanup goroutines. This is called when dynamic thresholds are enabled at runtime
// via the settings UI. It is safe to call multiple times; if goroutines are already
// running (thresholdsCancel is non-nil), the call is a no-op.
func (p *Processor) StartDynamicThresholds() {
	// Fast-path skip if already running, to avoid a redundant DB reload on repeated
	// enable toggles. startThresholdGoroutines re-checks atomically under the lock.
	if p.snapshotThresholdsCtx() != nil {
		return
	}

	if err := p.loadDynamicThresholdsFromDB(); err != nil {
		GetLogger().Debug("Starting with fresh dynamic thresholds",
			logger.String("reason", err.Error()),
			logger.String("operation", "start_dynamic_thresholds"))
	}

	p.startThresholdGoroutines()

	GetLogger().Info("Dynamic threshold goroutines started",
		logger.String("operation", "start_dynamic_thresholds"))
}

// StopDynamicThresholds stops the persistence and cleanup goroutines, flushes any
// in-memory thresholds to the database, and clears the in-memory threshold map.
// This is called when dynamic thresholds are disabled at runtime via the settings UI.
// It is safe to call when goroutines are not running.
//
// Concurrency contract: StartDynamicThresholds and StopDynamicThresholds are driven
// exclusively by ControlMonitor's single-goroutine control loop (one settings signal
// processed at a time), so a re-enable cannot interleave with an in-progress stop
// flush; the queued enable signal is handled only after this call returns and tears
// the session down, so the last toggle wins. The TOCTOU guard below (thresholdsCtx ==
// ctx) and snapshotThresholdsCtx keep the shared fields race-free against the
// concurrent persistence goroutine and ShutdownWithContext; they are NOT a license to
// call these lifecycle methods concurrently from multiple goroutines.
func (p *Processor) StopDynamicThresholds() {
	// Snapshot the lifecycle context under the lock (never read the field unlocked).
	// If goroutines were never started, ctx is nil and we skip the flush; the retry
	// path inside saveThresholdsWithRetry also defends against a nil ctx.
	ctx := p.snapshotThresholdsCtx()

	// Flush thresholds to DB BEFORE cancelling the context, using the snapshotted ctx
	// so the retry-backoff abort tracks the session we are stopping.
	if p.Ds != nil && ctx != nil {
		if err := p.persistDynamicThresholds(ctx); err != nil {
			GetLogger().Warn("Failed to flush dynamic thresholds during disable",
				logger.Error(err),
				logger.String("operation", "stop_dynamic_thresholds"))
		}
	}

	// Cancel persistence and cleanup goroutines after flush completes.
	// Protected by thresholdsMutex to prevent races with StartDynamicThresholds.
	p.thresholdsMutex.Lock()
	// Only tear down if the session is still the one we snapshotted. The flush above
	// runs without the lock and can block on DB-retry backoff; if the feature was
	// toggled back on in the meantime, StartDynamicThresholds installed a fresh ctx
	// and goroutines, and we must not cancel/clear those (see #1025).
	if p.thresholdsCtx == ctx {
		if p.thresholdsCancel != nil {
			p.thresholdsCancel()
			p.thresholdsCancel = nil
		}
		p.thresholdsCtx = nil
		// Clear in-memory thresholds while still holding the lock.
		p.DynamicThresholds = make(map[string]*DynamicThreshold)
	}
	p.thresholdsMutex.Unlock()

	GetLogger().Info("Dynamic threshold goroutines stopped and thresholds cleared",
		logger.String("operation", "stop_dynamic_thresholds"))
}

// FlushDynamicThresholds immediately persists all dynamic thresholds to the database
// This is useful during graceful shutdown to ensure no data loss
func (p *Processor) FlushDynamicThresholds() error {
	GetLogger().Info("Flushing dynamic thresholds to database",
		logger.String("operation", "flush_dynamic_thresholds"))

	// Use the lifecycle context if running; otherwise fall back to Background so a
	// flush during shutdown (or before Start) still completes. Read under the lock.
	ctx := p.snapshotThresholdsCtx()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.persistDynamicThresholds(ctx); err != nil {
		GetLogger().Error("Failed to flush dynamic thresholds",
			logger.Error(err),
			logger.String("operation", "flush_dynamic_thresholds"))
		return err
	}

	GetLogger().Info("Dynamic thresholds flushed successfully",
		logger.String("operation", "flush_dynamic_thresholds"))
	return nil
}
