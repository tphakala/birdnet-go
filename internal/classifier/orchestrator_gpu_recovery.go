// orchestrator_gpu_recovery.go moves the models that ran on the OpenVINO GPU off it
// after the GPU fault latch trips, when the selection is auto (backend auto and
// OpenVINO device auto). An explicit selection never falls back, so it is never
// recovered: its GPU models stop and the GPU notice says so.
package classifier

import (
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// gpuRecoveryState is the recovery pass bookkeeping.
//
// mu is a leaf lock held only to read or write the flags, so it is safe on an
// inference goroutine (the fault observer). callbackMu guards callback and moved and
// is held while the callback runs, so clearing the callback waits for a call in
// flight; it is taken with no orchestrator lock. Passes are serialized by running,
// not by a mutex.
type gpuRecoveryState struct {
	mu sync.Mutex
	// due is set by a trigger and consumed by a pass. running is set while a pass
	// goroutine (or the inline post-publish pass) is live. stopped is set by Delete.
	due, running, stopped bool
	wg                    sync.WaitGroup
	// failedMoves holds the IDs of models a pass tried and failed to move off the
	// GPU, guarded by mu. The partial notice names only these: a model still on the
	// GPU that no pass has tried yet means recovery is not done, not that it failed.
	failedMoves map[string]struct{}

	callbackMu sync.Mutex
	callback   func()
	// moved is set when a pass moved a model and no callback was registered to tell.
	moved bool
}

// requestGPURecovery marks a recovery due when the GPU fault latch is tripped and the
// selection is auto, and starts a pass goroutine when the orchestrator is published
// and no pass is running. An explicit selection clears the mark instead. It takes
// only the leaf lock and never blocks, so it is safe on the inference hot path.
func (o *Orchestrator) requestGPURecovery() {
	if o.claimGPURecovery() {
		go func() {
			defer o.gpuRecovery.wg.Done()
			o.runGPURecovery()
		}()
	}
}

// claimGPURecovery marks a recovery due and reports whether the caller must now run
// the pass: the latch is tripped, the selection is auto, the orchestrator is
// published and no pass is running. A true result has set running and counted the
// pass in wg (under mu, so a Delete that set stopped before waiting never misses
// it); the caller runs runGPURecovery and calls wg.Done. The selection is read under
// mu, and an explicit one clears the due mark instead of setting it. Either way the
// moves earlier passes failed are forgotten, unless recovery was stopped.
func (o *Orchestrator) claimGPURecovery() bool {
	faulted, _ := ovGPUFault()
	if !faulted {
		return false
	}
	st := &o.gpuRecovery
	st.mu.Lock()
	defer st.mu.Unlock()
	// Every request, and an explicit period, starts from "untried": a failure recorded
	// by an earlier pass says nothing about the retry this trigger asks for. Once
	// stopped no pass runs again, so the record the notice shows is kept.
	if !st.stopped {
		st.failedMoves = nil
	}
	// Read under mu: a stale explicit read must not clear a due mark that a trigger
	// after a switch back to auto has just set.
	if o.gpuSelectionExplicit() {
		st.due = false
		return false
	}
	if st.stopped {
		return false
	}
	st.due = true
	if st.running || !o.published.Load() {
		return false
	}
	st.running = true
	st.wg.Add(1)
	return true
}

// gpuSelectionExplicit reports whether the current settings name OpenVINO
// explicitly. Lock-free. Missing settings (only a bare test orchestrator has none)
// count as auto.
func (o *Orchestrator) gpuSelectionExplicit() bool {
	s := o.currentSettings()
	if s == nil {
		return false
	}
	return openVINOSelectionExplicit(s.BirdNET.Backend, s.BirdNET.OpenVINODevice)
}

// recoveryPending reports whether a recovery pass is due or running, so the GPU
// notice waits for its outcome.
func (o *Orchestrator) recoveryPending() bool {
	st := &o.gpuRecovery
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.due || st.running
}

func (o *Orchestrator) gpuRecoveryStopped() bool {
	st := &o.gpuRecovery
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.stopped
}

// runGPURecovery runs passes while one is due, then clears running and kicks the
// notice reconcile, which now sees no pass pending.
func (o *Orchestrator) runGPURecovery() {
	st := &o.gpuRecovery
	for {
		st.mu.Lock()
		if st.stopped || !st.due {
			st.running = false
			st.mu.Unlock()
			o.kickInferenceHealthSync()
			return
		}
		st.due = false
		st.mu.Unlock()
		o.recoverModelsFromFailedGPU()
	}
}

// runGPURecoveryAfterPublish runs a due pass inline right after NewOrchestrator
// published the orchestrator: a trip during construction only marked recovery due,
// and a latch that tripped before this orchestrator existed is read here. The
// callback is not registered yet; the pipeline plans its first cadence afterwards.
func (o *Orchestrator) runGPURecoveryAfterPublish() {
	if o.claimGPURecovery() {
		defer o.gpuRecovery.wg.Done()
		o.runGPURecovery()
	}
}

// ReconcileGPUFaultAfterSettingsReload re-evaluates the GPU fault after a settings
// reload: in auto mode it moves any model still on the failed GPU (a selection
// change can leave one), and the GPU notice is reconciled either way. A pass starts
// only when a model is on the failed GPU, and runs on its own goroutine. The control
// monitor calls it at the end of a BirdNET reload.
func (o *Orchestrator) ReconcileGPUFaultAfterSettingsReload() {
	// A pass with nothing on the GPU would only hide the notice while it runs, so
	// the notice would be deleted and raised again on an unrelated settings change.
	if len(o.modelsOnFailedGPU()) > 0 {
		o.requestGPURecovery()
	}
	o.kickInferenceHealthSync()
}

// recoverModelsFromFailedGPU is one pass: it rebuilds, in registry ID order, every
// loaded model whose live instance still runs on the failed OpenVINO GPU, and stops
// when Delete began or the selection became explicit. It records which models it
// failed to move, which the partial notice names. The planner skips the GPU now, so
// on amd64 the rebuilt models run on ONNX Runtime. A rebuild
// that fails keeps the old instance; the next user-driven trigger retries it. It
// holds no orchestrator lock across a rebuild.
func (o *Orchestrator) recoverModelsFromFailedGPU() {
	if o.gpuSelectionExplicit() {
		return
	}
	log := GetLogger()
	movedAny := false
	for _, id := range o.modelsOnFailedGPU() {
		if o.gpuRecoveryStopped() || o.gpuSelectionExplicit() {
			break
		}
		swapped, err := o.rebuildFromCurrentSettings(id, true)
		o.setMoveFailed(id, err != nil)
		if err != nil {
			log.Warn("model could not be moved off the failed OpenVINO GPU; it detects nothing until a settings change or restart",
				logger.String("registry_id", id), logger.Error(err))
			continue
		}
		if !swapped {
			continue
		}
		movedAny = true
		device, backend, _ := o.GetModelRuntimeInfo(id)
		log.Info("model moved off the failed OpenVINO GPU",
			logger.String("registry_id", id), logger.String("backend", backend), logger.String("device", device))
	}
	if movedAny {
		o.notifyGPURecoveryMoved()
	}
}

// setMoveFailed records whether the last pass attempt to move id failed; a model
// that moved, or was no longer on the GPU, is cleared.
func (o *Orchestrator) setMoveFailed(id string, failed bool) {
	st := &o.gpuRecovery
	st.mu.Lock()
	defer st.mu.Unlock()
	if !failed {
		delete(st.failedMoves, id)
		return
	}
	if st.failedMoves == nil {
		st.failedMoves = make(map[string]struct{})
	}
	st.failedMoves[id] = struct{}{}
}

// moveFailed reports whether a pass tried and failed to move id.
func (o *Orchestrator) moveFailed(id string) bool {
	st := &o.gpuRecovery
	st.mu.Lock()
	defer st.mu.Unlock()
	_, failed := st.failedMoves[id]
	return failed
}

// loadedModelIDsSorted returns the loaded model IDs in order.
func (o *Orchestrator) loadedModelIDsSorted() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return slices.Sorted(maps.Keys(o.models))
}

// modelsOnFailedGPU returns the IDs, in order, of the loaded models whose live
// instance still runs on the failed OpenVINO GPU.
func (o *Orchestrator) modelsOnFailedGPU() []string {
	var ids []string
	for _, id := range o.loadedModelIDsSorted() {
		if builtOnFailedOpenVINOGPU(o.instanceFor(id)) {
			ids = append(ids, id)
		}
	}
	return ids
}

// gpuModelNames renders model IDs as the names the notice shows.
func gpuModelNames(ids []string) string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		name := id
		if info, known := ModelRegistry[id]; known && info.Name != "" {
			name = info.Name
		}
		names = append(names, name)
	}
	return strings.Join(names, refusalModelNameSep)
}

// notifyGPURecoveryMoved tells the registered callback that a pass moved a model, or
// remembers it for the callback registered later. Nothing is told once stopped.
func (o *Orchestrator) notifyGPURecoveryMoved() {
	st := &o.gpuRecovery
	st.callbackMu.Lock()
	defer st.callbackMu.Unlock()
	if o.gpuRecoveryStopped() {
		return
	}
	if st.callback == nil {
		st.moved = true
		return
	}
	st.callback()
}

// SetGPURecoveryCallback registers cb, called after a recovery pass moved at least
// one model so the pipeline can re-plan its cadence and the UI refresh. It runs
// holding no orchestrator lock, on the pass goroutine (a replay runs on the registering goroutine), and must not call this
// setter. Registering waits for a call in flight, and replays once a move that
// happened while no callback was registered, unless Delete already stopped recovery.
// A nil cb clears it, also waiting.
func (o *Orchestrator) SetGPURecoveryCallback(cb func()) {
	st := &o.gpuRecovery
	st.callbackMu.Lock()
	defer st.callbackMu.Unlock()
	st.callback = cb
	if cb != nil && st.moved && !o.gpuRecoveryStopped() {
		st.moved = false
		cb()
	}
}

// stopGPURecovery stops new passes; a pass already running finishes on its own.
// Delete calls it, then waits on gpuRecovery.wg after closing the models.
func (o *Orchestrator) stopGPURecovery() {
	st := &o.gpuRecovery
	st.mu.Lock()
	st.stopped = true
	st.mu.Unlock()
}
