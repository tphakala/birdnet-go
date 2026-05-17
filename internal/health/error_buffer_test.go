// internal/health/error_buffer_test.go
package health

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEntry(msg string, ts time.Time) *LogEntry {
	return &LogEntry{
		Level:     "error",
		Message:   msg,
		Timestamp: ts,
	}
}

func TestErrorRingBuffer_AddAndCount(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(5)
	assert.Equal(t, 0, buf.Count())

	buf.Add(makeEntry("first", time.Now()))
	assert.Equal(t, 1, buf.Count())

	buf.Add(makeEntry("second", time.Now()))
	assert.Equal(t, 2, buf.Count())
}

func TestErrorRingBuffer_EntriesChronological(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(5)
	base := time.Now()

	for i := range 3 {
		buf.Add(makeEntry(fmt.Sprintf("msg-%d", i), base.Add(time.Duration(i)*time.Second)))
	}

	entries := buf.Entries()
	require.Len(t, entries, 3)
	assert.Equal(t, "msg-0", entries[0].Message)
	assert.Equal(t, "msg-1", entries[1].Message)
	assert.Equal(t, "msg-2", entries[2].Message)
}

func TestErrorRingBuffer_RecentReverseChronological(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(5)
	base := time.Now()

	for i := range 5 {
		buf.Add(makeEntry(fmt.Sprintf("msg-%d", i), base.Add(time.Duration(i)*time.Second)))
	}

	recent := buf.Recent(3)
	require.Len(t, recent, 3)
	// Newest first.
	assert.Equal(t, "msg-4", recent[0].Message)
	assert.Equal(t, "msg-3", recent[1].Message)
	assert.Equal(t, "msg-2", recent[2].Message)
}

func TestErrorRingBuffer_RecentNLargerThanCount(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(10)
	buf.Add(makeEntry("only", time.Now()))

	recent := buf.Recent(100)
	require.Len(t, recent, 1)
	assert.Equal(t, "only", recent[0].Message)
}

func TestErrorRingBuffer_RecentZero(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(5)
	buf.Add(makeEntry("msg", time.Now()))

	recent := buf.Recent(0)
	assert.Empty(t, recent)
}

func TestErrorRingBuffer_Overflow(t *testing.T) {
	t.Parallel()
	const maxSize = 3
	buf := NewErrorRingBuffer(maxSize)
	base := time.Now()

	// Add more entries than the buffer can hold.
	for i := range maxSize + 2 {
		buf.Add(makeEntry(fmt.Sprintf("msg-%d", i), base.Add(time.Duration(i)*time.Second)))
	}

	// Count must be capped at maxSize.
	assert.Equal(t, maxSize, buf.Count())

	// Entries should contain the most recent maxSize messages in chronological order.
	entries := buf.Entries()
	require.Len(t, entries, maxSize)
	assert.Equal(t, "msg-2", entries[0].Message)
	assert.Equal(t, "msg-3", entries[1].Message)
	assert.Equal(t, "msg-4", entries[2].Message)
}

func TestErrorRingBuffer_CountSince(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(10)
	base := time.Now()

	for i := range 5 {
		buf.Add(makeEntry(fmt.Sprintf("msg-%d", i), base.Add(time.Duration(i)*time.Second)))
	}

	// Count entries at or after base+2s: messages 2, 3, 4.
	cutoff := base.Add(2 * time.Second)
	assert.Equal(t, 3, buf.CountSince(cutoff))

	// Count entries at or after base+4s: message 4 only.
	assert.Equal(t, 1, buf.CountSince(base.Add(4*time.Second)))

	// Future cutoff: none.
	assert.Equal(t, 0, buf.CountSince(base.Add(10*time.Second)))

	// Past cutoff: all entries.
	assert.Equal(t, 5, buf.CountSince(base.Add(-time.Second)))
}

func TestErrorRingBuffer_CountSince_OutOfOrder(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(10)
	base := time.Now()

	// Simulate out-of-order timestamps from concurrent callers:
	// entries added as: t+0, t+3, t+1, t+4, t+2
	buf.Add(makeEntry("msg-0", base))
	buf.Add(makeEntry("msg-3", base.Add(3*time.Second)))
	buf.Add(makeEntry("msg-1", base.Add(1*time.Second)))
	buf.Add(makeEntry("msg-4", base.Add(4*time.Second)))
	buf.Add(makeEntry("msg-2", base.Add(2*time.Second)))

	// Count entries at or after base+2s: msg-3 (t+3), msg-4 (t+4), msg-2 (t+2) = 3
	cutoff := base.Add(2 * time.Second)
	assert.Equal(t, 3, buf.CountSince(cutoff))

	// All entries should be counted regardless of insertion order.
	assert.Equal(t, 5, buf.CountSince(base))

	// Future cutoff: none.
	assert.Equal(t, 0, buf.CountSince(base.Add(10*time.Second)))
}

func TestErrorRingBuffer_Clear(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(5)
	for i := range 3 {
		buf.Add(makeEntry(fmt.Sprintf("msg-%d", i), time.Now()))
	}
	require.Equal(t, 3, buf.Count())

	buf.Clear()

	assert.Equal(t, 0, buf.Count())
	assert.Empty(t, buf.Entries())
	assert.Empty(t, buf.Recent(5))
}

func TestErrorRingBuffer_ClearThenRefill(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(5)
	buf.Add(makeEntry("before-clear", time.Now()))
	buf.Clear()

	buf.Add(makeEntry("after-clear", time.Now()))
	assert.Equal(t, 1, buf.Count())
	entries := buf.Entries()
	require.Len(t, entries, 1)
	assert.Equal(t, "after-clear", entries[0].Message)
}

func TestErrorRingBuffer_ConcurrentAdd(t *testing.T) {
	t.Parallel()
	const workers = 10
	const perWorker = 100
	const maxSize = 200

	buf := NewErrorRingBuffer(maxSize)
	var wg sync.WaitGroup

	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range perWorker {
				buf.Add(makeEntry(fmt.Sprintf("w%d-msg%d", id, i), time.Now()))
			}
		}(w)
	}

	wg.Wait()

	// Count should be capped at maxSize; no panic or data race.
	count := buf.Count()
	assert.LessOrEqual(t, count, maxSize)
	assert.Positive(t, count)
}
