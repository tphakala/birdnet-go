// internal/api/v2/events_aggregation.go
package api

import (
	"fmt"
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger/reader"
)

// topDiscardedCount is the number of most-discarded species to include in metrics.
const topDiscardedCount = 3

// bucketKeyFormat is the time format used for hourly bucket keys (e.g., "2006-01-02T15").
const bucketKeyFormat = "2006-01-02T15"

// bucketLabelFormat is the human-readable label for buckets (e.g., "15:00–16:00").
const bucketLabelFormat = "15:00"

// speciesIndex tracks species entries within a bucket for O(1) lookup during construction.
type speciesIndex struct {
	byName map[string]int // name → index in bucket.Species
}

// aggregateDetectionEvents processes raw log entries into hourly bucketed detection data.
// Events are assigned to buckets based on their resolution timestamp (approve/discard/flush),
// not the pending creation time. The returned buckets are sorted newest-first.
func (c *Controller) aggregateDetectionEvents(entries []reader.LogEntry, _ time.Time) DetectionEventsResponse {
	bucketMap := make(map[string]*DetectionBucket)
	speciesIdxMap := make(map[string]*speciesIndex) // bucket key → species index
	var hourlyPending [24]int
	speciesMap := make(map[string]*DetectionSpeciesSummary)
	discardCounts := make(map[string]int) // species → total discard count

	for i := range entries {
		entry := &entries[i]

		switch entry.Operation {
		case "create_pending_detection":
			hour := entry.Time.Hour()
			hourlyPending[hour]++

		case "approve_detection":
			bucketKey := toBucketKey(entry.Time)
			bucket := getOrCreateBucket(bucketMap, speciesIdxMap, entry.Time, bucketKey)
			speciesName := getStringField(entry.Fields, "species")
			if speciesName == "" {
				continue
			}
			sp := getOrCreateSpecies(speciesIdxMap[bucketKey], bucket, speciesName)
			sp.Approved++

			if conf := getFloat64Field(entry.Fields, "confidence"); conf > sp.PeakConfidence {
				sp.PeakConfidence = conf
			}
			if mc := getIntField(entry.Fields, "match_count"); mc > sp.MaxMatchCount {
				sp.MaxMatchCount = mc
			}

			sp.ApproveTimestamps = append(sp.ApproveTimestamps, entry.Time.Format(time.RFC3339))

			summary := getOrCreateSummary(speciesMap, speciesName)
			summary.Approved++
			summary.Total++

		case "discard_detection":
			bucketKey := toBucketKey(entry.Time)
			bucket := getOrCreateBucket(bucketMap, speciesIdxMap, entry.Time, bucketKey)
			speciesName := getStringField(entry.Fields, "species")
			if speciesName == "" {
				continue
			}
			sp := getOrCreateSpecies(speciesIdxMap[bucketKey], bucket, speciesName)
			sp.Discarded++

			if conf := getFloat64Field(entry.Fields, "confidence"); conf > sp.PeakConfidence {
				sp.PeakConfidence = conf
			}
			if reason := getStringField(entry.Fields, "reason"); reason != "" {
				sp.DiscardReasons = append(sp.DiscardReasons, reason)
			}
			sp.DiscardTimestamps = append(sp.DiscardTimestamps, entry.Time.Format(time.RFC3339))

			summary := getOrCreateSummary(speciesMap, speciesName)
			summary.Discarded++
			summary.Total++

			discardCounts[speciesName]++

		case "flush_detection":
			bucketKey := toBucketKey(entry.Time)
			bucket := getOrCreateBucket(bucketMap, speciesIdxMap, entry.Time, bucketKey)
			speciesName := getStringField(entry.Fields, "species")
			if speciesName == "" {
				continue
			}
			sp := getOrCreateSpecies(speciesIdxMap[bucketKey], bucket, speciesName)
			sp.Flushed++

			summary := getOrCreateSummary(speciesMap, speciesName)
			summary.Total++

		case "dog_bark_filter":
			bucketKey := toBucketKey(entry.Time)
			bucket := getOrCreateBucket(bucketMap, speciesIdxMap, entry.Time, bucketKey)
			bucket.PreFilters.DogBark++

		case "privacy_filter":
			bucketKey := toBucketKey(entry.Time)
			bucket := getOrCreateBucket(bucketMap, speciesIdxMap, entry.Time, bucketKey)
			bucket.PreFilters.Privacy++

		case "audio_export_success":
			bucketKey := toBucketKey(entry.Time)
			bucket := getOrCreateBucket(bucketMap, speciesIdxMap, entry.Time, bucketKey)
			speciesName := getStringField(entry.Fields, "species")
			clipPath := getStringField(entry.Fields, "clip_path")
			if speciesName != "" && clipPath != "" {
				sp := getOrCreateSpecies(speciesIdxMap[bucketKey], bucket, speciesName)
				sp.ClipPaths = append(sp.ClipPaths, clipPath)
			}
		}
	}

	// Finalize buckets: compute totals, sort species, build slice
	buckets := make([]DetectionBucket, 0, len(bucketMap))
	for _, bucket := range bucketMap {
		finalizeBucket(bucket)
		buckets = append(buckets, *bucket)
	}

	// Sort buckets newest-first
	slices.SortFunc(buckets, func(a, b DetectionBucket) int {
		return b.Timestamp.Compare(a.Timestamp)
	})

	// Build metrics
	metrics := buildDetectionMetrics(buckets, &hourlyPending, discardCounts)

	// Build sorted species summary
	speciesList := buildDetectionSpeciesSummaryList(speciesMap)

	return DetectionEventsResponse{
		Buckets: buckets,
		Metrics: metrics,
		Species: speciesList,
	}
}

// toBucketKey returns the bucket key for a given timestamp (floored to the hour).
func toBucketKey(t time.Time) string {
	return t.Truncate(time.Hour).Format(bucketKeyFormat)
}

// getOrCreateBucket returns the bucket for the given key, creating it if necessary.
func getOrCreateBucket(bucketMap map[string]*DetectionBucket, idxMap map[string]*speciesIndex, t time.Time, key string) *DetectionBucket {
	if b, ok := bucketMap[key]; ok {
		return b
	}

	floored := t.Truncate(time.Hour)
	nextHour := floored.Add(time.Hour)
	label := fmt.Sprintf("%s\u2013%s", floored.Format(bucketLabelFormat), nextHour.Format(bucketLabelFormat))

	b := &DetectionBucket{
		Key:       key,
		Label:     label,
		Timestamp: floored,
		Species:   []SpeciesEntry{},
	}
	bucketMap[key] = b
	idxMap[key] = &speciesIndex{
		byName: make(map[string]int),
	}
	return b
}

// getOrCreateSpecies returns a pointer to the species entry within the bucket,
// creating it if it does not yet exist. Uses the species index for O(1) lookup.
func getOrCreateSpecies(idx *speciesIndex, bucket *DetectionBucket, name string) *SpeciesEntry {
	if i, ok := idx.byName[name]; ok {
		return &bucket.Species[i]
	}

	bucket.Species = append(bucket.Species, SpeciesEntry{
		Name:              name,
		DiscardReasons:    []string{},
		DiscardTimestamps: []string{},
		ApproveTimestamps: []string{},
		ClipPaths:         []string{},
	})
	newIdx := len(bucket.Species) - 1
	idx.byName[name] = newIdx
	return &bucket.Species[newIdx]
}

// getOrCreateSummary returns the species summary entry, creating it if needed.
func getOrCreateSummary(m map[string]*DetectionSpeciesSummary, name string) *DetectionSpeciesSummary {
	if s, ok := m[name]; ok {
		return s
	}
	s := &DetectionSpeciesSummary{Name: name}
	m[name] = s
	return s
}

// finalizeBucket computes totals, approve ratio, species count, and sorts species.
func finalizeBucket(bucket *DetectionBucket) {
	var approved, discarded, flushed int
	for i := range bucket.Species {
		sp := &bucket.Species[i]
		approved += sp.Approved
		discarded += sp.Discarded
		flushed += sp.Flushed
	}

	bucket.Totals = BucketTotals{
		Pending:   approved + discarded + flushed,
		Approved:  approved,
		Discarded: discarded,
		Flushed:   flushed,
	}

	if bucket.Totals.Pending > 0 {
		bucket.ApproveRatio = float64(approved) / float64(bucket.Totals.Pending)
	}

	// Sort species: approved species first, then by total count descending
	slices.SortFunc(bucket.Species, func(a, b SpeciesEntry) int {
		aTotal := a.Approved + a.Discarded + a.Flushed
		bTotal := b.Approved + b.Discarded + b.Flushed

		// Approved species first
		aHasApproved := a.Approved > 0
		bHasApproved := b.Approved > 0
		if aHasApproved != bHasApproved {
			if aHasApproved {
				return -1
			}
			return 1
		}

		// Then by total count descending
		if bTotal != aTotal {
			return bTotal - aTotal
		}
		return 0
	})

	bucket.SpeciesCount = len(bucket.Species)
}

// buildDetectionMetrics computes day-level aggregate metrics from finalized buckets.
func buildDetectionMetrics(buckets []DetectionBucket, hourlyPending *[24]int, discardCounts map[string]int) DetectionMetrics {
	var pendingTotal, approvedTotal, discardedTotal, flushedTotal int
	var dogBarkTotal, privacyTotal int
	activeHours := 0

	for i := range buckets {
		b := &buckets[i]
		pendingTotal += b.Totals.Pending
		approvedTotal += b.Totals.Approved
		discardedTotal += b.Totals.Discarded
		flushedTotal += b.Totals.Flushed
		dogBarkTotal += b.PreFilters.DogBark
		privacyTotal += b.PreFilters.Privacy
		activeHours++
	}

	// Top discarded species
	topDiscarded := buildTopDiscarded(discardCounts)

	// Approved per hour
	approvedPerHour := "0.0"
	if activeHours > 0 {
		approvedPerHour = fmt.Sprintf("%.1f", float64(approvedTotal)/float64(activeHours))
	}

	return DetectionMetrics{
		PendingTotal:    pendingTotal,
		ApprovedTotal:   approvedTotal,
		DiscardedTotal:  discardedTotal,
		FlushedTotal:    flushedTotal,
		DogBarkTotal:    dogBarkTotal,
		PrivacyTotal:    privacyTotal,
		TopDiscarded:    topDiscarded,
		HourlyPending:   *hourlyPending,
		ApprovedPerHour: approvedPerHour,
	}
}

// buildTopDiscarded returns the top N most-discarded species sorted by count descending.
func buildTopDiscarded(discardCounts map[string]int) []SpeciesCount {
	if len(discardCounts) == 0 {
		return []SpeciesCount{}
	}

	all := make([]SpeciesCount, 0, len(discardCounts))
	for name, count := range discardCounts {
		all = append(all, SpeciesCount{Name: name, Count: count})
	}

	slices.SortFunc(all, func(a, b SpeciesCount) int {
		if b.Count != a.Count {
			return b.Count - a.Count
		}
		// Stable tie-break by name
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})

	if len(all) > topDiscardedCount {
		all = all[:topDiscardedCount]
	}
	return all
}

// buildDetectionSpeciesSummaryList converts the species summary map to a sorted slice.
// Sorted by total count descending.
func buildDetectionSpeciesSummaryList(m map[string]*DetectionSpeciesSummary) []DetectionSpeciesSummary {
	if len(m) == 0 {
		return []DetectionSpeciesSummary{}
	}

	list := make([]DetectionSpeciesSummary, 0, len(m))
	for _, s := range m {
		list = append(list, *s)
	}

	slices.SortFunc(list, func(a, b DetectionSpeciesSummary) int {
		if b.Total != a.Total {
			return b.Total - a.Total
		}
		// Stable tie-break by name
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})

	return list
}

// --- Field extraction helpers ---

// getStringField safely extracts a string value from a fields map.
func getStringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	if v, ok := fields[key].(string); ok {
		return v
	}
	return ""
}

// getFloat64Field safely extracts a float64 value from a fields map.
// JSON numbers decode as float64 via encoding/json; int is handled as a fallback.
func getFloat64Field(fields map[string]any, key string) float64 {
	if fields == nil {
		return 0
	}
	switch v := fields[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

// getIntField safely extracts an integer value from a fields map.
// JSON numbers decode as float64 via encoding/json, so this converts accordingly.
func getIntField(fields map[string]any, key string) int {
	if fields == nil {
		return 0
	}
	switch v := fields[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}
