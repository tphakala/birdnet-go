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
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// permanentRegistryID is the registry ID for the built-in BirdNET model
// that cannot be uninstalled.
const permanentRegistryID = "BirdNET_V2.4"

// Download status constants used in DownloadState.Status and SSE progress events.
const (
	StatusDownloading = "downloading"
	StatusVerifying   = "verifying"
	StatusLoading     = "loading"
	StatusComplete    = "complete"
	StatusFailed      = "failed"
	StatusRemoved     = "removed"
)

// ModelManager handles the lifecycle of downloadable models.
type ModelManager struct {
	modelsDir    string
	orchestrator *Orchestrator
	settings     *conf.Settings
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
	CurrentFile     int    `json:"currentFile"`
	TotalFiles      int    `json:"totalFiles"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
}

// NewModelManager creates a ModelManager that manages downloadable models
// stored under modelsDir. The orchestrator is used for coordinating with
// running model instances during install/uninstall operations. The settings
// parameter is used to update configuration after install/uninstall; it may
// be nil for testing.
func NewModelManager(modelsDir string, orchestrator *Orchestrator, settings *conf.Settings) *ModelManager {
	return &ModelManager{
		modelsDir:    modelsDir,
		orchestrator: orchestrator,
		settings:     settings,
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

	// Sync Models.Enabled with installed/configured models so the model picker
	// always reflects the actual system state.
	if mm.settings != nil {
		// BirdNET is always present (permanent built-in).
		if !slices.ContainsFunc(mm.settings.Models.Enabled, func(id string) bool {
			return strings.EqualFold(id, conf.ModelIDBirdNET)
		}) {
			mm.settings.Models.Enabled = append([]string{conf.ModelIDBirdNET}, mm.settings.Models.Enabled...)
		}

		changed := false
		addIfMissing := func(alias string) {
			if alias != "" && !slices.ContainsFunc(mm.settings.Models.Enabled, func(id string) bool {
				return strings.EqualFold(id, alias)
			}) {
				mm.settings.Models.Enabled = append(mm.settings.Models.Enabled, alias)
				changed = true
			}
		}

		// Add models found in the gallery models directory.
		for catalogID := range mm.installed {
			entry, found := GetCatalogEntry(catalogID)
			if !found {
				continue
			}
			addIfMissing(ConfigAliasForRegistry(entry.RegistryID))
		}

		// Also add models enabled via legacy per-model config flags.
		if mm.settings.Bat.Enabled || mm.settings.Bat.ClassifierModel != "" {
			addIfMissing(conf.ModelIDBat)
		}
		if mm.settings.Perch.Enabled || mm.settings.Perch.ModelPath != "" {
			addIfMissing(conf.ModelIDPerchV2)
		}
		if mm.settings.BSG.Enabled || mm.settings.BSG.ModelPath != "" {
			addIfMissing(conf.ModelIDBSG)
		}

		if changed {
			conf.StoreSettings(mm.settings)
			if err := conf.SaveSettings(); err != nil {
				log.Warn("Failed to persist Models.Enabled sync",
					logger.Error(err))
			}
		}
	}
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

	// Unload from orchestrator BEFORE deleting files to avoid crashes.
	if mm.orchestrator != nil && entry.RegistryID != "" {
		if err := mm.orchestrator.UnloadModel(entry.RegistryID); err != nil {
			log.Warn("Failed to unload model from orchestrator",
				logger.String("catalog_id", catalogID),
				logger.Error(err))
		}
	}

	subdir := filepath.Join(mm.modelsDir, catalogID)

	// Delete model ONNX files and associated data files (calibration, distribution, etc.).
	for _, f := range entry.Files {
		if f.Role == RoleModel || f.Role == RoleData {
			path := filepath.Join(subdir, f.LocalName)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return errors.Newf("failed to remove file %s: %v", path, err).
					Component("classifier.model_manager").
					Category(errors.CategoryFileIO).
					Context("catalog_id", catalogID).
					Context("file", path).
					Build()
			}
			log.Info("Removed file",
				logger.String("catalog_id", catalogID),
				logger.String("role", f.Role),
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
					path := filepath.Join(mm.modelsDir, "shared", f.LocalName)
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

	mm.applyConfigForUninstall(&entry)

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

	// Check if already installed or already downloading.
	mm.mu.Lock()
	if _, ok := mm.installed[entry.ID]; ok {
		mm.mu.Unlock()
		return errors.Newf("model %s is already installed", entry.ID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", entry.ID).
			Build()
	}
	if _, downloading := mm.downloading[entry.ID]; downloading {
		mm.mu.Unlock()
		return errors.Newf("model %s is already being downloaded", entry.ID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", entry.ID).
			Build()
	}

	// Record download as in-progress.
	mm.downloading[entry.ID] = &DownloadState{
		CatalogID: entry.ID,
		Status:    StatusDownloading,
	}
	mm.mu.Unlock()

	// Create model subdirectory.
	subdir := filepath.Join(mm.modelsDir, entry.ID)
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		mkdirErr := errors.Newf("failed to create model directory %s: %v", subdir, err).
			Component("classifier.model_manager").
			Category(errors.CategoryFileIO).
			Context("catalog_id", entry.ID).
			Context("directory", subdir).
			Build()
		mm.markFailed(entry.ID, mkdirErr, progress)
		time.AfterFunc(30*time.Second, func() {
			mm.removeDownloading(entry.ID)
		})
		return mkdirErr
	}

	// Track files we downloaded so we can clean up on failure.
	var downloadedFiles []string

	cleanup := func() {
		for _, f := range downloadedFiles {
			_ = os.Remove(f)
		}
		// Keep failed state briefly for SSE pollers, then clean up.
		time.AfterFunc(30*time.Second, func() {
			mm.removeDownloading(entry.ID)
		})
	}

	// fileDestPath returns the local destination for a catalog file.
	fileDestPath := func(f CatalogFile) string {
		if f.Role == RoleEmbeddings {
			return filepath.Join(mm.modelsDir, "shared", f.LocalName)
		}
		return filepath.Join(subdir, f.LocalName)
	}

	// Compute cumulative totals for progress tracking across all files.
	var totalAllBytes int64
	filesToDownload := 0
	for _, f := range entry.Files {
		if _, err := os.Stat(fileDestPath(f)); err != nil {
			totalAllBytes += f.SizeBytes
			filesToDownload++
		}
	}

	// Download each file.
	var modelPath, labelsPath string
	var completedBytes int64
	fileIndex := 0
	for _, f := range entry.Files {
		destPath := fileDestPath(f)

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

		fileIndex++

		// Update download state with cumulative totals.
		mm.mu.Lock()
		if state, ok := mm.downloading[entry.ID]; ok {
			state.TotalBytes = totalAllBytes
			state.DownloadedBytes = completedBytes
			state.CurrentFile = fileIndex
			state.TotalFiles = filesToDownload
			state.Status = StatusDownloading
		}
		mm.mu.Unlock()

		if err := mm.downloadFile(entry.ID, url, destPath, f.SHA256, f.SizeBytes, completedBytes); err != nil {
			log.Error("Failed to download file",
				logger.String("catalog_id", entry.ID),
				logger.String("url", url),
				logger.Error(err))
			mm.markFailed(entry.ID, err, progress)
			cleanup()
			return err
		}

		completedBytes += f.SizeBytes
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

	// Find embeddings path for bat models.
	embeddingsPath := ""
	for _, f := range entry.Files {
		if f.Role == RoleEmbeddings {
			embeddingsPath = filepath.Join(mm.modelsDir, "shared", f.LocalName)
			break
		}
	}
	mm.applyConfigForInstall(entry, modelPath, labelsPath, embeddingsPath)

	// Hot-load into orchestrator.
	if mm.orchestrator != nil && entry.RegistryID != "" {
		if err := mm.orchestrator.LoadModel(entry.RegistryID); err != nil {
			log.Warn("Failed to hot-load model (will be available after restart)",
				logger.String("catalog_id", entry.ID),
				logger.Error(err))
		}
	}

	// Send final complete status (non-blocking in case the consumer is gone).
	if progress != nil {
		select {
		case progress <- DownloadState{
			CatalogID: entry.ID,
			Status:    StatusComplete,
		}:
		default:
		}
	}

	log.Info("Model installed",
		logger.String("catalog_id", entry.ID),
		logger.String("model_path", modelPath))

	return nil
}

// markFailed sets the download state to StatusFailed so SSE pollers can
// observe the failure before the entry is cleaned up.
func (mm *ModelManager) markFailed(catalogID string, err error, progress chan<- DownloadState) {
	mm.mu.Lock()
	if state, ok := mm.downloading[catalogID]; ok {
		state.Status = StatusFailed
		state.Error = err.Error()
	}
	mm.mu.Unlock()

	if progress != nil {
		select {
		case progress <- DownloadState{
			CatalogID: catalogID,
			Status:    StatusFailed,
			Error:     err.Error(),
		}:
		default:
		}
	}
}

// removeDownloading removes a catalog ID from the downloading map.
func (mm *ModelManager) removeDownloading(catalogID string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	delete(mm.downloading, catalogID)
}

// applyConfigForInstall updates settings to reflect a newly installed model.
// Only fields with non-empty paths are set. The caller must hold no locks.
// Settings are persisted to disk via conf.SaveSettings so changes survive
// restarts and are visible to concurrent readers through conf.Setting().
func (mm *ModelManager) applyConfigForInstall(entry *CatalogEntry, modelPath, labelsPath, embeddingsPath string) {
	if mm.settings == nil {
		return
	}

	switch entry.RegistryID {
	case RegistryIDBirdNETV3:
		GetLogger().Info("BirdNET v3.0 config wiring not yet implemented",
			logger.String("catalog_id", entry.ID))
	case RegistryIDPerchV2:
		mm.settings.Perch.Enabled = true
		if modelPath != "" {
			mm.settings.Perch.ModelPath = modelPath
		}
		if labelsPath != "" {
			mm.settings.Perch.LabelPath = labelsPath
		}
	case RegistryIDBSG:
		mm.settings.BSG.Enabled = true
		if modelPath != "" {
			mm.settings.BSG.ModelPath = modelPath
		}
		if labelsPath != "" {
			mm.settings.BSG.LabelPath = labelsPath
		}
	case RegistryIDBat:
		mm.settings.Bat.Enabled = true
		if modelPath != "" {
			mm.settings.Bat.ClassifierModel = modelPath
		}
		if labelsPath != "" {
			mm.settings.Bat.LabelPath = labelsPath
		}
		if embeddingsPath != "" {
			mm.settings.Bat.EmbeddingModel = embeddingsPath
		}
	}

	// Add config alias to Models.Enabled so the model appears in source config.
	alias := ConfigAliasForRegistry(entry.RegistryID)
	if alias != "" && !slices.ContainsFunc(mm.settings.Models.Enabled, func(id string) bool {
		return strings.EqualFold(id, alias)
	}) {
		mm.settings.Models.Enabled = append(mm.settings.Models.Enabled, alias)
	}

	// Publish the mutated settings so SaveSettings picks up our changes.
	conf.StoreSettings(mm.settings)
	if err := conf.SaveSettings(); err != nil {
		GetLogger().Warn("Failed to persist settings after model install",
			logger.String("catalog_id", entry.ID),
			logger.Error(err))
	}
}

// applyConfigForUninstall updates settings to reflect a removed model.
// For bat models, Enabled is only set to false when no other bat models
// remain installed. The caller must hold mm.mu (at least RLock).
// Settings are persisted to disk via conf.SaveSettings so changes survive
// restarts and are visible to concurrent readers through conf.Setting().
func (mm *ModelManager) applyConfigForUninstall(entry *CatalogEntry) {
	if mm.settings == nil {
		return
	}

	switch entry.RegistryID {
	case RegistryIDBirdNETV3:
		GetLogger().Info("BirdNET v3.0 config wiring not yet implemented",
			logger.String("catalog_id", entry.ID))
	case RegistryIDPerchV2:
		mm.settings.Perch.Enabled = false
		mm.settings.Perch.ModelPath = ""
		mm.settings.Perch.LabelPath = ""
	case RegistryIDBSG:
		mm.settings.BSG.Enabled = false
		mm.settings.BSG.ModelPath = ""
		mm.settings.BSG.LabelPath = ""
	case RegistryIDBat:
		// Only disable if no other bat models remain installed.
		otherBatInstalled := false
		for id := range mm.installed {
			other, found := GetCatalogEntry(id)
			if found && other.Category == CategoryBat {
				otherBatInstalled = true
				break
			}
		}
		if !otherBatInstalled {
			mm.settings.Bat.Enabled = false
		}
		mm.settings.Bat.ClassifierModel = ""
		mm.settings.Bat.LabelPath = ""
		mm.settings.Bat.EmbeddingModel = ""
	}

	// Remove config alias from Models.Enabled and from any source/stream that references it.
	alias := ConfigAliasForRegistry(entry.RegistryID)
	if alias != "" {
		mm.settings.Models.Enabled = slices.DeleteFunc(mm.settings.Models.Enabled, func(id string) bool {
			return strings.EqualFold(id, alias)
		})

		// Remove from sound card sources.
		for i := range mm.settings.Realtime.Audio.Sources {
			src := &mm.settings.Realtime.Audio.Sources[i]
			src.Models = slices.DeleteFunc(src.Models, func(id string) bool {
				return strings.EqualFold(id, alias)
			})
			if len(src.Models) == 0 {
				src.Models = []string{conf.ModelIDBirdNET}
			}
		}

		// Remove from RTSP/stream sources.
		for i := range mm.settings.Realtime.RTSP.Streams {
			stream := &mm.settings.Realtime.RTSP.Streams[i]
			stream.Models = slices.DeleteFunc(stream.Models, func(id string) bool {
				return strings.EqualFold(id, alias)
			})
			if len(stream.Models) == 0 {
				stream.Models = []string{conf.ModelIDBirdNET}
			}
		}
	}

	// Publish the mutated settings so SaveSettings picks up our changes.
	conf.StoreSettings(mm.settings)
	if err := conf.SaveSettings(); err != nil {
		GetLogger().Warn("Failed to persist settings after model uninstall",
			logger.String("catalog_id", entry.ID),
			logger.Error(err))
	}
}

// progressInterval is the minimum number of bytes between progress reports
// sent to the progress channel during a download.
const progressInterval = 1 << 20 // 1 MiB

// downloadHTTPClient is used for model file downloads with a generous timeout
// to accommodate large files on slow connections.
var downloadHTTPClient = &http.Client{
	Timeout: 30 * time.Minute,
}

// downloadFile downloads a file from url to destPath, verifying the SHA256
// checksum. The catalogID is used to update shared download state for SSE
// polling. completedBytes is the cumulative size of previously downloaded
// files, used so progress reflects total download, not just the current file.
// On failure, any temporary file is cleaned up.
func (mm *ModelManager) downloadFile(catalogID, url, destPath, expectedSHA256 string, totalBytes, completedBytes int64) error {
	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return errors.Newf("failed to create directory for %s: %v", destPath, err).
			Component("classifier.model_manager").
			Category(errors.CategoryFileIO).
			Context("dest_path", destPath).
			Build()
	}

	// Use a unique temp file in the same directory to avoid collisions when
	// multiple goroutines download the same shared file (e.g., embeddings).
	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), filepath.Base(destPath)+".*.tmp")
	if err != nil {
		return errors.Newf("failed to create temp file for %s: %v", destPath, err).
			Component("classifier.model_manager").
			Category(errors.CategoryFileIO).
			Context("dest_path", destPath).
			Build()
	}
	tmpPath := tmpFile.Name()

	// Always close the temp file and clean it up on error.
	success := false
	defer func() {
		_ = tmpFile.Close()
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	resp, err := downloadHTTPClient.Get(url) //nolint:noctx // URL is constructed from catalog metadata, not user input
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

	hasher := sha256.New()
	reader := io.TeeReader(resp.Body, hasher)

	var downloaded int64
	var lastReport int64
	buf := make([]byte, 32*1024) // 32 KiB read buffer

	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if _, writeErr := tmpFile.Write(buf[:n]); writeErr != nil {
				return errors.Newf("failed to write to %s: %v", tmpPath, writeErr).
					Component("classifier.model_manager").
					Category(errors.CategoryFileIO).
					Context("tmp_path", tmpPath).
					Build()
			}
			downloaded += int64(n)

			// Report progress at intervals (non-blocking to avoid stalling
			// the download if the SSE consumer disconnects).
			if downloaded-lastReport >= progressInterval || readErr == io.EOF {
				// Update shared download state with cumulative progress.
				mm.mu.Lock()
				if state, ok := mm.downloading[catalogID]; ok {
					state.DownloadedBytes = completedBytes + downloaded
				}
				mm.mu.Unlock()

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
	if err := tmpFile.Close(); err != nil {
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
