package processor

import (
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
)

// Extended capture timeout thresholds.
const (
	extendedCaptureMinInitialWait  = 15 * time.Second
	extendedCaptureMediumThreshold = 30 * time.Second
	extendedCaptureMediumWait      = 30 * time.Second
	extendedCaptureLongThreshold   = 2 * time.Minute
	extendedCaptureLongWait        = 60 * time.Second
)

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
func resolveSpeciesFilter(configSpecies, labels []string, taxonomyDB *birdnet.TaxonomyDatabase) (isAll bool, resolvedSet map[string]bool) {
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

		// Try taxonomy lookups if database is available
		if taxonomyDB != nil {
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

		// Try as scientific name
		if scientificNames[entryLower] {
			resolved[entryLower] = true
			continue
		}

		// Try as common name
		if sci, ok := commonToScientific[entryLower]; ok {
			resolved[sci] = true
			continue
		}

		// Unknown entry - skip silently (logging will be added when wired into processor)
	}

	return false, resolved
}

// applyExtendedCapture applies extended capture logic to a pending detection.
// It sets the ExtendedCapture flag, MaxDeadline, and calculates the scaled flush deadline.
// This is called from processDetections after the pending detection is created/updated.
func applyExtendedCapture(p *Processor, mapKey string, now time.Time, normalDetectionWindow time.Duration) {
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
