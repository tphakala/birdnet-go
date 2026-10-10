package analysis

import (
	"context"
	"fmt"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	overrunTestWindowBytes = 960
	// overrunTestEffectiveOverlap makes the BirdNET v2.4 analysis interval 100 ms
	// (3 s clip minus 2.9 s overlap).
	overrunTestEffectiveOverlap = 2900 * time.Millisecond
	overrunTestInterval         = 100 * time.Millisecond
	// overrunTestSlow is well above the interval; overrunTestFast well below.
	overrunTestSlow = 150 * time.Millisecond
	overrunTestFast = time.Millisecond
)

// timedBackend is a classifierBackend fake whose PredictModelTimed sleeps for
// wallWait (on synctest's fake clock, see runProcessData) and reports a
// scripted timing split.
type timedBackend struct {
	classifierBackend
	wallWait time.Duration
	timing   classifier.PredictTiming
	err      error // returned from PredictModelTimed alongside the timing
}

func (b timedBackend) PredictModelTimed(_ context.Context, _ string, _ [][]float32) ([]datastore.Results, classifier.PredictTiming, error) {
	time.Sleep(b.wallWait)
	return nil, b.timing, b.err
}

func (b timedBackend) EffectiveBaseOverlap() time.Duration { return overrunTestEffectiveOverlap }

func (b timedBackend) ModelSpecFor(id string) (classifier.ModelSpec, bool) {
	info, ok := classifier.ModelRegistry[id]
	return info.Spec, ok
}

// runOverrunCase runs one ProcessData call against a timedBackend with a fresh
// metrics registry installed, and returns the registry and the overrun count.
func runOverrunCase(t *testing.T, source string, backend timedBackend) (reg *prometheus.Registry, overruns int64) {
	t.Helper()
	reg, err := runProcessData(t, source, backend)
	require.NoError(t, err)
	return reg, overrunCount(source)
}

// runProcessData runs one ProcessData call with a fresh metrics registry
// installed and returns the registry and ProcessData's error. The call runs in
// a synctest bubble, so the backend's wall wait is fake time and PCM conversion
// takes no time. Results sent to the queue stay there until the test ends, and
// the source's overrun trackers are removed when it ends.
func runProcessData(t *testing.T, source string, backend timedBackend) (*prometheus.Registry, error) {
	t.Helper()
	// Not parallel: package-global overrunTrackers, processMetrics, ResultsQueue.
	conftest.SetTestSettings(conftest.GetTestSettings())
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	reg := prometheus.NewRegistry()
	m, err := metrics.NewMyAudioMetrics(reg)
	require.NoError(t, err)
	prev := processMetrics.Swap(m)
	t.Cleanup(func() { processMetrics.Store(prev) })
	t.Cleanup(func() { RemoveOverrunTrackers(source) })

	t.Cleanup(func() {
		for {
			select {
			case <-classifier.ResultsQueue:
			default:
				return
			}
		}
	})

	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	synctest.Test(t, func(t *testing.T) {
		err = ProcessData(t.Context(), backend, mgr, &ProcessRequest{
			Data:            make([]byte, overrunTestWindowBytes),
			StartTime:       time.Now(),
			AudioCapturedAt: time.Now(),
			Source:          source,
			ModelID:         classifier.RegistryIDBirdNETV24,
		})
	})
	return reg, err
}

// overrunCount returns the overrun tracker count for source and the BirdNET model.
func overrunCount(source string) int64 {
	count, _ := overrunTrackerState(source)
	return count
}

// overrunTrackerState returns the overrun count and the longest overrun the
// tracker for source and the BirdNET model has recorded.
func overrunTrackerState(source string) (count int64, maxElapsed time.Duration) {
	tracker := getOverrunTracker(source, classifier.RegistryIDBirdNETV24)
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.overrunCount, tracker.maxElapsed
}

// histogramSum returns the sample sum of the named histogram for source, or 0.
func histogramSum(t *testing.T, reg *prometheus.Registry, name, source string) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, metric := range mf.GetMetric() {
			if hasLabel(metric, source) {
				return metric.GetHistogram().GetSampleSum()
			}
		}
	}
	return 0
}

// counterValue returns the named counter for source, or 0 when absent.
func counterValue(t *testing.T, reg *prometheus.Registry, name, source string) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, metric := range mf.GetMetric() {
			if hasLabel(metric, source) {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func hasLabel(metric *dto.Metric, value string) bool {
	for _, l := range metric.GetLabel() {
		if l.GetValue() == value {
			return true
		}
	}
	return false
}

func TestProcessData_LockWaitAloneIsNotAnOverrun(t *testing.T) {
	// Lock waits from just over one analysis step to many steps; none of them
	// is this window's own work.
	waits := []struct {
		name string
		wait time.Duration
	}{
		{name: "just over one step", wait: overrunTestInterval + time.Millisecond},
		{name: "one and a half steps", wait: overrunTestSlow},
		{name: "ten steps", wait: 10 * overrunTestInterval},
	}
	for i, tt := range waits {
		t.Run(tt.name, func(t *testing.T) {
			source := fmt.Sprintf("overrun-lockwait-src-%d", i)
			reg, overruns := runOverrunCase(t, source, timedBackend{
				wallWait: tt.wait,
				timing:   classifier.PredictTiming{LockWait: tt.wait, Predict: overrunTestFast},
			})

			assert.Zero(t, overruns, "waiting for the inference lock is not this window's own work")
			assert.Zero(t, counterValue(t, reg, "myaudio_birdnet_processing_overruns_total", source))
			assert.Zero(t, histogramSum(t, reg, "myaudio_birdnet_processing_overrun_duration_seconds", source))
			assert.Zero(t, histogramSum(t, reg, "myaudio_birdnet_processing_overrun_ratio", source))
		})
	}
}

func TestProcessData_SlowPredictIsAnOverrun(t *testing.T) {
	const source = "overrun-slowpredict-src"
	// No wall wait, so the wall time is zero, and a scripted lock wait as long
	// as the model time, so these values rule out wall time and own work plus
	// lock wait. Lock wait alone is ruled out by the lock-wait-only test.
	reg, err := runProcessData(t, source, timedBackend{
		timing: classifier.PredictTiming{LockWait: overrunTestSlow, Predict: overrunTestSlow},
	})
	require.NoError(t, err)

	overruns, maxElapsed := overrunTrackerState(source)
	assert.Equal(t, int64(1), overruns)
	assert.Equal(t, overrunTestSlow, maxElapsed)
	assert.InDelta(t, 1.0, counterValue(t, reg, "myaudio_birdnet_processing_overruns_total", source), 0.001)
	assert.InDelta(t, overrunTestSlow.Seconds(), histogramSum(t, reg, "myaudio_birdnet_processing_overrun_duration_seconds", source), 1e-9)
	assert.InDelta(t, overrunTestSlow.Seconds()/overrunTestInterval.Seconds(), histogramSum(t, reg, "myaudio_birdnet_processing_overrun_ratio", source), 1e-9)
}

func TestProcessData_InferenceDurationMetricExcludesLockWait(t *testing.T) {
	const source = "overrun-metrics-src"
	reg, _ := runOverrunCase(t, source, timedBackend{
		wallWait: overrunTestSlow,
		timing:   classifier.PredictTiming{LockWait: overrunTestSlow, Predict: overrunTestFast},
	})

	assert.InDelta(t, overrunTestFast.Seconds(), histogramSum(t, reg, "myaudio_audio_inference_duration_seconds", source), 1e-9)
	assert.InDelta(t, overrunTestSlow.Seconds(), histogramSum(t, reg, "myaudio_audio_inference_lock_wait_seconds", source), 1e-9)
}

func TestProcessData_ModelErrorStillRecordsTimingMetrics(t *testing.T) {
	const source = "overrun-error-src"
	reg, err := runProcessData(t, source, timedBackend{
		wallWait: overrunTestSlow,
		timing:   classifier.PredictTiming{LockWait: overrunTestSlow, Predict: overrunTestFast},
		err:      errors.NewStd("predict failed"),
	})

	require.Error(t, err)
	assert.InDelta(t, overrunTestSlow.Seconds(), histogramSum(t, reg, "myaudio_audio_inference_lock_wait_seconds", source), 1e-9)
	assert.InDelta(t, overrunTestFast.Seconds(), histogramSum(t, reg, "myaudio_audio_inference_duration_seconds", source), 1e-9)
}

func TestProcessData_StoredElapsedTimeIncludesLockWait(t *testing.T) {
	const source = "overrun-elapsed-src"
	_, err := runProcessData(t, source, timedBackend{
		wallWait: overrunTestSlow,
		timing:   classifier.PredictTiming{LockWait: overrunTestSlow, Predict: overrunTestFast},
	})
	require.NoError(t, err)

	select {
	case res := <-classifier.ResultsQueue:
		assert.Equal(t, overrunTestSlow, res.ElapsedTime, "the stored processing time is wall time")
	default:
		t.Fatal("ProcessData queued no result")
	}
}
