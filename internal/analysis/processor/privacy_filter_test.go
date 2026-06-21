package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
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
				LastHumanDetection: map[string]time.Time{source: tt.humanTime},
			}
			item := newPrivacyFilterDetection(source, tt.birdTime)

			discard, reason := p.shouldDiscardDetection(item, settings, 1)

			assert.Equal(t, tt.wantDiscard, discard)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}
