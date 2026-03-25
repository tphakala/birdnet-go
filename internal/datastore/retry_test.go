package datastore

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			err := retryOnLock("test_op", func() error {
				calls++
				return tc.fn(&calls)
			})

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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, isTransientDBError(tc.err))
		})
	}
}
