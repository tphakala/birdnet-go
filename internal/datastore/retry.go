package datastore

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// retryMaxAttempts is the number of times to retry a write operation
	// that fails with a transient SQLite "database is locked" error.
	retryMaxAttempts = 3

	// retryBaseDelay is the initial backoff delay between retries.
	// Subsequent retries double the delay (exponential backoff).
	retryBaseDelay = 50 * time.Millisecond
)

// retryOnLock executes fn and retries up to retryMaxAttempts times if the
// error is a transient SQLite lock error. Uses exponential backoff between
// retries. Returns the first non-lock error or the last error after all
// retries are exhausted.
func retryOnLock(operation string, fn func() error) error {
	var err error
	for attempt := range retryMaxAttempts {
		err = fn()
		if err == nil {
			return nil
		}

		// Only retry on transient lock errors; bail immediately on others.
		if !isDatabaseLocked(err) {
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
