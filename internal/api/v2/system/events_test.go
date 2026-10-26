// events_test.go: system-domain events endpoint tests (extracted from package api).

package system

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/logger/reader"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

// baseTime is a fixed reference point for all event tests (2024-06-15 10:30:00 UTC).
var baseTime = time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

// makeEntry creates a reader.LogEntry for testing with sensible defaults.
func makeEntry(t time.Time, operation, species string, fields map[string]any) reader.LogEntry {
	if fields == nil {
		fields = make(map[string]any)
	}
	if species != "" {
		fields["species"] = species
	}
	return reader.LogEntry{
		Time:      t,
		Level:     "INFO",
		Msg:       "test message",
		Module:    "analysis.processor",
		Operation: operation,
		Fields:    fields,
	}
}

// --- Aggregation Logic Tests ---

func TestAggregateDetectionEvents(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	t.Run("empty entries returns empty response", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		result := c.aggregateDetectionEvents(nil)

		assert.Empty(t, result.Buckets)
		assert.Empty(t, result.Species)
		assert.Equal(t, 0, result.Metrics.ApprovedTotal)
		assert.Equal(t, 0, result.Metrics.DiscardedTotal)
		assert.Equal(t, 0, result.Metrics.FlushedTotal)
		assert.Equal(t, 0, result.Metrics.PendingTotal)
		assert.Equal(t, [24]int{}, result.Metrics.HourlyPending)
	})

	t.Run("single approve creates one bucket and one species", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{
				"confidence":  0.85,
				"match_count": float64(3),
			}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 1)
		bucket := result.Buckets[0]
		assert.Equal(t, "2024-06-15T10", bucket.Key)
		assert.Equal(t, "10:00\u201311:00", bucket.Label)
		require.Len(t, bucket.Species, 1)

		sp := bucket.Species[0]
		assert.Equal(t, "Robin", sp.Name)
		assert.Equal(t, 1, sp.Approved)
		assert.Equal(t, 0, sp.Discarded)
		assert.Equal(t, 0, sp.Flushed)
		assert.InDelta(t, 0.85, sp.PeakConfidence, 0.001)
		assert.Equal(t, 3, sp.MaxMatchCount)

		assert.Equal(t, 1, bucket.Totals.Approved)
		assert.Equal(t, 1, bucket.Totals.Pending) // pending = approved + discarded + flushed
		assert.InDelta(t, 1.0, bucket.ApproveRatio, 0.001)

		// Species summary
		require.Len(t, result.Species, 1)
		assert.Equal(t, "Robin", result.Species[0].Name)
		assert.Equal(t, 1, result.Species[0].Approved)
		assert.Equal(t, 1, result.Species[0].Total)
	})

	t.Run("multiple operations same bucket same species accumulate", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Sparrow", map[string]any{"confidence": 0.7, "match_count": float64(1)}),
			makeEntry(baseTime.Add(5*time.Minute), "discard_detection", "Sparrow", map[string]any{"confidence": 0.3, "reason": "low_confidence"}),
			makeEntry(baseTime.Add(10*time.Minute), "flush_detection", "Sparrow", nil),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 1)
		bucket := result.Buckets[0]
		require.Len(t, bucket.Species, 1)

		sp := bucket.Species[0]
		assert.Equal(t, 1, sp.Approved)
		assert.Equal(t, 1, sp.Discarded)
		assert.Equal(t, 1, sp.Flushed)
		assert.InDelta(t, 0.7, sp.PeakConfidence, 0.001) // max of 0.7 and 0.3
		assert.Equal(t, []string{"low_confidence"}, sp.DiscardReasons)

		assert.Equal(t, 3, bucket.Totals.Pending)
		assert.Equal(t, 1, bucket.Totals.Approved)
		assert.Equal(t, 1, bucket.Totals.Discarded)
		assert.Equal(t, 1, bucket.Totals.Flushed)
	})

	t.Run("multiple species same bucket grouped correctly", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(2)}),
			makeEntry(baseTime.Add(time.Minute), "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
			makeEntry(baseTime.Add(2*time.Minute), "discard_detection", "Crow", map[string]any{"confidence": 0.4}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 1)
		assert.Equal(t, 3, result.Buckets[0].SpeciesCount)
	})

	t.Run("multiple buckets separated by hour and sorted newest first", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		hour10 := time.Date(2024, 6, 15, 10, 15, 0, 0, time.UTC)
		hour14 := time.Date(2024, 6, 15, 14, 45, 0, 0, time.UTC)

		entries := []reader.LogEntry{
			makeEntry(hour10, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(hour14, "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 2)
		// Newest first
		assert.Equal(t, "2024-06-15T14", result.Buckets[0].Key)
		assert.Equal(t, "2024-06-15T10", result.Buckets[1].Key)
	})

	t.Run("pre-filters counted in bucket but not in species", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			makeEntry(baseTime, "dog_bark_filter", "", nil),
			makeEntry(baseTime.Add(time.Minute), "dog_bark_filter", "", nil),
			makeEntry(baseTime.Add(2*time.Minute), "privacy_filter", "", nil),
			makeEntry(baseTime.Add(3*time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 1)
		bucket := result.Buckets[0]
		assert.Equal(t, 2, bucket.PreFilters.DogBark)
		assert.Equal(t, 1, bucket.PreFilters.Privacy)
		// Only the approve creates a species entry
		require.Len(t, bucket.Species, 1)

		// Metrics
		assert.Equal(t, 2, result.Metrics.DogBarkTotal)
		assert.Equal(t, 1, result.Metrics.PrivacyTotal)
	})

	t.Run("pending count from create_pending_detection hourly array", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			makeEntry(time.Date(2024, 6, 15, 8, 0, 0, 0, time.UTC), "create_pending_detection", "", nil),
			makeEntry(time.Date(2024, 6, 15, 8, 15, 0, 0, time.UTC), "create_pending_detection", "", nil),
			makeEntry(time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC), "create_pending_detection", "", nil),
		}

		result := c.aggregateDetectionEvents(entries)

		assert.Equal(t, 2, result.Metrics.HourlyPending[8])
		assert.Equal(t, 1, result.Metrics.HourlyPending[10])
		assert.Equal(t, 0, result.Metrics.HourlyPending[0])
	})

	t.Run("species sorting approved first then by total descending", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			// Crow: 3 discards (no approvals)
			makeEntry(baseTime, "discard_detection", "Crow", map[string]any{"confidence": 0.3}),
			makeEntry(baseTime.Add(time.Minute), "discard_detection", "Crow", map[string]any{"confidence": 0.3}),
			makeEntry(baseTime.Add(2*time.Minute), "discard_detection", "Crow", map[string]any{"confidence": 0.3}),
			// Robin: 1 approve
			makeEntry(baseTime.Add(3*time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			// Sparrow: 2 approves
			makeEntry(baseTime.Add(4*time.Minute), "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
			makeEntry(baseTime.Add(5*time.Minute), "approve_detection", "Sparrow", map[string]any{"confidence": 0.85, "match_count": float64(2)}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 1)
		species := result.Buckets[0].Species
		require.Len(t, species, 3)
		// Approved species first (Sparrow 2 > Robin 1), then non-approved (Crow)
		assert.Equal(t, "Sparrow", species[0].Name)
		assert.Equal(t, "Robin", species[1].Name)
		assert.Equal(t, "Crow", species[2].Name)
	})

	t.Run("top discarded species top 3", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			// 5 discards for Alpha
			makeEntry(baseTime, "discard_detection", "Alpha", map[string]any{"confidence": 0.1}),
			makeEntry(baseTime, "discard_detection", "Alpha", map[string]any{"confidence": 0.1}),
			makeEntry(baseTime, "discard_detection", "Alpha", map[string]any{"confidence": 0.1}),
			makeEntry(baseTime, "discard_detection", "Alpha", map[string]any{"confidence": 0.1}),
			makeEntry(baseTime, "discard_detection", "Alpha", map[string]any{"confidence": 0.1}),
			// 3 discards for Bravo
			makeEntry(baseTime, "discard_detection", "Bravo", map[string]any{"confidence": 0.2}),
			makeEntry(baseTime, "discard_detection", "Bravo", map[string]any{"confidence": 0.2}),
			makeEntry(baseTime, "discard_detection", "Bravo", map[string]any{"confidence": 0.2}),
			// 2 discards for Charlie
			makeEntry(baseTime, "discard_detection", "Charlie", map[string]any{"confidence": 0.3}),
			makeEntry(baseTime, "discard_detection", "Charlie", map[string]any{"confidence": 0.3}),
			// 1 discard for Delta (should NOT be in top 3)
			makeEntry(baseTime, "discard_detection", "Delta", map[string]any{"confidence": 0.4}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Metrics.TopDiscarded, 3)
		assert.Equal(t, "Alpha", result.Metrics.TopDiscarded[0].Name)
		assert.Equal(t, 5, result.Metrics.TopDiscarded[0].Count)
		assert.Equal(t, "Bravo", result.Metrics.TopDiscarded[1].Name)
		assert.Equal(t, 3, result.Metrics.TopDiscarded[1].Count)
		assert.Equal(t, "Charlie", result.Metrics.TopDiscarded[2].Name)
		assert.Equal(t, 2, result.Metrics.TopDiscarded[2].Count)
	})

	t.Run("audio clip matching attached to correct species", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(baseTime.Add(time.Minute), "audio_export_success", "Robin", map[string]any{"clip_path": "/clips/robin_01.wav"}),
			makeEntry(baseTime.Add(2*time.Minute), "audio_export_success", "Robin", map[string]any{"clip_path": "/clips/robin_02.wav"}),
			makeEntry(baseTime.Add(3*time.Minute), "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
			makeEntry(baseTime.Add(4*time.Minute), "audio_export_success", "Sparrow", map[string]any{"clip_path": "/clips/sparrow_01.wav"}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 1)
		species := result.Buckets[0].Species
		// Find Robin and Sparrow by name (sorting may reorder them)
		var robin, sparrow *SpeciesEntry
		for i := range species {
			switch species[i].Name {
			case "Robin":
				robin = &species[i]
			case "Sparrow":
				sparrow = &species[i]
			}
		}
		require.NotNil(t, robin)
		require.NotNil(t, sparrow)
		assert.Equal(t, []string{"/clips/robin_01.wav", "/clips/robin_02.wav"}, robin.ClipPaths)
		assert.Equal(t, []string{"/clips/sparrow_01.wav"}, sparrow.ClipPaths)
	})

	t.Run("peak confidence tracks highest value", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{"confidence": 0.7, "match_count": float64(1)}),
			makeEntry(baseTime.Add(time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.95, "match_count": float64(1)}),
			makeEntry(baseTime.Add(2*time.Minute), "discard_detection", "Robin", map[string]any{"confidence": 0.8}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 1)
		require.Len(t, result.Buckets[0].Species, 1)
		assert.InDelta(t, 0.95, result.Buckets[0].Species[0].PeakConfidence, 0.001)
	})

	t.Run("max match count tracks highest value", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{"confidence": 0.8, "match_count": float64(2)}),
			makeEntry(baseTime.Add(time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(5)}),
			makeEntry(baseTime.Add(2*time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.85, "match_count": float64(3)}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 1)
		require.Len(t, result.Buckets[0].Species, 1)
		assert.Equal(t, 5, result.Buckets[0].Species[0].MaxMatchCount)
	})

	t.Run("empty species name entries are skipped", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(baseTime.Add(time.Minute), "discard_detection", "", map[string]any{"confidence": 0.5}),
			makeEntry(baseTime.Add(2*time.Minute), "flush_detection", "", nil),
		}

		result := c.aggregateDetectionEvents(entries)

		// Buckets may be created (getOrCreateBucket is called before species check)
		// but no species entries should exist
		for _, bucket := range result.Buckets {
			assert.Empty(t, bucket.Species)
		}
		assert.Empty(t, result.Species)
	})

	t.Run("approved per hour metric calculated correctly", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		hour10 := time.Date(2024, 6, 15, 10, 15, 0, 0, time.UTC)
		hour14 := time.Date(2024, 6, 15, 14, 45, 0, 0, time.UTC)

		entries := []reader.LogEntry{
			makeEntry(hour10, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(hour10.Add(time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.85, "match_count": float64(1)}),
			makeEntry(hour14, "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
		}

		result := c.aggregateDetectionEvents(entries)

		// 3 approved across 2 active hours = 1.5
		assert.Equal(t, "1.5", result.Metrics.ApprovedPerHour)
		assert.Equal(t, 3, result.Metrics.ApprovedTotal)
	})

	t.Run("half-hour offset events bucket by wall-clock hour", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		ist := time.FixedZone("IST", int((5*time.Hour + 30*time.Minute).Seconds()))
		at := func(hour, minute int) time.Time {
			return time.Date(2024, 6, 15, hour, minute, 0, 0, ist)
		}

		entries := []reader.LogEntry{
			makeEntry(at(10, 15), "create_pending_detection", "Robin", nil),
			makeEntry(at(10, 15), "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(at(10, 45), "discard_detection", "Robin", map[string]any{"confidence": 0.5, "reason": "low confidence"}),
			makeEntry(at(11, 0), "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
			makeEntry(at(11, 0), "create_pending_detection", "Sparrow", nil),
			makeEntry(at(11, 45), "dog_bark_filter", "", nil),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 2)
		newer, older := result.Buckets[0], result.Buckets[1]

		assert.Equal(t, "2024-06-15T11", newer.Key)
		assert.Equal(t, "11:00\u201312:00", newer.Label)
		assert.Equal(t, 1, newer.Totals.Approved)
		assert.Equal(t, 1, newer.PreFilters.DogBark)

		assert.Equal(t, "2024-06-15T10", older.Key)
		assert.Equal(t, "10:00\u201311:00", older.Label)
		assert.Equal(t, 1, older.Totals.Approved)
		assert.Equal(t, 1, older.Totals.Discarded)

		// Bucket hours agree with the hourly pending index, which uses the wall clock.
		assert.Equal(t, 1, result.Metrics.HourlyPending[10])
		assert.Equal(t, 1, result.Metrics.HourlyPending[11])
		for _, b := range result.Buckets {
			assert.NotZero(t, result.Metrics.HourlyPending[b.Timestamp.Hour()])
		}
		assert.Equal(t, "1.0", result.Metrics.ApprovedPerHour)

		// Bucket identity must not depend on which event creates the bucket first.
		reversed := make([]reader.LogEntry, len(entries))
		for i := range entries {
			reversed[len(entries)-1-i] = entries[i]
		}
		again := c.aggregateDetectionEvents(reversed)
		require.Len(t, again.Buckets, len(result.Buckets))
		for i := range result.Buckets {
			assert.Equal(t, result.Buckets[i].Key, again.Buckets[i].Key)
			assert.Equal(t, result.Buckets[i].Label, again.Buckets[i].Label)
			assert.True(t, result.Buckets[i].Timestamp.Equal(again.Buckets[i].Timestamp))
		}
	})

	t.Run("timestamps are recorded for approve and discard", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		approveTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
		discardTime := time.Date(2024, 6, 15, 10, 35, 0, 0, time.UTC)

		entries := []reader.LogEntry{
			makeEntry(approveTime, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(discardTime, "discard_detection", "Robin", map[string]any{"confidence": 0.3, "reason": "low_confidence"}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 1)
		require.Len(t, result.Buckets[0].Species, 1)
		sp := result.Buckets[0].Species[0]
		assert.Equal(t, []string{approveTime.Format(time.RFC3339)}, sp.ApproveTimestamps)
		assert.Equal(t, []string{discardTime.Format(time.RFC3339)}, sp.DiscardTimestamps)
	})

	t.Run("species summary sorted by total descending", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())

		entries := []reader.LogEntry{
			// Robin: 3 total
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(baseTime, "discard_detection", "Robin", map[string]any{"confidence": 0.3}),
			// Sparrow: 1 total
			makeEntry(baseTime, "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
			// Crow: 5 total
			makeEntry(baseTime, "discard_detection", "Crow", map[string]any{"confidence": 0.2}),
			makeEntry(baseTime, "discard_detection", "Crow", map[string]any{"confidence": 0.2}),
			makeEntry(baseTime, "discard_detection", "Crow", map[string]any{"confidence": 0.2}),
			makeEntry(baseTime, "discard_detection", "Crow", map[string]any{"confidence": 0.2}),
			makeEntry(baseTime, "discard_detection", "Crow", map[string]any{"confidence": 0.2}),
		}

		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Species, 3)
		assert.Equal(t, "Crow", result.Species[0].Name)
		assert.Equal(t, 5, result.Species[0].Total)
		assert.Equal(t, "Robin", result.Species[1].Name)
		assert.Equal(t, 3, result.Species[1].Total)
		assert.Equal(t, "Sparrow", result.Species[2].Name)
		assert.Equal(t, 1, result.Species[2].Total)
	})
}

// --- Field Extraction Helper Tests ---

func TestFieldExtractionHelpers(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	t.Run("getStringField", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			fields   map[string]any
			key      string
			expected string
		}{
			{"nil map returns empty", nil, "key", ""},
			{"missing key returns empty", map[string]any{"other": "val"}, "key", ""},
			{"correct type returns value", map[string]any{"species": "Robin"}, "species", "Robin"},
			{"wrong type returns empty", map[string]any{"species": 42}, "species", ""},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				result := getStringField(tt.fields, tt.key)
				assert.Equal(t, tt.expected, result)
			})
		}
	})

	t.Run("getFloat64Field", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			fields   map[string]any
			key      string
			expected float64
		}{
			{"nil map returns zero", nil, "key", 0},
			{"missing key returns zero", map[string]any{"other": 1.0}, "key", 0},
			{"float64 value extracted", map[string]any{"confidence": 0.95}, "confidence", 0.95},
			{"int value converted to float64", map[string]any{"confidence": 1}, "confidence", 1.0},
			{"wrong type returns zero", map[string]any{"confidence": "high"}, "confidence", 0},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				result := getFloat64Field(tt.fields, tt.key)
				assert.InDelta(t, tt.expected, result, 0.001)
			})
		}
	})

	t.Run("getIntField", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			fields   map[string]any
			key      string
			expected int
		}{
			{"nil map returns zero", nil, "key", 0},
			{"missing key returns zero", map[string]any{"other": 1}, "key", 0},
			{"int value extracted", map[string]any{"match_count": 5}, "match_count", 5},
			{"float64 to int conversion (JSON numbers)", map[string]any{"match_count": float64(7)}, "match_count", 7},
			{"wrong type returns zero", map[string]any{"match_count": "many"}, "match_count", 0},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				result := getIntField(tt.fields, tt.key)
				assert.Equal(t, tt.expected, result)
			})
		}
	})
}

// --- Noise Filtering Tests ---

func TestNoiseEntry(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	tests := []struct {
		name    string
		entry   reader.LogEntry
		isNoise bool
	}{
		{
			name: "detection operation is noise",
			entry: reader.LogEntry{
				Operation: "approve_detection",
				Module:    "analysis.processor",
				Msg:       "detection approved",
			},
			isNoise: true,
		},
		{
			name: "create_pending_detection is noise",
			entry: reader.LogEntry{
				Operation: "create_pending_detection",
				Module:    "analysis.processor",
				Msg:       "pending detection created",
			},
			isNoise: true,
		},
		{
			name: "process_detections_summary is noise",
			entry: reader.LogEntry{
				Operation: "process_detections_summary",
				Module:    "analysis.processor",
				Msg:       "summary",
			},
			isNoise: true,
		},
		{
			name: "event bus performance metrics is noise",
			entry: reader.LogEntry{
				Operation: "bus_performance_stats",
				Module:    "events",
				Msg:       "event bus stats",
			},
			isNoise: true,
		},
		{
			name: "event bus metrics is noise",
			entry: reader.LogEntry{
				Operation: "delivery_metrics",
				Module:    "events",
				Msg:       "metrics report",
			},
			isNoise: true,
		},
		{
			name: "disk usage below threshold is noise",
			entry: reader.LogEntry{
				Operation: "disk_check",
				Module:    "system",
				Msg:       "Disk usage below threshold, no action needed",
			},
			isNoise: true,
		},
		{
			name: "normal system event passes through",
			entry: reader.LogEntry{
				Operation: "config_reload",
				Module:    "system",
				Msg:       "Configuration reloaded",
			},
			isNoise: false,
		},
		{
			name: "audio capture event passes through",
			entry: reader.LogEntry{
				Operation: "capture_start",
				Module:    "audio",
				Msg:       "Audio capture started",
			},
			isNoise: false,
		},
		{
			name: "events module with non-metrics operation passes through",
			entry: reader.LogEntry{
				Operation: "subscriber_added",
				Module:    "events",
				Msg:       "new subscriber",
			},
			isNoise: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isNoiseEntry(&tt.entry)
			assert.Equal(t, tt.isNoise, result)
		})
	}
}

// --- Event ID Generation Tests ---

func TestGenerateEventID(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	t.Run("deterministic same input same output", func(t *testing.T) {
		t.Parallel()
		entry := &reader.LogEntry{
			Time:      baseTime,
			Msg:       "test message",
			Operation: "test_op",
		}

		id1 := generateEventID(entry)
		id2 := generateEventID(entry)

		assert.Equal(t, id1, id2)
	})

	t.Run("different inputs produce different IDs", func(t *testing.T) {
		t.Parallel()
		entry1 := &reader.LogEntry{
			Time:      baseTime,
			Msg:       "message one",
			Operation: "op_a",
		}
		entry2 := &reader.LogEntry{
			Time:      baseTime.Add(time.Second),
			Msg:       "message two",
			Operation: "op_b",
		}

		id1 := generateEventID(entry1)
		id2 := generateEventID(entry2)

		assert.NotEqual(t, id1, id2)
	})

	t.Run("correct format evt_ prefix", func(t *testing.T) {
		t.Parallel()
		entry := &reader.LogEntry{
			Time:      baseTime,
			Msg:       "hello",
			Operation: "test",
		}

		id := generateEventID(entry)

		assert.Regexp(t, `^evt_[0-9a-f]+$`, id)
	})

	t.Run("different timestamp same message produces different ID", func(t *testing.T) {
		t.Parallel()
		entry1 := &reader.LogEntry{Time: baseTime, Msg: "same msg", Operation: "same_op"}
		entry2 := &reader.LogEntry{Time: baseTime.Add(time.Nanosecond), Msg: "same msg", Operation: "same_op"}

		assert.NotEqual(t, generateEventID(entry1), generateEventID(entry2))
	})

	t.Run("same timestamp different message produces different ID", func(t *testing.T) {
		t.Parallel()
		entry1 := &reader.LogEntry{Time: baseTime, Msg: "msg A", Operation: "op"}
		entry2 := &reader.LogEntry{Time: baseTime, Msg: "msg B", Operation: "op"}

		assert.NotEqual(t, generateEventID(entry1), generateEventID(entry2))
	})
}

// --- Bucket Operations Tests ---

func TestBucketHourBoundaries(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	ist := time.FixedZone("IST", int((5*time.Hour + 30*time.Minute).Seconds()))
	npt := time.FixedZone("NPT", int((5*time.Hour + 45*time.Minute).Seconds()))
	nst := time.FixedZone("NST", -int((3*time.Hour + 30*time.Minute).Seconds()))
	acst := time.FixedZone("ACST", int((9*time.Hour + 30*time.Minute).Seconds()))
	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	lordHowe, err := time.LoadLocation("Australia/Lord_Howe")
	require.NoError(t, err)

	tests := []struct {
		name          string
		input         time.Time
		wantKey       string
		wantTimestamp string
		// wantLabel is not checked when empty.
		wantLabel string
	}{
		{
			"floors to hour correctly",
			time.Date(2024, 6, 15, 10, 45, 30, 0, time.UTC),
			"2024-06-15T10", "2024-06-15T10:00:00Z", "10:00\u201311:00",
		},
		{
			"exact hour stays same",
			time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
			"2024-06-15T10", "2024-06-15T10:00:00Z", "10:00\u201311:00",
		},
		{
			"midnight",
			time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
			"2024-06-15T00", "2024-06-15T00:00:00Z", "00:00\u201301:00",
		},
		{
			"end of day",
			time.Date(2024, 6, 15, 23, 59, 59, 0, time.UTC),
			"2024-06-15T23", "2024-06-15T23:00:00Z", "23:00\u201300:00",
		},
		{
			"+05:30 first half of the hour",
			time.Date(2024, 6, 15, 10, 15, 0, 0, ist),
			"2024-06-15T10", "2024-06-15T10:00:00+05:30", "10:00\u201311:00",
		},
		{
			"+05:30 second half of the hour",
			time.Date(2024, 6, 15, 10, 45, 0, 0, ist),
			"2024-06-15T10", "2024-06-15T10:00:00+05:30", "10:00\u201311:00",
		},
		{
			"+05:30 just after midnight stays on the same day",
			time.Date(2024, 6, 15, 0, 10, 0, 0, ist),
			"2024-06-15T00", "2024-06-15T00:00:00+05:30", "00:00\u201301:00",
		},
		{
			"+05:45 offset",
			time.Date(2024, 6, 15, 10, 15, 0, 0, npt),
			"2024-06-15T10", "2024-06-15T10:00:00+05:45", "10:00\u201311:00",
		},
		{
			"-03:30 offset",
			time.Date(2024, 6, 15, 10, 15, 0, 0, nst),
			"2024-06-15T10", "2024-06-15T10:00:00-03:30", "10:00\u201311:00",
		},
		{
			"+09:30 offset",
			time.Date(2024, 6, 15, 10, 15, 0, 0, acst),
			"2024-06-15T10", "2024-06-15T10:00:00+09:30", "10:00\u201311:00",
		},
		{
			"+05:30 last nanosecond of the hour",
			time.Date(2024, 6, 15, 10, 59, 59, 999999999, ist),
			"2024-06-15T10", "2024-06-15T10:00:00+05:30", "10:00\u201311:00",
		},
		{
			// First occurrence of the repeated 03:00 hour on the DST fall-back day.
			"repeated DST hour keeps the event's own offset",
			time.Date(2024, 10, 27, 0, 30, 0, 0, time.UTC).In(helsinki),
			"2024-10-27T03", "2024-10-27T03:00:00+03:00", "03:00\u201304:00",
		},
		{
			// 03:00 EET jumps to 04:00 EEST at 01:00 UTC, so 02:00 EET plus one hour reads 04:00.
			// The label ends at the next wall-clock hour, not at that instant.
			"hour before a spring forward gap ends at the next wall-clock hour",
			time.Date(2024, 3, 31, 0, 30, 0, 0, time.UTC).In(helsinki),
			"2024-03-31T02", "2024-03-31T02:00:00+02:00", "02:00\u201303:00",
		},
		{
			// Lord Howe moves from +10:30 to +11:00 at 02:00, so 02:00 to 02:30 never happens.
			"30 minute DST spring forward stays in the event's hour",
			time.Date(2024, 10, 5, 15, 45, 0, 0, time.UTC).In(lordHowe),
			"2024-10-06T02", "2024-10-06T02:00:00+11:00", "02:00\u201303:00",
		},
		{
			// Lord Howe moves from +11:00 back to +10:30 at 02:00, repeating 01:30 to 02:00.
			"30 minute DST fall back stays on the event's hour start",
			time.Date(2024, 4, 6, 15, 15, 0, 0, time.UTC).In(lordHowe),
			"2024-04-07T01", "2024-04-07T01:00:00+10:30", "01:00\u201302:00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantKey, toBucketKey(tt.input))

			b := getOrCreateBucket(map[string]*DetectionBucket{}, map[string]*speciesIndex{}, tt.input, tt.wantKey)
			assert.Equal(t, tt.wantKey, b.Key)
			assert.Equal(t, tt.wantTimestamp, b.Timestamp.Format(time.RFC3339))
			if tt.wantLabel != "" {
				assert.Equal(t, tt.wantLabel, b.Label)
			}
			assert.False(t, b.Timestamp.After(tt.input), "bucket must not start after the event")
			assert.True(t, tt.input.Before(b.Timestamp.Add(time.Hour)), "bucket must contain the event")
		})
	}
}

func TestFinalizeBucket(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	t.Run("totals computed correctly", func(t *testing.T) {
		t.Parallel()
		bucket := &DetectionBucket{
			Species: []SpeciesEntry{
				{Name: "Robin", Approved: 3, Discarded: 1, Flushed: 0},
				{Name: "Sparrow", Approved: 0, Discarded: 2, Flushed: 1},
			},
		}

		finalizeBucket(bucket)

		assert.Equal(t, 3, bucket.Totals.Approved)
		assert.Equal(t, 3, bucket.Totals.Discarded)
		assert.Equal(t, 1, bucket.Totals.Flushed)
		assert.Equal(t, 7, bucket.Totals.Pending) // 3+3+1
	})

	t.Run("approve ratio calculated correctly", func(t *testing.T) {
		t.Parallel()
		bucket := &DetectionBucket{
			Species: []SpeciesEntry{
				{Name: "Robin", Approved: 3, Discarded: 1, Flushed: 0},
			},
		}

		finalizeBucket(bucket)

		// 3 approved / 4 pending = 0.75
		assert.InDelta(t, 0.75, bucket.ApproveRatio, 0.001)
	})

	t.Run("approve ratio zero when no species", func(t *testing.T) {
		t.Parallel()
		bucket := &DetectionBucket{
			Species: []SpeciesEntry{},
		}

		finalizeBucket(bucket)

		assert.InDelta(t, 0.0, bucket.ApproveRatio, 0.001)
	})

	t.Run("species sorted approved first then by total descending", func(t *testing.T) {
		t.Parallel()
		bucket := &DetectionBucket{
			Species: []SpeciesEntry{
				{Name: "Crow", Approved: 0, Discarded: 5, Flushed: 0},    // no approvals, total 5
				{Name: "Robin", Approved: 1, Discarded: 0, Flushed: 0},   // approved, total 1
				{Name: "Sparrow", Approved: 2, Discarded: 1, Flushed: 0}, // approved, total 3
			},
		}

		finalizeBucket(bucket)

		require.Len(t, bucket.Species, 3)
		// Approved first: Sparrow (total 3) > Robin (total 1), then non-approved: Crow
		assert.Equal(t, "Sparrow", bucket.Species[0].Name)
		assert.Equal(t, "Robin", bucket.Species[1].Name)
		assert.Equal(t, "Crow", bucket.Species[2].Name)
	})

	t.Run("species count set correctly", func(t *testing.T) {
		t.Parallel()
		bucket := &DetectionBucket{
			Species: []SpeciesEntry{
				{Name: "Robin", Approved: 1},
				{Name: "Sparrow", Approved: 1},
			},
		}

		finalizeBucket(bucket)

		assert.Equal(t, 2, bucket.SpeciesCount)
	})
}

// --- System Events Builder Tests ---

func TestBuildSystemEvents(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	t.Run("entries converted to SystemEvent correctly", func(t *testing.T) {
		t.Parallel()
		entries := []reader.LogEntry{
			{
				Time:      baseTime,
				Level:     "INFO",
				Msg:       "Application started",
				Module:    "system",
				Operation: "startup",
				Fields:    map[string]any{"version": "1.0"},
			},
		}

		events, metrics := buildSystemEvents(entries)

		require.Len(t, events, 1)
		evt := events[0]
		assert.Equal(t, baseTime, evt.Timestamp)
		assert.Equal(t, "INFO", evt.Level)
		assert.Equal(t, "system", evt.Source)
		assert.Equal(t, "startup", evt.Operation)
		assert.Equal(t, "Application started", evt.Message)
		assert.Equal(t, map[string]any{"version": "1.0"}, evt.Fields)
		assert.NotEmpty(t, evt.ID)
		assert.Contains(t, evt.ID, "evt_")

		assert.Equal(t, 1, metrics.Total)
		assert.Equal(t, 1, metrics.ByLevel["INFO"])
	})

	t.Run("noise entries filtered out", func(t *testing.T) {
		t.Parallel()
		entries := []reader.LogEntry{
			{
				Time:      baseTime,
				Level:     "INFO",
				Msg:       "Application started",
				Module:    "system",
				Operation: "startup",
			},
			{
				Time:      baseTime.Add(time.Second),
				Level:     "INFO",
				Msg:       "detection approved",
				Module:    "analysis.processor",
				Operation: "approve_detection",
			},
			{
				Time:      baseTime.Add(2 * time.Second),
				Level:     "INFO",
				Msg:       "Disk usage below threshold, all good",
				Module:    "system",
				Operation: "disk_check",
			},
		}

		events, metrics := buildSystemEvents(entries)

		require.Len(t, events, 1)
		assert.Equal(t, "Application started", events[0].Message)
		assert.Equal(t, 1, metrics.Total)
	})

	t.Run("metrics computed by level", func(t *testing.T) {
		t.Parallel()
		entries := []reader.LogEntry{
			{Time: baseTime, Level: "INFO", Msg: "info 1", Module: "a", Operation: "op1"},
			{Time: baseTime.Add(time.Second), Level: "INFO", Msg: "info 2", Module: "b", Operation: "op2"},
			{Time: baseTime.Add(2 * time.Second), Level: "WARN", Msg: "warning", Module: "c", Operation: "op3"},
			{Time: baseTime.Add(3 * time.Second), Level: "ERROR", Msg: "error", Module: "d", Operation: "op4"},
		}

		events, metrics := buildSystemEvents(entries)

		require.Len(t, events, 4)
		assert.Equal(t, 4, metrics.Total)
		assert.Equal(t, 2, metrics.ByLevel["INFO"])
		assert.Equal(t, 1, metrics.ByLevel["WARN"])
		assert.Equal(t, 1, metrics.ByLevel["ERROR"])
	})

	t.Run("empty entries returns empty response", func(t *testing.T) {
		t.Parallel()

		events, metrics := buildSystemEvents(nil)

		assert.Empty(t, events)
		assert.Equal(t, 0, metrics.Total)
		assert.NotNil(t, metrics.ByLevel)
	})
}

// TestRepeatedHourBucketIsOrderIndependent pins the merged bucket of a repeated wall-clock hour:
// both occurrences share one key, and the bucket starts at the first occurrence whichever event
// is processed first.
func TestRepeatedHourBucketIsOrderIndependent(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	zones := testutil.ToolchainZones(t)

	tests := []struct {
		name string
		zone string
		// first and second are fixed instants, one in each occurrence of the repeated hour.
		first, second time.Time
		wantKey       string
		wantTimestamp time.Time
		wantLabel     string
		wantHour      int
	}{
		{
			// 04:00 EEST falls back to 03:00 EET at 01:00 UTC, repeating 03:00 to 04:00.
			name:          "Europe/Helsinki 2024-10-27",
			zone:          "Europe/Helsinki",
			first:         time.Date(2024, 10, 27, 0, 30, 0, 0, time.UTC),
			second:        time.Date(2024, 10, 27, 1, 30, 0, 0, time.UTC),
			wantKey:       "2024-10-27T03",
			wantTimestamp: time.Date(2024, 10, 27, 0, 0, 0, 0, time.UTC),
			wantLabel:     "03:00\u201304:00",
			wantHour:      3,
		},
		{
			// 02:00 +11:00 falls back to 01:30 +10:30 at 15:00 UTC, repeating 01:30 to 02:00.
			// The second occurrence floors to 01:00 +10:30, which is later than 01:00 +11:00.
			name:          "Australia/Lord_Howe 2024-04-07",
			zone:          "Australia/Lord_Howe",
			first:         time.Date(2024, 4, 6, 14, 45, 0, 0, time.UTC),
			second:        time.Date(2024, 4, 6, 15, 15, 0, 0, time.UTC),
			wantKey:       "2024-04-07T01",
			wantTimestamp: time.Date(2024, 4, 6, 14, 0, 0, 0, time.UTC),
			wantLabel:     "01:00\u201302:00",
			wantHour:      1,
		},
		{
			// 02:00 EDT falls back to 01:00 EST at 06:00 UTC, repeating 01:00 to 02:00.
			name:          "America/New_York 2024-11-03",
			zone:          "America/New_York",
			first:         time.Date(2024, 11, 3, 5, 30, 0, 0, time.UTC),
			second:        time.Date(2024, 11, 3, 6, 30, 0, 0, time.UTC),
			wantKey:       "2024-11-03T01",
			wantTimestamp: time.Date(2024, 11, 3, 5, 0, 0, 0, time.UTC),
			wantLabel:     "01:00\u201302:00",
			wantHour:      1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			loc, ok := zones[tt.zone]
			require.True(t, ok, "zone %s missing from the toolchain database", tt.zone)
			first, second := tt.first.In(loc), tt.second.In(loc)
			require.Equal(t, first.Hour(), second.Hour(), "both events must read the same wall-clock hour")
			require.NotEqual(t, first.Format("-07:00"), second.Format("-07:00"), "events must be in different occurrences")

			entriesFor := func(times ...time.Time) []reader.LogEntry {
				entries := make([]reader.LogEntry, 0, 2*len(times))
				for _, ts := range times {
					entries = append(entries,
						makeEntry(ts, "create_pending_detection", "Robin", nil),
						makeEntry(ts, "approve_detection", "Robin", nil))
				}
				return entries
			}

			for _, order := range []struct {
				name   string
				events []time.Time
			}{
				{"first occurrence first", []time.Time{first, second}},
				{"second occurrence first", []time.Time{second, first}},
			} {
				c := &Handler{Core: &apicore.Core{}}
				c.Settings.Store(apitest.NewValidTestSettings())
				result := c.aggregateDetectionEvents(entriesFor(order.events...))

				require.Len(t, result.Buckets, 1, order.name)
				b := result.Buckets[0]
				assert.Equal(t, tt.wantKey, b.Key, order.name)
				assert.True(t, b.Timestamp.Equal(tt.wantTimestamp), "%s: timestamp %s, want %s", order.name, b.Timestamp, tt.wantTimestamp)
				assert.Equal(t, tt.wantLabel, b.Label, order.name)
				assert.Equal(t, 2, b.Totals.Approved, order.name)
				assert.Equal(t, 2, result.Metrics.HourlyPending[tt.wantHour], order.name)
			}
		})
	}
}

// TestBucketsSharingATimestampSortByKey pins the tie break of the newest-first sort. Pacific/Chatham
// falls back from 03:45 +13:45 to 02:45 +12:45 on 2024-04-07 local (2024-04-06 UTC), so the events below land in the keys
// 03 and 02 and both floor to the same instant (13:15 UTC). The bucket map iterates in random
// order, so without the tie break the order changes from call to call.
func TestBucketsSharingATimestampSortByKey(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	chatham, ok := testutil.ToolchainZones(t)["Pacific/Chatham"]
	require.True(t, ok, "Pacific/Chatham missing from the toolchain database")

	entries := []reader.LogEntry{
		makeEntry(time.Date(2024, 4, 6, 13, 30, 0, 0, time.UTC).In(chatham), "approve_detection", "Robin", nil),
		makeEntry(time.Date(2024, 4, 6, 14, 5, 0, 0, time.UTC).In(chatham), "approve_detection", "Robin", nil),
	}

	// The unlucky order shows up in about one call in seven, so this many calls make a pass by
	// chance negligible.
	const calls = 200
	for range calls {
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())
		result := c.aggregateDetectionEvents(entries)

		require.Len(t, result.Buckets, 2)
		require.True(t, result.Buckets[0].Timestamp.Equal(result.Buckets[1].Timestamp), "fixture must tie on Timestamp")
		require.Equal(t, "2024-04-07T03", result.Buckets[0].Key)
		require.Equal(t, "2024-04-07T02", result.Buckets[1].Key)
	}
}

// TestRepeatedHourAggregationIgnoresEventOrder feeds every permutation of events that include the
// transition instant, the last second of the first occurrence and the end of the repeated span,
// and requires one result.
func TestRepeatedHourAggregationIgnoresEventOrder(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	zones := testutil.ToolchainZones(t)
	utc := func(y int, m time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, m, d, h, mi, s, 0, time.UTC)
	}

	tests := []struct {
		zone   string
		events []time.Time
	}{
		{"Europe/Helsinki", []time.Time{
			utc(2024, 10, 27, 0, 30, 0), utc(2024, 10, 27, 0, 59, 59), utc(2024, 10, 27, 1, 0, 0),
			utc(2024, 10, 27, 1, 30, 0), utc(2024, 10, 27, 1, 59, 59), utc(2024, 10, 27, 2, 0, 0),
		}},
		{"Australia/Lord_Howe", []time.Time{
			utc(2024, 4, 6, 14, 45, 0), utc(2024, 4, 6, 14, 59, 59), utc(2024, 4, 6, 15, 0, 0),
			utc(2024, 4, 6, 15, 15, 0), utc(2024, 4, 6, 15, 29, 59), utc(2024, 4, 6, 15, 30, 0),
		}},
		{"Pacific/Chatham", []time.Time{
			utc(2024, 4, 6, 13, 30, 0), utc(2024, 4, 6, 13, 59, 59), utc(2024, 4, 6, 14, 0, 0),
			utc(2024, 4, 6, 14, 5, 0), utc(2024, 4, 6, 14, 59, 59), utc(2024, 4, 6, 15, 0, 0),
		}},
	}

	// summary leaves out the per-species timestamp lists, which follow the input order by design.
	type summary struct {
		Key, Label string
		Timestamp  string
		Approved   int
	}
	run := func(loc *time.Location, order []time.Time) ([]summary, [24]int) {
		entries := make([]reader.LogEntry, 0, 2*len(order))
		for _, ts := range order {
			entries = append(entries,
				makeEntry(ts.In(loc), "create_pending_detection", "Robin", nil),
				makeEntry(ts.In(loc), "approve_detection", "Robin", nil))
		}
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())
		result := c.aggregateDetectionEvents(entries)
		out := make([]summary, 0, len(result.Buckets))
		for _, b := range result.Buckets {
			out = append(out, summary{b.Key, b.Label, b.Timestamp.Format(time.RFC3339), b.Totals.Approved})
		}
		return out, result.Metrics.HourlyPending
	}

	for _, tt := range tests {
		t.Run(tt.zone, func(t *testing.T) {
			t.Parallel()
			loc, ok := zones[tt.zone]
			require.True(t, ok, "zone %s missing from the toolchain database", tt.zone)

			wantBuckets, wantPending := run(loc, tt.events)
			total := 0
			for _, b := range wantBuckets {
				total += b.Approved
			}
			require.Equal(t, len(tt.events), total, "every event must be counted once")

			order := slices.Clone(tt.events)
			var permute func(k int)
			permute = func(k int) {
				if k == len(order) {
					gotBuckets, gotPending := run(loc, order)
					if !assert.Equal(t, wantBuckets, gotBuckets, "order %v", order) || !assert.Equal(t, wantPending, gotPending, "order %v", order) {
						t.FailNow()
					}
					return
				}
				for i := k; i < len(order); i++ {
					order[k], order[i] = order[i], order[k]
					permute(k + 1)
					order[k], order[i] = order[i], order[k]
				}
			}
			permute(0)
		})
	}
}
