// model_manager.go handles the lifecycle of downloadable models: scanning
// for installed models, tracking download progress, and uninstalling models.
package classifier

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// permanentRegistryID is the registry ID for the built-in BirdNET model
// that cannot be uninstalled.
const permanentRegistryID = "BirdNET_V2.4"

// ModelManager handles the lifecycle of downloadable models.
type ModelManager struct {
	modelsDir    string
	orchestrator *Orchestrator
	mu           sync.RWMutex
	installed    map[string]InstalledModel
	downloading  map[string]*DownloadState
}

// InstalledModel represents a model that has been downloaded and is available.
type InstalledModel struct {
	CatalogID   string    `json:"catalogId"`
	ModelPath   string    `json:"modelPath"`
	LabelsPath  string    `json:"labelsPath"`
	InstalledAt time.Time `json:"installedAt"`
	Version     string    `json:"version"`
}

// DownloadState tracks the progress of an ongoing model download.
type DownloadState struct {
	CatalogID       string `json:"catalogId"`
	TotalBytes      int64  `json:"totalBytes"`
	DownloadedBytes int64  `json:"downloadedBytes"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
}

// NewModelManager creates a ModelManager that manages downloadable models
// stored under modelsDir. The orchestrator is used for coordinating with
// running model instances during install/uninstall operations.
func NewModelManager(modelsDir string, orchestrator *Orchestrator) *ModelManager {
	return &ModelManager{
		modelsDir:    modelsDir,
		orchestrator: orchestrator,
		installed:    make(map[string]InstalledModel),
		downloading:  make(map[string]*DownloadState),
	}
}

// ScanInstalled scans modelsDir for subdirectories matching catalog IDs. For
// each matching subdirectory, it checks whether the ONNX model file (the
// CatalogFile with Role "model") exists on disk. If found, the model is
// recorded as installed.
func (mm *ModelManager) ScanInstalled() {
	log := GetLogger()
	mm.mu.Lock()
	defer mm.mu.Unlock()

	for i := range EmbeddedCatalog {
		entry := &EmbeddedCatalog[i]
		subdir := filepath.Join(mm.modelsDir, entry.ID)

		// Find the model file (Role "model") in the catalog entry.
		modelFile := ""
		labelsFile := ""
		for _, f := range entry.Files {
			if f.Role == RoleModel {
				modelFile = f.LocalName
			}
			if f.Role == RoleLabels {
				labelsFile = f.LocalName
			}
		}
		if modelFile == "" {
			continue
		}

		modelPath := filepath.Join(subdir, modelFile)
		if _, err := os.Stat(modelPath); err != nil {
			continue
		}

		labelsPath := ""
		if labelsFile != "" {
			labelsPath = filepath.Join(subdir, labelsFile)
		}

		mm.installed[entry.ID] = InstalledModel{
			CatalogID:   entry.ID,
			ModelPath:   modelPath,
			LabelsPath:  labelsPath,
			InstalledAt: fileModTime(modelPath),
			Version:     entry.Version,
		}

		log.Debug("Found installed model",
			logger.String("catalog_id", entry.ID),
			logger.String("path", modelPath))
	}

	log.Info("Model scan complete",
		logger.Int("installed_count", len(mm.installed)))
}

// IsInstalled returns true if the model identified by catalogID is installed.
func (mm *ModelManager) IsInstalled(catalogID string) bool {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	_, ok := mm.installed[catalogID]
	return ok
}

// ListInstalled returns a copy of all installed models.
func (mm *ModelManager) ListInstalled() []InstalledModel {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	if len(mm.installed) == 0 {
		return []InstalledModel{}
	}
	return slices.Collect(maps.Values(mm.installed))
}

// GetDownloadState returns the current download state for the given catalog
// ID, or nil if no download is in progress.
func (mm *ModelManager) GetDownloadState(catalogID string) *DownloadState {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	state, ok := mm.downloading[catalogID]
	if !ok {
		return nil
	}
	// Return a copy to avoid races on the caller side.
	cp := *state
	return &cp
}

// Uninstall removes a downloaded model from disk and the installed map.
// It refuses to uninstall the permanent built-in model (BirdNET v2.4).
// Label files are retained on disk; shared embeddings files are only deleted
// when no other bat models remain installed.
func (mm *ModelManager) Uninstall(catalogID string) error {
	log := GetLogger()

	// Look up catalog entry first (before locking) since the catalog is immutable.
	entry, ok := GetCatalogEntry(catalogID)
	if !ok {
		return errors.Newf("unknown catalog ID: %s", catalogID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", catalogID).
			Build()
	}

	// Reject uninstall of the permanent model.
	if entry.RegistryID == permanentRegistryID {
		return errors.Newf("cannot uninstall the built-in %s model", entry.Name).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", catalogID).
			Context("registry_id", entry.RegistryID).
			Build()
	}

	mm.mu.Lock()
	defer mm.mu.Unlock()

	if _, installed := mm.installed[catalogID]; !installed {
		return errors.Newf("model %s is not installed", catalogID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", catalogID).
			Build()
	}

	subdir := filepath.Join(mm.modelsDir, catalogID)

	// Delete model ONNX files.
	for _, f := range entry.Files {
		if f.Role == RoleModel {
			path := filepath.Join(subdir, f.LocalName)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return errors.Newf("failed to remove model file %s: %v", path, err).
					Component("classifier.model_manager").
					Category(errors.CategoryFileIO).
					Context("catalog_id", catalogID).
					Context("file", path).
					Build()
			}
			log.Info("Removed model file",
				logger.String("catalog_id", catalogID),
				logger.String("path", path))
		}
	}

	// Delete shared embeddings files only if no other bat models remain.
	if entry.Category == CategoryBat {
		otherBatInstalled := false
		for id := range mm.installed {
			if id == catalogID {
				continue
			}
			other, found := GetCatalogEntry(id)
			if found && other.Category == CategoryBat {
				otherBatInstalled = true
				break
			}
		}

		if !otherBatInstalled {
			for _, f := range entry.Files {
				if f.Role == RoleEmbeddings {
					path := filepath.Join(subdir, f.LocalName)
					if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
						log.Warn("Failed to remove embeddings file",
							logger.String("path", path),
							logger.Error(err))
					} else {
						log.Info("Removed shared embeddings file",
							logger.String("path", path))
					}
				}
			}
		} else {
			log.Debug("Retaining shared embeddings; other bat models still installed",
				logger.String("catalog_id", catalogID))
		}
	}

	// Labels are intentionally retained on disk.

	delete(mm.installed, catalogID)
	log.Info("Model uninstalled",
		logger.String("catalog_id", catalogID))

	return nil
}

// Install downloads all files for a catalog entry and records it as installed.
// The baseURL parameter overrides the HuggingFace URL for testing; pass an
// empty string to use the default HuggingFace URL constructed from the entry's
// repo. Progress is reported via the channel if non-nil.
func (mm *ModelManager) Install(entry *CatalogEntry, baseURL string, progress chan<- DownloadState) error {
	log := GetLogger()

	// Check if already installed.
	mm.mu.Lock()
	if _, ok := mm.installed[entry.ID]; ok {
		mm.mu.Unlock()
		return errors.Newf("model %s is already installed", entry.ID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", entry.ID).
			Build()
	}

	// Record download as in-progress.
	mm.downloading[entry.ID] = &DownloadState{
		CatalogID: entry.ID,
		Status:    "downloading",
	}
	mm.mu.Unlock()

	// Create model subdirectory.
	subdir := filepath.Join(mm.modelsDir, entry.ID)
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		mm.removeDownloading(entry.ID)
		return errors.Newf("failed to create model directory %s: %v", subdir, err).
			Component("classifier.model_manager").
			Category(errors.CategoryFileIO).
			Context("catalog_id", entry.ID).
			Context("directory", subdir).
			Build()
	}

	// Track files we downloaded so we can clean up on failure.
	var downloadedFiles []string

	cleanup := func() {
		for _, f := range downloadedFiles {
			_ = os.Remove(f)
		}
		mm.removeDownloading(entry.ID)
	}

	// Download each file.
	var modelPath, labelsPath string
	for _, f := range entry.Files {
		// Determine destination path: embeddings go in shared/, others in the model subdir.
		var destPath string
		if f.Role == RoleEmbeddings {
			destPath = filepath.Join(mm.modelsDir, "shared", f.LocalName)
		} else {
			destPath = filepath.Join(subdir, f.LocalName)
		}

		// Skip download if file already exists (for shared embeddings).
		if _, err := os.Stat(destPath); err == nil {
			log.Debug("File already exists, skipping download",
				logger.String("catalog_id", entry.ID),
				logger.String("path", destPath))
			// Still track paths for the installed record.
			if f.Role == RoleModel {
				modelPath = destPath
			}
			if f.Role == RoleLabels {
				labelsPath = destPath
			}
			continue
		}

		// Build download URL.
		var url string
		if baseURL != "" {
			url = baseURL + "/" + f.RemotePath
		} else {
			url = buildHuggingFaceURL(entry.HuggingFaceRepo, f.RemotePath)
		}

		// Update download state with current file info.
		mm.mu.Lock()
		if state, ok := mm.downloading[entry.ID]; ok {
			state.TotalBytes = f.SizeBytes
			state.DownloadedBytes = 0
			state.Status = "downloading"
		}
		mm.mu.Unlock()

		if err := mm.downloadFile(url, destPath, f.SHA256, f.SizeBytes, progress); err != nil {
			log.Error("Failed to download file",
				logger.String("catalog_id", entry.ID),
				logger.String("url", url),
				logger.Error(err))
			cleanup()
			return err
		}

		downloadedFiles = append(downloadedFiles, destPath)

		if f.Role == RoleModel {
			modelPath = destPath
		}
		if f.Role == RoleLabels {
			labelsPath = destPath
		}
	}

	// Record as installed.
	mm.mu.Lock()
	mm.installed[entry.ID] = InstalledModel{
		CatalogID:   entry.ID,
		ModelPath:   modelPath,
		LabelsPath:  labelsPath,
		InstalledAt: time.Now(),
		Version:     entry.Version,
	}
	delete(mm.downloading, entry.ID)
	mm.mu.Unlock()

	// Send final complete status.
	if progress != nil {
		progress <- DownloadState{
			CatalogID: entry.ID,
			Status:    "complete",
		}
	}

	log.Info("Model installed",
		logger.String("catalog_id", entry.ID),
		logger.String("model_path", modelPath))

	return nil
}

// removeDownloading removes a catalog ID from the downloading map.
func (mm *ModelManager) removeDownloading(catalogID string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	delete(mm.downloading, catalogID)
}

// progressInterval is the minimum number of bytes between progress reports
// sent to the progress channel during a download.
const progressInterval = 1 << 20 // 1 MiB

// downloadFile downloads a file from url to destPath, verifying the SHA256
// checksum. Progress is reported via the progress channel if non-nil.
// On failure, any temporary file is cleaned up.
func (mm *ModelManager) downloadFile(url, destPath, expectedSHA256 string, totalBytes int64, progress chan<- DownloadState) error {
	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return errors.Newf("failed to create directory for %s: %v", destPath, err).
			Component("classifier.model_manager").
			Category(errors.CategoryFileIO).
			Context("dest_path", destPath).
			Build()
	}

	tmpPath := destPath + ".tmp"

	// Always attempt best-effort cleanup of the temp file on error.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	resp, err := http.Get(url) //nolint:gosec // URL is constructed from catalog metadata, not user input
	if err != nil {
		return errors.Newf("HTTP request failed for %s: %v", url, err).
			Component("classifier.model_manager").
			Category(errors.CategoryNetwork).
			Context("url", url).
			Build()
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return errors.Newf("HTTP %d for %s", resp.StatusCode, url).
			Component("classifier.model_manager").
			Category(errors.CategoryNetwork).
			Context("url", url).
			Context("status", fmt.Sprintf("%d", resp.StatusCode)).
			Build()
	}

	outFile, err := os.Create(tmpPath)
	if err != nil {
		return errors.Newf("failed to create temp file %s: %v", tmpPath, err).
			Component("classifier.model_manager").
			Category(errors.CategoryFileIO).
			Context("tmp_path", tmpPath).
			Build()
	}
	defer func() { _ = outFile.Close() }()

	hasher := sha256.New()
	reader := io.TeeReader(resp.Body, hasher)

	var downloaded int64
	var lastReport int64
	buf := make([]byte, 32*1024) // 32 KiB read buffer

	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if _, writeErr := outFile.Write(buf[:n]); writeErr != nil {
				return errors.Newf("failed to write to %s: %v", tmpPath, writeErr).
					Component("classifier.model_manager").
					Category(errors.CategoryFileIO).
					Context("tmp_path", tmpPath).
					Build()
			}
			downloaded += int64(n)

			// Report progress at intervals.
			if progress != nil && (downloaded-lastReport >= progressInterval || readErr == io.EOF) {
				progress <- DownloadState{
					TotalBytes:      totalBytes,
					DownloadedBytes: downloaded,
					Status:          "downloading",
				}
				lastReport = downloaded
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return errors.Newf("read error downloading %s: %v", url, readErr).
				Component("classifier.model_manager").
				Category(errors.CategoryNetwork).
				Context("url", url).
				Build()
		}
	}

	// Verify checksum.
	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	if actualSHA256 != expectedSHA256 {
		return errors.Newf("checksum mismatch for %s: expected %s, got %s", destPath, expectedSHA256, actualSHA256).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("dest_path", destPath).
			Context("expected_sha256", expectedSHA256).
			Context("actual_sha256", actualSHA256).
			Build()
	}

	// Close before rename so the file is flushed.
	if err := outFile.Close(); err != nil {
		return errors.Newf("failed to close temp file %s: %v", tmpPath, err).
			Component("classifier.model_manager").
			Category(errors.CategoryFileIO).
			Context("tmp_path", tmpPath).
			Build()
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return errors.Newf("failed to rename %s to %s: %v", tmpPath, destPath, err).
			Component("classifier.model_manager").
			Category(errors.CategoryFileIO).
			Context("tmp_path", tmpPath).
			Context("dest_path", destPath).
			Build()
	}

	success = true
	return nil
}

// buildHuggingFaceURL constructs the download URL for a file in a HuggingFace repo.
func buildHuggingFaceURL(repo, filePath string) string {
	return "https://huggingface.co/" + repo + "/resolve/main/" + filePath
}

// fileModTime returns the modification time for a file, or the zero time on error.
func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
