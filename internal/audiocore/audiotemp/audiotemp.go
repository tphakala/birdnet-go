// Package audiotemp centralizes the temp-file naming and atomic-finalize
// contract shared by BirdNET-Go's audio clip writers: the FFmpeg exporter, the
// native FLAC, AAC and Opus encoders, and the WAV writer. Each writes a clip to
// a process-unique temp file and atomically renames it into place, so two
// simultaneous detections that resolve to the same clip path (see GitHub #3323)
// dedup safely instead of colliding on a shared temp or corrupting the final
// file mid-write.
//
// WriteFile packages that whole sequence for encoders that can write to an
// *os.File. It is not yet used everywhere: the FLAC encoder, the WAV writer, the
// FFmpeg exporter, the importer and the spectrogram generator still drive
// UniquePath and Finalize themselves, so migrating them is worthwhile but out of
// scope for the change that introduced this. The spectrogram generator in
// particular needs FinalizeWith rather than WriteFile, since it injects a
// SecureFS rename to keep its writes inside the os.Root sandbox.
package audiotemp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
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

// IsTempFor reports whether name is an in-progress temp file for the clip whose
// base filename is base. It is the single source of truth for the temp-file
// grammar UniquePath writes, so callers that serve or reconcile clips (the media
// handler and the reconcile crawler) recognize an actively-writing clip as
// present rather than missing. It matches both the legacy fixed form
// "<base>.temp" and the process-unique form "<base>.<pid>.<seq>.temp" (pid and
// seq are integers).
func IsTempFor(name, base string) bool {
	if name == base+Ext {
		return true
	}
	middle, ok := strings.CutPrefix(name, base+".")
	if !ok {
		return false
	}
	middle, ok = strings.CutSuffix(middle, Ext)
	if !ok {
		return false
	}
	pidStr, seqStr, ok := strings.Cut(middle, ".")
	if !ok {
		return false
	}
	if _, err := strconv.Atoi(pidStr); err != nil {
		return false
	}
	_, err := strconv.Atoi(seqStr)
	return err == nil
}

// dirPerm is the mode for a created clip directory. Clips can carry location
// data, so the directory is not world-readable.
const dirPerm = 0o750

// WriteFile runs encode against a process-unique temp file next to finalPath and
// atomically renames the result into place. It creates the parent directory,
// creates and fsyncs the temp file, closes it, and finalizes; encode only has to
// write. On any failure the temp file is removed and finalPath is left as it was,
// so a consumer never observes a partially written clip.
//
// encode receives a real *os.File, so it works for an encoder wanting a plain
// io.Writer (Ogg Opus) and for one that must seek to patch a header on close
// (the MP4 muxer behind AAC). ctx is checked before any file is created; encode
// is responsible for honouring it thereafter.
//
// Error handling is deliberately three-way, because these failures mean very
// different things to whoever reads the telemetry:
//
//   - A cancelled context is returned raw and never wrapped, so a shutdown
//     mid-export does not manufacture an error report.
//   - WriteFile's own failures are filesystem failures (a full disk, a
//     read-only mount, a permissions problem), and are tagged here as file I/O
//     against the caller's component with the specific stage that failed. They
//     must not surface as codec failures.
//   - Whatever encode returns is passed through untouched, so the caller tags
//     its own codec errors. Note that the payload write happens inside encode,
//     so a disk that fills up mid-clip surfaces there rather than here; callers
//     should run that error through IsWriteFault before calling it a codec
//     failure.
func WriteFile(ctx context.Context, component, finalPath string, encode func(f *os.File) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if finalPath == "" {
		// Without this, filepath.Dir("") is "." and the temp file lands in the
		// working directory, turning a caller bug into a confusing I/O error.
		return errors.Newf("audiotemp: empty output path").
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "audiotemp_validate").
			Build()
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), dirPerm); err != nil {
		return fileIOErr(component, "audiotemp_mkdir", err)
	}

	tempPath := UniquePath(finalPath)
	f, createErr := os.Create(tempPath) //nolint:gosec // path derived from a validated clip path
	if createErr != nil {
		return fileIOErr(component, "audiotemp_create_temp", createErr)
	}

	// closeFile is idempotent so the deferred cleanup cannot double-close after
	// the success path has already closed and checked the file.
	committed := false
	fileOpen := true
	closeFile := func() error {
		if !fileOpen {
			return nil
		}
		fileOpen = false
		return f.Close()
	}
	defer func() {
		_ = closeFile()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	// encode's error is the caller's to classify, so it passes through untouched.
	if err := encode(f); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return fileIOErr(component, "audiotemp_sync", err)
	}
	if err := closeFile(); err != nil {
		return fileIOErr(component, "audiotemp_close_temp", err)
	}
	if err := Finalize(tempPath, finalPath); err != nil {
		return fileIOErr(component, "audiotemp_rename", err)
	}
	committed = true
	return nil
}

// IsWriteFault reports whether an error returned by a WriteFile encode callback
// actually came from the file underneath rather than from the codec.
//
// The encoders are handed one *os.File and touch no other path, so any
// *os.PathError surfacing through them is a write fault: a full disk, an
// exceeded quota, a read-only remount, an I/O error. Those must be reported as
// filesystem failures, not as codec failures, or a full disk shows up in
// telemetry as an audio bug carrying a sample rate and a bitrate.
func IsWriteFault(err error) bool {
	var pathErr *os.PathError
	return errors.As(err, &pathErr)
}

// fileIOErr tags a filesystem failure against the caller's component. Keeping
// the stage in the operation field preserves the granularity the encoders had
// when each of them open-coded this sequence, so a full disk still reports as a
// distinct file I/O failure rather than as a generic encode error.
func fileIOErr(component, operation string, err error) error {
	return errors.New(err).
		Component(component).
		Category(errors.CategoryFileIO).
		Context("operation", operation).
		Build()
}

// Finalize atomically renames the unique temp file onto the final clip path. On
// non-Windows platforms it is a single os.Rename (rename(2) is atomic and the
// kernel serializes concurrent renames to the same target). On Windows,
// MoveFileEx can transiently fail with a sharing violation when two concurrent
// exports rename onto the same path; Finalize retries a bounded number of times
// there to absorb it.
func Finalize(tempPath, finalPath string) error {
	return FinalizeWith(tempPath, finalPath, os.Rename)
}

// FinalizeWith is Finalize with a caller-supplied rename function. Callers whose
// files live behind a path-validated boundary (e.g. SecureFS / os.Root) inject
// their own rename so the move stays inside the sandbox, while still reusing the
// Windows sharing-violation retry. Both rename targets must already be on the
// same filesystem for the rename to be atomic. Finalize itself passes os.Rename.
func FinalizeWith(tempPath, finalPath string, rename func(oldpath, newpath string) error) error {
	err := rename(tempPath, finalPath)
	if err == nil || runtime.GOOS != osWindows {
		return err
	}
	for attempt := 1; attempt < renameRetryAttempts; attempt++ {
		time.Sleep(renameRetryDelay)
		if err = rename(tempPath, finalPath); err == nil {
			return nil
		}
	}
	return err
}
