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

// clipPathLocks serialise writing a clip at a path with removing a clip there.
var clipPathLocks [clipPathLockStripes]sync.Mutex

// clipPathLock returns the lock for a clip path. writeClipLocked holds it while
// the encoder writes the clip and until the written file's identity is read,
// and exportedClip.remove holds it from its identity check until the removal is
// done, so another detection's SaveAudioAction export cannot rename a file onto
// the same name in between. Paths that hash to the same stripe share a lock.
func clipPathLock(path string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(filepath.Clean(path)))
	return &clipPathLocks[h.Sum32()%clipPathLockStripes]
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
}

// writeClipLocked runs write, which writes the clip at path, under the path
// lock and reads the written file's identity before the lock is released. The
// lock is released by a deferred call, so an encoder panic that the job queue
// recovers does not leave the stripe held.
func writeClipLocked(path string, write func() error) (writtenClip, error) {
	lock := clipPathLock(path)
	lock.Lock()
	defer lock.Unlock()
	if err := write(); err != nil {
		return writtenClip{}, err
	}
	info, statErr := os.Stat(path)
	return writtenClip{
		info:    info,
		statErr: statErr,
		pinned:  statErr == nil && os.SameFile(info, info),
	}, nil
}
