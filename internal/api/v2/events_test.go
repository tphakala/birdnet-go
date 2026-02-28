// events_test.go: Package api provides tests for API v2 events endpoints.

package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger/reader"
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
		c := &Controller{}

		result := c.aggregateDetectionEvents(nil, baseTime)

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
		c := &Controller{}

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{
				"confidence":  0.85,
				"match_count": float64(3),
			}),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

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
		c := &Controller{}

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Sparrow", map[string]any{"confidence": 0.7, "match_count": float64(1)}),
			makeEntry(baseTime.Add(5*time.Minute), "discard_detection", "Sparrow", map[string]any{"confidence": 0.3, "reason": "low_confidence"}),
			makeEntry(baseTime.Add(10*time.Minute), "flush_detection", "Sparrow", nil),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

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
		c := &Controller{}

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(2)}),
			makeEntry(baseTime.Add(time.Minute), "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
			makeEntry(baseTime.Add(2*time.Minute), "discard_detection", "Crow", map[string]any{"confidence": 0.4}),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

		require.Len(t, result.Buckets, 1)
		assert.Equal(t, 3, result.Buckets[0].SpeciesCount)
	})

	t.Run("multiple buckets separated by hour and sorted newest first", func(t *testing.T) {
		t.Parallel()
		c := &Controller{}

		hour10 := time.Date(2024, 6, 15, 10, 15, 0, 0, time.UTC)
		hour14 := time.Date(2024, 6, 15, 14, 45, 0, 0, time.UTC)

		entries := []reader.LogEntry{
			makeEntry(hour10, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(hour14, "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

		require.Len(t, result.Buckets, 2)
		// Newest first
		assert.Equal(t, "2024-06-15T14", result.Buckets[0].Key)
		assert.Equal(t, "2024-06-15T10", result.Buckets[1].Key)
	})

	t.Run("pre-filters counted in bucket but not in species", func(t *testing.T) {
		t.Parallel()
		c := &Controller{}

		entries := []reader.LogEntry{
			makeEntry(baseTime, "dog_bark_filter", "", nil),
			makeEntry(baseTime.Add(time.Minute), "dog_bark_filter", "", nil),
			makeEntry(baseTime.Add(2*time.Minute), "privacy_filter", "", nil),
			makeEntry(baseTime.Add(3*time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

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
		c := &Controller{}

		entries := []reader.LogEntry{
			makeEntry(time.Date(2024, 6, 15, 8, 0, 0, 0, time.UTC), "create_pending_detection", "", nil),
			makeEntry(time.Date(2024, 6, 15, 8, 15, 0, 0, time.UTC), "create_pending_detection", "", nil),
			makeEntry(time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC), "create_pending_detection", "", nil),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

		assert.Equal(t, 2, result.Metrics.HourlyPending[8])
		assert.Equal(t, 1, result.Metrics.HourlyPending[10])
		assert.Equal(t, 0, result.Metrics.HourlyPending[0])
	})

	t.Run("species sorting approved first then by total descending", func(t *testing.T) {
		t.Parallel()
		c := &Controller{}

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

		result := c.aggregateDetectionEvents(entries, baseTime)

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
		c := &Controller{}

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

		result := c.aggregateDetectionEvents(entries, baseTime)

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
		c := &Controller{}

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(baseTime.Add(time.Minute), "audio_export_success", "Robin", map[string]any{"clip_path": "/clips/robin_01.wav"}),
			makeEntry(baseTime.Add(2*time.Minute), "audio_export_success", "Robin", map[string]any{"clip_path": "/clips/robin_02.wav"}),
			makeEntry(baseTime.Add(3*time.Minute), "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
			makeEntry(baseTime.Add(4*time.Minute), "audio_export_success", "Sparrow", map[string]any{"clip_path": "/clips/sparrow_01.wav"}),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

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
		c := &Controller{}

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{"confidence": 0.7, "match_count": float64(1)}),
			makeEntry(baseTime.Add(time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.95, "match_count": float64(1)}),
			makeEntry(baseTime.Add(2*time.Minute), "discard_detection", "Robin", map[string]any{"confidence": 0.8}),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

		require.Len(t, result.Buckets, 1)
		require.Len(t, result.Buckets[0].Species, 1)
		assert.InDelta(t, 0.95, result.Buckets[0].Species[0].PeakConfidence, 0.001)
	})

	t.Run("max match count tracks highest value", func(t *testing.T) {
		t.Parallel()
		c := &Controller{}

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "Robin", map[string]any{"confidence": 0.8, "match_count": float64(2)}),
			makeEntry(baseTime.Add(time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(5)}),
			makeEntry(baseTime.Add(2*time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.85, "match_count": float64(3)}),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

		require.Len(t, result.Buckets, 1)
		require.Len(t, result.Buckets[0].Species, 1)
		assert.Equal(t, 5, result.Buckets[0].Species[0].MaxMatchCount)
	})

	t.Run("empty species name entries are skipped", func(t *testing.T) {
		t.Parallel()
		c := &Controller{}

		entries := []reader.LogEntry{
			makeEntry(baseTime, "approve_detection", "", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(baseTime.Add(time.Minute), "discard_detection", "", map[string]any{"confidence": 0.5}),
			makeEntry(baseTime.Add(2*time.Minute), "flush_detection", "", nil),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

		// Buckets may be created (getOrCreateBucket is called before species check)
		// but no species entries should exist
		for _, bucket := range result.Buckets {
			assert.Empty(t, bucket.Species)
		}
		assert.Empty(t, result.Species)
	})

	t.Run("approved per hour metric calculated correctly", func(t *testing.T) {
		t.Parallel()
		c := &Controller{}

		hour10 := time.Date(2024, 6, 15, 10, 15, 0, 0, time.UTC)
		hour14 := time.Date(2024, 6, 15, 14, 45, 0, 0, time.UTC)

		entries := []reader.LogEntry{
			makeEntry(hour10, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(hour10.Add(time.Minute), "approve_detection", "Robin", map[string]any{"confidence": 0.85, "match_count": float64(1)}),
			makeEntry(hour14, "approve_detection", "Sparrow", map[string]any{"confidence": 0.8, "match_count": float64(1)}),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

		// 3 approved across 2 active hours = 1.5
		assert.Equal(t, "1.5", result.Metrics.ApprovedPerHour)
		assert.Equal(t, 3, result.Metrics.ApprovedTotal)
	})

	t.Run("timestamps are recorded for approve and discard", func(t *testing.T) {
		t.Parallel()
		c := &Controller{}

		approveTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
		discardTime := time.Date(2024, 6, 15, 10, 35, 0, 0, time.UTC)

		entries := []reader.LogEntry{
			makeEntry(approveTime, "approve_detection", "Robin", map[string]any{"confidence": 0.9, "match_count": float64(1)}),
			makeEntry(discardTime, "discard_detection", "Robin", map[string]any{"confidence": 0.3, "reason": "low_confidence"}),
		}

		result := c.aggregateDetectionEvents(entries, baseTime)

		require.Len(t, result.Buckets, 1)
		require.Len(t, result.Buckets[0].Species, 1)
		sp := result.Buckets[0].Species[0]
		assert.Equal(t, []string{approveTime.Format(time.RFC3339)}, sp.ApproveTimestamps)
		assert.Equal(t, []string{discardTime.Format(time.RFC3339)}, sp.DiscardTimestamps)
	})

	t.Run("species summary sorted by total descending", func(t *testing.T) {
		t.Parallel()
		c := &Controller{}

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

		result := c.aggregateDetectionEvents(entries, baseTime)

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

func TestBucketKey(t *testing.T) {
	t.Parallel()
	t.Attr("component", "events")
	t.Attr("type", "unit")

	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{
			"floors to hour correctly",
			time.Date(2024, 6, 15, 10, 45, 30, 0, time.UTC),
			"2024-06-15T10",
		},
		{
			"exact hour stays same",
			time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
			"2024-06-15T10",
		},
		{
			"midnight",
			time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
			"2024-06-15T00",
		},
		{
			"end of day",
			time.Date(2024, 6, 15, 23, 59, 59, 0, time.UTC),
			"2024-06-15T23",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := toBucketKey(tt.input)
			assert.Equal(t, tt.expected, result)
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
