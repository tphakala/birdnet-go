package monitor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestDiskMonitoringMultiplePaths(t *testing.T) {
	// Create config with multiple disk paths
	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1, // 1 second for testing
				Disk: conf.DiskThresholdSettings{
					Enabled:  true,
					Warning:  80.0,
					Critical: 90.0,
					Paths:    []string{"/", "/tmp"}, // Monitor root and /tmp
				},
			},
		},
	}

	// Create monitor
	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Test that multiple paths are configured
	assert.Equal(t, []string{"/", "/tmp"}, config.Realtime.Monitoring.Disk.Paths)

	// Test checkDisk with multiple paths
	monitor.checkDisk()

	// Verify that validated paths include both configured paths
	monitor.mu.RLock()
	_, rootValidated := monitor.validatedPaths["/"]
	_, tmpValidated := monitor.validatedPaths["/tmp"]
	monitor.mu.RUnlock()

	assert.True(t, rootValidated, "Root path should be validated")
	assert.True(t, tmpValidated, "/tmp path should be validated")

	// Verify alert states are created for both paths
	monitor.mu.RLock()
	_, rootState := monitor.alertStates["disk|/"]
	_, tmpState := monitor.alertStates["disk|/tmp"]
	monitor.mu.RUnlock()

	assert.True(t, rootState, "Alert state should exist for root path")
	assert.True(t, tmpState, "Alert state should exist for /tmp path")
}

func TestDiskMonitoringEmptyPaths(t *testing.T) {
	// Create config with empty disk paths
	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				Disk: conf.DiskThresholdSettings{
					Enabled:  true,
					Warning:  80.0,
					Critical: 90.0,
					Paths:    []string{}, // Empty paths
				},
			},
		},
	}

	// Create monitor
	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Test checkDisk with empty paths (should default to "/")
	monitor.checkDisk()

	// Verify that root path is validated
	monitor.mu.RLock()
	validated, exists := monitor.validatedPaths["/"]
	monitor.mu.RUnlock()

	assert.True(t, exists && validated, "Root path should be validated when paths is empty")
}

func TestDiskMonitoringInvalidPath(t *testing.T) {
	// Create config with an invalid path
	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				Disk: conf.DiskThresholdSettings{
					Enabled:  true,
					Warning:  80.0,
					Critical: 90.0,
					Paths:    []string{"/", "/this/path/does/not/exist"},
				},
			},
		},
	}

	// Create monitor
	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Test checkDisk with invalid path
	monitor.checkDisk()

	// Verify that valid path is marked as validated
	monitor.mu.RLock()
	rootValidated, rootExists := monitor.validatedPaths["/"]
	invalidValidated, invalidExists := monitor.validatedPaths["/this/path/does/not/exist"]
	monitor.mu.RUnlock()

	assert.True(t, rootExists && rootValidated, "Root path should be validated")
	assert.True(t, invalidExists && !invalidValidated, "Invalid path should be marked as not validated")
}

func TestDiskMonitoringPathSpecificStates(t *testing.T) {
	// Create config
	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				Disk: conf.DiskThresholdSettings{
					Enabled:  true,
					Warning:  80.0,
					Critical: 90.0,
					Paths:    []string{"/", "/tmp"},
				},
			},
		},
	}

	// Create monitor
	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Simulate different thresholds for different paths
	// This tests that each path maintains its own state
	monitor.checkThresholdsWithPath(ResourceDisk, 85.0, 80.0, 90.0, "/")
	monitor.checkThresholdsWithPath(ResourceDisk, 50.0, 80.0, 90.0, "/tmp")

	// Check states
	monitor.mu.RLock()
	rootState := monitor.alertStates["disk|/"]
	tmpState := monitor.alertStates["disk|/tmp"]
	monitor.mu.RUnlock()

	require.NotNil(t, rootState, "Root state should exist")
	require.NotNil(t, tmpState, "Tmp state should exist")

	assert.True(t, rootState.InWarning, "Root should be in warning state (85% > 80%)")
	assert.False(t, rootState.InCritical, "Root should not be in critical state (85% < 90%)")
	assert.False(t, tmpState.InWarning, "Tmp should not be in warning state (50% < 80%)")
	assert.False(t, tmpState.InCritical, "Tmp should not be in critical state (50% < 90%)")
}

func TestDiskMonitoringRecoveryPerPath(t *testing.T) {
	// Create config
	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:           true,
				CheckInterval:     1,
				HysteresisPercent: 5.0,
				Disk: conf.DiskThresholdSettings{
					Enabled:  true,
					Warning:  80.0,
					Critical: 90.0,
					Paths:    []string{"/", "/tmp"},
				},
			},
		},
	}

	// Create monitor
	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Put both paths into warning state
	monitor.checkThresholdsWithPath(ResourceDisk, 85.0, 80.0, 90.0, "/")
	monitor.checkThresholdsWithPath(ResourceDisk, 85.0, 80.0, 90.0, "/tmp")

	// Verify both are in warning
	monitor.mu.RLock()
	rootWarning1 := monitor.alertStates["disk|/"].InWarning
	tmpWarning1 := monitor.alertStates["disk|/tmp"].InWarning
	monitor.mu.RUnlock()

	assert.True(t, rootWarning1, "Root should be in warning state")
	assert.True(t, tmpWarning1, "Tmp should be in warning state")

	// Recover only /tmp (below warning - hysteresis)
	monitor.checkThresholdsWithPath(ResourceDisk, 74.0, 80.0, 90.0, "/tmp") // 74% < 75% (80-5)

	// Check states after recovery
	monitor.mu.RLock()
	rootWarning2 := monitor.alertStates["disk|/"].InWarning
	tmpWarning2 := monitor.alertStates["disk|/tmp"].InWarning
	monitor.mu.RUnlock()

	assert.True(t, rootWarning2, "Root should still be in warning state")
	assert.False(t, tmpWarning2, "Tmp should have recovered from warning state")
}

func TestSystemMonitorLifecycle(t *testing.T) {
	// Create config
	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				Disk: conf.DiskThresholdSettings{
					Enabled:  true,
					Warning:  80.0,
					Critical: 90.0,
					Paths:    []string{"/"},
				},
			},
		},
	}

	// Create and start monitor
	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	monitor.Start()

	// Let it run for a short time
	time.Sleep(100 * time.Millisecond)

	// Stop monitor
	monitor.Stop()

	// Verify monitor stopped properly
	select {
	case <-monitor.ctx.Done():
		// Context should be cancelled
	default:
		t.Fatal("Monitor context should be cancelled after Stop()")
	}
}