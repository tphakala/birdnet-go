// maintenance.go - Sync, pruning, cleanup, and notification operations

package species

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
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
	getLog().Debug("Starting database sync",
		logger.Int("existing_lifetime_species", existingLifetimeCount),
		logger.Int("existing_yearly_species", existingYearlyCount),
		logger.Int("existing_seasonal_species", existingSeasonalCount))

	// Perform database sync
	if err := t.InitFromDatabase(); err != nil {
		getLog().Error("Database sync failed, preserving existing data",
			logger.Error(err),
			logger.Int("existing_species", existingLifetimeCount))
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
		getLog().Warn("Database sync returned no data but had existing data - possible database issue",
			logger.Int("previous_count", existingLifetimeCount),
			logger.Int("new_count", newLifetimeCount))
	}

	// Also perform periodic cleanup of old records (both species and notification records)
	pruned := t.PruneOldEntries()
	if pruned > 0 {
		getLog().Debug("Pruned old entries during sync",
			logger.Int("count", pruned))
	}

	return nil
}

// dateOnlyBefore reports whether the calendar date of a is strictly before the
// calendar date of b, ignoring time-of-day and timezone. This prevents
// incorrect comparisons when one timestamp is UTC midnight (from time.Parse)
// and the other uses a local timezone (from time.Date with Location).
func dateOnlyBefore(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	if ay != by {
		return ay < by
	}
	if am != bm {
		return am < bm
	}
	return ad < bd
}

// pruneLifetimeEntriesLocked removes very old lifetime entries (>10 years).
// Assumes lock is held.
func (t *SpeciesTracker) pruneLifetimeEntriesLocked(now time.Time) int {
	lifetimeCutoff := now.AddDate(-lifetimeRetentionYears, 0, 0)
	pruned := 0
	for scientificName, firstSeen := range t.speciesFirstSeen {
		if dateOnlyBefore(firstSeen, lifetimeCutoff) {
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
		if dateOnlyBefore(firstSeen, currentYearStart) {
			delete(t.speciesThisYear, scientificName)
			pruned++
			getLog().Debug("Pruned old yearly entry",
				logger.String("species", scientificName),
				logger.String("first_seen", firstSeen.Format(time.DateOnly)),
				logger.String("year_start", currentYearStart.Format(time.DateOnly)))
		}
	}
	return pruned
}

// isSeasonOld checks if all entries in a season map are older than the cutoff.
// Uses date-only comparison to avoid timezone mismatch between UTC-parsed
// database dates and local-timezone cutoffs. An entry on the same calendar
// date as the cutoff is considered current (not old).
func isSeasonOld(speciesMap map[string]time.Time, cutoff time.Time) bool {
	for _, firstSeen := range speciesMap {
		if !dateOnlyBefore(firstSeen, cutoff) {
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
			getLog().Debug("Pruned old season data",
				logger.String("season", season),
				logger.Int("entries_removed", prunedFromSeason))
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

	// Also cleanup old notification records (only if suppression is enabled).
	// Lifer cleanup shares this gate with new-species even though its window is
	// fixed rather than user-configurable: RecordLiferNotificationSent only
	// persists lifer rows to the database under this same condition (see its
	// doc comment), so there's nothing here to clean up when it's disabled.
	if t.notificationSuppressionWindow > 0 {
		pruned += t.cleanupOldNotificationRecordsLocked(t.notificationLastSent, now, t.notificationSuppressionWindow)
		pruned += t.cleanupOldNotificationRecordsLocked(t.liferNotificationLastSent, now, liferNotificationSuppressionWindow)
	}

	return pruned
}

// cleanupOldNotificationRecordsLocked is an internal version that assumes lock
// is already held. Shared by notificationLastSent and
// liferNotificationLastSent, which are independent maps that use different
// suppression windows (see liferNotificationLastSent's doc comment); window is
// the one that applies to m.
func (t *SpeciesTracker) cleanupOldNotificationRecordsLocked(m map[string]time.Time, currentTime time.Time, window time.Duration) int {
	if m == nil || window <= 0 {
		return 0
	}

	cleaned := 0
	// Compute cutoff = currentTime - window to remove records no longer needed
	// Once the suppression window has passed, we can notify again, so no need to keep the record
	cutoffTime := currentTime.Add(-window)

	for species, sentTime := range m {
		if sentTime.Before(cutoffTime) {
			delete(m, species)
			cleaned++
		}
	}

	return cleaned
}

// ShouldSuppressNotification checks if a notification for this species should be suppressed
// based on when the last notification was sent for this species.
// Returns true if notification should be suppressed, false if it should be sent.
func (t *SpeciesTracker) ShouldSuppressNotification(scientificName string, currentTime time.Time) bool {
	scientificName = canonicalSpeciesName(scientificName)
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
		getLog().Debug("Suppressing duplicate notification",
			logger.String("species", scientificName),
			logger.Time("suppress_until", suppressUntil),
			logger.Duration("suppression_window", window))
	}
	return shouldSuppress
}

// RecordNotificationSent records that a notification was sent for a species.
// This is used to prevent duplicate notifications within the suppression window.
func (t *SpeciesTracker) RecordNotificationSent(scientificName string, sentTime time.Time) {
	if t.notificationSuppressionWindow <= 0 {
		return
	}
	// Record and persist under the canonical name so suppression survives a restart
	// and matches a later detection under either the legacy or canonical name.
	scientificName = canonicalSpeciesName(scientificName)

	t.mu.Lock()
	if t.notificationLastSent == nil {
		t.notificationLastSent = make(map[string]time.Time, initialSpeciesCapacity)
	}
	t.notificationLastSent[scientificName] = sentTime
	t.mu.Unlock()

	t.persistNotificationSent(scientificName, sentTime, t.notificationSuppressionWindow, notificationTypeNewSpecies, "notification")
}

// persistNotificationSent logs and asynchronously persists a suppression record
// shared by RecordNotificationSent and RecordLiferNotificationSent. window is
// the suppression window that applies to this kind (the user-configurable
// notificationSuppressionWindow for new-species, or the fixed
// liferNotificationSuppressionWindow for lifer), notifType selects the
// persisted NotificationHistory type, and kind is a short label for log
// messages. The two kinds keep independent in-memory maps, windows, and DB
// types on purpose — see liferNotificationLastSent's doc comment; only this
// persistence tail is common to both.
func (t *SpeciesTracker) persistNotificationSent(scientificName string, sentTime time.Time, window time.Duration, notifType, kind string) {
	// Log outside the critical section to reduce lock contention
	getLog().Debug("Recorded "+kind+" sent",
		logger.String("species", scientificName),
		logger.String("sent_time", sentTime.Format(time.DateTime)))

	// Persist to database asynchronously to avoid blocking (BG-17 fix)
	// This ensures notification suppression state survives application restarts.
	// The write runs under a bounded context so a stuck SQLite write cannot leak
	// this goroutine; in-memory suppression still works if the persist fails.
	if t.ds == nil {
		return
	}
	t.asyncOpsWg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), notificationPersistTimeout)
		defer cancel()

		// ExpiresAt = when the suppression ends (sentTime + window)
		expiresAt := sentTime.Add(window)
		history := &datastore.NotificationHistory{
			ScientificName:   scientificName,
			NotificationType: notifType,
			LastSent:         sentTime,
			ExpiresAt:        expiresAt,
			CreatedAt:        sentTime,
			UpdatedAt:        sentTime,
		}

		if err := t.ds.SaveNotificationHistory(ctx, history); err != nil {
			getLog().Error("Failed to save "+kind+" history to database",
				logger.String("species", scientificName),
				logger.Error(err),
				logger.String("operation", "save_notification_history"))
			// Don't crash - in-memory suppression still works
		} else {
			getLog().Debug("Persisted "+kind+" history to database",
				logger.String("species", scientificName),
				logger.String("expires_at", expiresAt.Format(time.DateTime)))
		}
	})
}

// ShouldSuppressLiferNotification checks if a lifer notification for this
// species should be suppressed based on when the last lifer notification was
// sent for it. Structural sibling of ShouldSuppressNotification, operating on
// liferNotificationLastSent instead of notificationLastSent, and using the
// fixed liferNotificationSuppressionWindow instead of the user-configurable
// notificationSuppressionWindow — see that constant's doc comment for why.
func (t *SpeciesTracker) ShouldSuppressLiferNotification(scientificName string, currentTime time.Time) bool {
	scientificName = canonicalSpeciesName(scientificName)
	t.mu.RLock()
	lastSent, exists := t.liferNotificationLastSent[scientificName]
	t.mu.RUnlock()

	if !exists {
		return false // Never sent, don't suppress
	}

	suppressUntil := lastSent.Add(liferNotificationSuppressionWindow)
	shouldSuppress := currentTime.Before(suppressUntil)

	if shouldSuppress {
		getLog().Debug("Suppressing duplicate lifer notification",
			logger.String("species", scientificName),
			logger.Time("suppress_until", suppressUntil),
			logger.Duration("suppression_window", liferNotificationSuppressionWindow))
	}
	return shouldSuppress
}

// RecordLiferNotificationSent records that a lifer notification was sent for
// a species. Structural sibling of RecordNotificationSent — see
// liferNotificationLastSent's doc comment for why this is a separate map and
// database notification type rather than reusing the new-species one.
//
// The in-memory record (this function's map write) is never skipped: lifer
// suppression uses the fixed liferNotificationSuppressionWindow and works
// regardless of NotificationSuppressionHours. Database persistence for
// restart-survival, however, piggybacks on that same setting being enabled
// (matching loadLiferNotificationsFromDatabase's gate) — writing rows that
// would never be reloaded or cleaned up when it's disabled would just leak
// storage, so it's skipped in that case; suppression still works correctly
// in-memory for the life of the process either way.
func (t *SpeciesTracker) RecordLiferNotificationSent(scientificName string, sentTime time.Time) {
	// Record and persist under the canonical name so suppression survives a restart
	// and matches a later detection under either the legacy or canonical name.
	scientificName = canonicalSpeciesName(scientificName)

	t.mu.Lock()
	if t.liferNotificationLastSent == nil {
		t.liferNotificationLastSent = make(map[string]time.Time, initialSpeciesCapacity)
	}
	t.liferNotificationLastSent[scientificName] = sentTime
	t.mu.Unlock()

	if t.notificationSuppressionWindow <= 0 {
		return
	}
	t.persistNotificationSent(scientificName, sentTime, liferNotificationSuppressionWindow, notificationTypeLifer, "lifer notification")
}

// CleanupOldNotificationRecords removes notification records older than the
// suppression window to prevent unbounded memory growth. Lifer cleanup shares
// this gate with new-species even though its window is fixed rather than
// user-configurable: RecordLiferNotificationSent only persists lifer rows to
// the database under this same condition, so there's nothing to clean up when
// it's disabled (see that function's doc comment).
// BG-17 fix: Also cleans up expired records from database
func (t *SpeciesTracker) CleanupOldNotificationRecords(currentTime time.Time) int {
	// Early return if suppression is disabled (0 window)
	if t.notificationSuppressionWindow <= 0 {
		return 0
	}

	// Clean up in-memory records (removes entries older than currentTime - suppressionWindow)
	t.mu.Lock()
	cleaned := t.cleanupOldNotificationRecordsLocked(t.notificationLastSent, currentTime, t.notificationSuppressionWindow)
	cleaned += t.cleanupOldNotificationRecordsLocked(t.liferNotificationLastSent, currentTime, liferNotificationSuppressionWindow)
	t.mu.Unlock()

	if cleaned > 0 {
		getLog().Debug("Cleaned up old notification records from memory",
			logger.Int("removed_count", cleaned))
	}

	// Clean up database records asynchronously (BG-17 fix)
	// Deletes records where ExpiresAt < currentTime (i.e., suppression has expired).
	// Runs under a bounded context so a stuck delete cannot leak this goroutine.
	if t.ds != nil {
		t.asyncOpsWg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), notificationPersistTimeout)
			defer cancel()

			// Delete records that have expired (ExpiresAt < now)
			deletedCount, err := t.ds.DeleteExpiredNotificationHistory(ctx, currentTime)
			if err != nil {
				getLog().Error("Failed to cleanup expired notification history from database",
					logger.Error(err),
					logger.String("current_time", currentTime.Format(time.DateTime)))
			} else if deletedCount > 0 {
				getLog().Debug("Cleaned up expired notification history from database",
					logger.Int64("deleted_count", deletedCount),
					logger.String("current_time", currentTime.Format(time.DateTime)))
			}
		})
	}

	return cleaned
}

// Close releases resources associated with the species tracker.
// This should be called during application shutdown or when the tracker is no longer needed.
// It waits for any in-flight async database operations to complete before returning.
func (t *SpeciesTracker) Close() error {
	// Cancel any in-flight background historical load first so shutdown does not
	// block on a long DB scan started by InitFromDatabaseAsync. Done lock-free
	// (the loader holds t.mu for the scan), and cancel is idempotent.
	if cancel := t.initCancel.Load(); cancel != nil {
		(*cancel)()
	}

	// Wait for any in-flight async database operations (notification persistence/cleanup)
	// This prevents goroutine leaks and ensures data is persisted before shutdown
	t.asyncOpsWg.Wait()

	// Note: The logger is a global resource managed at the application level,
	// so it's not closed here.
	return nil
}
