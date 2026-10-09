package classifier

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// scriptedInstance sleeps a scripted duration per Predict call and optionally
// fails on a chosen call. Embeds fakeInstance for the rest of the interface.
type scriptedInstance struct {
	fakeInstance
	durations []time.Duration
	failOn    int // 1-based call index that fails; 0 = never
	calls     int
	onCall    func(call int) // runs inside Predict, under whatever locks the caller holds
}

func (s *scriptedInstance) Predict(ctx context.Context, _ [][]float32) ([]datastore.Results, error) {
	s.calls++
	idx := s.calls
	if s.onCall != nil {
		s.onCall(idx)
	}
	if s.failOn == idx {
		return nil, errors.NewStd("scripted failure")
	}
	d := time.Duration(0)
	if idx-1 < len(s.durations) {
		d = s.durations[idx-1]
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return nil, nil
}

func newScripted(durations ...time.Duration) *scriptedInstance {
	return &scriptedInstance{
		id:         "Probe_Model",
		sampleRate: 48000,
		clip:       3 * time.Second,
		durations:  durations,
	}
}

func TestEffectiveBaseOverlap_FallsBackToConfigured(t *testing.T) {
	t.Parallel()
	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.4
	o := &Orchestrator{Settings: s}
	assert.Equal(t, 2400*time.Millisecond, o.EffectiveBaseOverlap())
	assert.Nil(t, o.CadencePlan())
}

func TestEffectiveBaseOverlap_PlanWinsOverLaterSettingsChange(t *testing.T) {
	t.Parallel()
	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.8
	o := &Orchestrator{Settings: s}
	plan := cadence.Plan{ConfiguredBaseOverlap: 2800 * time.Millisecond, EffectiveBaseOverlap: 2000 * time.Millisecond}
	o.SetCadencePlan(&plan)

	// A later settings change must not move the buffers' step until a new plan is published.
	changed := conf.CloneSettings(s)
	changed.BirdNET.Overlap = 1.0
	o.updateSettings(changed)

	assert.Equal(t, 2000*time.Millisecond, o.EffectiveBaseOverlap())
	plan.EffectiveBaseOverlap = 1 // the stored plan is a copy
	assert.Equal(t, 2000*time.Millisecond, o.CadencePlan().EffectiveBaseOverlap)
}

func TestProbeLatency_MedianOfRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		o := &Orchestrator{modelRSS: map[string]int64{"Probe_Model": 7}}
		// First run is the untimed one (4 s would dominate if it were counted). The
		// timed runs include one slow outlier, so the median (200 ms) differs from
		// the mean (500 ms).
		inst := newScripted(4*time.Second, 1200*time.Millisecond, 100*time.Millisecond, 200*time.Millisecond)
		got, ok := o.probeLatency("Probe_Model", inst, silentInput(inst.Spec()), false, func(run func()) bool { run(); return true })
		require.True(t, ok)
		assert.Equal(t, 200*time.Millisecond, got, "median of the timed runs, not their mean")
		assert.Equal(t, 1+cadenceProbeRuns, inst.calls)
		rss, _ := o.ModelRSS()
		assert.Equal(t, map[string]int64{"Probe_Model": 7}, rss, "probe must not touch RSS accounting")

		// The same runs in another order put the median first, so neither the last
		// run nor the unsorted middle run can pass for it.
		reordered := newScripted(4*time.Second, 200*time.Millisecond, 1200*time.Millisecond, 100*time.Millisecond)
		got, ok = o.probeLatency("Probe_Model", reordered, silentInput(reordered.Spec()), false, func(run func()) bool { run(); return true })
		require.True(t, ok)
		assert.Equal(t, 200*time.Millisecond, got, "median regardless of run order")
	})
}

// TestProbeLatency_SkipsUntimedRunAfterWarmup pins that a probe following a
// warm-up on the same instance times every run it makes.
func TestProbeLatency_SkipsUntimedRunAfterWarmup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		o := &Orchestrator{}
		inst := newScripted(300*time.Millisecond, 100*time.Millisecond, 200*time.Millisecond)
		got, ok := o.probeLatency("Probe_Model", inst, silentInput(inst.Spec()), true, func(run func()) bool { run(); return true })
		require.True(t, ok)
		assert.Equal(t, 200*time.Millisecond, got)
		assert.Equal(t, cadenceProbeRuns, inst.calls)
	})
}

func TestProbeLatency_FailedRunMarksUnknown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		o := &Orchestrator{}
		inst := newScripted(time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond)
		inst.failOn = 3
		_, ok := o.probeLatency("Probe_Model", inst, silentInput(inst.Spec()), false, func(run func()) bool { run(); return true })
		assert.False(t, ok)

		o.storeProbedLatency("Probe_Model", time.Second, true)
		o.storeProbedLatency("Probe_Model", 0, false)
		assert.NotContains(t, o.ProbedLatencies(), "Probe_Model")
	})
}

func TestProbeLatency_AbortsWhenRunRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		o := &Orchestrator{}
		inst := newScripted()
		_, ok := o.probeLatency("Probe_Model", inst, silentInput(inst.Spec()), false, func(func()) bool { return false })
		assert.False(t, ok)
		assert.Zero(t, inst.calls)
	})
}

// TestProbeLatency_ReleasesLockBetweenRuns guards the stall described for the
// load path: a concurrent inference must be able to take the lock between probe
// runs instead of waiting for the whole probe.
func TestProbeLatency_ReleasesLockBetweenRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		o := &Orchestrator{}
		inst := newScripted(time.Second, time.Second, time.Second, time.Second)
		// A channel semaphore stands in for inferenceMu: a goroutine blocked on a
		// sync.Mutex is not durably blocked, so synctest could not advance its clock.
		sem := make(chan struct{}, 1)
		lockRun := func(run func()) bool {
			sem <- struct{}{}
			defer func() { <-sem }()
			run()
			return true
		}

		var acquiredAt []time.Duration
		start := time.Now()
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(500 * time.Millisecond) // lands mid-way through the first run
			sem <- struct{}{}
			acquiredAt = append(acquiredAt, time.Since(start))
			<-sem
		}()
		_, ok := o.probeLatency("Probe_Model", inst, silentInput(inst.Spec()), false, lockRun)
		<-done
		require.True(t, ok)
		require.Len(t, acquiredAt, 1)
		assert.Equal(t, time.Second, acquiredAt[0], "waiter gets the lock after one run, not after the whole probe")
	})
}

// TestRunPendingWarmups_StoresProbedLatency pins that the deferred warm-up path
// records the probe median for the model, and a failing probe leaves it unknown.
func TestRunPendingWarmups_StoresProbedLatency(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		good := newScripted(time.Second, 100*time.Millisecond, 300*time.Millisecond, 200*time.Millisecond)
		good.id = "Good_Model"
		bad := newScripted(time.Second, time.Millisecond, time.Millisecond, time.Millisecond)
		bad.id = "Bad_Model"
		bad.failOn = 3 // warm-up is call 1, the timed probe runs calls 2 to 4

		o := &Orchestrator{
			models: map[string]*modelEntry{
				good.id: {instance: good},
				bad.id:  {instance: bad},
			},
			modelRSS: make(map[string]int64),
		}
		o.deferWarmup(good.id, 0)
		o.deferWarmup(bad.id, 0)
		o.runPendingWarmups()

		got := o.ProbedLatencies()
		assert.Equal(t, 200*time.Millisecond, got[good.id])
		assert.NotContains(t, got, bad.id)
	})
}

// TestModelInfos_StampsEffectiveOverlap pins that ModelInfos resolves each model's
// overlap from the published cadence plan, not from the configured overlap.
func TestModelInfos_StampsEffectiveOverlap(t *testing.T) {
	t.Parallel()
	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.8
	inst := newScripted()
	o := &Orchestrator{
		Settings: s,
		models:   map[string]*modelEntry{inst.id: {instance: inst}},
	}

	infos := o.ModelInfos()
	require.Len(t, infos, 1)
	assert.Equal(t, 2800*time.Millisecond, infos[0].Overlap, "no plan: configured overlap")

	o.SetCadencePlan(&cadence.Plan{ConfiguredBaseOverlap: 2800 * time.Millisecond, EffectiveBaseOverlap: 1800 * time.Millisecond})
	infos = o.ModelInfos()
	require.Len(t, infos, 1)
	assert.Equal(t, 1800*time.Millisecond, infos[0].Overlap, "published plan wins")
}

// TestRunPendingWarmups_ProbeOfReplacedInstanceStoresNothing pins that a probe
// whose instance was swapped out mid-probe neither stores nor deletes a latency:
// the replacement's fresher probe must survive.
func TestRunPendingWarmups_ProbeOfReplacedInstanceStoresNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		old := newScripted(time.Second, time.Second, time.Second, time.Second, time.Second)
		old.id = "Swapped_Model"
		replacement := newScripted()
		replacement.id = old.id
		entry := &modelEntry{instance: old}
		// The swap lands during the second timed probe run (call 3), as a reload would.
		old.onCall = func(call int) {
			if call == 3 {
				entry.instance = replacement
			}
		}

		o := &Orchestrator{
			models:   map[string]*modelEntry{old.id: entry},
			modelRSS: make(map[string]int64),
		}
		const fresh = 7 * time.Second
		o.storeProbedLatency(old.id, fresh, true) // what the reload of the replacement recorded
		o.deferWarmup(old.id, 0)
		o.runPendingWarmups()

		assert.Equal(t, fresh, o.ProbedLatencies()[old.id], "the replacement's probe must not be overwritten or deleted")
	})
}
