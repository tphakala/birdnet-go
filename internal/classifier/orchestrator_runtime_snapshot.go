package classifier

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// ModelRuntimeSnapshot is what a loaded model was built as, published when the
// model loads or reloads so a diagnostic reader never has to wait on inference.
type ModelRuntimeSnapshot struct {
	// RegistryID is the model's registry ID.
	RegistryID string
	// Backend, Device and Precision are the instance's RuntimeInfo triplet.
	Backend   string
	Device    string
	Precision string
	// ModelPath is the raw ResolvedModelPath of the instance; empty for the
	// built-in model. It can hold a home directory, so callers ship only its
	// basename.
	ModelPath string
	// NumSpecies is the model's label count.
	NumSpecies int
	// ConfiguredThreads is the thread count the model was built with as
	// configured, not a measured runtime value; 0 means the backend default.
	ConfiguredThreads int
	// Generation counts the reloads of this model since it was loaded.
	Generation uint64
	// LoadedAt is when this instance was published.
	LoadedAt time.Time
}

// RangeFilterRuntimeSummary is the range-filter state a diagnostic reader can
// take without waiting on inference.
type RangeFilterRuntimeSummary struct {
	// Available is false when the orchestrator has no range-filter service.
	Available bool
	// Active is true when a range-filter backend is loaded.
	Active bool
	// FellBack is true when the backend is a fallback for the configured one.
	FellBack bool
	// Backend is the loaded backend kind, "none" when there is none.
	Backend string
	// MappedSpecies is the number of classifier species with a geomodel match.
	MappedSpecies int
	// Participants lists the registry IDs the active backend was built over.
	Participants []string
	// StockGeomodelPresent is true when the stock geomodel files are in the
	// models directory.
	StockGeomodelPresent bool
}

// publishedModel pairs a published snapshot with the entry it describes, so a
// drop removes the record only while it still belongs to that entry.
type publishedModel struct {
	entry *modelEntry
	snap  ModelRuntimeSnapshot
}

// configuredThreadsFor returns the thread count a model is built with as
// configured. BirdNET v2.4 ignores the shared budget and reads
// settings.BirdNET.Threads inside its constructor; every other model is handed
// the budget.
func configuredThreadsFor(registryID string, settings *conf.Settings, budget int) int {
	if registryID == RegistryIDBirdNETV24 {
		return settings.BirdNET.Threads
	}
	return budget
}

// newModelRuntimeSnapshot describes inst. The caller guarantees inst is not
// serving yet, or is otherwise safe to ask for its label count (NumSpecies can
// take the model's own lock).
func newModelRuntimeSnapshot(registryID string, inst ModelInstance, threads int) ModelRuntimeSnapshot {
	device, backend, precision := inst.RuntimeInfo()
	return ModelRuntimeSnapshot{
		RegistryID:        registryID,
		Backend:           backend,
		Device:            device,
		Precision:         precision,
		ModelPath:         inst.ResolvedModelPath(),
		NumSpecies:        inst.NumSpecies(),
		ConfiguredThreads: threads,
		LoadedAt:          time.Now(),
	}
}

// updateRuntimeSnaps applies fn to a copy of the published map and stores the
// copy only when fn returns true. runtimeSnapMu is a leaf lock: nothing is
// acquired while it is held, and readers never take it.
func (o *Orchestrator) updateRuntimeSnaps(fn func(m map[string]*publishedModel) bool) {
	o.runtimeSnapMu.Lock()
	defer o.runtimeSnapMu.Unlock()
	var next map[string]*publishedModel
	if cur := o.runtimeSnaps.Load(); cur != nil {
		next = maps.Clone(*cur)
	}
	if next == nil {
		next = make(map[string]*publishedModel)
	}
	if fn(next) {
		o.runtimeSnaps.Store(&next)
	}
}

// publishModelRuntime stores snap as the record for registryID, owned by entry.
func (o *Orchestrator) publishModelRuntime(entry *modelEntry, snap *ModelRuntimeSnapshot) {
	o.updateRuntimeSnaps(func(m map[string]*publishedModel) bool {
		m[snap.RegistryID] = &publishedModel{entry: entry, snap: *snap}
		return true
	})
}

// publishReloadedModelRuntime stores snap as the record for the reloaded model
// unless a different entry owns it. An unload followed by a load of the same ID
// publishes the new entry's record while the old entry may still be swapping in
// a reload; that reload must not replace the newer record, and the old entry's
// teardown would then drop it. A model with no record is published.
func (o *Orchestrator) publishReloadedModelRuntime(entry *modelEntry, snap *ModelRuntimeSnapshot) {
	o.updateRuntimeSnaps(func(m map[string]*publishedModel) bool {
		if pm, ok := m[snap.RegistryID]; ok && pm.entry != entry {
			return false
		}
		m[snap.RegistryID] = &publishedModel{entry: entry, snap: *snap}
		return true
	})
}

// dropModelRuntime removes the record for registryID only if it still belongs to
// entry. An unload followed by a load of the same ID can leave the old entry's
// teardown running after the new record was published; an ID-keyed drop would
// delete the new one.
func (o *Orchestrator) dropModelRuntime(registryID string, entry *modelEntry) {
	o.updateRuntimeSnaps(func(m map[string]*publishedModel) bool {
		if pm, ok := m[registryID]; !ok || pm.entry != entry {
			return false
		}
		delete(m, registryID)
		return true
	})
}

// publishLoadedModelLocked publishes the record of the model a loader just
// registered. The caller holds o.mu for writing, which is what makes reading
// entry.instance without entry.mu safe: the new entry is reachable only through
// o.models. Taking the model's own lock for NumSpecies is legal after o.mu.
func (o *Orchestrator) publishLoadedModelLocked(registryID string, settings *conf.Settings, budget int) {
	entry := o.models[registryID]
	if entry == nil || entry.instance == nil {
		return
	}
	snap := newModelRuntimeSnapshot(registryID, entry.instance, configuredThreadsFor(registryID, settings, budget))
	o.publishModelRuntime(entry, &snap)
}

// ModelRuntimeSnapshots returns the record of every loaded model, sorted by
// registry ID. It is an atomic load and never waits on inference, a model's
// lock, or a model build.
func (o *Orchestrator) ModelRuntimeSnapshots() []ModelRuntimeSnapshot {
	cur := o.runtimeSnaps.Load()
	if cur == nil {
		return []ModelRuntimeSnapshot{}
	}
	out := make([]ModelRuntimeSnapshot, 0, len(*cur))
	for _, pm := range *cur {
		out = append(out, pm.snap)
	}
	slices.SortFunc(out, func(a, b ModelRuntimeSnapshot) int {
		return strings.Compare(a.RegistryID, b.RegistryID)
	})
	return out
}

// storeModelsDir records the models directory in use for lock-free readers.
func (o *Orchestrator) storeModelsDir(dir string) {
	o.modelsDirSnap.Store(&dir)
}

// ModelsDirSnapshot returns the models directory the orchestrator uses, or ""
// when none is set. It is an atomic load.
func (o *Orchestrator) ModelsDirSnapshot() string {
	if dir := o.modelsDirSnap.Load(); dir != nil {
		return *dir
	}
	return ""
}

// RangeFilterRuntimeSummary summarizes the range filter from one atomic state
// snapshot. Unlike RangeFilterStatus it never takes a model lock, so it does not
// wait behind inference, and it never touches the backend itself, which is not
// goroutine-safe.
func (o *Orchestrator) RangeFilterRuntimeSummary() RangeFilterRuntimeSummary {
	rfs := o.rangeFilter
	if rfs == nil {
		return RangeFilterRuntimeSummary{}
	}
	state := rfs.loadState()
	kind := state.kind
	if kind == "" {
		kind = rfKindNone
	}
	summary := RangeFilterRuntimeSummary{
		Available:            true,
		Active:               state.backend != nil,
		FellBack:             state.fellBack,
		Backend:              string(kind),
		Participants:         make([]string, 0, len(state.participants)),
		StockGeomodelPresent: geomodelFilesPresent(o.ModelsDirSnapshot()),
	}
	if mrf, ok := state.backend.(*mappedRangeFilter); ok {
		summary.MappedSpecies = mrf.mappedCount
	}
	for i := range state.participants {
		summary.Participants = append(summary.Participants, state.participants[i].id)
	}
	return summary
}
