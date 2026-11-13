//go:build unix

package discovery

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// fifoLookupTimeout bounds how long TestResolveAudioRoot_FIFOByDateIsSkipped
// waits for the search. A search that opens the FIFO blocks until a writer
// appears, so the bound only turns a hang into a failure.
const fifoLookupTimeout = 10 * time.Second

// TestResolveAudioRoot_FIFOByDateIsSkipped verifies that a By_Date that is a
// FIFO does not block the search: opening a FIFO without a writer waits for
// one, and the search runs while the import slot is held.
func TestResolveAudioRoot_FIFOByDateIsSkipped(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	scripts := filepath.Join(base, "home", "BirdNET-Pi", "scripts")
	require.NoError(t, os.MkdirAll(filepath.Join(scripts, extractedDirName), 0o755))
	if err := syscall.Mkfifo(filepath.Join(scripts, extractedDirName, byDateDirName), 0o600); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}
	birdSongs := filepath.Join(base, "home", birdSongsDirName)
	require.NoError(t, os.MkdirAll(filepath.Join(birdSongs, audioRootTestTree), 0o755))

	type result struct {
		root  string
		found bool
	}
	done := make(chan result, 1)
	go func() {
		root, found := ResolveAudioRoot(filepath.Join(scripts, "birds.db"), "")
		done <- result{root, found}
	}()
	select {
	case got := <-done:
		assert.True(t, got.found)
		assert.Equal(t, birdSongs, got.root)
	case <-time.After(fifoLookupTimeout):
		t.Fatal("ResolveAudioRoot blocked on a FIFO By_Date")
	}
}
