package processor

import (
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// SetSunCalc injects the sun calculator into the processor and initializes
// the daylight filter species list. This is called after processor creation
// when the suncalc instance becomes available.
func (p *Processor) SetSunCalc(sc *suncalc.SunCalc) {
	p.sunCalc = sc
	p.initDaylightFilter()
}

// initDaylightFilter resolves the daylight filter species list at startup.
// Follows the same pattern as initExtendedCapture(). Safe to re-call on settings refresh.
func (p *Processor) initDaylightFilter() {
	if !p.Settings.Realtime.DaylightFilter.Enabled {
		p.daylightFilterMu.Lock()
		p.daylightFilterAll = false
		p.daylightFilterSpecies = nil
		p.daylightFilterMu.Unlock()
		return
	}

	// Validate coordinates: lat/lon 0,0 means unconfigured location
	if p.Settings.BirdNET.Latitude == 0 && p.Settings.BirdNET.Longitude == 0 {
		GetLogger().Warn("Daylight filter enabled but location not configured (lat/lon 0,0), filter will not be active",
			logger.String("operation", "daylight_filter_init"))
		p.daylightFilterMu.Lock()
		p.daylightFilterAll = false
		p.daylightFilterSpecies = nil
		p.daylightFilterMu.Unlock()
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
		p.Settings.Realtime.DaylightFilter.Species, labels, taxonomyDB, "daylight_filter",
	)

	// For an exclusionary filter, empty species list means "filter nothing",
	// not "filter everything". Override the isAll=true default from resolveSpeciesFilter.
	if isAll {
		GetLogger().Warn("Daylight filter has empty species list, no species will be filtered",
			logger.String("operation", "daylight_filter_init"))
		p.daylightFilterMu.Lock()
		p.daylightFilterAll = false
		p.daylightFilterSpecies = nil
		p.daylightFilterMu.Unlock()
		return
	}

	p.daylightFilterMu.Lock()
	p.daylightFilterAll = false
	p.daylightFilterSpecies = resolved
	p.daylightFilterMu.Unlock()

	GetLogger().Info("Daylight filter enabled for filtered species",
		logger.Int("species_count", len(resolved)),
		logger.Int("offset_hours", p.Settings.Realtime.DaylightFilter.Offset),
		logger.String("operation", "daylight_filter_init"))
}

// isDaylightFilterSpecies checks if a species is in the daylight filter set.
func (p *Processor) isDaylightFilterSpecies(scientificName string) bool {
	if !p.Settings.Realtime.DaylightFilter.Enabled {
		return false
	}

	p.daylightFilterMu.RLock()
	defer p.daylightFilterMu.RUnlock()

	if p.daylightFilterAll {
		return true
	}

	return p.daylightFilterSpecies[strings.ToLower(scientificName)]
}

// isDaylight checks if a time falls within the daylight window.
// The daylight window is defined as [CivilDawn + offset, CivilDusk - offset).
// A positive offset shrinks the window (more lenient), a negative offset expands it (stricter).
func (p *Processor) isDaylight(t time.Time) (bool, error) {
	if p.sunCalc == nil {
		return false, fmt.Errorf("sun calculator not initialized")
	}

	sunTimes, err := p.sunCalc.GetSunEventTimes(t)
	if err != nil {
		return false, err
	}

	offset := time.Duration(p.Settings.Realtime.DaylightFilter.Offset) * time.Hour
	daylightStart := sunTimes.CivilDawn.Add(offset)
	daylightEnd := sunTimes.CivilDusk.Add(-offset)

	// Guard: if offset inverts the window, no time is considered daylight
	if !daylightStart.Before(daylightEnd) {
		return false, nil
	}

	// t is in [daylightStart, daylightEnd)
	return !t.Before(daylightStart) && t.Before(daylightEnd), nil
}

// checkDaylightFilter returns true if the detection should be discarded.
// A detection is discarded when the species is in the filter set AND the
// detection time falls within the daylight window. Fails open on suncalc errors.
func (p *Processor) checkDaylightFilter(scientificName string, detectionTime time.Time) bool {
	if !p.Settings.Realtime.DaylightFilter.Enabled {
		return false
	}

	if !p.isDaylightFilterSpecies(scientificName) {
		return false
	}

	daylight, err := p.isDaylight(detectionTime)
	if err != nil {
		// Fail open: if we can't determine daylight, don't discard
		GetLogger().Warn("Failed to determine daylight status, allowing detection",
			logger.Any("error", err),
			logger.String("species", scientificName),
			logger.String("operation", "daylight_filter_check"))
		return false
	}

	return daylight
}
