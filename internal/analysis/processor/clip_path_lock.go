package processor

import (
	"hash/fnv"
	"path/filepath"
	"sync"
)

// clipPathLockStripes is the number of locks clip paths are spread over. Clip
// names collide only rarely (same species, rounded confidence and second), so a
// fixed set of stripes keeps the locking bounded without per-path bookkeeping.
const clipPathLockStripes = 256

// clipPathLocks serialise writing a clip at a path with removing a clip there.
var clipPathLocks [clipPathLockStripes]sync.Mutex

// clipPathLock returns the lock for a clip path. SaveAudioAction holds it from
// the start of the encode until the written file's identity is read, and
// exportedClip.remove holds it from its identity check until the removal is
// done, so another detection's SaveAudioAction export cannot rename a file onto
// the same name in between. Paths that hash to the same stripe share a lock.
func clipPathLock(path string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(filepath.Clean(path)))
	return &clipPathLocks[h.Sum32()%clipPathLockStripes]
}
