package processor

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
	"gorm.io/gorm"
)

// Extended capture timeout thresholds.
const (
	extendedCaptureMinInitialWait  = 15 * time.Second
	extendedCaptureMediumThreshold = 30 * time.Second
	extendedCaptureMediumWait      = 30 * time.Second
	extendedCaptureLongThreshold   = 2 * time.Minute
	extendedCaptureLongWait        = 60 * time.Second
)

// getTaxonomyDB returns the cached taxonomy database, loading it on first call.
// Returns nil if the database cannot be loaded (with a warning logged).
func (p *Processor) getTaxonomyDB() *classifier.TaxonomyDatabase {
	p.taxonomyDBOnce.Do(func() {
		db, err := classifier.LoadTaxonomyDatabase()
		if err != nil {
			GetLogger().Warn("Failed to load taxonomy database, genus/family/order filtering unavailable",
				logger.Any("error", err),
				logger.String("operation", "taxonomy_db_load"))
			return
		}
		p.taxonomyDB = db
	})
	return p.taxonomyDB
}

// RebuildExtendedCaptureFilter re-resolves the extended capture species filter
// from the current settings. This is called by the control monitor when
// ExtendedCapture settings (Enabled, Species, MaxDuration) change at runtime.
func (p *Processor) RebuildExtendedCaptureFilter() {
	p.initExtendedCapture()
}

// initExtendedCapture resolves the extended capture species filter at startup.
// Called from Processor.New(). Safe to re-call on settings refresh.
func (p *Processor) initExtendedCapture() {
	settings := p.currentSettings()

	if !settings.Realtime.ExtendedCapture.Enabled {
		p.extendedCaptureMu.Lock()
		p.extendedCaptureAll = false
		p.extendedCaptureSpecies = nil
		p.extendedCaptureMu.Unlock()
		return
	}

	// Resolve config entries against the full multi-model label union (primary plus
	// secondary models such as bats/Perch) so secondary-model species match. Fall
	// back to the primary labels if the orchestrator is unavailable.
	var labels []string
	if p.Bn != nil {
		labels = p.Bn.AllLabels()
	}
	if len(labels) == 0 {
		labels = settings.BirdNET.Labels
	}
	locale := settings.BirdNET.Locale

	// Get cached taxonomy database for genus/family/order resolution
	taxonomyDB := p.getTaxonomyDB()

	configSpecies := settings.Realtime.ExtendedCapture.Species
	if p.Ds != nil {
		if managerGetter, ok := p.Ds.(interface{ GetDB() *gorm.DB }); ok {
			db := managerGetter.GetDB()
			if db != nil && datastoreV2.IsEnhancedDatabase() {
				repo := repository.NewSpeciesListRepository(db, nil)
				var expandedSpecies []string
				for _, sp := range configSpecies {
					if strings.HasPrefix(sp, "list:") {
						listIDStr := strings.TrimPrefix(sp, "list:")
						listID64, parseErr := strconv.ParseUint(listIDStr, 10, 32)
						if parseErr != nil {
							GetLogger().Warn("invalid species list ID in extended capture config",
								logger.String("list_entry", sp), logger.Error(parseErr))
							continue
						}
						sciNames, resolveErr := repo.ResolveSpeciesList(context.Background(), uint(listID64))
						if resolveErr == nil {
							expandedSpecies = append(expandedSpecies, sciNames...)
						} else {
							GetLogger().Warn("failed to resolve species list for extended capture",
								logger.String("list_id", listIDStr), logger.Error(resolveErr))
						}
					} else {
						expandedSpecies = append(expandedSpecies, sp)
					}
				}
				configSpecies = expandedSpecies
			}
		}
	}

	isAll, resolved := resolveSpeciesFilter(
		configSpecies, labels, taxonomyDB, locale, "extended_capture",
	)

	p.extendedCaptureMu.Lock()
	p.extendedCaptureAll = isAll
	p.extendedCaptureSpecies = resolved
	p.extendedCaptureMu.Unlock()

	if isAll {
		GetLogger().Info("Extended capture enabled for all species",
			logger.Int("max_duration_seconds", settings.Realtime.ExtendedCapture.MaxDuration),
			logger.String("operation", "extended_capture_init"))
	} else {
		GetLogger().Info("Extended capture enabled for filtered species",
			logger.Int("species_count", len(resolved)),
			logger.Int("max_duration_seconds", settings.Realtime.ExtendedCapture.MaxDuration),
			logger.String("operation", "extended_capture_init"))
	}
}

// isExtendedCaptureSpecies checks if a species qualifies for extended capture.
func (p *Processor) isExtendedCaptureSpecies(scientificName string) bool {
	settings := p.currentSettings()

	if !settings.Realtime.ExtendedCapture.Enabled {
		return false
	}

	p.extendedCaptureMu.RLock()
	defer p.extendedCaptureMu.RUnlock()

	if p.extendedCaptureAll {
		return true
	}

	return p.extendedCaptureSpecies[strings.ToLower(scientificName)]
}

// resolveSpeciesFilter resolves the config species list into a set of scientific names.
// Returns (isAll, resolvedSet) where isAll=true means all species qualify.
// taxonomyDB may be nil if taxonomy is unavailable.
func resolveSpeciesFilter(configSpecies, labels []string, taxonomyDB *classifier.TaxonomyDatabase, locale, operationName string) (isAll bool, resolvedSet map[string]bool) {
	if len(configSpecies) == 0 {
		return true, nil
	}

	resolved := make(map[string]bool)

	// Build common name -> scientific name lookup from the model labels.
	commonToScientific := make(map[string]string)
	scientificNames := make(map[string]bool)
	for _, label := range labels {
		if sci, common, found := strings.Cut(label, "_"); found {
			sciLower := strings.ToLower(sci)
			commonLower := strings.ToLower(common)
			commonToScientific[commonLower] = sciLower
			scientificNames[sciLower] = true
		} else if sci := strings.ToLower(strings.TrimSpace(label)); sci != "" {
			// Scientific-only labels (bats, Perch-unique species) have no embedded
			// common name; index them by scientific name so a scientific-name config
			// entry still matches them.
			scientificNames[sci] = true
		}
	}

	// Config entries that none of the cheap lookups resolved; reverse-resolved
	// through OpenFauna in a single batch pass after the loop.
	var unresolved []string

	for _, entry := range configSpecies {
		entryLower := strings.ToLower(strings.TrimSpace(entry))

		// Try as scientific name first (cheap map lookup, no side effects)
		if scientificNames[entryLower] {
			resolved[entryLower] = true
			continue
		}

		// Try as common name (cheap map lookup, no side effects)
		if sci, ok := commonToScientific[entryLower]; ok {
			resolved[sci] = true
			continue
		}

		// Try taxonomy lookups if database is available.
		// Use non-telemetry Lookup* methods to avoid Sentry noise for
		// localized common names that don't match any genus/family/order.
		if taxonomyDB != nil {
			// Try as genus name
			if genusSpecies := taxonomyDB.LookupAllSpeciesInGenus(entry); genusSpecies != nil {
				for _, sp := range genusSpecies {
					resolved[strings.ToLower(sp)] = true
				}
				continue
			}

			// Try as family name
			if familySpecies := taxonomyDB.LookupAllSpeciesInFamily(entry); familySpecies != nil {
				for _, sp := range familySpecies {
					resolved[strings.ToLower(sp)] = true
				}
				continue
			}

			// Try as order name
			if orderSpecies := taxonomyDB.LookupAllSpeciesInOrder(entry); orderSpecies != nil {
				for _, sp := range orderSpecies {
					resolved[strings.ToLower(sp)] = true
				}
				continue
			}
		}

		// Defer to the OpenFauna reverse lookup below.
		unresolved = append(unresolved, entry)
	}

	// Reverse-resolve any still-unresolved entries through OpenFauna in a single
	// cold-path pass. This canonicalizes localized common names of secondary-model
	// species (e.g. Finnish "mopsilepakko" -> "Barbastella barbastellus") that have
	// no embedded common name in the model labels and are not in the taxonomy DB.
	if len(unresolved) > 0 {
		// The shared helper returns scientific names already lower-cased, keyed per
		// entry so the per-entry "matched" tracking (and the unresolved warning below)
		// still works; it centralizes the lower-casing/locale handling shared with the
		// range-filter exclude matcher.
		reverse := openfauna.ReverseResolveToScientificNames(unresolved, locale)
		stillUnresolved := unresolved[:0]
		for _, entry := range unresolved {
			matched := false
			for _, sci := range reverse[entry] {
				// Only resolve to species a loaded model can actually emit. OpenFauna
				// may return scientific names for the localized common name that are
				// not in any loaded model's labels; resolving to those would silently
				// match a species nothing can detect and skip the unresolved warning.
				if !scientificNames[sci] {
					continue
				}
				resolved[sci] = true
				matched = true
			}
			if !matched {
				stillUnresolved = append(stillUnresolved, entry)
			}
		}
		unresolved = stillUnresolved
	}

	// Anything still unresolved is a likely config typo; warn so users can spot it.
	for _, entry := range unresolved {
		GetLogger().Warn("Species filter entry not resolved",
			logger.String("entry", entry),
			logger.String("operation", operationName+"_species_filter"))
	}

	return false, resolved
}

// normalizeDetectionTimes sets BeginTime/EndTime on an approved detection.
// BeginTime is always backdated to FirstDetected so the audio clip starts from
// the beginning of the event. EndTime handling depends on capture mode:
//   - Extended captures: EndTime = LastUpdated + normal detection window (spans full session)
//   - Normal captures: EndTime = FirstDetected + normal detection window (configured length)
//
// For extended captures the clip name is regenerated to reflect the actual duration.
func (p *Processor) normalizeDetectionTimes(item *PendingDetection) {
	settings := p.currentSettings()
	item.Detection.Result.BeginTime = item.FirstDetected

	captureLength := time.Duration(settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(settings.Realtime.Audio.Export.PreCapture) * time.Second
	normalDetectionWindow := max(time.Duration(0), captureLength-preCaptureLength)

	if item.ExtendedCapture {
		// For extended captures, EndTime reflects the last detection + normal detection window.
		// LastUpdated is always initialized (set on creation and every re-detection).
		item.Detection.Result.EndTime = item.LastUpdated.Add(normalDetectionWindow)

		// Regenerate clip name with actual duration (unknown at createDetection time)
		preCapture := settings.Realtime.Audio.Export.PreCapture
		durationSeconds := int(item.Detection.Result.EndTime.Sub(item.Detection.Result.BeginTime).Seconds()) + preCapture
		item.Detection.Result.ClipName = p.generateClipNameWithDuration(
			settings,
			item.Detection.Result.Species.ScientificName,
			float32(item.Confidence),
			durationSeconds,
			item.Detection.Result.Timestamp,
		)
	} else {
		// For non-extended detections, recalculate EndTime to maintain the configured
		// capture window. BeginTime was backdated to FirstDetected, but EndTime may
		// come from a later higher-confidence detection that replaced the Detection
		// struct during the pending window. Without this recalculation, the time span
		// (EndTime - BeginTime) inflates beyond the configured capture length.
		item.Detection.Result.EndTime = item.FirstDetected.Add(normalDetectionWindow)
	}
}

// applyExtendedCapture applies extended capture logic to a pending detection.
// It sets the ExtendedCapture flag, MaxDeadline, and calculates the scaled flush deadline.
// This is called from processDetections after the pending detection is created/updated.
// Must be called while pendingMutex is held.
func (p *Processor) applyExtendedCapture(mapKey string, now time.Time, normalDetectionWindow time.Duration) {
	settings := p.currentSettings()
	item := p.pendingDetections[mapKey]
	maxDuration := time.Duration(settings.Realtime.ExtendedCapture.MaxDuration) * time.Second

	if !item.ExtendedCapture {
		// First time: set extended capture flag and absolute deadline
		item.ExtendedCapture = true
		item.MaxDeadline = item.FirstDetected.Add(maxDuration)
	}

	item.FlushDeadline = calculateExtendedFlushDeadline(
		now, item.FirstDetected, item.MaxDeadline, normalDetectionWindow,
	)

	p.pendingDetections[mapKey] = item
}

// calculateExtendedFlushDeadline computes the next flush deadline for an extended capture
// detection using the scaled timeout algorithm. The deadline scales with session duration:
//   - Short (<30s): max(15s, normalDetectionWindow)
//   - Medium (30s-2m): 30s after now
//   - Long (>2m): 60s after now
//
// The result is always capped at maxDeadline to enforce the absolute maximum duration.
func calculateExtendedFlushDeadline(now, firstDetected, maxDeadline time.Time, normalDetectionWindow time.Duration) time.Time {
	sessionDuration := now.Sub(firstDetected)

	var deadline time.Time
	switch {
	case sessionDuration < extendedCaptureMediumThreshold:
		deadline = now.Add(max(normalDetectionWindow, extendedCaptureMinInitialWait))
	case sessionDuration < extendedCaptureLongThreshold:
		deadline = now.Add(extendedCaptureMediumWait)
	default:
		deadline = now.Add(extendedCaptureLongWait)
	}

	if deadline.After(maxDeadline) {
		deadline = maxDeadline
	}

	return deadline
}
