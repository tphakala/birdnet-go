package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

func TestIsHumanVocalization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawLabel string
		want     bool
	}{
		// BirdNET v2.4 classes, English (matched via the locale-stable prefix).
		{"BirdNET human vocal", "Human vocal_Human vocal", true},
		{"BirdNET human non-vocal", "Human non-vocal_Human non-vocal", true},
		{"BirdNET human whistle", "Human whistle_Human whistle", true},
		// BirdNET v2.4 classes, non-English locale. The common name is localized
		// ("Mensch Stimme"), so only raw-label matching catches these.
		{"BirdNET human vocal (de)", "Human vocal_Mensch Stimme", true},
		{"BirdNET human non-vocal (de)", "Human non-vocal_Mensch Geräusch", true},
		{"BirdNET human whistle (de)", "Human whistle_Mensch Pfeifen", true},
		// Perch v2 speech/voice classes (exact raw-label match).
		{"Perch Speech", "Speech", true},
		{"Perch Human_voice", "Human_voice", true},
		{"Perch male speech", "Male_speech_and_man_speaking", true},
		{"Perch female speech", "Female_speech_and_woman_speaking", true},
		{"Perch child speech", "Child_speech_and_kid_speaking", true},
		{"Perch Conversation", "Conversation", true},
		{"Perch Chatter", "Chatter", true},
		{"Perch Whispering", "Whispering", true},
		{"Perch Speech_synthesizer", "Speech_synthesizer", true},
		{"Perch Human_group_actions", "Human_group_actions", true},
		{"Perch Screaming", "Screaming", true},
		{"Perch Shout", "Shout", true},
		// Perch v2 other vocalizations.
		{"Perch Singing", "Singing", true},
		{"Perch Laughter", "Laughter", true},
		{"Perch Crying_and_sobbing", "Crying_and_sobbing", true},
		{"Perch Sigh", "Sigh", true},
		// Perch v2 non-vocal human sounds and actions.
		{"Perch Cough", "Cough", true},
		{"Perch Breathing", "Breathing", true},
		{"Perch Fart", "Fart", true},
		{"Perch Applause", "Applause", true},
		{"Perch Clapping", "Clapping", true},
		{"Perch Crowd", "Crowd", true},
		{"Perch Walk_and_footsteps", "Walk_and_footsteps", true},
		{"Perch Run", "Run", true},
		// Human taxon (the human species itself).
		{"Perch Homo sapiens", "Homo sapiens", true},
		// Case-insensitive matching (custom/future label files may vary casing).
		{"Perch speech lowercase", "speech", true},
		{"Perch HUMAN_VOICE uppercase", "HUMAN_VOICE", true},
		{"BirdNET human prefix lowercase", "human vocal_human vocal", true},
		// Negatives: bird binomials that merely contain the substring "human".
		{"cicada Pacarina schumanni", "Pacarina schumanni", false},
		{"warbler Phylloscopus humei", "Phylloscopus humei", false},
		{"BirdNET American Robin", "Turdus migratorius_American Robin", false},
		// Negatives: non-human FSD50K classes that co-occur with people.
		{"Perch Thump_and_thud", "Thump_and_thud", false},
		{"Perch Car_passing_by", "Car_passing_by", false},
		// Negatives: dog labels are not human.
		{"Perch Bark is not human", "Bark", false},
		{"Perch Dog is not human", "Dog", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isHumanVocalization(tt.rawLabel))
		})
	}
}

func TestIsDogDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawLabel string
		want     bool
	}{
		// BirdNET v2.4 dog class, English and a non-English locale.
		{"BirdNET Dog (en)", "Dog_Dog", true},
		{"BirdNET Dog (de)", "Dog_Hund", true},
		// Perch v2 dog sound classes and the domestic dog taxon.
		{"Perch Dog", "Dog", true},
		{"Perch Bark", "Bark", true},
		{"Perch Growling", "Growling", true},
		{"Perch Canis familiaris", "Canis familiaris", true},
		// Case-insensitive matching.
		{"Perch bark lowercase", "bark", true},
		{"BirdNET DOG_DOG uppercase", "DOG_DOG", true},
		// Negatives: bird/insect binomials that merely contain the substring "dog".
		// Tachyspiza rhodogaster is a real bird (Vinous-breasted Sparrowhawk); the
		// old "dog" substring match would have wrongly filtered it.
		{"hawk Tachyspiza rhodogaster", "Tachyspiza rhodogaster", false},
		{"katydid Poecilimon doga", "Poecilimon doga", false},
		{"cicada Cicada mordoganensis", "Cicada mordoganensis", false},
		{"cricket Lepidogryllus comparatus", "Lepidogryllus comparatus", false},
		{"cricket Lepidogryllus parvulus", "Lepidogryllus parvulus", false},
		// Negatives: wild canids stay detectable as wildlife.
		{"wolf Canis lupus", "Canis lupus", false},
		{"coyote Canis latrans", "Canis latrans", false},
		{"jackal Canis aureus", "Canis aureus", false},
		// Negatives: humans and birds are not dogs.
		{"Perch Speech is not dog", "Speech", false},
		{"bird Turdus merula", "Turdus merula", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isDogDetection(tt.rawLabel))
		})
	}
}

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
				LastHumanDetection: make(map[string]time.Time),
				LastDogDetection:   make(map[string]time.Time),
			}
			item := classifier.Results{StartTime: start}
			item.Source.ID = source
			result := datastore.Results{Species: tt.species, Confidence: tt.confidence}

			tt.record(p, settings, item, result)

			target, other := p.LastHumanDetection, p.LastDogDetection
			if !tt.isHuman {
				target, other = p.LastDogDetection, p.LastHumanDetection
			}

			got, ok := target[source]
			assert.Equal(t, tt.wantStored, ok, "unexpected record state in target map")
			if tt.wantStored {
				assert.Equal(t, start, got)
			}
			assert.Empty(t, other, "handler must not write the other filter's map")
		})
	}
}

// TestPerchHumanLabelsParityWithNonbird verifies that every key previously in
// perchHumanLabels (except "homo sapiens", which is the iNaturalist taxon
// preserved in perchHumanExtraLabels) is classified as CategoryHuman by the
// shared nonbird package. A failure here means a coverage regression: a label
// that used to engage the privacy filter would silently stop doing so.
func TestPerchHumanLabelsParityWithNonbird(t *testing.T) {
	t.Parallel()

	// The complete former perchHumanLabels key set (37 entries minus "homo sapiens").
	// "homo sapiens" is excluded: it is an iNaturalist taxon, not an AudioSet/FSD50K
	// sound class, so nonbird does not include it. It lives in perchHumanExtraLabels.
	oldAudioSetKeys := []string{
		"speech",
		"speech_synthesizer",
		"male_speech_and_man_speaking",
		"female_speech_and_woman_speaking",
		"child_speech_and_kid_speaking",
		"conversation",
		"chatter",
		"human_voice",
		"human_group_actions",
		"whispering",
		"shout",
		"yell",
		"screaming",
		"singing",
		"male_singing",
		"female_singing",
		"laughter",
		"giggle",
		"chuckle_and_chortle",
		"crying_and_sobbing",
		"gasp",
		"sigh",
		"cough",
		"sneeze",
		"breathing",
		"respiratory_sounds",
		"burping_and_eructation",
		"fart",
		"chewing_and_mastication",
		"crowd",
		"cheering",
		"applause",
		"clapping",
		"finger_snapping",
		"hands",
		"walk_and_footsteps",
		"run",
	}

	for _, key := range oldAudioSetKeys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			cat, ok := nonbird.CategoryOf(key)
			assert.True(t, ok, "nonbird.CategoryOf(%q) must find the key", key)
			assert.Equal(t, nonbird.CategoryHuman, cat,
				"nonbird.CategoryOf(%q) must return CategoryHuman", key)
		})
	}
}

// TestShouldFilterDetection_DropsHumanLabels covers the third changed call site:
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
