// Package migration provides background migration of legacy data to the v2 schema.
package migration

import (
	"fmt"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// MigrationTelemetry reports migration lifecycle events to Sentry.
// All methods are nil-safe — if the receiver is nil, calls are no-ops.
// Consent is handled by the telemetry package (checks settings.Sentry.Enabled).
type MigrationTelemetry struct {
	dbType string // "sqlite" or "mysql"
}

// NewMigrationTelemetry creates a new migration telemetry reporter.
func NewMigrationTelemetry(dbType string) *MigrationTelemetry {
	return &MigrationTelemetry{dbType: dbType}
}

// ReportStarted reports that a migration has been initiated.
func (mt *MigrationTelemetry) ReportStarted(totalRecords int64) {
	if mt == nil {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "migration")
		scope.SetTag("db_type", mt.dbType)
		scope.SetTag("outcome", "started")
		scope.SetFingerprint([]string{"migration", "started"})

		scope.SetContext("migration", map[string]any{
			"total_records": totalRecords,
			"db_type":       mt.dbType,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Database migration started (%d records)", totalRecords),
			sentry.LevelInfo,
			"migration",
		)
	})
}

// ReportCompleted reports that a migration has completed successfully.
// recordsPerSecond should be the overall average rate (totalMigrated / duration),
// not the sliding window rate used for UI ETA display.
func (mt *MigrationTelemetry) ReportCompleted(totalMigrated int64, duration time.Duration, recordsPerSecond float64, dirtyIDCount int64) {
	if mt == nil {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "migration")
		scope.SetTag("db_type", mt.dbType)
		scope.SetTag("outcome", "success")
		scope.SetFingerprint([]string{"migration", "completed"})

		scope.SetContext("migration", map[string]any{
			"total_migrated":     totalMigrated,
			"duration_seconds":   duration.Seconds(),
			"records_per_second": recordsPerSecond,
			"dirty_id_count":     dirtyIDCount,
			"db_type":            mt.dbType,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Database migration completed (%d records in %s)", totalMigrated, formatDurationHuman(duration)),
			sentry.LevelInfo,
			"migration",
		)
	})
}

// ReportValidationFailed reports that migration validation has failed.
func (mt *MigrationTelemetry) ReportValidationFailed(legacyCount, v2Count, dirtyIDCount int64, errMsg string) {
	if mt == nil {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "migration")
		scope.SetTag("db_type", mt.dbType)
		scope.SetTag("outcome", "validation_failed")
		scope.SetFingerprint([]string{"migration", "validation-failed"})

		diff := legacyCount - v2Count
		scope.SetContext("migration", map[string]any{
			"legacy_count":   legacyCount,
			"v2_count":       v2Count,
			"count_diff":     diff,
			"dirty_id_count": dirtyIDCount,
			"error":          privacy.ScrubMessage(errMsg),
			"db_type":        mt.dbType,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Migration validation failed (legacy=%d, v2=%d, diff=%d)", legacyCount, v2Count, diff),
			sentry.LevelError,
			"migration",
		)
	})
}

// ReportAutoPaused reports that the migration was auto-paused due to consecutive errors.
func (mt *MigrationTelemetry) ReportAutoPaused(consecutiveErrors int, lastErr error, migratedSoFar, totalRecords int64) {
	if mt == nil {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "migration")
		scope.SetTag("db_type", mt.dbType)
		scope.SetTag("outcome", "auto_paused")
		// Include error type in fingerprint so different root causes create separate Sentry issues
		scope.SetFingerprint([]string{"migration", "auto-paused", fmt.Sprintf("%T", lastErr)})

		var progressPercent float64
		if totalRecords > 0 {
			progressPercent = float64(migratedSoFar) / float64(totalRecords) * 100
		}

		scope.SetContext("migration", map[string]any{
			"consecutive_errors": consecutiveErrors,
			"last_error":         privacy.ScrubMessage(lastErr.Error()),
			"error_type":         fmt.Sprintf("%T", lastErr),
			"migrated_so_far":    migratedSoFar,
			"total_records":      totalRecords,
			"progress_percent":   progressPercent,
			"db_type":            mt.dbType,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Migration auto-paused after %d consecutive errors at %.1f%% progress", consecutiveErrors, progressPercent),
			sentry.LevelWarning,
			"migration",
		)
	})
}

// ReportCancelled reports that the migration was cancelled by the user.
func (mt *MigrationTelemetry) ReportCancelled(migratedSoFar, totalRecords int64) {
	if mt == nil {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "migration")
		scope.SetTag("db_type", mt.dbType)
		scope.SetTag("outcome", "cancelled")
		scope.SetFingerprint([]string{"migration", "cancelled"})

		var progressPercent float64
		if totalRecords > 0 {
			progressPercent = float64(migratedSoFar) / float64(totalRecords) * 100
		}

		scope.SetContext("migration", map[string]any{
			"migrated_so_far":  migratedSoFar,
			"total_records":    totalRecords,
			"progress_percent": progressPercent,
			"db_type":          mt.dbType,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Migration cancelled at %.1f%% progress (%d/%d records)", progressPercent, migratedSoFar, totalRecords),
			sentry.LevelWarning,
			"migration",
		)
	})
}

// ReportPanic reports that the migration worker goroutine panicked.
// This captures the panic value and ensures it reaches Sentry even if the
// worker dies. Called from the deferred recovery handler in run().
func (mt *MigrationTelemetry) ReportPanic(panicValue any) {
	if mt == nil {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "migration")
		scope.SetTag("db_type", mt.dbType)
		scope.SetTag("outcome", "panic")
		scope.SetFingerprint([]string{"migration", "panic", fmt.Sprintf("%v", panicValue)})

		scope.SetContext("migration", map[string]any{
			"panic_value": fmt.Sprintf("%v", panicValue),
			"panic_type":  fmt.Sprintf("%T", panicValue),
			"db_type":     mt.dbType,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Migration worker panic: %v", panicValue),
			sentry.LevelFatal,
			"migration",
		)
	})
}

// formatDurationHuman formats a duration as a human-readable string (e.g., "5m 30s").
func formatDurationHuman(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
