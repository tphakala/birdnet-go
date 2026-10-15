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

// discardNow runs shouldDiscardDetection with the privacy state read at the
// current time, as one flush tick does.
func discardNow(p *Processor, item *PendingDetection, settings *conf.Settings, minDetections int) (discard bool, reason string) {
	priv := p.checkPrivacy(settings, item, time.Now())
	return p.shouldDiscardDetection(item, settings, minDetections, &priv)
}

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
// across the human-voice-vs-clip-begin boundary. A hit is stamped with the end of
// the audio that produced it and the bird's clip begins at FirstDetected, so a
// hit at or after the clip begin (equal timestamps included, which the prior
// strict "human after bird" comparison leaked) must discard the bird. A hit from
// an earlier chunk, more than privacyClipGuard before the clip begin, must not
// discard a later bird.
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
			name:        "hit stamped exactly at the clip begin",
			humanTime:   base,
			birdTime:    base,
			wantDiscard: true,
			wantReason:  "privacy filter",
		},
		{
			name:        "hit after the clip begin",
			humanTime:   base.Add(time.Second),
			birdTime:    base,
			wantDiscard: true,
			wantReason:  "privacy filter",
		},
		{
			name:        "hit one second before the clip begin is kept",
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

			discard, reason := discardNow(p, item, settings, 1)

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
			// The hit is stamped at the clip begin (equal timestamps), so it is discarded.
			item := newPrivacyFilterDetection(source, humanTime)

			discard, reason := discardNow(p, item, settings, 1)
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
