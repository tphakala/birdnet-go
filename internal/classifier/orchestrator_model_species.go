// orchestrator_model_species.go exposes which species each loaded model can
// classify, as an immutable snapshot that follows the model topology.
package classifier

import (
	"strings"

	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// ModelSpecies is an immutable snapshot of the species each loaded model can
// classify, keyed by speciesindex.CanonicalKey. All methods are safe on a nil
// receiver and for concurrent use.
type ModelSpecies struct {
	// generation is the Orchestrator topology generation read before the models
	// were walked.
	generation uint64
	// sets maps a registry ID to its species: canonical species key to the model's
	// own scientific name for it (the first label with that key). A model with a
	// live instance but no labels has an entry with an empty map.
	sets map[string]map[string]string
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

// ScientificName returns the model's own scientific name of the species with the
// given canonical key (see speciesindex.CanonicalKey): the spelling its labels
// carry, which may be an older or alternate name of the species. It is false for
// an unknown model, an empty key, an unknown species, or a nil receiver.
func (m *ModelSpecies) ScientificName(modelID, key string) (string, bool) {
	if m == nil || key == "" {
		return "", false
	}
	name, ok := m.sets[modelID][key]
	return name, ok
}

// invalidateModelSpecies records a model-topology change (load, unload, reload or
// Delete): it bumps topologyGen, which ModelSpecies compares against the
// generation its cached snapshot was built for, and drops the cached snapshot so
// a stale per-model set is not kept alive until the next call. A code path that
// changes which models are loaded must call it after the change.
func (o *Orchestrator) invalidateModelSpecies() {
	o.topologyGen.Add(1)
	o.modelSpecies.Store(nil)
}

// ModelSpecies returns the per-model species snapshot. It is built lazily on the
// first call and rebuilt only after the model topology changed, so the common
// path takes no lock. It returns nil on a nil receiver.
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

// buildModelSpeciesSets walks the loaded models the way AllLabels does: the v2.4
// anchor returned by orderedEntryRefs, and every other entry by
// capturing its instance under entry.mu and calling Labels() only after releasing
// it, so entry.mu is not held across bn.mu. The anchor is keyed by its registry
// constant; every other entry with a live instance is keyed by its o.models key
// and takes part whatever its ID.
func (o *Orchestrator) buildModelSpeciesSets() map[string]map[string]string {
	index := o.SpeciesSnapshot()
	anchor, refs := o.orderedEntryRefs()
	sets := make(map[string]map[string]string, len(refs)+1)
	if anchor != nil {
		sets[RegistryIDBirdNETV24] = canonicalKeyNames(index, anchor.Labels())
	}
	for _, ref := range refs {
		ref.entry.mu.Lock()
		instance := ref.entry.instance
		ref.entry.mu.Unlock()
		if instance == nil {
			continue
		}
		sets[ref.id] = canonicalKeyNames(index, instance.Labels())
	}
	return sets
}

// canonicalKeyNames returns, for the labels, a map from each non-empty canonical
// key to the scientific name of the first label with that key. The species-index
// snapshot supplies a key from its memo when the label is in the index and
// computes it otherwise, so a snapshot that lags the topology only costs a
// recompute.
func canonicalKeyNames(index *speciesindex.Snapshot, labels []string) map[string]string {
	names := make(map[string]string, len(labels))
	for _, label := range labels {
		key := index.CanonicalKey(label)
		if key == "" {
			continue
		}
		if _, seen := names[key]; !seen {
			names[key] = strings.TrimSpace(detection.ExtractScientificName(label))
		}
	}
	return names
}
