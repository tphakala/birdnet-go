// orchestrator_speciesindex.go holds the orchestrator's ownership of the single
// process-wide species-name index (internal/speciesindex). The orchestrator is
// the only writer; the datastore and the api/v2 facade hold pointers to the same
// service and read it lock-free via Snapshot(). See the model de-privilege epic,
// Phase 2a.
package classifier

import "github.com/tphakala/birdnet-go/internal/speciesindex"

// SpeciesIndex returns the orchestrator-owned species-name index. Consumers read
// Snapshot() lock-free; only the orchestrator rebuilds it. It is never nil on an
// orchestrator built by NewOrchestrator; a bare struct returns nil.
func (o *Orchestrator) SpeciesIndex() *speciesindex.Service {
	if o == nil {
		return nil
	}
	return o.names
}

// SpeciesSnapshot returns the current species-name snapshot, i.e.
// SpeciesIndex().Snapshot(). It returns speciesindex.Empty() on a nil receiver or
// a bare struct so callers can index its maps without a nil guard.
func (o *Orchestrator) SpeciesSnapshot() *speciesindex.Snapshot {
	if o == nil || o.names == nil {
		return speciesindex.Empty()
	}
	return o.names.Snapshot()
}

// rebuildSpeciesIndex republishes the species-name index snapshot from the union of
// every loaded model's labels (AllLabels) and the last range-filter inclusion list
// the orchestrator was given (o.includedSpecies, recorded by RebuildNameResolver).
// This is the model-topology trigger (load, unload, reload): it rebuilds ONLY the
// index, from the OpenFauna resolver's CURRENT localized names, and deliberately does
// NOT rebuild the resolver working set. openfauna.Rebuild streams and decompresses
// the embedded dataset, so running it on every model toggle would be costly on a
// constrained host (bng-low, 512 MB); the resolver working set is refreshed only on
// the range-filter path (RebuildNameResolver). Building the index from the same union
// the resolver was last built from keeps the two consistent: every species in the
// resolver working set is in the index, so an inclusion-list species can never be in
// the resolver yet missing from the index. A newly loaded model's species enter the
// index immediately (searchable, and shown localized via the live resolver's
// on-demand Resolve on the display path); their reverse-search maps pick up localized
// names at the next range-filter rebuild.
//
// It MUST be called with no orchestrator lock held: AllLabels takes o.mu.RLock and
// each entry.mu in turn, and the index Rebuild takes a leaf lock. rebuildMu (taken
// here and in RebuildNameResolver, nowhere else) keeps the working-set read and the
// publication atomic across concurrent triggers, so the last publication reflects the
// last topology change rather than a stale union that blocked behind a newer one.
// Nil-safe on a nil receiver and a bare struct (no index).
func (o *Orchestrator) rebuildSpeciesIndex() {
	if o == nil || o.names == nil {
		return
	}
	o.rebuildMu.Lock()
	defer o.rebuildMu.Unlock()
	o.rebuildSpeciesIndexLocked()
}

// rebuildSpeciesIndexLocked rebuilds the index snapshot from the union of AllLabels
// and the stored inclusion list, using the resolver's current localized names. The
// caller MUST hold o.rebuildMu; o.includedSpecies is read here under that lock.
// RebuildNameResolver reuses it after refreshing the resolver, so both paths build
// the index from the identical working set.
func (o *Orchestrator) rebuildSpeciesIndexLocked() {
	if o.names == nil {
		return
	}
	o.names.Rebuild(unionLabels(o.AllLabels(), o.includedSpecies), o.CurrentSettings().BirdNET.Locale)
}
