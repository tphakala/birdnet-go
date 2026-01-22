package monitor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestDiskMonitoring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		paths         []string
		checkFunc     func(t *testing.T, monitor *SystemMonitor)
		skipCheckDisk bool // Some tests need custom threshold checks
	}{
		{
			name:  "multiple paths aggregated by mount",
			paths: []string{"/", "/tmp"},
			checkFunc: func(t *testing.T, monitor *SystemMonitor) {
				t.Helper()
				// With mount-point aggregation, / and /tmp (on same filesystem)
				// are grouped together. Only the mount point is validated.
				monitor.mu.RLock()
				rootValidated, rootExists := monitor.validatedPaths["/"]
				monitor.mu.RUnlock()

				assert.True(t, rootExists && rootValidated, "Root mount point should be validated")

				// Alert state uses mount point as key (not individual paths)
				monitor.mu.RLock()
				_, rootState := monitor.alertStates["disk|/"]
				monitor.mu.RUnlock()

				assert.True(t, rootState, "Alert state should exist for root mount point")
			},
		},
		{
			name:  "empty paths defaults to root",
			paths: []string{},
			checkFunc: func(t *testing.T, monitor *SystemMonitor) {
				t.Helper()
				// Verify that root path is validated
				monitor.mu.RLock()
				validated, exists := monitor.validatedPaths["/"]
				monitor.mu.RUnlock()

				assert.True(t, exists && validated, "Root path should be validated when paths is empty")
			},
		},
		{
			name:  "invalid path filtered during grouping",
			paths: []string{"/", "/this/path/does/not/exist"},
			checkFunc: func(t *testing.T, monitor *SystemMonitor) {
				t.Helper()
				// Invalid paths are filtered during mount grouping, not tracked
				// Only the valid root mount point should be validated
				monitor.mu.RLock()
				rootValidated, rootExists := monitor.validatedPaths["/"]
				// Invalid path should not be in validatedPaths at all (filtered during grouping)
				_, invalidExists := monitor.validatedPaths["/this/path/does/not/exist"]
				monitor.mu.RUnlock()

				assert.True(t, rootExists && rootValidated, "Root path should be validated")
				assert.False(t, invalidExists, "Invalid path should be filtered during grouping, not tracked")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

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
							Paths:    tt.paths,
						},
					},
				},
			}

			// Create monitor
			monitor := NewSystemMonitor(config)
			require.NotNil(t, monitor)

			// Test that paths are configured correctly
			if len(tt.paths) > 0 {
				// Note: NewSystemMonitor may add auto-detected paths
				for _, path := range tt.paths {
					assert.Contains(t, config.Realtime.Monitoring.Disk.Paths, path)
				}
			}

			// Run checkDisk unless test needs custom behavior
			if !tt.skipCheckDisk {
				monitor.checkDisk()
			}

			// Run test-specific checks
			tt.checkFunc(t, monitor)
		})
	}
}

func TestDiskMonitoringPathSpecificStates(t *testing.T) {
	t.Parallel()

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

func TestDiskMonitoringAggregation(t *testing.T) {
	t.Parallel()

	// Create config with paths that likely share the same mount point
	config := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Monitoring: conf.MonitoringSettings{
				Enabled:       true,
				CheckInterval: 1,
				Disk: conf.DiskThresholdSettings{
					Enabled:  true,
					Warning:  80.0,
					Critical: 90.0,
					Paths:    []string{"/", "/tmp", "/var"},
				},
			},
		},
	}

	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Run disk check
	monitor.checkDisk()

	// Verify that alert states are keyed by mount point, not individual paths
	monitor.mu.RLock()
	defer monitor.mu.RUnlock()

	// Count unique mount-based alert states
	mountStates := 0
	for key := range monitor.alertStates {
		if len(key) > 5 && key[:5] == "disk|" {
			mountStates++
		}
	}

	// Should have fewer or equal states than paths (aggregation)
	// On most systems, /, /tmp, /var are on same mount, so expect 1-2 states
	assert.LessOrEqual(t, mountStates, 3, "Should aggregate paths by mount point")
	assert.GreaterOrEqual(t, mountStates, 1, "Should have at least one mount point state")
}

func TestDiskMonitoringRecoveryPerPath(t *testing.T) {
	t.Parallel()

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
	// Note: This test cannot be run in parallel as it starts a goroutine
	// that could interfere with other tests if the monitor is not properly stopped

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

	// Use a channel to signal when the monitor has started
	started := make(chan struct{})

	// Wrap Start() to signal when it's running
	var wg sync.WaitGroup
	wg.Go(func() {
		monitor.Start()
		close(started)
	})

	// Wait for the monitor to start or timeout
	select {
	case <-started:
		// Monitor started
	case <-time.After(1 * time.Second):
		require.Fail(t, "Monitor failed to start within timeout")
	}

	// Give the monitor loop a chance to run at least once
	// Since CheckInterval is 1 second, wait slightly longer
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// Wait for one tick to ensure monitor is running
	<-ticker.C

	// Stop monitor
	monitor.Stop()

	// Wait for the goroutine to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Verify monitor stopped properly
	select {
	case <-done:
		// Monitor stopped successfully
	case <-time.After(2 * time.Second):
		require.Fail(t, "Monitor failed to stop within timeout")
	}

	// Verify context is cancelled
	select {
	case <-monitor.ctx.Done():
		// Context should be cancelled
	default:
		require.Fail(t, "Monitor context should be cancelled after Stop()")
	}
}
