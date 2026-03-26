package datastore

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

const (
	// retryMaxAttempts is the number of times to retry a write operation
	// that fails with a transient database lock or deadlock error.
	retryMaxAttempts = 5

	// retryBaseDelay is the initial backoff delay between retries.
	// Subsequent retries double the delay (exponential backoff).
	retryBaseDelay = 500 * time.Millisecond
)

// IsTransientDBError checks if an error (or any error in its unwrap chain) is a
// transient database lock or deadlock error that is safe to retry. Covers both
// SQLite (database is locked, SQLITE_BUSY) and MySQL (deadlock detected, lock
// wait timeout). Wrapped errors (e.g. EnhancedError) are unwrapped so the
// underlying message is inspected at every level.
func IsTransientDBError(err error) bool {
	if err == nil {
		return false
	}
	// Walk the error chain -- wrapped errors may hide the transient message.
	for e := err; e != nil; e = errors.Unwrap(e) {
		if isDatabaseLocked(e) || isDeadlock(e) {
			return true
		}
	}
	return false
}

// retryBackoff computes a jittered exponential backoff delay, logs a warning,
// records the retry metric, and sleeps. It is called by RetryOnLock and
// RetryTransactionOnLock when a real retry is about to happen.
func retryBackoff(attempt int, operation string, lastErr error, m *Metrics) {
	if m != nil {
		m.RecordTransactionRetry(operation, "database_locked")
	}
	backoff := retryBaseDelay * time.Duration(1<<uint(attempt)) //nolint:gosec // G115: attempt bounded by retryMaxAttempts
	// Add 0-25% jitter to avoid thundering herd when multiple writers contend.
	jitter := time.Duration(rand.Float64() * 0.25 * float64(backoff)) //nolint:gosec // G404: math/rand is fine for jitter
	delay := backoff + jitter
	GetLogger().Warn("retrying after database lock error",
		logger.String("operation", operation),
		logger.Int("attempt", attempt+1),
		logger.Int("max_attempts", retryMaxAttempts),
		logger.Int64("backoff_ms", delay.Milliseconds()),
		logger.Error(lastErr))
	time.Sleep(delay)
}

// recordRetryExhaustion records metrics when all retry attempts have been
// exhausted. Called by both RetryOnLock and RetryTransactionOnLock.
func recordRetryExhaustion(m *Metrics, operation string, start time.Time) {
	if m == nil {
		return
	}
	m.RecordLockRetriesExhausted(operation)
	m.RecordLockWaitTime("database", time.Since(start).Seconds())
	m.RecordLockContention("database", "max_retries_exhausted")
}

// RetryOnLock executes fn and retries up to retryMaxAttempts times if the
// error is a transient database lock or deadlock error. Uses exponential
// backoff between retries. Returns the first non-transient error or the
// last error after all retries are exhausted.
//
// If metrics is non-nil, each retry attempt and exhaustion are recorded.
func RetryOnLock(operation string, fn func() error, metrics *Metrics) error {
	start := time.Now()
	var err error
	for attempt := range retryMaxAttempts {
		err = fn()
		if err == nil {
			if attempt > 0 && metrics != nil {
				metrics.RecordLockWaitTime("database", time.Since(start).Seconds())
				metrics.RecordLockContention("database", "retry_succeeded")
			}
			return nil
		}

		// Only retry on transient lock/deadlock errors; bail immediately on others.
		if !IsTransientDBError(err) {
			return err
		}

		// Only record the retry metric and sleep when there will be a real retry.
		if attempt < retryMaxAttempts-1 {
			retryBackoff(attempt, operation, err, metrics)
		}
	}

	// All retries exhausted -- record the critical exhaustion metric.
	recordRetryExhaustion(metrics, operation, start)
	return err
}

// RetryTransactionOnLock wraps a transaction lifecycle (Begin/fn/Commit) inside
// the standard retry loop. Each retry starts a fresh transaction so that a
// failed attempt's rolled-back state does not leak into the next try.
//
// The caller-supplied fn receives the open *gorm.DB transaction and should
// execute its queries on it. It must NOT call Commit or Rollback -- that is
// handled by this helper.
//
// If metrics is non-nil, retry attempts, exhaustion, and lock wait duration
// are recorded.
func RetryTransactionOnLock(db *gorm.DB, operation string, fn func(tx *gorm.DB) error, metrics *Metrics) error {
	start := time.Now()
	var lastErr error

	for attempt := range retryMaxAttempts {
		tx := db.Begin()
		if tx.Error != nil {
			lastErr = tx.Error
			if !IsTransientDBError(tx.Error) {
				return tx.Error
			}
			// Begin itself hit a lock error; fall through to retry logic.
		} else {
			// Execute the caller's work inside the transaction with panic recovery.
			fnErr := func() (retErr error) {
				defer func() {
					if r := recover(); r != nil {
						tx.Rollback()
						retErr = fmt.Errorf("panic in transaction: %v", r)
					}
				}()
				return fn(tx)
			}()

			if fnErr != nil {
				tx.Rollback()
				lastErr = fnErr
				if !IsTransientDBError(fnErr) {
					return fnErr
				}
				// Transient error; fall through to retry logic.
			} else {
				// Commit the transaction.
				if err := tx.Commit().Error; err != nil {
					tx.Rollback() // Defensive rollback after failed commit.
					lastErr = err
					if !IsTransientDBError(err) {
						return err
					}
					// Commit hit a lock error; fall through to retry logic.
				} else {
					// Success.
					if attempt > 0 && metrics != nil {
						metrics.RecordLockWaitTime("database", time.Since(start).Seconds())
						metrics.RecordLockContention("database", "retry_succeeded")
					}
					return nil
				}
			}
		}

		// Only record the retry metric and sleep when there will be a real retry.
		if attempt < retryMaxAttempts-1 {
			retryBackoff(attempt, operation, lastErr, metrics)
		}
	}

	// All retries exhausted -- record the critical exhaustion metric.
	recordRetryExhaustion(metrics, operation, start)
	return lastErr
}

// buildSQLiteDSN constructs a SQLite DSN string with pragma query parameters.
// Handles the case where dbPath may already contain query parameters (e.g.,
// "file::memory:?cache=shared") by using "&" instead of "?" as the separator.
func buildSQLiteDSN(dbPath, pragmas string) string {
	sep := "?"
	if strings.Contains(dbPath, "?") {
		sep = "&"
	}
	return dbPath + sep + pragmas
}
