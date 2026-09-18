// range_filter_service.go
//
// rangeFilterService owns the range-filter backend, its lifecycle, prediction,
// scoring, occurrence cache and coverage/status, decoupled from the primary
// classifier instance. This state previously lived on the primary *BirdNET instance
// (bn.rangeFilter, bn.speciesCache); moving it to an orchestrator-owned service is a
// behavior-preserving refactor:
// the backend is selected from the same settings, scored identically, and the
// occurrence cache keys are unchanged. See internal/classifier/range_filter.go for
// the shared scoring helpers (scoreProbableSpecies, addUserOverrideSpeciesScores) that
// this service's predict path and BuildRangeFilter both use.
//
// Concurrency model (the load-bearing part):
//   - state is an immutable *rangeFilterState published behind an atomic.Pointer.
//     Non-predicting readers (runtime state, coverage, the mapped view) load it
//     lock-free.
//   - mu is a LEAF mutex. It is held only across a backend prediction (the
//     ONNX/TFLite session is not goroutine-safe) and the reload pointer swap.
//     Nothing acquires o.mu, entry.mu or bn.mu while holding mu, so mu can never
//     participate in a lock inversion. Reload builds the new backend fully unlocked
//     against a classifier-label snapshot the caller took under o.mu.RLock and
//     released first, then takes mu only for the swap and the old-backend Close().
package classifier

import (
	"fmt"
	"maps"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	onnx "github.com/tphakala/birdnet-go/internal/inference/onnx"
	"github.com/tphakala/birdnet-go/internal/inference/tflite"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// debugFunc gates a formatted debug line the way BirdNET.Debug does, so relocated
// range-filter log lines stay byte-identical. It is nil-safe via the service's
// Debug helper.
type debugFunc = func(format string, v ...any)

// participantLabels pairs a range-filter-participating classifier's registry ID
// with a snapshot of its label set. The labels are captured under the model lock
// and the lock released before the snapshot is used, so a build that consumes them
// runs entirely unlocked (see Orchestrator.rangeFilterView).
type participantLabels struct {
	id     string
	labels []string
}

// rangeFilterView is the immutable snapshot the service needs to build a
// range-filter backend independent of which acoustic classifier is loaded. It
// supersedes classifierView's single-anchor identity: instead of one classifier,
// it carries every loaded classifier that participates in range filtering
// (ParticipatesInRangeFilter), so a Perch-only or v3.0-only install with the v3.0
// geomodel builds a real backend rather than an empty one.
//
// participants lead with BirdNET v2.4 (when loaded) and are otherwise byte-sorted
// by registry ID, mirroring orderedEntryRefs so label unions resolve
// deterministically regardless of Go's randomized map iteration. The caller
// snapshots each label set under the model locks and releases them before the
// build, keeping buildMetaModel fully unlocked.
type rangeFilterView struct {
	// modelsDir is the gallery models directory, for geomodel auto-selection.
	modelsDir string
	// participants is every loaded classifier that participates in range filtering,
	// with its label snapshot, in the order described above.
	participants []participantLabels
	// v24Labels is BirdNET v2.4's label set when v2.4 is loaded, else nil. It gates
	// the v2.4-specific backend paths (embedded/strict MData, arm64 ONNX default,
	// TFLite fallback), which are byte-identical to the classifierView era whenever
	// v2.4 is loaded.
	v24Labels []string
	// wantsGeomodel is true when any participant's label space fits the mapped
	// geomodel v3 backend (rangeFilterCompatGeomodel: Perch v2, BirdNET v3.0). A
	// v2.4-only install leaves this false and stays on MData.
	wantsGeomodel bool
}

// classifierMapping is one participant's geomodel coverage: its registry ID, the
// label snapshot the coverage was computed from, and how many of those labels the
// loaded geomodel backend can score (mapped) versus cannot (unmapped). It is the
// per-participant unit the status surface reports and, from Phase 4 PR A2 onward,
// the inclusion-list build consumes. When no geomodel backend is loaded, mapped and
// unmapped are both zero (coverage is not computed), matching the pre-view status
// behavior of leaving the counters at zero.
type classifierMapping struct {
	id       string
	labels   []string
	mapped   int
	unmapped int
}

// buildClassifierViews computes per-participant geomodel coverage for every
// classifier in view, against the loaded backend's geomodel label set (geoLabels).
// It reuses ComputeGeomodelCoverage, so a participant's counts are byte-identical to
// the per-model coverage the status surface computed inline before. geoLabels is the
// mappedRangeFilter's geomodelLabels, or nil/empty when the backend is not a mapped
// geomodel (legacy MData, strict ONNX, or none), in which case coverage stays zero.
func buildClassifierViews(view rangeFilterView, geoLabels []string) []classifierMapping {
	views := make([]classifierMapping, 0, len(view.participants))
	for _, p := range view.participants {
		cm := classifierMapping{id: p.id, labels: p.labels}
		if len(geoLabels) > 0 {
			cm.mapped, cm.unmapped = ComputeGeomodelCoverage(p.labels, geoLabels)
		}
		views = append(views, cm)
	}
	return views
}

// rangeFilterBackendKind names which range-filter backend a rangeFilterState holds.
// It is surfaced (as its string value) on the status and health surfaces so a mixed
// classifier set is honest about which backend, if any, is filtering.
type rangeFilterBackendKind string

const (
	// rfKindNone means no range-filter backend is loaded (no geomodel and no v2.4
	// MData): filtering is off and, with a location configured, health is Critical.
	rfKindNone rangeFilterBackendKind = "none"
	// rfKindGeomodelV3 is the universal mapped BirdNET geomodel v3, applied to every
	// participant regardless of which acoustic classifier is loaded.
	rfKindGeomodelV3 rangeFilterBackendKind = "geomodel_v3"
	// rfKindMDataV2 is the legacy BirdNET v2.4 MData range filter (embedded TFLite or
	// strict ONNX). It only works while v2.4 is loaded.
	rfKindMDataV2 rangeFilterBackendKind = "mdata_v2"
	// rfKindMDataV1 is the v1 legacy MData model (rangefilter.model=legacy), v2.4 only.
	rfKindMDataV1 rangeFilterBackendKind = "mdata_v1"
)

// coveredLabels is the classifier-side label space the range-filter backend maps
// onto: BirdNET v2.4's labels when v2.4 is loaded (so existing v2.4 installs stay
// byte-identical, and legacy MData, which only maps the 6522 v2.4 labels, always uses
// them), otherwise the union of the loaded participants' labels (so a Perch-only or
// v3.0-only install maps and fails open over its own species). Returns nil when no
// participant is loaded (N=0).
func (v rangeFilterView) coveredLabels(settings *conf.Settings) []string {
	if v.v24Labels != nil {
		// v2.4 loaded: use the PUBLISHED v2.4 label set (settings.BirdNET.Labels), not
		// the view's bn.Labels() clone. The two hold the same species but can differ in
		// order (bn.Settings vs the atomic snapshot), and the pre-decouple fail-open and
		// backfill paths indexed the published slice, so using it keeps the inclusion
		// list and the name-resolver dedup byte-identical (invariant I1).
		return settings.BirdNET.Labels
	}
	if len(v.participants) == 0 {
		return nil
	}
	sets := make([][]string, 0, len(v.participants))
	for _, p := range v.participants {
		sets = append(sets, p.labels)
	}
	return unionLabels(sets...)
}

// rangeFilterState is the immutable, atomically published snapshot of the loaded
// range-filter backend. A new one is built unlocked on reload and swapped under
// rfs.mu. A non-nil *rangeFilterState whose backend is nil represents "no range
// filter loaded", so readers never dereference a nil pointer.
type rangeFilterState struct {
	// backend is the active range-filter implementation: a *mappedRangeFilter for
	// the geomodel path, a plain ONNX range filter for the strict path, a TFLite
	// range filter for the legacy path, or nil when none is loaded. It is NOT
	// goroutine-safe and frees native resources in Close(); a prediction reads it
	// and runs the native call under rfs.mu, and reload Close()s the replaced
	// backend under rfs.mu, so a swap can never free a backend mid-prediction.
	backend inference.RangeFilter
	// fellBack is true when the configured ONNX geomodel could not be loaded and the
	// classifier fell back to its embedded TFLite range filter. Reported for health.
	fellBack bool
	// generation is a monotonic counter bumped on every backend swap, written only
	// under rfs.mu and read lock-free via loadState() elsewhere. It makes the
	// swap+cache-invalidate atomic to the occurrence-cache readers, which take
	// speciesCacheMu rather than rfs.mu: a speciesCacheEntry is tagged with the
	// generation it was computed under and served only while that generation is still
	// current, so an entry produced against a superseded backend is rejected the instant
	// the swap publishes a new generation, without waiting for clearSpeciesCache to run.
	generation uint64
	// kind names which range-filter backend this state holds (geomodel_v3, mdata_v2,
	// mdata_v1, or none). Surfaced on the status/health API so a mixed set is honest
	// about which backend is filtering, and drives the "install the geomodel" health
	// hint when no backend is loaded.
	kind rangeFilterBackendKind
	// coveredLabels is the classifier-side label space the backend maps onto: BirdNET
	// v2.4's labels when v2.4 is loaded (byte-identical to the pre-decouple behavior),
	// otherwise the union of the loaded participants' labels. The synthetic-zero
	// fail-open path and scoreProbableSpecies index this slice, so a Perch-only or
	// v3.0-only install fails open over its own species instead of an empty v2.4 list.
	coveredLabels []string
}

// rangeFilterService owns the range-filter backend and occurrence cache.
type rangeFilterService struct {
	// buildMu serializes reloads with each other. It is held across the whole
	// reload (the unlocked build AND the swap), because reload is reachable
	// concurrently: ReloadModel runs on the monitor goroutine while
	// ReloadForVariantSwap and ReloadRangeFilter run on API HTTP goroutines
	// (model install/uninstall/variant-swap). Serializing here replaces the bn.mu
	// hold that previously serialized every range-filter rebuild, without blocking
	// predictions (which take mu, not buildMu). Lock order: buildMu is taken before
	// mu during the swap and never the other way, and predictions never take buildMu,
	// so no inversion is possible.
	buildMu sync.Mutex
	// closed is set by close() under buildMu and checked by reload() under buildMu.
	// It prevents a reload that raced an orchestrator teardown (both run unlocked
	// relative to o.mu) from building a fresh native backend AFTER close() already
	// tore the service down, which would leak that backend's native session forever.
	closed bool
	// mu is a leaf lock held across a backend prediction and the reload swap; see
	// the file header. It is a plain Mutex, not an RWMutex: non-predicting readers
	// use state.Load() lock-free, so the only holders are prediction and reload.
	mu    sync.Mutex
	state atomic.Pointer[rangeFilterState]

	// speciesCache memoizes occurrence-index maps keyed by date+lat/lon+model, moved
	// verbatim from *BirdNET. Guarded by its own RWMutex, independent of mu.
	speciesCacheMu sync.RWMutex
	speciesCache   map[string]*speciesCacheEntry

	// debug gates debug log lines like BirdNET.Debug (on the globally published
	// settings). May be nil in tests; use the Debug helper.
	debug debugFunc
}

// newRangeFilterService returns a service with an empty (no-backend) state.
func newRangeFilterService(debug debugFunc) *rangeFilterService {
	rfs := &rangeFilterService{
		speciesCache: make(map[string]*speciesCacheEntry),
		debug:        debug,
	}
	rfs.state.Store(&rangeFilterState{})
	return rfs
}

// Debug mirrors BirdNET.Debug for relocated log lines.
func (rfs *rangeFilterService) Debug(format string, v ...any) {
	if rfs.debug != nil {
		rfs.debug(format, v...)
	}
}

// loadState returns the current immutable state (never nil).
func (rfs *rangeFilterService) loadState() *rangeFilterState {
	if s := rfs.state.Load(); s != nil {
		return s
	}
	return &rangeFilterState{}
}

// backendKind returns the loaded backend kind, mapping the zero value (an unpublished
// initial state or a torn-down service, whose kind is the empty string) to rfKindNone.
// The status/health surfaces and the participation log report this as the JSON "backend"
// field, whose contract is one of "geomodel_v3", "mdata_v2", "mdata_v1", or "none": the
// empty string is never a valid value, so it must be normalized here rather than leaked.
func (rfs *rangeFilterService) backendKind() rangeFilterBackendKind {
	if k := rfs.loadState().kind; k != "" {
		return k
	}
	return rfKindNone
}

// reload rebuilds the range-filter backend from settings and swaps it in
// transactionally. The new backend is built entirely UNLOCKED; rfs.mu is taken
// only for the atomic swap and to Close the replaced backend. On a build failure
// with a backend already loaded, nothing is swapped and the previous backend keeps
// serving (the rollback contract that BirdNET.ReloadRangeFilter provided); on a
// build failure with NO backend loaded, the fresh participant covered labels are
// published with kind none so the service fails open (see the error branch below).
// The species cache is cleared on a successful swap because the backend changed.
func (rfs *rangeFilterService) reload(settings *conf.Settings, viewFn func() rangeFilterView) error {
	// Serialize reloads: concurrent build+swap from the monitor and API goroutines
	// would otherwise race the atomic swap (late-writer-wins) and double-Close.
	rfs.buildMu.Lock()
	defer rfs.buildMu.Unlock()

	// If the service was already torn down (a reload racing an orchestrator Delete,
	// both of which run unlocked relative to o.mu), do not build a fresh backend: it
	// would never be closed and its native session would leak. close() sets this flag
	// under the same buildMu, so the check and the teardown are serialized.
	if rfs.closed {
		return nil
	}

	// Snapshot the participant topology INSIDE buildMu, not before it. If the caller
	// evaluated the view first, two reloads racing a model install against an uninstall
	// could enter buildMu in the opposite order to how they read the topology, and the
	// loser would publish a stale participant set. Reading the view here makes the
	// reload that wins buildMu last also the one that read the newest topology.
	//
	// Lock edge: viewFn (Orchestrator.rangeFilterView) takes o.mu.RLock then entry.mu
	// (releasing each before Labels()), so this adds buildMu -> o.mu -> entry.mu -> bn.mu
	// to the hierarchy. It is deadlock-free because NO path holds o.mu (or a lock above
	// it) while acquiring buildMu: every reload/close caller releases o.mu first
	// (NewOrchestrator and ReloadRangeFilter hold nothing; reloadEntry step 7 holds only
	// o.reloadMu; LoadModel/UnloadModel call in after releasing o.mu; Delete likewise).
	// Do not call reload or close while holding o.mu.
	view := viewFn()

	backend, kind, fellBack, err := buildMetaModel(settings, view, rfs.debug)
	if err != nil {
		// Rollback contract: a build failure keeps a previously-loaded backend serving, so
		// a transient reload error never tears down a working range filter. But when NO
		// backend is currently loaded (the initial build failed, or a prior build produced
		// none), the published state still carries the OLD covered-label space (nil on a
		// fresh service). The fail-open paths score over coveredLabels, so leaving it nil
		// would synthesize an EMPTY inclusion list and drop every detection on a
		// location-configured Perch-only or v3.0-only install: the exact fail-closed
		// regression this decoupling exists to prevent. Publish the fresh covered labels
		// with no backend (kind none) so the service fails OPEN over the loaded
		// participants, while still returning the error so the caller surfaces and logs the
		// build failure.
		rfs.mu.Lock()
		if cur := rfs.loadState(); cur.backend == nil {
			rfs.state.Store(&rangeFilterState{
				kind:          rfKindNone,
				coveredLabels: view.coveredLabels(settings),
				generation:    cur.generation + 1,
			})
			rfs.mu.Unlock()
			rfs.clearSpeciesCache()
		} else {
			rfs.mu.Unlock()
		}
		return errors.New(err).
			Component("classifier.rangefilter").
			Category(errors.CategoryModelInit).
			Context("operation", "reload_range_filter").
			Build()
	}
	covered := view.coveredLabels(settings)

	rfs.mu.Lock()
	old := rfs.loadState()
	// Bump the generation with the swap so cache entries computed against the old
	// backend become logically stale to readers the moment this store is published,
	// even before clearSpeciesCache runs below. Every generation write (this swap, the
	// no-backend fail-open publish above, close, and swapTestBackend) happens under
	// rfs.mu, so the increment cannot race another writer.
	newGen := old.generation + 1
	rfs.state.Store(&rangeFilterState{
		backend:       backend,
		kind:          kind,
		fellBack:      fellBack,
		coveredLabels: covered,
		generation:    newGen,
	})
	// Close the replaced backend under rfs.mu: every prediction is serialized by the
	// same lock, so none is using the old backend at close time (issue #3336). A full
	// rebuild always produces a distinct backend (or nil), so the pointer compare is
	// sufficient; there is no shared-session aliasing to guard against.
	if old.backend != nil && old.backend != backend {
		old.backend.Close()
	}
	rfs.mu.Unlock()

	rfs.clearSpeciesCache()
	rfs.Debug("range filter backend swapped; occurrence cache invalidated (generation %d, kind=%s, fell_back=%t)", newGen, kind, fellBack)
	return nil
}

// close releases the backend and clears the cache. Called on orchestrator teardown.
// Serialized against reload via buildMu so a teardown racing an in-flight reload
// cannot double-Close or leak a just-built backend.
func (rfs *rangeFilterService) close() {
	rfs.buildMu.Lock()
	defer rfs.buildMu.Unlock()

	// Mark closed under buildMu so a reload that raced this teardown aborts before
	// building instead of leaking a fresh native backend into a dead service.
	rfs.closed = true

	rfs.mu.Lock()
	old := rfs.loadState()
	// Bump the generation on teardown too, so any cache entry that outlives the clear
	// below is rejected by a concurrent reader on generation mismatch.
	rfs.state.Store(&rangeFilterState{generation: old.generation + 1})
	if old.backend != nil {
		old.backend.Close()
	}
	rfs.mu.Unlock()
	rfs.clearSpeciesCache()
}

// runtimeState reports whether a backend is loaded and whether it is the embedded
// TFLite fallback. Lock-free.
func (rfs *rangeFilterService) runtimeState() (active, fellBack bool) {
	s := rfs.loadState()
	return s.backend != nil, s.fellBack
}

// mappedView returns the active backend as a *mappedRangeFilter (the geomodel
// path), lock-free. ok is false when no geomodel range filter is loaded. Its
// build-time fields (geomodelLabels, geomodelIndex, vocab, classifierToGeo) are
// immutable after publication, so the lock-free callers here (GeomodelSpeciesInfo
// and the heatmap service) may read them without a lock; a concurrent reload swaps
// the whole state pointer rather than mutating those fields. The one exception is
// unmappedScore, which predict updates under rfs.mu on the BuildRangeFilter path; no
// lock-free caller reads it, so this accessor stays lock-free.
func (rfs *rangeFilterService) mappedView() (mrf *mappedRangeFilter, ok bool) {
	mrf, ok = rfs.loadState().backend.(*mappedRangeFilter)
	return mrf, ok
}

// lockedBatchFilter returns the mapped backend for batch inference together with a
// release func, holding rfs.mu across the caller's batch prediction because the
// backend session is not goroutine-safe. The caller MUST call the release func.
func (rfs *rangeFilterService) lockedBatchFilter() (*mappedRangeFilter, func(), error) {
	rfs.mu.Lock()
	backend := rfs.loadState().backend
	// Keep the two error cases distinct (as the former lockedMappedRangeFilter did):
	// "not loaded" for a nil backend versus "not batch-capable" for a non-mapped one,
	// so callers can tell a missing range filter from a legacy/strict one.
	if backend == nil {
		rfs.mu.Unlock()
		return nil, func() {}, errors.Newf("range filter not loaded").
			Component("classifier.rangefilter").
			Category(errors.CategoryValidation).
			Build()
	}
	mrf, ok := backend.(*mappedRangeFilter)
	if !ok {
		rfs.mu.Unlock()
		return nil, func() {}, errors.Newf("range filter does not support batch inference").
			Component("classifier.rangefilter").
			Category(errors.CategoryValidation).
			Build()
	}
	return mrf, rfs.mu.Unlock, nil
}

// clearSpeciesCache clears the occurrence cache. Called on backend change, model
// reload, node delete, or location update.
func (rfs *rangeFilterService) clearSpeciesCache() {
	rfs.speciesCacheMu.Lock()
	clear(rfs.speciesCache)
	rfs.speciesCacheMu.Unlock()
}

// probableSpecies filters and sorts species by their range-filter scores for the
// supplied settings snapshot. It is the service-owned port of the former
// BirdNET.getProbableSpecies: same universal/legacy split, same single-lock hold
// across the prediction triple, same synthetic-override tagging.
//
// The lock (rfs.mu) covers only the prediction triple: loading the backend, the
// nil/location checks, the UniversalSpeciesPredictor assertion and
// PredictSpeciesScores. It is released before the exclude/override/pass-unmapped
// scoring, exactly where BirdNET.getProbableSpecies released bn.mu, so scoring
// never blocks a reload.
//
// Returns (scores, geomodel vocabulary, filterActive, err). geomodel is non-nil
// only on the universal path; filterActive is false when the scores are the
// synthetic all-zero fallback (no backend, or no location), so a caller never
// reports a synthetic zero as "very rare" (#3935).
func (rfs *rangeFilterService) probableSpecies(date time.Time, week float32, settings *conf.Settings) (probableSpecies []SpeciesScore, geomodel *LabelVocabulary, filterActive bool, err error) {
	rfs.Debug("Applying range filter")

	// Build the exclude matcher once: it reverse-resolves localized common-name
	// exclude entries through OpenFauna a single time so the per-score matches()
	// below (and zeroScoresForAllLabels) stay off the dataset scan.
	excluder := newExcludeMatcher(settings.Realtime.Species.Exclude, settings.BirdNET.Locale)

	// coveredLabels is the classifier label space the loaded backend maps onto: v2.4's
	// labels on a v2.4 install (byte-identical to the former settings.BirdNET.Labels),
	// the loaded-participant union otherwise. The fail-open paths score over it so a
	// Perch-only or v3.0-only install fails open over its own species instead of the
	// empty v2.4 label set (which would drop every detection).
	covered := rfs.loadState().coveredLabels

	// Skip filtering if location is not configured. This reads only the settings
	// snapshot, so it needs no lock and is checked before acquiring rfs.mu. A nil
	// backend and an unconfigured location both return identical synthetic zero
	// scores, so the order between the two checks is not observable beyond which
	// debug line is logged when both hold.
	if !settings.BirdNET.LocationConfigured {
		rfs.Debug("Location not configured, not using location based prediction filter")
		return failOpenScores(covered, excluder, settings, rfs.debug), nil, false, nil
	}

	threshold := settings.BirdNET.RangeFilter.Threshold
	if threshold < 0 || threshold > 1 {
		GetLogger().Warn("Invalid LocationFilterThreshold value, using default",
			logger.Float64("invalid_value", float64(threshold)),
			logger.Float64("default_value", 0.01))
		threshold = 0.01
	}

	if week == 0 {
		week = getWeekForFilter(date)
	}

	// Run the shared universal-path prediction under a SINGLE lock hold (see predict).
	// syncUnmappedScore is false on the read path: it must not mutate the published
	// backend's batch-inference unmappedScore.
	res, predErr := rfs.predict(settings, week, threshold, false)
	switch res.kind {
	case predictNotLoaded:
		rfs.Debug("Range filter model not loaded, returning zero scores for all covered labels (fail open)")
		return failOpenScores(covered, excluder, settings, rfs.debug), nil, false, nil

	case predictLegacy:
		// Legacy path: map geomodel scores to the classifier's label set. predictFilter
		// re-acquires rfs.mu and re-checks nil itself, so a reload racing between
		// predict's unlock and that re-lock still returns a "range filter was closed
		// during prediction" error for the legacy TFLite backend. That residual race is
		// accepted: this path is not the default (the universal geomodel path is).
		filters, ferr := rfs.predictFilter(date, week, settings, threshold)
		if ferr != nil {
			return nil, nil, false, errors.New(ferr).
				Category(errors.CategoryValidation).
				Context("date", date.Format(time.DateOnly)).
				Context("week", week).
				Context("model", settings.BirdNET.RangeFilter.Model).
				Build()
		}

		var speciesScores []SpeciesScore
		for _, filter := range filters {
			if !excluder.matches(filter.Label) {
				speciesScores = append(speciesScores, SpeciesScore{Score: float64(filter.Score), Label: filter.Label})
			} else {
				rfs.Debug("Excluding species from range filter: %s", filter.Label)
			}
		}

		// Apply user overrides through the shared resolver so the legacy path
		// canonicalizes localized common names identically to the universal path. The
		// legacy path has no geomodel label set, so resolution falls to the classifier
		// labels and the reverse lookup.
		addUserOverrideSpeciesScores(rfs.debug, &speciesScores, settings, nil)

		sort.Sort(ByScore(speciesScores))
		return speciesScores, nil, true, nil

	case predictUniversal:
		if predErr != nil {
			return nil, nil, false, errors.New(predErr).
				Category(errors.CategoryValidation).
				Context("date", date.Format(time.DateOnly)).
				Context("week", week).
				Context("model", settings.BirdNET.RangeFilter.Model).
				Build()
		}

		// Shared scoring tail (exclude, user overrides, pass-unmapped backfill over
		// coveredLabels); the read path then sorts by score. The synthetic-override
		// tagging is applied here so the species endpoint and occurrence index see the
		// same rows as before.
		speciesScores, _ := scoreProbableSpecies(rfs.debug, excluder, res.scores, res.allGeoLabels, res.cachedMapping, res.coveredLabels, settings)
		sort.Sort(ByScore(speciesScores))
		return speciesScores, res.geomodel, true, nil
	}

	// Unreachable: predict returns only the three kinds handled above. The switch has no
	// default on purpose, so the exhaustive linter flags a new predictKind that lacks a
	// case here (failing golangci-lint in CI, not the Go compiler) rather than letting it
	// silently fall into the universal scoring path. Fail safe to "no filter" if it is
	// ever reached anyway.
	return failOpenScores(covered, excluder, settings, rfs.debug), nil, false, nil
}

// predictKind classifies the outcome of the shared locked prediction so each caller
// reproduces its own fallback: probableSpecies returns zero scores for
// predictNotLoaded, runs the legacy predictFilter path for predictLegacy, and scores
// the geomodel rows for predictUniversal; BuildRangeFilter treats anything but
// predictUniversal as its o.GetProbableSpecies fallback.
type predictKind int

const (
	// predictNotLoaded: no backend is loaded, or the location is unconfigured. It is the
	// zero value on purpose, so an unset predictResult{} degrades to this safe fallback
	// rather than to the scoring path. The location check is folded in here so
	// predictLegacy strictly means "a legacy backend with a configured location", the
	// only state that should run predictFilter.
	predictNotLoaded predictKind = iota
	// predictLegacy: a non-universal backend is loaded; the caller runs predictFilter.
	predictLegacy
	// predictUniversal: a universal (geomodel) backend produced the raw scores.
	predictUniversal
)

// predictResult carries the output of the shared locked prediction. kind selects the
// path each caller then takes; scores, allGeoLabels and cachedMapping are populated
// only for predictUniversal, and geomodel only when that prediction also succeeded.
type predictResult struct {
	kind          predictKind
	scores        []SpeciesScore
	allGeoLabels  []string
	cachedMapping []int
	// coveredLabels is the classifier label space the cachedMapping was built against
	// (the backend's mrf.classifierLabels: v2.4's labels when v2.4 is loaded, else the
	// participant union). scoreProbableSpecies indexes the PassUnmapped backfill over
	// this slice rather than settings.BirdNET.Labels, which is stale for a non-v2.4
	// install.
	coveredLabels []string
	geomodel      *LabelVocabulary
}

// predict runs the universal-geomodel prediction triple under a SINGLE rfs.mu hold
// (load the backend, classify it, read GeomodelLabels, capture the classifier->geomodel
// mapping, run PredictSpeciesScores) and returns with the lock released. It is the one
// locked path shared by the probableSpecies read path and the BuildRangeFilter
// rebuild; holding rfs.mu across PredictSpeciesScores is mandatory because the backend is not
// goroutine-safe and reload Close()s it under the same lock (#3935/#4298).
//
// week and threshold are resolved by the caller (each clamps threshold its own way and
// derives week from its date). syncUnmappedScore mirrors mappedRangeFilter.unmappedScore
// with the current PassUnmappedSpecies setting under the lock so the legacy Predict path
// (batch inference) sees the right value without a full reload; only BuildRangeFilter
// does this (as it did under bn.mu), so the read path passes false and never mutates the
// published backend. The returned geomodel vocabulary and cachedMapping are read from the
// backend captured under the lock but dereferenced after release, which is safe because
// those fields are immutable for the backend's lifetime and Close() frees only the inner
// session (mappedView relies on the same invariant).
func (rfs *rangeFilterService) predict(settings *conf.Settings, week, threshold float32, syncUnmappedScore bool) (predictResult, error) {
	rfs.mu.Lock()
	rf := rfs.loadState().backend
	if rf == nil || !settings.BirdNET.LocationConfigured {
		rfs.mu.Unlock()
		return predictResult{kind: predictNotLoaded}, nil
	}
	up, isUniversal := rf.(UniversalSpeciesPredictor)
	if !isUniversal {
		rfs.mu.Unlock()
		return predictResult{kind: predictLegacy}, nil
	}

	allGeoLabels := up.GeomodelLabels()
	// Sync unmappedScore with PassUnmappedSpecies and capture the pre-computed mapping
	// plus the classifier label space it was built against, so the caller can build the
	// unmapped set after the lock is released and index it over the right slice.
	var cachedMapping []int
	var coveredLabels []string
	if mrf, mok := rf.(*mappedRangeFilter); mok {
		if syncUnmappedScore {
			var score float32
			if settings.BirdNET.RangeFilter.PassUnmappedSpecies {
				score = 1.0
			}
			mrf.unmappedScore = score
		}
		cachedMapping = mrf.classifierToGeo
		coveredLabels = mrf.classifierLabels
	}
	scores, err := up.PredictSpeciesScores(
		float32(settings.BirdNET.Latitude),
		float32(settings.BirdNET.Longitude),
		week,
		threshold,
	)
	rfs.mu.Unlock()

	res := predictResult{
		kind:          predictUniversal,
		scores:        scores,
		allGeoLabels:  allGeoLabels,
		cachedMapping: cachedMapping,
		coveredLabels: coveredLabels,
	}
	// Wrap the geomodel vocabulary so the species endpoint answers coverage from a
	// precomputed canonical-key memo instead of an openfauna.CanonicalName scan per
	// label. Gate on a non-nil label slice so the vocabulary is nil exactly when the
	// raw labels were nil, preserving the "isUniversal := geomodel != nil" check at
	// every caller. Skipped on error, matching the pre-dedup read path. Only
	// *mappedRangeFilter implements UniversalSpeciesPredictor today, so this is the cheap
	// mrf.vocab field read; the NewLabelVocabulary fallback is defensive. If a non-mapped
	// universal backend is ever added, the BuildRangeFilter path (which discards geomodel)
	// should gate this so it does not pay for a full vocab build it never reads.
	if err == nil && allGeoLabels != nil {
		if mrf, mok := rf.(*mappedRangeFilter); mok {
			res.geomodel = mrf.vocab
		} else {
			res.geomodel = NewLabelVocabulary(allGeoLabels)
		}
	}
	return res, err
}

// predictFilter applies the legacy (non-universal) range-filter backend to predict
// species by location and date. Ported from BirdNET.predictFilter: it re-acquires
// rfs.mu and re-checks the backend (the caller released the lock before calling), so
// a reload racing in between returns a clean error instead of using a freed backend.
func (rfs *rangeFilterService) predictFilter(date time.Time, week float32, settings *conf.Settings, threshold float32) ([]Filter, error) {
	start := time.Now()

	if week == 0 {
		week = getWeekForFilter(date)
	}

	// Lock across the native call: the TFLite interpreter is not goroutine-safe.
	// Re-check nil under lock in case a reload raced between the caller's nil check
	// and this point.
	rfs.mu.Lock()
	backend := rfs.loadState().backend
	if backend == nil {
		rfs.mu.Unlock()
		return nil, fmt.Errorf("range filter was closed during prediction")
	}
	scores, err := backend.Predict(
		float32(settings.BirdNET.Latitude),
		float32(settings.BirdNET.Longitude),
		week,
	)
	rfs.mu.Unlock()

	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "range_filter").
			Context("latitude", settings.BirdNET.Latitude).
			Context("longitude", settings.BirdNET.Longitude).
			Context("week", week).
			Timing("range-filter-invoke", time.Since(start)).
			Build()
	}

	var results []Filter
	for i, score := range scores {
		if score >= threshold && i < len(settings.BirdNET.Labels) {
			results = append(results, Filter{Score: score, Label: settings.BirdNET.Labels[i]})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// getCachedSpeciesScores returns occurrence scores with per-day caching, ported
// verbatim from BirdNET.getCachedSpeciesScores (the cache lives on the service now).
// The caller supplies the settings snapshot so the cache key and the scored
// prediction use one consistent generation.
func (rfs *rangeFilterService) getCachedSpeciesScores(targetDate time.Time, settings *conf.Settings) (map[string]float64, error) {
	// Composite cache key: date + rounded lat/lon + model.
	day := targetDate.Format(time.DateOnly)
	cacheKey := fmt.Sprintf("%s|%.4f,%.4f|%s",
		day,
		settings.BirdNET.Latitude,
		settings.BirdNET.Longitude,
		settings.BirdNET.RangeFilter.Model,
	)

	// FAST PATH: read under RLock and return a defensive copy. Serve an entry only
	// while its generation matches the currently-published backend generation. reload
	// swaps the backend into a new *rangeFilterState (bumping generation) under rfs.mu,
	// then clears the cache OUTSIDE rfs.mu; cache readers take speciesCacheMu, not
	// rfs.mu, so widening the backend lock cannot exclude them. Tagging entries with
	// their generation and rejecting a mismatch closes the window between the swap and
	// the clear: an entry computed against the superseded backend carries the old
	// generation and is skipped the instant the swap publishes. Reading
	// the generation via loadState (an atomic load) gives the needed happens-before: a
	// reader that observes the new state also observes the new generation.
	curGen := rfs.loadState().generation
	rfs.speciesCacheMu.RLock()
	if entry, ok := rfs.speciesCache[cacheKey]; ok && entry.key == cacheKey && entry.generation == curGen {
		out := make(map[string]float64, len(entry.scores))
		maps.Copy(out, entry.scores)
		rfs.speciesCacheMu.RUnlock()
		return out, nil
	}
	rfs.speciesCacheMu.RUnlock()

	// MISS PATH: use the same settings snapshot as the cache key. Sample the generation
	// just before predicting so the write path can detect a backend swap that raced the
	// prediction.
	genBefore := rfs.loadState().generation
	speciesScores, _, _, err := rfs.probableSpecies(targetDate, 0.0, settings)
	if err != nil {
		return nil, err
	}
	scores := buildOccurrenceIndex(speciesScores)

	// WRITE PATH: double-check, evict old entries, publish new results.
	rfs.speciesCacheMu.Lock()
	genNow := rfs.loadState().generation
	if entry, ok := rfs.speciesCache[cacheKey]; ok && entry.key == cacheKey && entry.generation == genNow {
		out := make(map[string]float64, len(entry.scores))
		maps.Copy(out, entry.scores)
		rfs.speciesCacheMu.Unlock()
		return out, nil
	}
	// Cache only when no reload swapped the backend across the prediction. generation is
	// monotonic and written only under rfs.mu, so genBefore == genNow proves
	// probableSpecies scored against exactly the genNow backend and the entry is safe to
	// tag with genNow. If a reload intervened, the fresh scores may reflect a superseded
	// backend, so hand them to this caller without caching (they are never served again).
	if genBefore == genNow {
		// Keep cache bounded: evict one arbitrary entry when limit is reached. Each key
		// encodes date+lat+lon+model, so a small limit is sufficient.
		const maxSpeciesCacheEntries = 8
		if len(rfs.speciesCache) >= maxSpeciesCacheEntries {
			for k := range rfs.speciesCache {
				delete(rfs.speciesCache, k)
				break
			}
		}
		rfs.speciesCache[cacheKey] = &speciesCacheEntry{
			key:        cacheKey,
			scores:     scores,
			generation: genNow,
		}
	}
	out := make(map[string]float64, len(scores))
	maps.Copy(out, scores)
	rfs.speciesCacheMu.Unlock()
	return out, nil
}

// occurrenceAtTime returns the occurrence probability for a species at a specific
// time, ported from BirdNET.GetSpeciesOccurrenceAtTime. The caller supplies the
// settings snapshot (the orchestrator's current settings).
func (rfs *rangeFilterService) occurrenceAtTime(species string, detectionTime time.Time, settings *conf.Settings) float64 {
	// Fast-path: no backend loaded, occurrence is 0. Lock-free read.
	if rfs.loadState().backend == nil {
		return 0.0
	}

	// If location not configured, the range filter is not active.
	if !settings.BirdNET.LocationConfigured {
		return 0.0
	}

	// Try cached scores first.
	cachedScores, err := rfs.getCachedSpeciesScores(detectionTime, settings)
	if err == nil && len(cachedScores) > 0 {
		if occurrence, found := lookupOccurrence(cachedScores, species); found {
			return clampOccurrence(occurrence)
		}
	}

	// Fallback on cache miss. Anchor to the local calendar day (matching
	// getCachedSpeciesScores, which keys on the local DateOnly of detectionTime).
	day := conf.LocalNoon(detectionTime)
	speciesScores, _, _, err := rfs.probableSpecies(day, 0.0, settings)
	if err != nil {
		rfs.Debug("Error getting probable species for occurrence: %v", err)
		return 0.0
	}

	// Resolve through the same index the cache uses, so a cache miss cannot answer
	// differently from a cache hit for the same species.
	if occurrence, found := lookupOccurrence(buildOccurrenceIndex(speciesScores), species); found {
		return clampOccurrence(occurrence)
	}

	return 0.0
}

// ----- backend builders (moved from birdnet.go / model_onnx.go, converted from
// mutate-in-place methods to build-and-return functions so reload can build the new
// backend fully unlocked and swap it in atomically) -----

// buildMetaModel selects and builds the range-filter backend for the resolved
// settings, returning the backend and whether it is the embedded TFLite fallback.
// It is the build-and-return port of BirdNET.initializeMetaModel: routing and
// auto-selection are unchanged; the only difference is it returns the backend
// instead of assigning bn.rangeFilter, and closes any partial backend on failure so
// it never leaks.
func buildMetaModel(settings *conf.Settings, view rangeFilterView, debug debugFunc) (backend inference.RangeFilter, kind rangeFilterBackendKind, fellBack bool, err error) {
	log := GetLogger()
	rf := settings.BirdNET.RangeFilter
	covered := view.coveredLabels(settings)
	hasV24 := view.v24Labels != nil

	log.Info("Initializing range filter",
		logger.String("model", rf.Model),
		logger.String("model_path", rf.ModelPath),
		logger.String("labels_path", rf.LabelsPath),
		logger.Int("participants", len(view.participants)),
		logger.Bool("v24_loaded", hasV24),
		logger.String("models_dir", view.modelsDir))

	// Rule 1: auto-select the shared v3 geomodel when any loaded participant fits it
	// (view.wantsGeomodel) and the stock files are present, on an auto-select config
	// with no explicit path. Keyed on the participant set instead of a single
	// classifier's compat, so a Perch/v3.0 set auto-selects the geomodel while a
	// v2.4-only set (wantsGeomodel=false) stays on MData exactly as before.
	if isAutoSelectRangeFilterModel(rf.Model) && rf.ModelPath == "" && view.wantsGeomodel &&
		geomodelFilesPresent(view.modelsDir) {
		localSettings := conf.CloneSettings(settings)
		applyAutoSelectedGeomodelPaths(localSettings, view.modelsDir)
		settings = localSettings
		rf = settings.BirdNET.RangeFilter
		log.Info("Auto-selected v3.0 geomodel for the loaded participant set",
			logger.String("models_dir", view.modelsDir))
	}

	// Rule 2: on arm64, prefer the ONNX MData range filter as the auto-select default.
	// Gated on v2.4 being loaded (view.v24Labels != nil): MData maps only the v2.4
	// label set, so this is identical to the former cv.id == v2.4 test. Passing the
	// v2.4 registry ID reuses the existing helper's compat check unchanged.
	if hasV24 {
		if path, ok := shouldSelectDefaultONNXRangeFilter(rf.Model, rf.ModelPath, RegistryIDBirdNETV24, runtime.GOARCH, findModelPathInStandardPaths); ok {
			localSettings := conf.CloneSettings(settings)
			localSettings.BirdNET.RangeFilter.ModelPath = path
			settings = localSettings
			rf = settings.BirdNET.RangeFilter
			log.Info("Selected ONNX range filter (arm64 default)",
				logger.String("model_path", path))
		}
	}

	switch resolveRangeFilterBackend(&rf) {
	case rangeFilterBackendMappedGeomodel:
		// Rule 3: the mapped geomodel remaps onto coveredLabels for any participant set,
		// regardless of whether v2.4 is loaded.
		b, onnxErr := buildONNXMetaModel(settings, covered)
		if onnxErr != nil {
			// Rule 5: fall back to the embedded v2.4 MData filter only when v2.4 is loaded
			// and a TFLite backend exists. Otherwise there is nothing to fall back to, so
			// report no backend (fail open over coveredLabels) rather than dropping every
			// detection; a genuine build failure still surfaces via the fallback's error.
			if hasV24 && tfliteBackendAvailable {
				fb, fbErr := fallbackToEmbeddedRangeFilter(settings, onnxErr, debug)
				if fbErr != nil {
					return nil, rfKindNone, false, fbErr
				}
				return fb, rfKindMDataV2, true, nil
			}
			// A CONFIGURED geomodel that fails to load is a genuine build failure, not a
			// "no backend for this config" state: return the error so reload keeps the
			// previously-working backend (its documented rollback contract) and the
			// settings-save API surfaces the failure, instead of silently swapping in a
			// nil backend. The "no geomodel configured at all" case never reaches this
			// switch arm (it resolves to the default MData path below).
			return nil, rfKindNone, false, onnxErr
		}
		return b, rfKindGeomodelV3, false, nil

	case rangeFilterBackendONNXStrict:
		// Rule 4: strict ONNX MData's output dimension must match the v2.4 label count,
		// so it requires v2.4. Without v2.4 there is no valid backend: report none (fail
		// open), not an error.
		if !hasV24 {
			log.Info("Strict ONNX MData range filter requires BirdNET v2.4, which is not loaded; range filtering off (install the BirdNET geomodel v3.0 for non-v2.4 classifiers)")
			return nil, rfKindNone, false, nil
		}
		b, onnxErr := buildONNXMetaModel(settings, covered)
		if onnxErr != nil {
			if tfliteBackendAvailable {
				fb, fbErr := fallbackToEmbeddedRangeFilter(settings, onnxErr, debug)
				if fbErr != nil {
					return nil, rfKindNone, false, fbErr
				}
				return fb, rfKindMDataV2, true, nil
			}
			return nil, rfKindNone, false, onnxErr
		}
		return b, rfKindMDataV2, false, nil

	default:
		// Rule 4: embedded/legacy TFLite MData maps only the v2.4 label set, so it
		// requires v2.4. Without v2.4 report no backend (fail open), not an error.
		if !hasV24 {
			log.Info("MData range filter requires BirdNET v2.4, which is not loaded; range filtering off (install the BirdNET geomodel v3.0 for non-v2.4 classifiers)")
			return nil, rfKindNone, false, nil
		}
		b, tfErr := buildTFLiteMetaModel(settings, debug)
		if tfErr != nil {
			return nil, rfKindNone, false, tfErr
		}
		if rf.Model == conf.RangeFilterModelLegacy {
			return b, rfKindMDataV1, false, nil
		}
		return b, rfKindMDataV2, false, nil
	}
}

// fallbackToEmbeddedRangeFilter recovers from an ONNX geomodel load failure by building
// the embedded BirdNET v2.4 MData (TFLite) range filter. The per-classifier gate was
// hoisted to the caller (buildMetaModel only calls this when v2.4 is loaded and a TFLite
// backend is available), so this no longer re-checks the classifier or propagates the
// original error itself.
func fallbackToEmbeddedRangeFilter(settings *conf.Settings, cause error, debug debugFunc) (inference.RangeFilter, error) {
	// The caller gates this on BirdNET v2.4 being loaded and a TFLite backend being
	// available (view.v24Labels != nil && tfliteBackendAvailable), so the embedded v2.4
	// MData filter is a valid fallback here; there is no longer a per-classifier gate.
	GetLogger().Warn("Range filter (ONNX geomodel) failed to load; falling back to the embedded BirdNET v2.4 MData range filter",
		logger.Error(cause))

	// getMetaModelData honors a non-empty rangefilter.modelpath first and would
	// re-read the failing geomodel file, so force the embedded MData model via empty
	// paths.
	fallbackSettings := conf.CloneSettings(settings)
	fallbackSettings.BirdNET.RangeFilter.Model = ""
	fallbackSettings.BirdNET.RangeFilter.ModelPath = ""
	fallbackSettings.BirdNET.RangeFilter.LabelsPath = ""

	b, tfErr := buildTFLiteMetaModel(fallbackSettings, debug)
	if tfErr != nil {
		// Join the original ONNX geomodel failure so reload and settings-save callers can
		// still inspect why the geomodel load failed, not just why the embedded fallback
		// did. cause is logged above but must also survive on the returned error.
		return nil, errors.New(errors.Join(cause, tfErr)).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			Context("operation", "range_filter_embedded_fallback").
			Build()
	}

	return b, nil
}

// hasNativeRangeFilter reports whether the classifier can fall back to the embedded
// MData range filter (BirdNET v2.4 family only). Build-and-return port keyed on the
// registry ID rather than a *BirdNET receiver.
//
// Kept keyed on the v2.4 family verbatim through Phase 3. Phase 4 (per-classifier
// range-filter views) owns the decision to ungate the MData fallback for other
// classifiers; do not change the gate before then.
func hasNativeRangeFilter(classifierID string) bool {
	// ONNX-only builds (notflite) have no embedded TFLite range filter to fall back to.
	return tfliteBackendAvailable && rangeFilterCompatFor(classifierID) == rangeFilterCompatMDataV24
}

// getMetaModelData returns the range-filter model bytes for the settings. Free
// function port of BirdNET.getMetaModelData (it never touched bn state beyond Debug).
func getMetaModelData(settings *conf.Settings, debug debugFunc) ([]byte, error) {
	rf := settings.BirdNET.RangeFilter

	// External model path.
	if rf.ModelPath != "" {
		modelPath := rf.ModelPath
		modelPath = os.ExpandEnv(modelPath)
		modelPath, err := conf.ExpandTildePath(modelPath)
		if err != nil {
			return nil, errors.New(err).
				Category(errors.CategoryFileIO).
				Context("path", rf.ModelPath).
				Build()
		}

		data, err := os.ReadFile(modelPath) //nolint:gosec // G304: modelPath is from application settings
		if err != nil {
			return nil, errors.New(err).
				Category(errors.CategoryFileIO).
				Context("path", modelPath).
				Context("range_filter_model", rf.Model).
				Build()
		}

		GetLogger().Info("Loaded range filter model", logger.String("path", modelPath))
		return data, nil
	}

	// No model path specified, try standard paths first (for noembed builds).
	if !hasEmbeddedModels {
		modelFileName := DefaultRangeFilterV2ModelName
		if rf.Model == "legacy" {
			modelFileName = DefaultRangeFilterV1ModelName
			GetLogger().Warn("Looking for legacy range filter model")
		}

		data, path, err := tryLoadModelFromStandardPaths(modelFileName, "range filter")
		if err != nil {
			return nil, errors.Wrap(err).
				Context("range_filter_model", rf.Model).
				Build()
		}
		GetLogger().Info("Loaded range filter model from standard path", logger.String("path", path))
		if debug != nil {
			debug("Loaded range filter model from standard path: %s", path)
		}
		return data, nil
	}

	// Fall back to embedded models.
	var data []byte
	if rf.Model == "legacy" {
		GetLogger().Warn("Using legacy range filter model")
		data = metaModelDataV1
	} else {
		data = metaModelDataV2
	}

	if data == nil {
		return nil, errors.Newf("range filter model not available: embedded model is nil").
			Category(errors.CategoryModelLoad).
			Context("embedded_models", hasEmbeddedModels).
			Context("range_filter_model", rf.Model).
			Build()
	}

	return data, nil
}

// buildTFLiteMetaModel builds a TFLite range-filter backend. Build-and-return port
// of BirdNET.initializeTFLiteMetaModel.
func buildTFLiteMetaModel(settings *conf.Settings, debug debugFunc) (inference.RangeFilter, error) {
	start := time.Now()
	log := GetLogger()

	metaModelData, err := getMetaModelData(settings, debug)
	if err != nil {
		return nil, err
	}

	rangeFilter, err := tflite.NewTFLiteRangeFilter(metaModelData, func(msg string) {
		log.Error("TFLite meta model error", logger.String("message", msg))
	})
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "range_filter").
			Context("range_filter_model", settings.BirdNET.RangeFilter.Model).
			Timing("meta-model-init", time.Since(start)).
			Build()
	}

	log.Info("TFLite range filter initialized",
		logger.Int("species", rangeFilter.NumSpecies()),
		logger.String("duration", time.Since(start).String()))

	return rangeFilter, nil
}

// buildONNXMetaModel builds an ONNX range-filter backend. A geomodel-shaped config
// (model=="v3" or an ONNX model path paired with a companion labels file) delegates
// to buildMappedGeoModel; an ONNX model path without a companion labels file uses the
// strict path, where the model output dimension must match the classifier label
// count. Build-and-return port of BirdNET.initializeONNXMetaModel.
func buildONNXMetaModel(settings *conf.Settings, coveredLabels []string) (inference.RangeFilter, error) {
	start := time.Now()
	rf := settings.BirdNET.RangeFilter
	mapped := resolveRangeFilterBackend(&rf) == rangeFilterBackendMappedGeomodel

	modelName := "ONNX range filter"
	modelCtx := "range_filter"
	switch {
	case rf.Model == conf.RangeFilterModelV3:
		modelName = "v3 geomodel"
		modelCtx = "v3_geomodel"
	case mapped:
		modelName = "geomodel range filter"
		modelCtx = "geomodel"
	}
	if err := checkORTOrFail(settings.BirdNET.ONNXRuntimePath, modelName, modelCtx, ""); err != nil {
		return nil, err
	}

	if mapped {
		return buildMappedGeoModel(settings, coveredLabels)
	}

	// Strict path: no companion labels file, so the classifier labels (coveredLabels,
	// the v2.4 label set on this path since strict MData requires v2.4) must match the
	// model output dimension one-to-one.
	if err := inference.InitONNXRuntime(settings.BirdNET.ONNXRuntimePath); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", settings.BirdNET.ONNXRuntimePath).
			Timing("onnx-init", time.Since(start)).
			Build()
	}

	rangeFilter, err := inference.NewONNXRangeFilter(
		rf.ModelPath,
		inference.ONNXRangeFilterOptions{
			Labels: coveredLabels,
		},
	)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "range_filter").
			Context("range_filter_model", rf.ModelPath).
			Timing("onnx-meta-model-init", time.Since(start)).
			Build()
	}

	return rangeFilter, nil
}

// buildMappedGeoModel loads the geomodel ONNX with its own labels and wraps the raw
// ONNX range filter in a mappedRangeFilter that remaps geomodel scores onto the
// classifier's label order by scientific name. Build-and-return port of
// BirdNET.initializeMappedGeoModel; classifier labels come from the settings snapshot.
func buildMappedGeoModel(settings *conf.Settings, coveredLabels []string) (inference.RangeFilter, error) {
	start := time.Now()
	log := GetLogger()
	rfSettings := settings.BirdNET.RangeFilter

	log.Info("Geomodel range filter: starting initialization",
		logger.String("model", rfSettings.Model),
		logger.String("model_path", rfSettings.ModelPath),
		logger.String("labels_path", rfSettings.LabelsPath),
		logger.Int("classifier_labels", len(coveredLabels)))

	if rfSettings.ModelPath == "" {
		return nil, errors.Newf("geomodel range filter requires rangefilter.modelpath to be set").
			Category(errors.CategoryModelInit).
			Context("model_type", "geomodel").
			Build()
	}
	if rfSettings.LabelsPath == "" {
		return nil, errors.Newf("geomodel range filter requires rangefilter.labelspath to be set").
			Category(errors.CategoryModelInit).
			Context("model_type", "geomodel").
			Build()
	}

	// Expand environment variables and ~ prefix in paths (consistent with getMetaModelData)
	modelPath := os.ExpandEnv(rfSettings.ModelPath)
	modelPath, err := conf.ExpandTildePath(modelPath)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryFileIO).
			Context("path", rfSettings.ModelPath).
			Build()
	}

	labelsPath := os.ExpandEnv(rfSettings.LabelsPath)
	labelsPath, err = conf.ExpandTildePath(labelsPath)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryFileIO).
			Context("path", rfSettings.LabelsPath).
			Build()
	}

	// Ensure ONNX Runtime is initialized (ORT availability checked by buildONNXMetaModel)
	log.Debug("Geomodel range filter: initializing ONNX Runtime")
	if err := inference.InitONNXRuntime(settings.BirdNET.ONNXRuntimePath); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", settings.BirdNET.ONNXRuntimePath).
			Timing("onnx-init", time.Since(start)).
			Build()
	}

	// Load geomodel labels from file
	log.Debug("Geomodel range filter: loading labels", logger.String("path", labelsPath))
	geoLabels, err := onnx.LoadLabels(labelsPath)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "geomodel").
			Context("labels_path", labelsPath).
			Build()
	}

	if len(geoLabels) == 0 {
		return nil, errors.Newf("geomodel labels file is empty: %s", labelsPath).
			Category(errors.CategoryModelInit).
			Context("model_type", "geomodel").
			Build()
	}

	log.Debug("Geomodel range filter: loaded labels",
		logger.Int("count", len(geoLabels)),
		logger.String("first", geoLabels[0]))

	// Create ONNX range filter using the geomodel's own labels
	log.Debug("Geomodel range filter: creating ONNX range filter", logger.String("model_path", modelPath))
	innerFilter, err := inference.NewONNXRangeFilter(
		modelPath,
		inference.ONNXRangeFilterOptions{
			Labels: geoLabels,
		},
	)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "geomodel").
			Context("range_filter_model", modelPath).
			Timing("onnx-geomodel-init", time.Since(start)).
			Build()
	}

	// Map onto coveredLabels: BirdNET v2.4's labels when v2.4 is loaded (byte-identical
	// to the pre-decouple mapping), otherwise the union of the loaded participants'
	// labels, so the geomodel remaps onto a Perch-only or v3.0-only classifier set too.
	classifierLabels := coveredLabels
	var unmappedScore float32
	if rfSettings.PassUnmappedSpecies {
		unmappedScore = 1.0
	}
	mapped := newMappedRangeFilter(innerFilter, classifierLabels, geoLabels, unmappedScore)

	if mapped.mappedCount == 0 && len(classifierLabels) > 0 {
		log.Warn("Geomodel range filter: no species matched classifier labels, range filter will filter out all detections (check labels file)",
			logger.Int("classifier_species", len(classifierLabels)),
			logger.String("labels_path", labelsPath))
	}
	log.Info("Geomodel range filter initialized with species mapping",
		logger.Int("geomodel_species", len(geoLabels)),
		logger.Int("classifier_species", len(classifierLabels)),
		logger.Int("mapped_species", mapped.mappedCount),
		logger.Int("unmapped_species", len(classifierLabels)-mapped.mappedCount),
		logger.String("duration", time.Since(start).String()))

	return mapped, nil
}
