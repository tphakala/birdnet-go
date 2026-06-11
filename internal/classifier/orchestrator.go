// orchestrator.go is the primary entry point for model management and inference.
// Manages one or more classifier models with per-model locking and name resolution.
package classifier

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// modelEntry holds a model instance with its own lock for concurrent access.
type modelEntry struct {
	instance ModelInstance
	mu       sync.Mutex // per-model lock; prevents inference on one model from blocking another
}

// entryRef pairs a registry ID with its model entry for the snapshot-then-iterate
// pattern used when walking o.models outside the o.mu critical section.
type entryRef struct {
	id    string
	entry *modelEntry
}

// Orchestrator manages classifier model instances and provides the primary
// inference API. It replaces direct *BirdNET usage at all call sites.
// Supports multiple models with per-model locking and name resolution.
//
// Lock ordering (acquire in this order to prevent deadlocks):
//  1. mu (RWMutex) - protects models map; released before inference
//  2. inferenceMu (Mutex) - serializes inference across all models
//  3. entry.mu (Mutex) - per-model; guards instance lifecycle
//
// Delete/UnloadModel acquire mu + entry.mu but NOT inferenceMu.
type Orchestrator struct {
	// Public fields, same layout as BirdNET for drop-in caller migration.
	Settings        *conf.Settings // Deprecated: use CurrentSettings() instead.
	settingsAtomic  atomic.Pointer[conf.Settings]
	ModelInfo       ModelInfo
	TaxonomyMap     TaxonomyMap
	TaxonomyPath    string
	ScientificIndex ScientificNameIndex

	// Name resolution chain. Resolvers are tried in order; first non-empty wins.
	nameResolvers []NameResolver

	// openfauna is the authoritative species-name resolver (chain[0]). Held as a
	// typed handle so refresh triggers can Rebuild its sparse index on
	// range-filter/model/locale change. Always also present in nameResolvers.
	openfauna *openfauna.Resolver

	// Model management.
	// NOTE: models map is keyed by ModelInfo.ID at construction time. If ReloadModel
	// changes the model ID, the key goes stale. Delete() iterates values so cleanup
	// is unaffected. ReloadModel re-keys the map after reload.
	mu          sync.RWMutex // protects the models map
	inferenceMu sync.Mutex   // serializes inference across all models
	models      map[string]*modelEntry
	primary     *BirdNET // direct access to the primary model
	modelsDir   string   // base directory for gallery-installed models

	// Nighttime scheduling for bat model. Stored as atomic.Pointer so
	// IsModelActive (called on every monitor tick) reads lock-free.
	scheduler atomic.Pointer[nighttimeScheduler]
}

// CurrentSettings returns the latest settings snapshot published via
// conf.StoreSettings, or the Orchestrator's constructor-provided settings when none
// has been published.
func (o *Orchestrator) CurrentSettings() *conf.Settings {
	if s := o.settingsAtomic.Load(); s != nil {
		return conf.CurrentOrFallback(s)
	}
	return conf.CurrentOrFallback(o.Settings)
}

// currentSettings returns the latest settings snapshot so hot-reloaded
// values (threads, locale, etc.) take effect without restarting.
func (o *Orchestrator) currentSettings() *conf.Settings {
	return o.CurrentSettings()
}

// updateSettings updates the settings pointer safely and atomically.
func (o *Orchestrator) updateSettings(s *conf.Settings) {
	o.Settings = s
	o.settingsAtomic.Store(s)
}

// NewOrchestrator creates a new Orchestrator with BirdNET as the primary model
// and loads any additional models from configuration.
// This is the primary constructor - callers should use this instead of NewBirdNET.
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
	ofResolver := openfauna.NewResolver()

	o := &Orchestrator{
		Settings:        settings,
		ModelInfo:       bn.ModelInfo,
		TaxonomyMap:     bn.TaxonomyMap,
		TaxonomyPath:    bn.TaxonomyPath,
		ScientificIndex: bn.ScientificIndex,
		// OpenFauna first so it overrides label/taxonomy names everywhere
		// ResolveName is consulted (display + inference).
		nameResolvers: []NameResolver{ofResolver, resolver},
		openfauna:     ofResolver,
		models: map[string]*modelEntry{
			bn.ModelInfo.ID: {instance: bn},
		},
		primary: bn,
	}
	o.settingsAtomic.Store(settings)

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
// geomodel auto-selection, and registers the taxonomy resolver if
// taxonomy.csv is available on disk.
func (o *Orchestrator) SetModelsDir(dir string) {
	// Guard the o.modelsDir write and the o.primary read under o.mu: o.modelsDir
	// is read by resolveInstalledPaths (always under o.mu via the model loaders),
	// and o.primary is cleared by Delete() under o.mu.Lock(). Release before the
	// downstream calls, which take their own locks. registerTaxonomyResolver in
	// particular acquires o.mu.RLock() internally, so holding o.mu here would
	// self-deadlock (the RWMutex is not reentrant).
	o.mu.Lock()
	o.modelsDir = dir
	primary := o.primary
	o.mu.Unlock()

	if primary != nil {
		primary.SetModelsDir(dir)
	}
	o.registerTaxonomyResolver(dir)
}

// registerTaxonomyResolver checks for taxonomy.csv in the shared models
// directory and, if present, appends a TaxonomyResolver to the name
// resolver chain. This provides multilingual common name resolution for
// species not covered by BirdNET's label files.
//
// The append and the ResolveName read are guarded by o.mu so a future dynamic
// registration (e.g. a models-directory hot-reload) cannot race with inference
// goroutines calling ResolveName. Registration is idempotent via a
// double-checked guard.
func (o *Orchestrator) registerTaxonomyResolver(modelsDir string) {
	// Read settings via the atomic-safe accessor; o.Settings is reassigned at
	// runtime by ReloadModel (under o.mu), so raw field reads would race.
	settings := o.CurrentSettings()
	if settings == nil {
		return
	}

	// Fast path: a taxonomy resolver is already registered. Read under RLock so
	// this never races with a concurrent ResolveName on the inference path.
	o.mu.RLock()
	exists := o.hasTaxonomyResolverLocked()
	o.mu.RUnlock()
	if exists {
		return
	}

	log := GetLogger()
	taxonomyPath := filepath.Join(modelsDir, "shared", "taxonomy.csv")

	locale := settings.BirdNET.Locale
	// Load the resolver outside the lock; NewTaxonomyResolver does file I/O.
	resolver, err := NewTaxonomyResolver(taxonomyPath, locale)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn("Failed to load taxonomy resolver",
				logger.String("path", taxonomyPath),
				logger.Error(err))
		}
		return
	}

	// Append under the write lock, re-checking in case another caller registered
	// a resolver while this one was loading. Keeps registration idempotent.
	o.mu.Lock()
	if o.hasTaxonomyResolverLocked() {
		o.mu.Unlock()
		return
	}
	o.nameResolvers = append(o.nameResolvers, resolver)
	o.mu.Unlock()

	log.Info("Taxonomy resolver registered",
		logger.String("path", taxonomyPath),
		logger.String("locale", locale),
		logger.Int("species", len(resolver.index)))
}

// hasTaxonomyResolverLocked reports whether a TaxonomyResolver is already
// present in the name-resolver chain. The caller must hold o.mu (read or write).
func (o *Orchestrator) hasTaxonomyResolverLocked() bool {
	for _, r := range o.nameResolvers {
		if _, ok := r.(*TaxonomyResolver); ok {
			return true
		}
	}
	return false
}

// SetSunCalc injects the sun calculator into the orchestrator and starts
// the bat nighttime scheduler if the bat model is loaded. Called during
// pipeline startup after the suncalc instance is available.
func (o *Orchestrator) SetSunCalc(sc *suncalc.SunCalc) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.scheduler.Load() != nil {
		return // already started
	}

	s := newNighttimeScheduler(sc)
	o.scheduler.Store(s)

	// Only start the scheduler if the bat model is actually loaded.
	if _, hasBat := o.models[RegistryIDBat]; hasBat {
		o.startBatScheduler(s)
	}
}

// IsModelActive returns whether a model should currently run inference.
// For the bat model, this checks the nighttime scheduler. For all other
// models, it always returns true.
func (o *Orchestrator) IsModelActive(modelID string) bool {
	if modelID != RegistryIDBat {
		return true
	}
	s := o.scheduler.Load()
	if s == nil {
		return true // no scheduler = no restriction
	}
	return s.isActive()
}

// ModelSpecFor returns the ModelSpec for the given model ID.
// Returns the zero value and false if the model is not loaded.
func (o *Orchestrator) ModelSpecFor(modelID string) (ModelSpec, bool) {
	o.mu.RLock()
	entry, ok := o.models[modelID]
	o.mu.RUnlock()
	if !ok {
		return ModelSpec{}, false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.instance == nil {
		return ModelSpec{}, false
	}
	return entry.instance.Spec(), true
}

// startBatScheduler creates a fresh scheduler (preserving the suncalc
// reference from an existing one if present) and starts it. Handles the
// unload/reload case where the previous scheduler's stopChan is closed.
func (o *Orchestrator) startBatScheduler(s *nighttimeScheduler) {
	nighttimeOnlyFn := func() bool {
		return conf.Setting().Bat.NighttimeOnly
	}
	s.start(nighttimeOnlyFn)
	GetLogger().Info("bat nighttime scheduler started",
		logger.String("operation", "bat_scheduler_start"))
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
// It uses a three-level locking protocol: a read lock on the models map to
// fetch the entry (fast), then inferenceMu to serialize inference across all
// models (only one model runs at a time), then entry.mu for instance lifecycle.
// The map lock is released before acquiring inference/model locks to prevent
// deadlocks with ReloadModel and Delete.
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

	o.inferenceMu.Lock()
	defer o.inferenceMu.Unlock()

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
	// Snapshot the resolver chain under RLock so a concurrent
	// registerTaxonomyResolver append cannot corrupt the slice header.
	// Resolvers are only ever appended (never mutated in place), so iterating
	// the snapshot outside the lock is safe.
	o.mu.RLock()
	resolvers := o.nameResolvers
	o.mu.RUnlock()
	for _, r := range resolvers {
		if name := r.Resolve(scientificName, locale); name != "" {
			return name
		}
	}
	return ""
}

// OpenFaunaResolver returns the orchestrator's authoritative name resolver so
// display surfaces that cannot import the classifier package (the datastore) and
// the api/v2 controller can share the same instance. Never nil after construction.
func (o *Orchestrator) OpenFaunaResolver() *openfauna.Resolver {
	return o.openfauna
}

// RebuildNameResolver rebuilds the OpenFauna sparse index for the given working
// set (range-filtered label strings) at the active BirdNET.Locale. Label strings
// in "Scientific_Common" form are reduced to their scientific name; an empty
// working set falls back to all model labels so a disabled range filter still
// pre-indexes the current model's species. Out-of-working-set (historic) species
// still resolve via the resolver's on-demand Lookup, so the working set is a
// performance optimization, not a correctness boundary.
func (o *Orchestrator) RebuildNameResolver(includedSpecies []string) error {
	if o == nil || o.openfauna == nil {
		return nil
	}
	sciNames := scientificNamesFromLabels(includedSpecies)
	if len(sciNames) == 0 {
		// Snapshot primary under the read lock so a concurrent Delete (which sets
		// o.primary = nil) cannot race the Labels() read.
		o.mu.RLock()
		primary := o.primary
		o.mu.RUnlock()
		if primary != nil {
			sciNames = scientificNamesFromLabels(primary.Labels())
		}
	}
	locale := o.CurrentSettings().BirdNET.Locale
	return o.openfauna.Rebuild(sciNames, locale)
}

// scientificNamesFromLabels extracts the scientific-name portion of each
// "Scientific_Common" label (Perch labels are scientific-only and pass through).
func scientificNamesFromLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		sci, _ := SplitSpeciesName(label)
		if sci != "" {
			out = append(out, sci)
		}
	}
	return out
}

// GetProbableSpecies returns species scores from the range filter.
func (o *Orchestrator) GetProbableSpecies(date time.Time, week float32) ([]SpeciesScore, error) {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return nil, nil
	}
	return primary.GetProbableSpecies(date, week)
}

// GetProbableSpeciesWithSettings filters species using the supplied settings
// snapshot, allowing callers to test arbitrary coordinates and thresholds
// without modifying global state.
func (o *Orchestrator) GetProbableSpeciesWithSettings(date time.Time, week float32, settings *conf.Settings) ([]SpeciesScore, error) {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return nil, nil
	}
	return primary.GetProbableSpeciesWithSettings(date, week, settings)
}

// GetAllProbableSpeciesWithSettings returns species from all active classifiers.
//
// The primary BirdNET model's species are filtered by the range filter using
// the supplied settings. When the primary uses a v3 geomodel, those primary
// scores already contain the full geomodel label set above the configured
// threshold ("ScientificName_CommonName"), exclude-filtered.
//
// Additional models (except bat) emit labels in their own convention (Perch v2
// uses scientific-name-only labels). To decide whether a non-primary species
// should be added, the geomodel's coverage is consulted by scientific name:
//
//   - already represented (present in the primary scores) -> skip (no duplicate)
//   - geomodel-covered but NOT above threshold            -> skip; the range
//     filter excludes it (this is the core fix for the active-species balloon
//     in issue #3250, where exact-string dedup let geomodel-covered Perch
//     species slip back in at score 1.0)
//   - geomodel-unmapped (no scientific-name match)        -> pass through at
//     score 1.0 only when PassUnmappedSpecies is enabled and the species is not
//     excluded
//
// When the primary is not a universal predictor (legacy TFLite range filter
// with no geomodel) there is no coverage set to consult, so every non-primary
// label whose scientific name is not already represented is added at score 1.0
// (exclude honored), preserving prior multi-classifier behavior. Deduplication
// is by scientific name throughout, never exact label, because the geomodel and
// Perch use different label conventions for the same species.
func (o *Orchestrator) GetAllProbableSpeciesWithSettings(date time.Time, week float32, settings *conf.Settings) ([]SpeciesScore, error) {
	// Snapshot primary under read lock to avoid racing with Delete().
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return nil, nil
	}

	// Get the primary's range-filtered scores together with the geomodel's full
	// label set, both from the same range-filter snapshot so a concurrent
	// ReloadRangeFilter cannot desync them. geoLabels is non-nil only on the
	// universal (v3 geomodel) path, where it covers every scientific name the
	// geomodel knows regardless of threshold.
	scores, geoLabels, err := primary.getProbableSpecies(date, week, settings)
	if err != nil {
		return nil, err
	}
	isUniversal := geoLabels != nil

	// Dedup by scientific name (lowercased). seenSci holds species already
	// represented via the primary scores; geoCovered holds every scientific
	// name the geomodel can predict at all.
	seenSci := make(map[string]bool, len(scores))
	for _, s := range scores {
		seenSci[strings.ToLower(detection.ExtractScientificName(s.Label))] = true
	}
	geoCovered := make(map[string]bool, len(geoLabels))
	for _, label := range geoLabels {
		geoCovered[strings.ToLower(detection.ExtractScientificName(label))] = true
	}

	o.mu.RLock()
	// Read o.ModelInfo.ID, the o.mu-guarded copy of the primary's identity, not
	// primary.ModelInfo.ID: the latter is mutated by BirdNET.ReloadModel under
	// bn.mu and would race with a concurrent reload.
	primaryID := o.ModelInfo.ID
	refs := make([]entryRef, 0, len(o.models))
	for id, entry := range o.models {
		if id == primaryID || id == RegistryIDBat {
			continue
		}
		refs = append(refs, entryRef{id: id, entry: entry})
	}
	o.mu.RUnlock()

	// Sort non-primary models by ID so dedup-by-scientific-name is
	// deterministic. When two secondary models emit different labels for the
	// same scientific name, the surviving label must not depend on Go's
	// randomized map iteration order.
	slices.SortFunc(refs, func(a, b entryRef) int { return strings.Compare(a.id, b.id) })

	passUnmapped := settings.BirdNET.RangeFilter.PassUnmappedSpecies
	// Build the exclude matcher once for this pass: it reverse-resolves localized
	// common-name exclude entries through OpenFauna a single time so the per-label
	// matches() below stays off the dataset scan.
	excluder := newExcludeMatcher(settings.Realtime.Species.Exclude, settings.BirdNET.Locale)

	for _, ref := range refs {
		ref.entry.mu.Lock()
		if ref.entry.instance == nil {
			ref.entry.mu.Unlock()
			continue
		}
		labels := ref.entry.instance.Labels()
		ref.entry.mu.Unlock()

		// Grow scores capacity once per model; an upper bound is one new entry
		// per non-primary label.
		if len(labels) > 0 {
			scores = slices.Grow(scores, len(labels))
		}

		for _, label := range labels {
			sci := strings.ToLower(detection.ExtractScientificName(label))
			switch {
			case seenSci[sci]:
				// Already represented via the primary (or an earlier model).
				continue
			case isUniversal && geoCovered[sci]:
				// Geomodel covers this species but it is not in the
				// above-threshold set, so the range filter excludes it. Skip.
				continue
			default:
				// Geomodel-unmapped, or the primary is not universal.
				if !isUniversal {
					// Legacy path: no geomodel to consult. Preserve prior
					// behavior and include the species, deduped by scientific
					// name, without gating on PassUnmappedSpecies.
					if !excluder.matches(label) {
						scores = append(scores, SpeciesScore{Label: label, Score: 1.0})
						seenSci[sci] = true
					}
					continue
				}
				if passUnmapped && !excluder.matches(label) {
					scores = append(scores, SpeciesScore{Label: label, Score: 1.0})
					seenSci[sci] = true
				}
			}
		}
	}

	return scores, nil
}

// GetSpeciesOccurrence returns the occurrence probability for a species at the current time.
func (o *Orchestrator) GetSpeciesOccurrence(species string) float64 {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return 0
	}
	return primary.GetSpeciesOccurrence(species)
}

// GetSpeciesOccurrenceAtTime returns the occurrence probability for a species at a specific time.
func (o *Orchestrator) GetSpeciesOccurrenceAtTime(species string, detectionTime time.Time) float64 {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return 0
	}
	return primary.GetSpeciesOccurrenceAtTime(species, detectionTime)
}

// NumSpecies returns the number of species labels of the primary model.
func (o *Orchestrator) NumSpecies() int {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return 0
	}
	return primary.NumSpecies()
}

// Labels returns a copy of the species labels of the primary model.
func (o *Orchestrator) Labels() []string {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return nil
	}
	return primary.Labels()
}

// unionLabels returns the deduplicated concatenation of the given label sets,
// preserving first-seen order and skipping empty strings.
func unionLabels(sets ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, set := range sets {
		for _, label := range set {
			if label == "" {
				continue
			}
			if _, dup := seen[label]; dup {
				continue
			}
			seen[label] = struct{}{}
			out = append(out, label)
		}
	}
	return out
}

// AllLabels returns the deduplicated union of every loaded model's labels: the
// primary model plus all secondary models, INCLUDING the bat model. Unlike Labels(),
// which returns the primary model's labels only, and unlike
// GetAllProbableSpeciesWithSettings, which range-filters and skips the bat model,
// AllLabels is the unfiltered superset. It is the label source for the reverse
// name-search maps so localized common names of secondary-model species (bats,
// Perch-unique species) are searchable, matching how the forward display path
// already resolves them.
func (o *Orchestrator) AllLabels() []string {
	if o == nil {
		return nil
	}
	o.mu.RLock()
	primary := o.primary
	primaryID := o.ModelInfo.ID
	refs := make([]entryRef, 0, len(o.models))
	for id, entry := range o.models {
		// When a primary is set, skip its map entry: its labels are unioned explicitly
		// below via the *BirdNET pointer, so including it here would snapshot them twice.
		// When primary is nil, do not skip, so the primary model's labels are still
		// covered via the map entry. unionLabels dedupes regardless.
		if primary != nil && id == primaryID {
			continue
		}
		refs = append(refs, entryRef{id: id, entry: entry})
	}
	o.mu.RUnlock()

	// Sort secondary models by ID so the union (and the reverse maps built from it) is
	// stable across rebuilds; Go's randomized map iteration would otherwise pick a
	// different winner for duplicate scientific names, matching GetAllProbableSpeciesWithSettings.
	slices.SortFunc(refs, func(a, b entryRef) int { return strings.Compare(a.id, b.id) })

	// Include the primary explicitly. primary.Labels() is safe without entry.mu because
	// BirdNET.Labels takes the model's own lock internally, matching Labels() and
	// GetAllProbableSpeciesWithSettings; only secondary entries are read under entry.mu.
	sets := make([][]string, 0, len(refs)+1)
	if primary != nil {
		sets = append(sets, primary.Labels())
	}
	for _, ref := range refs {
		ref.entry.mu.Lock()
		var labels []string
		if ref.entry.instance != nil {
			labels = ref.entry.instance.Labels()
		}
		ref.entry.mu.Unlock()
		sets = append(sets, labels)
	}
	return unionLabels(sets...)
}

// GetSpeciesCode returns the eBird species code for a given label.
func (o *Orchestrator) GetSpeciesCode(label string) (string, bool) {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return "", false
	}
	return primary.GetSpeciesCode(label)
}

// GetSpeciesNameFromCode returns the species name for a given eBird species code.
// The second return value reports whether the code was found in the TaxonomyMap
// by calling GetSpeciesNameFromCode(o.TaxonomyMap, code).
func (o *Orchestrator) GetSpeciesNameFromCode(code string) (string, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return GetSpeciesNameFromCode(o.TaxonomyMap, code)
}

// GetSpeciesWithScientificAndCommonName returns the scientific and common name for a label.
// OpenFauna (chain[0]) is authoritative: its localized name overrides the
// primary's label-derived common name whenever the resolver chain has one.
func (o *Orchestrator) GetSpeciesWithScientificAndCommonName(label string) (scientific, common string) {
	// Snapshot primary under read lock to avoid racing with Delete().
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return "", ""
	}
	scientific, common = primary.GetSpeciesWithScientificAndCommonName(label)
	if scientific != "" {
		if resolved := o.ResolveName(scientific, ""); resolved != "" {
			common = resolved
		}
	}
	return scientific, common
}

// EnrichResultWithTaxonomy adds taxonomy information to a detection result.
// OpenFauna (chain[0]) is authoritative: the resolver chain overrides the
// label-derived common name whenever it has a name, even if the primary already
// produced one. This localizes names and fixes scientific-only/bat labels. Only
// the primary's name is kept when the chain returns nothing.
func (o *Orchestrator) EnrichResultWithTaxonomy(speciesLabel string) (scientific, common, code string) {
	// Snapshot primary under read lock to avoid racing with Delete().
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return "", "", ""
	}
	scientific, common, code = primary.EnrichResultWithTaxonomy(speciesLabel)

	if scientific != "" {
		if resolved := o.ResolveName(scientific, ""); resolved != "" {
			common = resolved
		}
	}

	return scientific, common, code
}

// RangeFilterStatus returns introspection data about the range filter,
// including per-classifier geomodel coverage for all active non-bat models.
func (o *Orchestrator) RangeFilterStatus() RangeFilterStatusResponse {
	// Snapshot primary under read lock to avoid racing with Delete().
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return RangeFilterStatusResponse{}
	}

	settings := primary.currentSettings()
	rf := settings.BirdNET.RangeFilter

	geomodel, primaryCoverage, geoLabels, _ := primary.PrimaryRangeFilterCoverage()
	active, fellBack := primary.rangeFilterRuntimeState()

	resp := RangeFilterStatusResponse{
		Geomodel:            geomodel,
		PassUnmappedSpecies: rf.PassUnmappedSpecies,
		Threshold:           rf.Threshold,
		LocationConfigured:  settings.BirdNET.LocationConfigured,
		LastUpdated:         rf.LastUpdated,
		Active:              active,
		FellBack:            fellBack,
		MappedSpecies:       primaryCoverage.WithRangeData,
	}

	// Always include the primary classifier.
	resp.Classifiers = append(resp.Classifiers, primaryCoverage)

	// Collect additional model info under a brief lock, then compute
	// coverage outside the lock to avoid blocking writers.
	type modelTask struct {
		id     string
		name   string
		labels []string
	}

	var refs []entryRef
	o.mu.RLock()
	// Compare against o.ModelInfo.ID, the o.mu-guarded copy of the primary's
	// identity, not primary.ModelInfo.ID which is mutated by BirdNET.ReloadModel
	// under bn.mu and would race with a concurrent reload.
	primaryID := o.ModelInfo.ID
	for id, entry := range o.models {
		if id == primaryID || id == RegistryIDBat {
			continue
		}
		refs = append(refs, entryRef{id: id, entry: entry})
	}
	o.mu.RUnlock()

	var tasks []modelTask
	for _, ref := range refs {
		ref.entry.mu.Lock()
		if ref.entry.instance == nil {
			ref.entry.mu.Unlock()
			continue
		}
		info, exists := ModelRegistry[ref.id]
		name := ref.id
		if exists {
			name = info.Name
		}
		labels := ref.entry.instance.Labels()
		ref.entry.mu.Unlock()
		tasks = append(tasks, modelTask{
			id:     ref.id,
			name:   name,
			labels: labels,
		})
	}

	for _, task := range tasks {
		cov := ClassifierCoverage{
			ID:           task.id,
			Name:         task.name,
			TotalSpecies: len(task.labels),
		}
		if len(geoLabels) > 0 {
			cov.WithRangeData, cov.WithoutRangeData = ComputeGeomodelCoverage(
				task.labels, geoLabels,
			)
		}
		// No geomodel active: leave coverage counters at zero.
		resp.Classifiers = append(resp.Classifiers, cov)
	}

	// Sort classifiers by ID for stable API output (map iteration is random).
	sort.Slice(resp.Classifiers, func(i, j int) bool {
		return resp.Classifiers[i].ID < resp.Classifiers[j].ID
	})

	return resp
}

// ReloadRangeFilter reinitializes the range filter on the primary model
// from current settings without a full model reload, then rebuilds the
// species inclusion list so the processor's detection filter reflects
// the new backend immediately.
func (o *Orchestrator) ReloadRangeFilter() error {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return nil
	}
	if err := primary.ReloadRangeFilter(); err != nil {
		return err
	}
	return BuildRangeFilter(o)
}

// rangeFilterReloadFn is an optional callback invoked after the range filter
// is reloaded. Used by the API layer to invalidate caches.
var (
	rangeFilterReloadMu sync.Mutex
	rangeFilterReloadFn func()
)

// OnRangeFilterReload registers a callback that fires after every successful
// range filter reload. Only one callback is supported; later calls replace earlier ones.
func OnRangeFilterReload(fn func()) {
	rangeFilterReloadMu.Lock()
	rangeFilterReloadFn = fn
	rangeFilterReloadMu.Unlock()
}

func (o *Orchestrator) notifyRangeFilterReload() {
	rangeFilterReloadMu.Lock()
	fn := rangeFilterReloadFn
	rangeFilterReloadMu.Unlock()
	if fn != nil {
		fn()
	}
}

// RunFilterProcess executes the filter process on demand and prints results.
func (o *Orchestrator) RunFilterProcess(dateStr string, week float32) {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return
	}
	primary.RunFilterProcess(dateStr, week)
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
	// Key the lookup by o.ModelInfo.ID (o.mu-guarded), not primary.ModelInfo.ID
	// which BirdNET.ReloadModel mutates under bn.mu. The two are always equal
	// outside an in-progress reload (which holds o.mu.Lock), and the models map
	// is keyed by this same ID, so the lookup is unchanged but race-free.
	entry := o.models[o.ModelInfo.ID]
	o.mu.RUnlock()

	if entry == nil {
		return errors.Newf("primary model entry not found for reload").
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Build()
	}

	var reloadErr error
	func() {
		entry.mu.Lock()
		defer entry.mu.Unlock()
		reloadErr = primary.ReloadModel()
	}()
	if reloadErr != nil {
		return reloadErr
	}

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

	info, taxMap, taxPath, sciIndex := o.primary.ReloadSnapshot()
	o.ModelInfo = info
	o.TaxonomyMap = taxMap
	o.TaxonomyPath = taxPath
	o.ScientificIndex = sciIndex

	// Re-key the models map in case the model ID changed after reload (Forgejo #270).
	newModels := make(map[string]*modelEntry, len(o.models))
	for _, e := range o.models {
		if e.instance == nil {
			continue // skip entries closed by concurrent Delete
		}
		newModels[e.instance.ModelID()] = e
	}
	o.models = newModels

	// Update settings atomically
	o.updateSettings(o.primary.currentSettings())

	return nil
}

// Delete releases all resources held by the Orchestrator and its models.
// After calling Delete, the Orchestrator must not be used.
func (o *Orchestrator) Delete() {
	// Snapshot the models, stop the scheduler, and clear o.primary/o.models under
	// o.mu so the accessors (which snapshot o.primary under o.mu) observe the
	// deleted state immediately and fail fast. Then release o.mu before the
	// per-model Close() calls: Close does native teardown that can be slow, and
	// holding o.mu across it would block every accessor for the duration of
	// teardown. UnloadModel uses the same drop-lock-before-close shape.
	o.mu.Lock()
	models := o.models
	// Swap(nil) both retrieves the scheduler to stop and clears the pointer, so a
	// re-used orchestrator does not see a stale stopped scheduler (SetSunCalc skips
	// creating a new one when o.scheduler is already non-nil).
	if s := o.scheduler.Swap(nil); s != nil {
		s.stop()
	}
	o.primary = nil
	o.models = nil
	o.mu.Unlock()

	for id, entry := range models {
		entry.mu.Lock()
		if entry.instance != nil {
			if err := entry.instance.Close(); err != nil {
				GetLogger().Warn("failed to close model instance",
					logger.String("model_id", entry.instance.ModelID()),
					logger.Error(err))
			}
			entry.instance = nil // nil out to signal closed state to PredictModel
		}
		// Drop the model's global inference counters while still holding entry.mu,
		// mirroring UnloadModel: this prevents an in-flight RecordInvoke from
		// re-creating the entry after deletion, and stops a teardown-then-recreate
		// cycle from leaking counter entries.
		globalInferenceCounters.Delete(id)
		entry.mu.Unlock()
	}

	CloseHeatmapService()
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

// modelLoaders maps registry IDs to their loader functions. Models not in
// this map are recognized but not yet implemented; callers log a warning
// and skip. Adding a new loader only requires one entry here.
var modelLoaders = map[string]func(o *Orchestrator, threads int) error{
	RegistryIDPerchV2: (*Orchestrator).loadPerch,
	RegistryIDBat:     (*Orchestrator).loadBat,
}

// LoadModel dynamically loads a model into the Orchestrator at runtime.
// Called by ModelManager after a successful install. The method delegates
// to the appropriate model loader via modelLoaders based on the registry
// ID. Thread-safe.
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
		log.Debug("model already loaded, skipping",
			logger.String("registry_id", registryID))
		return nil
	}

	loader, implemented := modelLoaders[registryID]
	if !implemented {
		log.Warn("Loader not yet implemented",
			logger.String("registry_id", registryID))
		return errors.Newf("loader not yet implemented for model %s", registryID).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("registry_id", registryID).
			Build()
	}

	// Give the new model the full thread budget. Inference is serialized by
	// inferenceMu so concurrent CPU contention cannot occur.
	dynamicThreads := o.currentSettings().BirdNET.Threads
	if dynamicThreads <= 0 {
		dynamicThreads = runtime.NumCPU()
	}

	log.Info("Loading model dynamically",
		logger.String("registry_id", registryID),
		logger.Int("threads", dynamicThreads))

	if err := loader(o, dynamicThreads); err != nil {
		return err
	}

	log.Info("Model loaded dynamically",
		logger.String("registry_id", registryID))

	// Start nighttime scheduler if bat model was just loaded and suncalc is available.
	// Create a fresh scheduler to handle the unload/reload case where the
	// previous scheduler's stopChan was closed.
	if registryID == RegistryIDBat {
		if old := o.scheduler.Load(); old != nil && old.sunCalc != nil {
			old.stop()
			s := newNighttimeScheduler(old.sunCalc)
			o.scheduler.Store(s)
			o.startBatScheduler(s)
		}
	}

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

	// Refuse to unload the primary model. Compare against o.ModelInfo.ID
	// (o.mu-guarded, held here as a write lock) rather than
	// o.primary.ModelInfo.ID, which BirdNET.ReloadModel mutates under bn.mu.
	if o.primary != nil && o.ModelInfo.ID == registryID {
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
	if registryID == RegistryIDBat {
		if s := o.scheduler.Load(); s != nil {
			s.stop()
		}
	}
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

// lockedMappedRangeFilter snapshots primary under o.mu.RLock, then acquires
// primary.mu and unwraps the range filter to *mappedRangeFilter.
// The caller MUST defer primary.mu.Unlock() after using the returned filter.
// Returns (primary, mappedRangeFilter, error). On error, no locks are held.
func (o *Orchestrator) lockedMappedRangeFilter() (*BirdNET, *mappedRangeFilter, error) {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()

	if primary == nil {
		return nil, nil, errors.Newf("primary model not available").
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Build()
	}

	primary.mu.Lock()

	rf := primary.rangeFilter
	if rf == nil {
		primary.mu.Unlock()
		return nil, nil, errors.Newf("range filter not loaded").
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Build()
	}

	mrf, ok := rf.(*mappedRangeFilter)
	if !ok {
		primary.mu.Unlock()
		return nil, nil, errors.Newf("range filter does not support batch inference").
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Build()
	}

	return primary, mrf, nil
}

// BatchRangeFilterInference runs batch geomodel inference on multiple location/week
// inputs. The caller provides a flat slice of [lat, lon, week] triples and a batch
// size. Returns a flat slice of [batchSize * numGeoSpecies] scores in row-major order.
//
// Acquires primary.mu (not inferenceMu) because the range filter and classifier use
// independent ONNX sessions. Callers that need to process many grid points should
// chunk externally and call this method once per chunk, allowing the detection
// pipeline to interleave between calls.
func (o *Orchestrator) BatchRangeFilterInference(inputs []float32, batchSize int) ([]float32, error) {
	const inputWidth = 3 // [lat, lon, week]
	if batchSize <= 0 {
		return nil, errors.Newf("batchSize must be positive, got %d", batchSize).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Build()
	}
	// Check batchSize against len(inputs)/inputWidth before computing
	// batchSize*inputWidth: a near-math.MaxInt batchSize would otherwise overflow
	// the multiplication to a small positive value that could spuriously equal
	// len(inputs), bypass validation, and push an oversized batch into the ONNX
	// backend (out-of-bounds read). inputWidth is a positive constant, so the
	// division is safe; batchSize > 0 is guaranteed above, so len(inputs)==0 is
	// rejected cleanly.
	if batchSize > len(inputs)/inputWidth || len(inputs) != batchSize*inputWidth {
		return nil, errors.Newf("inputs length %d does not match batchSize %d * %d", len(inputs), batchSize, inputWidth).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Build()
	}

	primary, mrf, err := o.lockedMappedRangeFilter()
	if err != nil {
		return nil, err
	}
	defer primary.mu.Unlock()

	brf, ok := mrf.inner.(inference.BatchRangeFilter)
	if !ok {
		return nil, errors.Newf("underlying range filter does not support batch inference").
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Build()
	}

	return brf.PredictBatch(inputs, batchSize)
}

// GeomodelSpeciesInfo looks up a species label in the geomodel and returns
// its index and the total number of geomodel species, atomically under a
// single lock acquisition. This avoids TOCTOU issues from separate calls.
// Returns (speciesIndex, numGeoSpecies, true) on success, or (0, 0, false)
// if the species is not found or no range filter is loaded.
func (o *Orchestrator) GeomodelSpeciesInfo(label string) (speciesIdx, numGeoSpecies int, found bool) {
	primary, mrf, err := o.lockedMappedRangeFilter()
	if err != nil {
		return 0, 0, false
	}
	defer primary.mu.Unlock()

	idx, ok := mrf.geomodelIndex[label]
	if !ok {
		return 0, 0, false
	}
	return idx, len(mrf.geomodelLabels), true
}

// Debug prints debug messages if debug mode is enabled.
func (o *Orchestrator) Debug(format string, v ...any) {
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return
	}
	primary.Debug(format, v...)
}

// ModelInfos returns ModelInfo for all registered models. Thread-safe.
// Used by the pipeline to build ModelTarget lists for buffer fan-out.
func (o *Orchestrator) ModelInfos() []ModelInfo {
	o.mu.RLock()
	if o.models == nil {
		o.mu.RUnlock()
		return nil
	}
	refs := make([]entryRef, 0, len(o.models))
	for id, entry := range o.models {
		refs = append(refs, entryRef{id: id, entry: entry})
	}
	o.mu.RUnlock()

	infos := make([]ModelInfo, 0, len(refs))
	for _, ref := range refs {
		ref.entry.mu.Lock()
		if ref.entry.instance == nil {
			ref.entry.mu.Unlock()
			continue
		}
		info, exists := ModelRegistry[ref.id]
		if !exists {
			info = ModelInfo{
				ID:         ref.entry.instance.ModelID(),
				Name:       ref.entry.instance.ModelName(),
				Spec:       ref.entry.instance.Spec(),
				NumSpecies: ref.entry.instance.NumSpecies(),
			}
		}
		ref.entry.mu.Unlock()
		infos = append(infos, info)
	}
	return infos
}

// computeThreadAllocation pre-computes thread distribution for all models
// that will be loaded. Inference is serialized by inferenceMu, so each model
// gets the full thread budget (they never run simultaneously).
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

	total := settings.BirdNET.Threads
	if total <= 0 {
		total = runtime.NumCPU()
	}

	alloc := make(map[string]int, len(modelIDs))
	for _, id := range modelIDs {
		alloc[id] = total
	}

	if len(modelIDs) > 1 {
		GetLogger().Info("Thread allocation for multi-model (serialized inference)",
			logger.Int("threads_per_model", total),
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

		// Closure with defer ensures the mutex is released even if a
		// loader panics during model initialization.
		loadErr := func() error {
			o.mu.Lock()
			defer o.mu.Unlock()

			if _, exists := o.models[registryID]; exists {
				return nil
			}

			loader, implemented := modelLoaders[registryID]
			if !implemented {
				log.Warn("Loader not yet implemented, skipping",
					logger.String("registry_id", registryID))
				return nil
			}

			// Hold the lock through the loader call because loaders write
			// directly to o.models (e.g., loadPerch, loadBat).
			return loader(o, threadAlloc[registryID])
		}()
		if loadErr != nil {
			log.Warn("optional model failed to load, will retry after gallery scan",
				logger.String("registry_id", registryID),
				logger.Error(loadErr))
		}
	}

	return nil
}
