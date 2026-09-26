package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// The raw-label classification logic (isHumanVocalization / isDogDetection) is
// tested in internal/labels/vocalization. The tests here cover the processor
// call sites that consume it: the recording handlers and the save filter.

// TestDetectionHandlers_RecordTimestamp proves both recording handlers store a
// detection timestamp for the labels the old substring match missed (Perch v2
// FSD50K classes and localized non-English BirdNET classes), and do NOT record
// when the filter is disabled or the confidence is below the threshold. It also
// confirms each handler writes only its own map, never the other filter's.
func TestDetectionHandlers_RecordTimestamp(t *testing.T) {
	t.Parallel()

	const source = "src1"
	start := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)

	enablePrivacy := func(s *conf.Settings) {
		s.Realtime.PrivacyFilter.Enabled = true
		s.Realtime.PrivacyFilter.Confidence = 0.05
	}
	enableDog := func(s *conf.Settings) {
		s.Realtime.DogBarkFilter.Enabled = true
		s.Realtime.DogBarkFilter.Confidence = 0.05
	}

	tests := []struct {
		name       string
		species    string
		confidence float32
		enable     func(s *conf.Settings)
		record     func(p *Processor, s *conf.Settings, item classifier.Results, r datastore.Results)
		isHuman    bool // true: should write LastHumanDetection; false: LastDogDetection
		wantStored bool
	}{
		{"privacy records Perch speech", "Speech", 0.9, enablePrivacy, (*Processor).handleHumanDetection, true, true},
		{"privacy records localized BirdNET human", "Human vocal_Mensch Stimme", 0.9, enablePrivacy, (*Processor).handleHumanDetection, true, true},
		{"privacy disabled does not record", "Speech", 0.9, func(_ *conf.Settings) {}, (*Processor).handleHumanDetection, true, false},
		{"privacy below threshold does not record", "Speech", 0.01, enablePrivacy, (*Processor).handleHumanDetection, true, false},
		{"dog records Perch bark", "Bark", 0.9, enableDog, (*Processor).handleDogDetection, false, true},
		{"dog records localized BirdNET dog", "Dog_Hund", 0.9, enableDog, (*Processor).handleDogDetection, false, true},
		{"dog disabled does not record", "Bark", 0.9, func(_ *conf.Settings) {}, (*Processor).handleDogDetection, false, false},
		{"dog below threshold does not record", "Bark", 0.01, enableDog, (*Processor).handleDogDetection, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &conf.Settings{}
			tt.enable(settings)

			p := &Processor{
				LastHumanDetection: make(map[string]HumanDetection),
				LastDogDetection:   make(map[string]time.Time),
			}
			item := classifier.Results{StartTime: start}
			item.Source.ID = source
			result := datastore.Results{Species: tt.species, Confidence: tt.confidence}

			tt.record(p, settings, item, result)

			// The two maps carry different value types (human entries also record
			// the trigger), so assert each branch against its own map rather than a
			// shared target/other alias.
			if tt.isHuman {
				got, ok := p.LastHumanDetection[source]
				assert.Equal(t, tt.wantStored, ok, "unexpected record state in human map")
				if tt.wantStored {
					assert.Equal(t, start, got.Time)
				}
				assert.Empty(t, p.LastDogDetection, "human handler must not write the dog map")
			} else {
				got, ok := p.LastDogDetection[source]
				assert.Equal(t, tt.wantStored, ok, "unexpected record state in dog map")
				if tt.wantStored {
					assert.Equal(t, start, got)
				}
				assert.Empty(t, p.LastHumanDetection, "dog handler must not write the human map")
			}
		})
	}
}

// TestShouldFilterDetection_DropsHumanLabels covers the save filter call site:
// shouldFilterDetection must drop a human-labeled detection from being saved
// (Perch v2 class and a localized BirdNET class), while letting a normal bird
// through.
func TestShouldFilterDetection_DropsHumanLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		species    string
		wantFilter bool
	}{
		{"Perch speech is dropped", "Speech", true},
		{"localized BirdNET human is dropped", "Human vocal_Mensch Stimme", true},
		{"normal bird is not dropped by the human filter", "Turdus merula", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := &Processor{}
			settings := &conf.Settings{}
			result := datastore.Results{Species: tt.species, Confidence: 0.9}

			shouldFilter, _ := p.shouldFilterDetection(
				settings,
				result,
				tt.species, // commonName (unused by the human branch)
				tt.species, // scientificName
				tt.species, // speciesLowercase
				0.7,        // baseThreshold (Confidence 0.9 > 0.7 triggers the human branch)
				"Backyard",
				"Perch_V2",
			)

			if tt.wantFilter {
				assert.True(t, shouldFilter, "human-labeled detection must be filtered out")
			} else {
				// A normal bird is not dropped by the human privacy branch. Other
				// branches may still pass it through; the human check must not fire.
				assert.False(t, shouldFilter, "non-human detection must not hit the human privacy filter")
			}
		})
	}
}
