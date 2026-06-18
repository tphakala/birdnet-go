package observability

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/classifier/inferencestats"
)

func TestCollector_CollectsCPU(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(100)
	cpuFunc := func() float64 { return 42.5 }
	collector := NewCollector(store, time.Second, cpuFunc)

	collector.collect()

	points := store.Get("cpu.total", 1)
	require.Len(t, points, 1)
	assert.InDelta(t, 42.5, points[0].Value, 0.01)
}

func TestCollector_CollectsMemory(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(100)
	collector := NewCollector(store, time.Second, nil)

	collector.collect()

	// Memory should always be available on any platform
	points := store.Get("memory.used_percent", 1)
	require.Len(t, points, 1)
	assert.Greater(t, points[0].Value, 0.0)
	assert.LessOrEqual(t, points[0].Value, 100.0)
}

func TestCollector_NilCPUFunc(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(100)
	collector := NewCollector(store, time.Second, nil)

	collector.collect()

	// Should not record cpu.total when cpuFunc is nil
	points := store.Get("cpu.total", 1)
	assert.Nil(t, points)
}

func TestCollector_StartAndCancel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore(100)
		cpuFunc := func() float64 { return 50.0 }
		collector := NewCollector(store, 5*time.Second, cpuFunc)

		ctx, cancel := context.WithCancel(t.Context())

		done := make(chan struct{})
		go func() {
			collector.Start(ctx)
			close(done)
		}()

		// Let a few ticks pass
		time.Sleep(12 * time.Second)

		cancel()
		<-done

		// Should have the initial collection + 2 ticks = 3 points
		points := store.Get("cpu.total", 100)
		assert.GreaterOrEqual(t, len(points), 3)
	})
}

func TestCollector_DiskIODelta(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(100)
	cpuFunc := func() float64 { return 10.0 }
	collector := NewCollector(store, time.Second, cpuFunc)

	// First collection: establishes baseline (no disk.io.* recorded)
	collector.collect()
	names1 := store.Names()
	hasDiskIO := false
	for _, name := range names1 {
		if len(name) > 7 && name[:7] == "disk.io" {
			hasDiskIO = true
			break
		}
	}
	assert.False(t, hasDiskIO, "first collection should not produce disk I/O rates")

	// Second collection: computes deltas
	collector.collect()

	// After second collection, disk I/O rates should exist (if the system has disks)
	// We can't assert specific values since I/O rates depend on actual system activity
	names2 := store.Names()
	assert.GreaterOrEqual(t, len(names2), len(names1))
}

func TestSanitizeMountpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"/", "root"},
		{"/home", "home"},
		{"/mnt/data", "mnt-data"},
		{"/mnt/external/usb", "mnt-external-usb"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, sanitizeMountpoint(tt.input))
		})
	}
}

func TestSkipCollectorFS(t *testing.T) {
	t.Parallel()

	// Should skip virtual filesystems
	assert.True(t, skipCollectorFS("sysfs"))
	assert.True(t, skipCollectorFS("proc"))
	assert.True(t, skipCollectorFS("tmpfs"))
	assert.True(t, skipCollectorFS("overlay"))

	// Should not skip real filesystems
	assert.False(t, skipCollectorFS("ext4"))
	assert.False(t, skipCollectorFS("btrfs"))
	assert.False(t, skipCollectorFS("xfs"))
	assert.False(t, skipCollectorFS("ntfs"))
}

func TestReadThermalZone_InvalidPath(t *testing.T) {
	t.Parallel()

	_, ok := readThermalZone("/nonexistent/path")
	assert.False(t, ok)
}

func TestReadThermalZone_ValidSyntheticZone(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Create a synthetic thermal zone
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "type"), []byte("cpu-thermal\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "temp"), []byte("45000\n"), 0o600))

	temp, ok := readThermalZone(tmpDir)
	require.True(t, ok)
	assert.InDelta(t, 45.0, temp, 0.01)
}

func TestReadThermalZone_NonCPUSensor(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "type"), []byte("gpu-thermal\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "temp"), []byte("50000\n"), 0o600))

	_, ok := readThermalZone(tmpDir)
	assert.False(t, ok, "non-CPU sensor should be skipped")
}

func TestCollector_CollectsInferenceMetrics(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)
	collector := NewCollector(store, 1*time.Second, func() float64 { return 0 })

	counters := &inferencestats.CounterMap{}
	counters.RecordInvoke("BirdNET_V2.4", 5000)  // 5ms
	counters.RecordInvoke("BirdNET_V2.4", 15000) // 15ms
	counters.RecordInvoke("Perch_V2", 8000)      // 8ms
	collector.SetInferenceCounters(counters)

	birdnetKey := inferencestats.MetricKey("BirdNET_V2.4")
	perchKey := inferencestats.MetricKey("Perch_V2")

	// First tick: no avg yet (no previous snapshot)
	collector.collect()
	avgPts := store.Get(birdnetKey, 10)
	assert.Nil(t, avgPts, "avg should not be recorded on first tick")

	// Record more data for second tick
	counters.RecordInvoke("BirdNET_V2.4", 10000) // 10ms
	counters.RecordInvoke("Perch_V2", 6000)      // 6ms

	// Second tick: should have per-model avg
	collector.collect()

	birdnetAvg := store.Get(birdnetKey, 10)
	require.Len(t, birdnetAvg, 1)
	assert.InDelta(t, 10.0, birdnetAvg[0].Value, 0.01) // 10ms / 1 invoke

	perchAvg := store.Get(perchKey, 10)
	require.Len(t, perchAvg, 1)
	assert.InDelta(t, 6.0, perchAvg[0].Value, 0.01) // 6ms / 1 invoke

	// Old metric keys should not exist
	assert.Nil(t, store.Get("birdnet.invoke_avg_ms", 10))
	assert.Nil(t, store.Get("birdnet.invoke_max_ms", 10))
}

func TestCollector_InferenceIdlePeriod(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)
	collector := NewCollector(store, 1*time.Second, func() float64 { return 0 })

	counters := &inferencestats.CounterMap{}
	counters.RecordInvoke("BirdNET_V2.4", 5000)
	collector.SetInferenceCounters(counters)

	// Two ticks with no new data between them
	collector.collect()
	collector.collect()

	avgPts := store.Get(inferencestats.MetricKey("BirdNET_V2.4"), 10)
	require.Len(t, avgPts, 1)
	assert.InDelta(t, 0.0, avgPts[0].Value, 0.001, "idle period should record 0")
}

func TestCollector_EmitsRTFKeyAndGauge(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(60)
	counters := &inferencestats.CounterMap{}
	collector := NewCollector(store, time.Second, func() float64 { return 0 })
	collector.SetInferenceCounters(counters)
	collector.SetModelClipFunc(func() map[string]float64 { return map[string]float64{"M": 3.0} })

	var gotRTFModel string
	var gotRTF float64
	collector.SetInferenceGaugeSetters(
		func(model string, rtf float64) { gotRTFModel = model; gotRTF = rtf },
		func(_ string, _ int64) {},
		func(_ string) {},
	)

	// First tick: establishes the previous snapshot (no rtf emitted yet).
	counters.RecordInvoke("M", 30_000) // 30 ms
	collector.collect()

	// Second tick: one more 30 ms invocation -> interval avg 30 ms, rtf = 0.030s / 3s = 0.01.
	counters.RecordInvoke("M", 30_000)
	collector.collect()

	pts := store.Get(inferencestats.RTFMetricKey("M"), 10)
	require.Len(t, pts, 1, "expected an inference.M.rtf ring-buffer point")
	assert.InDelta(t, 0.01, pts[0].Value, 0.001, "ring-buffer RTF value")

	assert.Equal(t, "M", gotRTFModel, "rtf gauge model should be M")
	assert.InDelta(t, 0.01, gotRTF, 0.001, "rtf should be approx 0.01")
}

func TestReadThermalZone_OutOfRangeTemperature(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "type"), []byte("cpu-thermal\n"), 0o600))
	// 150°C is out of valid range
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "temp"), []byte("150000\n"), 0o600))

	_, ok := readThermalZone(tmpDir)
	assert.False(t, ok, "out-of-range temperature should be rejected")
}

// TestCollector_AudioQueueDepth verifies that collectAudio records the aggregate
// audio queue depth into the MetricsStore batch (via RecordBatch) so the frontend
// sparkline series and the metrics history API can serve it.
//
// Queue depth is an instantaneous gauge: each call to collect() writes the current
// sum of all source depths into the MetricsStore as a single "audio.queue_depth"
// point. The healthStore is NOT involved for queue depth.
func TestCollector_AudioQueueDepth(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(100)
	collector := NewCollector(store, time.Second, nil)

	snapshots := []AudioRouterSnapshot{
		{SourceID: "src1", Drops: 0, Errors: 0, QueueDepth: 3},
		{SourceID: "src2", Drops: 5, Errors: 1, QueueDepth: 7},
	}
	collector.SetAudioRouter(func() []AudioRouterSnapshot {
		return snapshots
	})

	// First call: aggregate (3 + 7 = 10) is recorded immediately.
	points := make(map[string]float64, 4)
	collector.collectAudio(points)

	require.Contains(t, points, MetricKeyAudioQueueDepthAggregate, "aggregate key must be in the batch")
	assert.InDelta(t, 10.0, points[MetricKeyAudioQueueDepthAggregate], 0.001, "aggregate = 3 + 7 = 10")

	// Commit the batch so the MetricsStore has a data point.
	store.RecordBatch(points)

	pts := store.Get(MetricKeyAudioQueueDepthAggregate, 10)
	require.Len(t, pts, 1, "MetricsStore must have one point after first batch")
	assert.InDelta(t, 10.0, pts[0].Value, 0.001, "MetricsStore value = 10")

	// Second call with changed depths: aggregate updates correctly.
	snapshots = []AudioRouterSnapshot{
		{SourceID: "src1", Drops: 0, Errors: 0, QueueDepth: 1},
		{SourceID: "src2", Drops: 5, Errors: 1, QueueDepth: 2},
	}
	points2 := make(map[string]float64, 4)
	collector.collectAudio(points2)

	require.Contains(t, points2, MetricKeyAudioQueueDepthAggregate)
	assert.InDelta(t, 3.0, points2[MetricKeyAudioQueueDepthAggregate], 0.001, "aggregate = 1 + 2 = 3")

	// No-op when audioRouterFn is nil.
	collector2 := NewCollector(NewMemoryStore(100), time.Second, nil)
	empty := make(map[string]float64, 4)
	collector2.collectAudio(empty)
	assert.Empty(t, empty, "no points recorded when audioRouterFn is nil")
}

// TestCollector_InferenceThroughput verifies that the collector records per-model
// throughput (invocations per second) in the MetricsStore under the correct key.
//
// Throughput is a delta metric: it is undefined on the first (seeding) tick and
// should only appear from the second tick onward. The value is computed as
// deltaInvokes / elapsedSeconds over the interval.
func TestCollector_InferenceThroughput(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)
	collector := NewCollector(store, time.Second, func() float64 { return 0 })

	counters := &inferencestats.CounterMap{}
	counters.RecordInvoke("ModelA", 10_000) // 10 ms
	counters.RecordInvoke("ModelA", 10_000)
	counters.RecordError("ModelA")
	collector.SetInferenceCounters(counters)

	throughputKey := inferencestats.ThroughputMetricKey("ModelA")

	// First tick: seeds the previous snapshot; no throughput recorded yet.
	collector.collect()
	pts := store.Get(throughputKey, 10)
	assert.Nil(t, pts, "throughput must not be recorded on the seeding tick")

	// Record two more invocations before the second tick.
	counters.RecordInvoke("ModelA", 10_000)
	counters.RecordInvoke("ModelA", 10_000)

	// Second tick: delta = 2 invocations over whatever elapsed time.
	collector.collect()

	pts = store.Get(throughputKey, 10)
	require.Len(t, pts, 1, "throughput must be recorded after second tick")
	// The elapsed time is tiny in tests (microseconds to milliseconds), so throughput
	// will be a large positive number. We verify it is strictly positive.
	assert.Greater(t, pts[0].Value, 0.0, "throughput must be positive when invocations occurred")
}

// TestCollector_InferenceThroughputZeroWhenIdle verifies that throughput is
// recorded as exactly 0 when no invocations occurred in the interval.
func TestCollector_InferenceThroughputZeroWhenIdle(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)
	collector := NewCollector(store, time.Second, func() float64 { return 0 })

	counters := &inferencestats.CounterMap{}
	counters.RecordInvoke("ModelB", 5_000)
	collector.SetInferenceCounters(counters)

	throughputKey := inferencestats.ThroughputMetricKey("ModelB")

	// First tick: seeding.
	collector.collect()

	// Second tick: no new invocations since the first tick.
	collector.collect()

	pts := store.Get(throughputKey, 10)
	require.Len(t, pts, 1, "throughput must be recorded even during idle")
	assert.InDelta(t, 0.0, pts[0].Value, 0.0001, "throughput must be 0 when idle")
}

// TestCollector_InferenceErrorRate verifies that the collector records per-model
// error rate (errors / (errors + invocations)) in the MetricsStore under the
// correct key. The value is in [0, 1].
func TestCollector_InferenceErrorRate(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)
	collector := NewCollector(store, time.Second, func() float64 { return 0 })

	counters := &inferencestats.CounterMap{}
	// Seed: 3 invocations, 1 error.
	counters.RecordInvoke("ModelC", 10_000)
	counters.RecordInvoke("ModelC", 10_000)
	counters.RecordInvoke("ModelC", 10_000)
	counters.RecordError("ModelC")
	collector.SetInferenceCounters(counters)

	errorRateKey := inferencestats.ErrorRateMetricKey("ModelC")

	// First tick: seeding only.
	collector.collect()
	pts := store.Get(errorRateKey, 10)
	assert.Nil(t, pts, "error_rate must not be recorded on the seeding tick")

	// Interval: 2 more invocations, 1 more error (delta errors=1, delta invokes=2).
	counters.RecordInvoke("ModelC", 10_000)
	counters.RecordInvoke("ModelC", 10_000)
	counters.RecordError("ModelC")

	// Second tick: delta = 1 error / (1 error + 2 invokes) = 1/3 ≈ 0.333...
	collector.collect()

	pts = store.Get(errorRateKey, 10)
	require.Len(t, pts, 1, "error_rate must be recorded after second tick")
	assert.InDelta(t, 1.0/3.0, pts[0].Value, 0.0001, "error_rate = 1/(1+2) = 0.333")
}

// TestCollector_InferenceErrorRateZeroWhenNoErrors verifies that error_rate is
// exactly 0 when no errors occurred in the interval.
func TestCollector_InferenceErrorRateZeroWhenNoErrors(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)
	collector := NewCollector(store, time.Second, func() float64 { return 0 })

	counters := &inferencestats.CounterMap{}
	counters.RecordInvoke("ModelD", 5_000)
	collector.SetInferenceCounters(counters)

	errorRateKey := inferencestats.ErrorRateMetricKey("ModelD")

	// First tick: seeding.
	collector.collect()

	// Second tick: one more invocation, zero errors.
	counters.RecordInvoke("ModelD", 5_000)
	collector.collect()

	pts := store.Get(errorRateKey, 10)
	require.Len(t, pts, 1, "error_rate must be recorded")
	assert.InDelta(t, 0.0, pts[0].Value, 0.0001, "error_rate must be 0 when no errors")
}

// TestCollector_InferenceErrorRateZeroWhenIdle verifies that error_rate is 0
// when neither errors nor invocations occurred in the interval.
func TestCollector_InferenceErrorRateZeroWhenIdle(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)
	collector := NewCollector(store, time.Second, func() float64 { return 0 })

	counters := &inferencestats.CounterMap{}
	counters.RecordInvoke("ModelE", 5_000)
	counters.RecordError("ModelE")
	collector.SetInferenceCounters(counters)

	errorRateKey := inferencestats.ErrorRateMetricKey("ModelE")

	// First tick: seeding.
	collector.collect()

	// Second tick: no new activity.
	collector.collect()

	pts := store.Get(errorRateKey, 10)
	require.Len(t, pts, 1, "error_rate must be recorded even when idle")
	assert.InDelta(t, 0.0, pts[0].Value, 0.0001, "error_rate must be 0 when idle (no delta)")
}

// TestCollector_InferenceErrorRateCounterReset verifies that when the inference
// counters decrease (counter reset - e.g. process restart or counter wrap),
// the collector treats the current absolute values as the tick delta rather
// than computing a negative rate. A fresh CounterMap with lower absolute
// values is injected between ticks to force current < previous.
func TestCollector_InferenceErrorRateCounterReset(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)
	collector := NewCollector(store, time.Second, func() float64 { return 0 })

	// Tick 1: seed with high absolute values (10 invocations, 3 errors).
	counters1 := &inferencestats.CounterMap{}
	for range 10 {
		counters1.RecordInvoke("ModelF", 10_000)
	}
	for range 3 {
		counters1.RecordError("ModelF")
	}
	collector.SetInferenceCounters(counters1)
	collector.collect() // seeding tick; nothing written to store

	// Tick 2: inject a fresh CounterMap with lower absolute values so that
	// current < previous on both InvokeCount and InvokeErrors. This simulates
	// a counter reset (e.g. the underlying counters were replaced).
	// New absolute values: InvokeCount=2, InvokeErrors=1.
	counters2 := &inferencestats.CounterMap{}
	counters2.RecordInvoke("ModelF", 10_000)
	counters2.RecordInvoke("ModelF", 10_000)
	counters2.RecordError("ModelF")
	collector.SetInferenceCounters(counters2)
	collector.collect()

	errorRateKey := inferencestats.ErrorRateMetricKey("ModelF")
	pts := store.Get(errorRateKey, 10)
	require.Len(t, pts, 1)
	// Reset guard: tpInvokes=2 (absolute), tpErrors=1 (absolute).
	// error_rate = 1 / (1+2) = 0.333...
	assert.InDelta(t, 1.0/3.0, pts[0].Value, 0.0001, "error_rate uses absolute value after counter reset")

	// Throughput must also be positive (not zero or negative) after a reset.
	throughputKey := inferencestats.ThroughputMetricKey("ModelF")
	tpts := store.Get(throughputKey, 10)
	require.Len(t, tpts, 1)
	assert.Greater(t, tpts[0].Value, 0.0, "throughput uses absolute delta after counter reset")
}

// TestCollector_AudioQueueDepth_PrometheusGauges verifies that the Prometheus
// gauge setters are called with the correct source and value on each tick.
func TestCollector_AudioQueueDepth_PrometheusGauges(t *testing.T) {
	t.Parallel()

	healthStore := NewHealthMetricsStore()
	store := NewMemoryStore(100)
	collector := NewCollector(store, time.Second, nil)
	collector.SetHealthStore(healthStore)

	snapshots := []AudioRouterSnapshot{
		{SourceID: "src1", Drops: 10, Errors: 0, QueueDepth: 5},
	}
	collector.SetAudioRouter(func() []AudioRouterSnapshot { return snapshots })

	type gaugeCall struct {
		source string
		value  float64
	}
	var depthCalls []gaugeCall
	var dropCalls []gaugeCall

	collector.SetAudioGaugeSetters(
		func(source string, depth float64) { depthCalls = append(depthCalls, gaugeCall{source, depth}) },
		func(source string, total float64) { dropCalls = append(dropCalls, gaugeCall{source, total}) },
	)

	now := time.Now()

	// First tick: sources are new, so gauges must NOT be called (seeding tick).
	collector.collectAudioHealthCounters(now)
	assert.Empty(t, depthCalls, "no gauge calls on first (seeding) tick")
	assert.Empty(t, dropCalls, "no gauge calls on first (seeding) tick")

	// Second tick: gauges must be set.
	now2 := now.Add(2 * time.Hour)
	collector.collectAudioHealthCounters(now2)
	require.Len(t, depthCalls, 1, "queue-depth gauge called once on second tick")
	assert.Equal(t, "src1", depthCalls[0].source)
	assert.InDelta(t, 5.0, depthCalls[0].value, 0.001)

	require.Len(t, dropCalls, 1, "dropped-chunks gauge called once on second tick")
	assert.Equal(t, "src1", dropCalls[0].source)
	assert.InDelta(t, 10.0, dropCalls[0].value, 0.001)
}
