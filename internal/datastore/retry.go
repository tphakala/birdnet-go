package datastore

import (
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// retryMaxAttempts is the number of times to retry a write operation
	// that fails with a transient database lock or deadlock error.
	// Matches the retry budget used by the Save() method.
	retryMaxAttempts = 5

	// retryBaseDelay is the initial backoff delay between retries.
	// Subsequent retries double the delay (exponential backoff).
	// Matches the retry budget used by the Save() method.
	retryBaseDelay = 500 * time.Millisecond
)

// isTransientDBError checks if an error is a transient database lock or
// deadlock error that is safe to retry. Covers both SQLite (database is
// locked, SQLITE_BUSY) and MySQL (deadlock detected, lock wait timeout).
func isTransientDBError(err error) bool {
	if err == nil {
		return false
	}
	return isDatabaseLocked(err) || isDeadlock(err)
}

// retryOnLock executes fn and retries up to retryMaxAttempts times if the
// error is a transient database lock or deadlock error. Uses exponential
// backoff between retries. Returns the first non-transient error or the
// last error after all retries are exhausted.
func retryOnLock(operation string, fn func() error) error {
	var err error
	for attempt := range retryMaxAttempts {
		err = fn()
		if err == nil {
			return nil
		}

		// Only retry on transient lock/deadlock errors; bail immediately on others.
		if !isTransientDBError(err) {
			return err
		}

		// Don't sleep after the last attempt
		if attempt < retryMaxAttempts-1 {
			backoff := retryBaseDelay * time.Duration(1<<uint(attempt)) //nolint:gosec // G115: attempt bounded by retryMaxAttempts
			GetLogger().Warn("retrying after database lock error",
				logger.String("operation", operation),
				logger.Int("attempt", attempt+1),
				logger.Int("max_attempts", retryMaxAttempts),
				logger.Int64("backoff_ms", backoff.Milliseconds()),
				logger.Error(err))
			time.Sleep(backoff)
		}
	}
	return err
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
