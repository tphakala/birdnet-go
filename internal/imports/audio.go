// Package imports audio.go: audio clip resolution, path construction, and copying.
package imports

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// audioWorkerLimit is the maximum number of concurrent clip-copy goroutines per batch.
const audioWorkerLimit = 4

// sanitizePathComponent validates that s is a single safe path component.
// Returns the component and true if safe, or ("", false) if s contains a path separator,
// is ".", is "..", or is empty. A crafted DB value containing ".." is treated as "not found".
func sanitizePathComponent(s string) (string, bool) {
	if s == "" || s == "." || s == ".." {
		return "", false
	}
	if strings.ContainsAny(s, "/\\") {
		return "", false
	}
	base := filepath.Base(filepath.Clean(s))
	if base == "." || base == ".." || base == "" {
		return "", false
	}
	return base, true
}

// isWithinDir reports whether target is physically contained within root (both cleaned).
// Mirrors the isContained helper in internal/api/v2/import.go.
func isWithinDir(root, target string) bool {
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveSourceClipPath resolves the path of a source audio clip within the BirdNET-Pi
// audio tree. It tries the exact CommonName first, then a fallback form with spaces
// replaced by underscores and apostrophes stripped.
// Returns the resolved path and true if found, or ("", false) if neither form exists
// or if any DB-derived component contains path traversal sequences.
func resolveSourceClipPath(audioSourceDir, date, comName, fileName string) (string, bool) {
	// Sanitize all DB-derived path components before joining. A component containing
	// ".." or a separator is treated as "clip not found" (defense against a crafted DB).
	safeDate, ok := sanitizePathComponent(date)
	if !ok {
		return "", false
	}
	safeComName, ok := sanitizePathComponent(comName)
	if !ok {
		return "", false
	}
	safeFileName, ok := sanitizePathComponent(fileName)
	if !ok {
		return "", false
	}

	// Try exact common name first.
	exact := filepath.Join(audioSourceDir, "Extracted", "By_Date", safeDate, safeComName, safeFileName)
	if !isWithinDir(audioSourceDir, exact) {
		return "", false
	}
	if _, err := os.Stat(exact); err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(exact); resolveErr == nil {
			srcRoot, rootErr := filepath.EvalSymlinks(audioSourceDir)
			if rootErr != nil {
				srcRoot = audioSourceDir
			}
			if !isWithinDir(srcRoot, resolved) {
				return "", false
			}
		}
		return exact, true
	}

	// Fallback: replace spaces with underscores, strip apostrophes.
	fallback := strings.ReplaceAll(safeComName, " ", "_")
	fallback = strings.ReplaceAll(fallback, "'", "")
	if fallback == safeComName {
		return "", false
	}
	// Re-sanitize the transformed fallback name.
	safeFallback, ok := sanitizePathComponent(fallback)
	if !ok {
		return "", false
	}
	fallbackPath := filepath.Join(audioSourceDir, "Extracted", "By_Date", safeDate, safeFallback, safeFileName)
	if !isWithinDir(audioSourceDir, fallbackPath) {
		return "", false
	}
	if _, err := os.Stat(fallbackPath); err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(fallbackPath); resolveErr == nil {
			srcRoot, rootErr := filepath.EvalSymlinks(audioSourceDir)
			if rootErr != nil {
				srcRoot = audioSourceDir
			}
			if !isWithinDir(srcRoot, resolved) {
				return "", false
			}
		}
		return fallbackPath, true
	}

	return "", false
}

// targetClipRelPath constructs the relative clip path used in the export store,
// mirroring the format produced by buildClipPath in internal/analysis/processor/processor.go.
// Format: "YYYY/MM/<scientificName_lowercased_underscored>_<conf>p_<YYYYMMDDTHHMMSSZ>.<srcExt>"
func targetClipRelPath(scientificName string, confidence float64, ts time.Time, srcExt string) string {
	formattedName := strings.ToLower(strings.ReplaceAll(scientificName, " ", "_"))
	// Strip any residual path separators from a crafted scientificName by keeping only
	// the last component. filepath.Base("../../evil/name") -> "name".
	formattedName = filepath.Base(formattedName)
	formattedConf := fmt.Sprintf("%.0fp", confidence*100)
	// Format ts directly (no .UTC()) to match buildClipPath in processor.go, which
	// uses the configured-zone time with a literal "Z" suffix rather than converting to UTC.
	timestamp := ts.Format("20060102T150405Z")
	year := ts.Format("2006")
	month := ts.Format("01")
	filename := formattedName + "_" + formattedConf + "_" + timestamp + "." + srcExt
	return filepath.ToSlash(filepath.Join(year, month, filename))
}

// copyClipAtomic copies srcPath to destAbsPath using a unique temp file and atomic rename.
// It creates the destination directory if needed.
func copyClipAtomic(srcPath, destAbsPath string) error {
	if err := os.MkdirAll(filepath.Dir(destAbsPath), 0o755); err != nil {
		return errors.New(err).
			Component("imports/audio").
			Category(errors.CategoryFileIO).
			Context("operation", "mkdir").
			Context("path", filepath.Dir(destAbsPath)).
			Build()
	}

	tmpPath := audiotemp.UniquePath(destAbsPath)

	tmpF, err := os.Create(tmpPath)
	if err != nil {
		return errors.New(err).
			Component("imports/audio").
			Category(errors.CategoryFileIO).
			Context("operation", "create_temp").
			Context("path", tmpPath).
			Build()
	}

	srcF, err := os.Open(srcPath)
	if err != nil {
		_ = tmpF.Close()
		_ = os.Remove(tmpPath)
		return errors.New(err).
			Component("imports/audio").
			Category(errors.CategoryFileIO).
			Context("operation", "open_src").
			Context("path", srcPath).
			Build()
	}

	_, copyErr := io.Copy(tmpF, srcF)
	closeErr := srcF.Close()
	syncErr := tmpF.Sync()
	tmpCloseErr := tmpF.Close()

	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return errors.New(copyErr).
			Component("imports/audio").
			Category(errors.CategoryFileIO).
			Context("operation", "copy").
			Build()
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return errors.New(closeErr).
			Component("imports/audio").
			Category(errors.CategoryFileIO).
			Context("operation", "close_src").
			Build()
	}
	if syncErr != nil {
		_ = os.Remove(tmpPath)
		return errors.New(syncErr).
			Component("imports/audio").
			Category(errors.CategoryFileIO).
			Context("operation", "sync").
			Build()
	}
	if tmpCloseErr != nil {
		_ = os.Remove(tmpPath)
		return errors.New(tmpCloseErr).
			Component("imports/audio").
			Category(errors.CategoryFileIO).
			Context("operation", "close_temp").
			Build()
	}

	if err := audiotemp.Finalize(tmpPath, destAbsPath); err != nil {
		_ = os.Remove(tmpPath)
		return errors.New(err).
			Component("imports/audio").
			Category(errors.CategoryFileIO).
			Context("operation", "rename").
			Build()
	}

	return nil
}

// sumSourceClipSizes sums the sizes of all source clips for the given source detections
// and audio source directory. Missing clips are skipped silently.
func sumSourceClipSizes(audioSourceDir string, rows []SourceDetection) uint64 {
	var total uint64
	for i := range rows {
		row := &rows[i]
		srcPath, ok := resolveSourceClipPath(audioSourceDir, row.Date, row.CommonName, row.FileName)
		if !ok {
			continue
		}
		info, err := os.Stat(srcPath)
		if err != nil {
			continue
		}
		if info.Size() > 0 {
			total += uint64(info.Size())
		}
	}
	return total
}

// checkDiskSpace returns an error if free space on the export path is less than required bytes.
// freeSpaceFn defaults to diskmanager.GetAvailableSpace when nil.
func checkDiskSpace(exportPath string, requiredBytes uint64, freeSpaceFn func(string) (uint64, error)) error {
	fn := freeSpaceFn
	if fn == nil {
		fn = diskmanager.GetAvailableSpace
	}
	free, err := fn(exportPath)
	if err != nil {
		return errors.New(err).
			Component("imports/audio").
			Category(errors.CategoryDiskUsage).
			Context("operation", "disk_space_check").
			Context("path", exportPath).
			Build()
	}
	if free < requiredBytes {
		return errors.Newf("insufficient disk space: need %d bytes, have %d bytes free", requiredBytes, free).
			Component("imports/audio").
			Category(errors.CategoryDiskUsage).
			Context("operation", "disk_space_check").
			Context("path", exportPath).
			Build()
	}
	return nil
}

// copyCandidateClips concurrently copies audio clips for a set of to-import detections.
// clipNames is pre-allocated with length len(toImport); each slot is set by its worker.
// Missing or failed clips result in an empty string at that slot and increment missCount.
// All goroutines complete before this function returns.
func (e *Engine) copyCandidateClips(
	ctx context.Context,
	opts *ImportOptions,
	toImport []SourceDetection,
	timestamps []time.Time,
	clipNames []string,
	missCount *int,
) {
	sem := semaphore.NewWeighted(audioWorkerLimit)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for idx := range toImport {
		capturedIdx := idx
		capturedRow := toImport[idx]
		capturedTs := timestamps[idx]

		if acquireErr := sem.Acquire(ctx, 1); acquireErr != nil {
			break
		}

		wg.Go(func() {
			defer sem.Release(1)

			// Skip this copy if the context was cancelled between acquire and start.
			select {
			case <-ctx.Done():
				return
			default:
			}

			srcExt := strings.TrimPrefix(filepath.Ext(capturedRow.FileName), ".")
			if srcExt == "" {
				srcExt = "mp3"
			}
			relPath := targetClipRelPath(capturedRow.ScientificName, capturedRow.Confidence, capturedTs, srcExt)
			destAbs := filepath.Join(opts.ClipExportPath, filepath.FromSlash(relPath))

			// Containment check: reject a dest path that escapes ClipExportPath.
			// targetClipRelPath already sanitizes scientificName via filepath.Base; this
			// is a defense-in-depth guard against any future path that slips through.
			if !isWithinDir(opts.ClipExportPath, destAbs) {
				e.log.Warn("target clip path escapes export root, skipping",
					logger.String("dest", destAbs),
					logger.String("export_root", opts.ClipExportPath))
				mu.Lock()
				*missCount++
				mu.Unlock()
				return
			}

			// Skip copy if target already exists (idempotency guard).
			if _, statErr := os.Stat(destAbs); statErr == nil {
				// clipNames[capturedIdx] needs no mutex: each index is owned by exactly one
				// worker and is written before wg.Wait() ensures visibility.
				clipNames[capturedIdx] = relPath
				return
			}

			srcPath, found := resolveSourceClipPath(opts.AudioSourceDir, capturedRow.Date, capturedRow.CommonName, capturedRow.FileName)
			if !found {
				e.log.Warn("source clip not found, importing detection without audio",
					logger.String("date", capturedRow.Date),
					logger.String("common_name", capturedRow.CommonName),
					logger.String("file_name", capturedRow.FileName))
				mu.Lock()
				*missCount++
				mu.Unlock()
				return
			}

			if copyErr := copyClipAtomic(srcPath, destAbs); copyErr != nil {
				e.log.Warn("failed to copy audio clip, importing detection without audio",
					logger.String("src", srcPath),
					logger.String("dest", destAbs),
					logger.Error(copyErr))
				mu.Lock()
				*missCount++
				mu.Unlock()
				return
			}

			// clipNames[capturedIdx] needs no mutex: each index is owned by exactly one
			// worker and is written before wg.Wait() ensures visibility.
			clipNames[capturedIdx] = relPath
		})
	}

	wg.Wait()
}
