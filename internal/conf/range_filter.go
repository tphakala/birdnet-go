package conf

import (
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// canonicalSci extracts the scientific-name portion of a species label
// ("ScientificName_CommonName" or scientific-name only; a full Perch multi-word
// sound class is kept whole), resolves it through the
// OpenFauna taxonomic alias map, and lowercases it. Building and querying the
// included-species set through the same canonical key lets a legacy classifier
// label (e.g. "Streptopelia senegalensis") match a geomodel that lists the
// species under its current name ("Spilopelia senegalensis"): the range filter
// maps the two by alias, so the inclusion gate must too, or the reclassified
// species would be dropped here after passing the range filter. A non-aliased
// name resolves to itself, so this is a no-op for species without a
// reclassification.
func canonicalSci(label string) string {
	// Mirror detection.ExtractScientificName's sanitization (trim, strip CR, then
	// split on the first underscore, with a full Perch multi-word sound class kept whole
	// via nonbird.MultiWordLabelNames) so this key matches the one the range-filter
	// species mapping builds for the same label, including labels that arrive with
	// trailing whitespace or CRLF line endings. The logic is duplicated rather than
	// shared because internal/detection imports internal/conf, so conf importing
	// detection would create an import cycle.
	label = strings.TrimSpace(label)
	label = strings.ReplaceAll(label, "\r", "")
	sci := label
	if id, _, ok := nonbird.MultiWordLabelNames(label); ok {
		sci = id
	} else if idx := strings.IndexByte(label, '_'); idx >= 0 {
		sci = label[:idx]
	}
	return strings.ToLower(openfauna.CanonicalName(sci))
}

// UpdateIncludedSpecies replaces the species list, builds the O(1) scientific
// name lookup map, and publishes a new immutable snapshot. It publishes through
// the settings writer lock (updateRuntimeSettings), so it builds on the snapshot
// that is current when it publishes and cannot discard a concurrent settings save
// or model change. It does not change the snapshot's Revision: the fields it
// writes are server-owned, so a client that missed them loses nothing.
func UpdateIncludedSpecies(species []string) {
	// Build the list copy and the lookup map before taking the settings writer
	// lock: canonicalizing every label of a geomodel list is the slow part, it
	// depends only on the argument, and it should not hold up other writers.
	speciesCopy := make([]string, len(species))
	copy(speciesCopy, species)
	sciNames := make(map[string]struct{}, len(species))
	for _, label := range species {
		sciNames[canonicalSci(label)] = struct{}{}
	}

	_, _, err := updateRuntimeSettings(func(_, updated *Settings) error {
		updated.BirdNET.RangeFilter.Species = speciesCopy
		updated.BirdNET.RangeFilter.IncludedScientificNames = sciNames
		updated.BirdNET.RangeFilter.LastUpdated = time.Now()
		return nil
	})
	if err != nil && !errors.Is(err, ErrSettingsNotLoaded) {
		GetLogger().Error("Failed to publish included species", logger.Error(err))
	}
}

// GetIncludedSpecies returns a copy of the included species list from this
// snapshot. The snapshot is immutable, so no mutex is needed.
func (s *Settings) GetIncludedSpecies() []string {
	return slices.Clone(s.BirdNET.RangeFilter.Species)
}

// IsSpeciesIncluded reports whether the given label is in the included species
// set. When the IncludedScientificNames map is populated (the fast path), it
// performs an O(1) lookup on the lowercased scientific name portion of the
// label. Labels may be in BirdNET format ("Parus major_Great Tit") or contain
// the scientific name only ("Parus major"). The legacy O(n) fallback is used
// when the map is empty (e.g. for snapshots loaded before this feature).
func (s *Settings) IsSpeciesIncluded(result string) bool {
	if len(s.BirdNET.RangeFilter.IncludedScientificNames) > 0 {
		// Query through the same canonical key the set was built with, so a legacy
		// detection label resolves to its current name and matches.
		_, found := s.BirdNET.RangeFilter.IncludedScientificNames[canonicalSci(result)]
		return found
	}
	for _, fullSpeciesString := range s.BirdNET.RangeFilter.Species {
		if strings.HasPrefix(fullSpeciesString, result) {
			return true
		}
	}
	return false
}

// LocalNoon returns 12:00:00 on the calendar day of t, evaluated in t's own time
// zone. Range-filter date logic uses it to anchor "today" on the local calendar
// day: time.Time.Truncate operates on absolute (UTC) time, so truncating to a 24h
// boundary rounds to UTC midnight and, near the local day boundary on hosts with a
// non-zero UTC offset, yields the wrong calendar day for the geomodel week lookup
// (and rolls the daily-update marker over at UTC rather than local midnight). Using
// noon rather than midnight also sidesteps zones whose DST transition is at 00:00.
func LocalNoon(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, t.Location())
}

// ShouldUpdateRangeFilterToday atomically checks whether the range filter
// should be updated today and, if so, publishes a new snapshot with
// LastUpdated set to today. Only the first caller on a given day gets true;
// subsequent callers see the updated snapshot and return false.
//
// This solves GitHub issue #1357 where species appeared in detections that
// weren't in the range filter due to concurrent range filter updates.
//
// The processor calls this for every detection, so the common "already stamped
// today" case is answered from a lock-free snapshot read. Only a caller that
// sees a stale stamp goes through the settings writer lock
// (updateRuntimeSettings, which keeps the snapshot's Revision), where the check is
// repeated against the current snapshot before stamping, so that exactly one of
// several racing callers publishes and gets true.
func ShouldUpdateRangeFilterToday() bool {
	today := LocalNoon(time.Now())
	if !rangeFilterStale(GetSettings(), today) {
		return false
	}

	var previous time.Time
	_, updated, err := updateRuntimeSettings(func(current, updated *Settings) error {
		if !rangeFilterStale(current, today) {
			return ErrSettingsUnchanged
		}
		previous = current.BirdNET.RangeFilter.LastUpdated
		updated.BirdNET.RangeFilter.LastUpdated = today
		return nil
	})
	if err != nil {
		return false
	}

	if updated.Debug {
		GetLogger().Debug("Scheduled range filter update",
			logger.String("date", today.Format(time.DateOnly)),
			logger.String("last_updated", previous.Format(time.DateTime)))
	}

	return true
}

// rangeFilterStale reports whether the range filter has not been stamped on the
// local calendar day of today. It returns false for a nil snapshot.
//
// It compares local-day to local-day. UpdateIncludedSpecies stamps LastUpdated
// with the wall-clock time.Now() of the rebuild, which is earlier than today's
// noon anchor for any rebuild that finishes in the local morning. Comparing the
// raw stamp against noon would re-open this gate on every detection until local
// noon, triggering a full geomodel rebuild each time. Bucketing LastUpdated to
// its own local noon keeps the "only the first caller on a given local day gets
// true" guarantee (issue #1357) regardless of what time the last rebuild ran.
func rangeFilterStale(s *Settings, today time.Time) bool {
	if s == nil {
		return false
	}
	return LocalNoon(s.BirdNET.RangeFilter.LastUpdated).Before(today)
}

// GetLastRangeFilterUpdate returns the last time the range filter was updated
// from this snapshot. The snapshot is immutable, so no mutex is needed.
func (s *Settings) GetLastRangeFilterUpdate() time.Time {
	return s.BirdNET.RangeFilter.LastUpdated
}

// ResetRangeFilterUpdateFlag zeros the LastUpdated timestamp to allow retry of
// failed updates, and publishes a new immutable snapshot.
func ResetRangeFilterUpdateFlag() {
	_, _, err := updateRuntimeSettings(func(_, updated *Settings) error {
		updated.BirdNET.RangeFilter.LastUpdated = time.Time{}
		return nil
	})
	if err != nil && !errors.Is(err, ErrSettingsNotLoaded) {
		GetLogger().Error("Failed to reset range filter update flag", logger.Error(err))
	}
}
