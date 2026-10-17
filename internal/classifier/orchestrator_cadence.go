package classifier

import (
	"maps"
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
)

// cadenceProbeRuns is the number of timed silent inferences whose median is the
// model's probed latency. When no warm-up just ran on the instance, one further
// untimed run precedes them to absorb lazy allocation.
const cadenceProbeRuns = 3

// SetCadencePlan publishes the analysis cadence plan. The plan is copied; a nil
// plan clears the published plan.
func (o *Orchestrator) SetCadencePlan(p *cadence.Plan) {
	if p == nil {
		o.cadencePlan.Store(nil)
		return
	}
	cp := *p
	cp.Models = slices.Clone(p.Models)
	cp.UnknownLatencyModels = slices.Clone(p.UnknownLatencyModels)
	o.cadencePlan.Store(&cp)
}

// CadencePlan returns the published analysis cadence plan, or nil when none has
// been published. Callers must treat the result as read-only.
func (o *Orchestrator) CadencePlan() *cadence.Plan {
	return o.cadencePlan.Load()
}

// EffectiveBaseOverlap returns the base overlap the analysis pipeline uses: the
// published plan's effective overlap, else the configured birdnet.overlap. The
// configured value is never modified by the cadence cap.
func (o *Orchestrator) EffectiveBaseOverlap() time.Duration {
	if p := o.cadencePlan.Load(); p != nil {
		return p.EffectiveBaseOverlap
	}
	return ConfiguredBaseOverlap(o.CurrentSettings())
}

// ProbedLatencies returns a copy of the per-model probed inference latency.
// Models without a successful probe are absent.
func (o *Orchestrator) ProbedLatencies() map[string]time.Duration {
	o.probeMu.Lock()
	defer o.probeMu.Unlock()
	out := make(map[string]time.Duration, len(o.probedLatency))
	maps.Copy(out, o.probedLatency)
	return out
}

// storeProbedLatency records a probe result, or removes the model's entry when
// the probe failed so the model is reported as unknown.
func (o *Orchestrator) storeProbedLatency(modelID string, latency time.Duration, ok bool) {
	o.probeMu.Lock()
	defer o.probeMu.Unlock()
	if o.probedLatency == nil {
		o.probedLatency = make(map[string]time.Duration)
	}
	if !ok {
		delete(o.probedLatency, modelID)
		return
	}
	o.probedLatency[modelID] = latency
}

// clearProbedLatencies drops every model's probe.
func (o *Orchestrator) clearProbedLatencies() {
	o.probeMu.Lock()
	defer o.probeMu.Unlock()
	clear(o.probedLatency)
}

// dropProbedLatency drops one model's probe.
func (o *Orchestrator) dropProbedLatency(modelID string) {
	o.probeMu.Lock()
	defer o.probeMu.Unlock()
	delete(o.probedLatency, modelID)
}

// probeLatency measures a model's inference latency on input (silentInput):
// cadenceProbeRuns timed runs, returning the median. Unless warmedUp reports that
// a warm-up inference just ran on this instance, one untimed run precedes them to
// absorb lazy allocation. Each run is executed through lockRun so the caller
// controls which locks are held for that one run only; lockRun returns false when
// it did not execute the run (for example the entry was swapped), which aborts
// the probe. A nil input, or a failed or timed-out run, aborts the probe and the
// model is reported as unknown (ok == false).
//
// The probe calls instance.Predict directly, never PredictModel, so it stays out
// of the global inference counters, like warmup.
func (o *Orchestrator) probeLatency(modelID string, instance ModelInstance, input [][]float32, warmedUp bool, lockRun func(run func()) bool) (median time.Duration, ok bool) {
	if input == nil {
		return 0, false
	}

	runOnce := func() (time.Duration, bool) {
		var elapsed time.Duration
		var runErr error
		ran := lockRun(func() {
			elapsed, runErr = predictOnce(modelID, instance, input, "latency probe inference failed (non-fatal)")
		})
		return elapsed, ran && runErr == nil
	}

	if !warmedUp {
		if _, good := runOnce(); !good {
			return 0, false
		}
	}
	samples := make([]time.Duration, 0, cadenceProbeRuns)
	for range cadenceProbeRuns {
		d, good := runOnce()
		if !good {
			return 0, false
		}
		samples = append(samples, d)
	}
	slices.Sort(samples)
	return samples[len(samples)/2], true
}
