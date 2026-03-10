package processor

import (
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/logger"
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
func (p *Processor) getTaxonomyDB() *birdnet.TaxonomyDatabase {
	p.taxonomyDBOnce.Do(func() {
		db, err := birdnet.LoadTaxonomyDatabase()
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

// initExtendedCapture resolves the extended capture species filter at startup.
// Called from Processor.New(). Safe to re-call on settings refresh.
func (p *Processor) initExtendedCapture() {
	if !p.Settings.Realtime.ExtendedCapture.Enabled {
		p.extendedCaptureMu.Lock()
		p.extendedCaptureAll = false
		p.extendedCaptureSpecies = nil
		p.extendedCaptureMu.Unlock()
		return
	}

	// Get BirdNET labels for common name resolution
	var labels []string
	if p.Bn != nil && p.Bn.Settings != nil {
		labels = p.Bn.Settings.BirdNET.Labels
	}

	// Get cached taxonomy database for genus/family/order resolution
	taxonomyDB := p.getTaxonomyDB()

	isAll, resolved := resolveSpeciesFilter(
		p.Settings.Realtime.ExtendedCapture.Species, labels, taxonomyDB, "extended_capture",
	)

	p.extendedCaptureMu.Lock()
	p.extendedCaptureAll = isAll
	p.extendedCaptureSpecies = resolved
	p.extendedCaptureMu.Unlock()

	if isAll {
		GetLogger().Info("Extended capture enabled for all species",
			logger.Int("max_duration_seconds", p.Settings.Realtime.ExtendedCapture.MaxDuration),
			logger.String("operation", "extended_capture_init"))
	} else {
		GetLogger().Info("Extended capture enabled for filtered species",
			logger.Int("species_count", len(resolved)),
			logger.Int("max_duration_seconds", p.Settings.Realtime.ExtendedCapture.MaxDuration),
			logger.String("operation", "extended_capture_init"))
	}
}

// isExtendedCaptureSpecies checks if a species qualifies for extended capture.
func (p *Processor) isExtendedCaptureSpecies(scientificName string) bool {
	if !p.Settings.Realtime.ExtendedCapture.Enabled {
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
func resolveSpeciesFilter(configSpecies, labels []string, taxonomyDB *birdnet.TaxonomyDatabase, operationName string) (isAll bool, resolvedSet map[string]bool) {
	if len(configSpecies) == 0 {
		return true, nil
	}

	resolved := make(map[string]bool)

	// Build common name -> scientific name lookup from BirdNET labels
	commonToScientific := make(map[string]string)
	scientificNames := make(map[string]bool)
	for _, label := range labels {
		if sci, common, found := strings.Cut(label, "_"); found {
			sciLower := strings.ToLower(sci)
			commonLower := strings.ToLower(common)
			commonToScientific[commonLower] = sciLower
			scientificNames[sciLower] = true
		}
	}

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
		// These are tried after common/scientific name lookups because
		// failed taxonomy lookups generate telemetry errors via the
		// error builder, causing noise for localized common names that
		// don't match any genus/family/order.
		if taxonomyDB != nil {
			// Try as genus name
			if genusSpecies, err := taxonomyDB.GetAllSpeciesInGenus(entry); err == nil {
				for _, sp := range genusSpecies {
					resolved[strings.ToLower(sp)] = true
				}
				continue
			}

			// Try as family name
			if familySpecies, err := taxonomyDB.GetAllSpeciesInFamily(entry); err == nil {
				for _, sp := range familySpecies {
					resolved[strings.ToLower(sp)] = true
				}
				continue
			}

			// Try as order name
			if orderSpecies, err := taxonomyDB.GetAllSpeciesInOrder(entry); err == nil {
				for _, sp := range orderSpecies {
					resolved[strings.ToLower(sp)] = true
				}
				continue
			}
		}

		// Unknown entry — log warning so users can spot config typos
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
	item.Detection.Result.BeginTime = item.FirstDetected

	captureLength := time.Duration(p.Settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(p.Settings.Realtime.Audio.Export.PreCapture) * time.Second
	normalDetectionWindow := max(time.Duration(0), captureLength-preCaptureLength)

	if item.ExtendedCapture {
		// For extended captures, EndTime reflects the last detection + normal detection window.
		// LastUpdated is always initialized (set on creation and every re-detection).
		item.Detection.Result.EndTime = item.LastUpdated.Add(normalDetectionWindow)

		// Regenerate clip name with actual duration (unknown at createDetection time)
		preCapture := p.Settings.Realtime.Audio.Export.PreCapture
		durationSeconds := int(item.Detection.Result.EndTime.Sub(item.Detection.Result.BeginTime).Seconds()) + preCapture
		item.Detection.Result.ClipName = p.generateClipNameWithDuration(
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
	item := p.pendingDetections[mapKey]
	maxDuration := time.Duration(p.Settings.Realtime.ExtendedCapture.MaxDuration) * time.Second

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
