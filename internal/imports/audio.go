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

// resolveSourceClipPath resolves the path of a source audio clip within the BirdNET-Pi
// audio tree. It tries the exact CommonName first, then a fallback form with spaces
// replaced by underscores and apostrophes stripped.
// Returns the resolved path and true if found, or ("", false) if neither form exists.
func resolveSourceClipPath(audioSourceDir, date, comName, fileName string) (string, bool) {
	// Try exact common name first.
	exact := filepath.Join(audioSourceDir, "Extracted", "By_Date", date, comName, fileName)
	if _, err := os.Stat(exact); err == nil {
		return exact, true
	}

	// Fallback: replace spaces with underscores, strip apostrophes.
	fallback := strings.ReplaceAll(comName, " ", "_")
	fallback = strings.ReplaceAll(fallback, "'", "")
	if fallback == comName {
		return "", false
	}
	fallbackPath := filepath.Join(audioSourceDir, "Extracted", "By_Date", date, fallback, fileName)
	if _, err := os.Stat(fallbackPath); err == nil {
		return fallbackPath, true
	}

	return "", false
}

// targetClipRelPath constructs the relative clip path used in the export store,
// mirroring the format produced by buildClipPath in internal/analysis/processor/processor.go.
// Format: "YYYY/MM/<scientificName_lowercased_underscored>_<conf>p_<YYYYMMDDTHHMMSSZ>.<srcExt>"
func targetClipRelPath(scientificName string, confidence float64, ts time.Time, srcExt string) string {
	formattedName := strings.ToLower(strings.ReplaceAll(scientificName, " ", "_"))
	formattedConf := fmt.Sprintf("%.0fp", confidence*100)
	timestamp := ts.UTC().Format("20060102T150405Z")
	year := ts.UTC().Format("2006")
	month := ts.UTC().Format("01")
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

			srcExt := strings.TrimPrefix(filepath.Ext(capturedRow.FileName), ".")
			if srcExt == "" {
				srcExt = "mp3"
			}
			relPath := targetClipRelPath(capturedRow.ScientificName, capturedRow.Confidence, capturedTs, srcExt)
			destAbs := filepath.Join(opts.ClipExportPath, filepath.FromSlash(relPath))

			// Skip copy if target already exists (idempotency guard).
			if _, statErr := os.Stat(destAbs); statErr == nil {
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

			clipNames[capturedIdx] = relPath
		})
	}

	wg.Wait()
}
