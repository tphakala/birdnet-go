//go:build unix

package securefs

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fifoOpenHangGuard bounds how long the test waits for OpenRegularRel before
// treating the call as blocked on the FIFO.
const fifoOpenHangGuard = 10 * time.Second

// TestOpenRegularRel_FIFODoesNotBlock verifies that a FIFO at the requested
// path is rejected instead of blocking the open until a writer appears.
func TestOpenRegularRel_FIFODoesNotBlock(t *testing.T) {
	t.Parallel()
	sfs, base := setupSecureFS(t)
	require.NoError(t, syscall.Mkfifo(filepath.Join(base, "pipe.wav"), 0o600))

	done := make(chan error, 1)
	go func() {
		f, err := sfs.OpenRegularRel("pipe.wav")
		if f != nil {
			_ = f.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrNotRegularFile)
	case <-time.After(fifoOpenHangGuard):
		t.Fatal("OpenRegularRel blocked on a FIFO")
	}
}
