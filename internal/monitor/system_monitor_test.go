package monitor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestSystemMonitor_DiskCollectsMetrics(t *testing.T) {
	t.Parallel()

	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				Disk: conf.DiskMonitorConfig{
					Enabled: true,
					Paths:   []string{"/"},
				},
			},
		},
	}

	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Should not panic
	monitor.checkDisk()
}

func TestSystemMonitor_EmptyPathsDefaultsToRoot(t *testing.T) {
	t.Parallel()

	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				Disk: conf.DiskMonitorConfig{
					Enabled: true,
					Paths:   []string{},
				},
			},
		},
	}

	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Should not panic, defaults to "/"
	monitor.checkDisk()
}

func TestSystemMonitor_CPUCollectsMetrics(t *testing.T) {
	t.Parallel()

	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				CPU:           conf.ResourceEnabled{Enabled: true},
			},
		},
	}

	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Should not panic
	monitor.checkCPU()
}

func TestSystemMonitor_MemoryCollectsMetrics(t *testing.T) {
	t.Parallel()

	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				Memory:        conf.ResourceEnabled{Enabled: true},
			},
		},
	}

	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Should not panic
	monitor.checkMemory()
}

func TestSystemMonitor_Lifecycle(t *testing.T) {
	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				CPU:           conf.ResourceEnabled{Enabled: true},
				Memory:        conf.ResourceEnabled{Enabled: true},
				Disk: conf.DiskMonitorConfig{
					Enabled: true,
					Paths:   []string{"/"},
				},
			},
		},
	}

	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	var wg sync.WaitGroup
	started := make(chan struct{})
	wg.Go(func() {
		monitor.Start()
		close(started)
	})

	select {
	case <-started:
	case <-time.After(1 * time.Second):
		require.Fail(t, "Monitor failed to start within timeout")
	}

	time.Sleep(100 * time.Millisecond)
	monitor.Stop()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.Fail(t, "Monitor failed to stop within timeout")
	}

	select {
	case <-monitor.ctx.Done():
	default:
		require.Fail(t, "Monitor context should be cancelled after Stop()")
	}
}

func TestSystemMonitor_DisabledMonitoring(t *testing.T) {
	t.Parallel()

	config := &conf.Settings{}
	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Should return immediately when disabled
	monitor.Start()
	monitor.Stop()
}

func TestSystemMonitor_GetMonitoredPaths(t *testing.T) {
	t.Parallel()

	config := &conf.Settings{}
	config.Realtime.Monitoring.Disk.Enabled = true
	config.Realtime.Monitoring.Disk.Paths = []string{"/", "/home"}

	monitor := &SystemMonitor{
		config: config,
		log:    GetLogger(),
	}

	paths := monitor.GetMonitoredPaths()
	assert.Equal(t, []string{"/", "/home"}, paths)

	// Test with disk monitoring disabled
	config.Realtime.Monitoring.Disk.Enabled = false
	paths = monitor.GetMonitoredPaths()
	assert.Nil(t, paths)
}

func TestSystemMonitor_InvalidDiskPath(t *testing.T) {
	t.Parallel()

	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				Disk: conf.DiskMonitorConfig{
					Enabled: true,
					Paths:   []string{"/this/path/does/not/exist"},
				},
			},
		},
	}

	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Should not panic on invalid path
	monitor.checkDisk()
}
