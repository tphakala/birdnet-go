package ffmpeg

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBoundedBuffer_DefaultCap verifies that a zero-value boundedBuffer (or one
// with a non-positive limit) is bounded at stderrTailMaxBytes rather than
// growing without limit, which is the regression from issue #4257.
func TestBoundedBuffer_DefaultCap(t *testing.T) {
	t.Parallel()

	for _, limit := range []int{0, -1, -1024} {
		b := &boundedBuffer{limitBytes: limit}
		assert.Equal(t, stderrTailMaxBytes, b.limit(),
			"non-positive limit %d must select the default cap", limit)
	}

	// Write far more than the default cap and confirm retention stays bounded.
	b := &boundedBuffer{} // zero value
	chunk := []byte(strings.Repeat("x", 4096))
	for range (stderrTailMaxBytes / len(chunk)) * 4 { // ~4x the cap
		_, err := b.Write(chunk)
		require.NoError(t, err)
	}
	// Assert the exact retained size, not merely <= cap: after writing many
	// multiples of the cap the buffer must be full at exactly the cap. A bare
	// LessOrEqual would also pass if a regression cleared the buffer on
	// overflow, which is the failure this test exists to catch.
	assert.Len(t, b.buf, stderrTailMaxBytes,
		"retained bytes must be exactly the cap once far more than the cap is written")
}

// TestBoundedBuffer_SubCapacity verifies that writes staying within the cap are
// retained verbatim and in order, exactly like a plain bytes.Buffer.
func TestBoundedBuffer_SubCapacity(t *testing.T) {
	t.Parallel()

	b := &boundedBuffer{limitBytes: 64}
	n, err := b.Write([]byte("hello "))
	require.NoError(t, err)
	assert.Equal(t, 6, n)

	n, err = b.Write([]byte("world"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)

	assert.Equal(t, "hello world", b.String())
	assert.LessOrEqual(t, len(b.buf), b.limit())
}

// TestBoundedBuffer_Overflow verifies that when accumulated writes exceed the
// cap, the oldest bytes are discarded while the most recent trailing bytes are
// preserved in chronological order.
func TestBoundedBuffer_Overflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		limit  int
		writes []string
		want   string
	}{
		{
			name:   "single overflowing pair keeps tail",
			limit:  8,
			writes: []string{"abcdef", "ghij"},
			// "abcdefghij" (10) trimmed to last 8 -> "cdefghij"
			want: "cdefghij",
		},
		{
			name:   "many small writes keep trailing window",
			limit:  5,
			writes: []string{"1", "2", "3", "4", "5", "6", "7"},
			want:   "34567",
		},
		{
			name:   "exact fit keeps everything",
			limit:  6,
			writes: []string{"abc", "def"},
			want:   "abcdef",
		},
		{
			name:   "overflow by one drops first byte",
			limit:  5,
			writes: []string{"abc", "def"},
			// "abcdef" (6) trimmed to last 5 -> "bcdef"
			want: "bcdef",
		},
		{
			name:   "consecutive oversized writes keep last window",
			limit:  4,
			writes: []string{"01234", "56789"},
			// each write alone exceeds the cap: first -> "1234", second -> "6789"
			want: "6789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := &boundedBuffer{limitBytes: tt.limit}
			for _, w := range tt.writes {
				n, err := b.Write([]byte(w))
				require.NoError(t, err)
				assert.Equal(t, len(w), n, "Write must report full length written")
				assert.LessOrEqual(t, len(b.buf), tt.limit,
					"retained bytes must never exceed the cap after write %q", w)
			}
			assert.Equal(t, tt.want, b.String())
		})
	}
}

// TestBoundedBuffer_SingleOversizedWrite verifies that a single write larger
// than the cap retains only its trailing cap bytes and discards any previously
// retained content.
func TestBoundedBuffer_SingleOversizedWrite(t *testing.T) {
	t.Parallel()

	b := &boundedBuffer{limitBytes: 4}
	_, err := b.Write([]byte("older"))
	require.NoError(t, err)

	// A single write that exceeds the cap: keep only its last 4 bytes.
	n, err := b.Write([]byte("0123456789"))
	require.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, "6789", b.String())
	assert.Len(t, b.buf, 4)
}

// TestBoundedBuffer_WriteExactlyCap verifies the boundary where a single write
// equals the cap exactly.
func TestBoundedBuffer_WriteExactlyCap(t *testing.T) {
	t.Parallel()

	b := &boundedBuffer{limitBytes: 4}
	_, err := b.Write([]byte("junk"))
	require.NoError(t, err)

	n, err := b.Write([]byte("ABCD")) // len == cap
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "ABCD", b.String())
}

// TestBoundedBuffer_EmptyWrite verifies that a zero-length write is a no-op that
// preserves existing content.
func TestBoundedBuffer_EmptyWrite(t *testing.T) {
	t.Parallel()

	b := &boundedBuffer{limitBytes: 8}
	_, err := b.Write([]byte("keep"))
	require.NoError(t, err)

	n, err := b.Write(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	n, err = b.Write([]byte{})
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	assert.Equal(t, "keep", b.String())
}

// TestBoundedBuffer_Reset verifies that Reset clears retained content while
// leaving the buffer usable, matching bytes.Buffer.Reset semantics relied on by
// startProcess.
func TestBoundedBuffer_Reset(t *testing.T) {
	t.Parallel()

	b := &boundedBuffer{limitBytes: 8}
	_, err := b.Write([]byte("garbage"))
	require.NoError(t, err)
	require.NotEmpty(t, b.String())

	b.Reset()
	assert.Empty(t, b.String())
	assert.Empty(t, b.buf)

	// Still usable after reset.
	_, err = b.Write([]byte("fresh"))
	require.NoError(t, err)
	assert.Equal(t, "fresh", b.String())
}

// TestBoundedBuffer_PreservesChronologicalOrderUnderChurn drives a long stream
// of distinct lines through the buffer and asserts the retained content is
// always an exact suffix of everything written, so trimming never reorders or
// corrupts data.
func TestBoundedBuffer_PreservesChronologicalOrderUnderChurn(t *testing.T) {
	t.Parallel()

	const limit = 32
	b := &boundedBuffer{limitBytes: limit}
	var full strings.Builder
	for i := range 200 {
		line := []byte("line-" + strings.Repeat("y", i%7) + "\n")
		full.Write(line)
		_, err := b.Write(line)
		require.NoError(t, err)
		require.LessOrEqual(t, len(b.buf), limit)
	}
	// Assert the retained content equals the exact trailing `limit` bytes of
	// the full stream (all ASCII, so byte slicing is safe). This is stronger
	// than strings.HasSuffix(full, b.String()), which is vacuously true if the
	// buffer were ever emptied on overflow.
	require.Len(t, b.buf, limit, "buffer must be full at the cap after churn")
	wholeStream := full.String()
	assert.Equal(t, wholeStream[len(wholeStream)-limit:], b.String(),
		"retained content must equal the exact trailing window of the full stream")
}

// TestThreadSafeWriter_ConcurrentBounded exercises boundedBuffer through
// threadSafeWriter with the shared RWMutex under concurrent writers and a
// concurrent reader, matching how Stream drives it. Run with -race to catch
// data races; also asserts the cap is never exceeded.
func TestThreadSafeWriter_ConcurrentBounded(t *testing.T) {
	t.Parallel()

	buf := &boundedBuffer{limitBytes: 1024}
	var mu sync.RWMutex
	w := &threadSafeWriter{buf: buf, mu: &mu}

	var wg sync.WaitGroup
	const writers = 8
	const perWriter = 500

	for id := range writers {
		wg.Go(func() {
			payload := []byte(strings.Repeat(string(rune('a'+id)), 40))
			for range perWriter {
				_, err := w.Write(payload)
				if err != nil {
					t.Errorf("unexpected write error: %v", err)
					return
				}
			}
		})
	}

	// Concurrent readers, mirroring checkEarlyErrors / quick-exit reads.
	for range 4 {
		wg.Go(func() {
			for range perWriter {
				mu.RLock()
				_ = buf.String()
				mu.RUnlock()
			}
		})
	}

	wg.Wait()

	mu.RLock()
	defer mu.RUnlock()
	assert.LessOrEqual(t, len(buf.buf), 1024,
		"retained bytes must stay within the cap under concurrent writes")
}

// TestStderrLogTail verifies the pure tail-extraction used for the
// stderr-on-exit log field: trailing-byte capping, leading-partial-line drop,
// whitespace trimming, and the maxBytes <= 0 passthrough.
func TestStderrLogTail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{name: "empty", input: "", maxBytes: 16, want: ""},
		{name: "whitespace only", input: "  \n\t\n ", maxBytes: 16, want: ""},
		{
			name:     "under cap returned trimmed",
			input:    "line one\nline two\n",
			maxBytes: 64,
			want:     "line one\nline two",
		},
		{
			name:     "over cap keeps trailing window on line boundary",
			input:    "aaaa\nbbbb\ncccc\ndddd\n",
			maxBytes: 12, // last 12 bytes = "b\ncccc\ndddd\n"; partial "b" line dropped
			want:     "cccc\ndddd",
		},
		{
			// The cut lands exactly on a newline, so the first retained line is
			// complete and must be kept rather than dropped.
			name:     "cut on line boundary keeps complete first line",
			input:    "aaaa\nbbbb\ncccc\n",
			maxBytes: 10, // last 10 bytes = "bbbb\ncccc\n"; cut sits right after a newline
			want:     "bbbb\ncccc",
		},
		{
			name:     "single long line over cap keeps trailing bytes",
			input:    "0123456789abcdef",
			maxBytes: 6,
			want:     "abcdef",
		},
		{
			// The window's only newline is its last byte, so the partial-line
			// drop is skipped (nl+1 < len(s) is false) and TrimSpace removes the
			// trailing newline rather than collapsing to "".
			name:     "trailing newline is last byte in window",
			input:    "xxabc\n",
			maxBytes: 4,
			want:     "abc",
		},
		{
			name:     "non-positive maxBytes returns whole trimmed input",
			input:    "  keep everything  ",
			maxBytes: 0,
			want:     "keep everything",
		},
		{
			name:     "negative maxBytes returns whole trimmed input",
			input:    "\nkeep\n",
			maxBytes: -1,
			want:     "keep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stderrLogTail(tt.input, tt.maxBytes)
			assert.Equal(t, tt.want, got)
			if tt.maxBytes > 0 {
				assert.LessOrEqual(t, len(got), tt.maxBytes,
					"result must not exceed the cap")
			}
		})
	}
}

// TestStream_stderrTailForLog verifies the Stream method reads the captured
// stderr under its mutex, sanitizes FFmpeg memory-address prefixes, returns ""
// when nothing was captured, and bounds the snippet to stderrLogTailMaxBytes.
func TestStream_stderrTailForLog(t *testing.T) {
	t.Parallel()

	t.Run("empty buffer returns empty", func(t *testing.T) {
		t.Parallel()
		s := &Stream{}
		assert.Empty(t, s.stderrTailForLog())
	})

	t.Run("strips ffmpeg memory-address prefix", func(t *testing.T) {
		t.Parallel()
		s := &Stream{}
		_, err := s.stderr.Write([]byte("[rtsp @ 0x55d4a4808980] method DESCRIBE failed: 404 Not Found\n"))
		require.NoError(t, err)

		got := s.stderrTailForLog()
		assert.NotContains(t, got, "0x55d4a4808980", "memory address must be sanitized")
		assert.Contains(t, got, "404 Not Found", "diagnostic message must be preserved")
	})

	t.Run("redacts URL credentials", func(t *testing.T) {
		t.Parallel()
		s := &Stream{}
		_, err := s.stderr.Write([]byte("Error opening input file rtsp://user:secret@cam.example.com:554/stream.\n"))
		require.NoError(t, err)

		got := s.stderrTailForLog()
		// Credentials embedded in a stream URL must never reach the log; the
		// host and path may remain for debugging.
		assert.NotContains(t, got, "secret", "URL password must be redacted")
		assert.NotContains(t, got, "user:", "URL username must be redacted")
	})

	t.Run("redacts credentials that straddle the tail boundary", func(t *testing.T) {
		t.Parallel()
		s := &Stream{}
		// One long line with no newlines (so the partial-line drop cannot remove
		// the fragment), sized so the last stderrLogTailMaxBytes bytes begin right
		// after the URL scheme. If the tail were taken before sanitizing, only
		// "user:secret@..." would remain and evade URL redaction. Sanitizing the
		// whole capture first strips the credential regardless of the cut point.
		url := "rtsp://user:secret@cam.example.com/stream"
		// tail length 2014 makes the cut land at urlStart+7 (just past "rtsp://").
		full := strings.Repeat("x", 50) + url + strings.Repeat("y", stderrLogTailMaxBytes-34)
		_, err := s.stderr.Write([]byte(full))
		require.NoError(t, err)
		require.Greater(t, len(full), stderrLogTailMaxBytes, "input must exceed the cap to force truncation")

		got := s.stderrTailForLog()
		assert.NotContains(t, got, "secret", "credential must be redacted even when the URL straddles the tail cut")
		assert.NotContains(t, got, "user:", "credential username must be redacted across the boundary")
		// The host survives redaction and lies in the retained window, proving the
		// URL region was kept and sanitized rather than simply truncated away.
		assert.Contains(t, got, "cam.example.com", "host should remain after credential redaction")
		assert.LessOrEqual(t, len(got), stderrLogTailMaxBytes)
	})

	t.Run("bounds snippet to the log tail cap", func(t *testing.T) {
		t.Parallel()
		s := &Stream{}
		// Write far more than the log tail cap as distinct newline-terminated lines.
		var written strings.Builder
		for i := range 4000 {
			line := "ffmpeg diagnostic line number " + strings.Repeat("x", i%5) + "\n"
			written.WriteString(line)
			_, err := s.stderr.Write([]byte(line))
			require.NoError(t, err)
		}
		got := s.stderrTailForLog()
		assert.NotEmpty(t, got)
		assert.LessOrEqual(t, len(got), stderrLogTailMaxBytes,
			"logged tail must not exceed the log tail cap")
		// The tail must be a suffix of the full written stream (all ASCII).
		assert.True(t, strings.HasSuffix(strings.TrimSpace(written.String()), got),
			"logged tail must be the trailing portion of the captured stderr")
	})
}
