package processor

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// newPrivacyFilterDetection builds a minimal pending bird detection for a single
// audio source, back-dated to firstDetected.
func newPrivacyFilterDetection(source string, firstDetected time.Time) *PendingDetection {
	return &PendingDetection{
		Detection: Detections{
			Result: detection.Result{
				Species: detection.Species{
					CommonName:     "Talitiainen",
					ScientificName: "Parus major",
				},
			},
		},
		Source:        source,
		FirstDetected: firstDetected,
		Count:         5,
	}
}

// TestShouldDiscardDetection_PrivacyFilterBoundary exercises the privacy filter
// across the human-voice-vs-bird timestamp boundary. The key case is equal
// timestamps: a human voice and a bird detected in the exact same audio chunk
// share the same back-dated start time, and that must still discard the bird
// (the prior strict "human after bird" comparison leaked it). A human voice from
// an earlier chunk (strictly before) must not discard a later bird.
func TestShouldDiscardDetection_PrivacyFilterBoundary(t *testing.T) {
	t.Parallel()

	const source = "src1"
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		humanTime   time.Time
		birdTime    time.Time
		wantDiscard bool
		wantReason  string
	}{
		{
			name:        "same chunk: human and bird share the timestamp",
			humanTime:   base,
			birdTime:    base,
			wantDiscard: true,
			wantReason:  "privacy filter",
		},
		{
			name:        "human voice after the bird started",
			humanTime:   base.Add(time.Second),
			birdTime:    base,
			wantDiscard: true,
			wantReason:  "privacy filter",
		},
		{
			name:        "human voice from an earlier chunk is kept",
			humanTime:   base,
			birdTime:    base.Add(time.Second),
			wantDiscard: false,
			wantReason:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &conf.Settings{}
			settings.Realtime.PrivacyFilter.Enabled = true

			p := &Processor{
				LastHumanDetection: map[string]HumanDetection{source: {Time: tt.humanTime}},
			}
			item := newPrivacyFilterDetection(source, tt.birdTime)

			discard, reason := p.shouldDiscardDetection(item, settings, 1)

			assert.Equal(t, tt.wantDiscard, discard)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

// TestShouldDiscardDetection_RecordsTriggerMetric verifies the privacy-filter
// discard is attributed to the mechanism that flagged the human voice, emitting
// privacy_filter_discards_total{trigger} with the correct label. This is the
// central new behavior of the trigger-attribution change and exercises the emit
// path (telemetry enabled, Metrics present).
func TestShouldDiscardDetection_RecordsTriggerMetric(t *testing.T) {
	t.Parallel()

	const source = "s1"
	humanTime := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		stored      HumanDetection
		wantTrigger string
	}{
		{"vad trigger", HumanDetection{Time: humanTime, Trigger: metrics.TriggerVAD}, "vad"},
		{"label trigger", HumanDetection{Time: humanTime, Trigger: metrics.TriggerLabel}, "label"},
		// Defensive fallback: a detection with no trigger set must attribute to
		// label rather than emit a bogus empty label.
		{"empty trigger falls back to label", HumanDetection{Time: humanTime}, "label"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := prometheus.NewRegistry()
			pfm, err := metrics.NewPrivacyFilterMetrics(reg)
			require.NoError(t, err)

			settings := &conf.Settings{}
			settings.Realtime.PrivacyFilter.Enabled = true
			settings.Realtime.Telemetry.Enabled = true

			p := &Processor{
				Metrics:            &observability.Metrics{PrivacyFilter: pfm},
				LastHumanDetection: map[string]HumanDetection{source: tt.stored},
			}
			// The bird shares the human's chunk (equal timestamps), so it is discarded.
			item := newPrivacyFilterDetection(source, humanTime)

			discard, reason := p.shouldDiscardDetection(item, settings, 1)
			require.True(t, discard, "a human voice at/after the bird must discard")
			assert.Equal(t, "privacy filter", reason)

			expected := "\n" +
				"# HELP privacy_filter_discards_total Total detections discarded by the privacy filter, by trigger\n" +
				"# TYPE privacy_filter_discards_total counter\n" +
				"privacy_filter_discards_total{trigger=\"" + tt.wantTrigger + "\"} 1\n"
			require.NoError(t, testutil.CollectAndCompare(pfm, strings.NewReader(expected), "privacy_filter_discards_total"))
		})
	}
}
