// maintenance.go - Sync, pruning, cleanup, and notification operations

package species

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// SyncIfNeeded checks if a database sync is needed and performs it
// This helps keep the tracker updated with any database changes
func (t *SpeciesTracker) SyncIfNeeded() error {
	t.mu.RLock()
	elapsed := time.Since(t.lastSyncTime)
	interval := t.syncIntervalMins
	t.mu.RUnlock()

	// Compare durations directly; min interval is in minutes
	if elapsed < time.Duration(interval)*time.Minute {
		return nil // No sync needed yet
	}

	// Store count of existing data before sync
	t.mu.RLock()
	existingLifetimeCount := len(t.speciesFirstSeen)
	existingYearlyCount := len(t.speciesThisYear)
	existingSeasonalCount := 0
	for _, seasonMap := range t.speciesBySeason {
		existingSeasonalCount += len(seasonMap)
	}
	t.mu.RUnlock()

	// Log sync attempt
	logger.Debug("Starting database sync",
		"existing_lifetime_species", existingLifetimeCount,
		"existing_yearly_species", existingYearlyCount,
		"existing_seasonal_species", existingSeasonalCount)

	// Perform database sync
	if err := t.InitFromDatabase(); err != nil {
		logger.Error("Database sync failed, preserving existing data",
			"error", err,
			"existing_species", existingLifetimeCount)
		// Don't propagate error if we have existing data - continue with cached data
		if existingLifetimeCount > 0 {
			return nil
		}
		return err
	}

	// Check if sync suspiciously cleared all data
	t.mu.RLock()
	newLifetimeCount := len(t.speciesFirstSeen)
	t.mu.RUnlock()

	if existingLifetimeCount > 0 && newLifetimeCount == 0 {
		logger.Warn("Database sync returned no data but had existing data - possible database issue",
			"previous_count", existingLifetimeCount,
			"new_count", newLifetimeCount)
	}

	// Also perform periodic cleanup of old records (both species and notification records)
	pruned := t.PruneOldEntries()
	if pruned > 0 {
		logger.Debug("Pruned old entries during sync",
			"count", pruned)
	}

	return nil
}

// pruneLifetimeEntriesLocked removes very old lifetime entries (>10 years).
// Assumes lock is held.
func (t *SpeciesTracker) pruneLifetimeEntriesLocked(now time.Time) int {
	lifetimeCutoff := now.AddDate(-lifetimeRetentionYears, 0, 0)
	pruned := 0
	for scientificName, firstSeen := range t.speciesFirstSeen {
		if firstSeen.Before(lifetimeCutoff) {
			delete(t.speciesFirstSeen, scientificName)
			pruned++
		}
	}
	return pruned
}

// pruneYearlyEntriesLocked removes entries from previous tracking years.
// Assumes lock is held.
func (t *SpeciesTracker) pruneYearlyEntriesLocked(now time.Time) int {
	if !t.yearlyEnabled {
		return 0
	}

	currentYearStart := time.Date(now.Year(), time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, now.Location())
	if now.Before(currentYearStart) {
		currentYearStart = currentYearStart.AddDate(-1, 0, 0)
	}

	pruned := 0
	for scientificName, firstSeen := range t.speciesThisYear {
		if firstSeen.Before(currentYearStart) {
			delete(t.speciesThisYear, scientificName)
			pruned++
			logger.Debug("Pruned old yearly entry",
				"species", scientificName,
				"first_seen", firstSeen.Format("2006-01-02"),
				"year_start", currentYearStart.Format("2006-01-02"))
		}
	}
	return pruned
}

// isSeasonOld checks if all entries in a season map are older than the cutoff.
func isSeasonOld(speciesMap map[string]time.Time, cutoff time.Time) bool {
	for _, firstSeen := range speciesMap {
		if firstSeen.After(cutoff) {
			return false
		}
	}
	return true
}

// pruneSeasonalEntriesLocked removes old seasonal data (>1 year).
// Assumes lock is held.
func (t *SpeciesTracker) pruneSeasonalEntriesLocked(now time.Time) int {
	if !t.seasonalEnabled {
		return 0
	}

	seasonCutoff := now.AddDate(-1, 0, 0)
	pruned := 0

	for season, speciesMap := range t.speciesBySeason {
		if len(speciesMap) > 0 && isSeasonOld(speciesMap, seasonCutoff) {
			prunedFromSeason := len(speciesMap)
			delete(t.speciesBySeason, season)
			pruned += prunedFromSeason
			logger.Debug("Pruned old season data",
				"season", season,
				"entries_removed", prunedFromSeason)
		}
	}
	return pruned
}

// PruneOldEntries removes species entries older than 2x their respective window periods
// This prevents unbounded memory growth over time using period-specific cutoff times
func (t *SpeciesTracker) PruneOldEntries() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	pruned := 0

	// CRITICAL: Lifetime tracking should NEVER be pruned based on the new species window!
	// We only prune lifetime entries older than 10 years to handle edge cases.
	pruned += t.pruneLifetimeEntriesLocked(now)

	// Prune yearly tracking map if enabled
	pruned += t.pruneYearlyEntriesLocked(now)

	// Prune seasonal tracking maps if enabled
	pruned += t.pruneSeasonalEntriesLocked(now)

	// Also cleanup old notification records (only if suppression is enabled)
	if t.notificationSuppressionWindow > 0 {
		pruned += t.cleanupOldNotificationRecordsLocked(now)
	}

	return pruned
}

// cleanupOldNotificationRecordsLocked is an internal version that assumes lock is already held
func (t *SpeciesTracker) cleanupOldNotificationRecordsLocked(currentTime time.Time) int {
	if t.notificationLastSent == nil || t.notificationSuppressionWindow <= 0 {
		return 0
	}

	cleaned := 0
	// Compute cutoff = currentTime - suppressionWindow to remove records no longer needed
	// Once the suppression window has passed, we can notify again, so no need to keep the record
	cutoffTime := currentTime.Add(-t.notificationSuppressionWindow)

	for species, sentTime := range t.notificationLastSent {
		if sentTime.Before(cutoffTime) {
			delete(t.notificationLastSent, species)
			cleaned++
		}
	}

	return cleaned
}

// ShouldSuppressNotification checks if a notification for this species should be suppressed
// based on when the last notification was sent for this species.
// Returns true if notification should be suppressed, false if it should be sent.
func (t *SpeciesTracker) ShouldSuppressNotification(scientificName string, currentTime time.Time) bool {
	t.mu.RLock()
	lastSent, exists := t.notificationLastSent[scientificName]
	window := t.notificationSuppressionWindow
	t.mu.RUnlock()

	if !exists {
		return false // Never sent, don't suppress
	}
	if window <= 0 {
		return false // Suppression disabled
	}

	suppressUntil := lastSent.Add(window)
	shouldSuppress := currentTime.Before(suppressUntil)

	if shouldSuppress {
		logger.Debug("Suppressing duplicate notification",
			"species", scientificName,
			"suppress_until", suppressUntil,
			"suppression_window", window)
	}
	return shouldSuppress
}

// RecordNotificationSent records that a notification was sent for a species.
// This is used to prevent duplicate notifications within the suppression window.
func (t *SpeciesTracker) RecordNotificationSent(scientificName string, sentTime time.Time) {
	// Early return when suppression is disabled to avoid unnecessary operations
	if t.notificationSuppressionWindow <= 0 {
		return
	}

	t.mu.Lock()
	// Initialize map if needed
	if t.notificationLastSent == nil {
		t.notificationLastSent = make(map[string]time.Time, initialSpeciesCapacity)
	}

	// Record the notification time
	t.notificationLastSent[scientificName] = sentTime
	t.mu.Unlock()

	// Log outside the critical section to reduce lock contention
	logger.Debug("Recorded notification sent",
		"species", scientificName,
		"sent_time", sentTime.Format("2006-01-02 15:04:05"))

	// Persist to database asynchronously to avoid blocking (BG-17 fix)
	// This ensures notification suppression state survives application restarts
	//
	// Note: Database methods don't accept context, so timeout cannot be enforced.
	// However, SQLite is local and GORM has internal timeouts, so hangs are unlikely.
	// If a goroutine does leak due to database hang, in-memory suppression still works.
	// TODO(BG-17): Consider adding context.Context parameter to SaveNotificationHistory interface
	if t.ds != nil {
		go func() {
			// ExpiresAt = when the suppression ends (sentTime + suppressionWindow)
			expiresAt := sentTime.Add(t.notificationSuppressionWindow)
			history := &datastore.NotificationHistory{
				ScientificName:   scientificName,
				NotificationType: notificationTypeNewSpecies,
				LastSent:         sentTime,
				ExpiresAt:        expiresAt,
				CreatedAt:        sentTime,
				UpdatedAt:        sentTime,
			}

			if err := t.ds.SaveNotificationHistory(history); err != nil {
				logger.Error("Failed to save notification history to database",
					"species", scientificName,
					"error", err,
					"operation", "save_notification_history")
				// Don't crash - in-memory suppression still works
			} else {
				logger.Debug("Persisted notification history to database",
					"species", scientificName,
					"expires_at", expiresAt.Format("2006-01-02 15:04:05"))
			}
		}()
	}
}

// CleanupOldNotificationRecords removes notification records older than the suppression window
// to prevent unbounded memory growth.
// BG-17 fix: Also cleans up expired records from database
func (t *SpeciesTracker) CleanupOldNotificationRecords(currentTime time.Time) int {
	// Early return if suppression is disabled (0 window)
	if t.notificationSuppressionWindow <= 0 {
		return 0
	}

	// Clean up in-memory records (removes entries older than currentTime - suppressionWindow)
	t.mu.Lock()
	cleaned := t.cleanupOldNotificationRecordsLocked(currentTime)
	t.mu.Unlock()

	if cleaned > 0 {
		// Log the actual cutoff used by cleanupOldNotificationRecordsLocked
		cutoffTime := currentTime.Add(-t.notificationSuppressionWindow)
		logger.Debug("Cleaned up old notification records from memory",
			"removed_count", cleaned,
			"cutoff_time", cutoffTime.Format("2006-01-02 15:04:05"))
	}

	// Clean up database records asynchronously (BG-17 fix)
	// Deletes records where ExpiresAt < currentTime (i.e., suppression has expired)
	//
	// Note: Database methods don't accept context, so timeout cannot be enforced.
	// However, SQLite is local and GORM has internal timeouts, so hangs are unlikely.
	// TODO(BG-17): Consider adding context.Context parameter to DeleteExpiredNotificationHistory interface
	if t.ds != nil {
		go func() {
			// Delete records that have expired (ExpiresAt < now)
			deletedCount, err := t.ds.DeleteExpiredNotificationHistory(currentTime)
			if err != nil {
				logger.Error("Failed to cleanup expired notification history from database",
					"error", err,
					"current_time", currentTime.Format("2006-01-02 15:04:05"))
			} else if deletedCount > 0 {
				logger.Debug("Cleaned up expired notification history from database",
					"deleted_count", deletedCount,
					"current_time", currentTime.Format("2006-01-02 15:04:05"))
			}
		}()
	}

	return cleaned
}

// Close releases resources associated with the species tracker, including the logger.
// This should be called during application shutdown or when the tracker is no longer needed.
func (t *SpeciesTracker) Close() error {
	// Close the shared logger used by all tracker instances
	// Note: This is a package-level resource shared across all tracker instances
	if err := Close(); err != nil {
		return errors.New(err).
			Component("new-species-tracker").
			Category(errors.CategoryResource).
			Context("operation", "close_logger").
			Build()
	}
	return nil
}
