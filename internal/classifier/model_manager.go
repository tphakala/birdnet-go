// model_manager.go handles the lifecycle of downloadable models: scanning
// for installed models, tracking download progress, and uninstalling models.
package classifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
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

// failedStateRetention is how long a failed download state is kept in the
// downloading map so SSE pollers can observe the failure before cleanup.
const failedStateRetention = 30 * time.Second

// ModelManager handles the lifecycle of downloadable models.
type ModelManager struct {
	modelsDir    string
	orchestrator *Orchestrator
	settings     *conf.Settings // nil sentinel: non-nil means config sync is enabled
	mu           sync.RWMutex
	settingsMu   sync.Mutex // serializes clone-mutate-publish cycles on settings
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
	if orchestrator != nil {
		orchestrator.SetModelsDir(modelsDir)
	}
	return &ModelManager{
		modelsDir:    modelsDir,
		orchestrator: orchestrator,
		settings:     settings,
		installed:    make(map[string]InstalledModel),
		downloading:  make(map[string]*DownloadState),
	}
}

// ModelInfos returns ModelInfo for all currently loaded models.
func (mm *ModelManager) ModelInfos() []ModelInfo {
	if mm.orchestrator == nil {
		return nil
	}
	return mm.orchestrator.ModelInfos()
}

// ScanInstalled scans modelsDir for subdirectories matching catalog IDs. For
// each matching subdirectory, it checks whether the ONNX model file (the
// CatalogFile with Role "model") exists on disk. If found, the model is
// recorded as installed.
func (mm *ModelManager) ScanInstalled() {
	log := GetLogger()

	// Phase 1: scan the filesystem under mm.mu.
	mm.mu.Lock()
	for i := range EmbeddedCatalog {
		entry := &EmbeddedCatalog[i]
		subdir := filepath.Join(mm.modelsDir, entry.ID)

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

		// Shared-only entries (e.g. geomodels): all files live in models/shared/.
		// Detect these by checking that every file is a shared role and all exist.
		if modelFile == "" {
			if mm.scanSharedOnlyEntry(log, entry) {
				continue
			}
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

	installedIDs := slices.Collect(maps.Keys(mm.installed))
	log.Info("Model scan complete",
		logger.Int("installed_count", len(mm.installed)))
	mm.mu.Unlock()

	// Phase 2: sync Models.Enabled and load models (lock-free).
	if mm.settings != nil {
		mm.settingsMu.Lock()
		updated := conf.CloneSettings(conf.GetSettings())
		changed := false

		if !slices.ContainsFunc(updated.Models.Enabled, func(id string) bool {
			return strings.EqualFold(id, conf.ModelIDBirdNET)
		}) {
			updated.Models.Enabled = append([]string{conf.ModelIDBirdNET}, updated.Models.Enabled...)
			changed = true
		}
		addIfMissing := func(alias string) {
			if alias != "" && !slices.ContainsFunc(updated.Models.Enabled, func(id string) bool {
				return strings.EqualFold(id, alias)
			}) {
				updated.Models.Enabled = append(updated.Models.Enabled, alias)
				changed = true
			}
		}

		for _, catalogID := range installedIDs {
			entry, found := GetCatalogEntry(catalogID)
			if !found {
				continue
			}
			addIfMissing(ConfigAliasForRegistry(entry.RegistryID))
		}

		if updated.Bat.ClassifierModel != "" {
			addIfMissing(conf.ModelIDBat)
		}
		if updated.Perch.ModelPath != "" {
			addIfMissing(conf.ModelIDPerchV2)
		}
		if updated.BSG.ModelPath != "" {
			addIfMissing(conf.ModelIDBSG)
		}

		if changed {
			conf.StoreSettings(updated)
			if err := conf.SaveSettings(); err != nil {
				log.Warn("Failed to persist Models.Enabled sync",
					logger.Error(err))
			}
		}
		mm.settingsMu.Unlock()

		mm.loadInstalledModels(log, installedIDs)

		// After loading models, check if any installed model has geomodel
		// companion files on disk. If so, ensure the range filter config is
		// up to date and reload the filter. This handles the upgrade case
		// where a new binary adds geomodel support to existing models.
		mm.ensureGeomodelConfig(log, installedIDs)
	}
}

// geomodelOrphanAction is the decision the orphan self-heal makes for a
// gallery-managed geomodel range filter config when no installed geomodel-capable
// model was matched.
type geomodelOrphanAction int

const (
	// geomodelOrphanNone leaves the config untouched (custom paths, or already
	// consistent with the on-disk reality).
	geomodelOrphanNone geomodelOrphanAction = iota
	// geomodelOrphanPromote sets Model to the v3 literal because the gallery
	// shared files exist on disk.
	geomodelOrphanPromote
	// geomodelOrphanClear wipes the dead geomodel references because the gallery
	// shared files are absent.
	geomodelOrphanClear
)

// geomodelRangeFilterVersion is the literal that the runtime, status code, and
// UI key off to recognize the geomodel v3 range filter (matches the catalog
// entry GeomodelVersion for every geomodel-capable model).
const geomodelRangeFilterVersion = "v3"

// decideGeomodelOrphanAction is the pure decision for the orphan self-heal. It
// only acts when the range filter points at the EXACT gallery-managed shared
// paths; custom or hand-edited paths yield geomodelOrphanNone. When the shared
// files exist it promotes to v3 (no-op if already v3); when they are absent it
// clears the dead references (no-op if already cleared).
func decideGeomodelOrphanAction(rf *conf.RangeFilterSettings, expectedModelPath, expectedLabelsPath string, filesPresent bool) geomodelOrphanAction {
	// Only reconcile gallery-managed configs (exact match on both shared paths).
	if rf.ModelPath != expectedModelPath || rf.LabelsPath != expectedLabelsPath {
		return geomodelOrphanNone
	}

	if filesPresent {
		if rf.Model == geomodelRangeFilterVersion {
			return geomodelOrphanNone
		}
		return geomodelOrphanPromote
	}

	// Files absent: the gallery paths are still set (the guard above required an
	// exact, non-empty match), so clearing them is always a real change.
	return geomodelOrphanClear
}

// ensureGeomodelConfig checks if any installed model has geomodel companion
// files on disk and, if the range filter config doesn't already reflect them,
// updates the config and reloads the range filter. When NO installed
// geomodel-capable model is matched, it runs the orphan self-heal so a persisted
// config that references the gallery shared geomodel paths stays consistent with
// reality (promote when the shared files exist, clear when they are absent).
func (mm *ModelManager) ensureGeomodelConfig(log logger.Logger, installedIDs []string) {
	if mm.orchestrator == nil {
		return
	}

	for _, catalogID := range installedIDs {
		entry, found := GetCatalogEntry(catalogID)
		if !found || !HasGeomodelFiles(&entry) {
			continue
		}

		// Check if all geomodel files exist on disk.
		allPresent := true
		for _, f := range entry.Files {
			if !isGeomodelRole(f.Role) {
				continue
			}
			path := filepath.Join(mm.modelsDir, "shared", f.LocalName)
			if _, err := os.Stat(path); err != nil {
				allPresent = false
				break
			}
		}
		if !allPresent {
			continue
		}

		// A geomodel-capable model is installed with its files present. Apply
		// the install promote behavior and stop; the orphan self-heal must not run.
		mm.applyInstalledGeomodelConfig(log, &entry, catalogID)
		return
	}

	// No installed geomodel-capable model was matched. Reconcile a possibly
	// orphaned gallery-managed config with the shared files on disk.
	mm.healOrphanGeomodelConfig(log)
}

// applyInstalledGeomodelConfig promotes the range filter config to match an
// installed geomodel-capable model whose shared files are present on disk. It is
// a no-op when the config already matches the expected paths and version.
func (mm *ModelManager) applyInstalledGeomodelConfig(log logger.Logger, entry *CatalogEntry, catalogID string) {
	// Build expected paths from catalog entry.
	expectedModelPath := ""
	expectedLabelsPath := ""
	for _, f := range entry.Files {
		switch f.Role {
		case RoleGeomodelModel:
			expectedModelPath = filepath.Join(mm.modelsDir, "shared", f.LocalName)
		case RoleGeomodelLabels:
			expectedLabelsPath = filepath.Join(mm.modelsDir, "shared", f.LocalName)
		}
	}

	// Decide and write under settingsMu so the already-matches check and the
	// store operate on one consistent snapshot. Reading outside the lock would
	// let a concurrent install/uninstall publish a newer config between the
	// check and the store, overwriting it with stale data.
	mm.settingsMu.Lock()
	current := conf.GetSettings()
	rf := current.BirdNET.RangeFilter
	if rf.Model == entry.GeomodelVersion &&
		rf.ModelPath == expectedModelPath &&
		rf.LabelsPath == expectedLabelsPath {
		// Config already set; initializeMetaModel handled it at startup.
		mm.settingsMu.Unlock()
		return
	}

	// Config is stale or missing; update it.
	log.Info("Applying geomodel config for installed model",
		logger.String("catalog_id", catalogID),
		logger.String("geomodel_version", entry.GeomodelVersion))

	updated := conf.CloneSettings(current)
	updated.BirdNET.RangeFilter.Model = entry.GeomodelVersion
	updated.BirdNET.RangeFilter.ModelPath = expectedModelPath
	updated.BirdNET.RangeFilter.LabelsPath = expectedLabelsPath
	conf.StoreSettings(updated)
	if err := conf.SaveSettings(); err != nil {
		log.Warn("Failed to persist geomodel config",
			logger.String("catalog_id", catalogID),
			logger.Error(err))
	}
	mm.settingsMu.Unlock()

	if err := mm.orchestrator.ReloadRangeFilter(); err != nil {
		log.Warn("Failed to reload range filter after geomodel config update",
			logger.String("catalog_id", catalogID),
			logger.Error(err))
	}
}

// healOrphanGeomodelConfig reconciles a gallery-managed geomodel range filter
// config when no geomodel-capable model is installed. It promotes the config to
// v3 when the shared files are present (e.g. an upgrade that left Model unset),
// or clears the dead references when the shared files are absent (e.g. the user
// removed the only geomodel-capable model, leaving BirdNET v2.4 which cleanly
// uses the embedded TFLite filter). Custom paths are never touched. It only
// persists and reloads when something actually changed.
//
// On a normal startup, conf.Load's MigrateOrphanGeomodelRangeFilter has usually
// already applied the same promote/clear at config-load time, so this path is
// then a no-op. It still runs here to reload the range filter on the running
// orchestrator and to cover cases where the config migration did not persist
// (e.g. no config file on disk).
func (mm *ModelManager) healOrphanGeomodelConfig(log logger.Logger) {
	expectedModelPath := filepath.Join(mm.modelsDir, "shared", geomodelONNXLocalName)
	expectedLabelsPath := filepath.Join(mm.modelsDir, "shared", geomodelLabelsLocalName)

	filesPresent := true
	for _, path := range []string{expectedModelPath, expectedLabelsPath} {
		if _, err := os.Stat(path); err != nil {
			filesPresent = false
			break
		}
	}

	// Decide and write under settingsMu so the decision and the store operate on
	// one consistent snapshot. Reading the config outside the lock would let a
	// concurrent install publish a valid geomodel config between the decision
	// and the store, after which a stale "clear" would wipe it. The filesystem
	// check above is independent of settings, so it stays outside the lock.
	mm.settingsMu.Lock()
	current := conf.GetSettings()
	rf := current.BirdNET.RangeFilter
	action := decideGeomodelOrphanAction(&rf, expectedModelPath, expectedLabelsPath, filesPresent)
	if action == geomodelOrphanNone {
		mm.settingsMu.Unlock()
		return
	}

	updated := conf.CloneSettings(current)
	switch action {
	case geomodelOrphanPromote:
		log.Info("Promoting orphaned geomodel range filter config to v3 (shared files present)")
		updated.BirdNET.RangeFilter.Model = geomodelRangeFilterVersion
	case geomodelOrphanClear:
		log.Info("Clearing orphaned geomodel range filter config (shared files absent)")
		updated.BirdNET.RangeFilter.Model = ""
		updated.BirdNET.RangeFilter.ModelPath = ""
		updated.BirdNET.RangeFilter.LabelsPath = ""
		updated.BirdNET.RangeFilter.PassUnmappedSpecies = false
	case geomodelOrphanNone:
		// Unreachable: handled by the early return above.
	}
	conf.StoreSettings(updated)
	if err := conf.SaveSettings(); err != nil {
		log.Warn("Failed to persist orphan geomodel config self-heal",
			logger.Error(err))
	}
	mm.settingsMu.Unlock()

	if err := mm.orchestrator.ReloadRangeFilter(); err != nil {
		log.Warn("Failed to reload range filter after orphan geomodel self-heal",
			logger.Error(err))
	}
}

// loadInstalledModels loads any installed models that are not yet loaded in
// the orchestrator. The caller must provide the list of installed catalog IDs
// (collected while holding mm.mu) so this method runs lock-free.
func (mm *ModelManager) loadInstalledModels(log logger.Logger, installedIDs []string) {
	if mm.orchestrator == nil {
		return
	}
	for _, catalogID := range installedIDs {
		entry, found := GetCatalogEntry(catalogID)
		if !found || entry.RegistryID == "" {
			continue
		}
		if mm.orchestrator.IsModelLoaded(entry.RegistryID) {
			continue
		}
		if err := mm.orchestrator.LoadModel(entry.RegistryID); err != nil {
			log.Warn("failed to load installed model at startup",
				logger.String("catalog_id", catalogID),
				logger.String("registry_id", entry.RegistryID),
				logger.Error(err))
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

// scanSharedOnlyEntry checks whether a catalog entry whose files all live in
// models/shared/ (e.g. geomodels) is installed. If all shared files exist on
// disk, it registers the entry in mm.installed and returns true. The caller
// must hold mm.mu.
func (mm *ModelManager) scanSharedOnlyEntry(log logger.Logger, entry *CatalogEntry) bool {
	if !IsSharedOnly(entry) {
		return false
	}
	sharedDir := filepath.Join(mm.modelsDir, "shared")
	var modelPath, labelsPath string
	for _, f := range entry.Files {
		p := filepath.Join(sharedDir, f.LocalName)
		if _, err := os.Stat(p); err != nil {
			return false
		}
		switch f.Role {
		case RoleGeomodelModel:
			modelPath = p
		case RoleGeomodelLabels:
			labelsPath = p
		}
	}
	if modelPath == "" {
		return false
	}
	mm.installed[entry.ID] = InstalledModel{
		CatalogID:   entry.ID,
		ModelPath:   modelPath,
		LabelsPath:  labelsPath,
		InstalledAt: fileModTime(modelPath),
		Version:     entry.Version,
	}
	log.Debug("Found installed shared-only model",
		logger.String("catalog_id", entry.ID),
		logger.String("path", modelPath))
	return true
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
	// Only attempt unload if the model is currently loaded; if it is not
	// loaded, file deletion is safe (nothing is memory-mapping the ONNX file).
	// If unload fails, abort: the model may still be memory-mapped by a
	// running inference engine, so deleting the file could cause a segfault.
	if mm.orchestrator != nil && entry.RegistryID != "" && mm.orchestrator.IsModelLoaded(entry.RegistryID) {
		if err := mm.orchestrator.UnloadModel(entry.RegistryID); err != nil {
			return errors.Newf("cannot uninstall %s: model still in use", catalogID).
				Component("classifier.model_manager").
				Category(errors.CategorySystem).
				Context("catalog_id", catalogID).
				Context("registry_id", entry.RegistryID).
				Context("unload_error", err.Error()).
				Build()
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

	// Update installed map and config BEFORE reloading range filter so the
	// reload sees the cleared geomodel path and does not re-acquire the handle.
	delete(mm.installed, catalogID)
	mm.applyConfigForUninstall(&entry)

	// Reload range filter with updated config (geomodel cleared), then delete files.
	// Skip geomodel file deletion if reload fails (session may still hold handles).
	geomodelReloadOK := true
	if mm.orchestrator != nil && HasGeomodelFiles(&entry) {
		if err := mm.orchestrator.ReloadRangeFilter(); err != nil {
			geomodelReloadOK = false
			log.Warn("Range filter reload failed after geomodel uninstall, retaining geomodel files",
				logger.String("catalog_id", catalogID),
				logger.Error(err))
		}
	}

	// Clean up shared embeddings, geomodel, and taxonomy files if no other dependent models remain.
	mm.cleanupSharedFiles(log, catalogID, &entry, HasEmbeddingsFiles, isEmbeddingsRole, "embeddings")
	if geomodelReloadOK {
		mm.cleanupSharedFiles(log, catalogID, &entry, HasGeomodelFiles, isGeomodelRole, "geomodel")
	}
	mm.cleanupSharedFiles(log, catalogID, &entry, HasTaxonomyFiles, isTaxonomyRole, "taxonomy")

	// Remove the per-model subdirectory if it is now empty (labels are retained,
	// so Remove will fail with ENOTEMPTY if any remain, which is the desired behavior).
	if err := os.Remove(subdir); err == nil {
		log.Info("Removed empty model directory",
			logger.String("path", subdir))
	} else if !os.IsNotExist(err) {
		log.Debug("Model directory not removed (likely non-empty)",
			logger.String("path", subdir))
	}

	log.Info("Model uninstalled",
		logger.String("catalog_id", catalogID))

	return nil
}

// cleanupSharedFiles removes shared files of a given kind when uninstalling
// a model, but only if no other installed or currently-downloading model
// depends on the same files.
// hasFiles checks whether a catalog entry depends on this kind of shared file.
// matchRole checks whether a CatalogFile belongs to this kind.
// The caller must hold mm.mu.
func (mm *ModelManager) cleanupSharedFiles(log logger.Logger, catalogID string, entry *CatalogEntry, hasFiles func(*CatalogEntry) bool, matchRole func(string) bool, label string) {
	if !hasFiles(entry) {
		return
	}
	for id := range mm.installed {
		if id == catalogID {
			continue
		}
		other, found := GetCatalogEntry(id)
		if found && hasFiles(&other) {
			log.Debug("Retaining shared "+label+" files; other dependent models still installed",
				logger.String("catalog_id", catalogID))
			return
		}
	}
	for id := range mm.downloading {
		if id == catalogID {
			continue
		}
		other, found := GetCatalogEntry(id)
		if found && hasFiles(&other) {
			log.Debug("Retaining shared "+label+" files; another model is downloading",
				logger.String("catalog_id", catalogID),
				logger.String("downloading_id", id))
			return
		}
	}
	for _, f := range entry.Files {
		if matchRole(f.Role) {
			path := filepath.Join(mm.modelsDir, "shared", f.LocalName)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Warn("Failed to remove "+label+" file",
					logger.String("path", path),
					logger.Error(err))
			} else if err == nil {
				log.Info("Removed shared "+label+" file",
					logger.String("path", path))
			}
		}
	}
}

// Install downloads all files for a catalog entry and records it as installed.
// The baseURL parameter overrides the HuggingFace URL for testing; pass an
// empty string to use the default HuggingFace URL constructed from the entry's
// repo. Progress is reported via the channel if non-nil.
func (mm *ModelManager) Install(ctx context.Context, entry *CatalogEntry, baseURL string, progress chan<- DownloadState) error {
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

	if err := mm.downloadModelFiles(ctx, entry, baseURL, progress, true); err != nil {
		// Keep failed state briefly for SSE pollers, then clean up.
		time.AfterFunc(failedStateRetention, func() {
			mm.removeDownloading(entry.ID)
		})
		return err
	}

	return nil
}

// Reinstall re-downloads missing or corrupt files for an already-installed model.
// Files that pass SHA256 validation are skipped. The baseURL parameter overrides
// the HuggingFace URL for testing; pass an empty string to use the default.
// Progress is reported via the channel if non-nil.
func (mm *ModelManager) Reinstall(ctx context.Context, entry *CatalogEntry, baseURL string, progress chan<- DownloadState) error {
	// Check that the model IS installed (opposite of Install's guard).
	mm.mu.Lock()
	if _, ok := mm.installed[entry.ID]; !ok {
		mm.mu.Unlock()
		return errors.Newf("model %s is not installed", entry.ID).
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

	if err := mm.downloadModelFiles(ctx, entry, baseURL, progress, false); err != nil {
		// Keep failed state briefly for SSE pollers, then clean up.
		time.AfterFunc(failedStateRetention, func() {
			mm.removeDownloading(entry.ID)
		})
		return err
	}

	return nil
}

// downloadModelFiles handles the actual file download, validation, recording,
// config application, and hot-load for a catalog entry. The caller must have
// already registered the entry in mm.downloading before calling this method.
// On failure, downloadModelFiles calls markFailed but the caller is responsible
// for scheduling cleanup of the download state (e.g., via time.AfterFunc).
// When cleanupOnFailure is true (Install), newly downloaded files are removed
// on failure. When false (Reinstall), repaired files are kept so partial
// progress is not lost.
func (mm *ModelManager) downloadModelFiles(ctx context.Context, entry *CatalogEntry, baseURL string, progress chan<- DownloadState, cleanupOnFailure bool) error {
	log := GetLogger()

	// Create model subdirectory only if the entry has non-shared files.
	// Shared-only entries (e.g. geomodels) store all files in models/shared/.
	subdir := filepath.Join(mm.modelsDir, entry.ID)
	if !IsSharedOnly(entry) {
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			mkdirErr := errors.Newf("failed to create model directory %s: %v", subdir, err).
				Component("classifier.model_manager").
				Category(errors.CategoryFileIO).
				Context("catalog_id", entry.ID).
				Context("directory", subdir).
				Build()
			mm.markFailed(entry.ID, mkdirErr, progress)
			return mkdirErr
		}
	}

	// Track files we downloaded so we can clean up on failure.
	var downloadedFiles []string

	cleanup := func() {
		for _, f := range downloadedFiles {
			_ = os.Remove(f)
		}
	}

	// fileDestPath returns the local destination for a catalog file.
	// Shared files (embeddings, geomodel, taxonomy) are stored in a common directory.
	fileDestPath := func(f CatalogFile) string {
		if isSharedRole(f.Role) {
			return filepath.Join(mm.modelsDir, "shared", f.LocalName)
		}
		return filepath.Join(subdir, f.LocalName)
	}

	// Compute cumulative totals for progress tracking across all files.
	// Also validate existing shared files and mark corrupt ones for re-download.
	needsRedownload := make(map[string]bool)
	var totalAllBytes int64
	filesToDownload := 0
	for _, f := range entry.Files {
		destPath := fileDestPath(f)
		if _, err := os.Stat(destPath); err != nil {
			totalAllBytes += f.SizeBytes
			filesToDownload++
		} else if f.SHA256 != "" && !verifySHA256(destPath, f.SHA256) {
			log.Warn("Existing file failed SHA256 validation, will re-download",
				logger.String("catalog_id", entry.ID),
				logger.String("path", destPath))
			needsRedownload[destPath] = true
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

		// Skip download if file already exists and passes SHA256 validation.
		if _, err := os.Stat(destPath); err == nil && !needsRedownload[destPath] {
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

		// Build download URL. Per-file HuggingFaceRepo overrides the entry-level repo,
		// allowing companion files (e.g., geomodel) to live in a separate repository.
		var url string
		if baseURL != "" {
			url = baseURL + "/" + f.RemotePath
		} else {
			repo := entry.HuggingFaceRepo
			if f.HuggingFaceRepo != "" {
				repo = f.HuggingFaceRepo
			}
			url = buildHuggingFaceURL(repo, f.RemotePath)
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

		if err := mm.downloadFile(ctx, entry.ID, url, destPath, f.SHA256, completedBytes); err != nil {
			log.Error("Failed to download file",
				logger.String("catalog_id", entry.ID),
				logger.String("url", url),
				logger.Error(err))
			mm.markFailed(entry.ID, err, progress)
			if cleanupOnFailure {
				cleanup()
			}
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

	// For shared-only entries (e.g. geomodels), derive paths from shared files.
	modelPath, labelsPath = resolveSharedPaths(entry, modelPath, labelsPath, fileDestPath)

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

	mm.hotLoadAfterInstall(log, entry)
	sendProgress(progress, entry.ID, StatusComplete)

	log.Info("Model installed",
		logger.String("catalog_id", entry.ID),
		logger.String("model_path", modelPath))

	return nil
}

// hotLoadAfterInstall hot-loads the classifier model and, if the entry
// includes geomodel companion files, reloads the range filter.
func (mm *ModelManager) hotLoadAfterInstall(log logger.Logger, entry *CatalogEntry) {
	if mm.orchestrator == nil {
		return
	}
	if entry.RegistryID != "" {
		if err := mm.orchestrator.LoadModel(entry.RegistryID); err != nil {
			log.Warn("Failed to hot-load model (will be available after restart)",
				logger.String("catalog_id", entry.ID),
				logger.Error(err))
		}
	}
	if HasGeomodelFiles(entry) {
		if err := mm.orchestrator.ReloadRangeFilter(); err != nil {
			log.Warn("Failed to hot-reload range filter after geomodel install",
				logger.String("catalog_id", entry.ID),
				logger.Error(err))
		}
	}
}

// resolveSharedPaths fills in modelPath and labelsPath for shared-only entries
// (e.g. geomodels) that have no RoleModel or RoleLabels files.
func resolveSharedPaths(entry *CatalogEntry, modelPath, labelsPath string, destPath func(CatalogFile) string) (resolvedModel, resolvedLabels string) {
	if modelPath != "" {
		return modelPath, labelsPath
	}
	for _, f := range entry.Files {
		switch f.Role {
		case RoleGeomodelModel:
			modelPath = destPath(f)
		case RoleGeomodelLabels:
			labelsPath = destPath(f)
		}
	}
	return modelPath, labelsPath
}

// sendProgress sends a non-blocking status update to the progress channel.
func sendProgress(progress chan<- DownloadState, catalogID, status string) {
	if progress == nil {
		return
	}
	select {
	case progress <- DownloadState{
		CatalogID: catalogID,
		Status:    status,
	}:
	default:
	}
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
// Only fields with non-empty paths are set. The caller must hold no locks
// other than mm.settingsMu (acquired internally).
// Uses clone-mutate-publish so the shared settings snapshot is never mutated
// in place. Settings are persisted to disk via conf.SaveSettings so changes
// survive restarts and are visible to concurrent readers through conf.Setting().
func (mm *ModelManager) applyConfigForInstall(entry *CatalogEntry, modelPath, labelsPath, embeddingsPath string) {
	if mm.settings == nil {
		return
	}

	mm.settingsMu.Lock()
	defer mm.settingsMu.Unlock()

	updated := conf.CloneSettings(conf.GetSettings())

	switch entry.RegistryID {
	case RegistryIDBirdNETV3:
		// BirdNET v3.0 acoustic model config will be added when the model is released.
		// Geomodel range filter config is applied generically below.
	case RegistryIDPerchV2:
		if modelPath != "" {
			updated.Perch.ModelPath = modelPath
		}
		if labelsPath != "" {
			updated.Perch.LabelPath = labelsPath
		}
	case RegistryIDBSG:
		if modelPath != "" {
			updated.BSG.ModelPath = modelPath
		}
		if labelsPath != "" {
			updated.BSG.LabelPath = labelsPath
		}
	case RegistryIDBat:
		if modelPath != "" {
			updated.Bat.ClassifierModel = modelPath
		}
		if labelsPath != "" {
			updated.Bat.LabelPath = labelsPath
		}
		if embeddingsPath != "" {
			updated.Bat.EmbeddingModel = embeddingsPath
		}
	}

	// Apply geomodel range filter config if this entry includes geomodel files.
	if HasGeomodelFiles(entry) && entry.GeomodelVersion != "" {
		updated.BirdNET.RangeFilter.Model = entry.GeomodelVersion
		for _, f := range entry.Files {
			switch f.Role {
			case RoleGeomodelModel:
				updated.BirdNET.RangeFilter.ModelPath = filepath.Join(mm.modelsDir, "shared", f.LocalName)
			case RoleGeomodelLabels:
				updated.BirdNET.RangeFilter.LabelsPath = filepath.Join(mm.modelsDir, "shared", f.LocalName)
			}
		}
	}

	// Add config alias to Models.Enabled so the model appears in source config.
	alias := ConfigAliasForRegistry(entry.RegistryID)
	if alias != "" && !slices.ContainsFunc(updated.Models.Enabled, func(id string) bool {
		return strings.EqualFold(id, alias)
	}) {
		updated.Models.Enabled = append(updated.Models.Enabled, alias)
	}

	conf.StoreSettings(updated)
	if err := conf.SaveSettings(); err != nil {
		GetLogger().Warn("Failed to persist settings after model install",
			logger.String("catalog_id", entry.ID),
			logger.Error(err))
	}
}

// applyConfigForUninstall updates settings to reflect a removed model.
// For bat models, Enabled is only set to false when no other bat models
// remain installed; if another bat model exists, config is re-pointed to it.
// The caller must hold mm.mu for writing; the uninstalled entry must already
// be deleted from mm.installed so the geomodel and bat searches skip it.
// Uses clone-mutate-publish so the shared settings snapshot is never mutated
// in place. Settings are persisted to disk via conf.SaveSettings so changes
// survive restarts and are visible to concurrent readers through conf.Setting().
func (mm *ModelManager) applyConfigForUninstall(entry *CatalogEntry) {
	if mm.settings == nil {
		return
	}

	mm.settingsMu.Lock()
	defer mm.settingsMu.Unlock()

	updated := conf.CloneSettings(conf.GetSettings())
	retainAlias := false

	switch entry.RegistryID {
	case RegistryIDBirdNETV3:
		// BirdNET v3.0 acoustic model config will be cleared when the model is released.
		// Geomodel range filter config is handled generically below.
	case RegistryIDPerchV2:
		updated.Perch.ModelPath = ""
		updated.Perch.LabelPath = ""
	case RegistryIDBSG:
		updated.BSG.ModelPath = ""
		updated.BSG.LabelPath = ""
	case RegistryIDBat:
		// Find another installed bat model to re-point config to.
		var replacement *InstalledModel
		var replacementEntry CatalogEntry
		for id, inst := range mm.installed {
			other, found := GetCatalogEntry(id)
			if found && other.Category == CategoryBat {
				replacement = &inst
				replacementEntry = other
				break
			}
		}
		if replacement == nil {
			updated.Bat.ClassifierModel = ""
			updated.Bat.LabelPath = ""
			updated.Bat.EmbeddingModel = ""
		} else {
			retainAlias = true
			updated.Bat.ClassifierModel = replacement.ModelPath
			updated.Bat.LabelPath = replacement.LabelsPath
			updated.Bat.EmbeddingModel = ""
			for _, f := range replacementEntry.Files {
				if f.Role == RoleEmbeddings {
					updated.Bat.EmbeddingModel = filepath.Join(mm.modelsDir, "shared", f.LocalName)
					break
				}
			}
		}
	}

	// Reset geomodel range filter config if no other geomodel-dependent model remains.
	// mm.installed no longer contains the uninstalled entry (deleted by caller).
	if HasGeomodelFiles(entry) {
		otherGeomodel := false
		for id := range mm.installed {
			other, found := GetCatalogEntry(id)
			if found && HasGeomodelFiles(&other) {
				otherGeomodel = true
				break
			}
		}
		if !otherGeomodel {
			updated.BirdNET.RangeFilter.Model = ""
			updated.BirdNET.RangeFilter.ModelPath = ""
			updated.BirdNET.RangeFilter.LabelsPath = ""
			updated.BirdNET.RangeFilter.PassUnmappedSpecies = false
		}
	}

	// Remove config alias from Models.Enabled and from any source/stream that
	// references it, but only when no replacement model of the same category exists.
	alias := ConfigAliasForRegistry(entry.RegistryID)
	if alias != "" && !retainAlias {
		updated.Models.Enabled = slices.DeleteFunc(updated.Models.Enabled, func(id string) bool {
			return strings.EqualFold(id, alias)
		})

		// Remove from sound card sources.
		for i := range updated.Realtime.Audio.Sources {
			src := &updated.Realtime.Audio.Sources[i]
			src.Models = slices.DeleteFunc(src.Models, func(id string) bool {
				return strings.EqualFold(id, alias)
			})
			if len(src.Models) == 0 {
				src.Models = []string{conf.ModelIDBirdNET}
			}
		}

		// Remove from RTSP/stream sources.
		for i := range updated.Realtime.RTSP.Streams {
			stream := &updated.Realtime.RTSP.Streams[i]
			stream.Models = slices.DeleteFunc(stream.Models, func(id string) bool {
				return strings.EqualFold(id, alias)
			})
			if len(stream.Models) == 0 {
				stream.Models = []string{conf.ModelIDBirdNET}
			}
		}
	}

	conf.StoreSettings(updated)
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
func (mm *ModelManager) downloadFile(ctx context.Context, catalogID, url, destPath, expectedSHA256 string, completedBytes int64) error {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return errors.Newf("failed to create request for %s: %v", url, err).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("url", url).
			Build()
	}
	resp, err := downloadHTTPClient.Do(req)
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

	var hasher hash.Hash
	var reader io.Reader
	if expectedSHA256 != "" {
		hasher = sha256.New()
		reader = io.TeeReader(resp.Body, hasher)
	} else {
		reader = resp.Body
	}

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

	// Verify checksum (skip when the catalog entry has no expected hash).
	if expectedSHA256 != "" {
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

// verifySHA256 checks whether the file at path matches the expected hex-encoded
// SHA-256 checksum. Returns true on match, false on mismatch or any I/O error.
func verifySHA256(path, expected string) bool {
	if expected == "" {
		return true
	}
	f, err := os.Open(path) //nolint:gosec // G304: path is from catalog metadata
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expected)
}

// fileModTime returns the modification time for a file, or the zero time on error.
func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
