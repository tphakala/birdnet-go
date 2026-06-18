package classifier

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// warmupTimeout bounds the best-effort warm-up inference run during model load.
// It is intentionally short: a legitimate first inference completes well under
// 5 s even on a Raspberry Pi 5, and the warm-up holds o.mu (callers of
// PredictModel block on o.mu.RLock during a dynamic load). On timeout the
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
