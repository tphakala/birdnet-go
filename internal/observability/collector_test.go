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
	"github.com/tphakala/birdnet-go/internal/birdnet/inferencestats"
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

	counters := &inferencestats.Counters{}
	counters.RecordInvoke(5000)  // 5ms
	counters.RecordInvoke(15000) // 15ms
	collector.SetInferenceCounters(counters)

	// First tick: only max (no previous snapshot for avg)
	collector.collect()
	maxPts := store.Get("birdnet.invoke_max_ms", 1)
	require.Len(t, maxPts, 1)
	assert.InDelta(t, 15.0, maxPts[0].Value, 0.01)

	// Avg should NOT be recorded on first tick (no prev snapshot)
	avgPts := store.Get("birdnet.invoke_avg_ms", 10)
	assert.Nil(t, avgPts, "avg should not be recorded on first tick")

	// Record more data for second tick
	counters.RecordInvoke(8000) // 8ms

	// Second tick: should have avg
	collector.collect()
	avgPts = store.Get("birdnet.invoke_avg_ms", 10)
	require.Len(t, avgPts, 1)
	// 8ms total / 1 invoke = 8ms avg
	assert.InDelta(t, 8.0, avgPts[0].Value, 0.01)
}

func TestCollector_InferenceIdleRecordsZero(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)
	collector := NewCollector(store, 1*time.Second, func() float64 { return 0 })

	counters := &inferencestats.Counters{}
	counters.RecordInvoke(5000)
	collector.SetInferenceCounters(counters)

	// First tick: establishes baseline
	collector.collect()

	// Second tick: no new invocations — should record 0
	collector.collect()
	avgPts := store.Get("birdnet.invoke_avg_ms", 10)
	require.Len(t, avgPts, 1)
	assert.InDelta(t, 0.0, avgPts[0].Value, 0.01)
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
