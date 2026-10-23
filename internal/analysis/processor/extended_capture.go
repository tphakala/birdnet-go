package processor

import (
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
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

	isAll, resolved := resolveSpeciesFilter(
		settings.Realtime.ExtendedCapture.Species, labels, taxonomyDB, locale, "extended_capture",
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
// A Perch multi-word sound class is keyed by its full lowercased label and can be
// referenced by that label or by its spaced name.
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
		if id, display, ok := nonbird.MultiWordLabelNames(label); ok {
			// Perch v2 multi-word sound class: the whole label is the scientific key.
			idLower := strings.ToLower(id)
			commonToScientific[strings.ToLower(display)] = idLower
			scientificNames[idLower] = true
		} else if sci, common, found := strings.Cut(label, "_"); found {
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
//   - Extended captures: EndTime = LastUpdated + normal detection window (spans full session),
//     with LastUpdated capped so EndTime stops at MaxDeadline once MaxDuration is at
//     least the normal window (see detectionEndTime)
//   - Normal captures: EndTime = FirstDetected + normal detection window (configured length)
//
// EndTime comes from detectionEndTime, read with the caller's settings snapshot so the
// saved span matches the one the privacy check used.
// For extended captures the clip name is regenerated to reflect the actual duration,
// with its container chosen from the Result's own model and sample rate, and the
// planned clip sample rate is recomputed from it.
func (p *Processor) normalizeDetectionTimes(item *PendingDetection, settings *conf.Settings) {
	item.Detection.Result.BeginTime = item.FirstDetected

	item.Detection.Result.EndTime = detectionEndTime(settings, item)

	// For non-extended detections EndTime is recalculated from FirstDetected so the
	// time span (EndTime - BeginTime) keeps the configured capture length even when
	// a later higher-confidence detection replaced the Detection struct during the
	// pending window. For extended captures it reflects LastUpdated (set on
	// creation and every re-detection) + the normal detection window, with
	// LastUpdated capped at MaxDeadline minus the window (see detectionEndTime).
	if item.ExtendedCapture {
		// Regenerate clip name with the actual duration (unknown at createDetection
		// time), but only when export is enabled. When export is off the clip name
		// stays empty so it remains a truthful per-detection signal (no file will be
		// written); regenerating it here would reintroduce a ghost reference.
		if settings.Realtime.Audio.Export.Enabled {
			preCapture := settings.Realtime.Audio.Export.PreCapture
			durationSeconds := int(item.Detection.Result.EndTime.Sub(item.Detection.Result.BeginTime).Seconds()) + preCapture
			clipName := p.generateClipNameWithDuration(
				settings,
				item.Detection.Result.Species.ScientificName,
				float32(item.Confidence),
				durationSeconds,
				item.Detection.Result.Timestamp,
			)
			// Reapply the export-format extension so the stored ClipName records the
			// container: a bat/ultrasonic detection above the analysis rate is named
			// in UltrasonicType, not the configured Export.Type. The model and rate
			// come from the Result, as in createDetection's resolveClipName.
			item.Detection.Result.ClipName = p.applyExportFormatExtension(
				settings, clipName, &item.Detection.Result)
			// The clip may not have existed when the rate was planned (export was
			// disabled then), so plan it again from the final clip name.
			item.Detection.Result.ClipSampleRate = plannedClipSampleRate(
				&item.Detection.Result, &settings.Realtime.Audio.Export)
		}
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
// The result is always capped at maxDeadline. The privacy filter may still hold the
// detection after that deadline until its clip is scanned, up to the clip end plus
// the coverage wait; detectionEndTime caps the end time at the same bound, and the
// saved clip ends PreCapture after it.
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
