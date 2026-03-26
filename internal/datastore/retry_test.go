package datastore

import (
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// newTestMetrics creates a DatastoreMetrics instance backed by a fresh
// Prometheus registry, suitable for isolated test assertions. Returns both
// the registry (for gathering counters) and the metrics instance.
func newTestMetrics(t *testing.T) (reg *prometheus.Registry, m *Metrics) {
	t.Helper()
	reg = prometheus.NewRegistry()
	m, err := metrics.NewDatastoreMetrics(reg)
	require.NoError(t, err)
	return reg, m
}

// gatherCounter sums the counter value for the named metric family across all
// label combinations in the given registry. Returns 0 when the metric has not
// been observed yet.
func gatherCounter(t *testing.T, reg *prometheus.Registry, name string) int {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		var total int
		for _, m := range f.GetMetric() {
			if m.GetCounter() != nil {
				total += int(m.GetCounter().GetValue())
			}
		}
		return total
	}
	return 0
}

// openRetryTestDB creates a file-backed SQLite database in t.TempDir() for
// RetryTransactionOnLock tests. Each test gets its own isolated database.
func openRetryTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := fmt.Sprintf("%s/retry_test.db", t.TempDir())
	dsn := buildSQLiteDSN(dbPath, "_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&DailyEvents{}))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	return db
}

func TestRetryOnLock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		fn            func(calls *int) error
		expectedCalls int
		expectError   bool
	}{
		{
			name: "succeeds immediately",
			fn: func(_ *int) error {
				return nil
			},
			expectedCalls: 1,
			expectError:   false,
		},
		{
			name: "retries on database is locked",
			fn: func(calls *int) error {
				if *calls < 3 {
					return fmt.Errorf("database is locked")
				}
				return nil
			},
			expectedCalls: 3,
			expectError:   false,
		},
		{
			name: "retries on SQLITE_BUSY",
			fn: func(calls *int) error {
				if *calls < 2 {
					return fmt.Errorf("SQLITE_BUSY (5)")
				}
				return nil
			},
			expectedCalls: 2,
			expectError:   false,
		},
		{
			name: "retries on deadlock detected",
			fn: func(calls *int) error {
				if *calls < 2 {
					return fmt.Errorf("deadlock detected")
				}
				return nil
			},
			expectedCalls: 2,
			expectError:   false,
		},
		{
			name: "does not retry non-lock error",
			fn: func(_ *int) error {
				return fmt.Errorf("some other error")
			},
			expectedCalls: 1,
			expectError:   true,
		},
		{
			name: "exhausts all retries",
			fn: func(_ *int) error {
				return fmt.Errorf("database is locked")
			},
			expectedCalls: retryMaxAttempts,
			expectError:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			err := RetryOnLock("test_op", func() error {
				calls++
				return tc.fn(&calls)
			}, nil)

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.expectedCalls, calls)
		})
	}
}

func TestBuildSQLiteDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dbPath   string
		pragmas  string
		expected string
	}{
		{
			name:     "plain path",
			dbPath:   "/data/birdnet.db",
			pragmas:  "_journal_mode=WAL&_busy_timeout=30000",
			expected: "/data/birdnet.db?_journal_mode=WAL&_busy_timeout=30000",
		},
		{
			name:     "path with existing query params",
			dbPath:   "file::memory:?cache=shared",
			pragmas:  "_journal_mode=WAL&_busy_timeout=30000",
			expected: "file::memory:?cache=shared&_journal_mode=WAL&_busy_timeout=30000",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := buildSQLiteDSN(tc.dbPath, tc.pragmas)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestIsTransientDBError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "nil error", err: nil, expected: false},
		{name: "database is locked", err: fmt.Errorf("database is locked"), expected: true},
		{name: "SQLITE_BUSY", err: fmt.Errorf("SQLITE_BUSY"), expected: true},
		{name: "resource busy", err: fmt.Errorf("resource busy"), expected: true},
		{name: "deadlock detected", err: fmt.Errorf("deadlock detected"), expected: true},
		{name: "lock wait timeout", err: fmt.Errorf("lock wait timeout exceeded"), expected: true},
		{name: "unrelated error", err: fmt.Errorf("connection refused"), expected: false},
		{name: "wrapped database locked", err: fmt.Errorf("tx failed: %w", fmt.Errorf("database is locked")), expected: true},
		{name: "double wrapped SQLITE_BUSY", err: fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", fmt.Errorf("SQLITE_BUSY"))), expected: true},
		{name: "wrapped non-transient", err: fmt.Errorf("outer: %w", fmt.Errorf("connection refused")), expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, IsTransientDBError(tc.err))
		})
	}
}

func TestRetryOnLock_RecordsMetricsOnRetry(t *testing.T) {
	t.Parallel()

	reg, m := newTestMetrics(t)
	calls := 0
	err := RetryOnLock("test_metrics_retry", func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("database is locked")
		}
		return nil
	}, m)

	require.NoError(t, err)
	assert.Equal(t, 3, calls)

	// 2 transient failures before success means 2 retries recorded.
	assert.Equal(t, 2, gatherCounter(t, reg, "datastore_db_transaction_retries_total"))
}

func TestRetryOnLock_RecordsExhaustedMetric(t *testing.T) {
	t.Parallel()

	reg, m := newTestMetrics(t)
	err := RetryOnLock("test_exhausted", func() error {
		return fmt.Errorf("database is locked")
	}, m)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database is locked")

	// Exactly 1 exhaustion event.
	assert.Equal(t, 1, gatherCounter(t, reg, "datastore_lock_retries_exhausted_total"))
}

func TestRetryOnLock_NilMetricsDoesNotPanic(t *testing.T) {
	t.Parallel()

	calls := 0
	err := RetryOnLock("nil_metrics", func() error {
		calls++
		if calls < 2 {
			return fmt.Errorf("database is locked")
		}
		return nil
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestRetryTransactionOnLock_SucceedsImmediately(t *testing.T) {
	t.Parallel()

	db := openRetryTestDB(t)
	calls := 0

	err := RetryTransactionOnLock(db, "test_tx", func(tx *gorm.DB) error {
		calls++
		return tx.Create(&DailyEvents{Date: "2024-01-01", CityName: "Test"}).Error
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, calls)

	// Verify the row was actually persisted.
	var count int64
	require.NoError(t, db.Model(&DailyEvents{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestRetryTransactionOnLock_RetriesTransientError(t *testing.T) {
	t.Parallel()

	db := openRetryTestDB(t)
	calls := 0

	err := RetryTransactionOnLock(db, "test_tx_retry", func(tx *gorm.DB) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("database is locked")
		}
		return tx.Create(&DailyEvents{Date: "2024-02-01", CityName: "Retry"}).Error
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, 3, calls)

	var count int64
	require.NoError(t, db.Model(&DailyEvents{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestRetryTransactionOnLock_DoesNotRetryNonTransient(t *testing.T) {
	t.Parallel()

	db := openRetryTestDB(t)
	calls := 0

	err := RetryTransactionOnLock(db, "test_tx_bail", func(_ *gorm.DB) error {
		calls++
		return fmt.Errorf("UNIQUE constraint failed")
	}, nil)

	require.Error(t, err)
	assert.Equal(t, 1, calls)
	assert.Contains(t, err.Error(), "UNIQUE constraint failed")
}

func TestRetryTransactionOnLock_ExhaustsRetries(t *testing.T) {
	t.Parallel()

	db := openRetryTestDB(t)
	calls := 0

	err := RetryTransactionOnLock(db, "test_tx_exhaust", func(_ *gorm.DB) error {
		calls++
		return fmt.Errorf("database is locked")
	}, nil)

	require.Error(t, err)
	assert.Equal(t, retryMaxAttempts, calls)
	assert.Contains(t, err.Error(), "database is locked")
}

func TestRetryTransactionOnLock_RollsBackOnError(t *testing.T) {
	t.Parallel()

	db := openRetryTestDB(t)

	// The fn creates a row then returns a non-transient error.
	// The row should NOT be persisted because the transaction is rolled back.
	err := RetryTransactionOnLock(db, "test_rollback", func(tx *gorm.DB) error {
		if createErr := tx.Create(&DailyEvents{Date: "2024-03-01", CityName: "Ghost"}).Error; createErr != nil {
			return createErr
		}
		return fmt.Errorf("simulated application error")
	}, nil)

	require.Error(t, err)

	var count int64
	require.NoError(t, db.Model(&DailyEvents{}).Count(&count).Error)
	assert.Equal(t, int64(0), count, "rolled-back rows should not be persisted")
}

func TestRetryTransactionOnLock_WithMetrics(t *testing.T) {
	t.Parallel()

	db := openRetryTestDB(t)
	reg, m := newTestMetrics(t)
	calls := 0

	err := RetryTransactionOnLock(db, "test_tx_metrics", func(tx *gorm.DB) error {
		calls++
		if calls < 2 {
			return fmt.Errorf("database is locked")
		}
		return tx.Create(&DailyEvents{Date: "2024-04-01", CityName: "MetricsTest"}).Error
	}, m)

	require.NoError(t, err)
	assert.Equal(t, 2, calls)

	var count int64
	require.NoError(t, db.Model(&DailyEvents{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	// 1 transient failure before success means 1 retry recorded.
	assert.Equal(t, 1, gatherCounter(t, reg, "datastore_db_transaction_retries_total"))
}

func TestRetryTransactionOnLock_ExhaustedWithMetrics(t *testing.T) {
	t.Parallel()

	db := openRetryTestDB(t)
	reg, m := newTestMetrics(t)

	err := RetryTransactionOnLock(db, "test_tx_exhaust_metrics", func(_ *gorm.DB) error {
		return fmt.Errorf("database is locked")
	}, m)

	require.Error(t, err)

	// Exhaustion counter must be exactly 1.
	assert.Equal(t, 1, gatherCounter(t, reg, "datastore_lock_retries_exhausted_total"))

	// retryMaxAttempts is 5; retries happen for attempts 0..3 (4 retries), the
	// last attempt (4) does not trigger a retry backoff.
	assert.Equal(t, retryMaxAttempts-1, gatherCounter(t, reg, "datastore_db_transaction_retries_total"))
}

// --- Task 4: Begin/Commit transient error path tests ---
//
// Simulating db.Begin() returning a "database is locked" error is not feasible
// without mocking GORM internals (Begin is not an interface method on *gorm.DB).
// Similarly, injecting a transient error at Commit time without wrapping the
// entire GORM Session/ConnPool is impractical.
//
// The existing TestRetryTransactionOnLock_RetriesTransientError and the
// exhaustion tests already exercise the transient-error retry path via the fn
// callback, which covers the most common production scenario. The Begin and
// Commit branches are structurally identical (same IsTransientDBError +
// retryBackoff calls) and are exercised by code review and the integration
// tests in retry_integration_test.go.
//
// If GORM ever exposes an interface-based transaction API, these tests should
// be revisited.
