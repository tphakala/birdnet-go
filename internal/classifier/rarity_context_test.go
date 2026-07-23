package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// A legacy/current synonym pair from the vendored OpenFauna alias map. The geomodel
// carries the current name while the BirdNET v2.4 classifier still emits the legacy
// one, which is the split that made Laughing Dove disappear under the geomodel.
const (
	aliasLegacyLabel    = "Streptopelia senegalensis_Laughing Dove"
	aliasCanonicalLabel = "Spilopelia senegalensis_Laughing Dove"
	aliasCanonicalKey   = "spilopelia senegalensis"
)

// Two species BirdNET v2.4 ships separately while the alias map maps the second onto
// the first. Any lookup keyed only on the canonical name merges them and reports one
// bird's occurrence probability for the other.
const (
	collidingLabelA = "Dicrurus adsimilis_Fork-tailed Drongo"
	collidingLabelB = "Dicrurus divaricatus_Glossy-backed Drongo"
)

// These tests read settings through BirdNET.currentSettings, which resolves via
// conf.CurrentOrFallback and therefore prefers the process-global snapshot over the
// instance's own pointer. They must publish their fixture globally and must not run in
// parallel, matching the rest of this package; a sibling test leaving a snapshot
// published would otherwise silently supply their settings.
func publishTestSettings(t *testing.T, settings *conf.Settings) {
	t.Helper()
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })
}

func TestBuildOccurrenceIndexAndLookup(t *testing.T) {
	scores := []SpeciesScore{
		{Label: aliasCanonicalLabel, Score: 0.4},
		{Label: "Turdus merula_Common Blackbird", Score: 0.7},
	}
	index := buildOccurrenceIndex(scores)

	tests := []struct {
		name      string
		lookup    string
		wantScore float64
		wantFound bool
	}{
		{name: "exact scientific name", lookup: aliasCanonicalLabel, wantScore: 0.4, wantFound: true},
		{name: "legacy synonym resolves to the canonical entry", lookup: aliasLegacyLabel, wantScore: 0.4, wantFound: true},
		{name: "bare scientific name", lookup: "Turdus merula", wantScore: 0.7, wantFound: true},
		{name: "case and space are normalized", lookup: "  tURdus MERula  ", wantScore: 0.7, wantFound: true},
		{name: "absent species", lookup: "Myotis brandtii", wantFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := lookupOccurrence(index, tt.lookup)
			assert.Equal(t, tt.wantFound, found)
			if tt.wantFound {
				assert.InDelta(t, tt.wantScore, got, 0.001)
			}
		})
	}
}

// TestBuildOccurrenceIndex_CollidingSpeciesKeepOwnScores guards the defect that an
// earlier revision of this code shipped: keying the occurrence cache on the canonical
// name alone merged Dicrurus adsimilis and D. divaricatus, so both reported whichever
// score the map happened to write last, and the cached and uncached paths disagreed
// about which one that was.
func TestBuildOccurrenceIndex_CollidingSpeciesKeepOwnScores(t *testing.T) {
	require.Equal(t, canonicalSpeciesKey(collidingLabelA), canonicalSpeciesKey(collidingLabelB),
		"fixture is only meaningful while the alias map still merges these two species")

	// Descending by score, as getProbableSpecies returns it.
	index := buildOccurrenceIndex([]SpeciesScore{
		{Label: collidingLabelA, Score: 0.9},
		{Label: collidingLabelB, Score: 0.1},
	})

	gotA, foundA := lookupOccurrence(index, collidingLabelA)
	require.True(t, foundA)
	assert.InDelta(t, 0.9, gotA, 0.001, "exact name must win over the alias collapse")

	gotB, foundB := lookupOccurrence(index, collidingLabelB)
	require.True(t, foundB)
	assert.InDelta(t, 0.1, gotB, 0.001, "the merged species must keep its own score")
}

// newAliasedGeomodelBirdNET builds a primary model whose classifier labels use the
// legacy synonym while the geomodel labels use the current name, the configuration
// that separates the occurrence cache's keys from the caller's lookup key.
func newAliasedGeomodelBirdNET(t *testing.T, geoScore float32) (*BirdNET, *fakeRangeFilter) {
	t.Helper()

	classifierLabels := []string{aliasLegacyLabel, "Turdus merula_Common Blackbird"}
	geomodelLabels := []string{aliasCanonicalLabel, "Turdus merula_Amsel"}

	settings := &conf.Settings{}
	settings.BirdNET.Labels = classifierLabels
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.Latitude = 60.1
	settings.BirdNET.Longitude = 24.9
	settings.BirdNET.RangeFilter.Model = conf.RangeFilterModelV3
	settings.BirdNET.RangeFilter.Threshold = 0.01
	publishTestSettings(t, settings)

	inner := &fakeRangeFilter{scores: []float32{geoScore, geoScore}}
	mapped := newMappedRangeFilter(inner, classifierLabels, geomodelLabels, 0.0)

	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		ModelInfo:    ModelInfo{ID: RegistryIDBirdNETV3, Name: ModelNameBirdNETv30},
		rangeFilter:  mapped,
	}
	t.Cleanup(bn.Delete)

	return bn, inner
}

// TestGetSpeciesOccurrenceAtTime_AliasHitsCache guards the split that made the
// occurrence cache useless for taxonomic synonyms. The universal geomodel path labels
// its scores with geomodel names, so a cache keyed on the raw name alone never matched
// a caller passing the classifier's legacy name: every lookup missed, fell through to a
// full probable-species recomputation on the per-detection path, and then still returned
// 0.0 because the fallback scan compared raw names too.
func TestGetSpeciesOccurrenceAtTime_AliasHitsCache(t *testing.T) {
	const wantScore = 0.42
	bn, inner := newAliasedGeomodelBirdNET(t, wantScore)
	at := time.Date(2026, 5, 14, 12, 0, 0, 0, time.Local)

	got := bn.GetSpeciesOccurrenceAtTime(aliasLegacyLabel, at)
	assert.InDelta(t, wantScore, got, 0.001,
		"legacy classifier label must resolve to the geomodel's canonical score")
	require.Equal(t, 1, inner.calls,
		"first lookup should populate the cache and answer from it, not also run the fallback")

	got = bn.GetSpeciesOccurrenceAtTime(aliasLegacyLabel, at)
	assert.InDelta(t, wantScore, got, 0.001)
	assert.Equal(t, 1, inner.calls, "second lookup must be served entirely from cache")
}

// TestGetSpeciesOccurrenceAtTime_CanonicalNameAlsoResolves covers the other direction:
// detections are normalized to the canonical name before storage, so the canonical name
// must resolve against a classifier whose labels are still legacy.
func TestGetSpeciesOccurrenceAtTime_CanonicalNameAlsoResolves(t *testing.T) {
	const wantScore = 0.42
	bn, _ := newAliasedGeomodelBirdNET(t, wantScore)
	at := time.Date(2026, 5, 14, 12, 0, 0, 0, time.Local)

	got := bn.GetSpeciesOccurrenceAtTime(aliasCanonicalLabel, at)
	assert.InDelta(t, wantScore, got, 0.001)
}

// TestGetSpeciesOccurrenceAtTime_UnknownSpecies exercises the uncached fallback scan,
// which the cache-hit tests above never reach.
func TestGetSpeciesOccurrenceAtTime_UnknownSpecies(t *testing.T) {
	bn, _ := newAliasedGeomodelBirdNET(t, 0.42)
	at := time.Date(2026, 5, 14, 12, 0, 0, 0, time.Local)

	got := bn.GetSpeciesOccurrenceAtTime("Myotis brandtii_Brandt's Bat", at)
	assert.InDelta(t, 0.0, got, 0.001, "a species the range filter cannot score has no occurrence")
}

func TestGetRarityContext_UniversalGeomodel(t *testing.T) {
	bn, _ := newAliasedGeomodelBirdNET(t, 0.5)
	orch := &Orchestrator{Settings: bn.Settings, ModelInfo: bn.ModelInfo, primary: bn}

	scores, geomodelLabels, classifierLabels, err := orch.GetRarityContext(time.Now())
	require.NoError(t, err)

	assert.NotEmpty(t, scores, "universal geomodel path should return scored species")
	assert.Contains(t, geomodelLabels, aliasCanonicalLabel,
		"geomodel vocabulary should come back so coverage is checked against it")
	assert.Contains(t, classifierLabels, aliasLegacyLabel,
		"classifier vocabulary should come back as the fallback for non-geomodel backends")

	// The classifier labels must be a copy: a caller mutating them must not corrupt the
	// live settings the model classifies against. geomodelLabels is deliberately NOT
	// copied (it aliases the range filter's immutable label slice, as
	// PrimaryRangeFilterCoverage and RangeFilterStatus also return it), so this
	// asymmetry is contractual rather than accidental.
	classifierLabels[0] = "mutated"
	assert.Equal(t, aliasLegacyLabel, bn.Settings.BirdNET.Labels[0])
}

// TestGetRarityContext_NoGeomodel covers the case where no universal geomodel produced
// the scores, so the caller must fall back to the classifier vocabulary rather than
// treat every species as uncovered. Location is unconfigured here, which is one of the
// four states that yields an empty geomodel vocabulary; the TFLite meta model and the
// plain ONNX range filter reach the same state by taking the legacy path.
func TestGetRarityContext_NoGeomodel(t *testing.T) {
	settings := &conf.Settings{}
	settings.BirdNET.Labels = []string{"Turdus merula_Common Blackbird"}
	settings.BirdNET.LocationConfigured = false
	publishTestSettings(t, settings)

	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		ModelInfo:    ModelInfo{ID: BirdNET_V2_4, Name: ModelNameBirdNETv24},
		rangeFilter:  &fakeRangeFilter{scores: []float32{0.5}},
	}
	t.Cleanup(bn.Delete)
	orch := &Orchestrator{Settings: settings, ModelInfo: bn.ModelInfo, primary: bn}

	_, geomodelLabels, classifierLabels, err := orch.GetRarityContext(time.Now())
	require.NoError(t, err)

	assert.Empty(t, geomodelLabels, "no universal geomodel means no geomodel vocabulary")
	assert.Contains(t, classifierLabels, "Turdus merula_Common Blackbird")
}

func TestGetRarityContext_NilPrimary(t *testing.T) {
	orch := &Orchestrator{}

	scores, geomodelLabels, classifierLabels, err := orch.GetRarityContext(time.Now())
	require.NoError(t, err)
	assert.Nil(t, scores)
	assert.Nil(t, geomodelLabels)
	assert.Nil(t, classifierLabels)
}
