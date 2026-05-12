// orchestrator.go is the primary entry point for model management and inference.
// Manages one or more classifier models with per-model locking and name resolution.
package classifier

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// modelEntry holds a model instance with its own lock for concurrent access.
type modelEntry struct {
	instance ModelInstance
	mu       sync.Mutex // per-model lock; prevents inference on one model from blocking another
}

// Orchestrator manages classifier model instances and provides the primary
// inference API. It replaces direct *BirdNET usage at all call sites.
// Supports multiple models with per-model locking and name resolution.
type Orchestrator struct {
	// Public fields — same layout as BirdNET for drop-in caller migration.
	Settings        *conf.Settings
	ModelInfo       ModelInfo
	TaxonomyMap     TaxonomyMap
	TaxonomyPath    string
	ScientificIndex ScientificNameIndex

	// Name resolution chain. Resolvers are tried in order; first non-empty wins.
	nameResolvers []NameResolver

	// Model management.
	// NOTE: models map is keyed by ModelInfo.ID at construction time. If ReloadModel
	// changes the model ID, the key goes stale. Delete() iterates values so cleanup
	// is unaffected. ReloadModel re-keys the map after reload.
	mu        sync.RWMutex // protects the models map
	models    map[string]*modelEntry
	primary   *BirdNET // direct access to the primary model
	modelsDir string   // base directory for gallery-installed models
}

// NewOrchestrator creates a new Orchestrator with BirdNET as the primary model
// and loads any additional models from configuration.
// This is the primary constructor — callers should use this instead of NewBirdNET.
func NewOrchestrator(settings *conf.Settings) (*Orchestrator, error) {
	// Resolve primary model identity from config
	var primaryInfo *ModelInfo
	if settings.BirdNET.Version != "" {
		info, ok := ResolveBirdNETVersion(settings.BirdNET.Version)
		if ok {
			if settings.BirdNET.ModelPath != "" {
				info.CustomPath = settings.BirdNET.ModelPath
			}
			primaryInfo = &info
		}
	}
	bn, err := NewBirdNET(settings, primaryInfo)
	if err != nil {
		return nil, err
	}

	resolver := NewBirdNETLabelResolver(bn.Labels())

	o := &Orchestrator{
		Settings:        settings,
		ModelInfo:       bn.ModelInfo,
		TaxonomyMap:     bn.TaxonomyMap,
		TaxonomyPath:    bn.TaxonomyPath,
		ScientificIndex: bn.ScientificIndex,
		nameResolvers:   []NameResolver{resolver},
		models: map[string]*modelEntry{
			bn.ModelInfo.ID: {instance: bn},
		},
		primary: bn,
	}

	// Pre-compute thread allocation so model constructors receive their share.
	// BirdNET already uses settings.BirdNET.Threads at construction; additional
	// models get their allocated count passed to their constructor.
	threadAlloc := o.computeThreadAllocation(settings, bn.ModelInfo.ID)

	// Load additional models from configuration
	if err := o.loadAdditionalModels(threadAlloc); err != nil {
		// Clean up all models registered so far (primary + any partially loaded)
		o.Delete()
		return nil, err
	}

	return o, nil
}

// SetModelsDir sets the base directory for gallery-installed models.
// Called by ModelManager after creation so model loaders can resolve
// paths from the installed models directory when config paths are empty.
// Also propagates the directory to the primary BirdNET instance for
// geomodel auto-selection.
func (o *Orchestrator) SetModelsDir(dir string) {
	o.modelsDir = dir
	if o.primary != nil {
		o.primary.SetModelsDir(dir)
	}
}

// resolveInstalledPaths looks up catalog entries for the given registry ID
// and returns the absolute paths for the first installed model found on disk.
// Returns empty strings if no installed model is found.
func (o *Orchestrator) resolveInstalledPaths(registryID string) (modelPath, labelsPath, embeddingsPath string) {
	log := GetLogger()
	if o.modelsDir == "" {
		log.Debug("cannot resolve model paths: models directory not set",
			logger.String("registry_id", registryID))
		return "", "", ""
	}
	for i := range EmbeddedCatalog {
		entry := &EmbeddedCatalog[i]
		if entry.RegistryID != registryID {
			continue
		}
		subdir := filepath.Join(o.modelsDir, entry.ID)
		var mp, lp, ep string
		for _, f := range entry.Files {
			switch f.Role {
			case RoleModel:
				mp = filepath.Join(subdir, f.LocalName)
			case RoleLabels:
				lp = filepath.Join(subdir, f.LocalName)
			case RoleEmbeddings:
				ep = filepath.Join(o.modelsDir, "shared", f.LocalName)
			}
		}
		if mp != "" {
			if _, err := os.Stat(mp); err == nil {
				log.Debug("resolved model paths from gallery",
					logger.String("registry_id", registryID),
					logger.String("model_path", mp))
				return mp, lp, ep
			}
		}
	}
	log.Warn("model in models.enabled but not installed on disk",
		logger.String("registry_id", registryID),
		logger.String("models_dir", o.modelsDir))
	return "", "", ""
}

// Predict runs inference using the primary model.
// Delegates to PredictModel for uniform locking and telemetry.
func (o *Orchestrator) Predict(ctx context.Context, sample [][]float32) ([]datastore.Results, error) {
	o.mu.RLock()
	id := o.ModelInfo.ID
	o.mu.RUnlock()
	return o.PredictModel(ctx, id, sample)
}

// PredictModel runs inference on a specific model identified by modelID.
// It uses a two-level locking protocol: a read lock on the models map to fetch
// the entry (fast), then a per-model lock for inference (slow). The map lock is
// released before acquiring the model lock to prevent deadlocks with ReloadModel.
func (o *Orchestrator) PredictModel(ctx context.Context, modelID string, sample [][]float32) ([]datastore.Results, error) {
	log := GetLogger()

	o.mu.RLock()
	entry, ok := o.models[modelID]
	o.mu.RUnlock()

	if !ok {
		log.Error("PredictModel unknown model",
			logger.String("model_id", modelID))
		return nil, errors.Newf("unknown model: %s", modelID).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Context("model_id", modelID).
			Build()
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.instance == nil {
		return nil, errors.Newf("model %s has been closed", modelID).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Context("model_id", modelID).
			Build()
	}

	chunkLen := 0
	if len(sample) > 0 {
		chunkLen = len(sample[0])
	}
	log.Debug("PredictModel dispatching",
		logger.String("model_id", modelID),
		logger.Int("sample_chunks", len(sample)),
		logger.Int("chunk_len", chunkLen))

	start := time.Now()
	results, err := entry.instance.Predict(ctx, sample)
	duration := time.Since(start)

	if err != nil {
		log.Error("PredictModel inference failed",
			logger.String("model_id", modelID),
			logger.Error(err),
			logger.Duration("duration", duration))
	} else {
		globalInferenceCounters.RecordInvoke(modelID, duration.Microseconds())
		log.Debug("PredictModel complete",
			logger.String("model_id", modelID),
			logger.Int("result_count", len(results)),
			logger.Duration("duration", duration))
	}

	return results, err
}

// ResolveName walks the resolver chain and returns the first non-empty
// common name for the given scientific name and locale.
func (o *Orchestrator) ResolveName(scientificName, locale string) string {
	for _, r := range o.nameResolvers {
		if name := r.Resolve(scientificName, locale); name != "" {
			return name
		}
	}
	return ""
}

// GetProbableSpecies returns species scores from the range filter.
func (o *Orchestrator) GetProbableSpecies(date time.Time, week float32) ([]SpeciesScore, error) {
	return o.primary.GetProbableSpecies(date, week)
}

// GetProbableSpeciesWithSettings filters species using the supplied settings
// snapshot, allowing callers to test arbitrary coordinates and thresholds
// without modifying global state.
func (o *Orchestrator) GetProbableSpeciesWithSettings(date time.Time, week float32, settings *conf.Settings) ([]SpeciesScore, error) {
	return o.primary.GetProbableSpeciesWithSettings(date, week, settings)
}

// GetSpeciesOccurrence returns the occurrence probability for a species at the current time.
func (o *Orchestrator) GetSpeciesOccurrence(species string) float64 {
	return o.primary.GetSpeciesOccurrence(species)
}

// GetSpeciesOccurrenceAtTime returns the occurrence probability for a species at a specific time.
func (o *Orchestrator) GetSpeciesOccurrenceAtTime(species string, detectionTime time.Time) float64 {
	return o.primary.GetSpeciesOccurrenceAtTime(species, detectionTime)
}

// GetSpeciesCode returns the eBird species code for a given label.
func (o *Orchestrator) GetSpeciesCode(label string) (string, bool) {
	return o.primary.GetSpeciesCode(label)
}

// GetSpeciesWithScientificAndCommonName returns the scientific and common name for a label.
func (o *Orchestrator) GetSpeciesWithScientificAndCommonName(label string) (scientific, common string) {
	return o.primary.GetSpeciesWithScientificAndCommonName(label)
}

// EnrichResultWithTaxonomy adds taxonomy information to a detection result.
// If the primary model cannot resolve a common name (e.g., Perch labels
// contain only scientific names), the name resolver chain is consulted
// to map the scientific name to BirdNET's common name.
func (o *Orchestrator) EnrichResultWithTaxonomy(speciesLabel string) (scientific, common, code string) {
	scientific, common, code = o.primary.EnrichResultWithTaxonomy(speciesLabel)

	// Perch v2 labels are scientific-name-only. Try the resolver chain
	// to look up a common name from BirdNET's label database.
	if common == "" && scientific != "" {
		if resolved := o.ResolveName(scientific, ""); resolved != "" {
			common = resolved
		}
	}

	return scientific, common, code
}

// RangeFilterStatus returns introspection data about the primary model's
// active range filter configuration.
func (o *Orchestrator) RangeFilterStatus() RangeFilterStatusInfo {
	return o.primary.RangeFilterStatus()
}

// ReloadRangeFilter reinitializes the range filter on the primary model
// from current settings without a full model reload.
func (o *Orchestrator) ReloadRangeFilter() error {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return nil
	}
	return primary.ReloadRangeFilter()
}

// RunFilterProcess executes the filter process on demand and prints results.
func (o *Orchestrator) RunFilterProcess(dateStr string, week float32) {
	o.primary.RunFilterProcess(dateStr, week)
}

// ReloadModel reloads the primary model and re-syncs shared state.
// Acquires the per-model lock before reload to prevent concurrent inference,
// then the write lock to re-key the models map.
func (o *Orchestrator) ReloadModel() error {
	// Step 1: acquire per-model lock to prevent concurrent inference during reload.
	o.mu.RLock()
	primary := o.primary
	if primary == nil {
		o.mu.RUnlock()
		return errors.Newf("primary model not available for reload").
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Build()
	}
	entry := o.models[primary.ModelInfo.ID]
	o.mu.RUnlock()

	if entry == nil {
		return errors.Newf("primary model entry not found for reload").
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Build()
	}

	entry.mu.Lock()
	if err := primary.ReloadModel(); err != nil {
		entry.mu.Unlock()
		return err
	}
	entry.mu.Unlock()

	// Step 2: write lock to re-sync shared state and re-key if model ID changed.
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.models == nil {
		// Orchestrator has been deleted, abort the reload.
		return errors.Newf("orchestrator has been deleted, cannot reload model").
			Component("classifier.orchestrator").
			Category(errors.CategorySystem).
			Build()
	}

	o.ModelInfo = o.primary.ModelInfo
	o.TaxonomyMap = o.primary.TaxonomyMap
	o.TaxonomyPath = o.primary.TaxonomyPath
	o.ScientificIndex = o.primary.ScientificIndex

	// Re-key the models map in case the model ID changed after reload (Forgejo #270).
	newModels := make(map[string]*modelEntry, len(o.models))
	for _, e := range o.models {
		if e.instance == nil {
			continue // skip entries closed by concurrent Delete
		}
		newModels[e.instance.ModelID()] = e
	}
	o.models = newModels

	return nil
}

// Delete releases all resources held by the Orchestrator and its models.
// After calling Delete, the Orchestrator must not be used.
func (o *Orchestrator) Delete() {
	o.mu.Lock()
	defer o.mu.Unlock()

	for _, entry := range o.models {
		entry.mu.Lock()
		if entry.instance != nil {
			if err := entry.instance.Close(); err != nil {
				GetLogger().Warn("failed to close model instance",
					logger.String("model_id", entry.instance.ModelID()),
					logger.Error(err))
			}
			entry.instance = nil // nil out to signal closed state to PredictModel
		}
		entry.mu.Unlock()
	}
	// Nil out references to fail fast on use-after-delete.
	o.primary = nil
	o.models = nil
}

// IsModelLoaded returns true if a model with the given registry ID is
// currently loaded in the orchestrator.
func (o *Orchestrator) IsModelLoaded(registryID string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.models == nil {
		return false
	}
	_, exists := o.models[registryID]
	return exists
}

// LoadModel dynamically loads a model into the Orchestrator at runtime.
// Called by ModelManager after a successful install. The method delegates
// to the appropriate model loader (loadPerch, loadBat, etc.) based on
// the registry ID. Thread-safe.
//
// The write lock is held for the entire load (including I/O-heavy ONNX
// initialization, typically 1-3 seconds). This briefly blocks inference
// via PredictModel but is acceptable because dynamic loading is rare
// (user-initiated install only) and correctness requires the lock: the
// loaders write directly to o.models, so concurrent map access without
// the lock would be a data race.
func (o *Orchestrator) LoadModel(registryID string) error {
	log := GetLogger()

	// Validate that the registry ID is known before acquiring locks.
	if _, known := ModelRegistry[registryID]; !known {
		return errors.Newf("unknown registry ID: %s", registryID).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Context("registry_id", registryID).
			Build()
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if o.models == nil {
		return errors.Newf("orchestrator has been deleted, cannot load model").
			Component("classifier.orchestrator").
			Category(errors.CategorySystem).
			Build()
	}

	if _, exists := o.models[registryID]; exists {
		return errors.Newf("model %s is already loaded", registryID).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Context("registry_id", registryID).
			Build()
	}

	// Allocate a single thread for the new model. The thread count defaults
	// to 1 because the primary model's thread allocation was computed at
	// startup and should not be reduced by a dynamic load.
	const dynamicThreads = 1

	log.Info("Loading model dynamically",
		logger.String("registry_id", registryID),
		logger.Int("threads", dynamicThreads))

	var err error
	switch registryID {
	case RegistryIDBirdNETV3:
		log.Warn("BirdNET v3.0 loader not yet implemented",
			logger.String("registry_id", registryID))
		return errors.Newf("BirdNET v3.0 loader not yet implemented").
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("registry_id", registryID).
			Build()
	case RegistryIDPerchV2:
		err = o.loadPerch(dynamicThreads)
	case RegistryIDBSG:
		log.Warn("BSG loader not yet implemented",
			logger.String("registry_id", registryID))
		return errors.Newf("BSG loader not yet implemented").
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("registry_id", registryID).
			Build()
	case RegistryIDBat:
		err = o.loadBat(dynamicThreads)
	default:
		return errors.Newf("no loader implemented for model %s", registryID).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("registry_id", registryID).
			Build()
	}

	if err != nil {
		return err
	}

	log.Info("Model loaded dynamically",
		logger.String("registry_id", registryID))

	return nil
}

// UnloadModel removes a model from the Orchestrator and releases its resources.
// Called by ModelManager during uninstall. Refuses to unload the primary model.
// Thread-safe.
func (o *Orchestrator) UnloadModel(registryID string) error {
	log := GetLogger()

	o.mu.Lock()

	if o.models == nil {
		o.mu.Unlock()
		return errors.Newf("orchestrator has been deleted, cannot unload model").
			Component("classifier.orchestrator").
			Category(errors.CategorySystem).
			Build()
	}

	// Refuse to unload the primary model.
	if o.primary != nil && o.primary.ModelInfo.ID == registryID {
		o.mu.Unlock()
		return errors.Newf("cannot unload the primary model %s", registryID).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Context("registry_id", registryID).
			Build()
	}

	entry, exists := o.models[registryID]
	if !exists {
		o.mu.Unlock()
		return errors.Newf("model %s is not loaded", registryID).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Context("registry_id", registryID).
			Build()
	}

	// Remove from map while holding the write lock so no new PredictModel
	// calls can obtain this entry.
	delete(o.models, registryID)
	o.mu.Unlock()

	// Close the model instance outside the map lock. Acquire the per-model
	// lock to wait for any in-flight inference to complete before deleting
	// the counter (prevents re-creation by in-flight RecordInvoke).
	entry.mu.Lock()
	defer entry.mu.Unlock()

	globalInferenceCounters.Delete(registryID)

	if entry.instance != nil {
		modelID := entry.instance.ModelID()
		if err := entry.instance.Close(); err != nil {
			log.Warn("failed to close model instance during unload",
				logger.String("model_id", modelID),
				logger.Error(err))
		}
		entry.instance = nil

		log.Info("Model unloaded",
			logger.String("registry_id", registryID),
			logger.String("model_id", modelID))
	}

	return nil
}

// Debug prints debug messages if debug mode is enabled.
func (o *Orchestrator) Debug(format string, v ...any) {
	o.primary.Debug(format, v...)
}

// ModelInfos returns ModelInfo for all registered models. Thread-safe.
// Used by the pipeline to build ModelTarget lists for buffer fan-out.
func (o *Orchestrator) ModelInfos() []ModelInfo {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if o.models == nil {
		return nil
	}

	infos := make([]ModelInfo, 0, len(o.models))
	for id, entry := range o.models {
		if entry.instance == nil {
			continue
		}
		info, exists := ModelRegistry[id]
		if !exists {
			info = ModelInfo{
				ID:         entry.instance.ModelID(),
				Name:       entry.instance.ModelName(),
				Spec:       entry.instance.Spec(),
				NumSpecies: entry.instance.NumSpecies(),
			}
		}
		infos = append(infos, info)
	}
	return infos
}

// computeThreadAllocation pre-computes thread distribution for all models
// that will be loaded. This runs before loading additional models so
// constructors receive their allocated thread count.
func (o *Orchestrator) computeThreadAllocation(settings *conf.Settings, primaryID string) map[string]int {
	// Collect unique model IDs that will be loaded. Deduplicates
	// case variants like ["perch_v2", "PERCH_V2"] that resolve to the same ID.
	seen := map[string]bool{primaryID: true}
	modelIDs := []string{primaryID}
	for _, configID := range settings.Models.Enabled {
		registryID, known := ResolveConfigModelID(configID)
		if !known || seen[registryID] {
			continue
		}
		seen[registryID] = true
		modelIDs = append(modelIDs, registryID)
	}

	alloc := divideThreads(settings.BirdNET.Threads, modelIDs, primaryID)

	if len(modelIDs) > 1 {
		GetLogger().Info("Thread allocation for multi-model",
			logger.Int("total_threads", settings.BirdNET.Threads),
			logger.Int("model_count", len(modelIDs)))
		for id, threads := range alloc {
			GetLogger().Debug("Model thread allocation",
				logger.String("model_id", id),
				logger.Int("threads", threads))
		}
	}

	return alloc
}

// loadAdditionalModels iterates settings.Models.Enabled and loads any
// non-primary models. Each loaded model is registered in the models map.
// threadAlloc provides the pre-computed thread count for each model.
func (o *Orchestrator) loadAdditionalModels(threadAlloc map[string]int) error {
	log := GetLogger()

	for _, configID := range o.Settings.Models.Enabled {
		registryID, known := ResolveConfigModelID(configID)
		if !known {
			log.Warn("skipping unknown model ID in models.enabled",
				logger.String("model_id", configID))
			continue
		}

		// Skip the primary model (already loaded)
		if _, exists := o.models[registryID]; exists {
			continue
		}

		threads := threadAlloc[registryID]

		var loadErr error
		switch registryID {
		case RegistryIDBirdNETV3:
			log.Warn("BirdNET v3.0 loader not yet implemented, skipping",
				logger.String("registry_id", registryID))
		case RegistryIDPerchV2:
			loadErr = o.loadPerch(threads)
		case RegistryIDBSG:
			log.Warn("BSG loader not yet implemented, skipping",
				logger.String("registry_id", registryID))
		case RegistryIDBat:
			loadErr = o.loadBat(threads)
		default:
			log.Warn("model registered but no loader implemented",
				logger.String("registry_id", registryID))
		}
		if loadErr != nil {
			log.Warn("optional model failed to load, will retry after gallery scan",
				logger.String("registry_id", registryID),
				logger.Error(loadErr))
		}
	}

	return nil
}
