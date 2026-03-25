package datastore

import (
	"fmt"
	"testing"
)

func TestRetryOnLock_SucceedsImmediately(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retryOnLock("test_op", func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryOnLock_RetriesOnLockError(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retryOnLock("test_op", func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("database is locked")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error after retries, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetryOnLock_DoesNotRetryNonLockError(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retryOnLock("test_op", func() error {
		calls++
		return fmt.Errorf("some other error")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry for non-lock error), got %d", calls)
	}
}

func TestRetryOnLock_ExhaustsRetries(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retryOnLock("test_op", func() error {
		calls++
		return fmt.Errorf("database is locked")
	})
	if err == nil {
		t.Fatal("expected error after exhausted retries, got nil")
	}
	if calls != retryMaxAttempts {
		t.Fatalf("expected %d calls, got %d", retryMaxAttempts, calls)
	}
}

func TestRetryOnLock_RetriesOnSQLiteBusy(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retryOnLock("test_op", func() error {
		calls++
		if calls < 2 {
			return fmt.Errorf("SQLITE_BUSY (5)")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error after retry, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}
