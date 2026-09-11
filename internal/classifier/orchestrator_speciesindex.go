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

// rebuildSpeciesIndex republishes the species-name snapshot from the union of
// every loaded model's labels (AllLabels) and the current BirdNET.Locale.
//
// It MUST be called with no orchestrator lock held: AllLabels takes o.mu.RLock and
// each entry.mu in turn, and Rebuild takes the service's leaf buildMu. rebuildMu
// (taken here, and only here) keeps the union snapshot and its publication atomic
// across concurrent triggers, so the last published snapshot always reflects the
// last topology change rather than a stale union that blocked behind a newer one.
// Nil-safe on both o and o.names (bare-struct tests and file-analysis paths that
// never build the index).
func (o *Orchestrator) rebuildSpeciesIndex() {
	if o == nil || o.names == nil {
		return
	}
	o.rebuildMu.Lock()
	defer o.rebuildMu.Unlock()
	labels := o.AllLabels()
	o.names.Rebuild(labels, o.CurrentSettings().BirdNET.Locale)
}
