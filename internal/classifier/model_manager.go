// model_manager.go handles the lifecycle of downloadable models: scanning
// for installed models, tracking download progress, and uninstalling models.
package classifier

import (
	"maps"
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

// fileModTime returns the modification time for a file, or the zero time on error.
func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
