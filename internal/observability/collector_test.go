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

// TestCollector_AudioQueueDepth verifies that collectAudioHealthCounters records
// per-source queue-depth gauges and the aggregate key on every tick (not as deltas).
//
// First tick behavior: seeds all keys with 0 (consistent with the drops/overruns
// first-tick pattern). Second tick onwards: records the actual instantaneous value.
func TestCollector_AudioQueueDepth(t *testing.T) {
	t.Parallel()

	healthStore := NewHealthMetricsStore()
	store := NewMemoryStore(100)
	collector := NewCollector(store, time.Second, nil)
	collector.SetHealthStore(healthStore)

	snapshots := []AudioRouterSnapshot{
		{SourceID: "src1", Drops: 0, Errors: 0, QueueDepth: 3},
		{SourceID: "src2", Drops: 5, Errors: 1, QueueDepth: 7},
	}
	collector.SetAudioRouter(func() []AudioRouterSnapshot {
		return snapshots
	})

	now := time.Now()

	// First tick: per-source and aggregate keys are seeded with 0.
	collector.collectAudioHealthCounters(now)

	src1Buckets := healthStore.Buckets(MetricPrefixAudioQueueDepth+"src1", 1)
	require.Len(t, src1Buckets, 1, "per-source queue depth for src1 must be seeded on first tick")
	assert.Equal(t, int64(0), src1Buckets[0].Count, "src1 seeded with 0 on first tick")

	src2Buckets := healthStore.Buckets(MetricPrefixAudioQueueDepth+"src2", 1)
	require.Len(t, src2Buckets, 1, "per-source queue depth for src2 must be seeded on first tick")
	assert.Equal(t, int64(0), src2Buckets[0].Count, "src2 seeded with 0 on first tick")

	aggBuckets := healthStore.Buckets(MetricKeyAudioQueueDepthAggregate, 1)
	require.Len(t, aggBuckets, 1, "aggregate queue depth must be seeded on first tick")
	assert.Equal(t, int64(0), aggBuckets[0].Count, "aggregate seeded with 0 on first tick")

	// Second tick (different hour to get a fresh bucket): queue depths are recorded.
	now2 := now.Add(2 * time.Hour)
	collector.collectAudioHealthCounters(now2)

	src1B2 := healthStore.Buckets(MetricPrefixAudioQueueDepth+"src1", 1)
	require.Len(t, src1B2, 1, "src1 bucket present after second tick")
	assert.Equal(t, int64(3), src1B2[0].Count, "src1 queue depth = 3 on second tick")

	src2B2 := healthStore.Buckets(MetricPrefixAudioQueueDepth+"src2", 1)
	require.Len(t, src2B2, 1, "src2 bucket present after second tick")
	assert.Equal(t, int64(7), src2B2[0].Count, "src2 queue depth = 7 on second tick")

	aggB2 := healthStore.Buckets(MetricKeyAudioQueueDepthAggregate, 1)
	require.Len(t, aggB2, 1, "aggregate bucket present after second tick")
	assert.Equal(t, int64(10), aggB2[0].Count, "aggregate queue depth = 3 + 7 = 10 on second tick")

	// Third tick in same hour as second: values accumulate within the bucket.
	snapshots = []AudioRouterSnapshot{
		{SourceID: "src1", Drops: 0, Errors: 0, QueueDepth: 1},
		{SourceID: "src2", Drops: 5, Errors: 1, QueueDepth: 2},
	}
	now3 := now2.Add(time.Minute)
	collector.collectAudioHealthCounters(now3)

	// Within the same hour bucket: 3 + 1 = 4 for src1, 7 + 2 = 9 for src2.
	src1Total := healthStore.LifetimeTotal(MetricPrefixAudioQueueDepth + "src1")
	assert.Equal(t, int64(3+1), src1Total, "src1 lifetime total after three ticks: seed(0) + tick2(3) + tick3(1)")

	src2Total := healthStore.LifetimeTotal(MetricPrefixAudioQueueDepth + "src2")
	assert.Equal(t, int64(7+2), src2Total, "src2 lifetime total: seed(0) + tick2(7) + tick3(2)")

	aggTotal := healthStore.LifetimeTotal(MetricKeyAudioQueueDepthAggregate)
	assert.Equal(t, int64(10+3), aggTotal, "aggregate lifetime total: seed(0) + tick2(10) + tick3(3)")
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
