package processor

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// defaultRarityBands returns the shipped default bands.
func defaultRarityBands() []conf.RarityBand {
	return []conf.RarityBand{
		{MaxOccurrence: conf.DefaultRarityRareMaxOccurrence, MinDetections: conf.DefaultRarityRareMinDetections},
		{MaxOccurrence: conf.DefaultRarityUncommonMaxOccurrence, MinDetections: conf.DefaultRarityUncommonMinDetections},
	}
}

func TestRarityMinDetections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		enabled    bool
		bands      []conf.RarityBand
		valid      bool
		occurrence float64
		want       int
	}{
		{name: "disabled", enabled: false, bands: defaultRarityBands(), valid: true, occurrence: 0.05, want: 0},
		{name: "invalid occurrence is not read as rare", enabled: true, bands: defaultRarityBands(), valid: false, occurrence: 0, want: 0},
		{name: "genuine zero is rare", enabled: true, bands: defaultRarityBands(), valid: true, occurrence: 0, want: 3},
		{name: "below rare bound", enabled: true, bands: defaultRarityBands(), valid: true, occurrence: 0.0999, want: 3},
		{name: "at rare bound falls to next band", enabled: true, bands: defaultRarityBands(), valid: true, occurrence: 0.10, want: 2},
		{name: "within uncommon band", enabled: true, bands: defaultRarityBands(), valid: true, occurrence: 0.5, want: 2},
		{name: "at top bound matches no band", enabled: true, bands: defaultRarityBands(), valid: true, occurrence: 0.90, want: 0},
		{name: "common species", enabled: true, bands: defaultRarityBands(), valid: true, occurrence: 1.0, want: 0},
		{name: "no bands", enabled: true, bands: nil, valid: true, occurrence: 0.05, want: 0},
		{name: "NaN occurrence is not read as rare", enabled: true, bands: defaultRarityBands(), valid: true, occurrence: math.NaN(), want: 0},
		{
			name:    "unsorted bands still pick the tightest match",
			enabled: true,
			bands: []conf.RarityBand{
				{MaxOccurrence: 0.90, MinDetections: 2},
				{MaxOccurrence: 0.10, MinDetections: 3},
			},
			valid:      true,
			occurrence: 0.05,
			want:       3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rf := &conf.RarityFilterSettings{Enabled: tt.enabled, Bands: tt.bands}
			assert.Equal(t, tt.want, rarityMinDetections(rf, tt.valid, tt.occurrence))
		})
	}
}

func TestEffectiveMinDetections(t *testing.T) {
	t.Parallel()

	newItem := func(modelID string, occurrence float64, valid bool) *PendingDetection {
		return &PendingDetection{
			BestModelID: modelID,
			Detection: Detections{Result: detection.Result{
				Occurrence:      occurrence,
				OccurrenceValid: valid,
			}},
		}
	}

	// Level 0 (the default) gives every bird model a false positive count of 1, so any
	// rarity band (at least 1) that exceeds it is observable.
	settings := &conf.Settings{}
	settings.Realtime.RarityFilter = conf.RarityFilterSettings{Enabled: true, Bands: defaultRarityBands()}
	birdBase := calculateMinDetectionsForModel(settings, classifier.RegistryIDBirdNETV24)
	batBase := calculateMinDetectionsForModel(settings, classifier.RegistryIDBat)
	require.Less(t, birdBase, conf.DefaultRarityRareMinDetections,
		"test precondition: the rare band must exceed the false positive count")

	t.Run("rare bird raises the count", func(t *testing.T) {
		t.Parallel()
		got := effectiveMinDetections(settings, newItem(classifier.RegistryIDBirdNETV24, 0.05, true))
		assert.Equal(t, minDetectionRequirement{count: conf.DefaultRarityRareMinDetections, rarityApplied: true}, got)
	})

	t.Run("common bird keeps the false positive filter count", func(t *testing.T) {
		t.Parallel()
		got := effectiveMinDetections(settings, newItem(classifier.RegistryIDBirdNETV24, 0.95, true))
		assert.Equal(t, minDetectionRequirement{count: birdBase}, got)
	})

	t.Run("invalid occurrence keeps the false positive filter count", func(t *testing.T) {
		t.Parallel()
		got := effectiveMinDetections(settings, newItem(classifier.RegistryIDBirdNETV24, 0, false))
		assert.Equal(t, minDetectionRequirement{count: birdBase}, got)
	})

	t.Run("bat model is never raised", func(t *testing.T) {
		t.Parallel()
		got := effectiveMinDetections(settings, newItem(classifier.RegistryIDBat, 0.05, true))
		assert.Equal(t, minDetectionRequirement{count: batBase}, got)
	})

	t.Run("never lowers a stricter false positive filter count", func(t *testing.T) {
		t.Parallel()
		strict := conf.CloneSettings(settings)
		strict.Realtime.FalsePositiveFilter.Level = 5
		strict.BirdNET.Overlap = 2.4
		strictBase := calculateMinDetectionsForModel(strict, classifier.RegistryIDBirdNETV24)
		require.Greater(t, strictBase, conf.DefaultRarityRareMinDetections,
			"test precondition: level 5 must exceed the rare band")

		got := effectiveMinDetections(strict, newItem(classifier.RegistryIDBirdNETV24, 0.05, true))
		assert.Equal(t, minDetectionRequirement{count: strictBase}, got)
	})
}

func TestMinDetectionRequirementDiscardReason(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "false positive, matched 1/2 times",
		minDetectionRequirement{count: 2}.discardReason(1))
	assert.Equal(t, "rarity filter, matched 2/3 times",
		minDetectionRequirement{count: 3, rarityApplied: true}.discardReason(2))
}

// TestShouldDiscardDetection_RarityReason checks that a detection below a count the
// rarity filter raised is discarded with the rarity reason, not as a false positive.
func TestShouldDiscardDetection_RarityReason(t *testing.T) {
	t.Parallel()

	p := &Processor{Settings: &conf.Settings{}}
	item := &PendingDetection{Count: 2, Detection: Detections{Result: detection.Result{
		Species: detection.Species{CommonName: "Rare Bird"},
	}}}

	discard, reason := p.shouldDiscardDetection(item, p.Settings, minDetectionRequirement{count: 3, rarityApplied: true})
	assert.True(t, discard)
	assert.Equal(t, "rarity filter, matched 2/3 times", reason)

	discard, _ = p.shouldDiscardDetection(item, p.Settings, minDetectionRequirement{count: 2, rarityApplied: true})
	assert.False(t, discard, "a detection that reaches the raised count is kept")
}
