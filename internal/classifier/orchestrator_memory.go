package classifier

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// warmupTimeout bounds the best-effort warm-up inference run during model load.
// It is intentionally short: a legitimate first inference completes well under
// 5 s even on a Raspberry Pi 5. The warm-up no longer runs under o.mu; the
// initial-load paths defer it and run it via the serialized inference path
// (warmupRegisteredModel takes inferenceMu, not o.mu), so PredictModel/ModelInfos
// callers are not blocked on o.mu during a dynamic load. The
// bound caps how long a wedged warm-up can hold inferenceMu. On timeout the
// warm-up aborts gracefully and RSS just undercounts for that model.
const warmupTimeout = 5 * time.Second

// captureRSSBefore reads the current process RSS to use as the "before" sample
// for a model load. On the first call it also records the process-wide runtime
// baseline (Go runtime + app, before any model arena) so the first-loaded model
// does not visually absorb the entire shared runtime cost. Returns 0 when RSS is
// unavailable on the platform; callers treat 0 as "do not record a delta".
func (o *Orchestrator) captureRSSBefore() uint64 {
	rss, err := sysinfo.CurrentProcessRSS()
	if err != nil {
		return 0
	}
	o.rssMu.Lock()
	if !o.baselineCaptured {
		o.runtimeBaseline = int64(rss)
		o.baselineCaptured = true
	}
	o.rssMu.Unlock()
	return rss
}

// warmupAndRecordRSS runs a best-effort warm-up inference on the freshly built
// instance to force lazy allocation, then records RSS_after - RSS_before as the
// host-RAM delta attributable to this model. The warm-up calls instance.Predict
// directly (never o.PredictModel) to avoid the re-entrant o.mu lock and to keep
// the warm-up out of the global inference counters. Negative deltas (OS page
// reclamation, measurement noise) are clamped to zero. RSS is host-RAM only and
// approximate.
func (o *Orchestrator) warmupAndRecordRSS(modelID string, before uint64, instance ModelInstance) {
	o.warmup(modelID, instance)

	after, err := sysinfo.CurrentProcessRSS()
	if err != nil || before == 0 {
		return // RSS unavailable: leave the model out of modelRSS (endpoint shows n/a)
	}
	delta := int64(after) - int64(before)
	if delta < 0 {
		delta = 0
	}
	o.rssMu.Lock()
	o.modelRSS[modelID] = delta
	o.rssMu.Unlock()
}

// warmup runs a single silent inference sized from the model spec. Failures are
// non-fatal and logged at debug level; the model still loads.
func (o *Orchestrator) warmup(modelID string, instance ModelInstance) {
	spec := instance.Spec()
	n := int(float64(spec.SampleRate) * spec.ClipLength.Seconds())
	if n <= 0 {
		return
	}
	dummy := [][]float32{make([]float32, n)}
	ctx, cancel := context.WithTimeout(context.Background(), warmupTimeout)
	defer cancel()
	if _, err := instance.Predict(ctx, dummy); err != nil {
		GetLogger().Debug("warm-up inference failed (non-fatal)",
			logger.String("model", modelID),
			logger.Error(err))
	}
}

// deferWarmup queues a freshly-registered model for warm-up after o.mu is
// released, instead of warming it up inline. Model loaders call this while they
// hold o.mu (write lock); running the warm-up inference here would block every
// PredictModel/ModelInfos caller on o.mu for the inference duration. The caller
// drains the queue via runPendingWarmups once o.mu is free.
// Must be called with o.mu held.
func (o *Orchestrator) deferWarmup(modelID string, before uint64) {
	o.pendingWarmups = append(o.pendingWarmups, pendingWarmup{modelID: modelID, before: before})
}

// runPendingWarmups drains the deferred warm-up queue, running each warm-up via
// the serialized inference path (warmupRegisteredModel) so it never holds o.mu.
// The queue is snapshotted and cleared under o.mu so concurrent loader appends
// cannot race the slice header and an entry is never warmed twice. Safe to call
// with o.mu NOT held (it must not be: it acquires o.mu itself).
func (o *Orchestrator) runPendingWarmups() {
	o.mu.Lock()
	pending := o.pendingWarmups
	o.pendingWarmups = nil
	o.mu.Unlock()

	for _, w := range pending {
		o.warmupRegisteredModel(w.modelID, w.before)
	}
}

// warmupRegisteredModel runs the deferred warm-up + RSS measurement for a model
// already published in o.models. It mirrors PredictModel's lock protocol
// (o.mu.RLock to fetch the entry, release, then inferenceMu, then entry.mu) so
// it behaves like a normal first inference: it never holds o.mu and serializes
// with live inference via inferenceMu. The warm-up is skipped if the entry was
// unloaded (absent, or instance == nil) before it ran, so a teardown that races
// the load leaves no stale modelRSS entry. Unlike PredictModel it does not record
// into globalInferenceCounters (warmupAndRecordRSS calls instance.Predict
// directly), so the warm-up does not pollute inference stats.
func (o *Orchestrator) warmupRegisteredModel(modelID string, before uint64) {
	o.mu.RLock()
	entry, ok := o.models[modelID]
	o.mu.RUnlock()
	if !ok {
		return
	}

	o.inferenceMu.Lock()
	defer o.inferenceMu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.instance == nil {
		return
	}

	o.warmupAndRecordRSS(modelID, before, entry.instance)
}

// ModelRSS returns a copy of the per-model host-RSS deltas (bytes) and the
// process RSS baseline captured before the first model load. Safe for concurrent
// use; the returned map is a copy.
func (o *Orchestrator) ModelRSS() (perModel map[string]int64, runtimeBaseline int64) {
	o.rssMu.Lock()
	defer o.rssMu.Unlock()
	out := make(map[string]int64, len(o.modelRSS))
	for k, v := range o.modelRSS {
		out[k] = v
	}
	return out, o.runtimeBaseline
}
