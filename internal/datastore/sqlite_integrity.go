package datastore

import (
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// performStartupIntegrityCheck runs PRAGMA quick_check synchronously during
// Open(). If corruption is found, it attempts REINDEX auto-recovery. On
// failure the corruption flag is latched and a deferred notification is
// queued, but no error is returned: the application continues in degraded
// mode so the web UI remains accessible for diagnostics.
func (s *SQLiteStore) performStartupIntegrityCheck() {
	log := GetLogger()
	log.Info("running startup database integrity check")

	result := s.runQuickCheck()

	// Cache the result for the health endpoint.
	s.integrityMu.Lock()
	s.integrityResult = result
	s.integrityMu.Unlock()

	if result == "ok" {
		log.Info("startup integrity check passed")
		return
	}

	log.Warn("startup integrity check detected corruption",
		logger.String("result", truncateResult(result, 200)))

	// Attempt automatic recovery via REINDEX.
	if s.attemptAutoRecovery() {
		log.Info("REINDEX auto-recovery succeeded, database integrity restored")
		return
	}

	// Recovery failed: latch corruption flag.
	s.dbCorrupted.Store(true)

	log.Error("REINDEX auto-recovery failed, database flagged as corrupted",
		logger.String("integrity_result", truncateResult(result, 200)))

	// Report to Sentry once.
	if s.telemetry != nil {
		s.telemetry.CaptureEnhancedError(
			fmt.Errorf("startup integrity check failed: %s", truncateResult(result, 500)),
			"startup_integrity_check",
			s,
		)
	}

	// Queue a deferred user notification (notification service may not be
	// ready yet during Open()).
	s.notifyCorruptionDeferred(result)
}

// runQuickCheck executes PRAGMA quick_check and returns "ok" or an error
// description string.
func (s *SQLiteStore) runQuickCheck() string {
	var results []string
	if err := s.DB.Raw("PRAGMA quick_check").Scan(&results).Error; err != nil {
		return err.Error()
	}
	result := strings.Join(results, "; ")
	if result == "" {
		return "ok"
	}
	return result
}

// attemptAutoRecovery runs REINDEX and re-checks integrity. Returns true if
// the database passes quick_check after reindexing.
func (s *SQLiteStore) attemptAutoRecovery() bool {
	log := GetLogger()
	log.Info("attempting REINDEX auto-recovery")

	if err := s.DB.Exec("REINDEX").Error; err != nil {
		log.Error("REINDEX command failed", logger.Error(err))
		return false
	}

	result := s.runQuickCheck()

	// Update the cached result.
	s.integrityMu.Lock()
	s.integrityResult = result
	s.integrityMu.Unlock()

	return result == "ok"
}

// notifyCorruptionDeferred spawns a goroutine that polls for the notification
// service (up to 30 seconds) and sends a persistent warning notification.
// The notification service may not be initialized when Open() runs, so this
// must be deferred.
func (s *SQLiteStore) notifyCorruptionDeferred(integrityResult string) {
	s.monitoringWg.Go(func() {
		const (
			pollInterval = 500 * time.Millisecond
			maxWait      = 30 * time.Second
		)

		deadline := time.Now().Add(maxWait)
		var svc *notification.Service
		for time.Now().Before(deadline) {
			svc = notification.GetService()
			if svc != nil {
				break
			}
			time.Sleep(pollInterval)
		}
		if svc == nil {
			GetLogger().Warn("notification service unavailable, corruption notification not sent")
			return
		}

		notif := notification.NewNotification(
			notification.TypeWarning,
			notification.PriorityCritical,
			"Database Integrity Issue",
			fmt.Sprintf(
				"Database corruption detected during startup. REINDEX recovery failed. Details: %s. "+
					"Please back up your database and consider restoring from a known good backup.",
				truncateResult(integrityResult, 200),
			),
		).WithComponent("database")

		if err := svc.CreateWithMetadata(notif); err != nil {
			GetLogger().Warn("failed to send corruption notification", logger.Error(err))
		}
	})
}

// checkAndLatchCorruption inspects a runtime database error for signs of
// corruption. On first detection it latches the corruption flag, sends a
// single Sentry event, and queues a user notification. Returns true if the
// error is a corruption error.
func (s *SQLiteStore) checkAndLatchCorruption(err error, operation string) bool {
	if err == nil {
		return false
	}
	if !IsDatabaseCorruption(err) {
		return false
	}

	// Only act on the first detection (CompareAndSwap returns true on first flip).
	if !s.dbCorrupted.CompareAndSwap(false, true) {
		// Already latched; suppress duplicate processing.
		return true
	}

	log := GetLogger()
	log.Error("database corruption detected at runtime",
		logger.String("operation", operation),
		logger.Error(err))

	// Cache the result for the health endpoint.
	s.integrityMu.Lock()
	s.integrityResult = err.Error()
	s.integrityMu.Unlock()

	// Send one Sentry event.
	if s.telemetry != nil {
		s.telemetry.CaptureEnhancedError(err, operation, s)
	}

	// Queue user notification.
	s.notifyCorruptionDeferred(err.Error())

	return true
}

// truncateResult shortens a string to maxLen characters, appending an
// ellipsis if truncation occurred.
func truncateResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// corruptionSentryThrottled reports whether the corruption latch is set,
// indicating that further Sentry reports should be suppressed.
func (s *SQLiteStore) corruptionSentryThrottled() bool {
	return s.dbCorrupted.Load()
}
