package processor

import (
	"fmt"
	"math"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Discard reasons for a pending detection that did not reach its confirmation count.
// They prefix the discard_detection log's reason, which the detection events API
// aggregates, so a rarity rejection is reported separately from a false positive.
const (
	reasonFalsePositive = "false positive"
	reasonRarityFilter  = "rarity filter"
)

// minDetectionRequirement is the confirmation count a pending detection must reach,
// and whether the rarity filter raised it above the false positive filter's count.
type minDetectionRequirement struct {
	count         int
	rarityApplied bool
}

// discardReason formats the discard reason for a detection that matched only
// matched times against this requirement.
func (r minDetectionRequirement) discardReason(matched int) string {
	reason := reasonFalsePositive
	if r.rarityApplied {
		reason = reasonRarityFilter
	}
	return fmt.Sprintf("%s, matched %d/%d times", reason, matched, r.count)
}

// rarityMinDetections resolves the minimum confirmation count the rarity filter
// requires for a species with the given occurrence probability. The tightest band
// the occurrence falls under (the matching band with the smallest MaxOccurrence)
// applies, so the result does not depend on the order of rf.Bands.
//
// Returns 0 (no override) when the filter is disabled, when no band matches, or when
// the occurrence is not a genuine score (occurrenceValid false, or NaN), i.e. the
// detection carries the synthetic zero from a missing location or range filter, so
// "no data" is never mistaken for "extremely rare" (#3935).
func rarityMinDetections(rf *conf.RarityFilterSettings, occurrenceValid bool, occurrence float64) int {
	if !rf.Enabled || !occurrenceValid || math.IsNaN(occurrence) {
		return 0
	}

	minDetections := 0
	tightest := 0.0
	found := false
	for _, band := range rf.Bands {
		if occurrence >= band.MaxOccurrence {
			continue
		}
		if !found || band.MaxOccurrence < tightest {
			found = true
			tightest = band.MaxOccurrence
			minDetections = band.MinDetections
		}
	}
	return minDetections
}

// effectiveMinDetections returns the confirmation count a pending detection must reach
// before it is accepted: the false positive filter's count for the item's model,
// raised to the rarity filter's count when that is higher. The rarity filter never
// lowers the count, and it skips the bat model, whose species the range filter does
// not score.
func effectiveMinDetections(settings *conf.Settings, item *PendingDetection) minDetectionRequirement {
	fpCount := calculateMinDetectionsForModel(settings, item.BestModelID)
	if item.BestModelID == classifier.RegistryIDBat {
		return minDetectionRequirement{count: fpCount}
	}

	result := &item.Detection.Result
	rarityCount := rarityMinDetections(&settings.Realtime.RarityFilter, result.OccurrenceValid, result.Occurrence)
	if rarityCount > fpCount {
		return minDetectionRequirement{count: rarityCount, rarityApplied: true}
	}
	return minDetectionRequirement{count: fpCount}
}
