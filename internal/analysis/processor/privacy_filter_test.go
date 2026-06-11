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

// TestShouldDiscardDetection_PrivacyFilter_SameChunkHumanVoice covers the
// equal-timestamp edge: when a human voice and a bird are detected in the exact
// same audio chunk, both carry the same StartTime, so the privacy filter's strict
// "human after bird" comparison let the bird through. A simultaneous human voice
// must still discard the detection.
func TestShouldDiscardDetection_PrivacyFilter_SameChunkHumanVoice(t *testing.T) {
	t.Parallel()

	const source = "src1"
	chunkTime := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	settings := &conf.Settings{}
	settings.Realtime.PrivacyFilter.Enabled = true

	p := &Processor{
		LastHumanDetection: map[string]time.Time{source: chunkTime},
	}
	item := newPrivacyFilterDetection(source, chunkTime)

	discard, reason := p.shouldDiscardDetection(item, settings, 1)

	assert.True(t, discard,
		"a bird sharing an audio chunk with a human voice must be discarded by the privacy filter")
	assert.Equal(t, "privacy filter", reason)
}

// TestShouldDiscardDetection_PrivacyFilter_EarlierHumanVoiceKept locks the
// non-regression boundary: a human voice heard strictly before the bird detection
// started belongs to an earlier capture window and must not discard the bird.
func TestShouldDiscardDetection_PrivacyFilter_EarlierHumanVoiceKept(t *testing.T) {
	t.Parallel()

	const source = "src1"
	humanTime := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	birdTime := humanTime.Add(time.Second) // bird started after the human voice

	settings := &conf.Settings{}
	settings.Realtime.PrivacyFilter.Enabled = true

	p := &Processor{
		LastHumanDetection: map[string]time.Time{source: humanTime},
	}
	item := newPrivacyFilterDetection(source, birdTime)

	discard, _ := p.shouldDiscardDetection(item, settings, 1)

	assert.False(t, discard,
		"a human voice from an earlier chunk must not discard a later bird detection")
}
