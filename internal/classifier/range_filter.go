// rangefilter.go

package classifier

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	onnx "github.com/tphakala/birdnet-go/internal/inference/onnx"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// SpeciesScore holds a species label and its associated score.
type SpeciesScore struct {
	Score     float64
	Label     string
	Synthetic bool
}

// ByScore implements sort.Interface for []SpeciesScore based on the Score field.
type ByScore []SpeciesScore

func (a ByScore) Len() int           { return len(a) }
func (a ByScore) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByScore) Less(i, j int) bool { return a[i].Score > a[j].Score } // For descending order

// UniversalSpeciesPredictor is implemented by range filters that can
// produce a species inclusion list from their own labels, independent
// of any specific classifier's label set.
type UniversalSpeciesPredictor interface {
	PredictSpeciesScores(lat, lon, week, threshold float32) ([]SpeciesScore, error)
	GeomodelLabels() []string
}

// writeIncludedSpeciesDebug dumps the included-species list to a debug file when
// RangeFilter.Debug is enabled. It prefers a user-private cache directory over the
// process CWD (pollution, read-only-root containers) and over a world-writable
// shared temp dir, which is open to symlink clobbering (CWE-377/CWE-59) and
// multi-user filename collisions on a fixed name; it falls back to the OS temp dir
// only when no per-user cache dir is available.
func writeIncludedSpeciesDebug(includedSpecies []string) {
	var content strings.Builder
	fmt.Fprintf(&content, "Updated at: %s\nSpecies count: %d\n\nSpecies list:\n",
		time.Now().Format(time.DateTime),
		len(includedSpecies))
	for _, species := range includedSpecies {
		content.WriteString(species)
		content.WriteByte('\n')
	}
	data := []byte(content.String())

	// A user-private cache directory (0700) is not world-writable, so a fixed
	// filename there is not exposed to symlink clobbering.
	var path string
	var err error
	if cacheDir, cdErr := os.UserCacheDir(); cdErr == nil {
		birdnetCacheDir := filepath.Join(cacheDir, "birdnet-go")
		if mkErr := os.MkdirAll(birdnetCacheDir, 0o700); mkErr == nil {
			path = filepath.Join(birdnetCacheDir, "debug_included_species.txt")
			err = os.WriteFile(path, data, 0o600)
		}
	}

	// No per-user cache dir: fall back to the world-writable temp dir, but use
	// os.CreateTemp so the file is created O_EXCL with a random name and cannot
	// follow a pre-planted symlink (CWE-377/CWE-59).
	if path == "" {
		f, ctErr := os.CreateTemp("", "debug_included_species_*.txt")
		if ctErr != nil {
			GetLogger().Warn("Failed to create included species debug file", logger.Error(ctErr))
			return
		}
		path = f.Name()
		_, err = f.Write(data)
		_ = f.Close()
	}

	if err != nil {
		GetLogger().Warn("Failed to write included species debug file",
			logger.Error(err),
			logger.String("debug_file", path),
			logger.Int("species_count", len(includedSpecies)))
	}
}

// BuildRangeFilter updates the range filter with current probable species.
// If the active range filter implements UniversalSpeciesPredictor, the
// species list is derived directly from the geomodel's own labels
// (typically ~12K species). Otherwise it falls back to GetProbableSpecies,
// which is limited to the BirdNET classifier's label set.
func BuildRangeFilter(o *Orchestrator) error {
	start := time.Now()
	today := conf.LocalNoon(start)
	// Read settings via the atomic-safe accessor: o.Settings is reassigned at
	// runtime by Orchestrator.ReloadModel (under o.mu), so a raw field read here
	// would race with concurrent reloads.
	settings := o.CurrentSettings()

	var includedSpecies []string

	// Snapshot primary under read lock to avoid racing with Delete(), which sets
	// o.primary = nil under o.mu.Lock(). All callers reach BuildRangeFilter
	// without holding o.mu, so taking RLock here cannot self-deadlock.
	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return errors.Newf("orchestrator has no primary model").
			Component("classifier.orchestrator").
			Category(errors.CategorySystem).
			Build()
	}

	primary.mu.Lock()
	rf := primary.rangeFilter
	up, isUniversal := rf.(UniversalSpeciesPredictor)

	if isUniversal && settings.BirdNET.LocationConfigured {
		threshold := settings.BirdNET.RangeFilter.Threshold
		if threshold < 0 || threshold > 1 {
			threshold = 0.01
		}

		allGeoLabels := up.GeomodelLabels()
		scores, err := up.PredictSpeciesScores(
			float32(settings.BirdNET.Latitude),
			float32(settings.BirdNET.Longitude),
			getWeekForFilter(today),
			threshold,
		)
		if err != nil {
			primary.mu.Unlock()
			return errors.New(err).
				Category(errors.CategoryValidation).
				Context("date", today.Format(time.DateOnly)).
				Context("latitude", settings.BirdNET.Latitude).
				Context("longitude", settings.BirdNET.Longitude).
				Timing("range-filter-build", time.Since(start)).
				Build()
		}

		// Sync unmappedScore with the current PassUnmappedSpecies setting so
		// that the legacy predictFilter path (which calls Predict()) sees the
		// correct value without requiring a full ReloadRangeFilter.
		// Also capture the pre-computed classifier-to-geomodel mapping to
		// avoid rebuilding it after the lock is released.
		// Done after the error check so that a failed prediction does not
		// leave unmappedScore inconsistent with the inclusion list.
		var cachedMapping []int
		if mrf, ok := rf.(*mappedRangeFilter); ok {
			var score float32
			if settings.BirdNET.RangeFilter.PassUnmappedSpecies {
				score = 1.0
			}
			mrf.unmappedScore = score
			cachedMapping = mrf.classifierToGeo
		}
		primary.mu.Unlock()

		// Build the exclude matcher once per rebuild: it reverse-resolves localized
		// common-name exclude entries through OpenFauna a single time so the per-score
		// matches() below stays off the dataset scan.
		excluder := newExcludeMatcher(settings.Realtime.Species.Exclude, settings.BirdNET.Locale)

		includedSpecies = make([]string, 0, len(scores))
		for _, ss := range scores {
			if !excluder.matches(ss.Label) {
				includedSpecies = append(includedSpecies, ss.Label)
			}
		}

		addUserOverrideSpecies(&includedSpecies, settings, allGeoLabels)

		// When PassUnmappedSpecies is enabled, add classifier species that
		// have no corresponding entry in the geomodel so they are not
		// silently blocked by the species inclusion check in the processor.
		var unmappedCount int
		if settings.BirdNET.RangeFilter.PassUnmappedSpecies {
			seen := make(map[string]bool, len(includedSpecies))
			for _, s := range includedSpecies {
				seen[s] = true
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
						includedSpecies = append(includedSpecies, label)
						seen[label] = true
						unmappedCount++
					}
				}
			}
		}

		GetLogger().Info("Range filter updated via universal geomodel path",
			logger.Int("geomodel_species", len(scores)),
			logger.Int("included_species", len(includedSpecies)),
			logger.Int("unmapped_species_added", unmappedCount),
			logger.Float64("threshold", float64(threshold)),
			logger.String("duration", time.Since(start).String()))
	} else {
		primary.mu.Unlock()
		speciesScores, err := o.GetProbableSpecies(today, 0.0)
		if err != nil {
			return errors.New(err).
				Category(errors.CategoryValidation).
				Context("date", today.Format(time.DateOnly)).
				Context("latitude", settings.BirdNET.Latitude).
				Context("longitude", settings.BirdNET.Longitude).
				Timing("range-filter-build", time.Since(start)).
				Build()
		}
		includedSpecies = make([]string, 0, len(speciesScores))
		for _, speciesScore := range speciesScores {
			includedSpecies = append(includedSpecies, speciesScore.Label)
		}

		GetLogger().Info("Range filter updated via legacy classifier path",
			logger.Int("included_species", len(includedSpecies)),
			logger.String("duration", time.Since(start).String()))
	}

	if settings.BirdNET.RangeFilter.Debug {
		writeIncludedSpeciesDebug(includedSpecies)
	}

	conf.UpdateIncludedSpecies(includedSpecies)
	if err := o.RebuildNameResolver(includedSpecies); err != nil {
		// Non-fatal: the resolver keeps its previous snapshot and on-demand Lookup
		// still resolves names. Log and continue so a name-index hiccup never blocks
		// the range-filter rebuild.
		GetLogger().Warn("Failed to rebuild OpenFauna name resolver after range filter rebuild",
			logger.Error(err))
	}
	o.notifyRangeFilterReload()
	return nil
}

// matchingLabels returns every label that matches speciesName by its common or
// scientific name, in canonical "Scientific_Common" form so callers can append
// the label instead of the user's bare entry.
func matchingLabels(labels []string, speciesName string) []string {
	var matched []string
	for _, label := range labels {
		if matchesSpecies(label, speciesName) {
			matched = append(matched, label)
		}
	}
	return matched
}

// canonicalOverrideLabels resolves a user override entry to canonical labels,
// preferring the geomodel's own labels and falling back to the active
// classifier's localized labels. The geomodel labels are "Scientific_English",
// so a localized common name (e.g. the Finnish "sinitiainen") matches none of
// them; the active classifier's labels carry "Scientific_LocalizedCommon"
// (e.g. "Cyanistes caeruleus_sinitiainen") and do match. Resolving against both
// keeps a localized override from being appended verbatim and then mis-keyed as
// a scientific name by the inclusion gate and the OpenFauna name resolver
// (issue #982). Returns nil when the entry matches no biological label (e.g.
// non-bird classes like "drone"/"heatpump"), so callers append the raw entry
// and the name resolver legitimately reports it as unresolved.
func canonicalOverrideLabels(speciesName string, geoLabels, classifierLabels []string) []string {
	if matched := matchingLabels(geoLabels, speciesName); len(matched) > 0 {
		return matched
	}
	return matchingLabels(classifierLabels, speciesName)
}

// overrideSpeciesNames returns the user's force-include overrides: the
// realtime.species.include entries followed by the realtime.species.config keys.
func overrideSpeciesNames(settings *conf.Settings) []string {
	names := make([]string, 0, len(settings.Realtime.Species.Include)+len(settings.Realtime.Species.Config))
	names = append(names, settings.Realtime.Species.Include...)
	// Sort the config keys: Go map iteration is non-deterministic, and the override
	// order flows into the inclusion working set, debug logs, and the species-list API.
	configKeys := slices.Collect(maps.Keys(settings.Realtime.Species.Config))
	slices.Sort(configKeys)
	names = append(names, configKeys...)
	return names
}

// resolveOverrideLabels canonicalizes the user's force-include overrides (the
// realtime.species.include entries and .config keys) into concrete model labels,
// shared by both inclusion-set appenders. Each entry resolves, in order, to its
// matching geomodel labels, the active classifier's localized labels, or - for a
// species a non-primary model emits as a scientific-only label, named by its
// localized common name (e.g. the Finnish bat/mammal "kettu"/"ilves") - the
// scientific name(s) reverse-resolved from OpenFauna. The forward,
// scientific-keyed label matching cannot reverse those, since the localized name
// lives only in OpenFauna. Entries that resolve to nothing are kept verbatim, so the
// name resolver legitimately reports a genuinely unresolvable entry. Reverse
// resolution for every otherwise-unresolved entry is batched into one dataset scan,
// keeping the cost off the per-entry path on the (cold) range-filter rebuild.
func resolveOverrideLabels(settings *conf.Settings, geoLabels []string) []string {
	names := overrideSpeciesNames(settings)
	out := make([]string, 0, len(names))
	var unresolved []string
	for _, name := range names {
		if labels := canonicalOverrideLabels(name, geoLabels, settings.BirdNET.Labels); len(labels) > 0 {
			out = append(out, labels...)
		} else {
			unresolved = append(unresolved, name)
		}
	}
	if len(unresolved) > 0 {
		reverse := openfauna.LookupScientificNames(unresolved, settings.BirdNET.Locale)
		for _, name := range unresolved {
			if sci := reverse[name]; len(sci) > 0 {
				out = append(out, sci...)
			} else {
				out = append(out, name)
			}
		}
	}
	return out
}

// addUserOverrideSpeciesScores appends species from the explicit include list
// and species with configured actions to a SpeciesScore slice with score 1.0.
// Used by the universal geomodel path in getProbableSpecies. Each entry is
// canonicalized via resolveOverrideLabels so localized common names enter the
// set as their canonical model labels rather than the raw user string.
//
// Dedup here is by exact label and intentionally narrow: it only avoids
// re-appending a label already present verbatim. A force-included species that
// the geomodel also scored can therefore appear twice (its range-filter score
// plus this score-1.0 entry) when the two carry different label strings; that is
// expected. The score-1.0 entry must be kept so the species reads as
// always-active, and same-taxon near-duplicates are collapsed for the user at
// the display boundary (dedupeSpeciesForDisplay in internal/api/v2/range/range.go),
// not here, so the functional inclusion set keeps every scientific name.
func addUserOverrideSpeciesScores(bn *BirdNET, speciesScores *[]SpeciesScore, settings *conf.Settings, geoLabels []string) {
	seen := make(map[string]bool, len(*speciesScores))
	for _, ss := range *speciesScores {
		seen[ss.Label] = true
	}
	for _, label := range resolveOverrideLabels(settings, geoLabels) {
		if !seen[label] {
			bn.Debug("Adding override species with max score: %s", label)
			*speciesScores = append(*speciesScores, SpeciesScore{Score: 1.0, Label: label, Synthetic: true})
			seen[label] = true
		}
	}
}

// addUserOverrideSpecies appends species from the explicit include list and
// species with configured actions to the inclusion set. Each entry is
// canonicalized via resolveOverrideLabels (geomodel labels, then the active
// classifier's localized labels, then an OpenFauna reverse lookup for
// scientific-only non-primary-model species). A resolved entry is appended in its
// canonical label form so the scientific name map keys it correctly; an entry that
// resolves to nothing (e.g. a non-fauna class) is appended verbatim.
func addUserOverrideSpecies(includedSpecies *[]string, settings *conf.Settings, geoLabels []string) {
	seen := make(map[string]bool, len(*includedSpecies))
	for _, s := range *includedSpecies {
		seen[s] = true
	}
	for _, label := range resolveOverrideLabels(settings, geoLabels) {
		if !seen[label] {
			*includedSpecies = append(*includedSpecies, label)
			seen[label] = true
		}
	}
}

// GetProbableSpecies filters and sorts bird species based on their scores.
// Settings are read from the latest published snapshot (via conf.CurrentOrFallback)
// so that UI changes to coordinates, threshold, or LocationConfigured take
// effect immediately without restarting the service.
func (bn *BirdNET) GetProbableSpecies(date time.Time, week float32) ([]SpeciesScore, error) {
	scores, _, err := bn.getProbableSpecies(date, week, bn.currentSettings())
	return scores, err
}

// GetProbableSpeciesWithSettings filters species using the supplied settings
// snapshot instead of reading from the global atomic pointer. This allows the
// test endpoint to evaluate arbitrary coordinates and thresholds without
// publishing temporary values into the global settings, eliminating the race
// where a concurrent BuildRangeFilter could pick up test data.
func (bn *BirdNET) GetProbableSpeciesWithSettings(date time.Time, week float32, settings *conf.Settings) ([]SpeciesScore, error) {
	scores, _, err := bn.getProbableSpecies(date, week, settings)
	return scores, err
}

// getProbableSpecies is the shared implementation for both the global-settings
// and explicit-settings entry points.
//
// When the range filter implements UniversalSpeciesPredictor (v3 geomodel),
// species are predicted using the geomodel's own 12K label set, so the result
// includes all taxa the geomodel covers (birds, mammals, insects, etc.)
// regardless of which classifier is active. Otherwise, the legacy path maps
// geomodel scores to the classifier's label set.
//
// The second return value is the geomodel's full label set, captured from the
// same range-filter snapshot that produced the scores. It is non-nil only on
// the universal path. Returning it here lets callers that need both avoid a
// second lock that could observe a different range-filter instance after a
// concurrent ReloadRangeFilter.
func (bn *BirdNET) getProbableSpecies(date time.Time, week float32, settings *conf.Settings) ([]SpeciesScore, []string, error) {
	bn.Debug("Applying range filter")

	// Build the exclude matcher once: it reverse-resolves localized common-name exclude
	// entries through OpenFauna a single time so the per-score matches() below (and
	// zeroScoresForAllLabels) stay off the dataset scan.
	excluder := newExcludeMatcher(settings.Realtime.Species.Exclude, settings.BirdNET.Locale)

	// Skip filtering if range filter backend is not initialized.
	// Read under lock to avoid data race with Delete().
	bn.mu.Lock()
	hasRangeFilter := bn.rangeFilter != nil
	bn.mu.Unlock()
	if !hasRangeFilter {
		bn.Debug("Range filter model not loaded, returning zero scores for all labels")
		return zeroScoresForAllLabels(settings.BirdNET.Labels, excluder), nil, nil
	}

	// Skip filtering if location is not configured
	if !settings.BirdNET.LocationConfigured {
		bn.Debug("Location not configured, not using location based prediction filter")
		return zeroScoresForAllLabels(settings.BirdNET.Labels, excluder), nil, nil
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

	// Try the universal geomodel path first: predict from the geomodel's
	// own label set so that all 12K species are covered.
	bn.mu.Lock()
	up, isUniversal := bn.rangeFilter.(UniversalSpeciesPredictor)
	if isUniversal {
		allGeoLabels := up.GeomodelLabels()
		var cachedMapping []int
		if mrf, ok := bn.rangeFilter.(*mappedRangeFilter); ok {
			cachedMapping = mrf.classifierToGeo
		}
		scores, err := up.PredictSpeciesScores(
			float32(settings.BirdNET.Latitude),
			float32(settings.BirdNET.Longitude),
			week,
			threshold,
		)
		bn.mu.Unlock()

		if err != nil {
			return nil, nil, errors.New(err).
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

		addUserOverrideSpeciesScores(bn, &speciesScores, settings, allGeoLabels)

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
		return speciesScores, allGeoLabels, nil
	}
	bn.mu.Unlock()

	// Legacy path: map geomodel scores to the classifier's label set.
	filters, err := bn.predictFilter(date, week, settings, threshold)
	if err != nil {
		return nil, nil, errors.New(err).
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
			bn.Debug("Excluding species from range filter: %s", filter.Label)
		}
	}

	// Apply user overrides through the shared resolver so the legacy path canonicalizes
	// localized common names (including the OpenFauna reverse lookup for scientific-only
	// non-primary-model species) identically to the universal path. The legacy path has
	// no geomodel label set, so resolution falls to the classifier labels and the
	// reverse lookup.
	addUserOverrideSpeciesScores(bn, &speciesScores, settings, nil)

	sort.Sort(ByScore(speciesScores))
	return speciesScores, nil, nil
}

// zeroScoresForAllLabels creates a slice of SpeciesScore with zero scores for all provided labels,
// excluding any species the matcher rejects. This ensures that excluded species are
// filtered even when the range filter model is not active or location is not configured.
func zeroScoresForAllLabels(labels []string, excl excludeMatcher) []SpeciesScore {
	speciesScores := make([]SpeciesScore, 0, len(labels))
	for _, label := range labels {
		if !excl.matches(label) {
			speciesScores = append(speciesScores, SpeciesScore{Score: 0.0, Label: label})
		}
	}
	return speciesScores
}

// excludeMatcher matches model labels against the user's realtime.species.exclude
// list for one rebuild/prediction pass. It forward-matches each label's scientific and
// common name against the raw entries (like the include side), plus carries a set of
// scientific names reverse-resolved from localized common-name entries via OpenFauna. A
// non-primary model emits scientific-only labels (e.g. "Vulpes vulpes"), whose parsed
// common name falls back to the scientific name, so a localized exclude entry (the
// Finnish "Kettu") would never forward-match; that localized name lives only in
// OpenFauna. The reverse resolution is batched once at construction (one dataset scan),
// keeping the per-label match off OpenFauna so it stays safe on the hot paths the
// matcher runs on (per geomodel score during rebuild, zeroScoresForAllLabels). This
// mirrors the include-side reverse lookup in resolveOverrideLabels.
type excludeMatcher struct {
	entries    []string            // raw exclude entries, forward-matched per label
	reverseSci map[string]struct{} // lower-cased scientific names of localized entries
}

// newExcludeMatcher precomputes the reverse-resolution set for excludeList at the
// BirdNET locale. An empty exclude list yields a zero-value matcher whose matches() is
// a cheap no-op and skips the OpenFauna scan entirely.
func newExcludeMatcher(excludeList []string, locale string) excludeMatcher {
	m := excludeMatcher{entries: excludeList}
	if len(excludeList) == 0 {
		return m
	}
	// Reverse-resolve localized common-name entries to a flat set of lower-cased
	// scientific names via the shared helper, which centralizes the lower-casing and
	// locale handling (matches() also lower-cases the label's scientific name).
	// Leave reverseSci nil when nothing resolves so matches() keeps its nil-guard
	// fast path and skips the per-label ToLower + map lookup on the hot rebuild loop
	// (matches() runs per geomodel score during a range-filter rebuild).
	if sciSet := openfauna.ReverseResolveToScientificSet(excludeList, locale); len(sciSet) > 0 {
		m.reverseSci = sciSet
	}
	return m
}

// matches reports whether label is excluded: a forward scientific/common-name match
// against an entry, or a reverse match of the label's scientific name to a localized
// common-name entry resolved through OpenFauna.
func (m excludeMatcher) matches(label string) bool {
	sp := detection.ParseSpeciesString(label)
	for _, entry := range m.entries {
		if strings.EqualFold(sp.ScientificName, entry) || strings.EqualFold(sp.CommonName, entry) {
			return true
		}
	}
	if m.reverseSci != nil {
		if _, ok := m.reverseSci[strings.ToLower(sp.ScientificName)]; ok {
			return true
		}
	}
	return false
}

// matchesSpecies checks if a label matches a species name (either common or scientific)
func matchesSpecies(label, speciesName string) bool {
	sp := detection.ParseSpeciesString(label)
	return strings.EqualFold(sp.ScientificName, speciesName) || strings.EqualFold(sp.CommonName, speciesName)
}

// predictFilter applies the range filter model to predict species based on location and date.
// The caller supplies the settings snapshot and a pre-validated threshold so
// that the same values are used consistently across the entire
// GetProbableSpecies call.
func (bn *BirdNET) predictFilter(date time.Time, week float32, settings *conf.Settings, threshold float32) ([]Filter, error) {
	start := time.Now()

	// If week is not set, use current date to get week
	if week == 0 {
		week = getWeekForFilter(date)
	}

	// Lock to prevent concurrent access to the range filter backend.
	// The TFLite interpreter is not goroutine-safe. Also re-check nil
	// under lock in case Delete() raced between the caller's nil check
	// and this point.
	bn.mu.Lock()
	if bn.rangeFilter == nil {
		bn.mu.Unlock()
		return nil, fmt.Errorf("range filter was closed during prediction")
	}
	scores, err := bn.rangeFilter.Predict(
		float32(settings.BirdNET.Latitude),
		float32(settings.BirdNET.Longitude),
		week,
	)
	bn.mu.Unlock()

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

	// Filter and label the results, but only for indices that exist in labels
	var results []Filter
	for i, score := range scores {
		if score >= threshold && i < len(settings.BirdNET.Labels) {
			results = append(results, Filter{Score: score, Label: settings.BirdNET.Labels[i]})
		}
	}

	// Sort results by score in descending order
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// getWeekForFilter calculates the BirdNET week number for the filter model.
// BirdNET assumes 4 weeks per month, so days 29-31 are clamped to week 4 of
// their month; the result is always in [1, 48]. It delegates to the canonical
// onnx.CalculateWeek instead of reimplementing the formula: a drifted, un-clamped
// copy here was the original source of the out-of-range week (e.g. 49 for
// Dec 29-31) fed into the range filter model.
func getWeekForFilter(date time.Time) float32 {
	if date.IsZero() {
		date = time.Now()
	}
	return onnx.CalculateWeek(int(date.Month()), date.Day())
}

// debug functions

// RunFilterProcess executes the filter process on demand and prints the results.
func (bn *BirdNET) RunFilterProcess(dateStr string, week float32) {
	// If dateStr is not empty, parse the date
	var parsedDate time.Time
	var err error
	if dateStr != "" {
		parsedDate, err = time.Parse(time.DateOnly, dateStr)
		if err != nil {
			fmt.Printf("Error parsing date: %s\n", err)
			return
		}
	}

	// Get the probable species
	speciesScores, err := bn.GetProbableSpecies(parsedDate, week)
	if err != nil {
		fmt.Printf("Error during species prediction: %s\n", err)
		return
	}

	PrintSpeciesScores(parsedDate, speciesScores)
}

// PrintSpeciesScores prints out the list of species scores in a human-readable format.
func PrintSpeciesScores(date time.Time, speciesScores []SpeciesScore) {
	// Get settings
	threshold := conf.Setting().BirdNET.RangeFilter.Threshold
	lat := conf.Setting().BirdNET.Latitude
	lon := conf.Setting().BirdNET.Longitude

	week := int(getWeekForFilter(date))
	fmt.Printf("Included species for %v, %v on date %s, week %d, threshold %.6f\n\n", lat, lon, date.Format(time.DateOnly), week, threshold)

	// Get number of species in speciesScores slice
	numSpecies := len(speciesScores)

	// Print header
	fmt.Printf("%-33s %-33s %-6s\n", "Scientific Name", "Common Name", "Score")
	fmt.Println(strings.Repeat("-", 33), strings.Repeat("-", 33), strings.Repeat("-", 6))

	for _, speciesScore := range speciesScores {
		sp := detection.ParseSpeciesString(speciesScore.Label)
		fmt.Printf("%-33s %-33s %.4f\n", sp.ScientificName, sp.CommonName, speciesScore.Score)
	}

	fmt.Printf("\nTotal number of species: %d\n", numSpecies)
}
