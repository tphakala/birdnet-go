// clip_reconcile.go - reconciles persisted clip_name references against the audio
// files actually on disk, clearing references to files that no longer exist
// (ghosts from failed exports, or from detections created while export was off).
// It never deletes files; it only clears the DB reference so clip_name stays a
// truthful per-detection signal.
package diskmanager

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// ClipRecencyWindow is the age below which a detection's audio clip is treated as
// possibly still being written by the encoder (FFmpeg). It is keyed on the
// detection's COMPLETION time (Note.EndTime / LastUpdated), never its begin time:
// an extended capture records its begin time many minutes before the tail of the
// clip is written, so keying on begin time would treat an in-progress capture as
// old. Two subsystems share this single window so their notions of "recent" stay
// aligned:
//
//   - the media endpoint's grace-poll: a 404 for a detection whose completion
//     time is older than this window returns immediately instead of blocking a
//     worker waiting for an encode that will never happen (the file is a
//     pre-reconcile ghost, not a live encode);
//   - the reconcile crawler: rows whose completion time is younger than this
//     window are skipped so a clip still being encoded is never cleared as an
//     orphan.
const ClipRecencyWindow = 10 * time.Minute

// reconcileChunkSize is the number of clip references read per keyset-paginated
// chunk. Small chunks keep the crawler's memory and per-iteration I/O bounded.
const reconcileChunkSize = 200

// reconcileEncodingMaxAge bounds how fresh an export temp file must be to count as
// an active encode. It matches the media handler's audioEncodingMaxAge so both
// subsystems treat a stale temp (failed export) the same way. A temp older than
// this is ignored, so its clip resolves as an orphan.
const reconcileEncodingMaxAge = 30 * time.Second

// reconcileChunkPause is the delay between chunks, amortizing stat/readdir I/O on
// slow media (SD cards, NAS, FUSE). It is a var so tests can shorten it.
//
//nolint:gochecknoglobals // test-tunable throttle, not configuration
var reconcileChunkPause = 3 * time.Second

// ClipReference is a minimal projection of a detection row used by the reconcile
// crawler: the note ID (for keyset pagination), the persisted clip_name (in the
// exact DB format, so it can be matched by ClearNoteClipPathsByNames), and the
// capture completion time used for the recency guard.
type ClipReference struct {
	ID             uint
	ClipName       string    // DB clip_name value (relative, forward-slash separated)
	CompletionTime time.Time // capture completion time; zero when unknown
}

// ReconcileStore is the narrow datastore surface the clip reconcile crawler needs:
// a keyset-paginated read of clip references and a batch clear of orphaned ones. It
// is deliberately small so the crawler is decoupled from the full datastore
// interface and easy to fake in tests. A datastore.Interface satisfies it.
type ReconcileStore interface {
	// GetNoteClipReferences returns up to limit detection rows with a non-empty
	// clip_name and ID greater than afterID, ordered by ID ascending (keyset
	// pagination). An empty result means the walk is complete.
	GetNoteClipReferences(afterID uint, limit int) ([]ClipReference, error)
	// ClearNoteClipPathsByNames clears clip_name for the notes whose clip_name
	// exactly matches one of the given DB-format values. Returns the number of
	// rows updated. It never touches files on disk.
	ClearNoteClipPathsByNames(clipNames []string) (int64, error)
}

// ReconcileResult summarizes one reconcile pass for logging.
type ReconcileResult struct {
	Scanned           int    // clip references read across all chunks
	Cleared           int64  // rows whose clip_name was cleared
	Aborted           bool   // true when the pass stopped early (guard tripped, error, or shutdown)
	AbortReason       string // human-readable reason when Aborted is true
	ShutdownRequested bool   // true when the pass stopped because quitChan closed (not a data guard)
}

// clipState is the on-disk status of one clip reference.
type clipState int

const (
	clipPresent       clipState = iota // final file exists on disk
	clipEncoding                       // final file missing but a fresh export temp exists
	clipOrphan                         // final file missing and no active encode
	clipIndeterminate                  // could not be determined (e.g. permission error)
)

// ReconcileClipOrphansPass walks all clip references in keyset-paginated chunks and
// clears those whose audio file is confirmed missing. It never deletes files.
//
// Safety guards (fail-safe = leave the stale reference rather than risk data loss):
//
//   - Directory-present guard: if baseDir is unconfigured or not an accessible
//     directory, the pass aborts without clearing anything. An unmounted volume
//     must never be read as "all clips gone".
//   - Detached-storage guard: within a chunk, if every non-recent, evaluable row
//     resolves as an orphan (no present file and no active encode anywhere), the
//     pass aborts and clears nothing. Requiring at least one piece of positive
//     evidence that storage is attached prevents a cleanly-unmounted share (an
//     empty-but-present directory) from wiping the entire clip history. This is a
//     deliberate safety-over-completeness tradeoff: a user who never enabled export
//     has an all-orphan first chunk, so their pre-fix ghost references are left
//     untouched rather than risk mass-clearing on a detached volume; the truthful
//     empty-ClipName gating already prevents NEW ghosts, and the media/UI degrade
//     safely for the old ones.
//   - Recency guard: rows whose completion time is within ClipRecencyWindow (or is
//     unknown) are skipped so a clip still being encoded is never cleared.
//
// The pass honors quitChan for prompt shutdown.
func ReconcileClipOrphansPass(quitChan <-chan struct{}, store ReconcileStore, baseDir string) ReconcileResult {
	log := GetLogger()
	var result ReconcileResult

	// Directory-present guard.
	if strings.TrimSpace(baseDir) == "" {
		result.Aborted = true
		result.AbortReason = "export path not configured"
		return result
	}
	if info, err := os.Stat(baseDir); err != nil || !info.IsDir() {
		result.Aborted = true
		result.AbortReason = "export directory inaccessible"
		return result
	}

	// Resolve every clip lookup through an os.Root anchored at the export dir so a
	// persisted clip_name can never escape it (absolute path, "..", or a symlink
	// pointing outside): os.Root rejects such names, and the crawler treats the
	// resulting error as indeterminate rather than as a missing/orphan file.
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		result.Aborted = true
		result.AbortReason = "export directory inaccessible"
		return result
	}
	defer func() { _ = root.Close() }()

	var afterID uint
	for {
		select {
		case <-quitChan:
			result.Aborted = true
			result.AbortReason = "shutdown"
			result.ShutdownRequested = true
			return result
		default:
		}

		refs, err := store.GetNoteClipReferences(afterID, reconcileChunkSize)
		if err != nil {
			log.Warn("clip reconcile: failed to read clip references",
				logger.Error(err),
				logger.String("operation", "clip_reconcile_read"))
			result.Aborted = true
			result.AbortReason = "read error"
			return result
		}
		if len(refs) == 0 {
			return result // walk complete
		}
		result.Scanned += len(refs)
		afterID = refs[len(refs)-1].ID

		// Recompute now per chunk so the recency window tracks wall-clock across a
		// long, throttled walk.
		chunk := evaluateClipChunk(root, refs, time.Now())
		candidates := chunk.positiveCount + len(chunk.orphans)

		// Detached-storage guard.
		if candidates > 0 && chunk.positiveCount == 0 {
			log.Warn("clip reconcile: aborting pass, no evidence storage is attached",
				logger.Int("orphan_candidates", len(chunk.orphans)),
				logger.String("operation", "clip_reconcile_abort"))
			result.Aborted = true
			result.AbortReason = "no attached-storage evidence"
			return result
		}

		if len(chunk.orphans) > 0 {
			cleared, clearErr := store.ClearNoteClipPathsByNames(chunk.orphans)
			if clearErr != nil {
				log.Warn("clip reconcile: failed to clear orphaned clip references",
					logger.Error(clearErr),
					logger.Int("orphans", len(chunk.orphans)),
					logger.String("operation", "clip_reconcile_clear"))
			} else {
				result.Cleared += cleared
				log.Info("clip reconcile: cleared orphaned clip references",
					logger.Int64("cleared", cleared),
					logger.String("operation", "clip_reconcile_clear"))
			}
		}

		// Slow crawler: pause between chunks to amortize I/O on slow media.
		if !reconcileSleep(quitChan, reconcileChunkPause) {
			result.Aborted = true
			result.AbortReason = "shutdown"
			result.ShutdownRequested = true
			return result
		}
	}
}

// chunkResult accumulates the on-disk evaluation of one chunk of clip references.
type chunkResult struct {
	orphans       []string // DB-format clip_name values confirmed orphaned
	positiveCount int      // rows with a present file or an active encode (storage attached)
}

// evaluateClipChunk classifies each non-recent clip reference in refs against the
// files under root at time now. Recent rows (within ClipRecencyWindow), rows with
// an unknown completion time, and rows whose lookup is indeterminate (unreadable
// directory, escaping symlink, transient I/O error) are skipped entirely (neither
// cleared nor counted as evidence), so an ambiguous state never causes clearing.
func evaluateClipChunk(root *os.Root, refs []ClipReference, now time.Time) chunkResult {
	var res chunkResult
	// Per-chunk cache of directory listings so a chunk of missing files sharing a
	// directory does not re-read it once per file. A present map key means the dir
	// was read (entries may be nil for an absent or empty directory).
	dirCache := make(map[string][]os.DirEntry)

	for i := range refs {
		ref := &refs[i]
		if ref.ClipName == "" || ref.CompletionTime.IsZero() {
			continue // nothing to check, or unknown age -> leave untouched
		}
		if now.Sub(ref.CompletionTime) < ClipRecencyWindow {
			continue // recent -> may still be encoding
		}

		switch clipFileStateAt(root, filepath.FromSlash(ref.ClipName), dirCache, now) {
		case clipPresent, clipEncoding:
			res.positiveCount++
		case clipOrphan:
			res.orphans = append(res.orphans, ref.ClipName)
		case clipIndeterminate:
			// Ambiguous (permission/IO error, or a name that escapes the export
			// root) -> leave the reference untouched, never counted or cleared.
		}
	}
	return res
}

// clipFileStateAt reports the on-disk state of a single clip whose OS-native
// relative path is rel, resolved inside root. A name that escapes the root or a
// non-ErrNotExist stat error resolves to clipIndeterminate (never orphan).
func clipFileStateAt(root *os.Root, rel string, dirCache map[string][]os.DirEntry, now time.Time) clipState {
	if _, err := root.Stat(rel); err == nil {
		return clipPresent
	} else if !errors.Is(err, fs.ErrNotExist) {
		return clipIndeterminate
	}
	// Final file missing: an active encode writes to a sibling temp file.
	hasTemp, indeterminate := hasFreshEncodingTemp(root, filepath.Dir(rel), filepath.Base(rel), dirCache, now)
	switch {
	case indeterminate:
		return clipIndeterminate
	case hasTemp:
		return clipEncoding
	default:
		return clipOrphan
	}
}

// hasFreshEncodingTemp reports whether dir (relative to root) contains a
// not-yet-finalized export temp file for the clip basename base, written within
// reconcileEncodingMaxAge. The second return value is true when the directory or a
// temp entry could not be read (permission/IO error), which the caller must treat
// as indeterminate rather than as "no temp" -> orphan. An absent directory is not
// indeterminate: the final file is genuinely missing, so the clip is an orphan.
func hasFreshEncodingTemp(root *os.Root, dir, base string, dirCache map[string][]os.DirEntry, now time.Time) (found, indeterminate bool) {
	entries, seen := dirCache[dir]
	if !seen {
		d, err := root.Open(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				dirCache[dir] = nil // absent dir -> no temp; cacheable
				return false, false
			}
			return false, true // unreadable dir -> indeterminate (do not cache)
		}
		read, readErr := d.ReadDir(0)
		_ = d.Close()
		if readErr != nil {
			return false, true
		}
		entries = read
		dirCache[dir] = entries
	}
	for _, entry := range entries {
		if !isExportTempName(entry.Name(), base) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return false, true // cannot age the temp -> indeterminate
		}
		if now.Sub(info.ModTime()) < reconcileEncodingMaxAge {
			return true, false
		}
	}
	return false, false
}

// isExportTempName reports whether name is an in-progress export temp file for the
// clip basename base. It delegates to audiotemp.IsTempFor, the single source of
// truth for the temp-file grammar the encoder writes, so the reconcile crawler and
// the media handler stay in lockstep with the exporter.
func isExportTempName(name, base string) bool {
	return audiotemp.IsTempFor(name, base)
}

// reconcileSleep waits for d or until quitChan closes. It returns false if quitChan
// closed (shutdown requested), true if the full duration elapsed.
func reconcileSleep(quitChan <-chan struct{}, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-quitChan:
		return false
	case <-timer.C:
		return true
	}
}
