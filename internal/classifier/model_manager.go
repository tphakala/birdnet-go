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
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
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

// diskSpaceMarginBytes is the free-space headroom required beyond a download's
// total size before an install proceeds. It absorbs filesystem overhead and
// leaves a little slack on small SD cards, which matter here: variants run from
// 38 MB (int8-arm v2.4) to 557 MB (global v3.0 fp32) against Pi storage.
const diskSpaceMarginBytes int64 = 64 << 20 // 64 MiB

// settingsWriteMu serializes clone-mutate-publish cycles on the global conf
// settings across the whole classifier package. Every writer reads
// conf.GetSettings(), mutates a clone, and publishes it with conf.StoreSettings.
// Without one shared lock two such cycles interleave and the second clobbers the
// first's write. It is package-level rather than a ModelManager field because
// the Orchestrator's stale-path repair (applyPathCorrection) is a writer too and
// must serialize against ModelManager's install/uninstall config writers.
var settingsWriteMu sync.Mutex

// ModelManager handles the lifecycle of downloadable models.
type ModelManager struct {
	modelsDir    string
	orchestrator *Orchestrator
	settings     *conf.Settings // nil sentinel: non-nil means config sync is enabled
	mu           sync.RWMutex
	installed    map[string]InstalledModel
	downloading  map[string]*DownloadState

	// freeSpaceFn reports the bytes available on the filesystem holding a given
	// path. It is a field so tests can force the insufficient-space branch of the
	// install preflight without exhausting a real disk. NewModelManager wires it
	// to diskmanager.GetAvailableSpace.
	freeSpaceFn func(string) (uint64, error)

	// topologyChangedCb, when set, is invoked after a successful model load or
	// unload so observers (e.g. the metrics SSE stream) can signal that the
	// inference topology changed. It is injected to keep this package free of an
	// internal/observability import (which would create an import cycle). The
	// atomic pointer makes concurrent set and notify safe (the setter is
	// exported, so a caller could re-register a callback while loads/unloads run).
	topologyChangedCb atomic.Pointer[func()]

	// endpointResolver, when set, orders the HuggingFace endpoint chain per file
	// and remembers the host that worked, so downloads fail over from a blocked
	// canonical host to the mirror. It is injected at startup; a nil resolver
	// preserves the single-endpoint behavior, so callers that never inject one
	// keep downloading from exactly one host. The atomic pointer makes the
	// exported setter safe against concurrent installs.
	endpointResolver atomic.Pointer[EndpointResolver]
}

// EndpointResolver orders the HuggingFace endpoint chain to try for a download
// and records the host that served it, enabling automatic mirror failover.
// *conf.HFEndpointResolver implements it; the interface keeps this package
// decoupled from that concrete type and lets tests drive the failover loop.
type EndpointResolver interface {
	// OrderedEndpoints returns the base URLs to try, most-preferred first, for
	// the given settings override.
	OrderedEndpoints(configured string) []string
	// NoteWorking records the endpoint that just served a request so later calls
	// prefer it.
	NoteWorking(endpoint string)
}

// InstalledModel represents a model that has been downloaded and is available.
type InstalledModel struct {
	CatalogID string `json:"catalogId"`
	// VariantID is the id of the installed hardware variant (e.g. "fp32",
	// "int8-arm"). Empty means a flat, pre-variant entry with a single implicit
	// variant. It is recorded at install time (from the default variant) and
	// re-derived from disk on every ScanInstalled; it is never persisted, so no
	// on-disk migration is needed for existing installs.
	VariantID   string    `json:"variantId,omitempty"`
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
		freeSpaceFn:  diskmanager.GetAvailableSpace,
	}
}

// ModelInfos returns ModelInfo for all currently loaded models.
func (mm *ModelManager) ModelInfos() []ModelInfo {
	if mm.orchestrator == nil {
		return nil
	}
	return mm.orchestrator.ModelInfos()
}

// SetTopologyChangedCallback registers a callback invoked after a successful
// model load or unload. It is injected (rather than imported) to avoid an
// internal/observability import cycle. Passing nil disables the callback. The
// store is atomic, so it is safe to call concurrently with notifyTopologyChanged.
func (mm *ModelManager) SetTopologyChangedCallback(cb func()) {
	if cb == nil {
		mm.topologyChangedCb.Store(nil)
		return
	}
	mm.topologyChangedCb.Store(&cb)
}

// notifyTopologyChanged invokes the registered topology-changed callback if one
// is set. It must be called outside any held lock, since the callback may run
// arbitrary observer code. The load is atomic, so it is safe to call
// concurrently with SetTopologyChangedCallback.
func (mm *ModelManager) notifyTopologyChanged() {
	if p := mm.topologyChangedCb.Load(); p != nil {
		(*p)()
	}
}

// ScanInstalled scans modelsDir for subdirectories matching catalog IDs. For
// each matching subdirectory, it checks whether the ONNX model file (the
// CatalogFile with Role "model") exists on disk. If found, the model is
// recorded as installed.
func (mm *ModelManager) ScanInstalled() {
	log := GetLogger()

	// Snapshot the active catalog (honors a user-edited model-catalog.json)
	// before taking mm.mu; ActiveCatalog acquires its own lock.
	catalog := ActiveCatalog()

	// Snapshot settings once for the variant tie-break (which variant a family's
	// recorded model path points at). GetSettings returns an atomic snapshot.
	settings := conf.GetSettings()

	// Phase 1: scan the filesystem under mm.mu.
	mm.mu.Lock()
	// Preserve in-flight installs/switches: replaceVariant swaps mm.installed and
	// writes files after its own unlock while entry.ID stays in mm.downloading, so
	// re-deriving those entries from disk here (both variant files may be present
	// mid-switch) would record the wrong variant. Keep the in-flight record and
	// skip the disk scan for those ids. Reconcile the rest against disk: Phase 1
	// fully repopulates mm.installed by statting each entry's files, so clearing
	// first drops any model whose files were removed out-of-band since the last
	// scan. The clear and the repopulation both run under mm.mu, so a concurrent
	// reader (RLock) never observes the empty map.
	inFlight := make(map[string]InstalledModel)
	for id := range mm.downloading {
		if im, ok := mm.installed[id]; ok {
			inFlight[id] = im
		}
	}
	clear(mm.installed)
	maps.Copy(mm.installed, inFlight)
	for i := range catalog {
		entry := &catalog[i]
		if _, downloading := mm.downloading[entry.ID]; downloading {
			continue
		}
		subdir := filepath.Join(mm.modelsDir, entry.ID)

		// Variant entries: detect which hardware variant is present on disk
		// (the default variant's filename alone would miss a non-default install).
		// validateCatalogEntryFiles guarantees every variant carries a model-role
		// file, so a variant entry is never shared-only and never needs the
		// shared-only fall-through below.
		if len(entry.Variants) > 0 {
			if im, ok := scanVariantEntry(entry, subdir, installedModelBasenameHint(settings, entry.RegistryID)); ok {
				mm.installed[entry.ID] = im
				log.Debug("Found installed model variant",
					logger.String("catalog_id", entry.ID),
					logger.String("variant_id", im.VariantID),
					logger.String("path", im.ModelPath))
			}
			continue
		}

		modelFile, labelsFile := modelAndLabelsFiles(entry.Files)

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
		settingsWriteMu.Lock()
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
		settingsWriteMu.Unlock()

		mm.loadInstalledModels(log, installedIDs)

		// After loading models, check if any installed model has geomodel
		// companion files on disk. If so, ensure the range filter config is
		// up to date and reload the filter. This handles the upgrade case
		// where a new binary adds geomodel support to existing models.
		mm.ensureGeomodelConfig(log, installedIDs)
	}
}

// modelAndLabelsFiles extracts the model and labels file LocalNames from a list
// of catalog files. Either return value is empty when the corresponding role is
// absent (e.g. shared-only entries have no model role).
func modelAndLabelsFiles(files []CatalogFile) (modelFile, labelsFile string) {
	for _, f := range files {
		switch f.Role {
		case RoleModel:
			modelFile = f.LocalName
		case RoleLabels:
			labelsFile = f.LocalName
		}
	}
	return modelFile, labelsFile
}

// scanVariantEntry determines which variant of a variant-carrying catalog entry
// is installed on disk under subdir. modelBasenameHint is the basename of the
// model path recorded in settings for this family (empty when unknown): the
// variant whose model file matches it is preferred, so an ambiguous on-disk state
// (e.g. both files present after a crashed replace) resolves to the variant the
// loader will actually open. It then falls back to the default variant, then the
// remaining variants in catalog order. ok is false when no variant's model file
// is present on disk.
func scanVariantEntry(entry *CatalogEntry, subdir, modelBasenameHint string) (InstalledModel, bool) {
	// Permanent entry with a BuiltIn baseline (BirdNET v2.4): the model is ALWAYS
	// installed, but which variant is active is decided by the settings hint alone,
	// NOT by which files happen to be on disk. A non-empty hint that matches a
	// DFT-truncated variant's file (present on disk) selects it; anything else
	// resolves to the BuiltIn baseline with an empty ModelPath. This inverts the
	// usual "any file present -> that variant" fall-through: a stale DFT file left on
	// disk after the user reverted to the baseline (BirdNET.ModelPath cleared) must
	// NOT be reported as the active variant, because the primary loader opens the
	// embedded model, not that file.
	if builtin := builtInVariant(entry); builtin != nil {
		// A non-empty hint that matches a DFT-truncated variant's file selects it;
		// anything else (including an empty hint, or a stale DFT file whose variant
		// the hint no longer points at) resolves to the baseline below. The BuiltIn
		// baseline itself never matches: it carries no model file.
		if im, ok := variantByModelHint(entry, subdir, modelBasenameHint); ok {
			return im, true
		}
		// Baseline: always installed, no model path (the embedded model is used).
		return InstalledModel{
			CatalogID:   entry.ID,
			VariantID:   builtin.ID,
			ModelPath:   "",
			InstalledAt: time.Now(),
			Version:     entry.Version,
		}, true
	}

	// Prefer the variant whose model file matches the persisted settings path.
	// After a crashed replace both variants' files can be present on disk; the
	// settings path is what the loader (buildPerch/buildBirdNETV3) actually opens,
	// so aligning the detected variant to it keeps the reported install consistent
	// with what runs, rather than silently reporting the default.
	if im, ok := variantByModelHint(entry, subdir, modelBasenameHint); ok {
		return im, true
	}
	def := defaultVariant(entry)
	if def != nil {
		if im, ok := installedFromVariant(entry, def, subdir); ok {
			return im, true
		}
	}
	for i := range entry.Variants {
		v := &entry.Variants[i]
		if def != nil && v.ID == def.ID {
			continue
		}
		if im, ok := installedFromVariant(entry, v, subdir); ok {
			return im, true
		}
	}
	return InstalledModel{}, false
}

// variantByModelHint returns the InstalledModel for the variant of entry whose
// model file basename matches modelBasenameHint and exists on disk under subdir.
// ok is false when the hint is empty, no variant's model file matches it, or the
// matched variant's model file is absent. A BuiltIn baseline never matches: it
// carries no model file, so its basename is "" and cannot equal a non-empty hint.
// It is the shared tie-break behind both scanVariantEntry paths (the BuiltIn
// baseline branch and the general multi-variant branch).
func variantByModelHint(entry *CatalogEntry, subdir, modelBasenameHint string) (InstalledModel, bool) {
	if modelBasenameHint == "" {
		return InstalledModel{}, false
	}
	for i := range entry.Variants {
		v := &entry.Variants[i]
		mf, _ := modelAndLabelsFiles(v.Files)
		if mf == modelBasenameHint {
			// First (and, since basenames are unique, only) hint match wins; if its
			// file is absent, ok is false and the caller falls through to the default.
			return installedFromVariant(entry, v, subdir)
		}
	}
	return InstalledModel{}, false
}

// installedModelBasenameHint returns the basename of the model path recorded in
// settings for the given registry ID, or "" when settings are absent or the
// family carries no path. It is the tie-break scanVariantEntry uses to resolve an
// ambiguous multi-variant on-disk state to the variant the loader actually opens.
func installedModelBasenameHint(settings *conf.Settings, registryID string) string {
	if settings == nil {
		return ""
	}
	var p string
	switch registryID {
	case permanentRegistryID:
		// The permanent BirdNET v2.4 classifier: its selected DFT-truncated file
		// (if any) is recorded in BirdNET.ModelPath. An empty path means the
		// embedded BuiltIn baseline is active.
		p = settings.BirdNET.ModelPath
	case RegistryIDBirdNETV3:
		p = settings.BirdNETV3.ModelPath
	case RegistryIDPerchV2:
		p = settings.Perch.ModelPath
	case RegistryIDBSG:
		p = settings.BSG.ModelPath
	case RegistryIDBat:
		p = settings.Bat.ClassifierModel
	}
	if p == "" {
		return ""
	}
	return filepath.Base(p)
}

// installedFromVariant builds the InstalledModel for a specific variant if its
// model file exists on disk under subdir. ok is false when the variant carries
// no model role or its model file is absent.
func installedFromVariant(entry *CatalogEntry, v *CatalogVariant, subdir string) (InstalledModel, bool) {
	modelFile, labelsFile := modelAndLabelsFiles(v.Files)
	if modelFile == "" {
		return InstalledModel{}, false
	}
	modelPath := filepath.Join(subdir, modelFile)
	if _, err := os.Stat(modelPath); err != nil {
		return InstalledModel{}, false
	}
	labelsPath := ""
	if labelsFile != "" {
		labelsPath = filepath.Join(subdir, labelsFile)
	}
	return InstalledModel{
		CatalogID:   entry.ID,
		VariantID:   v.ID,
		ModelPath:   modelPath,
		LabelsPath:  labelsPath,
		InstalledAt: fileModTime(modelPath),
		Version:     entry.Version,
	}, true
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

	// Decide and write under settingsWriteMu so the already-matches check and the
	// store operate on one consistent snapshot. Reading outside the lock would
	// let a concurrent install/uninstall publish a newer config between the
	// check and the store, overwriting it with stale data.
	settingsWriteMu.Lock()
	current := conf.GetSettings()
	rf := current.BirdNET.RangeFilter
	if rf.Model == entry.GeomodelVersion &&
		rf.ModelPath == expectedModelPath &&
		rf.LabelsPath == expectedLabelsPath {
		// Config already set; initializeMetaModel handled it at startup.
		settingsWriteMu.Unlock()
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
	settingsWriteMu.Unlock()

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
	expectedModelPath := filepath.Join(mm.modelsDir, "shared", conf.GeomodelONNXLocalName)
	expectedLabelsPath := filepath.Join(mm.modelsDir, "shared", conf.GeomodelLabelsLocalName)

	filesPresent := true
	for _, path := range []string{expectedModelPath, expectedLabelsPath} {
		if _, err := os.Stat(path); err != nil {
			filesPresent = false
			break
		}
	}

	// Decide and write under settingsWriteMu so the decision and the store operate on
	// one consistent snapshot. Reading the config outside the lock would let a
	// concurrent install publish a valid geomodel config between the decision
	// and the store, after which a stale "clear" would wipe it. The filesystem
	// check above is independent of settings, so it stays outside the lock.
	settingsWriteMu.Lock()
	current := conf.GetSettings()
	rf := current.BirdNET.RangeFilter
	action := decideGeomodelOrphanAction(&rf, expectedModelPath, expectedLabelsPath, filesPresent)
	if action == geomodelOrphanNone {
		settingsWriteMu.Unlock()
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
	settingsWriteMu.Unlock()

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
	loaded := false
	for _, catalogID := range installedIDs {
		entry, found := GetCatalogEntry(catalogID)
		if !found || entry.RegistryID == "" {
			continue
		}
		// The permanent BirdNET v2.4 classifier is the primary model, resolved at
		// startup by NewBirdNET, not loaded through the orchestrator's secondary
		// loaders. It has no ModelLoaders entry, so calling LoadModel would only log
		// a spurious "failed to load" warning. Skip it: it is always "installed" but
		// never hot-loaded here.
		if entry.RegistryID == permanentRegistryID {
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
			continue
		}
		loaded = true
	}
	// Signal topology change once if at least one model was loaded (no lock held here).
	if loaded {
		mm.notifyTopologyChanged()
	}
}

// IsInstalled returns true if the model identified by catalogID is installed.
func (mm *ModelManager) IsInstalled(catalogID string) bool {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	_, ok := mm.installed[catalogID]
	return ok
}

// InstalledVariantID returns the installed variant id for catalogID and true, or
// "" and false when the model is not installed. An installed flat (pre-variant)
// entry reports an empty variant id with ok=true, so callers can distinguish
// "installed, single implicit variant" from "not installed".
func (mm *ModelManager) InstalledVariantID(catalogID string) (variantID string, ok bool) {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	im, found := mm.installed[catalogID]
	if !found {
		return "", false
	}
	return im.VariantID, true
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

	// topologyChanged is set when a model is unloaded below. The deferred notify
	// is registered BEFORE the unlock defer so it runs AFTER the lock is released
	// (deferred calls run LIFO), keeping notifyTopologyChanged outside the lock.
	topologyChanged := false
	defer func() {
		if topologyChanged {
			mm.notifyTopologyChanged()
		}
	}()

	mm.mu.Lock()
	defer mm.mu.Unlock()

	im, installed := mm.installed[catalogID]
	if !installed {
		return errors.Newf("model %s is not installed", catalogID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", catalogID).
			Build()
	}

	// Refuse while a download or variant switch is in flight for this model. A
	// concurrent replaceVariant swaps mm.installed and writes files after its own
	// unlock; uninstalling in that window would delete the old files and clear
	// config only for the switch to resurrect the entry as a zombie install.
	if _, downloading := mm.downloading[catalogID]; downloading {
		return errors.Newf("model %s is being downloaded; cannot uninstall", catalogID).
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
			log.Warn("Uninstall refused: model could not be unloaded (still in use)",
				logger.String("catalog_id", catalogID),
				logger.String("registry_id", entry.RegistryID),
				logger.Error(err))
			return errors.Newf("cannot uninstall %s: model still in use", catalogID).
				Component("classifier.model_manager").
				Category(errors.CategorySystem).
				Context("catalog_id", catalogID).
				Context("registry_id", entry.RegistryID).
				Context("unload_error", err.Error()).
				Build()
		}
		// Model unloaded: topology changed. Deferred notify fires after unlock.
		topologyChanged = true
	}

	subdir := filepath.Join(mm.modelsDir, catalogID)

	var deleteErr error

	// Delete the INSTALLED variant's model and data files, not the resolved
	// default, so a non-default install is not orphaned. If the installed variant
	// id is unknown (e.g. dropped from the catalog), its file list is unknown, so
	// fall back to the recorded on-disk model path: that is the actual installed
	// file, whereas the default file list would name a different file and orphan
	// it. Any data files of a dropped variant are unknown and left in place.
	deleteFiles, okVariant := variantFilesByID(&entry, im.VariantID)
	if !okVariant {
		deleteFiles = nil
		if im.ModelPath != "" {
			deleteFiles = []CatalogFile{{LocalName: filepath.Base(im.ModelPath), Role: RoleModel}}
		}
	}

	// Delete model ONNX files and associated data files (calibration, distribution, etc.).
	for _, f := range deleteFiles {
		if f.Role == RoleModel || f.Role == RoleData {
			path := filepath.Join(subdir, f.LocalName)
			err := os.Remove(path)
			if err == nil {
				log.Info("Removed file",
					logger.String("catalog_id", catalogID),
					logger.String("role", f.Role),
					logger.String("path", path))
				continue
			}
			if !os.IsNotExist(err) {
				// Warn, not Error: a leftover file during best-effort uninstall is a
				// recoverable/degraded condition (the model is still de-registered),
				// matching cleanupSharedFiles and avoiding needless Error/Sentry noise.
				log.Warn("Failed to remove file during uninstall",
					logger.String("path", path),
					logger.Error(err))
				if deleteErr == nil {
					deleteErr = err
				}
			}
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

	if deleteErr != nil {
		// Best-effort uninstall: the model is de-registered and config cleared,
		// but some files remain. Log at Warn (not the clean Info) so the record
		// matches the returned error and the actual on-disk state.
		log.Warn("Model uninstalled, but some files could not be removed",
			logger.String("catalog_id", catalogID),
			logger.Error(deleteErr))
		return errors.Newf("model uninstalled, but failed to remove some files: %v", deleteErr).
			Component("classifier.model_manager").
			Category(errors.CategoryFileIO).
			Context("catalog_id", catalogID).
			Build()
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

// Install downloads the selected variant's files for a catalog entry and records
// it as installed. variantID selects the hardware variant to install; an empty
// string installs the entry's default variant, and an unknown variant id is
// rejected before any download starts. The baseURL parameter overrides the
// HuggingFace URL for testing; pass an empty string to use the default
// HuggingFace URL constructed from the entry's repo. Progress is reported via the
// channel if non-nil.
func (mm *ModelManager) Install(ctx context.Context, entry *CatalogEntry, variantID, baseURL string, progress chan<- DownloadState) error {
	// Reject an unknown variant selection before taking any lock or registering a
	// download, so a bad selection cannot leave a lingering in-progress state.
	if _, ok := variantFilesByID(entry, variantID); !ok {
		return errors.Newf("unknown variant %q for model %s", variantID, entry.ID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", entry.ID).
			Context("variant_id", variantID).
			Build()
	}

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

	if err := mm.downloadModelFiles(ctx, entry, variantID, baseURL, progress, true); err != nil {
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
	im, ok := mm.installed[entry.ID]
	if !ok {
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

	// Reinstall repairs the INSTALLED variant, so resolve and validate it BEFORE
	// unloading the model. A stale variant id (e.g. the variant was dropped from
	// the catalog) then fails cleanly here rather than after the unload, which
	// would strand the running model unloaded until a restart.
	reinstallVariantID := im.VariantID
	if _, resolvable := variantFilesByID(entry, reinstallVariantID); !resolvable {
		mm.mu.Unlock()
		return errors.Newf("cannot reinstall %s: installed variant %q is no longer in the catalog", entry.ID, reinstallVariantID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", entry.ID).
			Context("variant_id", reinstallVariantID).
			Build()
	}

	// Unload from orchestrator BEFORE overwriting files to avoid crashes.
	unloaded := false
	if mm.orchestrator != nil && entry.RegistryID != "" && mm.orchestrator.IsModelLoaded(entry.RegistryID) {
		if err := mm.orchestrator.UnloadModel(entry.RegistryID); err != nil {
			mm.mu.Unlock()
			// Reinstall runs asynchronously, so the HTTP caller cannot surface
			// this. Log at the always-on manager logger; the API logger that the
			// handler uses may be disabled, which would otherwise leave a refused
			// reinstall completely silent.
			GetLogger().Warn("Reinstall refused: model could not be unloaded (still in use)",
				logger.String("catalog_id", entry.ID),
				logger.String("registry_id", entry.RegistryID),
				logger.Error(err))
			return errors.Newf("cannot reinstall %s: model still in use", entry.ID).
				Component("classifier.model_manager").
				Category(errors.CategorySystem).
				Context("catalog_id", entry.ID).
				Context("registry_id", entry.RegistryID).
				Context("unload_error", err.Error()).
				Build()
		}
		unloaded = true
	}

	// Record download as in-progress.
	mm.downloading[entry.ID] = &DownloadState{
		CatalogID: entry.ID,
		Status:    StatusDownloading,
	}
	mm.mu.Unlock()

	// Model unloaded above: topology changed. Notify outside the lock.
	if unloaded {
		mm.notifyTopologyChanged()
	}

	if err := mm.downloadModelFiles(ctx, entry, reinstallVariantID, baseURL, progress, false); err != nil {
		// Keep failed state briefly for SSE pollers, then clean up.
		time.AfterFunc(failedStateRetention, func() {
			mm.removeDownloading(entry.ID)
		})
		return err
	}

	return nil
}

// InstallOrReplace installs the selected variant of entry when nothing is
// installed for it, or switches an already-installed model to a different variant
// (download-before-delete). The installed-state check and the download
// registration happen under a single mm.mu acquisition so a concurrent Uninstall
// (which refuses while a download is in progress) cannot race between the check
// and the act. An empty variantID selects the entry's default variant; an unknown
// variant id is rejected. Re-selecting the already-installed variant is a no-op.
// The baseURL parameter overrides the HuggingFace URL for testing.
func (mm *ModelManager) InstallOrReplace(ctx context.Context, entry *CatalogEntry, variantID, baseURL string, progress chan<- DownloadState) error {
	// Reject an unknown variant selection before any lock or state change.
	if _, ok := variantFilesByID(entry, variantID); !ok {
		return errors.Newf("unknown variant %q for model %s", variantID, entry.ID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", entry.ID).
			Context("variant_id", variantID).
			Build()
	}

	// Resolve the effective target variant id (empty = default) for the
	// same-variant comparison and the replace path.
	target := variantID
	if target == "" {
		if v := defaultVariant(entry); v != nil {
			target = v.ID
		}
	}

	mm.mu.Lock()
	if _, downloading := mm.downloading[entry.ID]; downloading {
		mm.mu.Unlock()
		return errors.Newf("model %s is already being downloaded", entry.ID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", entry.ID).
			Build()
	}
	current, installed := mm.installed[entry.ID]

	// Permanent primary model (BirdNET v2.4): always "installed", and its variant is
	// swapped through the dedicated primary-reload path, never the generic
	// orchestrator unload/load. If ScanInstalled has not run yet, treat the current
	// state as the embedded BuiltIn baseline so the swap still has a rollback target.
	if IsPermanentEntry(entry) {
		if !installed {
			current = InstalledModel{CatalogID: entry.ID, Version: entry.Version}
			if b := builtInVariant(entry); b != nil {
				current.VariantID = b.ID
			}
		}
		if current.VariantID == target {
			mm.mu.Unlock()
			return nil
		}
		mm.downloading[entry.ID] = &DownloadState{
			CatalogID: entry.ID,
			Status:    StatusDownloading,
		}
		mm.mu.Unlock()
		return mm.replacePrimaryVariant(ctx, entry, &current, target, baseURL, progress)
	}

	if installed && current.VariantID == target {
		// Idempotent: the requested variant is already the installed one.
		mm.mu.Unlock()
		return nil
	}
	if !installed {
		// Fresh install: delegate to Install, which registers the download and owns
		// its failure cleanup. There is no installed entry for a concurrent Uninstall
		// to race, so releasing the lock before Install re-acquires it is safe.
		mm.mu.Unlock()
		return mm.Install(ctx, entry, variantID, baseURL, progress)
	}
	// Variant switch: register the switch atomically with the installed-state
	// decision, so a concurrent Uninstall (which refuses while downloading) cannot
	// slip in between the check and replaceVariant swapping mm.installed.
	mm.downloading[entry.ID] = &DownloadState{
		CatalogID: entry.ID,
		Status:    StatusDownloading,
	}
	mm.mu.Unlock()

	return mm.replaceVariant(ctx, entry, &current, target, baseURL, progress)
}

// replaceVariant switches an installed model to newVariantID using a
// download-before-delete strategy: the new variant's files are downloaded and
// verified first (the old model keeps running), then the old model is unloaded,
// the install record and config are swapped to the new variant, the new model is
// loaded, and only then are the old variant's superseded files removed. Any
// failure before the swap leaves the old variant installed and loaded, so a
// failed switch never strands the working model. The caller must have registered
// entry.ID in mm.downloading; replaceVariant keeps it registered until the
// superseded files are gone (so a concurrent ScanInstalled treats the switch as
// in-flight) and clears it (or schedules cleanup on failure) before returning.
func (mm *ModelManager) replaceVariant(ctx context.Context, entry *CatalogEntry, old *InstalledModel, newVariantID, baseURL string, progress chan<- DownloadState) error {
	log := GetLogger()

	// 1. Download the NEW variant's files (they coexist with the old on disk
	//    because a family's variants use distinct model LocalNames).
	//    cleanupOnFailure=true so a failed switch leaves no partial new files.
	_, modelPath, labelsPath, embeddingsPath, err := mm.downloadVariantFiles(ctx, entry, newVariantID, baseURL, progress, true)
	if err != nil {
		// downloadVariantFiles already marked the state failed; retain it briefly
		// for SSE pollers, then clear. The old variant is untouched and loaded.
		time.AfterFunc(failedStateRetention, func() {
			mm.removeDownloading(entry.ID)
		})
		return err
	}

	// 2. Unload the old model before activating the new one. The new files are
	//    already on disk, so an unload failure aborts cleanly with the old variant
	//    still installed and loaded.
	if mm.orchestrator != nil && entry.RegistryID != "" && mm.orchestrator.IsModelLoaded(entry.RegistryID) {
		if unloadErr := mm.orchestrator.UnloadModel(entry.RegistryID); unloadErr != nil {
			log.Warn("Variant switch refused: model could not be unloaded (still in use)",
				logger.String("catalog_id", entry.ID),
				logger.String("registry_id", entry.RegistryID),
				logger.Error(unloadErr))
			switchErr := errors.Newf("cannot switch %s to variant %q: model still in use", entry.ID, newVariantID).
				Component("classifier.model_manager").
				Category(errors.CategorySystem).
				Context("catalog_id", entry.ID).
				Context("registry_id", entry.RegistryID).
				Context("unload_error", unloadErr.Error()).
				Build()
			// The new variant's files were downloaded but never activated; remove
			// them (keeping shared companions) so the aborted switch strands no model
			// file. The old variant stays installed and loaded.
			mm.removeSupersededVariantFiles(log, entry, newVariantID, old.VariantID)
			// Report the failure over SSE: a bare removeDownloading would leave the
			// stream to read nil-state + IsInstalled as a false success.
			mm.markFailed(entry.ID, switchErr, progress)
			time.AfterFunc(failedStateRetention, func() {
				mm.removeDownloading(entry.ID)
			})
			return switchErr
		}
		mm.notifyTopologyChanged()
	}

	// 3. Swap the install record to the new variant. entry.ID stays in
	//    mm.downloading (cleared only in step 7) so a concurrent ScanInstalled
	//    treats the switch as in-flight until the superseded files are removed.
	mm.mu.Lock()
	mm.installed[entry.ID] = InstalledModel{
		CatalogID:   entry.ID,
		VariantID:   newVariantID,
		ModelPath:   modelPath,
		LabelsPath:  labelsPath,
		InstalledAt: time.Now(),
		Version:     entry.Version,
	}
	mm.mu.Unlock()

	// 4. Persist the new variant's paths BEFORE loading, so buildPerch /
	//    buildBirdNETV3 (which read settings.<family>.ModelPath first) load the new
	//    file, and a crash/restart before step 6 still resolves the new variant.
	mm.applyConfigForInstall(entry, modelPath, labelsPath, embeddingsPath)

	// 5. Load the new variant (the old one was unloaded in step 2).
	mm.hotLoadAfterInstall(log, entry)

	// hotLoadAfterInstall only logs a load failure. If the new variant did not
	// load, the old model is already unloaded, so completing the switch (deleting
	// the old files and reporting success) would leave the family with no
	// classifier loaded while the API reported success. Roll back to the previous
	// variant instead, extending the download-before-delete guarantee to a LOAD
	// failure. Skipped when there is no orchestrator to verify against.
	if mm.orchestrator != nil && entry.RegistryID != "" && !mm.orchestrator.IsModelLoaded(entry.RegistryID) {
		return mm.rollbackVariantSwitch(log, entry, old, newVariantID, progress)
	}

	// 6. Remove the OLD variant's superseded files (never shared companions).
	mm.removeSupersededVariantFiles(log, entry, old.VariantID, newVariantID)

	// 7. The switch is complete: clear the in-flight marker and report success.
	mm.removeDownloading(entry.ID)
	sendProgress(progress, entry.ID, StatusComplete)

	log.Info("Model variant switched",
		logger.String("catalog_id", entry.ID),
		logger.String("from_variant", old.VariantID),
		logger.String("to_variant", newVariantID),
		logger.String("model_path", modelPath))

	return nil
}

// rollbackVariantSwitch restores the previously-installed variant after the new
// variant was written and activated but failed to load. It re-records the old
// variant, re-persists its paths, reloads it, and removes the new variant's
// now-unused files, so a load failure during a switch leaves the family running
// its previous working variant rather than nothing. It reports the switch as
// failed over the progress stream. The caller must have registered entry.ID in
// mm.downloading; rollbackVariantSwitch schedules its cleanup.
func (mm *ModelManager) rollbackVariantSwitch(log logger.Logger, entry *CatalogEntry, old *InstalledModel, newVariantID string, progress chan<- DownloadState) error {
	log.Warn("New variant failed to load; rolling back to the previous variant",
		logger.String("catalog_id", entry.ID),
		logger.String("failed_variant", newVariantID),
		logger.String("restored_variant", old.VariantID))

	// Restore the install record to the old variant.
	mm.mu.Lock()
	mm.installed[entry.ID] = *old
	mm.mu.Unlock()

	// Re-persist the old variant's paths (step 4 wrote the new ones) and reload it.
	// Companion files are identical across a family's variants, so the old variant's
	// embeddings path (bat only) is derived from its own file list for completeness.
	oldEmbeddings := ""
	if oldFiles, ok := variantFilesByID(entry, old.VariantID); ok {
		for _, f := range oldFiles {
			if f.Role == RoleEmbeddings {
				oldEmbeddings = filepath.Join(mm.modelsDir, "shared", f.LocalName)
				break
			}
		}
	}
	mm.applyConfigForInstall(entry, old.ModelPath, old.LabelsPath, oldEmbeddings)
	mm.hotLoadAfterInstall(log, entry)

	// The new variant is unusable on this host: remove its files (keeping shared
	// companions) so disk state matches the restored record.
	mm.removeSupersededVariantFiles(log, entry, newVariantID, old.VariantID)

	switchErr := errors.Newf("switched %s to variant %q but it failed to load; restored previous variant %q", entry.ID, newVariantID, old.VariantID).
		Component("classifier.model_manager").
		Category(errors.CategoryModelInit).
		Context("catalog_id", entry.ID).
		Context("failed_variant", newVariantID).
		Context("restored_variant", old.VariantID).
		Build()
	mm.markFailed(entry.ID, switchErr, progress)
	time.AfterFunc(failedStateRetention, func() {
		mm.removeDownloading(entry.ID)
	})
	return switchErr
}

// replacePrimaryVariant swaps the permanent primary classifier (BirdNET v2.4)
// between its embedded BuiltIn baseline and a DFT-truncated ONNX build, in place,
// without a pipeline restart. It mirrors replaceVariant's download-before-delete and
// rollback discipline, but the primary cannot be orchestrator-unloaded/loaded, so it
// activates through the dedicated primary-reload path
// (Orchestrator.ReloadPrimaryForVariantSwap). The target's files (none for the
// BuiltIn baseline) are fetched first while the old model keeps running, then
// BirdNET.ModelPath is set (or cleared for the baseline) and the primary is reloaded.
// A reload failure restores the previous variant's config and record; the running
// model was already kept alive by reloadModelInternal's transactional rollback, so a
// failed swap never strands the classifier. The caller must have registered entry.ID
// in mm.downloading; replacePrimaryVariant clears it (or schedules cleanup on
// failure) before returning.
func (mm *ModelManager) replacePrimaryVariant(ctx context.Context, entry *CatalogEntry, old *InstalledModel, newVariantID, baseURL string, progress chan<- DownloadState) error {
	log := GetLogger()

	targetIsBuiltIn := false
	if v := resolveVariant(entry, newVariantID); v != nil {
		targetIsBuiltIn = v.BuiltIn
	}

	// 1. Acquire the new variant's model file. The BuiltIn baseline is embedded, so
	//    there is nothing to download; a DFT build is fetched (the old model keeps
	//    running) and removed on a failed switch.
	newModelPath := ""
	if !targetIsBuiltIn {
		_, modelPath, _, _, err := mm.downloadVariantFiles(ctx, entry, newVariantID, baseURL, progress, true)
		if err != nil {
			// downloadVariantFiles already marked the state failed; retain it briefly
			// for SSE pollers, then clear. The old variant is untouched and running.
			time.AfterFunc(failedStateRetention, func() {
				mm.removeDownloading(entry.ID)
			})
			return err
		}
		newModelPath = modelPath
	}

	// 2. Swap the install record to the new variant. entry.ID stays in
	//    mm.downloading (cleared in step 6) so a concurrent ScanInstalled treats the
	//    switch as in-flight until the superseded files are removed.
	mm.mu.Lock()
	mm.installed[entry.ID] = InstalledModel{
		CatalogID:   entry.ID,
		VariantID:   newVariantID,
		ModelPath:   newModelPath,
		InstalledAt: time.Now(),
		Version:     entry.Version,
	}
	mm.mu.Unlock()

	// 3. Persist BirdNET.ModelPath (set for a DFT build, cleared for the baseline)
	//    BEFORE reloading, so the primary loader resolves the new file and a
	//    crash/restart before step 4 still resolves the new variant.
	mm.applyConfigForPrimarySwap(newModelPath)

	// 4. Activate the new variant by reloading the primary in place. A reload failure
	//    rolls back (the running model was already kept alive transactionally).
	if mm.orchestrator != nil {
		if reloadErr := mm.orchestrator.ReloadPrimaryForVariantSwap(); reloadErr != nil {
			return mm.rollbackPrimaryVariantSwap(log, entry, old, newVariantID, reloadErr, progress)
		}
		mm.notifyTopologyChanged()
	}

	// 5. Remove the OLD variant's superseded files. Safe for builtin ids: the
	//    BuiltIn baseline carries no files, so nothing is targeted when the old
	//    variant was the baseline, and its files are removed when the old variant was
	//    a DFT build superseded by the baseline.
	mm.removeSupersededVariantFiles(log, entry, old.VariantID, newVariantID)

	// 6. The switch is complete: clear the in-flight marker and report success.
	mm.removeDownloading(entry.ID)
	sendProgress(progress, entry.ID, StatusComplete)

	log.Info("Primary model variant switched",
		logger.String("catalog_id", entry.ID),
		logger.String("from_variant", old.VariantID),
		logger.String("to_variant", newVariantID),
		logger.String("model_path", newModelPath))

	return nil
}

// rollbackPrimaryVariantSwap restores the previously-active primary variant after a
// failed reload of the new one. reloadModelInternal already kept the previous model
// serving via its transactional rollback, so this only re-records the old variant,
// re-persists its config, and removes the new variant's now-unused files; it does
// NOT reload again (that would put a working model at risk for no gain). It reports
// the swap as failed over the progress stream. The caller must have registered
// entry.ID in mm.downloading; rollbackPrimaryVariantSwap schedules its cleanup.
func (mm *ModelManager) rollbackPrimaryVariantSwap(log logger.Logger, entry *CatalogEntry, old *InstalledModel, newVariantID string, cause error, progress chan<- DownloadState) error {
	log.Warn("New primary variant failed to reload; rolled back to the previous variant",
		logger.String("catalog_id", entry.ID),
		logger.String("failed_variant", newVariantID),
		logger.String("restored_variant", old.VariantID),
		logger.Error(cause))

	// Restore the install record and re-persist the old variant's config (step 3
	// wrote the new path). The running model is already the old one.
	mm.mu.Lock()
	mm.installed[entry.ID] = *old
	mm.mu.Unlock()
	mm.applyConfigForPrimarySwap(old.ModelPath)

	// The new variant is unusable on this host: remove its downloaded files (none for
	// the BuiltIn baseline) so disk state matches the restored record.
	mm.removeSupersededVariantFiles(log, entry, newVariantID, old.VariantID)

	switchErr := errors.Newf("switched %s to variant %q but it failed to load; restored previous variant %q", entry.ID, newVariantID, old.VariantID).
		Component("classifier.model_manager").
		Category(errors.CategoryModelInit).
		Context("catalog_id", entry.ID).
		Context("failed_variant", newVariantID).
		Context("restored_variant", old.VariantID).
		Context("reload_error", cause.Error()).
		Build()
	mm.markFailed(entry.ID, switchErr, progress)
	time.AfterFunc(failedStateRetention, func() {
		mm.removeDownloading(entry.ID)
	})
	return switchErr
}

// applyConfigForPrimarySwap persists the primary classifier's selected model file
// path for a within-model BirdNET v2.4 variant swap: it sets BirdNET.ModelPath to
// the new DFT-truncated file, or clears it (empty modelPath) to revert to the
// embedded BuiltIn baseline. It never touches BirdNET.LabelPath: the v2.4 label set
// is embedded and identical across variants, so a swap must not disturb a
// user-configured custom label path. Uses clone-mutate-publish + SaveSettings so the
// change survives restarts and is visible to concurrent readers.
func (mm *ModelManager) applyConfigForPrimarySwap(modelPath string) {
	if mm.settings == nil {
		return
	}
	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()

	updated := conf.CloneSettings(conf.GetSettings())
	updated.BirdNET.ModelPath = modelPath
	conf.StoreSettings(updated)
	if err := conf.SaveSettings(); err != nil {
		GetLogger().Warn("Failed to persist settings after primary variant swap",
			logger.Error(err))
	}
}

// removeSupersededVariantFiles deletes the old variant's files that the new
// variant does not also carry, after a variant switch. A file whose LocalName the
// new variant also uses (shared companions keep identical names across a family's
// variants) is kept. Files with a shared role are skipped entirely and left to
// Uninstall/cleanupSharedFiles: their lifecycle spans models, so a variant that
// drops a companion must not globally delete a file another model still needs.
func (mm *ModelManager) removeSupersededVariantFiles(log logger.Logger, entry *CatalogEntry, oldVariantID, newVariantID string) {
	oldFiles, ok := variantFilesByID(entry, oldVariantID)
	if !ok {
		// The old variant is no longer in the catalog: its file list is unknown, so
		// there is nothing safe to target. A leaked model file, if any, is recovered
		// by a later scan's reconciliation rather than guessed at here.
		return
	}
	newFiles, _ := variantFilesByID(entry, newVariantID)
	keep := make(map[string]struct{}, len(newFiles))
	for _, f := range newFiles {
		keep[f.LocalName] = struct{}{}
	}
	subdir := filepath.Join(mm.modelsDir, entry.ID)
	for _, f := range oldFiles {
		if _, retained := keep[f.LocalName]; retained {
			continue
		}
		if isSharedRole(f.Role) {
			continue
		}
		path := filepath.Join(subdir, f.LocalName)
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Warn("Failed to remove superseded variant file after switch",
				logger.String("catalog_id", entry.ID),
				logger.String("path", path),
				logger.Error(rmErr))
		} else if rmErr == nil {
			log.Info("Removed superseded variant file after switch",
				logger.String("catalog_id", entry.ID),
				logger.String("path", path))
		}
	}
}

// preflightDiskSpace reports an error when the filesystem holding the models
// directory cannot fit totalDownloadBytes plus a safety margin, so a large
// download fails fast instead of part-way through. It is a no-op when there is
// nothing to download or the total is not positive (a catalog that does not
// declare usable size_bytes, so there is nothing to check). A free-space probe
// error fails open (returns nil): an inability to measure free space must not
// block an otherwise-valid install.
func (mm *ModelManager) preflightDiskSpace(entry *CatalogEntry, filesToDownload int, totalDownloadBytes int64) error {
	// A non-positive total means the sizes are unknown (or a hand-edited catalog
	// carries a bad value); skip rather than cast a negative total to a huge
	// uint64 below and reject spuriously.
	if filesToDownload == 0 || totalDownloadBytes <= 0 || mm.freeSpaceFn == nil {
		return nil
	}
	free, err := mm.freeSpaceFn(mm.modelsDir)
	if err != nil {
		GetLogger().Debug("Free-space check failed; proceeding with install",
			logger.String("catalog_id", entry.ID),
			logger.Error(err))
		return nil
	}
	needed := totalDownloadBytes + diskSpaceMarginBytes
	if free >= uint64(needed) {
		return nil
	}
	return errors.Newf("insufficient disk space to install %s: need %d bytes (incl. %d margin), have %d",
		entry.ID, needed, diskSpaceMarginBytes, free).
		Component("classifier.model_manager").
		Category(errors.CategoryDiskUsage).
		Context("catalog_id", entry.ID).
		Context("needed_bytes", needed).
		Context("free_bytes", free).
		Build()
}

// downloadVariantFiles downloads and verifies the files for the selected variant
// of entry into the models directory, reporting progress and returning the
// resolved model, labels, and embeddings paths. variantID selects the variant
// (empty = the entry's default). It deliberately does NOT record the install,
// apply config, hot-load, or clear mm.downloading; the caller owns that lifecycle
// so both the install path (downloadModelFiles) and the variant-replace path
// (replaceVariant) can share the download while differing in how they activate
// the result. The caller must have registered the entry in mm.downloading. On
// failure it calls markFailed, and when cleanupOnFailure is true it removes the
// files it downloaded this call (Install); when false, partial progress is kept
// (Reinstall).
func (mm *ModelManager) downloadVariantFiles(ctx context.Context, entry *CatalogEntry, variantID, baseURL string, progress chan<- DownloadState, cleanupOnFailure bool) (files []CatalogFile, modelPath, labelsPath, embeddingsPath string, err error) {
	log := GetLogger()

	// Resolve the files for the requested variant (empty selection = default).
	// An unknown variant should already have been rejected by the caller; treat
	// it as a failure here too, as a backstop, rather than silently installing the
	// default variant's files.
	files, okVariant := variantFilesByID(entry, variantID)
	if !okVariant {
		vErr := errors.Newf("unknown variant %q for model %s", variantID, entry.ID).
			Component("classifier.model_manager").
			Category(errors.CategoryValidation).
			Context("catalog_id", entry.ID).
			Context("variant_id", variantID).
			Build()
		mm.markFailed(entry.ID, vErr, progress)
		return nil, "", "", "", vErr
	}

	// Create model subdirectory only if the entry has non-shared files.
	// Shared-only entries (e.g. geomodels) store all files in models/shared/.
	subdir := filepath.Join(mm.modelsDir, entry.ID)
	if !IsSharedOnly(entry) {
		if mkErr := os.MkdirAll(subdir, 0o755); mkErr != nil {
			mkdirErr := errors.Newf("failed to create model directory %s: %v", subdir, mkErr).
				Component("classifier.model_manager").
				Category(errors.CategoryFileIO).
				Context("catalog_id", entry.ID).
				Context("directory", subdir).
				Build()
			mm.markFailed(entry.ID, mkdirErr, progress)
			return nil, "", "", "", mkdirErr
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
	for _, f := range files {
		destPath := fileDestPath(f)
		if _, statErr := os.Stat(destPath); statErr != nil {
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

	// Disk-space preflight: fail fast if the filesystem cannot hold the files we
	// are about to download (variants run up to 557 MB), rather than filling it
	// part-way through and leaving partial files behind. No model files have been
	// downloaded yet (the subdirectory may already exist), so no cleanup is needed
	// on the reject path.
	if pfErr := mm.preflightDiskSpace(entry, filesToDownload, totalAllBytes); pfErr != nil {
		mm.markFailed(entry.ID, pfErr, progress)
		return nil, "", "", "", pfErr
	}

	// Resolve the endpoint override once per install, for logging and for the
	// per-file failover chain. Skipped when baseURL is set, because that path
	// bypasses repo construction entirely.
	var configured string
	if baseURL == "" {
		configured = mm.configuredHuggingFaceEndpoint()
		// A non-default primary host is worth a log line: it can come from the
		// HF_ENDPOINT environment variable, which the settings UI cannot show,
		// so this is the only record of where the files actually came from. The
		// automatic mirror fallback is not "non-default" and is logged per file
		// only when it actually engages (see downloadModelFile).
		if primary := mm.huggingFaceEndpoint(); primary != conf.DefaultHuggingFaceEndpoint {
			log.Info("Downloading model files from a non-default HuggingFace host",
				logger.String("catalog_id", entry.ID),
				logger.String("endpoint", primary))
		}
	}

	// Download each file.
	var completedBytes int64
	fileIndex := 0
	for _, f := range files {
		destPath := fileDestPath(f)

		// Skip download if file already exists and passes SHA256 validation.
		if _, statErr := os.Stat(destPath); statErr == nil && !needsRedownload[destPath] {
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

		// Per-file HuggingFaceRepo overrides the entry-level repo, allowing
		// companion files (e.g., geomodel) to live in a separate repository.
		repo := entry.HuggingFaceRepo
		if f.HuggingFaceRepo != "" {
			repo = f.HuggingFaceRepo
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

		var dlErr error
		if baseURL != "" {
			// Explicit base URL (test injection): no repo construction, no failover.
			dlErr = mm.downloadFile(ctx, entry.ID, baseURL+"/"+f.RemotePath, destPath, f.SHA256, completedBytes)
		} else {
			dlErr = mm.downloadModelFile(ctx, entry.ID, repo, f.RemotePath, destPath, f.SHA256, completedBytes, configured)
		}
		if dlErr != nil {
			log.Error("Failed to download file",
				logger.String("catalog_id", entry.ID),
				logger.String("repo", repo),
				logger.String("remote_path", f.RemotePath),
				logger.Error(dlErr))
			mm.markFailed(entry.ID, dlErr, progress)
			if cleanupOnFailure {
				cleanup()
			}
			return nil, "", "", "", dlErr
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
	modelPath, labelsPath = resolveSharedPaths(files, modelPath, labelsPath, fileDestPath)

	// Find embeddings path for bat models.
	for _, f := range files {
		if f.Role == RoleEmbeddings {
			embeddingsPath = filepath.Join(mm.modelsDir, "shared", f.LocalName)
			break
		}
	}

	return files, modelPath, labelsPath, embeddingsPath, nil
}

// downloadModelFiles installs a catalog entry's selected variant: it delegates
// the file download and verification to downloadVariantFiles, then records the
// install, applies config, and hot-loads the model. It is the install/reinstall
// entry point; the variant-switch path (replaceVariant) uses downloadVariantFiles
// directly so it can interpose an unload between download and load. The caller
// must have registered the entry in mm.downloading. cleanupOnFailure is passed
// through: true (Install) removes newly downloaded files on failure, false
// (Reinstall) keeps repaired files so partial progress is not lost.
func (mm *ModelManager) downloadModelFiles(ctx context.Context, entry *CatalogEntry, variantID, baseURL string, progress chan<- DownloadState, cleanupOnFailure bool) error {
	log := GetLogger()

	_, modelPath, labelsPath, embeddingsPath, err := mm.downloadVariantFiles(ctx, entry, variantID, baseURL, progress, cleanupOnFailure)
	if err != nil {
		return err
	}

	// Record the installed variant: the selected one, or the default variant when
	// the caller did not specify a selection (empty = default). This keeps the
	// install record in sync with what ScanInstalled later derives from disk.
	recordedVariant := variantID
	if recordedVariant == "" {
		if v := defaultVariant(entry); v != nil {
			recordedVariant = v.ID
		}
	}

	// Record as installed.
	mm.mu.Lock()
	mm.installed[entry.ID] = InstalledModel{
		CatalogID:   entry.ID,
		VariantID:   recordedVariant,
		ModelPath:   modelPath,
		LabelsPath:  labelsPath,
		InstalledAt: time.Now(),
		Version:     entry.Version,
	}
	delete(mm.downloading, entry.ID)
	mm.mu.Unlock()

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
		} else {
			// Model hot-loaded: topology changed (no lock held here).
			mm.notifyTopologyChanged()
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
// (e.g. geomodels) that have no RoleModel or RoleLabels files. It takes the
// resolved variant file list rather than the entry so no default-variant Files
// reference leaks into the download path.
func resolveSharedPaths(files []CatalogFile, modelPath, labelsPath string, destPath func(CatalogFile) string) (resolvedModel, resolvedLabels string) {
	if modelPath != "" {
		return modelPath, labelsPath
	}
	for _, f := range files {
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
// other than settingsWriteMu (acquired internally).
// Uses clone-mutate-publish so the shared settings snapshot is never mutated
// in place. Settings are persisted to disk via conf.SaveSettings so changes
// survive restarts and are visible to concurrent readers through conf.Setting().
func (mm *ModelManager) applyConfigForInstall(entry *CatalogEntry, modelPath, labelsPath, embeddingsPath string) {
	if mm.settings == nil {
		return
	}

	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()

	updated := conf.CloneSettings(conf.GetSettings())

	switch entry.RegistryID {
	case RegistryIDBirdNETV3:
		if modelPath != "" {
			updated.BirdNETV3.ModelPath = modelPath
		}
		if labelsPath != "" {
			updated.BirdNETV3.LabelPath = labelsPath
		}
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

	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()

	updated := conf.CloneSettings(conf.GetSettings())
	retainAlias := false

	switch entry.RegistryID {
	case RegistryIDBirdNETV3:
		updated.BirdNETV3.ModelPath = ""
		updated.BirdNETV3.LabelPath = ""
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

// downloadResponseHeaderTimeout bounds how long a connected host may take to
// send response headers before the download fails over to the next endpoint. It
// guards the slowloris / partial-block tail case where a host completes TCP+TLS
// then stalls: without it, such a host would hold the full 30-minute total
// budget before failover. The response body remains bounded only by the total
// Timeout, which is deliberately generous for large model files.
const downloadResponseHeaderTimeout = 30 * time.Second

// newDownloadTransport clones http.DefaultTransport and sets a
// ResponseHeaderTimeout so a stalled host does not defer mirror failover.
func newDownloadTransport() http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// DefaultTransport is *http.Transport in practice; if that ever changes,
		// keep the default rather than losing connection pooling and proxy support.
		return http.DefaultTransport
	}
	tr := base.Clone()
	tr.ResponseHeaderTimeout = downloadResponseHeaderTimeout
	return tr
}

// downloadTotalTimeout is the overall budget for a single model-file download,
// deliberately generous so a large file on a slow connection can complete.
const downloadTotalTimeout = 30 * time.Minute

// downloadHTTPClient is used for model file downloads with a generous total
// timeout to accommodate large files on slow connections. Its transport adds a
// ResponseHeaderTimeout (see downloadResponseHeaderTimeout) so a host that
// connects then stalls fails over promptly instead of holding the whole budget.
var downloadHTTPClient = &http.Client{
	Timeout:   downloadTotalTimeout,
	Transport: newDownloadTransport(),
}

// endpointAttemptError wraps a download failure with whether it warrants trying
// the next endpoint in the chain. A reachability failure (unreachable host,
// reset, timeout) or a gateway status (502/503/504) is retryable; a definite
// response such as 404, a checksum mismatch, or a local filesystem error is
// not, because failing over would only mask a real error. downloadFile returns
// this for its network failure sites; every other failure is a plain error,
// which shouldFailover treats as non-retryable.
type endpointAttemptError struct {
	retryable bool
	err       error
}

func (e *endpointAttemptError) Error() string { return e.err.Error() }
func (e *endpointAttemptError) Unwrap() error { return e.err }

// shouldFailover reports whether err came from a reachability failure that a
// different endpoint might not have.
func shouldFailover(err error) bool {
	var ae *endpointAttemptError
	return errors.As(err, &ae) && ae.retryable
}

// downloadModelFile downloads one catalog file, trying each endpoint in the
// resolver's ordered chain in turn. It fails over to the next endpoint only on
// a reachability failure (never on a definite HTTP status such as 404), and on
// success records the working endpoint so later files in the same install skip
// a blocked host. The per-file SHA256 in downloadFile makes a host switch safe:
// the file is verified against the manifest checksum whichever host served it.
//
// The endpoint chain is resolved per file, not once per install, so a file that
// established the mirror as sticky lets every subsequent file start there
// instead of re-paying the blocked host's connect timeout.
func (mm *ModelManager) downloadModelFile(ctx context.Context, catalogID, repo, remotePath, destPath, expectedSHA256 string, completedBytes int64, configured string) error {
	endpoints := mm.orderedDownloadEndpoints(configured)
	for i, endpoint := range endpoints {
		url := buildHuggingFaceURL(endpoint, repo, remotePath)
		err := mm.downloadFile(ctx, catalogID, url, destPath, expectedSHA256, completedBytes)
		if err == nil {
			mm.noteWorkingEndpoint(endpoint)
			return nil
		}
		// Surface the error (no failover) on the last endpoint or when the
		// failure is not a reachability problem another host could avoid.
		if i == len(endpoints)-1 || !shouldFailover(err) {
			return err
		}
		GetLogger().Warn("HuggingFace host unreachable, trying next endpoint",
			logger.String("catalog_id", catalogID),
			logger.String("failed_endpoint", endpoint),
			logger.String("next_endpoint", endpoints[i+1]),
			logger.Error(err))
	}
	// orderedDownloadEndpoints always returns at least one endpoint, so the loop
	// returns before here; this only guards a future empty-chain regression.
	return errors.Newf("no HuggingFace endpoint available for %s", repo).
		Component("classifier.model_manager").
		Category(errors.CategoryValidation).
		Build()
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
		// A transport failure (unreachable host, reset, timeout) is retryable on
		// the next endpoint; a cancelled context is not (IsUnreachable handles the
		// distinction).
		return &endpointAttemptError{
			retryable: conf.IsUnreachable(err),
			err: errors.Newf("HTTP request failed for %s: %v", url, err).
				Component("classifier.model_manager").
				Category(errors.CategoryNetwork).
				Context("url", url).
				Build(),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// A gateway status (502/503/504) means the origin is down and a mirror
		// may still serve the file, so it is retryable; any other status (404,
		// 403, 500, ...) means the host answered and failover would mask it.
		return &endpointAttemptError{
			retryable: conf.IsGatewayStatus(resp.StatusCode),
			err: errors.Newf("HTTP %d for %s", resp.StatusCode, url).
				Component("classifier.model_manager").
				Category(errors.CategoryNetwork).
				Context("url", url).
				Context("status", fmt.Sprintf("%d", resp.StatusCode)).
				Build(),
		}
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
			// A connection dropped mid-stream (a common Great-Firewall symptom) is
			// retryable on the next endpoint.
			return &endpointAttemptError{
				retryable: conf.IsUnreachable(readErr),
				err: errors.Newf("read error downloading %s: %v", url, readErr).
					Component("classifier.model_manager").
					Category(errors.CategoryNetwork).
					Context("url", url).
					Build(),
			}
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

// huggingFaceResolveMainPath is the HuggingFace file-resolve path segment for a
// repository's main revision, sitting between the repo id and the file path.
const huggingFaceResolveMainPath = "/resolve/main/"

// buildHuggingFaceURL constructs the download URL for a file in a HuggingFace
// repo. The endpoint is one host from the resolved download chain (see
// orderedDownloadEndpoints / downloadModelFile) and must not end in a slash.
func buildHuggingFaceURL(endpoint, repo, filePath string) string {
	return endpoint + "/" + repo + huggingFaceResolveMainPath + filePath
}

// huggingFaceEndpoint resolves the single primary HuggingFace host from the
// current settings override. Downloads themselves use the failover chain
// (orderedDownloadEndpoints); this is used for the non-default-host log line
// and as the unit-tested seam for settings/HF_ENDPOINT resolution and
// credential redaction. It reads the live settings on every call rather than
// caching, so a mirror configured through the UI takes effect without a restart.
func (mm *ModelManager) huggingFaceEndpoint() string {
	return conf.ResolveHuggingFaceEndpoint(mm.configuredHuggingFaceEndpoint())
}

// SetEndpointResolver injects the HuggingFace endpoint resolver used for mirror
// failover. It is wired once at startup; passing nil disables failover and
// restores single-endpoint downloads.
func (mm *ModelManager) SetEndpointResolver(r EndpointResolver) {
	if r == nil {
		mm.endpointResolver.Store(nil)
		return
	}
	mm.endpointResolver.Store(&r)
}

// configuredHuggingFaceEndpoint returns the raw endpoint override from settings
// (settings field only; the HF_ENDPOINT fallback is applied by the conf
// resolver). It is read live so a settings change hot-reloads on the next
// install.
func (mm *ModelManager) configuredHuggingFaceEndpoint() string {
	if current := conf.CurrentOrFallback(mm.settings); current != nil {
		return current.BirdNET.HuggingFaceEndpoint
	}
	return ""
}

// orderedDownloadEndpoints returns the endpoint chain to try for one file,
// most-preferred first. With a resolver injected this is the failover chain
// (canonical then mirror, sticky endpoint first); without one it is the single
// resolved endpoint, preserving the pre-failover behavior exactly.
func (mm *ModelManager) orderedDownloadEndpoints(configured string) []string {
	if p := mm.endpointResolver.Load(); p != nil {
		return (*p).OrderedEndpoints(configured)
	}
	return []string{conf.ResolveHuggingFaceEndpoint(configured)}
}

// noteWorkingEndpoint records the endpoint that just served a file, so the next
// file in the same install and later installs start from it instead of
// re-probing a blocked host. It is a no-op without a resolver.
func (mm *ModelManager) noteWorkingEndpoint(endpoint string) {
	if p := mm.endpointResolver.Load(); p != nil {
		(*p).NoteWorking(endpoint)
	}
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
