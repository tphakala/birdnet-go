package monitor

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestGetCriticalPaths(t *testing.T) {
	tests := []struct {
		name         string
		setupConfig  func() *conf.Settings
		wantContains []string
		minPaths     int
	}{
		{
			name: "SQLite enabled with relative path",
			setupConfig: func() *conf.Settings {
				s := &conf.Settings{}
				s.Output.SQLite.Enabled = true
				s.Output.SQLite.Path = "birdnet.db"
				return s
			},
			wantContains: []string{"/"},
			minPaths:     2, // root + current directory
		},
		{
			name: "SQLite enabled with absolute path",
			setupConfig: func() *conf.Settings {
				s := &conf.Settings{}
				s.Output.SQLite.Enabled = true
				s.Output.SQLite.Path = "/var/lib/birdnet/birdnet.db"
				return s
			},
			wantContains: []string{"/", "/var/lib/birdnet"},
			minPaths:     2,
		},
		{
			name: "Audio export enabled",
			setupConfig: func() *conf.Settings {
				s := &conf.Settings{}
				s.Realtime.Audio.Export.Enabled = true
				s.Realtime.Audio.Export.Path = "clips/"
				return s
			},
			wantContains: []string{"/"},
			minPaths:     2, // root + clips path
		},
		{
			name: "Both SQLite and audio export with same parent",
			setupConfig: func() *conf.Settings {
				s := &conf.Settings{}
				s.Output.SQLite.Enabled = true
				s.Output.SQLite.Path = "data/birdnet.db"
				s.Realtime.Audio.Export.Enabled = true
				s.Realtime.Audio.Export.Path = "data/clips/"
				return s
			},
			wantContains: []string{"/"},
			minPaths:     2, // root + data (deduped)
		},
		{
			name: "Nothing enabled",
			setupConfig: func() *conf.Settings {
				return &conf.Settings{}
			},
			wantContains: []string{"/"},
			minPaths:     1, // just root
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := tt.setupConfig()
			paths := GetCriticalPaths(settings)

			// Check minimum path count
			assert.GreaterOrEqual(t, len(paths), tt.minPaths, "Should have at least %d paths", tt.minPaths)

			// Check required paths are present
			for _, want := range tt.wantContains {
				assert.Contains(t, paths, want, "Should contain path: %s", want)
			}

			// Verify all paths are absolute and clean
			for _, path := range paths {
				assert.True(t, filepath.IsAbs(path), "Path should be absolute: %s", path)
				assert.Equal(t, filepath.Clean(path), path, "Path should be clean: %s", path)
			}

			// Verify no duplicates
			seen := make(map[string]bool)
			for _, path := range paths {
				assert.False(t, seen[path], "Found duplicate path: %s", path)
				seen[path] = true
			}
		})
	}
}

func TestDeduplicatePaths(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  int
	}{
		{
			name:  "No duplicates",
			input: []string{"/", "/home", "/var"},
			want:  3,
		},
		{
			name:  "Exact duplicates",
			input: []string{"/home", "/var", "/home", "/var"},
			want:  2,
		},
		{
			name:  "Different representations of same path",
			input: []string{"/home/", "/home", "/home/./"},
			want:  1, // All resolve to /home
		},
		{
			name:  "Empty and dot paths filtered",
			input: []string{"", ".", "/", "/home"},
			want:  2, // Only / and /home remain
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deduplicatePaths(tt.input)
			assert.Len(t, result, tt.want, "Unexpected number of deduplicated paths")

			// Verify no duplicates in result
			seen := make(map[string]bool)
			for _, path := range result {
				assert.False(t, seen[path], "Found duplicate path: %s", path)
				seen[path] = true
			}
		})
	}
}

func TestMergePaths(t *testing.T) {
	configured := []string{"/custom", "/data"}
	critical := []string{"/", "/data", "/config"}

	merged := mergePaths(configured, critical)

	// Should contain all unique paths
	expectedPaths := []string{"/", "/custom", "/data", "/config"}
	for _, expected := range expectedPaths {
		assert.Contains(t, merged, expected, "Should contain %s", expected)
	}

	// Should not have duplicates
	assert.Equal(t, 4, len(merged), "Should have exactly 4 unique paths")
}

func TestSystemMonitorIntegration(t *testing.T) {
	// Create a test configuration
	config := &conf.Settings{}
	config.Realtime.Monitoring.Enabled = true
	config.Realtime.Monitoring.CheckInterval = 1
	config.Realtime.Monitoring.Disk.Enabled = true
	config.Realtime.Monitoring.Disk.Warning = 80.0
	config.Realtime.Monitoring.Disk.Critical = 90.0
	config.Realtime.Monitoring.Disk.Paths = []string{"/custom"}
	
	// Enable audio export
	config.Realtime.Audio.Export.Enabled = true
	config.Realtime.Audio.Export.Path = "clips/"
	
	// Enable SQLite
	config.Output.SQLite.Enabled = true
	config.Output.SQLite.Path = "birdnet.db"

	// Create monitor (this will auto-append critical paths)
	monitor := NewSystemMonitor(config)
	require.NotNil(t, monitor)

	// Check that paths include both user-configured and critical paths
	paths := config.Realtime.Monitoring.Disk.Paths
	assert.Contains(t, paths, "/", "Should contain root")
	assert.Contains(t, paths, "/custom", "Should contain user-configured path")
	
	// Should have at least 3 paths (/, /custom, and at least one critical path)
	assert.GreaterOrEqual(t, len(paths), 3, "Should have user path plus critical paths")
}

func TestGetMonitoringPathsInfo(t *testing.T) {
	settings := &conf.Settings{}
	settings.Realtime.Monitoring.Disk.Paths = []string{"/custom", "/data"}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = "birdnet.db"
	settings.Realtime.Audio.Export.Enabled = true
	settings.Realtime.Audio.Export.Path = "clips/"

	configured, autoDetected, merged := GetMonitoringPathsInfo(settings)

	// Check configured paths
	assert.Equal(t, []string{"/custom", "/data"}, configured)
	
	// Check auto-detected paths contain at least root
	assert.Contains(t, autoDetected, "/")
	assert.GreaterOrEqual(t, len(autoDetected), 2) // At least root + one critical path
	
	// Check merged paths contain both configured and auto-detected
	assert.Contains(t, merged, "/custom")
	assert.Contains(t, merged, "/data")
	assert.Contains(t, merged, "/")
	
	// Verify no duplicates in merged
	seen := make(map[string]bool)
	for _, path := range merged {
		assert.False(t, seen[path], "Found duplicate in merged paths: %s", path)
		seen[path] = true
	}
}

func TestGetMonitoredPaths(t *testing.T) {
	config := &conf.Settings{}
	config.Realtime.Monitoring.Disk.Enabled = true
	config.Realtime.Monitoring.Disk.Paths = []string{"/", "/home"}
	
	monitor := &SystemMonitor{
		config: config,
		logger: logger,
	}
	
	paths := monitor.GetMonitoredPaths()
	assert.Equal(t, []string{"/", "/home"}, paths)
	
	// Test with disk monitoring disabled
	config.Realtime.Monitoring.Disk.Enabled = false
	paths = monitor.GetMonitoredPaths()
	assert.Nil(t, paths)
}