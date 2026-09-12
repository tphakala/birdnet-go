// range_filter_service.go
//
// rangeFilterService owns the range-filter backend, its lifecycle, prediction,
// scoring, occurrence cache and coverage/status, decoupled from the primary
// classifier instance. Before Phase 2b of the model de-privilege epic this state
// lived on the privileged primary *BirdNET (bn.rangeFilter, bn.speciesCache);
// moving it to an orchestrator-owned service is a behavior-preserving refactor:
// the backend is selected from the same settings, scored identically, and the
// occurrence cache keys are unchanged. See internal/classifier/range_filter.go for
// the shared scoring helpers (BuildRangeFilter, addUserOverrideSpeciesScores) that
// this service reuses.
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

// classifierView is the immutable snapshot of the classifier identity the service
// needs to build a range-filter backend: the registry ID (for the native-fallback
// and auto-select gates) and the gallery models directory (for geomodel
// auto-selection). The classifier labels themselves are read from the settings
// snapshot passed to reload, which carries the loaded label set. The caller
// snapshots these under o.mu.RLock and releases the lock before calling reload, so
// the build runs entirely unlocked.
type classifierView struct {
	id        string
	modelsDir string
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
}

// rangeFilterService owns the range-filter backend and occurrence cache.
type rangeFilterService struct {
	// buildMu serializes reloads with each other. It is held across the whole
	// reload (the unlocked build AND the swap), because reload is reachable
	// concurrently: ReloadModel runs on the monitor goroutine while
	// ReloadPrimaryForVariantSwap and ReloadRangeFilter run on API HTTP goroutines
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

// reload rebuilds the range-filter backend from settings and swaps it in
// transactionally. The new backend is built entirely UNLOCKED; rfs.mu is taken
// only for the atomic swap and to Close the replaced backend. On a build failure
// nothing is swapped and the previous backend keeps serving (the rollback contract
// that BirdNET.ReloadRangeFilter provided). The species cache is cleared on a
// successful swap because the backend changed.
func (rfs *rangeFilterService) reload(settings *conf.Settings, cv classifierView) error {
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

	backend, fellBack, err := buildMetaModel(settings, cv, rfs.debug)
	if err != nil {
		return errors.New(err).
			Component("classifier.rangefilter").
			Category(errors.CategoryModelInit).
			Context("operation", "reload_range_filter").
			Build()
	}

	rfs.mu.Lock()
	old := rfs.loadState()
	rfs.state.Store(&rangeFilterState{backend: backend, fellBack: fellBack})
	// Close the replaced backend under rfs.mu: every prediction is serialized by the
	// same lock, so none is using the old backend at close time (issue #3336).
	if old.backend != nil && old.backend != backend {
		old.backend.Close()
	}
	rfs.mu.Unlock()

	rfs.clearSpeciesCache()
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
	rfs.state.Store(&rangeFilterState{})
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
// path), lock-free. ok is false when no geomodel range filter is loaded. The
// returned pointer is immutable after publication, so callers may read its fields
// without a lock; a concurrent reload only swaps the state pointer, it never
// mutates a published mappedRangeFilter.
func (rfs *rangeFilterService) mappedView() (mrf *mappedRangeFilter, ok bool) {
	mrf, ok = rfs.loadState().backend.(*mappedRangeFilter)
	return mrf, ok
}

// lockedBatchFilter returns the mapped backend for batch inference together with a
// release func, holding rfs.mu across the caller's batch prediction because the
// backend session is not goroutine-safe. The caller MUST call the release func.
func (rfs *rangeFilterService) lockedBatchFilter() (*mappedRangeFilter, func(), error) {
	rfs.mu.Lock()
	mrf, ok := rfs.loadState().backend.(*mappedRangeFilter)
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

	// Skip filtering if location is not configured. This reads only the settings
	// snapshot, so it needs no lock and is checked before acquiring rfs.mu. A nil
	// backend and an unconfigured location both return identical synthetic zero
	// scores, so the order between the two checks is not observable beyond which
	// debug line is logged when both hold.
	if !settings.BirdNET.LocationConfigured {
		rfs.Debug("Location not configured, not using location based prediction filter")
		return zeroScoresForAllLabels(settings.BirdNET.Labels, excluder), nil, false, nil
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

	// Resolve the backend and run the universal-path prediction under a SINGLE lock
	// hold: the nil check and the UniversalSpeciesPredictor assertion must observe
	// the same backend instance. Holding rfs.mu across PredictSpeciesScores is
	// mandatory: the backend is not goroutine-safe, and reload Close()s it under the
	// same lock, so it cannot free the backend mid-prediction (#3935 follow-up).
	rfs.mu.Lock()
	rf := rfs.loadState().backend
	if rf == nil {
		rfs.mu.Unlock()
		rfs.Debug("Range filter model not loaded, returning zero scores for all labels")
		return zeroScoresForAllLabels(settings.BirdNET.Labels, excluder), nil, false, nil
	}

	// Universal geomodel path first: predict from the geomodel's own label set so
	// all ~12K species are covered.
	if up, isUniversal := rf.(UniversalSpeciesPredictor); isUniversal {
		allGeoLabels := up.GeomodelLabels()
		var cachedMapping []int
		if mrf, ok := rf.(*mappedRangeFilter); ok {
			cachedMapping = mrf.classifierToGeo
		}
		scores, predErr := up.PredictSpeciesScores(
			float32(settings.BirdNET.Latitude),
			float32(settings.BirdNET.Longitude),
			week,
			threshold,
		)
		rfs.mu.Unlock()

		if predErr != nil {
			return nil, nil, false, errors.New(predErr).
				Category(errors.CategoryValidation).
				Context("date", date.Format(time.DateOnly)).
				Context("week", week).
				Context("model", settings.BirdNET.RangeFilter.Model).
				Build()
		}

		speciesScores := make([]SpeciesScore, 0, len(scores))
		for _, ss := range scores {
			if !excluder.matches(ss.Label) {
				speciesScores = append(speciesScores, ss)
			}
		}

		addUserOverrideSpeciesScores(rfs.debug, &speciesScores, settings, allGeoLabels)

		if settings.BirdNET.RangeFilter.PassUnmappedSpecies {
			seen := make(map[string]bool, len(speciesScores))
			for _, ss := range speciesScores {
				seen[ss.Label] = true
			}
			mapping := cachedMapping
			if mapping == nil {
				mapping = buildSpeciesMapping(settings.BirdNET.Labels, allGeoLabels)
			}
			// cachedMapping (mappedRangeFilter.classifierToGeo) is sized from the
			// model's labels at load time and can be longer than the live settings
			// snapshot during a concurrent model/settings reload, so bounds-check the
			// snapshot index before reading it.
			for i, geoIdx := range mapping {
				if geoIdx == -1 && i < len(settings.BirdNET.Labels) {
					label := settings.BirdNET.Labels[i]
					if !seen[label] && !excluder.matches(label) {
						speciesScores = append(speciesScores, SpeciesScore{Score: 0.0, Label: label})
						seen[label] = true
					}
				}
			}
		}

		sort.Sort(ByScore(speciesScores))
		// Wrap the geomodel vocabulary in a *LabelVocabulary (the named return) so the
		// species endpoint answers coverage from a precomputed canonical-key memo
		// instead of an openfauna.CanonicalName scan per label. Gate on a non-nil
		// label slice so the returned vocabulary is nil exactly when the raw labels
		// were nil, preserving the "isUniversal := geomodel != nil" check at every
		// caller.
		if allGeoLabels != nil {
			if mrf, ok := rf.(*mappedRangeFilter); ok {
				geomodel = mrf.vocab
			} else {
				geomodel = NewLabelVocabulary(allGeoLabels)
			}
		}
		return speciesScores, geomodel, true, nil
	}
	rfs.mu.Unlock()

	// Legacy path: map geomodel scores to the classifier's label set. predictFilter
	// re-acquires rfs.mu and re-checks nil itself, so a reload racing between this
	// unlock and that re-lock still returns a "range filter was closed during
	// prediction" error for the legacy TFLite backend. That residual race is
	// accepted: this path is not the default (the universal geomodel path above is).
	filters, predErr := rfs.predictFilter(date, week, settings, threshold)
	if predErr != nil {
		return nil, nil, false, errors.New(predErr).
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
}

// universalPredict runs the universal (geomodel) backend prediction under a single
// rfs.mu hold and returns the raw geomodel scores, the full geomodel label set and
// the cached classifier->geomodel mapping. ok is false (and the other returns are
// zero) when no universal backend is loaded or the location is unconfigured, in
// which case the caller uses its legacy fallback. It is the service-owned port of
// the locked section of BuildRangeFilter (range_filter.go), kept separate from
// probableSpecies in Phase 2b; deduplicating the two is Forgejo #1666 (PR 3).
//
// Like the original, it syncs mappedRangeFilter.unmappedScore with the current
// PassUnmappedSpecies setting under the lock so the legacy Predict path sees the
// right value without a full reload; this mutates the published backend, but only
// under rfs.mu, exactly as BuildRangeFilter mutated it under bn.mu.
func (rfs *rangeFilterService) universalPredict(settings *conf.Settings, date time.Time, threshold float32) (scores []SpeciesScore, allGeoLabels []string, cachedMapping []int, ok bool, err error) {
	rfs.mu.Lock()
	rf := rfs.loadState().backend
	up, isUniversal := rf.(UniversalSpeciesPredictor)
	if !isUniversal || !settings.BirdNET.LocationConfigured {
		rfs.mu.Unlock()
		return nil, nil, nil, false, nil
	}

	allGeoLabels = up.GeomodelLabels()
	// Sync unmappedScore with PassUnmappedSpecies and capture the pre-computed
	// mapping so the caller can build the unmapped set after the lock is released.
	if mrf, mok := rf.(*mappedRangeFilter); mok {
		var score float32
		if settings.BirdNET.RangeFilter.PassUnmappedSpecies {
			score = 1.0
		}
		mrf.unmappedScore = score
		cachedMapping = mrf.classifierToGeo
	}
	scores, err = up.PredictSpeciesScores(
		float32(settings.BirdNET.Latitude),
		float32(settings.BirdNET.Longitude),
		getWeekForFilter(date),
		threshold,
	)
	rfs.mu.Unlock()
	return scores, allGeoLabels, cachedMapping, true, err
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

	// FAST PATH: read under RLock and return a defensive copy.
	rfs.speciesCacheMu.RLock()
	if entry, ok := rfs.speciesCache[cacheKey]; ok && entry.key == cacheKey {
		out := make(map[string]float64, len(entry.scores))
		maps.Copy(out, entry.scores)
		rfs.speciesCacheMu.RUnlock()
		return out, nil
	}
	rfs.speciesCacheMu.RUnlock()

	// MISS PATH: use the same settings snapshot as the cache key.
	speciesScores, _, _, err := rfs.probableSpecies(targetDate, 0.0, settings)
	if err != nil {
		return nil, err
	}
	scores := buildOccurrenceIndex(speciesScores)

	// WRITE PATH: double-check, evict old entries, publish new results.
	rfs.speciesCacheMu.Lock()
	if entry, ok := rfs.speciesCache[cacheKey]; ok && entry.key == cacheKey {
		out := make(map[string]float64, len(entry.scores))
		maps.Copy(out, entry.scores)
		rfs.speciesCacheMu.Unlock()
		return out, nil
	}
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
		key:    cacheKey,
		scores: scores,
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
func buildMetaModel(settings *conf.Settings, cv classifierView, debug debugFunc) (backend inference.RangeFilter, fellBack bool, err error) {
	log := GetLogger()
	rf := settings.BirdNET.RangeFilter

	log.Info("Initializing range filter",
		logger.String("model", rf.Model),
		logger.String("model_path", rf.ModelPath),
		logger.String("labels_path", rf.LabelsPath),
		logger.String("classifier", cv.id),
		logger.String("models_dir", cv.modelsDir))

	// Auto-select v3 geomodel for compatible classifiers when files exist on disk.
	// Only applies locally for routing; does NOT publish settings. Skipped when an
	// explicit rangefilter.modelpath is set.
	if shouldAutoSelectV3GeomodelForConfig(rf.Model, rf.ModelPath, cv.id, cv.modelsDir) {
		localSettings := conf.CloneSettings(settings)
		applyAutoSelectedGeomodelPaths(localSettings, cv.modelsDir)
		settings = localSettings
		rf = settings.BirdNET.RangeFilter
		log.Info("Auto-selected v3.0 geomodel for compatible classifier",
			logger.String("classifier", cv.id),
			logger.String("models_dir", cv.modelsDir))
	}

	// On arm64 prefer the ONNX MData range filter when left on auto-select and no
	// explicit path is set and the v3 geomodel was not auto-selected above. Gated to
	// the BirdNET v2.4 family. See the original comment in initializeMetaModel.
	if path, ok := shouldSelectDefaultONNXRangeFilter(rf.Model, rf.ModelPath, cv.id, runtime.GOARCH, findModelPathInStandardPaths); ok {
		localSettings := conf.CloneSettings(settings)
		localSettings.BirdNET.RangeFilter.ModelPath = path
		settings = localSettings
		rf = settings.BirdNET.RangeFilter
		log.Info("Selected ONNX range filter (arm64 default)",
			logger.String("model_path", path))
	}

	switch resolveRangeFilterBackend(&rf) {
	case rangeFilterBackendMappedGeomodel, rangeFilterBackendONNXStrict:
		// Both subpaths run through the ONNX backend. A genuine load failure (ORT
		// unavailable, missing/corrupt model or labels) falls back to the embedded
		// TFLite range filter when the classifier has one (BirdNET v2.4); otherwise it
		// surfaces as an error instead of silently running unfiltered.
		b, onnxErr := buildONNXMetaModel(settings, cv)
		if onnxErr != nil {
			return fallbackToEmbeddedRangeFilter(settings, cv, onnxErr, debug)
		}
		return b, false, nil
	default:
		b, tfErr := buildTFLiteMetaModel(settings, debug)
		if tfErr != nil {
			return nil, false, tfErr
		}
		return b, false, nil
	}
}

// fallbackToEmbeddedRangeFilter recovers from an ONNX range-filter load failure by
// using the classifier's embedded TFLite range filter. Only the BirdNET v2.4 family
// has one; for other classifiers the original error is propagated. Build-and-return
// port of BirdNET.fallbackToEmbeddedRangeFilter.
func fallbackToEmbeddedRangeFilter(settings *conf.Settings, cv classifierView, cause error, debug debugFunc) (backend inference.RangeFilter, fellBack bool, err error) {
	if !hasNativeRangeFilter(cv.id) {
		return nil, false, cause
	}

	GetLogger().Warn("Range filter (ONNX geomodel) failed to load; falling back to the classifier's embedded TFLite range filter",
		logger.Error(cause),
		logger.String("classifier", cv.id))

	// getMetaModelData honors a non-empty rangefilter.modelpath first and would
	// re-read the failing geomodel file, so force the embedded MData model via empty
	// paths.
	fallbackSettings := conf.CloneSettings(settings)
	fallbackSettings.BirdNET.RangeFilter.Model = ""
	fallbackSettings.BirdNET.RangeFilter.ModelPath = ""
	fallbackSettings.BirdNET.RangeFilter.LabelsPath = ""

	b, tfErr := buildTFLiteMetaModel(fallbackSettings, debug)
	if tfErr != nil {
		return nil, false, errors.New(tfErr).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			Context("operation", "range_filter_embedded_fallback").
			Build()
	}

	return b, true, nil
}

// hasNativeRangeFilter reports whether the classifier can fall back to the embedded
// MData range filter (BirdNET v2.4 family only). Build-and-return port keyed on the
// registry ID rather than a *BirdNET receiver.
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
func buildONNXMetaModel(settings *conf.Settings, cv classifierView) (inference.RangeFilter, error) {
	start := time.Now()
	rf := settings.BirdNET.RangeFilter
	mapped := resolveRangeFilterBackend(&rf) == rangeFilterBackendMappedGeomodel

	modelName := "ONNX range filter"
	modelCtx := "range_filter"
	switch {
	case rf.Model == "v3":
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
		return buildMappedGeoModel(settings, cv)
	}

	// Strict path: no companion labels file, so the classifier labels must match the
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
			Labels: settings.BirdNET.Labels,
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
func buildMappedGeoModel(settings *conf.Settings, _ classifierView) (inference.RangeFilter, error) {
	start := time.Now()
	log := GetLogger()
	rfSettings := settings.BirdNET.RangeFilter

	log.Info("Geomodel range filter: starting initialization",
		logger.String("model", rfSettings.Model),
		logger.String("model_path", rfSettings.ModelPath),
		logger.String("labels_path", rfSettings.LabelsPath),
		logger.Int("classifier_labels", len(settings.BirdNET.Labels)))

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

	classifierLabels := settings.BirdNET.Labels
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
