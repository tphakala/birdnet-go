// clip_reconcile.go - reconciles persisted clip_name references against the audio
// files actually on disk, clearing references to files that no longer exist
// (ghosts from failed exports, or from detections created while export was off).
// When a spectrogram render of the missing clip survives, the reference is moved
// to the spectrogram clip name instead, so the image stays reachable.
// It never deletes files; it only updates the DB reference so clip_name stays a
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
	"github.com/tphakala/birdnet-go/internal/spectrogram/specfile"
)

// ClipRecencyWindow is the age below which a detection's audio clip is treated as
// possibly still being written by the clip encoder. It is keyed on the
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

const (
	abortReasonNotConfigured    = "export path not configured"
	abortReasonDirInaccessible  = "export directory inaccessible"
	abortReasonReadError        = "read error"
	abortReasonShutdown         = "shutdown"
	abortReasonExportDirChanged = "export directory changed during pass"
)

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
	// RetainNoteSpectrogramsByClipNames moves clip_name into the spectrogram clip
	// name for the notes whose clip_name exactly matches one of the given
	// DB-format values, so a surviving spectrogram render stays reachable. Returns
	// the number of rows updated. It never touches files on disk.
	RetainNoteSpectrogramsByClipNames(clipNames []string) (int64, error)
}

// ReconcileResult summarizes one reconcile pass for logging.
type ReconcileResult struct {
	// Scanned is the number of clip references read across all chunks.
	Scanned int
	// Cleared is the number of rows whose clip_name was cleared. It is a DB
	// rows-affected sum and can include twin rows sharing a clip_name, so
	// Cleared + HeldNoEvidence may exceed Scanned.
	Cleared int64
	// Retained is the number of rows whose clip_name moved to the spectrogram clip name.
	Retained int64
	// HeldNoEvidence is the number of orphans left untouched because no positive
	// evidence (a present clip, an active encode or a surviving render) preceded
	// them in this pass.
	HeldNoEvidence int
	// HeldMissingDir is the number of orphans left untouched after evidence because
	// their parent directory is absent, or because they are flat-named and their
	// chunk holds no positive row.
	HeldMissingDir int
	// WriteErrors is the number of failed clear or retain calls.
	WriteErrors int
	// Aborted is true when the pass stopped early: inaccessible or changed export
	// directory, read error, or shutdown.
	Aborted bool
	// AbortReason is a human-readable reason when Aborted is true.
	AbortReason string
	// ShutdownRequested is true when the pass stopped because quitChan closed (not a data guard).
	ShutdownRequested bool
}

// clipState is the on-disk status of one clip reference.
type clipState int

const (
	clipPresent       clipState = iota // final file exists on disk
	clipEncoding                       // final file missing but a fresh export temp exists
	clipOrphan                         // final file missing and no active encode
	clipIndeterminate                  // could not be determined (e.g. permission error)
)

// logExportDirInaccessible logs why the export directory cannot be used. A
// directory that does not exist yet is the normal state of a fresh install or one
// that never exported a clip, so it stays at Debug; anything else (a permission or
// I/O error, or a path that is not a directory) is a Warn, because it usually means
// the storage is broken. cause is nil when the path exists but is not a directory.
func logExportDirInaccessible(log logger.Logger, cause error) {
	if errors.Is(cause, fs.ErrNotExist) {
		log.Debug("clip reconcile: skipping pass, export directory does not exist",
			logger.String("operation", "clip_reconcile_abort"))
		return
	}
	fields := []logger.Field{logger.String("operation", "clip_reconcile_abort")}
	if cause != nil {
		fields = append(fields, logger.Error(cause))
	}
	log.Warn("clip reconcile: skipping pass, export directory is not accessible", fields...)
}

// ReconcileClipOrphansPass walks all clip references in keyset-paginated chunks and
// clears those whose audio file is confirmed missing and that pass the guards below,
// or, when a spectrogram render of the clip survives, re-links the render by moving
// the name to the spectrogram clip name. It never deletes files.
//
// Safety guards (fail-safe = leave the stale reference rather than risk data loss):
//
//   - Directory-present guard: if baseDir is unconfigured or not an accessible
//     directory, the pass aborts without clearing anything. It logs a Warn when
//     the path exists but cannot be read, and only a Debug line when the directory
//     does not exist yet. An unmounted volume must never be read as "all clips gone".
//   - Row-order evidence rule: row IDs only grow, so after a storage swap every
//     present file belongs to a row newer than every pre-swap row. A missing clip is
//     therefore cleared only when a row with a lower ID, earlier in the same pass,
//     showed the storage is attached (a present file, a fresh export temp, or a
//     surviving render). Orphans before the first such row are held, never cleared,
//     and the pass keeps walking, so an aged-out library heals everything after its
//     first surviving clip while a detached or replaced volume loses nothing.
//     This relies on the store returning rows ID-ascending across chunks.
//   - Directory rule: a clip in a subdirectory is cleared only if that directory
//     still exists (retention removes files, never directories), so a vanished mount
//     or a deleted subtree is held. A clip at the export root is cleared only if
//     its own chunk holds a present row.
//   - Same-storage check: before every read and again before a chunk's writes the
//     pass aborts if baseDir no longer stats as the directory it opened, or if
//     pathCurrent reports false.
//   - Recency guard: rows whose completion time is within ClipRecencyWindow (or is
//     unknown) are skipped so a clip still being encoded is never cleared.
//
// Held rows are a deliberate safety-over-completeness tradeoff: orphans older than
// every surviving clip stay untouched, because no rule over files and IDs can tell
// them from a detached volume. The media and UI paths degrade safely for them.
//
// pathCurrent reports whether baseDir is still the configured export path; a nil
// pathCurrent means it always is. The pass honors quitChan for prompt shutdown.
func ReconcileClipOrphansPass(quitChan <-chan struct{}, store ReconcileStore, baseDir string, pathCurrent func() bool) ReconcileResult {
	log := GetLogger()
	var result ReconcileResult

	// Directory-present guard.
	if strings.TrimSpace(baseDir) == "" {
		result.Aborted = true
		result.AbortReason = abortReasonNotConfigured
		return result
	}
	startInfo, err := os.Stat(baseDir)
	if err != nil || !startInfo.IsDir() {
		logExportDirInaccessible(log, err)
		result.Aborted = true
		result.AbortReason = abortReasonDirInaccessible
		return result
	}

	// Resolve every clip lookup through an os.Root anchored at the export dir so a
	// persisted clip_name can never escape it (absolute path, "..", or a symlink
	// pointing outside): os.Root rejects such names, and the crawler treats the
	// resulting error as indeterminate rather than as a missing/orphan file.
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		logExportDirInaccessible(log, err)
		result.Aborted = true
		result.AbortReason = abortReasonDirInaccessible
		return result
	}
	defer func() { _ = root.Close() }()

	// abortOnStorageChange marks the pass aborted when the export storage is no
	// longer the one the pass started on. It runs before each read and again before
	// a chunk's writes, because evaluating a chunk takes many filesystem calls.
	abortOnStorageChange := func() bool {
		if sameExportStorage(baseDir, startInfo, pathCurrent) {
			return false
		}
		log.Warn("clip reconcile: aborting pass, export storage changed",
			logger.Int("scanned", result.Scanned),
			logger.String("operation", "clip_reconcile_abort"))
		result.Aborted = true
		result.AbortReason = abortReasonExportDirChanged
		return true
	}

	var afterID uint
	evidenceSeen := false
	for {
		select {
		case <-quitChan:
			result.Aborted = true
			result.AbortReason = abortReasonShutdown
			result.ShutdownRequested = true
			return result
		default:
		}

		if abortOnStorageChange() {
			return result
		}

		refs, err := store.GetNoteClipReferences(afterID, reconcileChunkSize)
		if err != nil {
			log.Warn("clip reconcile: failed to read clip references",
				logger.Error(err),
				logger.String("operation", "clip_reconcile_read"))
			result.Aborted = true
			result.AbortReason = abortReasonReadError
			return result
		}
		if len(refs) == 0 {
			if result.HeldNoEvidence > 0 {
				log.Warn("clip reconcile: left orphaned clip references untouched, no surviving clip precedes them (storage may be detached, or these clips were deleted before any that remain)",
					logger.Int("held_no_evidence", result.HeldNoEvidence),
					logger.Int("held_missing_dir", result.HeldMissingDir),
					logger.Int("scanned", result.Scanned),
					logger.String("operation", "clip_reconcile_held"))
			}
			return result // walk complete
		}
		result.Scanned += len(refs)
		afterID = refs[len(refs)-1].ID

		// Recompute now per chunk so the recency window tracks wall-clock across a
		// long, throttled walk.
		chunk := evaluateClipChunk(root, refs, time.Now(), evidenceSeen)
		if abortOnStorageChange() {
			return result
		}
		evidenceSeen = chunk.evidenceSeen
		result.HeldNoEvidence += chunk.heldNoEvidence
		result.HeldMissingDir += chunk.heldMissingDir

		if len(chunk.retained) > 0 {
			retained, retainErr := store.RetainNoteSpectrogramsByClipNames(chunk.retained)
			if retainErr != nil {
				// Fail safe: leave the rows untouched, the next pass retries.
				result.WriteErrors++
				log.Warn("clip reconcile: failed to re-link surviving spectrograms",
					logger.Error(retainErr),
					logger.Int("retained", len(chunk.retained)),
					logger.String("operation", "clip_reconcile_retain"))
			} else {
				result.Retained += retained
				log.Debug("clip reconcile: re-linked surviving spectrograms",
					logger.Int64("retained", retained),
					logger.String("operation", "clip_reconcile_retain"))
			}
		}

		if len(chunk.orphans) > 0 {
			cleared, clearErr := store.ClearNoteClipPathsByNames(chunk.orphans)
			if clearErr != nil {
				result.WriteErrors++
				log.Warn("clip reconcile: failed to clear orphaned clip references",
					logger.Error(clearErr),
					logger.Int("orphans", len(chunk.orphans)),
					logger.String("operation", "clip_reconcile_clear"))
			} else {
				result.Cleared += cleared
				log.Debug("clip reconcile: cleared orphaned clip references",
					logger.Int64("cleared", cleared),
					logger.String("operation", "clip_reconcile_clear"))
			}
		}

		// Slow crawler: pause between chunks to amortize I/O on slow media.
		if !reconcileSleep(quitChan, reconcileChunkPause) {
			result.Aborted = true
			result.AbortReason = abortReasonShutdown
			result.ShutdownRequested = true
			return result
		}
	}
}

// sameExportStorage reports whether baseDir still stats as the directory the pass
// opened (start) and the export path setting still equals it (pathCurrent, nil
// meaning always current). os.Root pins the original directory for the whole pass,
// so without this check a replaced or re-pointed export directory would keep being
// judged through the old tree.
func sameExportStorage(baseDir string, start os.FileInfo, pathCurrent func() bool) bool {
	now, err := os.Stat(baseDir)
	if err != nil || !os.SameFile(start, now) {
		return false
	}
	return pathCurrent == nil || pathCurrent()
}

// exportRootRelDir is what filepath.Dir returns for a clip stored directly in the
// export root.
const exportRootRelDir = "."

// chunkResult accumulates the on-disk evaluation of one chunk of clip references.
type chunkResult struct {
	orphans        []string // DB-format clip_name values to clear
	retained       []string // orphaned DB-format clip_name values whose spectrogram render survives
	positiveCount  int      // rows with a present file, an active encode or a surviving render
	heldNoEvidence int      // orphans left untouched: no positive row precedes them in the pass
	heldMissingDir int      // orphans left untouched: parent directory absent, or flat name without chunk evidence
	evidenceSeen   bool     // pass-level evidence flag after this chunk
}

// orphanCandidate is a renderless orphan awaiting the phase-2 decision.
type orphanCandidate struct {
	name          string
	afterEvidence bool // a positive row at a lower ID was seen before this row
	flat          bool // stored directly in the export root
	dirPresent    bool // parent directory exists
}

// evaluateClipChunk classifies each non-recent clip reference in refs against the
// files under root at time now. Recent rows (within ClipRecencyWindow), rows with
// an unknown completion time, and rows whose lookup is indeterminate (unreadable
// directory, escaping symlink, transient I/O error) are skipped entirely (neither
// cleared nor counted as evidence), so an ambiguous state never causes clearing.
// A missing clip whose directory still holds a non-empty spectrogram render of it is
// returned in retained instead of orphans, and counts as evidence that storage is
// attached.
//
// refs must be ordered by ID ascending. evidenceSeen says whether an earlier chunk
// of the same pass already showed positive evidence; it flips at the first positive
// row in slice order. A renderless orphan goes to orphans only if evidence preceded
// it and, for a clip in a subdirectory, that directory exists; a clip at the export
// root additionally needs a positive row in this chunk. Everything else is counted
// as heldNoEvidence or heldMissingDir and left untouched.
func evaluateClipChunk(root *os.Root, refs []ClipReference, now time.Time, evidenceSeen bool) chunkResult {
	var res chunkResult
	dirs := make(chunkDirs)
	var candidates []orphanCandidate

	for i := range refs {
		ref := &refs[i]
		if ref.ClipName == "" || ref.CompletionTime.IsZero() {
			continue // nothing to check, or unknown age -> leave untouched
		}
		if now.Sub(ref.CompletionTime) < ClipRecencyWindow {
			continue // recent -> may still be encoding
		}

		rel := filepath.FromSlash(ref.ClipName)
		switch clipFileStateAt(root, rel, dirs, now) {
		case clipPresent, clipEncoding:
			res.positiveCount++
			evidenceSeen = true
		case clipOrphan:
			switch renderStateAt(rel, dirs) {
			case renderPresent:
				// A render on disk is also evidence that storage is attached.
				res.retained = append(res.retained, ref.ClipName)
				res.positiveCount++
				evidenceSeen = true
			case renderIndeterminate:
				// Leave the reference untouched.
			case renderAbsent:
				dir := filepath.Dir(rel)
				candidates = append(candidates, orphanCandidate{
					name:          ref.ClipName,
					afterEvidence: evidenceSeen,
					flat:          dir == exportRootRelDir,
					dirPresent:    dirs[dir].exists,
				})
			}
		case clipIndeterminate:
			// Ambiguous (permission/IO error, or a name that escapes the export
			// root) -> leave the reference untouched, never counted or cleared.
		}
	}

	for _, c := range candidates {
		switch {
		case !c.afterEvidence:
			res.heldNoEvidence++
		case c.flat && res.positiveCount > 0, !c.flat && c.dirPresent:
			res.orphans = append(res.orphans, c.name)
		default:
			res.heldMissingDir++
		}
	}
	res.evidenceSeen = evidenceSeen
	return res
}

// dirListing is one cached directory read: its entries and whether it exists.
// exists cannot be derived from entries, which is nil for an absent directory and
// may also be nil for an empty one.
type dirListing struct {
	entries []os.DirEntry
	exists  bool
}

// chunkDirs is the per-chunk cache of directory listings, so a chunk of missing
// files sharing a directory does not re-read it once per file. A key is present once
// the directory was read or found absent; a directory that could not be read
// (permission or I/O error) has no key.
type chunkDirs map[string]dirListing

// renderState is whether a spectrogram render of an orphaned clip survives.
type renderState int

const (
	renderAbsent        renderState = iota // no non-empty render of the clip on disk
	renderPresent                          // a non-empty render exists
	renderIndeterminate                    // a render entry could not be inspected
)

// renderStateAt looks for a non-empty spectrogram render of the clip whose
// OS-native relative path is rel in the cached listing of its directory
// (dirs), which hasFreshEncodingTemp populated while classifying the clip as an orphan. The
// clip base is taken without its audio extension, as renders are named after it.
// A render entry that cannot be inspected makes the answer indeterminate only when
// no other non-empty render of the clip was found.
func renderStateAt(rel string, dirs chunkDirs) renderState {
	clipBase := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	found, indeterminate := specfile.Renders(dirs[filepath.Dir(rel)].entries, clipBase)
	switch {
	case len(found) > 0:
		return renderPresent
	case indeterminate:
		return renderIndeterminate
	default:
		return renderAbsent
	}
}

// clipFileStateAt reports the on-disk state of a single clip whose OS-native
// relative path is rel, resolved inside root. dirs caches directory listings and
// their existence for the chunk. A name that escapes the root or a
// non-ErrNotExist stat error resolves to clipIndeterminate (never orphan).
func clipFileStateAt(root *os.Root, rel string, dirs chunkDirs, now time.Time) clipState {
	if _, err := root.Stat(rel); err == nil {
		return clipPresent
	} else if !errors.Is(err, fs.ErrNotExist) {
		return clipIndeterminate
	}
	// Final file missing: an active encode writes to a sibling temp file.
	hasTemp, indeterminate := hasFreshEncodingTemp(root, filepath.Dir(rel), filepath.Base(rel), dirs, now)
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
// indeterminate: the final file is genuinely missing, so the clip is an orphan, and
// dirs records it as not existing. A directory that was read is recorded as existing.
func hasFreshEncodingTemp(root *os.Root, dir, base string, dirs chunkDirs, now time.Time) (found, indeterminate bool) {
	listing, seen := dirs[dir]
	entries := listing.entries
	if !seen {
		d, err := root.Open(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				dirs[dir] = dirListing{} // absent dir -> no temp; cacheable
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
		dirs[dir] = dirListing{entries: entries, exists: true}
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
