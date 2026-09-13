package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	birdNETModelID  = "BirdNET_V2.4"
	greatTitLabel   = "Parus major_Great Tit"
	blackbirdLabel  = "Turdus merula_Common Blackbird"
	greatTitSpecies = "Parus major"
)

// newBirdModelOrchestrator builds an orchestrator where Great Tit is known to
// both bird models, Blackbird only to BirdNET, and the bat model knows a bat.
func newBirdModelOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	return newTestOrchestrator(t,
		&mockModelInstance{id: birdNETModelID, labels: []string{greatTitLabel, blackbirdLabel}},
		&mockModelInstance{id: RegistryIDPerchV2, labels: []string{greatTitSpecies}},
		&mockModelInstance{id: RegistryIDBat, labels: []string{"Pipistrellus pipistrellus"}},
	)
}

func TestSpeciesSharedByBirdModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		species    string
		restrictTo []string
		minModels  int
		want       bool
	}{
		{
			name:      "species every bird model knows",
			species:   greatTitSpecies,
			minModels: 2,
			want:      true,
		},
		{
			name:      "species only one bird model knows",
			species:   "Turdus merula",
			minModels: 2,
			want:      false,
		},
		{
			name:      "bat species is known to no bird model",
			species:   "Pipistrellus pipistrellus",
			minModels: 2,
			want:      false,
		},
		{
			name:       "only one bird model runs on this source",
			species:    greatTitSpecies,
			restrictTo: []string{birdNETModelID},
			minModels:  2,
			want:       false,
		},
		{
			name:       "the bat model alone is not a quorum",
			species:    greatTitSpecies,
			restrictTo: []string{RegistryIDBat},
			minModels:  2,
			want:       false,
		},
		{
			name:       "a model ID that is not loaded cannot confirm",
			species:    greatTitSpecies,
			restrictTo: []string{birdNETModelID, RegistryIDBirdNETV3},
			minModels:  2,
			want:       false,
		},
		{
			name:       "a single model satisfies a quorum of one",
			species:    greatTitSpecies,
			restrictTo: []string{birdNETModelID},
			minModels:  1,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.restrictTo == nil {
				tt.restrictTo = []string{birdNETModelID, RegistryIDPerchV2, RegistryIDBat}
			}
			shared, ok := newBirdModelOrchestrator(t).SpeciesSharedByBirdModels(tt.species, tt.restrictTo, tt.minModels)

			require.True(t, ok, "support must be evaluable for a fully loaded orchestrator")
			assert.Equal(t, tt.want, shared)
		})
	}
}

// TestSpeciesSharedByBirdModels_Unevaluable covers the inputs the caller must
// treat as "cannot be evaluated reliably" and fail open on, rather than reading
// as shared=false.
func TestSpeciesSharedByBirdModels_Unevaluable(t *testing.T) {
	t.Parallel()

	t.Run("nil orchestrator", func(t *testing.T) {
		t.Parallel()
		var o *Orchestrator
		_, ok := o.SpeciesSharedByBirdModels(greatTitSpecies, []string{birdNETModelID, RegistryIDPerchV2}, 2)
		assert.False(t, ok)
	})

	t.Run("empty species name", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t, &mockModelInstance{id: birdNETModelID, labels: []string{greatTitLabel}})
		_, ok := o.SpeciesSharedByBirdModels("", []string{birdNETModelID}, 1)
		assert.False(t, ok)
	})

	t.Run("no model IDs", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t, &mockModelInstance{id: birdNETModelID, labels: []string{greatTitLabel}})
		_, ok := o.SpeciesSharedByBirdModels(greatTitSpecies, nil, 1)
		assert.False(t, ok)
	})

	t.Run("model mid-reload has no readable labels", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t, &mockModelInstance{id: birdNETModelID, labels: []string{greatTitLabel}})
		// An entry whose instance was torn down by a reload in flight.
		o.models[RegistryIDPerchV2] = &modelEntry{}
		_, ok := o.SpeciesSharedByBirdModels(greatTitSpecies, []string{birdNETModelID, RegistryIDPerchV2}, 2)
		require.False(t, ok)

		entry := o.models[RegistryIDPerchV2]
		entry.mu.Lock()
		entry.instance = &mockModelInstance{id: RegistryIDPerchV2, labels: []string{greatTitSpecies}}
		entry.mu.Unlock()

		shared, ok := o.SpeciesSharedByBirdModels(greatTitSpecies, []string{birdNETModelID, RegistryIDPerchV2}, 2)
		require.True(t, ok, "unreadable labels must not be memoized")
		assert.True(t, shared)
	})
}

// TestIsBirdCapableModel pins the mapping to the model-type resolution the v2
// datastore uses, so a new registry entry is classified by that rule rather than
// by an allow-list here.
func TestIsBirdCapableModel(t *testing.T) {
	t.Parallel()

	assert.False(t, IsBirdCapableModel(RegistryIDBat))
	assert.True(t, IsBirdCapableModel(birdNETModelID))
	assert.True(t, IsBirdCapableModel(RegistryIDBirdNETV3))
	assert.True(t, IsBirdCapableModel(RegistryIDPerchV2))
	assert.True(t, IsBirdCapableModel(RegistryIDBSG))
	assert.True(t, IsBirdCapableModel("unknown-model"), "an unknown ID falls back to the BirdNET default")
}

func TestSpeciesSharedByBirdModels_MemoInvalidation(t *testing.T) {
	t.Parallel()
	o := newBirdModelOrchestrator(t)
	modelIDs := []string{birdNETModelID, RegistryIDPerchV2}
	shared, ok := o.SpeciesSharedByBirdModels(greatTitSpecies, modelIDs, 2)
	require.True(t, ok)
	require.True(t, shared)

	entry := o.models[RegistryIDPerchV2]
	entry.mu.Lock()
	entry.instance = &mockModelInstance{id: RegistryIDPerchV2, labels: []string{blackbirdLabel}}
	entry.mu.Unlock()

	shared, ok = o.SpeciesSharedByBirdModels(greatTitSpecies, modelIDs, 2)
	require.True(t, ok)
	require.True(t, shared, "the previous species set must remain memoized until a rebuild")

	o.rebuildMu.Lock()
	o.rebuildSpeciesIndexLocked()
	o.rebuildMu.Unlock()

	shared, ok = o.SpeciesSharedByBirdModels(greatTitSpecies, modelIDs, 2)
	require.True(t, ok)
	assert.False(t, shared, "a rebuild must invalidate the previous species set")
}

func TestModelSpeciesSets_StoreGeneration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		invalidate bool
	}{
		{name: "current generation is stored"},
		{name: "stale generation is refused", invalidate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var sets modelSpeciesSets
			_, generation := sets.lookup(birdNETModelID)
			if tt.invalidate {
				sets.invalidate()
			}

			want := map[string]struct{}{greatTitSpecies: {}}
			sets.store(birdNETModelID, generation, want)
			got, _ := sets.lookup(birdNETModelID)
			if tt.invalidate {
				assert.Nil(t, got, "a stale build must not republish a species set")
				return
			}
			assert.Equal(t, want, got)
		})
	}
}
