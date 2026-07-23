package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// A legacy/current synonym pair from the vendored OpenFauna alias map. The geomodel
// carries the current name while the BirdNET v2.4 classifier still emits the legacy
// one, which is the split that made Laughing Dove disappear under the geomodel.
const (
	aliasLegacyLabel    = "Streptopelia senegalensis_Laughing Dove"
	aliasCanonicalLabel = "Spilopelia senegalensis_Laughing Dove"
	aliasCanonicalKey   = "spilopelia senegalensis"
)

// countingRangeFilter is a range-filter double that reports how many predictions it
// served, so a test can prove a lookup was answered from cache rather than by
// recomputing the probable-species list.
type countingRangeFilter struct {
	scores []float32
	calls  int
}

func (f *countingRangeFilter) Predict(_, _, _ float32) ([]float32, error) {
	f.calls++
	out := make([]float32, len(f.scores))
	copy(out, f.scores)
	return out, nil
}

func (f *countingRangeFilter) NumSpecies() int { return len(f.scores) }
func (f *countingRangeFilter) Close()          {}

func TestCanonicalScoreKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		label string
		want  string
	}{
		{
			name:  "legacy synonym resolves to canonical name",
			label: aliasLegacyLabel,
			want:  aliasCanonicalKey,
		},
		{
			name:  "canonical name is unchanged",
			label: aliasCanonicalLabel,
			want:  aliasCanonicalKey,
		},
		{
			name:  "bare scientific name matches the label form",
			label: "Streptopelia senegalensis",
			want:  aliasCanonicalKey,
		},
		{
			name:  "case and surrounding space are normalized",
			label: "  sTREPtopelia SENEgalensis  ",
			want:  aliasCanonicalKey,
		},
		{
			name:  "unaliased name passes through lowercased",
			label: "Turdus merula_Common Blackbird",
			want:  "turdus merula",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, canonicalScoreKey(tt.label))
		})
	}
}

// newAliasedGeomodelBirdNET builds a primary model whose classifier labels use the
// legacy synonym while the geomodel labels use the current name, the configuration
// that separates the occurrence cache's keys from the caller's lookup key.
func newAliasedGeomodelBirdNET(t *testing.T, geoScore float32) (*BirdNET, *countingRangeFilter) {
	t.Helper()

	classifierLabels := []string{aliasLegacyLabel, "Turdus merula_Common Blackbird"}
	geomodelLabels := []string{aliasCanonicalLabel, "Turdus merula_Amsel"}

	settings := &conf.Settings{}
	settings.BirdNET.Labels = classifierLabels
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.Latitude = 60.1
	settings.BirdNET.Longitude = 24.9
	settings.BirdNET.RangeFilter.Model = "v3"
	settings.BirdNET.RangeFilter.Threshold = 0.01

	inner := &countingRangeFilter{scores: []float32{geoScore, geoScore}}
	mapped := newMappedRangeFilter(inner, classifierLabels, geomodelLabels, 0.0)

	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		ModelInfo:    ModelInfo{ID: RegistryIDBirdNETV3, Name: ModelNameBirdNETv30},
		rangeFilter:  mapped,
	}

	return bn, inner
}

// TestGetSpeciesOccurrenceAtTime_AliasHitsCache guards the split that made the
// occurrence cache useless for taxonomic synonyms. The universal geomodel path labels
// its scores with geomodel names, so a cache keyed on the raw name never matches a
// caller passing the classifier's legacy name: every lookup missed and silently fell
// through to a full probable-species recomputation, on the per-detection path.
func TestGetSpeciesOccurrenceAtTime_AliasHitsCache(t *testing.T) {
	t.Parallel()

	const wantScore = 0.42
	bn, inner := newAliasedGeomodelBirdNET(t, wantScore)
	at := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

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
// detections are normalized to the canonical name before storage, so the canonical
// name must resolve against a classifier whose labels are still legacy.
func TestGetSpeciesOccurrenceAtTime_CanonicalNameAlsoResolves(t *testing.T) {
	t.Parallel()

	const wantScore = 0.42
	bn, _ := newAliasedGeomodelBirdNET(t, wantScore)
	at := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

	got := bn.GetSpeciesOccurrenceAtTime(aliasCanonicalLabel, at)
	assert.InDelta(t, wantScore, got, 0.001)
}

func TestGetRarityContext_UniversalGeomodel(t *testing.T) {
	t.Parallel()

	bn, _ := newAliasedGeomodelBirdNET(t, 0.5)
	orch := &Orchestrator{Settings: bn.Settings, ModelInfo: bn.ModelInfo, primary: bn}

	scores, geomodelLabels, classifierLabels, err := orch.GetRarityContext(time.Now())
	require.NoError(t, err)

	assert.NotEmpty(t, scores, "universal geomodel path should return scored species")
	assert.Contains(t, geomodelLabels, aliasCanonicalLabel,
		"geomodel vocabulary should come back so coverage is checked against it")
	assert.Contains(t, classifierLabels, aliasLegacyLabel,
		"classifier vocabulary should come back as the fallback for non-geomodel backends")

	// The classifier labels must be a copy: a caller mutating them must not corrupt
	// the live settings the model classifies against.
	classifierLabels[0] = "mutated"
	assert.Equal(t, aliasLegacyLabel, bn.Settings.BirdNET.Labels[0])
}

// TestGetRarityContext_NoGeomodel covers the backends whose vocabulary is the
// classifier's own label set, where geomodelLabels is legitimately empty and callers
// must fall back rather than treat the species as having no coverage.
func TestGetRarityContext_NoGeomodel(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.BirdNET.Labels = []string{"Turdus merula_Common Blackbird"}
	settings.BirdNET.LocationConfigured = false

	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		ModelInfo:    ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
		rangeFilter:  &countingRangeFilter{scores: []float32{0.5}},
	}
	orch := &Orchestrator{Settings: settings, ModelInfo: bn.ModelInfo, primary: bn}

	_, geomodelLabels, classifierLabels, err := orch.GetRarityContext(time.Now())
	require.NoError(t, err)

	assert.Empty(t, geomodelLabels, "no universal geomodel means no geomodel vocabulary")
	assert.Contains(t, classifierLabels, "Turdus merula_Common Blackbird")
}

func TestGetRarityContext_NilPrimary(t *testing.T) {
	t.Parallel()

	orch := &Orchestrator{}

	scores, geomodelLabels, classifierLabels, err := orch.GetRarityContext(time.Now())
	require.NoError(t, err)
	assert.Nil(t, scores)
	assert.Nil(t, geomodelLabels)
	assert.Nil(t, classifierLabels)
}
