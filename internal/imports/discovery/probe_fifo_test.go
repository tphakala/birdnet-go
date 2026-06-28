//go:build unix

package discovery

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProbe_SymlinkToFIFOReturnsOpenFailed verifies that a symlink pointing to
// a FIFO (or any non-regular file) is rejected. os.Stat follows the symlink;
// the resulting mode is not regular, so Probe rejects it before any open.
func TestProbe_SymlinkToFIFOReturnsOpenFailed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	link := filepath.Join(dir, "birds.db")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}
	if err := os.Symlink(fifo, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	got := Probe(t.Context(), link)
	assert.False(t, got.Valid)
	assert.Equal(t, ReasonOpenFailed, got.Reason, "symlink to FIFO must be rejected with open_failed")
}
