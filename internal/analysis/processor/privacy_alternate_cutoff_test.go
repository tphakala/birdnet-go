package processor

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// TestProcessResults_AlternateMinConfidenceKeepsPrivacyAndDogFilters pins that
// birdnet.alternateminconfidence only trims the stored alternates, at the save
// step: the privacy and dog bark filters still see a below-cutoff human or dog result from the
// in-memory classifier output, and still discard the bird of the same chunk.
// Not parallel: the dog bark filter reads the global settings.
func TestProcessResults_AlternateMinConfidenceKeepsPrivacyAndDogFilters(t *testing.T) {
	const (
		source         = "cutoff-src"
		birdConf       = 0.9
		filterConf     = 0.05
		belowCutoff    = 0.3
		highCutoff     = 0.5
		birdThreshold  = 0.5
		dogRememberMin = 5
	)
	tests := []struct {
		name        string
		label       string
		enable      func(s *conf.Settings)
		hitRecorded func(p *Processor) bool
		wantDiscard string
	}{
		{
			name:  "privacy filter",
			label: altHumanLabel,
			enable: func(s *conf.Settings) {
				s.Realtime.PrivacyFilter.Enabled = true
				s.Realtime.PrivacyFilter.Confidence = filterConf
			},
			hitRecorded: func(p *Processor) bool { _, ok := p.LastHumanDetection[source]; return ok },
			wantDiscard: reasonPrivacyFilter,
		},
		{
			name:  "dog bark filter",
			label: altDogLabel,
			enable: func(s *conf.Settings) {
				s.Realtime.DogBarkFilter.Enabled = true
				s.Realtime.DogBarkFilter.Confidence = filterConf
				s.Realtime.DogBarkFilter.Remember = dogRememberMin
				s.Realtime.DogBarkFilter.Species = []string{"turdus merula"}
			},
			hitRecorded: func(p *Processor) bool { _, ok := p.LastDogDetection[source]; return ok },
			wantDiscard: "recent dog bark",
		},
	}
	for _, tt := range tests {
		for _, cutoff := range []float64{0, highCutoff} {
			t.Run(fmt.Sprintf("%s cutoff %v", tt.name, cutoff), func(t *testing.T) {
				p := mergeTestProcessor(t)
				p.Bn = &classifier.Orchestrator{}
				p.LastHumanDetection = map[string]HumanDetection{}
				p.LastDogDetection = map[string]time.Time{}
				settings := &conf.Settings{}
				settings.BirdNET.Threshold = birdThreshold
				settings.BirdNET.AlternateMinConfidence = cutoff
				tt.enable(settings)
				p.Settings = settings
				conf.StoreSettings(settings)

				end := time.Now()
				item := classifier.Results{
					Source:    datastore.AudioSource{ID: source},
					ModelID:   classifier.RegistryIDBirdNETV24,
					StartTime: end.Add(-mergeTestClip),
					Audio:     classifier.AudioTiming{EndAt: end},
					Results: []datastore.Results{
						{Species: altBlackbirdLabel, Confidence: birdConf},
						{Species: tt.label, Confidence: belowCutoff},
					},
				}

				detections := p.processResults(settings, item)

				require.Len(t, detections, 1, "only the bird is a detection")
				assert.True(t, tt.hitRecorded(p), "the below-cutoff %s result must still be recorded (cutoff %v)", tt.name, cutoff)

				pending := &PendingDetection{Detection: detections[0], Source: source, FirstDetected: item.StartTime, Count: 5}
				discard, reason := discardNow(p, pending, settings, 1)
				assert.True(t, discard, "the bird must still be discarded (cutoff %v)", cutoff)
				assert.Equal(t, tt.wantDiscard, reason)

				// Later stages see the whole list; only the save step trims it.
				isFilterRow := func(r detection.AdditionalResult) bool { return r.RawLabel == tt.label }
				require.True(t, slices.ContainsFunc(detections[0].Results, isFilterRow), "the below-cutoff row stays on the detection (cutoff %v)", cutoff)
				stored := slices.ContainsFunc(storedAlternates(detections[0].Results, cutoff), isFilterRow)
				assert.Equal(t, cutoff == 0, stored, "stored alternate for cutoff %v", cutoff)
			})
		}
	}
}
