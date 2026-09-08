package vocalization

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

func TestIsHuman(t *testing.T) {
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
			assert.Equal(t, tt.want, IsHuman(tt.rawLabel))
		})
	}
}

func TestIsDog(t *testing.T) {
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
			assert.Equal(t, tt.want, IsDog(tt.rawLabel))
		})
	}
}

// TestClassify verifies the single-pass classifier returns the human and dog
// verdicts independently and agrees with IsHuman/IsDog. The classifier's per-
// inference truncation relies on Classify, so its correctness is load-bearing.
func TestClassify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rawLabel   string
		human, dog bool
	}{
		{"Perch speech is human only", "Speech", true, false},
		{"Perch bark is dog only", "Bark", false, true},
		{"Perch growling is dog only", "Growling", false, true},
		{"BirdNET human vocal is human only", "Human vocal_Human vocal", true, false},
		{"BirdNET dog is dog only", "Dog_Hund", false, true},
		{"localized BirdNET human is human only", "Human vocal_Mensch Stimme", true, false},
		{"homo sapiens taxon is human only", "Homo sapiens", true, false},
		{"canis familiaris taxon is dog only", "Canis familiaris", false, true},
		{"uppercase is case-insensitive", "HUMAN_VOICE", true, false},
		{"a bird is neither", "Turdus merula", false, false},
		{"collision cicada is neither", "Pacarina schumanni", false, false},
		{"wild canid is neither", "Canis latrans", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			human, dog := Classify(tt.rawLabel)
			assert.Equal(t, tt.human, human, "human verdict")
			assert.Equal(t, tt.dog, dog, "dog verdict")
			// Classify must agree with the single-verdict helpers.
			assert.Equal(t, IsHuman(tt.rawLabel), human, "Classify vs IsHuman")
			assert.Equal(t, IsDog(tt.rawLabel), dog, "Classify vs IsDog")
		})
	}
}

// TestPerchHumanLabelsParityWithNonbird verifies that every AudioSet/FSD50K human
// sound class the privacy filter relies on is classified as CategoryHuman by the
// shared nonbird package. A failure here means a coverage regression: a label
// that used to engage the privacy filter would silently stop doing so.
func TestPerchHumanLabelsParityWithNonbird(t *testing.T) {
	t.Parallel()

	// The complete AudioSet/FSD50K human-class key set. "homo sapiens" is
	// excluded: it is an iNaturalist taxon, not an AudioSet/FSD50K sound class,
	// so nonbird does not include it. It lives in perchHumanExtraLabels.
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
