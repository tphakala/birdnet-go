// orchestrator.go is the primary entry point for model management and inference.
// Manages one or more classifier models with per-model locking and name resolution.
package classifier

import (
	"context"
	"fmt"
	"iter"
	"maps"
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
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// modelEntry holds a model instance with its own lock for concurrent access.
type modelEntry struct {
	instance ModelInstance
	mu       sync.Mutex // per-model lock; prevents inference on one model from blocking another

	// backend records the inference-backend triplet this entry's instance was last
	// built against. It is only set and read for OV-capable secondary models (those
	// in openvinoCapableSecondaryBuilders); ReloadSecondaryModels compares it
	// per-entry against the current settings to decide whether a backend/device
	// change requires rebuilding this specific model. Guarded by mu, the same lock
	// that guards instance, so the triplet is always published with the instance it
	// describes. Non-secondary entries (e.g. the primary) leave it at the zero value.
	backend secondaryBackendKey

	// generation counts how many times reloadEntry has swapped this entry's instance.
	// Bumped under mu with every successful build-then-swap reload. Phase 3 introduces it
	// as the primitive a later phase consumes to pair a species-index snapshot with the
	// backend generation it was built from. Guarded by mu, alongside instance.
	generation uint64
}

// entryRef pairs a registry ID with its model entry for the snapshot-then-iterate
// pattern used when walking o.models outside the o.mu critical section.
type entryRef struct {
	id    string
	entry *modelEntry
}

// secondaryBackendKey identifies the inference backend / OpenVINO device / CPU
// thread count that an OV-capable secondary model was last built against. Each
// modelEntry stores its own key (see modelEntry.backend); ReloadSecondaryModels
// uses it as a per-entry change-detection gate so an unrelated reload_birdnet
// trigger (locale, thresholds) does not needlessly rebuild a large secondary
// model, while a change that DOES affect the built session (backend, device, or
// thread count) does force a rebuild.
type secondaryBackendKey struct {
	backend  string
	ovDevice string
	ovPath   string
	// threads is BirdNET.Threads (the CPU inference thread budget) the session was
	// built with. It is part of the gate so a runtime thread-count change rebuilds
	// every secondary model, matching the primary's reload. The raw configured
	// value is stored (0 = auto): it never misses a change, and at worst forces one
	// redundant rebuild when toggling 0 and a value that happens to equal NumCPU.
	// Ignored in practice by GPU sessions, which reject INFERENCE_NUM_THREADS, so a
	// GPU-bound secondary simply rebuilds to an equivalent session.
	threads int
}

// Orchestrator manages classifier model instances and provides the primary
// inference API. It replaces direct *BirdNET usage at all call sites.
// Supports multiple models with per-model locking and name resolution.
//
// Lock ordering (acquire in this order to prevent deadlocks):
//  1. reloadMu (Mutex) - serializes a whole reloadEntry (build+swap+notify)
//  2. rebuildMu (Mutex) - serializes the species-index/name-service rebuild
//  3. mu (RWMutex) - protects models map; released before inference
//  4. inferenceMu (Mutex) - serializes inference across all models
//  5. entry.mu (Mutex) - per-model; guards instance lifecycle
//  6. bn.mu (Mutex) - per-*BirdNET; guards its classifier/identity internals
//
// Delete/UnloadModel acquire mu + entry.mu but NOT inferenceMu or reloadMu.
type Orchestrator struct {
	// Public fields, same layout as BirdNET for drop-in caller migration.
	Settings       *conf.Settings // Deprecated: use CurrentSettings() instead.
	settingsAtomic atomic.Pointer[conf.Settings]

	// published becomes true once NewOrchestrator is about to hand the orchestrator
	// to its caller. loadBirdNETV24 reads it to decide whether the v2.4 build may
	// write BirdNET.Labels into the live published settings object (startup load:
	// false, the historical behavior) or must first clone it (a post-publish load:
	// true), so a concurrent reader never observes loadLabels mutating the shared
	// snapshot. Reloads always clone regardless.
	published atomic.Bool

	// taxonomy is the orchestrator-owned eBird taxonomy service, built once in
	// NewOrchestrator and read-only afterwards. It replaces the taxonomy maps the
	// primary model used to own, so a species-code lookup no longer depends on which
	// acoustic model is loaded (model de-privilege epic, Phase 2a).
	taxonomy *taxonomyService

	// Name resolution chain. Resolvers are tried in order; first non-empty wins.
	nameResolvers []NameResolver

	// openfauna is the authoritative species-name resolver (chain[0]). Held as a
	// typed handle so refresh triggers can Rebuild its sparse index on
	// range-filter/model/locale change. Always also present in nameResolvers.
	openfauna *openfauna.Resolver

	// names is the single process-wide species-name index. The orchestrator is its
	// only writer, rebuilding it from the union of every loaded model's labels
	// (AllLabels) on model load, unload, reload, range-filter rebuild and locale
	// change. The datastore and the api/v2 facade hold pointers to this same
	// service; readers use Snapshot() lock-free (model de-privilege epic, Phase 2a).
	names *speciesindex.Service

	// reloadMu serializes every reloadEntry call (settings reload, secondary reload,
	// variant swap) across its whole build -> swap -> notify span, so the settings-monitor
	// reload and the API variant-swap goroutine can never interleave their swap or their
	// post-swap range-filter rebuild (the serialization the in-place reload got from
	// holding bn.mu/entry.mu for its whole duration). It is the OUTERMOST orchestrator
	// lock: o.reloadMu -> o.rebuildMu -> o.mu -> inferenceMu -> entry.mu -> bn.mu.
	// Predictions, LoadModel, UnloadModel and Delete never take it.
	reloadMu sync.Mutex

	// rebuildMu serializes the name-service rebuild (rebuildSpeciesIndex and
	// RebuildNameResolver) so a working set taken by one trigger cannot be published
	// after a newer one's. It sits ABOVE o.mu in the lock order (o.reloadMu -> o.rebuildMu
	// -> o.mu -> entry.mu -> bn.mu): the rebuild calls AllLabels (which takes o.mu.RLock and
	// each entry.mu) while holding it, so it must never be taken with any other
	// orchestrator lock already held.
	rebuildMu sync.Mutex

	// includedSpecies is the last range-filter inclusion list handed to
	// RebuildNameResolver. It is unioned with AllLabels() to form the species-index
	// working set on every rebuild, so the model-topology triggers (load, unload,
	// reload) rebuild the index from the same set the range filter last established
	// rather than dropping the inclusion list; the resolver working set (rebuilt only
	// by RebuildNameResolver) is always a subset of it, so an inclusion-list species
	// can never be in the resolver yet missing from the index. Guarded by rebuildMu.
	includedSpecies []string

	// Model management.
	// NOTE: models map is keyed by the model's registry ID. The v2.4 reload keeps the
	// same ID, so the key stays valid and there is no re-key; Delete() iterates values
	// so cleanup is unaffected regardless.
	mu          sync.RWMutex // protects the models map
	inferenceMu sync.Mutex   // serializes inference across all models
	models      map[string]*modelEntry
	// rangeFilter is the orchestrator-owned range-filter and occurrence service
	// (model de-privilege epic, Phase 2b). It replaces the range filter that used to
	// live on the privileged primary *BirdNET, so occurrence, rarity and the species
	// inclusion list no longer depend on which classifier is primary. Built in
	// NewOrchestrator after the primary loads and closed in Delete. Its own leaf lock
	// keeps range-filter prediction off the classifier's inference lock.
	rangeFilter *rangeFilterService
	// ortAvailable and ovLoadable report whether each inference backend can
	// actually load from the given configured path. Both are nil in production,
	// where inference.CheckORTAvailability and inference.InitOpenVINO are used.
	// Tests set them so every side of the primary recovery's backend gate is
	// reachable regardless of what the host happens to have installed; without an
	// OpenVINO seam the gate's OpenVINO leg is untestable in the default build
	// (openvinoBackendAvailable is a compile-time false there) AND its ORT-only
	// test silently inverts on a machine that does have OpenVINO.
	ortAvailable func(configuredPath string) bool
	ovLoadable   func(libraryPath string) bool
	modelsDir    string // base directory for gallery-installed models

	// Nighttime scheduling for bat model. Stored as atomic.Pointer so
	// IsModelActive (called on every monitor tick) reads lock-free.
	scheduler atomic.Pointer[nighttimeScheduler]

	// Per-model host RSS-delta accounting (see orchestrator_memory.go).
	rssMu            sync.Mutex
	modelRSS         map[string]int64
	runtimeBaseline  int64
	baselineCaptured bool

	// modelLoadFailures tracks how many times LoadModel has failed per registry
	// ID. Uses sync.Map to avoid holding o.mu for reads (see LoadFailures).
	// Values are *atomic.Int64.
	modelLoadFailures sync.Map

	// modelLoadErrors records the last load error string per registry ID
	// (registryID -> string), set alongside modelLoadFailures by recordLoadFailure
	// so modelNotLoadedReason can explain why a model is missing. Lock-free.
	modelLoadErrors sync.Map

	// modelUnloaded is a tombstone set (registryID -> struct{}{}) marking models
	// removed by UnloadModel and not yet reloaded. The unloaded case is the benign
	// cause of the transient not-loaded predict during the topology-reconfigure
	// debounce window (Sentry BIRDNET-GO-2G6 / 1S1): the tombstone lets
	// modelNotLoadedReason report "unloaded, reconfigure in progress" instead of
	// conflating it with a model that never loaded. Lock-free.
	modelUnloaded sync.Map

	// acousticNotice latches the single persistent "no acoustic model" bell notification
	// (N = 0, model de-privilege epic Phase 4); see syncAcousticModelsNotice.
	acousticNotice acousticModelsNotice

	// pendingWarmups queues deferred warm-ups recorded by model loaders while
	// they hold o.mu (write lock). Drained by runPendingWarmups after o.mu is
	// released, so the warm-up inference runs via the serialized inference path
	// instead of stalling PredictModel on o.mu. Appended only
	// under o.mu.Lock(); snapshotted and cleared under o.mu by the drainer.
	pendingWarmups []pendingWarmup

	// pendingPathCorrections queues configuration repairs recorded by model
	// loaders while they hold o.mu, and by NewOrchestrator for the primary before o
	// is published. Drained by runPendingPathCorrections after
	// o.mu is released, because the drainer itself takes o.mu to snapshot and
	// clear this queue, and o.mu is not reentrant; applying one also writes
	// config.yaml (file I/O). (isGalleryManagedPath, reached from the apply step,
	// takes NO orchestrator lock; the drainer's own snapshot-and-clear is what
	// requires the deferral.) Appended only under o.mu.Lock(); snapshotted and
	// cleared under o.mu by the drainer.
	pendingPathCorrections []pendingPathCorrection
}

// pendingWarmup defers a freshly-registered model's warm-up + RSS measurement
// until after o.mu is released. Running the warm-up inference under o.mu would
// block every PredictModel/ModelInfos caller (which wait on o.mu.RLock) for the
// inference duration.
type pendingWarmup struct {
	modelID string // o.models key of the freshly registered instance
	before  uint64 // process RSS sampled before the build (0 = RSS unavailable)
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
	// Build o with an empty models map; the models load below through the same
	// modelLoaders path the secondaries use. The primary's identity is resolved
	// inside NewBirdNET (Tier 2/3/4) rather than pre-seeded here, so it sees the
	// RESOLVED model path and the stale-path recovery can take effect.
	o := &Orchestrator{
		Settings: settings,
		models:   map[string]*modelEntry{},
		modelRSS: make(map[string]int64),
	}
	o.settingsAtomic.Store(settings)

	// Resolve the gallery models directory up front, before the loaders run below.
	// ModelManager also sets it (via SetModelsDir) but is constructed only after
	// this constructor returns, so without this the loaders see an empty
	// o.modelsDir on their first attempt and resolveInstalledPaths cannot find an
	// installed model (GitHub #4201, #4204). Assigned directly rather than through
	// SetModelsDir, whose resolver wiring the constructor overwrites below anyway.
	if modelsDir, ok := settings.ResolveModelsDir(); ok {
		o.modelsDir = modelsDir
	}

	// Fix the process-wide RSS baseline before the first model allocates its arena.
	// The loaders capture their own per-model "before" sample; this call only pins
	// the runtime baseline (Go runtime + app) on its first invocation so the
	// first-loaded model does not visually absorb the shared runtime cost. The
	// returned value is intentionally discarded.
	o.captureRSSBefore()

	// Build the orchestrator-owned taxonomy service before the models. It loads the
	// same embedded eBird taxonomy the primary used to load, via the same
	// LoadTaxonomyData path, so a load failure aborts construction exactly as before.
	taxonomy, err := newTaxonomyService("")
	if err != nil {
		return nil, err
	}
	o.taxonomy = taxonomy

	// Seed the name-resolution chain and the single species-name index with the
	// OpenFauna resolver before any model loads. OpenFauna is chain[0] so it
	// overrides label/taxonomy names everywhere ResolveName is consulted. The v2.4
	// label resolver is appended as chain[1] after the models load, from the v2.4
	// entry's labels (a construction-time snapshot, never refreshed on reload),
	// preserving the previous wiring exactly.
	ofResolver := openfauna.NewResolver()
	o.openfauna = ofResolver
	o.nameResolvers = []NameResolver{ofResolver}
	o.names = speciesindex.New(ofResolver)

	// Build the orchestrator-owned range-filter service with EMPTY state; its
	// backend is built against the v2.4 anchor once the model is loaded, below.
	o.rangeFilter = newRangeFilterService(o.rangeFilterDebug)

	// Pre-compute the per-model thread allocation so each loader receives its share.
	threadAlloc := o.computeThreadAllocation(settings)

	// Load every model named in models.enabled (authoritative since Phase 4), in config
	// order, through modelLoaders. No load failure is fatal: loadEnabledModels records each
	// failure and continues, so construction succeeds even at N=0 (nothing enabled, or every
	// enabled model failed to load). AcousticModelsState reports the degraded state.
	o.loadEnabledModels(threadAlloc)

	// The BirdNET v2.4 label resolver is wired into the chain by loadBirdNETV24 itself
	// (withV24LabelResolverLocked), so it is refreshed on BOTH the construction load and a
	// later retry via LoadModel, and dropped on unload; there is no construction-time
	// snapshot to go stale.

	// Build the range-filter backend from the loaded participant set now that the
	// models are loaded, independent of BirdNET v2.4: a Perch-only or v3.0-only install
	// with the geomodel builds a real backend. reload snapshots the participant view
	// under its own lock. A build failure is non-fatal: the process starts without
	// species filtering and the user can fix it via Settings > Species.
	if err := o.rangeFilter.reload(settings, o.rangeFilterView); err != nil {
		GetLogger().Warn("Range filter initialization failed, starting without species filtering (fix via Settings > Species)",
			logger.Error(err),
			logger.String("range_filter_model", settings.BirdNET.RangeFilter.Model),
			logger.String("model_path", settings.BirdNET.RangeFilter.ModelPath))
	}

	// Log any labels missing from the taxonomy at debug level, reproducing the
	// diagnostics BirdNET used to emit from loadLabels now that the taxonomy is
	// orchestrator-owned. Genuinely v2.4-specific: these diagnostics were only ever
	// emitted for the v2.4 label set.
	if anchorBN, ok := o.birdNETV24Instance(); ok {
		o.logMissingTaxonomyCodes(anchorBN, anchorBN.Labels())
	}

	// Publish the initial species-name snapshot from the union of every loaded
	// model's labels. Kept here (rather than relying on the first BuildRangeFilter)
	// so cmd/benchmark and cmd/rangefilter, which build an orchestrator and never
	// call BuildRangeFilter, still get a populated index.
	o.rebuildSpeciesIndex()

	// Mark the orchestrator published: a subsequent runtime LoadModel of v2.4 now
	// clones settings before building (loadBirdNETV24) instead of mutating the live
	// published snapshot under a concurrent reader.
	o.published.Store(true)

	// Evaluate the persistent "no acoustic model" bell notice now the orchestrator is
	// published. If the notification service is not up yet at construction, the sync latches
	// nothing and ScanInstalled re-syncs it once startup loading completes.
	o.syncAcousticModelsNotice()

	return o, nil
}

// SetModelsDir sets the base directory for gallery-installed models.
// Called by ModelManager after creation so model loaders can resolve
// paths from the installed models directory when config paths are empty,
// and registers the taxonomy resolver if taxonomy.csv is available on disk.
func (o *Orchestrator) SetModelsDir(dir string) {
	// Guard the o.modelsDir write under o.mu: the model loaders read o.modelsDir
	// under o.mu.
	//
	// The write is guarded, but NOT every read is, so this lock alone is not what
	// makes the field safe. resolveInstalledPaths, resolveSiblingSet and
	// isGalleryManagedPath all read o.modelsDir with NO lock held: the first two on
	// the ReloadSecondaryModels path (which releases o.mu before calling the model
	// builders) and on the primary's construction and hot-reload path (via
	// resolvePrimaryModelPath), and the third on the config-correction drain path. Those reads are
	// safe only because this setter runs at most once, before the pipeline starts.
	// See resolveSiblingSet for the full rationale and for what making the models
	// directory dynamic would require.
	//
	// Release before the downstream calls, which take their own locks.
	// registerTaxonomyResolver in particular acquires o.mu.RLock() internally, so
	// holding o.mu here would self-deadlock (the RWMutex is not reentrant).
	o.mu.Lock()
	o.modelsDir = dir
	o.mu.Unlock()

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
	taxonomyPath := filepath.Join(modelsDir, sharedDirName, "taxonomy.csv")

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

// withV24LabelResolverLocked republishes the name-resolver chain with the BirdNET v2.4 label
// resolver replaced (labels != nil) or removed (labels == nil). It builds a FRESH slice
// (copy-on-write) rather than mutating the existing backing array, because ResolveName
// iterates a lock-free snapshot of the chain after releasing its RLock, so mutating an
// element in place would race that read. OpenFauna leads the chain, the v2.4 resolver sits
// directly after it, and every other resolver (the taxonomy resolver) keeps its relative
// order. The caller MUST hold o.mu for writing.
func (o *Orchestrator) withV24LabelResolverLocked(labels []string) {
	fresh := make([]NameResolver, 0, len(o.nameResolvers)+1)
	if o.openfauna != nil {
		fresh = append(fresh, o.openfauna) // OpenFauna always leads the chain
	}
	if labels != nil {
		fresh = append(fresh, NewBirdNETLabelResolver(labels))
	}
	for _, r := range o.nameResolvers {
		if r == o.openfauna {
			continue // re-added above as the lead
		}
		if _, ok := r.(*BirdNETLabelResolver); ok {
			continue // replaced above, or removed when labels == nil
		}
		fresh = append(fresh, r)
	}
	o.nameResolvers = fresh
}

// setV24LabelResolver takes o.mu only for the chain swap. The caller MUST compute labels
// (e.g. bn.Labels(), which takes the model lock) BEFORE calling, never while holding o.mu,
// so the o.mu -> model-lock edge is never taken on the inference hot path where PredictModel
// holds the model lock for a full native inference.
func (o *Orchestrator) setV24LabelResolver(labels []string) {
	o.mu.Lock()
	o.withV24LabelResolverLocked(labels)
	o.mu.Unlock()
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

// scheduleReasonNight is the human-readable reason ModelScheduleStatus returns
// when a model is paused by the nighttime schedule (currently only the bat model).
const scheduleReasonNight = "Night schedule"

// isScheduleGated reports whether registryID runs only inside a schedule (today
// the bat model's nighttime scheduler). It is the single predicate behind
// IsModelActive, ModelScheduleStatus and DefaultTargets, read from the registry
// table so adding a gated model is a one-line registry change. An unknown ID is
// never gated: the zero-value ModelInfo has scheduleGated false.
func isScheduleGated(registryID string) bool {
	return ModelRegistry[registryID].scheduleGated
}

// IsModelActive returns whether a model should currently run inference. A
// schedule-gated model (isScheduleGated, today only the bat model) runs only
// while the nighttime scheduler has it active; every other model is always active.
func (o *Orchestrator) IsModelActive(modelID string) bool {
	if !isScheduleGated(modelID) {
		return true
	}
	s := o.scheduler.Load()
	if s == nil {
		return true // no scheduler = no restriction
	}
	return s.isActive()
}

// ModelScheduleStatus reports whether a model is currently allowed to run and,
// when paused, a human-readable reason. It is the reason-carrying sibling of
// IsModelActive (which stays bool-only because it is on the hot monitor-tick
// path). Only the bat model is schedule-gated today: it returns
// (false, scheduleReasonNight) when the nighttime scheduler has it paused, and
// (true, "") otherwise. Every other model returns (true, ""). The design is
// general so a future gated model can return its own reason.
func (o *Orchestrator) ModelScheduleStatus(modelID string) (active bool, reason string) {
	if !isScheduleGated(modelID) {
		return true, ""
	}
	s := o.scheduler.Load()
	if s == nil || s.isActive() {
		return true, ""
	}
	return false, scheduleReasonNight
}

// instanceFor returns the live ModelInstance for modelID, or nil when the model
// is not loaded or its instance has been torn down. Locking mirrors ModelInfos /
// PredictModel: o.mu.RLock to find the entry, then entry.mu only briefly to
// capture the instance pointer. The pointer is captured under entry.mu and the
// lock released before the caller invokes any instance method, because those
// methods may take their own per-model lock (BirdNET locks bn.mu), and holding
// entry.mu across such a call would stall PredictModel, which needs entry.mu only
// briefly on the inference hot path.
func (o *Orchestrator) instanceFor(modelID string) ModelInstance {
	o.mu.RLock()
	entry, ok := o.models[modelID]
	o.mu.RUnlock()
	if !ok {
		return nil
	}
	entry.mu.Lock()
	inst := entry.instance
	entry.mu.Unlock()
	return inst
}

// GetModelRuntimeInfo returns the live compute device, execution backend, and
// effective runtime precision for the named model, resolved from the live
// instance's RuntimeInfo() in a single instance lookup. Because RuntimeInfo()
// returns the triplet from one consistent snapshot, the status card never
// observes a mixed-generation triplet when a reload completes mid-read.
//
// device is the live execution provider ("CPU"/"GPU"), falling back to
// deviceUnknown when the model is not loaded or its instance has been torn down.
// backend (BackendTFLite/BackendONNX/BackendOpenVINO) and precision
// ("INT8"/"FP16"/"FP32") come from the loaded instance, so an ONNX model executed
// on OpenVINO reports "OpenVINO" and its effective precision; both are "" when the
// model is not loaded, signalling callers to fall back to the static ModelInfo
// file metadata.
func (o *Orchestrator) GetModelRuntimeInfo(modelID string) (device, backend, precision string) {
	inst := o.instanceFor(modelID)
	if inst == nil {
		return deviceUnknown, "", ""
	}
	return inst.RuntimeInfo()
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
	catalog := ActiveCatalog()
	for i := range catalog {
		entry := &catalog[i]
		if entry.RegistryID != registryID {
			continue
		}
		subdir := filepath.Join(o.modelsDir, entry.ID)

		// A variant entry's resolved Files name the DEFAULT variant, which is not
		// the file on disk when a non-default variant is installed. Probe each
		// variant's own files and return the one whose model file exists (a
		// completed switch leaves exactly one), so a non-default install still
		// resolves here when settings carry no path. Flat entries probe entry.Files.
		for _, files := range entryFileSets(entry) {
			var mp, lp, ep string
			for _, f := range files {
				switch f.Role {
				case RoleModel:
					mp = filepath.Join(subdir, f.LocalName)
				case RoleLabels:
					lp = filepath.Join(subdir, f.LocalName)
				case RoleEmbeddings:
					ep = filepath.Join(o.modelsDir, sharedDirName, f.LocalName)
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
	}
	// Worded for BOTH callers. The secondary loaders reach this for a model listed
	// in models.enabled, but resolvePrimaryModelPath also calls it for the primary
	// classifier, which is never in that list, so naming models.enabled here would
	// put a claim in a support dump that is false for the primary.
	log.Warn("no installed model found on disk for this model family",
		logger.String("registry_id", registryID),
		logger.String("models_dir", o.modelsDir))
	return "", "", ""
}

// Predict runs inference using the primary model.
// Delegates to PredictModel for uniform locking and telemetry.
// inferenceFailureLogEvery is the interval, in consecutive failures of one
// model, at which PredictModel repeats its ERROR log after the first failure.
const inferenceFailureLogEvery = 100

// inferenceFailureStreaks tracks consecutive PredictModel failures per model ID
// (value: *atomic.Int64) for the log rate limiting in PredictModel.
//
//nolint:gochecknoglobals // log-flood guard shared with globalInferenceCounters
var inferenceFailureStreaks sync.Map

// inferenceFailureStreak increments and returns the consecutive failure count
// for modelID.
func (o *Orchestrator) inferenceFailureStreak(modelID string) int64 {
	v, _ := inferenceFailureStreaks.LoadOrStore(modelID, new(atomic.Int64))
	return v.(*atomic.Int64).Add(1) //nolint:errcheck // stored type is fixed above
}

// resetInferenceFailureStreak clears the consecutive failure count for modelID
// after a successful inference so the next fault is logged at ERROR again.
func (o *Orchestrator) resetInferenceFailureStreak(modelID string) {
	if v, ok := inferenceFailureStreaks.Load(modelID); ok {
		v.(*atomic.Int64).Store(0) //nolint:errcheck // stored type is fixed above
	}
}

// dropInferenceFailureStreak removes modelID's streak entry when its instance is
// torn down or replaced, so a later instance under the same ID starts fresh (its
// first failure is logged at ERROR) and stale IDs do not accumulate across reloads.
func dropInferenceFailureStreak(modelID string) {
	inferenceFailureStreaks.Delete(modelID)
}

// inferenceFailureLogsAtError reports whether the streak-th consecutive failure
// of one model is logged at ERROR (the first, then every
// inferenceFailureLogEvery-th) rather than DEBUG.
func inferenceFailureLogsAtError(streak int64) bool {
	return streak == 1 || streak%inferenceFailureLogEvery == 0
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
		reason := o.modelNotLoadedReason(modelID)
		log.Error("PredictModel model not loaded",
			logger.String("model_id", modelID),
			logger.String("reason", reason))
		return nil, errors.Newf("%w: %s (%s)", ErrModelNotLoaded, modelID, reason).
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
		globalInferenceCounters.RecordError(modelID)
		// A broken backend fails every window (one per few seconds per source), so
		// after the first failure only every inferenceFailureLogEvery-th repeat is
		// logged at ERROR; the rest go to DEBUG. The metrics counter above still
		// records each one.
		streak := o.inferenceFailureStreak(modelID)
		emit := log.Debug
		if inferenceFailureLogsAtError(streak) {
			emit = log.Error
		}
		emit("PredictModel inference failed",
			logger.String("model_id", modelID),
			logger.Error(err),
			logger.Int64("consecutive_failures", streak),
			logger.Duration("duration", duration))
	} else {
		o.resetInferenceFailureStreak(modelID)
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
	// Snapshot the resolver chain under RLock so a concurrent writer cannot corrupt the
	// slice header. Writers (registerTaxonomyResolver append, withV24LabelResolverLocked
	// replace/remove) only ever append or publish a FRESH slice, never mutate an element in
	// place, so iterating this snapshot outside the lock is safe.
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
	o.rebuildMu.Lock()
	defer o.rebuildMu.Unlock()
	// The working set is the union of every loaded model's labels plus this inclusion
	// list (design 5.4): a superset of the previous seed (the inclusion list, or the
	// primary's labels when it was empty), so every species pre-indexed before stays
	// pre-indexed, and secondary-model species stop falling to the on-demand Lookup
	// path.
	working := unionLabels(o.AllLabels(), includedSpecies)
	locale := o.CurrentSettings().BirdNET.Locale
	// This is the only path that refreshes the OpenFauna resolver working set;
	// openfauna.Rebuild decompresses the embedded dataset, so the cheaper
	// model-topology triggers deliberately skip it and rebuild only the index.
	// Rebuild the resolver first: the index built next reads its localized names.
	// The returned resolver-rebuild error is this method's contract.
	if err := o.openfauna.Rebuild(scientificNamesFromLabels(working), locale); err != nil {
		return err
	}
	// Record the inclusion list only after the resolver has adopted it, so a failed
	// rebuild does not leave o.includedSpecies ahead of the resolver working set (the
	// model-topology triggers union it into the index, so it must not reference
	// species the resolver never took). Defensive copy: the caller may reuse the slice.
	o.includedSpecies = slices.Clone(includedSpecies)
	// Publish the index from the same union so the resolver and index stay in sync.
	if o.names != nil {
		o.names.Rebuild(working, locale)
	}
	return nil
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

// LoadedModelPaths returns, for each currently-loaded model family (keyed by its
// registry ID, which is also the o.models key), the model file that instance is
// actually running. A family PRESENT in the map with an EMPTY value is loaded and
// running its built-in/default source; a family ABSENT from the map has no loaded
// instance. That distinction lets the model-gallery scan tell "loaded, running the
// built-in" from "not loaded", so it consults configuration only for the latter.
// The model set is snapshotted under o.mu, which is then RELEASED before each
// instance is read under its own entry.mu (see the body for why entry.instance
// needs entry.mu). Because o.mu is never held while entry.mu is acquired, a caller
// already holding another lock (ModelManager.mu, taken by ScanInstalled) never
// causes o.mu to nest under it.
func (o *Orchestrator) LoadedModelPaths() map[string]string {
	// entry.instance is guarded by entry.mu, NOT o.mu: ReloadSecondaryModels,
	// UnloadModel and Delete all swap it under entry.mu (see the "PredictModel reads
	// entry.instance under entry.mu" contract at the reload swap), while o.mu guards
	// only the o.models map itself. So snapshot the entries under o.mu, release it,
	// then read each instance under its own entry.mu. This mirrors
	// ReloadSecondaryModels (snapshot refs under o.mu, release, then per-entry
	// entry.mu), and crucially never holds o.mu while acquiring entry.mu, so it adds
	// no new lock-ordering edge.
	type entryRef struct {
		id    string
		entry *modelEntry
	}
	o.mu.RLock()
	refs := make([]entryRef, 0, len(o.models))
	for id, entry := range o.models {
		if entry != nil {
			refs = append(refs, entryRef{id: id, entry: entry})
		}
	}
	o.mu.RUnlock()

	out := make(map[string]string, len(refs))
	for _, r := range refs {
		// Capture the instance under entry.mu, then call the lock-free
		// ResolvedModelPath() on the captured value after releasing the lock: the
		// resolved path is fixed at construction (secondaries) or published lock-free
		// (the primary) and is not touched by Close(), so reading it off-lock on a
		// captured instance is safe even if the entry is torn down concurrently.
		r.entry.mu.Lock()
		inst := r.entry.instance
		r.entry.mu.Unlock()
		if inst != nil {
			out[r.id] = inst.ResolvedModelPath()
		}
	}
	return out
}

// rangeFilterDebug gates a debug log line on the currently published settings,
// mirroring BirdNET.Debug so range-filter log lines relocated into the service stay
// byte-identical. Passed to the service as its debug hook.
func (o *Orchestrator) rangeFilterDebug(format string, v ...any) {
	if s := o.CurrentSettings(); s != nil && s.BirdNET.Debug {
		GetLogger().Debug(fmt.Sprintf(format, v...))
	}
}

// rangeFilterReady returns the orchestrator's range-filter service and whether it is
// present. Readiness is keyed on the SERVICE existing (created once in NewOrchestrator,
// never reassigned), NOT on BirdNET v2.4 being loaded, so it takes no lock. The two
// genuinely v2.4-specific consumers call birdNETV24Instance instead.
func (o *Orchestrator) rangeFilterReady() (rfs *rangeFilterService, ok bool) {
	// Keyed on the range-filter SERVICE being present, not on BirdNET v2.4 being
	// loaded. The service is created once in NewOrchestrator and never reassigned, so
	// the read needs no lock; whether a backend is actually loaded (and thus whether
	// filtering is active) is decided per call inside the service. Decoupling readiness
	// from v2.4 is what lets a Perch-only or v3.0-only install with the geomodel filter,
	// and lets an N=0 runtime fail open over an empty label set instead of dropping
	// every detection.
	rfs = o.rangeFilter
	return rfs, rfs != nil
}

// birdNETV24Instance returns the loaded BirdNET v2.4 instance, if any. It serves the
// two genuinely v2.4-specific consumers that survive the range-filter decoupling: the
// NewBirdNETLabelResolver chain and logMissingTaxonomyCodes. Its name states the v2.4
// privilege that range-filter readiness must no longer carry. Lock discipline mirrors
// the former rangeFilterAnchor: snapshot the entry under o.mu, release, then take
// entry.mu only to read the instance pointer.
func (o *Orchestrator) birdNETV24Instance() (*BirdNET, bool) {
	o.mu.RLock()
	entry := o.models[RegistryIDBirdNETV24]
	o.mu.RUnlock()
	if entry == nil {
		return nil, false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	bn, isBirdNET := entry.instance.(*BirdNET)
	if !isBirdNET {
		return nil, false
	}
	return bn, true
}

// rangeFilterView snapshots every loaded classifier that participates in range
// filtering, so the range-filter backend can be built independent of BirdNET v2.4.
// It replaces rangeFilterAnchor, whose single-classifier view tied the whole range
// filter to v2.4 being loaded.
//
// Lock discipline (mandatory): entries are snapshotted under o.mu (via
// orderedEntryRefs), o.mu is released, then per entry entry.mu is taken ONLY to read
// the instance pointer and released BEFORE calling Labels(). Holding entry.mu across
// Labels() is a lock inversion (o.mu -> inferenceMu -> entry.mu -> bn.mu) that would
// stall PredictModel on the inference hot path. This mirrors AllLabels exactly.
func (o *Orchestrator) rangeFilterView() rangeFilterView {
	o.mu.RLock()
	modelsDir := o.modelsDir
	o.mu.RUnlock()

	// orderedEntryRefs returns the v2.4 instance first (as *BirdNET) and every other
	// entry byte-sorted by ID, the deterministic ordering the union relies on.
	primary, refs := o.orderedEntryRefs()

	view := rangeFilterView{modelsDir: modelsDir}

	// BirdNET v2.4 leads when loaded and participates. primary.Labels() is safe
	// without entry.mu because BirdNET.Labels takes the model's own lock internally
	// (the AllLabels contract).
	if primary != nil && ParticipatesInRangeFilter(RegistryIDBirdNETV24) {
		labels := primary.Labels()
		view.participants = append(view.participants, participantLabels{id: RegistryIDBirdNETV24, labels: labels})
		view.v24Labels = labels
	}

	for _, ref := range refs {
		if !ParticipatesInRangeFilter(ref.id) {
			continue
		}
		// Capture the instance under entry.mu, release, THEN call Labels(): see the
		// lock-discipline note above.
		ref.entry.mu.Lock()
		instance := ref.entry.instance
		ref.entry.mu.Unlock()
		if instance == nil {
			continue
		}
		view.participants = append(view.participants, participantLabels{id: ref.id, labels: instance.Labels()})
		if rangeFilterCompatFor(ref.id) == rangeFilterCompatGeomodel {
			view.wantsGeomodel = true
		}
	}

	return view
}

// GetProbableSpecies returns species scores from the range filter.
func (o *Orchestrator) GetProbableSpecies(date time.Time, week float32) ([]SpeciesScore, error) {
	rfs, ok := o.rangeFilterReady()
	if !ok || rfs == nil {
		return nil, nil
	}
	scores, _, _, err := rfs.probableSpecies(date, week, o.CurrentSettings())
	return scores, err
}

// GetProbableSpeciesWithSettings filters species using the supplied settings
// snapshot, allowing callers to test arbitrary coordinates and thresholds
// without modifying global state.
func (o *Orchestrator) GetProbableSpeciesWithSettings(date time.Time, week float32, settings *conf.Settings) ([]SpeciesScore, error) {
	rfs, ok := o.rangeFilterReady()
	if !ok || rfs == nil {
		return nil, nil
	}
	scores, _, _, err := rfs.probableSpecies(date, week, settings)
	return scores, err
}

// GetAllProbableSpeciesWithSettings returns species from all active classifiers.
//
// The primary BirdNET model's species are filtered by the range filter using
// the supplied settings. When the primary uses a v3 geomodel, those primary
// scores already contain the full geomodel label set above the configured
// threshold ("ScientificName_CommonName"), exclude-filtered.
//
// Additional non-bat models emit labels in their own convention (Perch v2
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
// (exclude honored), preserving prior multi-classifier behavior. Non-primary
// additions are deduped against the primary by scientific name (never exact
// label), because the geomodel and Perch use different label conventions for
// the same species.
//
// This does NOT collapse near-duplicates that already exist within the primary
// scores: a force-included species can appear both at its range-filter score and
// as the override's score-1.0 entry (the two carry different label strings), and
// two taxonomic synonyms for one taxon (e.g. "Cnephaeus nilssonii" vs the older
// "Eptesicus nilssonii") are deliberately kept as distinct scientific names so
// both pass the scientific-name inclusion gate (conf.Settings.IsSpeciesIncluded)
// during audio processing. Those near-duplicates are collapsed for the user only
// at the display boundary (dedupeSpeciesForDisplay in internal/api/v2/range/range.go),
// keyed on the resolved common name, so the functional inclusion set stays intact.
//
// The bat model is handled separately from the loop above: it has no geomodel,
// so its species can never be range-filtered. They are all included as
// always-active (score 1.0), deduped by scientific name and exclude-honored, so
// a BattyBirdNET setup sees its bat species in the active/probable list instead
// of a bird-only view.
func (o *Orchestrator) GetAllProbableSpeciesWithSettings(date time.Time, week float32, settings *conf.Settings) ([]SpeciesScore, error) {
	// Bind the range-filter service; it is present whenever the orchestrator is live, so
	// this is NOT gated on BirdNET v2.4. A non-v2.4 install proceeds through the
	// participant walk and bat handling below instead of early-returning nil.
	rfs, ok := o.rangeFilterReady()
	if !ok || rfs == nil {
		return nil, nil
	}

	// Get the range-filtered scores together with the geomodel's full label set,
	// both from the same range-filter snapshot so a concurrent ReloadRangeFilter
	// cannot desync them. geoLabels is non-nil only on the universal (v3 geomodel)
	// path, where it covers every scientific name the geomodel knows regardless of
	// threshold.
	scores, geo, _, err := rfs.probableSpecies(date, week, settings)
	if err != nil {
		return nil, err
	}
	// Dedup by canonical scientific name: seenSci holds every species already represented
	// via the range-filter scores.
	seenSci := make(map[string]bool, len(scores))
	for _, s := range scores {
		seenSci[canonicalSpeciesKey(s.Label)] = true
	}

	// Add participants outside the loaded backend's mapping space (non-v2.4 classifiers
	// when v2.4 is loaded) via the SAME shared helper BuildRangeFilter uses, so the
	// displayed set and the inclusion (gate) set cannot disagree (Phase 4 PR B2). geo is
	// the geomodel vocabulary on the universal path and nil otherwise, where the helper
	// fails the uncovered participants open wholesale. It reads the built-over participant
	// snapshot, so no o.mu/entry.mu walk is needed here.
	excluder := newExcludeMatcher(settings.Realtime.Species.Exclude, settings.BirdNET.Locale)
	scores = append(scores, rfs.uncoveredParticipantSpecies(settings, geo, excluder, seenSci)...)

	// The bat model is intentionally skipped by the range-filter loop above: it
	// has no geomodel, so its species can never be location-filtered. Include
	// them all here as always-active (score 1.0), deduped by scientific name and
	// honoring the exclude list, so multi-model (BattyBirdNET) setups see their
	// bat species in the active/probable list instead of a bird-only view.
	o.mu.RLock()
	batEntry, hasBat := o.models[RegistryIDBat]
	o.mu.RUnlock()
	if hasBat {
		batEntry.mu.Lock()
		var batLabels []string
		if batEntry.instance != nil {
			batLabels = batEntry.instance.Labels()
		}
		batEntry.mu.Unlock()
		if len(batLabels) > 0 {
			scores = slices.Grow(scores, len(batLabels))
		}
		for _, label := range batLabels {
			sci := canonicalSpeciesKey(label)
			if seenSci[sci] || excluder.matches(label) {
				continue
			}
			scores = append(scores, SpeciesScore{Label: label, Score: 1.0})
			seenSci[sci] = true
		}
	}

	// The range-filtered scores arrive pre-sorted descending, but the
	// secondary-model and bat species above are appended after that sort with a
	// flat always-active score of 1.0. Re-sort the merged slice so the full set
	// is ordered by score descending, matching the range-filter path's contract:
	// otherwise always-active species (the maximum 1.0) would trail behind
	// low-probability birds in consumers that do not re-sort (the CSV export and
	// the range-filter test preview). A stable sort keeps the deterministic append
	// order of the equal-scored 1.0 species (secondary models are walked in
	// sorted-by-ID order above), so the output does not shuffle run to run.
	sort.Stable(ByScore(scores))

	return scores, nil
}

// GetSpeciesOccurrence returns the occurrence probability for a species at the current time.
func (o *Orchestrator) GetSpeciesOccurrence(species string) float64 {
	return o.GetSpeciesOccurrenceAtTime(species, time.Now())
}

// GetSpeciesOccurrenceAtTime returns the occurrence probability for a species at a specific time.
func (o *Orchestrator) GetSpeciesOccurrenceAtTime(species string, detectionTime time.Time) float64 {
	rfs, ok := o.rangeFilterReady()
	if !ok || rfs == nil {
		return 0
	}
	return rfs.occurrenceAtTime(species, detectionTime, o.CurrentSettings())
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
// GetAllProbableSpeciesWithSettings, which range-filters the bird models,
// AllLabels is the unfiltered superset. It is the label source for the reverse
// name-search maps so localized common names of secondary-model species (bats,
// Perch-unique species) are searchable, matching how the forward display path
// already resolves them.
func (o *Orchestrator) AllLabels() []string {
	if o == nil {
		return nil
	}
	primary, refs := o.orderedEntryRefs()

	sets := make([][]string, 0, len(refs)+1)
	// Include the range-filter anchor (v2.4) explicitly via the returned pointer so
	// its labels lead, matching the pre-refactor ordering. primary.Labels() is safe
	// without entry.mu because BirdNET.Labels takes the model's own lock internally.
	if primary != nil {
		sets = append(sets, primary.Labels())
	}
	for _, ref := range refs {
		// Capture the instance under entry.mu, release the lock, THEN call Labels().
		// BirdNET.Labels takes bn.mu, so holding entry.mu across it would stall
		// PredictModel on the inference hot path (the hazard the ModelInfos comment
		// calls out). Mirrors ModelInfos / LoadedModelPaths.
		ref.entry.mu.Lock()
		instance := ref.entry.instance
		ref.entry.mu.Unlock()
		var labels []string
		if instance != nil {
			labels = instance.Labels()
		}
		sets = append(sets, labels)
	}
	return unionLabels(sets...)
}

// orderedEntryRefs returns the range-filter anchor instance (the loaded BirdNET v2.4
// model, or nil) plus the other model entries byte-sorted by ID. When the anchor is
// present its own map entry is skipped, because its labels are taken from the returned
// pointer (unionLabels dedupes regardless). This reproduces the pre-refactor AllLabels
// ordering exactly: anchor first, then the rest byte-sorted, so Go's randomized map
// iteration cannot pick a different winner for a duplicate scientific name in the
// reverse name maps. The caller reads each other entry's instance under entry.mu.
func (o *Orchestrator) orderedEntryRefs() (primary *BirdNET, refs []entryRef) {
	// The range-filter anchor (v2.4) leads; every other entry follows, sorted
	// byte-wise by registry ID, so a duplicate scientific name resolves to the same
	// label regardless of Go's randomized map iteration order. The anchor entry is
	// dropped from refs only when it resolved to a *BirdNET (returned as primary);
	// if some other instance occupies that key it stays in refs so its labels are
	// not lost.
	primary, _ = o.birdNETV24Instance()
	o.mu.RLock()
	refs = make([]entryRef, 0, len(o.models))
	for id, entry := range o.models {
		if primary != nil && id == RegistryIDBirdNETV24 {
			continue
		}
		refs = append(refs, entryRef{id: id, entry: entry})
	}
	o.mu.RUnlock()

	slices.SortFunc(refs, func(a, b entryRef) int { return strings.Compare(a.id, b.id) })
	return primary, refs
}

// logMissingTaxonomyCodes emits, at debug level, the labels absent from the
// taxonomy, reproducing BirdNET.logMissingTaxonomyCodes now that the taxonomy is
// orchestrator-owned. primary supplies the "custom model/labels" phrasing; a nil
// taxonomy or primary is a no-op.
func (o *Orchestrator) logMissingTaxonomyCodes(primary *BirdNET, labels []string) {
	if o == nil || o.taxonomy == nil || primary == nil {
		return
	}
	s := o.currentSettings()
	customModelOrLabels := primary.configuredModelPath() != "" || s.BirdNET.LabelPath != ""
	o.taxonomy.logMissingCodes(labels, customModelOrLabels, s.BirdNET.Debug)
}

// GetSpeciesCode returns the eBird species code for a given label. The taxonomy is
// orchestrator-owned and immutable, so this needs no lock and no primary model; it
// keeps answering after Delete releases the primary (a shutdown-only state).
func (o *Orchestrator) GetSpeciesCode(label string) (string, bool) {
	if o == nil || o.taxonomy == nil {
		return "", false
	}
	return o.taxonomy.speciesCode(label)
}

// GetSpeciesNameFromCode returns the species name for a given eBird species code.
// The second return value reports whether the code was found in the orchestrator-
// owned taxonomy, which is immutable and so needs no lock; like GetSpeciesCode it
// keeps answering after Delete (a shutdown-only state).
func (o *Orchestrator) GetSpeciesNameFromCode(code string) (string, bool) {
	if o == nil || o.taxonomy == nil {
		return "", false
	}
	return o.taxonomy.nameFromCode(code)
}

// GetSpeciesWithScientificAndCommonName returns the scientific and common name for a label.
// The common name is label-derived (via SplitSpeciesName); OpenFauna (chain[0]) is
// authoritative and overrides it whenever the resolver chain has a localized name.
func (o *Orchestrator) GetSpeciesWithScientificAndCommonName(label string) (scientific, common string) {
	if o == nil {
		return "", ""
	}
	scientific, common = SplitSpeciesName(label)
	if scientific != "" {
		if resolved := o.ResolveName(scientific, ""); resolved != "" {
			common = resolved
		}
	}
	return scientific, common
}

// EnrichResultWithTaxonomy adds taxonomy information to a detection result.
// The common name is label-derived (via SplitSpeciesName); OpenFauna (chain[0]) is
// authoritative and overrides it whenever the resolver chain has a localized name,
// which localizes names and fixes scientific-only/bat labels. The label-derived
// name is kept only when the chain returns nothing.
func (o *Orchestrator) EnrichResultWithTaxonomy(speciesLabel string) (scientific, common, code string) {
	if o == nil {
		return "", "", ""
	}
	scientific, common = SplitSpeciesName(speciesLabel)
	if o.taxonomy != nil {
		var exists bool
		code, exists = o.taxonomy.speciesCode(speciesLabel)
		// Gate the debug format on the debug flag so the placeholder-code args are not
		// built (and o.Debug's lock not taken) on the taxonomy-miss path when debug is
		// off, matching the old bn.Debug call site which was wrapped in the same check.
		if !exists && o.currentSettings().BirdNET.Debug {
			o.Debug("Species '%s' not found in taxonomy, using generated placeholder code: %s", speciesLabel, code)
		}
	}

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
	settings := o.CurrentSettings()
	rf := settings.BirdNET.RangeFilter
	view := o.rangeFilterView()

	var (
		geomodel      *GeomodelStatus
		geoLabels     []string
		active        bool
		fellBack      bool
		mappedSpecies int
		kind          = rfKindNone
		state         *rangeFilterState
	)
	if rfs := o.rangeFilter; rfs != nil {
		// Take ONE state snapshot for the whole response so active, fellBack, kind, the
		// mapped view and per-participant coverage all describe the same backend even if
		// a concurrent reload swaps the state mid-build.
		state = rfs.loadState()
		active = state.backend != nil
		fellBack = state.fellBack
		if kind = state.kind; kind == "" {
			kind = rfKindNone // normalize the zero value, mirroring backendKind()
		}
		if mrf, ok := state.backend.(*mappedRangeFilter); ok {
			geoLabels = mrf.geomodelLabels
			// mrf.mappedCount is the mapped count over coveredLabels (v2.4's labels on a
			// v2.4 install, the participant union otherwise), so this is byte-identical
			// to the former primaryCoverage.WithRangeData for a v2.4 install.
			mappedSpecies = mrf.mappedCount

			version := rf.Model
			if version == conf.RangeFilterModelV3 {
				version = "v3.0"
			}
			geomodel = &GeomodelStatus{
				Version:      version,
				TotalSpecies: mrf.inner.NumSpecies(),
			}
		}
	}

	// Report whether an active v3 geomodel was auto-selected from the shared models
	// directory (ported from PrimaryRangeFilterCoverage).
	if geomodel != nil && rf.Model == conf.RangeFilterModelV3 && view.modelsDir != "" {
		sharedDir := filepath.Join(view.modelsDir, sharedDirName)
		expectedONNX := filepath.Join(sharedDir, conf.GeomodelONNXLocalName)
		expectedLabels := filepath.Join(sharedDir, conf.GeomodelLabelsLocalName)
		geomodel.AutoSelected = rf.ModelPath == expectedONNX && rf.LabelsPath == expectedLabels
	}

	// coveredByBackend reports whether the loaded backend actually scores a participant's
	// species, so the status surface is honest about which classifiers are really
	// range-filtered. It keys on the participant set the ACTIVE backend was built over
	// (state.participants), not the live view: after a failed rebuild the service keeps the
	// previous backend (rollback) while the live view already shows the just-loaded
	// participant, so reading the live view would falsely report that participant as covered
	// before the retained backend ever mapped its labels. A participant absent from the
	// built-over set is never covered. The universal geomodel scores every built-over
	// participant by canonical scientific name (the participant-union backfill in the range
	// filter and the Settings preview keep the gate and display in step), so every
	// participant under a geomodel is covered; the residual for geomodel-unknown species is
	// governed by the "allow species without range data" toggle, not by coverage. The legacy
	// v2.4-only MData backend covers only v2.4; no backend covers nothing.
	coveredByBackend := func(id string) bool {
		if state == nil || !state.hasParticipant(id) {
			return false
		}
		switch kind {
		case rfKindGeomodelV3:
			return true
		case rfKindMDataV2, rfKindMDataV1:
			return id == RegistryIDBirdNETV24
		default: // rfKindNone (or unknown): nothing is covered.
			return false
		}
	}

	// Per-participant coverage, built from the same rangeFilterView the backend build
	// consumes. Bat/BSG are not range-filter participants, so rangeFilterView never
	// includes them; a Perch-only or v3.0-only install is reported without a v2.4 gate.
	classifiers := buildClassifierViews(view, geoLabels)
	resp := RangeFilterStatusResponse{
		Geomodel:            geomodel,
		Classifiers:         make([]ClassifierCoverage, 0, len(classifiers)),
		PassUnmappedSpecies: rf.PassUnmappedSpecies,
		Threshold:           rf.Threshold,
		LocationConfigured:  settings.BirdNET.LocationConfigured,
		LastUpdated:         rf.LastUpdated,
		Active:              active,
		FellBack:            fellBack,
		MappedSpecies:       mappedSpecies,
		Backend:             string(kind),
		ParticipantsLoaded:  len(view.participants) > 0,
	}
	for _, cm := range classifiers {
		name := cm.id
		if info, exists := ModelRegistry[cm.id]; exists {
			name = info.Name
		}
		resp.Classifiers = append(resp.Classifiers, ClassifierCoverage{
			ID:               cm.id,
			Name:             name,
			TotalSpecies:     len(cm.labels),
			WithRangeData:    cm.mapped,
			WithoutRangeData: cm.unmapped,
			CoveredByBackend: coveredByBackend(cm.id),
		})
	}

	// Sort classifiers by ID for stable API output (map iteration is random).
	sort.Slice(resp.Classifiers, func(i, j int) bool {
		return resp.Classifiers[i].ID < resp.Classifiers[j].ID
	})

	return resp
}

// RangeFilterActive reports whether a range-filter backend is loaded (filtering is being
// applied). It is the cheap, lock-free predicate for the range-test API's FilterActive
// field: unlike RangeFilterStatus it does not recompute per-participant coverage over the
// full label set. False at N = 0 or when no backend is loaded.
func (o *Orchestrator) RangeFilterActive() bool {
	if o.rangeFilter == nil {
		return false
	}
	active, _ := o.rangeFilter.runtimeState()
	return active
}

// ReloadRangeFilter reinitializes the range filter on the primary model
// from current settings without a full model reload, then rebuilds the
// species inclusion list so the processor's detection filter reflects
// the new backend immediately.
func (o *Orchestrator) ReloadRangeFilter() error {
	rfs := o.rangeFilter
	if rfs == nil {
		return nil
	}
	GetLogger().Info("Reloading range filter from updated settings")
	// reload snapshots the participant view under its own lock, so this is not gated on
	// v2.4: applyInstalledGeomodelConfig on a Perch-only or v3.0-only install can now
	// light up the geomodel here.
	if err := rfs.reload(o.CurrentSettings(), o.rangeFilterView); err != nil {
		return err
	}
	GetLogger().Info("Range filter reloaded successfully")
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

// RunFilterProcess executes the filter process on demand and prints results. Ported
// from the former BirdNET.RunFilterProcess onto the orchestrator so the rangefilter
// CLI keeps working after the range filter moved to the service (Phase 2b).
func (o *Orchestrator) RunFilterProcess(dateStr string, week float32) {
	if _, ok := o.rangeFilterReady(); !ok {
		return
	}

	// If dateStr is not empty, parse the date.
	var parsedDate time.Time
	var err error
	if dateStr != "" {
		parsedDate, err = time.Parse(time.DateOnly, dateStr)
		if err != nil {
			fmt.Printf("Error parsing date: %s\n", err)
			return
		}
	}

	speciesScores, err := o.GetProbableSpecies(parsedDate, week)
	if err != nil {
		fmt.Printf("Error during species prediction: %s\n", err)
		return
	}

	PrintSpeciesScores(parsedDate, speciesScores)
}

// ReloadModel reloads the BirdNET v2.4 anchor through the single build-then-swap
// reloadEntry path with settings-reload semantics: an identity or model-file change is
// refused (v24SettingsReloadCheck) with the same "requires orchestrator restart" texts the
// former in-place reload used. reloadEntry serializes the reload, publishes the reloaded
// settings and labels, re-emits the missing-taxonomy diagnostics, then rebuilds the range
// filter (non-fatal) and the species index. Inference keeps flowing on the previous
// instance until the fresh one is swapped in.
func (o *Orchestrator) ReloadModel() error {
	_, err := o.reloadEntry(RegistryIDBirdNETV24, v24ReloadBuilder, reloadOpts{check: v24SettingsReloadCheck})
	return err
}

// reloadAnchorRangeFilter rebuilds the range-filter backend from the current
// settings after a full classifier reload. It is deliberately non-fatal: unlike the
// former joint in-place reload transaction, the classifier reload has already
// committed by the time this runs, so a range-filter build failure must not roll the
// classifier back. The service keeps its previous backend on failure, which stays
// correct because a locale change or a v2.4 variant swap preserves the species set
// and scientific names the mapping is keyed on.
func (o *Orchestrator) reloadAnchorRangeFilter() {
	rfs := o.rangeFilter
	if rfs == nil {
		return
	}
	// A v2.4 locale/variant reload can change the v2.4 label set the geomodel maps
	// onto, so rebuild the range filter from the current participant view.
	if err := rfs.reload(o.CurrentSettings(), o.rangeFilterView); err != nil {
		GetLogger().Warn("Range filter reload after model reload failed; keeping the previous range filter",
			logger.Error(err))
	}
}

// secondaryTripletFor returns the inference-backend key that OV-capable secondary
// models build against. The secondaries share the primary's OpenVINO configuration
// and CPU thread budget, so the key is derived from settings.BirdNET. Each
// modelEntry records this (modelEntry.backend) at build time so ReloadSecondaryModels
// can detect a per-model backend/device/thread-count change.
func secondaryTripletFor(settings *conf.Settings) secondaryBackendKey {
	return secondaryBackendKey{
		backend:  settings.BirdNET.Backend,
		ovDevice: settings.BirdNET.OpenVINODevice,
		ovPath:   settings.BirdNET.OpenVINOPath,
		threads:  settings.BirdNET.Threads,
	}
}

// ReloadSecondaryModels rebuilds the OV-capable secondary models (Perch, and the
// bat embedding extractor) when the BirdNET inference backend, OpenVINO device
// preference, or CPU thread count changes at runtime, so they move to the new
// device (or thread budget) without a full restart, matching the primary reload.
// It mirrors the primary reload's transactional safety: each model is built on
// the new backend BEFORE the old instance is closed, and a build failure leaves
// the old instance serving (one model failing does not abort the others).
//
// The change-detection gate is per-entry (modelEntry.backend): each OV-capable
// secondary is rebuilt only when its own recorded triplet differs from the
// current settings, so an unrelated reload_birdnet trigger (locale, thresholds)
// does not rebuild a large secondary model. Per-entry tracking (vs a single
// orchestrator-wide gate) stays exact when a secondary is installed out-of-band
// at runtime (LoadModel records the entry's triplet at load) and when more than
// one OV-capable secondary is loaded. Each entry's triplet is advanced
// unconditionally once its rebuild is reconciled: a hard build failure is
// reported once and the gate still advances, so it is not retried on every
// subsequent unrelated reload; the user re-toggles the device setting to retry.
//
// Returns the first build error encountered (for caller notification). The
// reload itself does not fail on a secondary error because the primary model
// has already reloaded by the time this runs.
//
// Lock discipline mirrors UnloadModel: o.mu is held only to snapshot the entry
// set, then released before the (potentially slow, JIT-compiling) builds; the
// per-entry gate read and the swap take entry.mu alone, which cannot deadlock
// against PredictModel (o.mu.RLock -> inferenceMu -> entry.mu) because entry.mu
// is acquired here as a leaf lock while holding nothing else. ReloadSecondaryModels
// is invoked from the serialized reload path, so concurrent invocations are not
// expected; if two ever interleaved, the per-entry gate makes the worst case a
// redundant rebuild, never a corrupted swap.
func (o *Orchestrator) ReloadSecondaryModels() error {
	log := GetLogger()

	// Read the fresh settings published by the primary reload that ran just before this
	// (ReloadModel atomically swapped the settings pointer) and pin ONE clone for the whole
	// batch, so the triplet, the thread budget, and every per-entry build stay on the same
	// snapshot even if another reload commits concurrently mid-batch. Each reloadEntry is
	// handed this snapshot via reloadOpts.settings instead of re-reading o.currentSettings().
	settings := conf.CloneSettings(o.currentSettings())
	triplet := secondaryTripletFor(settings)

	// Full per-model thread budget, computed once for the whole batch (inference is
	// serialized by inferenceMu, so each model gets the full budget) and passed per entry
	// into reloadEntry via reloadOpts.threads, so the allocation is not recomputed per
	// secondary.
	threadAlloc := o.computeThreadAllocation(settings)

	// Snapshot the OV-capable secondary entries under o.mu, then release it before the
	// per-entry gate checks and the reloadEntry calls (each of which takes o.reloadMu and,
	// briefly, o.mu again).
	o.mu.Lock()
	if o.models == nil {
		o.mu.Unlock()
		return errors.Newf("orchestrator has been deleted, cannot reload secondary models").
			Component("classifier.orchestrator").
			Category(errors.CategorySystem).
			Build()
	}
	refs := make([]entryRef, 0, len(openvinoCapableSecondaryBuilders))
	for id, entry := range o.models {
		if _, ok := openvinoCapableSecondaryBuilders[id]; ok {
			refs = append(refs, entryRef{id: id, entry: entry})
		}
	}
	// Sort by registry ID so the rebuild order (and thus logs and the returned firstErr when
	// several secondaries fail) is deterministic across the random map iteration order.
	slices.SortFunc(refs, func(a, b entryRef) int { return strings.Compare(a.id, b.id) })
	o.mu.Unlock()

	if len(refs) == 0 {
		return nil
	}

	var firstErr error
	// swapped tracks whether any entry's instance was actually replaced, so the species-name
	// index is rebuilt at most once, after the loop.
	swapped := false
	for _, ref := range refs {
		// Per-entry gate (the caller's policy; reloadEntry owns the build/warm-up/swap/close):
		// skip an entry whose instance was already torn down by a concurrent Delete/Unload, or
		// one already built on the current triplet. The gate read pairs with the triplet write
		// reloadEntry publishes under entry.mu with the swap (opts.backend). Skipping a detached
		// entry here avoids a multi-second JIT build that the orphan guard would only discard.
		ref.entry.mu.Lock()
		current := ref.entry.backend
		orphaned := ref.entry.instance == nil
		ref.entry.mu.Unlock()
		if orphaned {
			continue
		}
		if current == triplet {
			log.Debug("secondary model already built on current backend/device/threads, skipping reload",
				logger.String("registry_id", ref.id),
				logger.String("backend", triplet.backend),
				logger.String("ov_device", triplet.ovDevice),
				logger.Int("threads", triplet.threads))
			continue
		}

		// Build-then-swap through the single reload path. reloadEntry warms up under
		// inferenceMu, swaps instance+generation+backend atomically under entry.mu, closes the
		// old instance after unlock, and cleans up an orphaned build; it computes the thread
		// budget from the settings clone exactly as the previous inline builder did. The
		// per-entry backend triplet is published with the swap so the gate above stays coherent.
		// A secondary present in o.models but absent from settings.Models.Enabled is not
		// keyed in threadAlloc; fall back to the full budget, matching LoadModel's default
		// (inference is serialized by inferenceMu, so each model gets all threads).
		threads := threadAlloc[ref.id]
		if threads <= 0 {
			threads = settings.BirdNET.Threads
			if threads <= 0 {
				threads = runtime.NumCPU()
			}
		}
		sw, rerr := o.reloadEntry(ref.id, openvinoCapableSecondaryBuilders[ref.id], reloadOpts{
			backend:          &triplet,
			skipSpeciesIndex: true,
			threads:          threads,
			settings:         settings,
		})
		if rerr != nil {
			if firstErr == nil {
				firstErr = rerr
			}
			// Hard build failure: keep the old instance serving, but advance this entry's gate
			// (advance-always) so an unrelated reload does not retry the failed build. Skip the
			// advance if the entry was torn down while building (a detached entry's gate is moot).
			ref.entry.mu.Lock()
			if ref.entry.instance != nil {
				ref.entry.backend = triplet
			}
			ref.entry.mu.Unlock()
			log.Error("failed to rebuild secondary model on backend/device/threads change; keeping existing instance",
				logger.String("registry_id", ref.id),
				logger.String("backend", triplet.backend),
				logger.String("ov_device", triplet.ovDevice),
				logger.Int("threads", triplet.threads),
				logger.Error(rerr))
			continue
		}
		if sw {
			swapped = true
			log.Info("secondary model reloaded on new backend/device/threads",
				logger.String("registry_id", ref.id),
				logger.String("backend", triplet.backend),
				logger.String("ov_device", triplet.ovDevice),
				logger.Int("threads", triplet.threads))
		}
	}

	// Rebuild once if any instance was swapped, with no orchestrator lock held. A
	// backend/device/threads swap keeps the label set, so the union is unchanged; the single
	// rebuild keeps the "last trigger in a reload sequence republishes with the newest-locale
	// resolver" ordering guarantee cheap and uniform across triggers.
	if swapped {
		o.rebuildSpeciesIndex()
	}

	return firstErr
}

// Delete releases all resources held by the Orchestrator and its models.
// After calling Delete, the Orchestrator must not be used.
func (o *Orchestrator) Delete() {
	// Snapshot the models, stop the scheduler, and clear o.models under o.mu so the
	// accessors (which resolve the range-filter anchor from o.models under o.mu)
	// observe the deleted state immediately and fail fast. Then release o.mu before the
	// per-model Close() calls: Close does native teardown that can be slow, and
	// holding o.mu across it would block every accessor for the duration of
	// teardown. UnloadModel uses the same drop-lock-before-close shape.
	o.mu.Lock()
	models := o.models
	rfs := o.rangeFilter
	// Swap(nil) both retrieves the scheduler to stop and clears the pointer, so a
	// re-used orchestrator does not see a stale stopped scheduler (SetSunCalc skips
	// creating a new one when o.scheduler is already non-nil).
	if s := o.scheduler.Swap(nil); s != nil {
		s.stop()
	}
	o.models = nil
	o.mu.Unlock()

	// Close the range-filter backend outside o.mu. close() empties the published
	// state under rfs.mu, so any accessor that runs after teardown reads a nil
	// backend and returns its zero value: the range-filter accessors read the empty
	// published state (no backend), so they fail open / return their zero value, and
	// GeomodelSpeciesInfo via mappedView gets ok=false from that empty state. The
	// service pointer is intentionally left set: GeomodelSpeciesInfo reads it without
	// o.mu, so nilling it here would be a data race for no benefit.
	if rfs != nil {
		rfs.close()
	}

	o.rssMu.Lock()
	o.modelRSS = make(map[string]int64)
	o.rssMu.Unlock()

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
		dropInferenceFailureStreak(id)
		entry.mu.Unlock()
	}

	CloseHeatmapService()
}

// IsModelLoaded returns true if a model with the given registry ID is
// currently loaded in the orchestrator.
//
// This takes o.mu.RLock, unlike its sibling IsModelActive which is deliberately
// lock-free: IsModelActive reads only the scheduler atomic.Pointer, while the
// models map read here is mutable state guarded by o.mu (ReloadModel, Delete, and
// the loaders all mutate it under the write lock). The asymmetry is intentional.
// On the monitor tick the RLock is reached only once a full analysis window is
// present and sits directly before a millisecond-scale inference, so it was
// measured as negligible; a lock-free models map is not worth the copy-on-write
// machinery it would require.
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
	RegistryIDBirdNETV24: (*Orchestrator).loadBirdNETV24,
	RegistryIDBirdNETV3:  (*Orchestrator).loadBirdNETV3,
	RegistryIDPerchV2:    (*Orchestrator).loadPerch,
	RegistryIDBat:        (*Orchestrator).loadBat,
}

// entryBuilder constructs (but does not register) a fresh model instance from a
// settings snapshot, for reloadEntry's transactional build-then-swap hot-reload path.
// Used by both the OV-capable secondary builders and the v2.4 reload builder; the
// pathResolution a builder computes is discarded (a reload never rewrites paths).
type entryBuilder func(o *Orchestrator, settings *conf.Settings, threads int) (ModelInstance, error)

// openvinoCapableSecondaryBuilders maps the registry IDs of secondary models
// whose construction honors the BirdNET inference backend / OpenVINO device
// preference and CPU thread count to a builder returning a fresh, unregistered
// instance. ReloadSecondaryModels rebuilds exactly these models when the
// backend/device/thread-count changes at runtime. For Bat, only the heavy
// embedding extractor honors the preference; the tiny bat classifier head always
// runs on ORT. Giving a new secondary OpenVINO support is a one-line entry here,
// paired with the OV fields on its loader config.
var openvinoCapableSecondaryBuilders = map[string]entryBuilder{
	RegistryIDBirdNETV3: func(o *Orchestrator, settings *conf.Settings, threads int) (ModelInstance, error) {
		// Explicit nil-on-error return avoids the typed-nil interface trap (a
		// nil *BirdNETV3 wrapped in a non-nil ModelInstance).
		// The path resolution is deliberately discarded: a backend or device swap
		// must never rewrite the user's configured paths, so the reload path does
		// not call queuePathCorrection.
		m, _, err := o.buildBirdNETV3(settings, threads)
		if err != nil {
			return nil, err
		}
		return m, nil
	},
	RegistryIDPerchV2: func(o *Orchestrator, settings *conf.Settings, threads int) (ModelInstance, error) {
		// Explicit nil-on-error return avoids the typed-nil interface trap (a
		// nil *Perch wrapped in a non-nil ModelInstance).
		// The path resolution is deliberately discarded (see the BirdNET v3.0 entry above).
		p, _, err := o.buildPerch(settings, threads)
		if err != nil {
			return nil, err
		}
		return p, nil
	},
	RegistryIDBat: func(o *Orchestrator, settings *conf.Settings, threads int) (ModelInstance, error) {
		// Explicit nil-on-error return avoids the typed-nil interface trap (a
		// nil *Bat wrapped in a non-nil ModelInstance).
		// The path resolution is deliberately discarded (see the BirdNET v3.0 entry above).
		b, _, err := o.buildBat(settings, threads)
		if err != nil {
			return nil, err
		}
		return b, nil
	},
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

	// Drain any queued configuration repair after o.mu is released: loaders queue
	// it under o.mu, and applying it writes config.yaml, so it must run outside the
	// lock. Registered BEFORE the warm-up drain so that, defers being LIFO, it runs
	// AFTER the warm-ups. The config write must not land inside the window the
	// per-model RSS delta measures (see runPendingWarmups), matching the drain
	// order in loadEnabledModels (warm-ups first, config write after).
	defer o.runPendingPathCorrections()

	// Drain the deferred warm-up after o.mu is released, on every return path.
	// Loaders queue the warm-up via deferWarmup while holding o.mu; running it
	// here (outside o.mu, via the serialized inference path) is what keeps a
	// runtime install from stalling live inference. Deferring it
	// (rather than calling it only on success) guarantees a loader that queues a
	// warm-up and then fails cannot orphan an entry in the queue for a later load.
	// Registered LAST so it runs FIRST (LIFO).
	defer o.runPendingWarmups()

	// Build and register the model under o.mu (loaders write directly to
	// o.models).
	if err := func() error {
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
			o.recordLoadFailure(registryID, err)
			return err
		}
		// The model registered successfully; clear any stale unload tombstone and the
		// stored load error so a later not-loaded diagnosis reports the model's actual
		// current state rather than a now-superseded unload or failure. The cumulative
		// modelLoadFailures count is intentionally kept for the LoadFailures metric.
		o.modelUnloaded.Delete(registryID)
		o.modelLoadErrors.Delete(registryID)

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
	}(); err != nil {
		return err
	}

	// Republish the species-name index from the new union now that this model's
	// labels are loaded. Runs after the locked closure released o.mu, and before
	// the deferred warm-up and path-correction drains (neither reads the index, so
	// the order relative to them is immaterial).
	o.rebuildSpeciesIndex()

	// A newly loaded range-filter participant changes the covered label set and can
	// flip backend selection to the geomodel, so rebuild the range-filter backend and
	// inclusion list; otherwise its species stay dropped until the next daily rebuild.
	// Non-participants (Bat/BSG) do not affect range filtering, so they skip this.
	if ParticipatesInRangeFilter(registryID) {
		o.rebuildRangeFilterAfterModelChange()
	}

	// A successful load can clear the "no acoustic model" state (or, on a retry that still
	// fails, this is unreachable because the loader returned an error above). Re-evaluate the
	// notice after o.mu is released (the sync reads AcousticModelsState under o.mu.RLock).
	o.syncAcousticModelsNotice()

	return nil
}

// ReconcileEnabledModels loads every model named in models.enabled that is not currently
// loaded and unloads every loaded acoustic model no longer named, so a runtime edit of
// models.enabled takes effect without a restart (Phase 4, since models.enabled is
// authoritative). Unloads run first to free memory on constrained hosts before loads
// allocate. It holds no orchestrator lock across the loop (LoadModel/UnloadModel each take
// o.mu themselves); a model that fails to load is recorded by the loader (recordLoadFailure)
// and skipped so one bad model does not block the rest, matching startup loadEnabledModels.
// The acoustic-model notice is re-evaluated once at the end regardless of per-model outcome.
func (o *Orchestrator) ReconcileEnabledModels() (loaded, unloaded []string, err error) {
	defer o.syncAcousticModelsNotice()

	settings := o.currentSettings()

	// Desired set: the known registry IDs named by models.enabled, in config order.
	desired := make([]string, 0)
	desiredSet := make(map[string]bool)
	for m := range enabledModels(settings) {
		if !m.known || desiredSet[m.registryID] {
			continue
		}
		desiredSet[m.registryID] = true
		desired = append(desired, m.registryID)
	}

	// Snapshot the currently loaded models.
	o.mu.RLock()
	loadedNow := slices.Collect(maps.Keys(o.models))
	o.mu.RUnlock()

	var errs []error

	// Unload first: a loaded model no longer named. "not loaded" (a concurrent unload won the
	// race) is benign and not reported.
	for _, id := range loadedNow {
		if desiredSet[id] {
			continue
		}
		if uerr := o.UnloadModel(id); uerr != nil {
			if !o.IsModelLoaded(id) {
				continue
			}
			errs = append(errs, uerr)
			continue
		}
		unloaded = append(unloaded, id)
	}

	// Then load desired models that are not already loaded.
	for _, id := range desired {
		if o.IsModelLoaded(id) {
			continue
		}
		if lerr := o.LoadModel(id); lerr != nil {
			errs = append(errs, lerr) // LoadModel already recorded the failure; keep going
			continue
		}
		loaded = append(loaded, id)
	}

	return loaded, unloaded, errors.Join(errs...)
}

// recordLoadFailure atomically increments the load-failure counter for registryID
// and records the last error text, so a later "model not loaded" diagnosis
// (modelNotLoadedReason) can explain why the model is missing. Safe to call
// concurrently; does not require o.mu.
func (o *Orchestrator) recordLoadFailure(registryID string, err error) {
	v, _ := o.modelLoadFailures.LoadOrStore(registryID, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
	if err != nil {
		o.modelLoadErrors.Store(registryID, err.Error())
	}
}

// LoadFailures returns a snapshot of the per-model load-failure counts accumulated
// since this Orchestrator was created. The returned map is a copy; callers may
// read and discard it freely. Safe to call concurrently with LoadModel.
func (o *Orchestrator) LoadFailures() map[string]int64 {
	result := make(map[string]int64)
	o.modelLoadFailures.Range(func(key, value any) bool {
		result[key.(string)] = value.(*atomic.Int64).Load()
		return true
	})
	return result
}

// LoadErrors returns the last load error text for each currently-enabled model whose most
// recent load attempt failed and has not since succeeded. It is the live-fault view: it is
// filtered to models.enabled so a disabled model's stale error is not reported, and a
// successful load clears a model's entry (loadEnabledModels, LoadModel). The cumulative
// LoadFailures counter is deliberately left unfiltered. The returned map is a copy; safe to
// call concurrently. modelIDEnabled reads the published settings snapshot lock-free, so
// this holds no orchestrator lock while calling it.
func (o *Orchestrator) LoadErrors() map[string]string {
	result := make(map[string]string)
	o.modelLoadErrors.Range(func(key, value any) bool {
		k, kok := key.(string)
		v, vok := value.(string)
		if kok && vok && o.modelIDEnabled(k) {
			result[k] = v
		}
		return true
	})
	return result
}

// AcousticModelsState is the coarse "is anything loaded" verdict. It is intended for the
// acoustic_models health check and GET /api/v2/system/inference, which consume it in a later
// paired change; in this change it has no non-test consumer yet.
type AcousticModelsState string

const (
	// AcousticModelsOK: at least one acoustic model is loaded.
	AcousticModelsOK AcousticModelsState = "ok"
	// AcousticModelsNoneInstalled: nothing loaded and no enabled model failed to load
	// (nothing enabled, or nothing installed). This is the supported N=0 state.
	AcousticModelsNoneInstalled AcousticModelsState = "none_installed"
	// AcousticModelsLoadFailed: nothing loaded and at least one enabled model failed to
	// load (a fault, not a fresh-install state).
	AcousticModelsLoadFailed AcousticModelsState = "load_failed"
)

// AcousticModelsState reports ok when any acoustic model is loaded, load_failed when none
// is and at least one ENABLED model has a load error still on record, and none_installed
// otherwise (nothing enabled, nothing installed, or the orchestrator has been shut down).
// The load_failed check is filtered to models.enabled so a disabled model's stale error
// does not force the fault state; a stored error is cleared by a later successful load
// (loadEnabledModels, LoadModel), so a recovered model does not keep the state at
// load_failed. Safe to call concurrently (modelIDEnabled reads the settings snapshot
// lock-free, and this holds no orchestrator lock while calling it).
func (o *Orchestrator) AcousticModelsState() AcousticModelsState {
	o.mu.RLock()
	deleted := o.models == nil
	loaded := len(o.models)
	o.mu.RUnlock()
	switch {
	case loaded > 0:
		return AcousticModelsOK
	case deleted:
		return AcousticModelsNoneInstalled
	}
	// Nothing loaded and not deleted: an ENABLED model may have failed to load. Only an
	// enabled model's error counts as a live fault; a disabled model's stored error is
	// ignored.
	state := AcousticModelsNoneInstalled
	o.modelLoadErrors.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && o.modelIDEnabled(k) {
			state = AcousticModelsLoadFailed
			return false // the first enabled failure is enough
		}
		return true // keep scanning for an enabled failure
	})
	return state
}

// ErrModelNotLoaded is returned by PredictModel when the requested model is not
// in the loaded set. It wraps a per-call reason (see modelNotLoadedReason);
// callers match it with errors.Is. Plain sentinel: no telemetry registered at
// package init.
var ErrModelNotLoaded = errors.NewStd("model not loaded")

// Reasons a model can be absent from o.models when PredictModel is called.
// modelNotLoadedReason returns the most specific, most actionable one so a
// transient not-loaded predict (Sentry BIRDNET-GO-2G6 / 1S1, the monitor that
// outlives an unload during the topology-reconfigure debounce) is legible rather
// than a bare "unknown model".
const (
	notLoadedReasonDeleted         = "the orchestrator has been shut down"
	notLoadedReasonUnknownRegistry = "no model with this ID is registered"
	notLoadedReasonNotEnabled      = "the model is not enabled in settings"
	notLoadedReasonUnloaded        = "the model was unloaded (a model reconfigure or reinstall is in progress)"
	notLoadedReasonNeverLoaded     = "the model has not finished loading"
	// notLoadedReasonFailedFormat renders the load-failure count and last error.
	notLoadedReasonFailedFormat = "the model failed to load %d time(s), last error: %s"
)

// modelNotLoadedReason explains why modelID is absent from o.models, turning the
// bare "unknown model" into an actionable diagnosis. Safe to call without holding
// o.mu: it takes its own read lock only for the map-nil check (PredictModel has
// already released the lock at this point) and otherwise reads lock-free
// sync.Maps and the published settings snapshot. The most specific, most
// actionable cause wins.
func (o *Orchestrator) modelNotLoadedReason(modelID string) string {
	o.mu.RLock()
	deleted := o.models == nil
	o.mu.RUnlock()
	if deleted {
		return notLoadedReasonDeleted
	}
	if _, registered := ModelRegistry[modelID]; !registered {
		return notLoadedReasonUnknownRegistry
	}
	// A stored error means the model's most recent load attempt failed and has not
	// since succeeded: a successful load clears the error (see LoadModel /
	// loadEnabledModels), so gate on the error's PRESENCE rather than the
	// modelLoadFailures count, which is a cumulative lifetime counter (kept for the
	// LoadFailures metric) that survives a later success. Without this gate, a model
	// that failed once, recovered, then was cleanly unloaded would be misreported as
	// "failed to load" with a stale error instead of "unloaded".
	if e, ok := o.modelLoadErrors.Load(modelID); ok {
		lastErr, _ := e.(string)
		count := int64(0)
		if v, ok := o.modelLoadFailures.Load(modelID); ok {
			count = v.(*atomic.Int64).Load()
		}
		return fmt.Sprintf(notLoadedReasonFailedFormat, count, lastErr)
	}
	if _, unloaded := o.modelUnloaded.Load(modelID); unloaded {
		return notLoadedReasonUnloaded
	}
	if !o.modelIDEnabled(modelID) {
		return notLoadedReasonNotEnabled
	}
	return notLoadedReasonNeverLoaded
}

// modelIDEnabled reports whether registryID corresponds to a model the user has
// enabled: an entry in settings.Models.Enabled once its config alias is resolved
// to a registry ID. Uses the shared enabledModels walk, so it agrees with
// computeThreadAllocation and loadEnabledModels on what "enabled" means.
func (o *Orchestrator) modelIDEnabled(registryID string) bool {
	for m := range enabledModels(o.currentSettings()) {
		if m.known && m.registryID == registryID {
			return true
		}
	}
	return false
}

// UnloadModel removes a model from the Orchestrator and releases its resources.
// Called by ModelManager during uninstall. The built-in v2.4 model is protected
// from uninstall by ModelManager (Uninstall refuses the permanent model), not here.
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
	// Tombstone the model so a predict that races the asynchronous monitor
	// teardown (the topology-reconfigure debounce window) is diagnosed as
	// "unloaded" rather than a bare unknown model. Cleared on the next load.
	o.modelUnloaded.Store(registryID, struct{}{})
	if registryID == RegistryIDBat {
		if s := o.scheduler.Load(); s != nil {
			s.stop()
		}
	}
	if registryID == RegistryIDBirdNETV24 {
		// v2.4 is gone: drop its label resolver from the chain (copy-on-write under o.mu)
		// so ResolveName falls through to OpenFauna.
		o.withV24LabelResolverLocked(nil)
	}
	o.mu.Unlock()

	// Close the model instance outside the map lock, in an inner func so entry.mu
	// is released before rebuildSpeciesIndex runs. Acquire the per-model lock to
	// wait for any in-flight inference to complete before deleting the counter
	// (prevents re-creation by in-flight RecordInvoke). Rebuilding while still
	// holding entry.mu would take entry.mu -> o.mu (via AllLabels), inverting the
	// documented o.mu -> entry.mu order.
	func() {
		entry.mu.Lock()
		defer entry.mu.Unlock()

		globalInferenceCounters.Delete(registryID)
		dropInferenceFailureStreak(registryID)

		o.rssMu.Lock()
		delete(o.modelRSS, registryID)
		o.rssMu.Unlock()

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
	}()

	// Republish the species-name index from the new (smaller) union now that this
	// model's labels are gone, with no orchestrator lock held.
	o.rebuildSpeciesIndex()

	// Removing a range-filter participant shrinks the covered label set (and can drop
	// the geomodel back to MData or nothing), so rebuild the range-filter backend and
	// inclusion list. Non-participants (Bat/BSG) do not affect range filtering.
	if ParticipatesInRangeFilter(registryID) {
		o.rebuildRangeFilterAfterModelChange()
	}

	// Unloading may have reached N = 0 (or cleared a load failure): re-evaluate the
	// acoustic-model notice. Called after the locked closures release o.mu, since the sync
	// reads AcousticModelsState() under o.mu.RLock (acousticNotice.mu -> o.mu leaf edge).
	o.syncAcousticModelsNotice()

	return nil
}

// rebuildRangeFilterAfterModelChange rebuilds the range-filter backend and inclusion
// list after a range-filter participant is installed or removed at runtime.
// LoadModel/UnloadModel update the species-name index but not the range-filter backend
// or the inclusion list (conf.IncludedScientificNames), so without this a newly
// installed participant's species would be dropped, a removed one's kept, and
// installing a geomodel-capable participant would not light up the geomodel until the
// next daily rebuild. Both steps are non-fatal: on failure the previous range filter
// keeps serving. Rare (a gallery install/uninstall), so the geomodel rebuild cost is
// acceptable, and a full reload (rather than an in-place relabel) re-runs backend
// selection and closes the old backend cleanly. Runs with no orchestrator lock held.
func (o *Orchestrator) rebuildRangeFilterAfterModelChange() {
	rfs := o.rangeFilter
	if rfs == nil {
		return
	}
	if err := rfs.reload(o.CurrentSettings(), o.rangeFilterView); err != nil {
		GetLogger().Warn("Range filter reload after model load/unload failed; keeping the previous range filter",
			logger.Error(err))
		return
	}
	if err := BuildRangeFilter(o); err != nil {
		GetLogger().Warn("Range filter inclusion-list rebuild after model load/unload failed",
			logger.Error(err))
	}
}

// lockedBatchRangeFilter returns the geomodel-backed mapped range filter for batch
// inference together with a release func that MUST be called after use; the range
// filter service lock is held across the returned filter's native call because its
// ONNX session is not goroutine-safe (Phase 2b; formerly lockedMappedRangeFilter
// held primary.mu). On error no lock is held and the release func is a no-op.
func (o *Orchestrator) lockedBatchRangeFilter() (*mappedRangeFilter, func(), error) {
	rfs, ok := o.rangeFilterReady()

	if !ok || rfs == nil {
		return nil, func() {}, errors.Newf("range filter not available: no range filter service").
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Build()
	}

	return rfs.lockedBatchFilter()
}

// BatchRangeFilterInference runs batch geomodel inference on multiple location/week
// inputs. The caller provides a flat slice of [lat, lon, week] triples and a batch
// size. Returns a flat slice of [batchSize * numGeoSpecies] scores in row-major order.
//
// Acquires the range-filter service lock (not inferenceMu) because the range filter
// and classifier use independent ONNX sessions. Callers that need to process many
// grid points should chunk externally and call this method once per chunk, allowing
// the detection pipeline to interleave between calls.
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

	mrf, release, err := o.lockedBatchRangeFilter()
	if err != nil {
		return nil, err
	}
	defer release()

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
	rfs := o.rangeFilter
	if rfs == nil {
		return 0, 0, false
	}
	// The mapped filter is published immutably behind an atomic pointer, so a single
	// lock-free load gives a consistent snapshot; geomodelIndex and geomodelLabels are
	// read-only after construction, so no lock is needed (formerly held primary.mu).
	mrf, ok := rfs.mappedView()
	if !ok {
		return 0, 0, false
	}
	idx, ok := mrf.geomodelIndex[label]
	if !ok {
		return 0, 0, false
	}
	return idx, len(mrf.geomodelLabels), true
}

// Debug prints debug messages if debug mode is enabled.
func (o *Orchestrator) Debug(format string, v ...any) {
	if s := o.CurrentSettings(); s != nil && s.BirdNET.Debug {
		GetLogger().Debug(fmt.Sprintf(format, v...))
	}
}

// DefaultTargets returns the models a source with an empty model list analyzes
// with: every loaded model that is not schedule-gated (isScheduleGated, today the
// bat model), the BirdNET v2.4 entry first when it is loaded and the rest byte
// ordered by registry ID, each carrying the live Backend/Quantization/NumSpecies
// and effective overlap ModelInfos stamps. Nil when nothing qualifies (N = 0, or
// only gated models loaded). v2.4 leads so the first entry is stable: a source with
// a non-empty but unresolvable model list falls back to the first default alone
// (fallbackTargets), which must stay v2.4 so an upgrade never swaps a misconfigured
// source onto a different model, and the species_count startup read keys on element
// 0; past the first entry the order only fixes what the status API and logs report.
func (o *Orchestrator) DefaultTargets() []ModelInfo {
	infos := o.ModelInfos()
	targets := make([]ModelInfo, 0, len(infos))
	for i := range infos {
		if isScheduleGated(infos[i].ID) {
			continue
		}
		targets = append(targets, infos[i])
	}
	if len(targets) == 0 {
		return nil
	}
	slices.SortFunc(targets, func(a, b ModelInfo) int {
		if ra, rb := defaultTargetRank(a.ID), defaultTargetRank(b.ID); ra != rb {
			return ra - rb
		}
		return strings.Compare(a.ID, b.ID)
	})
	return targets
}

// defaultTargetRank orders the BirdNET v2.4 entry ahead of every other default
// target so the first default is stable: fallbackTargets uses defaults[:1] for a
// misconfigured source (it must stay v2.4 for I1) and the species_count startup read
// keys on element 0; every other model shares a rank and falls back to byte order.
func defaultTargetRank(registryID string) int {
	if registryID == RegistryIDBirdNETV24 {
		return 0
	}
	return 1
}

// ResolvedModelPathForID returns the model file the loaded registryID instance is
// actually running: "" when the instance runs its built-in/default source, when the
// ID is not loaded, or when the models map is not initialized. It snapshots the entry
// under o.mu, captures the instance under entry.mu and releases both before calling
// the lock-free ResolvedModelPath(), mirroring LoadedModelPaths so it adds no
// lock-ordering edge.
func (o *Orchestrator) ResolvedModelPathForID(registryID string) string {
	o.mu.RLock()
	entry := o.models[registryID]
	o.mu.RUnlock()
	if entry == nil {
		return ""
	}
	entry.mu.Lock()
	instance := entry.instance
	entry.mu.Unlock()
	if instance == nil {
		return ""
	}
	return instance.ResolvedModelPath()
}

// liveModelInfoProvider is implemented by instances whose effective identity
// (Backend, Quantization, CustomPath) is resolved at build time and can differ from
// the static ModelRegistry template. ModelInfos prefers it over the template for ANY
// instance that implements it, so no registry ID is special-cased. Today only *BirdNET
// implements it; secondaries fall through to the registry template.
type liveModelInfoProvider interface{ LiveModelInfo() ModelInfo }

// ModelInfos returns ModelInfo for all registered models. Thread-safe.
// Used by the pipeline to build ModelTarget lists for buffer fan-out.
// For an instance that reports a live identity (liveModelInfoProvider), that live
// ModelInfo is returned rather than the static registry template, so the reported
// Backend and Quantization match the actually loaded model (e.g. ONNX/INT8 on the
// arm64 container default). NumSpecies is always sourced from the live instance (not
// the template) so a sliced or custom model reports its actual loaded label count
// rather than the stock catalog number.
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

	// Resolve overlap against the live settings snapshot (independently
	// synchronized), so every returned ModelInfo carries the effective overlap
	// used for buffer allocation and cadence.
	settings := o.CurrentSettings()

	infos := make([]ModelInfo, 0, len(refs))
	for _, ref := range refs {
		// Capture the instance pointer under entry.mu, then release the lock before
		// calling any instance method. Those accessors take their own per-model lock
		// (BirdNET locks bn.mu), so holding entry.mu across them would stall
		// PredictModel, which needs entry.mu only briefly on the inference hot path.
		// This mirrors instanceFor / GetModelRuntimeInfo. The captured pointer stays
		// usable even if the entry is torn down concurrently: the accessors below read
		// label/settings/info state, not the native classifier that Close() frees.
		ref.entry.mu.Lock()
		instance := ref.entry.instance
		ref.entry.mu.Unlock()
		if instance == nil {
			continue
		}
		var info ModelInfo
		if p, ok := instance.(liveModelInfoProvider); ok {
			// Prefer the instance's live identity so Backend/Quantization reflect the
			// actually loaded model, not the static registry template. LiveModelInfo
			// takes the instance's own lock; entry.mu is already released above, so this
			// does not deepen the o.mu -> entry.mu nesting.
			info = p.LiveModelInfo()
		} else {
			var exists bool
			info, exists = ModelRegistry[ref.id]
			if !exists {
				info = ModelInfo{
					ID:   instance.ModelID(),
					Name: instance.ModelName(),
					Spec: instance.Spec(),
				}
			}
		}
		// NumSpecies depends on the model's loaded label file, which a user can
		// override with a sliced or custom model (e.g. a regional Perch v2 slice
		// with far fewer classes than the stock 14,795). The static registry template
		// carries the stock catalog count, so source it from the live instance to
		// report what is actually loaded.
		info.NumSpecies = instance.NumSpecies()
		info.Overlap = ResolveModelOverlap(info.ID, info.Spec, settings)
		infos = append(infos, info)
	}
	return infos
}

// enabledModel is one entry of settings.Models.Enabled resolved against the
// model registry.
type enabledModel struct {
	configID   string // the raw ID as written in models.enabled
	registryID string // the resolved registry ID (empty when known is false)
	known      bool   // whether configID resolved to a registry model
}

// enabledModels yields each settings.Models.Enabled entry in config order,
// resolved to its registry ID. It centralizes the settings.Models.Enabled ->
// ResolveConfigModelID walk shared by modelIDEnabled, computeThreadAllocation,
// and loadEnabledModels so the three stay in step. Unknown config IDs are
// yielded with known=false so each caller decides whether to warn or skip;
// deduplication is left to the callers that need it (computeThreadAllocation
// tracks a seen-set, loadEnabledModels relies on the models-map existence
// check), so the helper preserves each caller's existing behavior.
func enabledModels(settings *conf.Settings) iter.Seq[enabledModel] {
	return func(yield func(enabledModel) bool) {
		for _, configID := range settings.Models.Enabled {
			registryID, known := ResolveConfigModelID(configID)
			if !yield(enabledModel{configID: configID, registryID: registryID, known: known}) {
				return
			}
		}
	}
}

// computeThreadAllocation pre-computes thread distribution for all models
// that will be loaded. Inference is serialized by inferenceMu, so each model
// gets the full thread budget (they never run simultaneously).
func (o *Orchestrator) computeThreadAllocation(settings *conf.Settings) map[string]int {
	// Collect unique model IDs that will be loaded, walking models.enabled in config order
	// (authoritative since Phase 4) so it matches loadEnabledModels. Deduplicates case
	// variants like ["perch_v2", "PERCH_V2"] that resolve to the same ID.
	seen := make(map[string]bool, len(settings.Models.Enabled))
	modelIDs := make([]string, 0, len(settings.Models.Enabled))
	for m := range enabledModels(settings) {
		if !m.known || seen[m.registryID] {
			continue
		}
		seen[m.registryID] = true
		modelIDs = append(modelIDs, m.registryID)
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

// loadEnabledModels iterates models.enabled in config order (authoritative since Phase 4)
// and loads each one in that order. Each loaded model is registered in the models map; an
// already-registered model is skipped.
// threadAlloc provides the pre-computed thread count for each model. No load failure is
// fatal now that models.enabled is authoritative and N=0 is a supported runtime state
// (model de-privilege epic, Phase 4): every failure (v2.4 included) is recorded and the
// rest still load, so this never returns an error.
func (o *Orchestrator) loadEnabledModels(threadAlloc map[string]int) {
	log := GetLogger()

	// Drain the queued configuration repairs on every exit path, including a
	// panic unwinding out of a loader. Deferred rather than called after the loop
	// so this reads the same as the sibling drain in LoadModel; on the happy path
	// it still runs exactly where it did, immediately before the return.
	//
	// Warm-ups are drained per-iteration INSIDE the loop below, so they still
	// complete before this does: the config write must not land inside the window
	// a per-model RSS delta measures (see runPendingWarmups). That ordering is why
	// LoadModel registers its two drains in the order it does.
	defer o.runPendingPathCorrections()

	// Read the live published settings snapshot rather than the deprecated
	// o.Settings pointer, consistent with the per-model loaders (loadPerch/loadBat).
	settings := o.currentSettings()

	for m := range enabledModels(settings) {
		if !m.known {
			log.Warn("skipping unknown model ID in models.enabled",
				logger.String("model_id", m.configID))
			continue
		}
		registryID := m.registryID

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
			if err := loader(o, threadAlloc[registryID]); err != nil {
				return err
			}
			// The model registered; clear any stale unload tombstone and stored load
			// error (the cumulative failure count is kept for the LoadFailures metric).
			o.modelUnloaded.Delete(registryID)
			o.modelLoadErrors.Delete(registryID)
			return nil
		}()
		if loadErr != nil {
			// No load failure is fatal now that models.enabled is authoritative and N=0 is
			// a supported runtime state (model de-privilege epic, Phase 4). Record the
			// failure (not just log it) so a later not-loaded predict on this model can
			// report why it is missing instead of a bare unknown model, and keep loading
			// the rest. A v2.4 failure (e.g. the embedded model compiled out under noembed)
			// leaves the process running degraded rather than exiting; AcousticModelsState
			// then reports load_failed. An installed enabled model is retried by the startup
			// model scan (loadInstalledModels); a model with no on-disk install is not.
			o.recordLoadFailure(registryID, loadErr)
			log.Warn("model failed to load, will retry on the next model scan if installed",
				logger.String("registry_id", registryID),
				logger.Error(loadErr))
		}

		// Warm up the freshly-loaded model now that o.mu is released, before the
		// next model's build. Draining per-iteration (rather than once after the
		// loop) keeps RSS accounting accurate: this model's RSS-after is measured
		// before the next model allocates its arena.
		o.runPendingWarmups()
	}
}

// RarityContext bundles everything a caller needs to compute a species' rarity from one
// coherent settings generation: the probable-species scores, the two label vocabularies
// used to interpret them, whether the range filter was active, and the settings snapshot
// the scores were produced from. See GetRarityContext for the per-field semantics and the
// consistency guarantees.
type RarityContext struct {
	Scores []SpeciesScore
	// Geomodel is the universal geomodel's label vocabulary paired with Scores. It
	// is nil unless the universal geomodel path ran; when non-nil it is built from
	// the same range-filter instance that produced Scores. Coverage reads its
	// canonical-key memo, and a nil value means fall back to ClassifierLabels.
	Geomodel         *LabelVocabulary
	ClassifierLabels []string
	FilterActive     bool
	Settings         *conf.Settings
}

// GetRarityContext returns the primary model's probable-species scores together with
// the two label vocabularies needed to interpret them, so a caller computing rarity
// does not have to reassemble them from calls that can each observe a different model.
//
// Rarity is the geomodel occurrence probability, so the scores come from the primary
// (geomodel-backed) range filter, not the multi-model union: the union assigns
// synthetic always-active scores to secondary-model species that have no real
// occurrence probability.
//
// Consistency, stated precisely because the guarantee is partial: scores and the
// geomodel vocabulary (Geomodel) always describe the same range-filter instance,
// because getProbableSpecies captures the geomodel vocabulary under the same bn.mu hold that
// produces the scores. classifierLabels comes from the settings snapshot read here,
// which is the same snapshot getProbableSpecies indexes for zeroScoresForAllLabels and
// the unmapped-species mapping, so it agrees with the scores; but the range-filter
// instance is resolved later, under its own lock, so a reload landing in that window can
// still leave classifierLabels one generation behind the backend. That is narrower than
// the skew separate GetProbableSpecies + Labels calls admit, not an absence of skew.
//
// Note also that currentSettings resolves through conf.CurrentOrFallback, which prefers
// the globally published snapshot; an in-place model reload republishes only to the
// instance, so classifierLabels can lag a label-set change until the next restart.
//
// Geomodel is nil unless the universal geomodel path ran. It is nil for the
// TFLite meta model and the plain ONNX range filter, and when no range filter or
// location is configured; in every one of those cases the scores are labeled with the
// classifier's own vocabulary, so callers must fall back to classifierLabels.
//
// filterActive is true only when the returned scores are genuine location-based
// predictions; it is false when they are the synthetic all-zero fallback (no range
// filter loaded, or no location configured), so a caller can avoid reporting a zero as
// "very rare" (#3935).
//
// The returned RarityContext bundles the settings snapshot the scores were produced from
// together with the scores, the two vocabularies, and filterActive, so a caller
// assembling rarity metadata (location, threshold, coordinates) derives every field from
// the SAME settings generation as the score rather than taking a second,
// independently-resolved CurrentSettings() read that a concurrent reload could
// desynchronise from the score. Settings is non-nil whenever a primary exists or any
// settings have been published (i.e. in a running app); it can be nil only for an
// uninitialised orchestrator, so a caller that may run before startup must nil-check it.
func (o *Orchestrator) GetRarityContext(date time.Time) (RarityContext, error) {
	// Bind the range-filter service (present whenever the orchestrator is live; not
	// gated on BirdNET v2.4), then drive every read below from o.CurrentSettings() so
	// scores and labels come from one snapshot.
	rfs, ok := o.rangeFilterReady()
	if !ok || rfs == nil {
		// No anchor, so no scores: hand back the orchestrator's current snapshot.
		return RarityContext{Settings: o.CurrentSettings()}, nil
	}

	settings := o.CurrentSettings()
	// filterActive is decided inside probableSpecies, in the same locked section
	// that produced the scores: it is false whenever those scores are the synthetic
	// all-zero fallback (no range-filter backend loaded, OR no location configured).
	// Deriving it here rather than from a separate runtimeState() read closes a TOCTOU
	// where a concurrent unload between the two reads could pair filterActive=true with
	// synthetic zeros, and it also covers the no-location case a bare backend!=nil
	// check missed, so a caller never reports a synthetic zero as "very rare" (#3935).
	scores, geomodel, filterActive, err := rfs.probableSpecies(date, 0.0, settings)
	return RarityContext{
		Scores:   scores,
		Geomodel: geomodel,
		// ClassifierLabels is the backend's covered label space: v2.4's published labels on
		// a v2.4 install (byte-identical to the former settings.BirdNET.Labels read), the
		// participant union otherwise, and the loaded v2.4 instance's labels on the
		// post-construction retry path where the global snapshot never received them.
		ClassifierLabels: slices.Clone(rfs.loadState().coveredLabels),
		FilterActive:     filterActive,
		Settings:         settings,
	}, err
}
