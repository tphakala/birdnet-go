// orchestrator_model_species.go exposes which species each loaded model can
// classify, as an immutable snapshot that follows the model topology.
package classifier

import (
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// ModelSpecies is an immutable snapshot of the species each loaded model can
// classify, keyed by speciesindex.CanonicalKey. All methods are safe on a nil
// receiver and for concurrent use.
type ModelSpecies struct {
	// generation is the Orchestrator topology generation read before the models
	// were walked.
	generation uint64
	// sets maps a registry ID to the canonical species keys of its labels. A model
	// with a live instance but no labels has an entry with an empty set.
	sets map[string]map[string]struct{}
}

// Generation returns the topology generation the snapshot was built for, or 0 on a
// nil receiver.
func (m *ModelSpecies) Generation() uint64 {
	if m == nil {
		return 0
	}
	return m.generation
}

// HasModel reports whether the model was loaded with a live instance when the
// snapshot was built.
func (m *ModelSpecies) HasModel(modelID string) bool {
	if m == nil {
		return false
	}
	_, ok := m.sets[modelID]
	return ok
}

// Knows reports whether the model can classify the species with the given
// canonical key (see speciesindex.CanonicalKey). It is false for an unknown model,
// an empty key, or a nil receiver.
func (m *ModelSpecies) Knows(modelID, key string) bool {
	if m == nil || key == "" {
		return false
	}
	_, ok := m.sets[modelID][key]
	return ok
}

// ModelSpecies returns the per-model species snapshot. It is built lazily on the
// first call and rebuilt only after the model topology changed (a load, unload or
// reload, tracked by topologyGen), so the steady-state cost is one atomic load and
// a compare. It returns nil on a nil receiver.
//
// It MUST be called with no orchestrator lock held, and never under a caller lock
// that the inference result path also takes: the walk calls Labels() on each
// instance, and BirdNET.Labels takes bn.mu, which inference holds.
func (o *Orchestrator) ModelSpecies() *ModelSpecies {
	if o == nil {
		return nil
	}
	if cached := o.modelSpecies.Load(); cached != nil && cached.generation == o.topologyGen.Load() {
		return cached
	}

	o.modelSpeciesMu.Lock()
	defer o.modelSpeciesMu.Unlock()
	// Read the generation BEFORE walking the models. A topology change that races
	// the walk leaves a snapshot stamped with the older generation, so the next
	// call rebuilds instead of trusting a possibly stale set.
	gen := o.topologyGen.Load()
	if cached := o.modelSpecies.Load(); cached != nil && cached.generation == gen {
		return cached
	}
	built := &ModelSpecies{generation: gen, sets: o.buildModelSpeciesSets()}
	o.modelSpecies.Store(built)
	return built
}

// buildModelSpeciesSets walks the loaded models exactly like AllLabels (the v2.4
// anchor through the orderedEntryRefs primary pointer, every other entry by
// capturing its instance under entry.mu and calling Labels() after releasing it)
// and keys each model's labels by speciesindex.CanonicalKey. It has no per-model-ID
// cases: every entry with a live instance takes part.
func (o *Orchestrator) buildModelSpeciesSets() map[string]map[string]struct{} {
	primary, refs := o.orderedEntryRefs()
	sets := make(map[string]map[string]struct{}, len(refs)+1)
	if primary != nil {
		sets[RegistryIDBirdNETV24] = canonicalKeySet(primary.Labels())
	}
	for _, ref := range refs {
		// Capture the instance under entry.mu, release it, THEN call Labels(), as
		// AllLabels does, so entry.mu is not held across bn.mu.
		ref.entry.mu.Lock()
		instance := ref.entry.instance
		ref.entry.mu.Unlock()
		if instance == nil {
			continue
		}
		sets[ref.id] = canonicalKeySet(instance.Labels())
	}
	return sets
}

// canonicalKeySet returns the set of non-empty speciesindex.CanonicalKey values of
// the labels.
func canonicalKeySet(labels []string) map[string]struct{} {
	set := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		if key := speciesindex.CanonicalKey(label); key != "" {
			set[key] = struct{}{}
		}
	}
	return set
}
