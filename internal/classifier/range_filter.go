// rangefilter.go

package classifier

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// SpeciesScore holds a species label and its associated score.
type SpeciesScore struct {
	Score float64
	Label string
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

// BuildRangeFilter updates the range filter with current probable species.
// If the active range filter implements UniversalSpeciesPredictor, the
// species list is derived directly from the geomodel's own labels
// (typically ~12K species). Otherwise it falls back to GetProbableSpecies,
// which is limited to the BirdNET classifier's label set.
func BuildRangeFilter(o *Orchestrator) error {
	start := time.Now()
	today := start.Truncate(24 * time.Hour)
	settings := conf.CurrentOrFallback(o.Settings)

	var includedSpecies []string

	o.primary.mu.Lock()
	rf := o.primary.rangeFilter
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
			o.primary.mu.Unlock()
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
		o.primary.mu.Unlock()

		includedSpecies = make([]string, 0, len(scores))
		for _, ss := range scores {
			if !isSpeciesExcluded(ss.Label, settings.Realtime.Species.Exclude) {
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
			for i, geoIdx := range mapping {
				if geoIdx == -1 {
					label := settings.BirdNET.Labels[i]
					if !seen[label] && !isSpeciesExcluded(label, settings.Realtime.Species.Exclude) {
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
		o.primary.mu.Unlock()
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
		debugFile := "debug_included_species.txt"
		var content strings.Builder
		fmt.Fprintf(&content, "Updated at: %s\nSpecies count: %d\n\nSpecies list:\n",
			time.Now().Format(time.DateTime),
			len(includedSpecies))
		for _, species := range includedSpecies {
			content.WriteString(species)
			content.WriteByte('\n')
		}
		if err := os.WriteFile(debugFile, []byte(content.String()), 0o600); err != nil {
			GetLogger().Warn("Failed to write included species debug file",
				logger.Error(err),
				logger.String("debug_file", debugFile),
				logger.Int("species_count", len(includedSpecies)))
		}
	}

	conf.UpdateIncludedSpecies(includedSpecies)
	o.notifyRangeFilterReload()
	return nil
}

// addUserOverrideSpeciesScores appends species from the explicit include list
// and species with configured actions to a SpeciesScore slice with score 1.0.
// Used by the universal geomodel path in getProbableSpecies.
func addUserOverrideSpeciesScores(bn *BirdNET, speciesScores *[]SpeciesScore, settings *conf.Settings, availableLabels []string) {
	seen := make(map[string]bool, len(*speciesScores))
	for _, ss := range *speciesScores {
		seen[ss.Label] = true
	}

	addOverride := func(speciesName string) {
		matched := false
		for _, label := range availableLabels {
			if matchesSpecies(label, speciesName) {
				matched = true
				if !seen[label] {
					bn.Debug("Adding override species with max score: %s (matched with: %s)", label, speciesName)
					*speciesScores = append(*speciesScores, SpeciesScore{Score: 1.0, Label: label})
					seen[label] = true
				}
			}
		}
		if !matched && !seen[speciesName] {
			*speciesScores = append(*speciesScores, SpeciesScore{Score: 1.0, Label: speciesName})
			seen[speciesName] = true
		}
	}

	for _, species := range settings.Realtime.Species.Include {
		addOverride(species)
	}
	for species := range settings.Realtime.Species.Config {
		addOverride(species)
	}
}

// addUserOverrideSpecies appends species from the explicit include list
// and species with configured actions to the inclusion set. Each entry is
// matched against availableLabels by common or scientific name; if no match
// is found the raw entry is appended so the scientific name map picks it up.
func addUserOverrideSpecies(includedSpecies *[]string, settings *conf.Settings, availableLabels []string) {
	seen := make(map[string]bool, len(*includedSpecies))
	for _, s := range *includedSpecies {
		seen[s] = true
	}

	addOverride := func(speciesName string) {
		matched := false
		for _, label := range availableLabels {
			if matchesSpecies(label, speciesName) {
				matched = true
				if !seen[label] {
					*includedSpecies = append(*includedSpecies, label)
					seen[label] = true
				}
			}
		}
		if !matched && !seen[speciesName] {
			*includedSpecies = append(*includedSpecies, speciesName)
			seen[speciesName] = true
		}
	}

	for _, species := range settings.Realtime.Species.Include {
		addOverride(species)
	}
	for species := range settings.Realtime.Species.Config {
		addOverride(species)
	}
}

// GetProbableSpecies filters and sorts bird species based on their scores.
// Settings are read from the latest published snapshot (via conf.CurrentOrFallback)
// so that UI changes to coordinates, threshold, or LocationConfigured take
// effect immediately without restarting the service.
func (bn *BirdNET) GetProbableSpecies(date time.Time, week float32) ([]SpeciesScore, error) {
	scores, _, err := bn.getProbableSpecies(date, week, conf.CurrentOrFallback(bn.Settings))
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

	// Skip filtering if range filter backend is not initialized.
	// Read under lock to avoid data race with Delete().
	bn.mu.Lock()
	hasRangeFilter := bn.rangeFilter != nil
	bn.mu.Unlock()
	if !hasRangeFilter {
		bn.Debug("Range filter model not loaded, returning zero scores for all labels")
		return zeroScoresForAllLabels(settings.BirdNET.Labels, settings.Realtime.Species.Exclude), nil, nil
	}

	// Skip filtering if location is not configured
	if !settings.BirdNET.LocationConfigured {
		bn.Debug("Location not configured, not using location based prediction filter")
		return zeroScoresForAllLabels(settings.BirdNET.Labels, settings.Realtime.Species.Exclude), nil, nil
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
			if !isSpeciesExcluded(ss.Label, settings.Realtime.Species.Exclude) {
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
			for i, geoIdx := range mapping {
				if geoIdx == -1 {
					label := settings.BirdNET.Labels[i]
					if !seen[label] && !isSpeciesExcluded(label, settings.Realtime.Species.Exclude) {
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
		if !isSpeciesExcluded(filter.Label, settings.Realtime.Species.Exclude) {
			speciesScores = append(speciesScores, SpeciesScore{Score: float64(filter.Score), Label: filter.Label})
		} else {
			bn.Debug("Excluding species from range filter: %s", filter.Label)
		}
	}

	seen := make(map[string]bool, len(speciesScores))
	for _, ss := range speciesScores {
		seen[ss.Label] = true
	}

	processedSpecies := make(map[string]bool)
	labels := settings.BirdNET.Labels
	for _, includedSpecies := range settings.Realtime.Species.Include {
		addSpeciesWithMaxScore(bn, &speciesScores, includedSpecies, processedSpecies, labels, seen)
	}
	for species := range settings.Realtime.Species.Config {
		addSpeciesWithMaxScore(bn, &speciesScores, species, processedSpecies, labels, seen)
	}

	sort.Sort(ByScore(speciesScores))
	return speciesScores, nil, nil
}

// zeroScoresForAllLabels creates a slice of SpeciesScore with zero scores for all provided labels,
// excluding any species that appear in the exclude list. This ensures that excluded species are
// filtered even when the range filter model is not active or location is not configured.
func zeroScoresForAllLabels(labels, excludeList []string) []SpeciesScore {
	speciesScores := make([]SpeciesScore, 0, len(labels))
	for _, label := range labels {
		if !isSpeciesExcluded(label, excludeList) {
			speciesScores = append(speciesScores, SpeciesScore{Score: 0.0, Label: label})
		}
	}
	return speciesScores
}

// addSpeciesWithMaxScore adds all matching species to the scores list with maximum score.
func addSpeciesWithMaxScore(bn *BirdNET, speciesScores *[]SpeciesScore, speciesName string, processedSpecies map[string]bool, labels []string, seen map[string]bool) {
	if processedSpecies[speciesName] {
		return
	}

	matchFound := false
	for _, label := range labels {
		if matchesSpecies(label, speciesName) {
			if !seen[label] {
				bn.Debug("Adding species with max score: %s (matched with: %s)", label, speciesName)
				*speciesScores = append(*speciesScores, SpeciesScore{Score: 1.0, Label: label})
				seen[label] = true
			}
			matchFound = true
		}
	}

	if matchFound {
		processedSpecies[speciesName] = true
	}
}

// isSpeciesExcluded checks if a species should be excluded based on its label
func isSpeciesExcluded(label string, excludeList []string) bool {
	for _, excludedSpecies := range excludeList {
		if matchesSpecies(label, excludedSpecies) {
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

// getWeekForFilter calculates the current week number for the filter model.
func getWeekForFilter(date time.Time) float32 {
	var month int
	var day int

	if date.IsZero() {
		date = time.Now()
	}

	month = int(date.Month())
	day = date.Day()

	// Calculate the week number
	weeksFromMonths := (month - 1) * 4
	weekInMonth := (day-1)/7 + 1

	return float32(weeksFromMonths + weekInMonth)
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
