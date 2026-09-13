// species_consensus.go exposes the per-model species support lookups used by the
// processor's first-daily-detection consensus rule. They are read-only queries over
// label sets the orchestrator already holds; nothing here participates in inference.
package classifier

import (
	"slices"
	"sync"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// IsBirdCapableModel reports whether a model classifies birds. It reuses the
// model-type resolution the v2 datastore already uses to stamp a detection's
// taxonomic class, so a future model is classified by the same rule rather than
// by an allow-list here that it would silently fall out of.
//
// An unknown model ID resolves to the BirdNET default, i.e. bird-capable.
func IsBirdCapableModel(modelID string) bool {
	info := DetectionModelInfoForID(modelID)
	return detection.ResolveModelType(info.Name, info.Version) != entities.ModelTypeBat
}

// modelSpeciesSets memoizes each model's canonical species keys, so a membership
// check is a map lookup instead of a copy and canonicalization of the model's whole
// label set. Sets are built lazily, only for models a caller asks about, and are
// dropped by invalidate on every model-topology rebuild (rebuildSpeciesIndexLocked).
// mu is a leaf lock: it is never held while model labels are read.
//
// Between a model's label change and the rebuild that follows it, a lookup can
// still be answered from the previous set. That window is the length of one
// reload, and its worst case is one first-of-day detection held back that the
// replacement model could not have confirmed; the next window re-evaluates.
type modelSpeciesSets struct {
	mu         sync.Mutex
	generation uint64
	sets       map[string]map[string]struct{}
}

// invalidate drops every memoized set. A build that read labels before this call
// is refused by store, so it cannot republish a stale set.
func (c *modelSpeciesSets) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.sets = nil
}

// lookup returns the memoized set for modelID (nil when absent) and the generation
// a subsequent build must be stored under.
func (c *modelSpeciesSets) lookup(modelID string) (set map[string]struct{}, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sets[modelID], c.generation
}

// store memoizes set for modelID unless an invalidate happened since generation
// was read.
func (c *modelSpeciesSets) store(modelID string, generation uint64, set map[string]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return
	}
	if c.sets == nil {
		c.sets = make(map[string]map[string]struct{})
	}
	c.sets[modelID] = set
}

// SpeciesSharedByBirdModels reports whether at least minModels of modelIDs are
// loaded, active, bird-capable models and every one of them can predict
// scientificName. It is the "could a second model realistically have confirmed
// this?" test. Callers pass the models analysing one audio source; IDs that are not
// loaded are ignored, since they cannot confirm anything.
//
// ok is false when the answer cannot be trusted (no orchestrator, no model IDs, an
// unusable species name, or a model whose labels cannot be read because its
// instance is mid-reload). Callers must fail open on it rather than read
// shared=false.
func (o *Orchestrator) SpeciesSharedByBirdModels(scientificName string, modelIDs []string, minModels int) (shared, ok bool) {
	if o == nil || minModels < 1 || len(modelIDs) == 0 {
		return false, false
	}
	key := canonicalSpeciesKey(scientificName)
	if key == "" {
		return false, false
	}

	// Snapshot under o.mu and release it before any label is read; see labelsOf.
	o.mu.RLock()
	primary := o.primary
	primaryID := o.ModelInfo.ID
	refs := make([]entryRef, 0, len(modelIDs))
	for id, entry := range o.models {
		// IsModelActive reads an atomic, not o.mu, so it is safe under the RLock.
		if slices.Contains(modelIDs, id) && IsBirdCapableModel(id) && o.IsModelActive(id) {
			refs = append(refs, entryRef{id: id, entry: entry})
		}
	}
	o.mu.RUnlock()

	// Settle the quorum before touching any label set: below it the answer is no.
	if len(refs) < minModels {
		return false, true
	}

	for _, ref := range refs {
		set, readable := o.modelSpecies(ref, primary, primaryID)
		if !readable {
			// A loaded model we cannot read labels for makes the whole comparison
			// unreliable, so report that rather than quietly under-counting.
			return false, false
		}
		if _, known := set[key]; !known {
			return false, true
		}
	}
	return true, true
}

// modelSpecies returns ref's canonical species keys, building and memoizing them
// on first use. readable is false when the model's labels cannot be read (its
// instance is mid-reload); that answer is not memoized.
func (o *Orchestrator) modelSpecies(ref entryRef, primary *BirdNET, primaryID string) (set map[string]struct{}, readable bool) {
	set, generation := o.speciesSets.lookup(ref.id)
	if set != nil {
		return set, true
	}
	labels := labelsOf(ref, primary, primaryID)
	if len(labels) == 0 {
		return nil, false
	}
	// The species-name snapshot already memoizes the canonical key of every loaded
	// label, so building the set is mostly map hits rather than alias resolution.
	// A key is a pure function of its label, so a snapshot lagging a reload is safe.
	snapshot := o.SpeciesSnapshot()
	set = make(map[string]struct{}, len(labels))
	for _, label := range labels {
		set[snapshot.CanonicalKey(label)] = struct{}{}
	}
	o.speciesSets.store(ref.id, generation, set)
	return set, true
}

// labelsOf reads ref's current labels, or nil when its instance is gone. The primary
// is read through BirdNET.Labels, which takes the model's own lock, so entry.mu is
// neither needed nor safe to hold for it; every other entry is read under entry.mu.
// The caller must not hold o.mu: the reload, unload and delete paths take entry.mu
// and o.mu in the opposite order.
func labelsOf(ref entryRef, primary *BirdNET, primaryID string) []string {
	if primary != nil && ref.id == primaryID {
		return primary.Labels()
	}
	ref.entry.mu.Lock()
	defer ref.entry.mu.Unlock()
	if ref.entry.instance == nil {
		return nil
	}
	return ref.entry.instance.Labels()
}
