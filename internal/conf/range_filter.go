package conf

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// speciesListMutex serializes clone-mutate-publish operations on range filter
// fields (Species, LastUpdated) so that concurrent writers do not lose each
// other's updates. Readers no longer need this mutex because published
// snapshots are immutable.
var speciesListMutex sync.Mutex

// UpdateIncludedSpecies clones the current settings, replaces the species
// list, builds the O(1) scientific name lookup map, and publishes a new
// immutable snapshot.
func UpdateIncludedSpecies(species []string) {
	speciesListMutex.Lock()
	defer speciesListMutex.Unlock()

	current := GetSettings()
	if current == nil {
		return
	}

	updated := CloneSettings(current)
	updated.BirdNET.RangeFilter.Species = make([]string, len(species))
	copy(updated.BirdNET.RangeFilter.Species, species)

	sciNames := make(map[string]struct{}, len(species))
	for _, label := range species {
		sci := label
		if idx := strings.IndexByte(label, '_'); idx >= 0 {
			sci = label[:idx]
		}
		sciNames[strings.ToLower(sci)] = struct{}{}
	}
	updated.BirdNET.RangeFilter.IncludedScientificNames = sciNames

	updated.BirdNET.RangeFilter.LastUpdated = time.Now()
	StoreSettings(updated)
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
		sci := result
		if idx := strings.IndexByte(result, '_'); idx >= 0 {
			sci = result[:idx]
		}
		_, found := s.BirdNET.RangeFilter.IncludedScientificNames[strings.ToLower(sci)]
		return found
	}
	for _, fullSpeciesString := range s.BirdNET.RangeFilter.Species {
		if strings.HasPrefix(fullSpeciesString, result) {
			return true
		}
	}
	return false
}

// ShouldUpdateRangeFilterToday atomically checks whether the range filter
// should be updated today and, if so, publishes a new snapshot with
// LastUpdated set to today. Only the first caller on a given day gets true;
// subsequent callers see the updated snapshot and return false.
//
// This solves GitHub issue #1357 where species appeared in detections that
// weren't in the range filter due to concurrent range filter updates.
func ShouldUpdateRangeFilterToday() bool {
	speciesListMutex.Lock()
	defer speciesListMutex.Unlock()

	current := GetSettings()
	if current == nil {
		return false
	}

	today := time.Now().Truncate(24 * time.Hour)
	if !current.BirdNET.RangeFilter.LastUpdated.Before(today) {
		return false
	}

	updated := CloneSettings(current)
	updated.BirdNET.RangeFilter.LastUpdated = today
	StoreSettings(updated)

	if current.Debug {
		GetLogger().Debug("Scheduled range filter update",
			logger.String("date", today.Format(time.DateOnly)),
			logger.String("last_updated", current.BirdNET.RangeFilter.LastUpdated.Format(time.DateTime)))
	}

	return true
}

// GetLastRangeFilterUpdate returns the last time the range filter was updated
// from this snapshot. The snapshot is immutable, so no mutex is needed.
func (s *Settings) GetLastRangeFilterUpdate() time.Time {
	return s.BirdNET.RangeFilter.LastUpdated
}

// ResetRangeFilterUpdateFlag clones the current settings, zeros the
// LastUpdated timestamp to allow retry of failed updates, and publishes
// a new immutable snapshot.
func ResetRangeFilterUpdateFlag() {
	speciesListMutex.Lock()
	defer speciesListMutex.Unlock()

	current := GetSettings()
	if current == nil {
		return
	}

	updated := CloneSettings(current)
	updated.BirdNET.RangeFilter.LastUpdated = time.Time{}
	StoreSettings(updated)
}
