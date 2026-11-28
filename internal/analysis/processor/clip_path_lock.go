package processor

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"sync"
)

// clipPathLockStripes is the number of locks clip paths are spread over. Clip
// names collide only rarely (same species, rounded confidence and second), so a
// fixed set of stripes keeps the locking bounded without per-path bookkeeping.
const clipPathLockStripes = 256

// clipPathStripe serialises writing a clip at a path with removing a clip
// there, and counts the writes made under it.
type clipPathStripe struct {
	mu sync.Mutex
	// writes counts the clip writes made under mu. A removal compares it with
	// the count its own write recorded: os.SameFile compares only device and
	// inode numbers on Unix, and a freed inode number can be given to a later
	// file at the same path, so a matching identity alone does not prove the
	// file is still the one this export wrote.
	writes uint64
}

var clipPathStripes [clipPathLockStripes]clipPathStripe

// clipPathStripeFor returns the stripe of a clip path. Paths that hash to the
// same stripe share it.
func clipPathStripeFor(path string) *clipPathStripe {
	h := fnv.New32a()
	_, _ = h.Write([]byte(filepath.Clean(path)))
	return &clipPathStripes[h.Sum32()%clipPathLockStripes]
}

// clipPathLock returns the lock for a clip path. writeClipLocked holds it while
// the encoder writes the clip and until the written file's identity is read,
// and exportedClip.remove holds it from its checks until the removal is done,
// so another detection's SaveAudioAction export cannot rename a file onto the
// same name in between.
func clipPathLock(path string) *sync.Mutex {
	return &clipPathStripeFor(path).mu
}

// writtenClip is the file a clip export left at its path, read right after the
// write while the path lock is still held.
type writtenClip struct {
	info    os.FileInfo // nil when the Stat failed
	statErr error
	// pinned reports that info already carries the file identity. On Windows
	// os.Stat records only the path and os.SameFile reads the file ID from that
	// path at its first call, so the self-compare in writeClipLocked fixes the
	// identity to this file instead of whatever holds the name when a removal
	// later compares it (see StatSource in internal/spectrogram/generator.go).
	pinned bool
	// gen is the stripe's write count after this write (clipPathStripe.writes).
	gen uint64
}

// writeClipLocked runs write, which writes the clip at path, under the path
// lock and reads the written file's identity before the lock is released. Every
// write attempt counts on the stripe, a failed one included, since it may have
// replaced the file before failing. The lock is released by a deferred call, so
// an encoder panic that the job queue recovers does not leave the stripe held.
func writeClipLocked(path string, write func() error) (writtenClip, error) {
	stripe := clipPathStripeFor(path)
	stripe.mu.Lock()
	defer stripe.mu.Unlock()
	stripe.writes++
	gen := stripe.writes
	if err := write(); err != nil {
		return writtenClip{}, err
	}
	info, statErr := os.Stat(path)
	return writtenClip{
		info:    info,
		statErr: statErr,
		pinned:  statErr == nil && os.SameFile(info, info),
		gen:     gen,
	}, nil
}
