package classifier

import (
	"context"
	"maps"
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
// host-RAM delta attributable to this model. input is the instance's silent
// input (silentInput), shared with the latency probe that follows. The warm-up
// calls instance.Predict directly (never o.PredictModel) to avoid the re-entrant
// o.mu lock and to keep the warm-up out of the global inference counters.
// Negative deltas (OS page reclamation, measurement noise) are clamped to zero.
// RSS is host-RAM only and approximate. It reports whether the warm-up inference
// succeeded, so the latency probe knows whether lazy allocation already happened.
func (o *Orchestrator) warmupAndRecordRSS(modelID string, before uint64, instance ModelInstance, input [][]float32) (warmedUp bool) {
	warmedUp = o.warmup(modelID, instance, input)

	after, err := sysinfo.CurrentProcessRSS()
	if err != nil || before == 0 {
		return warmedUp // RSS unavailable: leave the model out of modelRSS (endpoint shows n/a)
	}
	delta := max(int64(after)-int64(before), 0)
	o.rssMu.Lock()
	o.modelRSS[modelID] = delta
	o.rssMu.Unlock()
	return warmedUp
}

// warmup runs a single silent inference and reports whether it succeeded.
// Failures are non-fatal and logged at debug level; the model still loads. A nil
// input (a spec without samples) skips the warm-up and reports false.
func (o *Orchestrator) warmup(modelID string, instance ModelInstance, input [][]float32) bool {
	if input == nil {
		return false
	}
	_, err := predictOnce(modelID, instance, input, "warm-up inference failed (non-fatal)")
	return err == nil
}

// silentInput returns one clip of silence sized from the model spec, or nil when
// the spec has no samples. The warm-up and the latency probe share it.
func silentInput(spec ModelSpec) [][]float32 {
	n := int(float64(spec.SampleRate) * spec.ClipLength.Seconds())
	if n <= 0 {
		return nil
	}
	return [][]float32{make([]float32, n)}
}

// predictOnce runs one inference on input, bounded by warmupTimeout, and returns
// how long it took. A failure is logged at debug level with failMsg and returned.
// It calls instance.Predict directly, never PredictModel, so it stays out of the
// global inference counters.
func predictOnce(modelID string, instance ModelInstance, input [][]float32, failMsg string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), warmupTimeout)
	defer cancel()
	start := time.Now()
	_, err := instance.Predict(ctx, input)
	elapsed := time.Since(start)
	if err != nil {
		GetLogger().Debug(failMsg,
			logger.String("model", modelID),
			logger.Error(err))
		return 0, err
	}
	return elapsed, nil
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
// directly), so the warm-up does not pollute inference stats. After the warm-up
// it probes the model's latency and stores the result only while the entry still
// serves the probed instance, checked and stored under entry.mu.
func (o *Orchestrator) warmupRegisteredModel(modelID string, before uint64) {
	o.mu.RLock()
	entry, ok := o.models[modelID]
	o.mu.RUnlock()
	if !ok {
		return
	}

	var input [][]float32
	var warmedUp bool
	instance := func() ModelInstance {
		o.inferenceMu.Lock()
		defer o.inferenceMu.Unlock()

		entry.mu.Lock()
		defer entry.mu.Unlock()
		if entry.instance == nil {
			return nil
		}

		input = silentInput(entry.instance.Spec())
		warmedUp = o.warmupAndRecordRSS(modelID, before, entry.instance, input)
		return entry.instance
	}()
	if instance == nil {
		return
	}

	// Probe outside the warm-up's locked section: probeLatency re-acquires the locks
	// for each run, so live inference interleaves between runs. Each run re-checks
	// that the entry still serves the same instance. When the warm-up succeeded on
	// this instance the probe needs no untimed run of its own; when it failed, lazy
	// allocation may not have happened yet, so the probe keeps its untimed run.
	latency, ok := o.probeLatency(modelID, instance, input, warmedUp, func(run func()) bool {
		o.inferenceMu.Lock()
		defer o.inferenceMu.Unlock()
		entry.mu.Lock()
		defer entry.mu.Unlock()
		if entry.instance != instance {
			return false
		}
		run()
		return true
	})
	// Store under entry.mu, as reloadEntry does with its swap, so a reload or
	// unload cannot land between the serving check and the store and have its
	// newer state overwritten by this instance's latency.
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.instance == instance {
		o.storeProbedLatency(modelID, latency, ok)
	}
}

// ModelRSS returns a copy of the per-model host-RSS deltas (bytes) and the
// process RSS baseline captured before the first model load. Safe for concurrent
// use; the returned map is a copy.
func (o *Orchestrator) ModelRSS() (perModel map[string]int64, runtimeBaseline int64) {
	o.rssMu.Lock()
	defer o.rssMu.Unlock()
	out := make(map[string]int64, len(o.modelRSS))
	maps.Copy(out, o.modelRSS)
	return out, o.runtimeBaseline
}
