//go:build linux

package discovery

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_RejectsFifoWithoutBlocking verifies Probe returns quickly with
// Valid=false when the path is a named pipe. os.Open on a FIFO blocks until a
// writer opens the other end; the Lstat + regular-file guard must short-circuit
// before any open attempt.
func TestProbe_RejectsFifoWithoutBlocking(t *testing.T) {
	t.Parallel()
	fifo := filepath.Join(t.TempDir(), "birds.db")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	done := make(chan SourceCandidate, 1)
	go func() { done <- Probe(t.Context(), fifo) }()
	select {
	case got := <-done:
		assert.False(t, got.Valid, "FIFO must not be treated as a valid source")
	case <-time.After(2 * time.Second):
		t.Fatal("Probe blocked on a FIFO")
	}
}
