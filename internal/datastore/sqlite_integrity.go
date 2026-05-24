package datastore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// performStartupIntegrityCheck runs PRAGMA quick_check synchronously during
// Open(). If corruption is found, it attempts REINDEX auto-recovery. On
// failure a Sentry event is sent, the corruption flag is latched, and a
// deferred notification is queued. No error is returned: the application
// continues in degraded mode so the web UI remains accessible.
func (s *SQLiteStore) performStartupIntegrityCheck() {
	result := s.runQuickCheck()

	s.integrityMu.Lock()
	s.integrityResult = result
	s.integrityMu.Unlock()

	if result == "ok" {
		GetLogger().Debug("startup integrity check passed")
		return
	}

	GetLogger().Warn("startup integrity check detected corruption",
		logger.String("result", truncateResult(result, 200)))

	if s.attemptAutoRecovery() {
		GetLogger().Info("REINDEX auto-recovery succeeded, database integrity restored")
		return
	}

	GetLogger().Error("REINDEX auto-recovery failed, database flagged as corrupted",
		logger.String("integrity_result", truncateResult(result, 200)))

	// Send Sentry event BEFORE latching so the suppression guard in
	// CaptureEnhancedError does not block this first report.
	if s.telemetry != nil {
		enhancedErr := errors.Newf("startup integrity check failed: %s", truncateResult(result, 500)).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Priority(errors.PriorityCritical).
			Context("operation", "startup_integrity_check").
			Context("integrity_result", truncateResult(result, 200)).
			Build()
		s.telemetry.CaptureEnhancedError(enhancedErr, "startup_integrity_check", s)
	}

	s.dbCorrupted.Store(true)

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
	GetLogger().Info("attempting REINDEX auto-recovery")

	if err := s.DB.Exec("REINDEX").Error; err != nil {
		GetLogger().Error("REINDEX command failed", logger.Error(err))
		return false
	}

	result := s.runQuickCheck()

	s.integrityMu.Lock()
	s.integrityResult = result
	s.integrityMu.Unlock()

	return result == "ok"
}

// notifyCorruptionDeferred spawns a goroutine that polls for the notification
// service (up to 30 seconds) and sends a persistent warning notification.
// The notification service may not be initialized when Open() runs, so this
// must be deferred. The goroutine respects the monitoring context for clean
// shutdown.
func (s *SQLiteStore) notifyCorruptionDeferred(integrityResult string) {
	// Use a dedicated context with timeout instead of monitoringCtx.
	// StartMonitoring cancels and replaces monitoringCtx shortly after
	// Open() returns, which would kill this goroutine before it can send
	// the notification.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	s.monitoringWg.Go(func() {
		defer cancel()

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		var svc *notification.Service
		for {
			svc = notification.GetService()
			if svc != nil {
				break
			}
			select {
			case <-ctx.Done():
				GetLogger().Warn("notification service unavailable, corruption notification not sent")
				return
			case <-ticker.C:
			}
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
// corruption. On first detection it sends a single Sentry event, latches the
// corruption flag, and queues a user notification. Returns true if the error
// is a corruption error (regardless of whether the latch was already set).
func (s *SQLiteStore) checkAndLatchCorruption(err error, operation string) bool {
	if err == nil || !IsDatabaseCorruption(err) {
		return false
	}

	if s.dbCorrupted.Load() {
		return true
	}

	GetLogger().Error("database corruption detected at runtime",
		logger.String("operation", operation),
		logger.Error(err))

	s.integrityMu.Lock()
	s.integrityResult = err.Error()
	s.integrityMu.Unlock()

	// Send Sentry event BEFORE latching so the suppression guard in
	// CaptureEnhancedError does not block this first report.
	if s.telemetry != nil {
		s.telemetry.CaptureEnhancedError(err, operation, s)
	}

	s.dbCorrupted.Store(true)

	s.notifyCorruptionDeferred(err.Error())

	return true
}

// truncateResult shortens a string to maxLen characters, appending an
// ellipsis if truncation occurred.
func truncateResult(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 4 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}
