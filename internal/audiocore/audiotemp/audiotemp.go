// Package audiotemp centralizes the temp-file naming and atomic-finalize
// contract shared by BirdNET-Go's audio clip writers: the FFmpeg exporter, the
// native FLAC encoder, and the WAV writer. Each writes a clip to a process-
// unique temp file and atomically renames it into place, so two simultaneous
// detections that resolve to the same clip path (see GitHub #3323) dedup safely
// instead of colliding on a shared temp or corrupting the final file mid-write.
package audiotemp

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

// Ext is the suffix of an in-progress export's temp file. A clip is finalized by
// atomically renaming "<clip>...Ext" onto "<clip>"; diskmanager skips any file
// ending in Ext, so a temp is never treated as a finished clip. diskmanager keeps
// its own copy of this literal (diskmanager.tempFileExt); the two must stay in
// sync, or in-progress clips would stop being skipped during cleanup.
const Ext = ".temp"

// osWindows is runtime.GOOS on Windows, where concurrent same-target renames
// need a retry (see Finalize).
const osWindows = "windows"

const (
	renameRetryAttempts = 8
	renameRetryDelay    = 15 * time.Millisecond
)

// seq makes every temp file unique within the process. Two exports targeting the
// same final clip (e.g. two audio sources detecting the same species in the same
// one-second window at the same rounded confidence) must not share one temp
// file: otherwise the first rename wins and the rest fail with ENOENT, and two
// writers could corrupt the shared temp before it is renamed. See GitHub #3323.
//
//nolint:gochecknoglobals // process-wide monotonic counter for unique temp names
var seq atomic.Uint64

// UniquePath returns a process-unique temp path for outputPath. The result still
// ends in Ext so diskmanager keeps skipping it during cleanup, and the pid +
// counter prefix avoids colliding with a stale temp left by a crashed run that
// happens to share the recordings directory.
func UniquePath(outputPath string) string {
	return fmt.Sprintf("%s.%d.%d%s", outputPath, os.Getpid(), seq.Add(1), Ext)
}

// Finalize atomically renames the unique temp file onto the final clip path. On
// non-Windows platforms it is a single os.Rename (rename(2) is atomic and the
// kernel serializes concurrent renames to the same target). On Windows,
// MoveFileEx can transiently fail with a sharing violation when two concurrent
// exports rename onto the same path; Finalize retries a bounded number of times
// there to absorb it.
func Finalize(tempPath, finalPath string) error {
	err := os.Rename(tempPath, finalPath)
	if err == nil || runtime.GOOS != osWindows {
		return err
	}
	for attempt := 1; attempt < renameRetryAttempts; attempt++ {
		time.Sleep(renameRetryDelay)
		if err = os.Rename(tempPath, finalPath); err == nil {
			return nil
		}
	}
	return err
}
