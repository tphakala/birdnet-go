package analysis

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
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
// wallWait and reports a scripted timing split.
type timedBackend struct {
	classifierBackend
	wallWait time.Duration
	timing   classifier.PredictTiming
}

func (b timedBackend) PredictModelTimed(_ context.Context, _ string, _ [][]float32) ([]datastore.Results, classifier.PredictTiming, error) {
	time.Sleep(b.wallWait)
	return nil, b.timing, nil
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
	// Not parallel: package-global overrunTrackers, processMetrics, ResultsQueue.
	conftest.SetTestSettings(conftest.GetTestSettings())
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	reg = prometheus.NewRegistry()
	m, err := metrics.NewMyAudioMetrics(reg)
	require.NoError(t, err)
	prev := processMetrics.Swap(m)
	t.Cleanup(func() { processMetrics.Store(prev) })

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
	err = ProcessData(t.Context(), backend, mgr, &ProcessRequest{
		Data:            make([]byte, overrunTestWindowBytes),
		StartTime:       time.Now(),
		AudioCapturedAt: time.Now(),
		Source:          source,
		ModelID:         classifier.RegistryIDBirdNETV24,
	})
	require.NoError(t, err)

	tracker := getOverrunTracker(source, classifier.RegistryIDBirdNETV24)
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return reg, tracker.overrunCount
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
	const source = "overrun-lockwait-src"
	reg, overruns := runOverrunCase(t, source, timedBackend{
		wallWait: overrunTestSlow,
		timing:   classifier.PredictTiming{LockWait: overrunTestSlow, Predict: overrunTestFast},
	})

	assert.Zero(t, overruns, "waiting for the inference lock is not this window's own work")
	assert.Zero(t, counterValue(t, reg, "myaudio_birdnet_processing_overruns_total", source))
}

func TestProcessData_SlowPredictIsAnOverrun(t *testing.T) {
	const source = "overrun-slowpredict-src"
	reg, overruns := runOverrunCase(t, source, timedBackend{
		timing: classifier.PredictTiming{Predict: overrunTestSlow},
	})

	assert.Equal(t, int64(1), overruns)
	assert.InDelta(t, 1.0, counterValue(t, reg, "myaudio_birdnet_processing_overruns_total", source), 0.001)
}

func TestProcessData_InferenceDurationMetricExcludesLockWait(t *testing.T) {
	const source = "overrun-metrics-src"
	reg, _ := runOverrunCase(t, source, timedBackend{
		wallWait: overrunTestSlow,
		timing:   classifier.PredictTiming{LockWait: overrunTestSlow, Predict: overrunTestFast},
	})

	assert.Less(t, histogramSum(t, reg, "myaudio_audio_inference_duration_seconds", source), overrunTestInterval.Seconds())
	assert.GreaterOrEqual(t, histogramSum(t, reg, "myaudio_audio_inference_lock_wait_seconds", source), overrunTestSlow.Seconds())
}
