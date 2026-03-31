// orchestrator.go is the primary entry point for model management and inference.
// Manages one or more classifier models with per-model locking and name resolution.
package classifier

import (
	"context"
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
	mu      sync.RWMutex // protects the models map
	models  map[string]*modelEntry
	primary *BirdNET // direct access to the primary model
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

// Predict runs inference using the primary model.
// Relies on BirdNET's internal locking.
func (o *Orchestrator) Predict(ctx context.Context, sample [][]float32) ([]datastore.Results, error) {
	return o.primary.Predict(ctx, sample)
}

// PredictModel runs inference on a specific model identified by modelID.
// It uses a two-level locking protocol: a read lock on the models map to fetch
// the entry (fast), then a per-model lock for inference (slow). The map lock is
// released before acquiring the model lock to prevent deadlocks with ReloadModel.
func (o *Orchestrator) PredictModel(ctx context.Context, modelID string, sample [][]float32) ([]datastore.Results, error) {
	// Step 1: fetch entry under read lock (fast)
	o.mu.RLock()
	entry, ok := o.models[modelID]
	o.mu.RUnlock() // release BEFORE acquiring model lock

	if !ok {
		return nil, errors.Newf("unknown model: %s", modelID).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Context("model_id", modelID).
			Build()
	}

	// Step 2: acquire per-model lock for inference (slow).
	// Guard against Delete having closed the instance between RUnlock and Lock.
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.instance == nil {
		return nil, errors.Newf("model %s has been closed", modelID).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Context("model_id", modelID).
			Build()
	}
	return entry.instance.Predict(ctx, sample)
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

		switch registryID {
		case "Perch_V2":
			//nolint:staticcheck // SA4023: loadPerch always errors in non-onnx build, but returns nil in onnx build
			if err := o.loadPerch(threads); err != nil {
				return err
			}
		default:
			log.Warn("model registered but no loader implemented",
				logger.String("registry_id", registryID))
		}
	}

	return nil
}
