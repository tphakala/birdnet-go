// system_test.go: Package api provides tests for API v2 system endpoints.

package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// setupSystemTestEnvironment creates a test environment for system API tests
func setupSystemTestEnvironment(t *testing.T) (*echo.Echo, *Controller) {
	t.Helper()

	e := echo.New()

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Source: "test-device",
			},
		},
	}

	controller := &Controller{
		Echo:     e,
		Group:    e.Group("/api/v2"),
		Settings: settings,
		logger:   log.New(io.Discard, "", 0),
	}

	return e, controller
}

// TestGetDeviceBaseName tests the getDeviceBaseName helper function
func TestGetDeviceBaseName(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "device-name-parsing")

	tests := []struct {
		name     string
		device   string
		expected string
	}{
		{
			name:     "Linux partition with number",
			device:   "/dev/sda1",
			expected: "sda",
		},
		{
			name:     "Linux partition with two digit number",
			device:   "/dev/sda12",
			expected: "sda",
		},
		{
			name:     "Linux device without partition",
			device:   "/dev/sda",
			expected: "sda",
		},
		{
			name:     "NVMe device with partition",
			device:   "/dev/nvme0n1p1",
			expected: "nvme0n1p",
		},
		{
			name:     "Simple device name",
			device:   "sda1",
			expected: "sda",
		},
		{
			name:     "Device name only letters",
			device:   "sda",
			expected: "sda",
		},
		{
			name:     "MMC device with partition",
			device:   "/dev/mmcblk0p1",
			expected: "mmcblk0p",
		},
		{
			name:     "Loop device",
			device:   "/dev/loop0",
			expected: "loop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := getDeviceBaseName(tt.device)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsRemoteFilesystem tests the isRemoteFilesystem helper function
func TestIsRemoteFilesystem(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "filesystem-detection")

	tests := []struct {
		name     string
		fstype   string
		expected bool
	}{
		{
			name:     "NFS filesystem",
			fstype:   "nfs",
			expected: true,
		},
		{
			name:     "NFS4 filesystem",
			fstype:   "nfs4",
			expected: true,
		},
		{
			name:     "CIFS filesystem",
			fstype:   "cifs",
			expected: true,
		},
		{
			name:     "SMBFS filesystem",
			fstype:   "smbfs",
			expected: true,
		},
		{
			name:     "SSHFS filesystem",
			fstype:   "sshfs",
			expected: true,
		},
		{
			name:     "FUSE SSHFS filesystem",
			fstype:   "fuse.sshfs",
			expected: true,
		},
		{
			name:     "AFS filesystem",
			fstype:   "afs",
			expected: true,
		},
		{
			name:     "9P filesystem",
			fstype:   "9p",
			expected: true,
		},
		{
			name:     "NCPFS filesystem",
			fstype:   "ncpfs",
			expected: true,
		},
		{
			name:     "EXT4 filesystem (local)",
			fstype:   "ext4",
			expected: false,
		},
		{
			name:     "XFS filesystem (local)",
			fstype:   "xfs",
			expected: false,
		},
		{
			name:     "NTFS filesystem (local)",
			fstype:   "ntfs",
			expected: false,
		},
		{
			name:     "Empty string",
			fstype:   "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isRemoteFilesystem(tt.fstype)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsReadOnlyMount tests the isReadOnlyMount helper function
func TestIsReadOnlyMount(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "mount-options")

	tests := []struct {
		name     string
		opts     []string
		expected bool
	}{
		{
			name:     "Read-only mount",
			opts:     []string{"ro", "noexec"},
			expected: true,
		},
		{
			name:     "Read-write mount",
			opts:     []string{"rw", "noexec"},
			expected: false,
		},
		{
			name:     "Empty options",
			opts:     []string{},
			expected: false,
		},
		{
			name:     "Nil options",
			opts:     nil,
			expected: false,
		},
		{
			name:     "Read-only as only option",
			opts:     []string{"ro"},
			expected: true,
		},
		{
			name:     "Similar but not ro",
			opts:     []string{"ronly", "rodata"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isReadOnlyMount(tt.opts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSkipFilesystem tests the skipFilesystem helper function
func TestSkipFilesystem(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "filesystem-filtering")

	tests := []struct {
		name     string
		fstype   string
		expected bool
	}{
		// System filesystems
		{name: "sysfs", fstype: "sysfs", expected: true},
		{name: "proc", fstype: "proc", expected: true},
		{name: "procfs", fstype: "procfs", expected: true},
		{name: "devfs", fstype: "devfs", expected: true},
		{name: "devtmpfs", fstype: "devtmpfs", expected: true},
		{name: "debugfs", fstype: "debugfs", expected: true},
		{name: "securityfs", fstype: "securityfs", expected: true},

		// Virtual filesystems
		{name: "fusectl", fstype: "fusectl", expected: true},
		{name: "overlay", fstype: "overlay", expected: true},

		// Temporary filesystems
		{name: "tmpfs", fstype: "tmpfs", expected: true},
		{name: "ramfs", fstype: "ramfs", expected: true},

		// Special filesystems
		{name: "devpts", fstype: "devpts", expected: true},
		{name: "cgroup", fstype: "cgroup", expected: true},
		{name: "bpf", fstype: "bpf", expected: true},

		// Real filesystems (should NOT be skipped)
		{name: "ext4", fstype: "ext4", expected: false},
		{name: "xfs", fstype: "xfs", expected: false},
		{name: "ntfs", fstype: "ntfs", expected: false},
		{name: "btrfs", fstype: "btrfs", expected: false},
		{name: "zfs", fstype: "zfs", expected: false},
		{name: "nfs", fstype: "nfs", expected: false},

		// Edge cases
		{name: "empty", fstype: "", expected: false},
		{name: "unknown", fstype: "unknown_fs", expected: false},

		// Prefix matching
		{name: "fuseblk (fuse prefix)", fstype: "fuseblk", expected: true},
		{name: "cgroupfs (cgroup prefix)", fstype: "cgroupfs", expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := skipFilesystem(tt.fstype)
			assert.Equal(t, tt.expected, result, "skipFilesystem(%q) should return %v", tt.fstype, tt.expected)
		})
	}
}

// TestMapProcessStatus tests the mapProcessStatus helper function
func TestMapProcessStatus(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "process-status")

	tests := []struct {
		name       string
		statusCode string
		expected   string
	}{
		{name: "Running", statusCode: "R", expected: "running"},
		{name: "Sleeping", statusCode: "S", expected: "sleeping"},
		{name: "Disk Sleep", statusCode: "D", expected: "disk sleep"},
		{name: "Zombie", statusCode: "Z", expected: "zombie"},
		{name: "Stopped", statusCode: "T", expected: "stopped"},
		{name: "Paging", statusCode: "W", expected: "paging"},
		{name: "Idle", statusCode: "I", expected: "idle"},
		{name: "Unknown code", statusCode: "X", expected: "x"},
		{name: "Empty", statusCode: "", expected: ""},
		{name: "Multi-char", statusCode: "RS", expected: "rs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := mapProcessStatus(tt.statusCode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetCachedCPUUsage tests the GetCachedCPUUsage function
func TestGetCachedCPUUsage(t *testing.T) {
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "cpu-cache")

	// The cache is initialized with [0], so we should get at least that
	result := GetCachedCPUUsage()
	require.NotNil(t, result, "CPU cache should not be nil")
	require.Len(t, result, 1, "CPU cache should have at least 1 value")

	// Verify it returns a copy (modifying result shouldn't affect cache)
	originalValue := result[0]
	result[0] = 999.0

	newResult := GetCachedCPUUsage()
	assert.InDelta(t, originalValue, newResult[0], 0.001, "Cache should return a copy, not the original slice")
}

// TestGetSystemInfo tests the GetSystemInfo endpoint
func TestGetSystemInfo(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "system-info")

	e, controller := setupSystemTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/info", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/system/info")

	err := controller.GetSystemInfo(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response SystemInfo
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify required fields are present
	assert.NotEmpty(t, response.Hostname, "Hostname should not be empty")
	assert.NotEmpty(t, response.Architecture, "Architecture should not be empty")
	assert.NotEmpty(t, response.OSDisplay, "OSDisplay should not be empty")
	assert.Positive(t, response.NumCPU, "NumCPU should be greater than 0")
	assert.NotZero(t, response.BootTime, "BootTime should not be zero")
	assert.NotZero(t, response.AppStart, "AppStart should not be zero")
}

// TestGetResourceInfo tests the GetResourceInfo endpoint
func TestGetResourceInfo(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "resource-info")

	e, controller := setupSystemTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/resources", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/system/resources")

	err := controller.GetResourceInfo(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response ResourceInfo
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify memory fields are present and reasonable
	assert.Positive(t, response.MemoryTotal, "MemoryTotal should be greater than 0")
	assert.LessOrEqual(t, response.MemoryUsed, response.MemoryTotal, "MemoryUsed should be <= MemoryTotal")
	assert.GreaterOrEqual(t, response.MemoryUsage, float64(0), "MemoryUsage should be >= 0")
	assert.LessOrEqual(t, response.MemoryUsage, float64(100), "MemoryUsage should be <= 100")

	// CPU usage should be within valid range
	assert.GreaterOrEqual(t, response.CPUUsage, float64(0), "CPUUsage should be >= 0")
	assert.LessOrEqual(t, response.CPUUsage, float64(100), "CPUUsage should be <= 100")

	// Process memory should be positive
	assert.GreaterOrEqual(t, response.ProcessMem, float64(0), "ProcessMem should be >= 0")
}

// TestGetDiskInfo tests the GetDiskInfo endpoint
func TestGetDiskInfo(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "disk-info")

	e, controller := setupSystemTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/disks", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/system/disks")

	err := controller.GetDiskInfo(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response []DiskInfo
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should have at least one disk on most systems
	// Note: In containers this might be empty, so we just verify the response is valid JSON array
	assert.NotNil(t, response, "Response should be a valid array")

	// If disks are present, verify their structure
	for _, disk := range response {
		assert.NotEmpty(t, disk.Mountpoint, "Disk mountpoint should not be empty")
		assert.NotEmpty(t, disk.Fstype, "Disk fstype should not be empty")
		assert.GreaterOrEqual(t, disk.UsagePerc, float64(0), "UsagePerc should be >= 0")
		assert.LessOrEqual(t, disk.UsagePerc, float64(100), "UsagePerc should be <= 100")
	}
}

// TestGetProcessInfo tests the GetProcessInfo endpoint
func TestGetProcessInfo(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "process-info")

	e, controller := setupSystemTestEnvironment(t)

	// Test default behavior (current process and children only)
	t.Run("Default filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/processes", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/processes")

		err := controller.GetProcessInfo(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response []ProcessInfo
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Should have at least the current process
		require.GreaterOrEqual(t, len(response), 1, "Should have at least current process")

		// Verify the current process is in the list
		foundCurrentProcess := false
		for _, proc := range response {
			if proc.PID > 0 {
				foundCurrentProcess = true
				assert.NotEmpty(t, proc.Name, "Process name should not be empty")
				assert.NotEmpty(t, proc.Status, "Process status should not be empty")
				break
			}
		}
		assert.True(t, foundCurrentProcess, "Should find at least one valid process")
	})

	// Test with all=true parameter
	t.Run("All processes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/processes?all=true", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/processes")

		err := controller.GetProcessInfo(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response []ProcessInfo
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// With all=true, should have more processes
		assert.Greater(t, len(response), 1, "Should have multiple processes with all=true")
	})
}

// TestGetEqualizerConfig tests the GetEqualizerConfig endpoint
func TestGetEqualizerConfig(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "equalizer-config")

	e, controller := setupSystemTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/audio/equalizer/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/system/audio/equalizer/config")

	err := controller.GetEqualizerConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify cache headers are set
	assert.Contains(t, rec.Header().Get("Cache-Control"), "public", "Should have public cache header")

	// Response should be valid JSON
	var response any
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")
}

// TestGetActiveAudioDevice tests the GetActiveAudioDevice endpoint
func TestGetActiveAudioDevice(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "audio-device")

	t.Run("With configured device", func(t *testing.T) {
		e, controller := setupSystemTestEnvironment(t)
		// Settings already have a device configured

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/audio/active", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/audio/active")

		err := controller.GetActiveAudioDevice(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response map[string]any
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Should have device info
		assert.Contains(t, response, "device", "Response should contain device")
		assert.Contains(t, response, "active", "Response should contain active flag")
	})

	t.Run("No device configured", func(t *testing.T) {
		e := echo.New()
		settings := &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Source: "", // No device configured
				},
			},
		}
		controller := &Controller{
			Echo:     e,
			Group:    e.Group("/api/v2"),
			Settings: settings,
			logger:   log.New(io.Discard, "", 0),
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/audio/active", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/audio/active")

		err := controller.GetActiveAudioDevice(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response map[string]any
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Should indicate no device active
		assert.False(t, response["active"].(bool), "Should not be active when no device configured")
		assert.Contains(t, response["message"], "No audio device", "Should have appropriate message")
	})
}

// TestGetJobQueueStats tests the GetJobQueueStats endpoint error handling
func TestGetJobQueueStats(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "job-queue")

	t.Run("No processor in context", func(t *testing.T) {
		e, controller := setupSystemTestEnvironment(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/jobs", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/jobs")
		// Note: Not setting processor in context

		err := controller.GetJobQueueStats(c)
		require.NoError(t, err) // HandleError returns nil after writing response
		assert.Equal(t, http.StatusInternalServerError, rec.Code)

		var response map[string]any
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Contains(t, response["message"], "Processor not available")
	})

	t.Run("Invalid processor type", func(t *testing.T) {
		e, controller := setupSystemTestEnvironment(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/jobs", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/jobs")
		c.Set("processor", "not-a-processor") // Wrong type

		err := controller.GetJobQueueStats(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)

		var response map[string]any
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Contains(t, response["message"], "Invalid processor type")
	})
}

// TestGetSystemCPUTemperature tests the GetSystemCPUTemperature endpoint
func TestGetSystemCPUTemperature(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "cpu-temperature")

	e, controller := setupSystemTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/temperature/cpu", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/system/temperature/cpu")

	err := controller.GetSystemCPUTemperature(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response SystemTemperature
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Response should always have these fields
	assert.NotEmpty(t, response.Message, "Message should not be empty")

	// If temperature is available, validate it
	if response.IsAvailable {
		assert.GreaterOrEqual(t, response.Celsius, float64(0), "Temperature should be >= 0")
		assert.LessOrEqual(t, response.Celsius, float64(100), "Temperature should be <= 100")
		assert.NotEmpty(t, response.SensorDetails, "SensorDetails should be set when available")
	}
}

// TestDiskInfoStructure validates the DiskInfo struct fields
func TestDiskInfoStructure(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "disk-info-struct")

	// Create a sample DiskInfo and verify JSON serialization
	disk := DiskInfo{
		Device:          "/dev/sda1",
		Mountpoint:      "/",
		Fstype:          "ext4",
		Total:           1000000000,
		Used:            500000000,
		Free:            500000000,
		UsagePerc:       50.0,
		InodesTotal:     1000000,
		InodesUsed:      100000,
		InodesFree:      900000,
		InodesUsagePerc: 10.0,
		ReadBytes:       123456,
		WriteBytes:      654321,
		ReadCount:       1000,
		WriteCount:      500,
		ReadTime:        1234,
		WriteTime:       5678,
		IOBusyPerc:      5.5,
		IOTime:          6912,
		IsRemote:        false,
		IsReadOnly:      false,
	}

	data, err := json.Marshal(disk)
	require.NoError(t, err)

	var parsed DiskInfo
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, disk.Device, parsed.Device)
	assert.Equal(t, disk.Mountpoint, parsed.Mountpoint)
	assert.Equal(t, disk.Fstype, parsed.Fstype)
	assert.Equal(t, disk.Total, parsed.Total)
	assert.Equal(t, disk.Used, parsed.Used)
	assert.Equal(t, disk.Free, parsed.Free)
	assert.InDelta(t, disk.UsagePerc, parsed.UsagePerc, 0.01)
}

// TestSystemInfoStructure validates the SystemInfo struct fields
func TestSystemInfoStructure(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "system-info-struct")

	info := SystemInfo{
		Hostname:      "test-host",
		PlatformVer:   "22.04",
		KernelVersion: "5.15.0",
		UpTime:        86400,
		NumCPU:        4,
		SystemModel:   "Raspberry Pi 4",
		TimeZone:      "UTC",
		OSDisplay:     "Ubuntu Linux",
		Architecture:  "amd64",
	}

	data, err := json.Marshal(info)
	require.NoError(t, err)

	var parsed SystemInfo
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, info.Hostname, parsed.Hostname)
	assert.Equal(t, info.Architecture, parsed.Architecture)
	assert.Equal(t, info.NumCPU, parsed.NumCPU)
	assert.Equal(t, info.OSDisplay, parsed.OSDisplay)
}

// TestResourceInfoStructure validates the ResourceInfo struct fields
func TestResourceInfoStructure(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "resource-info-struct")

	resource := ResourceInfo{
		CPUUsage:        25.5,
		MemoryTotal:     8000000000,
		MemoryUsed:      4000000000,
		MemoryFree:      2000000000,
		MemoryAvailable: 3000000000,
		MemoryBuffers:   500000000,
		MemoryCached:    500000000,
		MemoryUsage:     50.0,
		SwapTotal:       4000000000,
		SwapUsed:        1000000000,
		SwapFree:        3000000000,
		SwapUsage:       25.0,
		ProcessMem:      256.5,
		ProcessCPU:      5.5,
	}

	data, err := json.Marshal(resource)
	require.NoError(t, err)

	var parsed ResourceInfo
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.InDelta(t, resource.CPUUsage, parsed.CPUUsage, 0.01)
	assert.Equal(t, resource.MemoryTotal, parsed.MemoryTotal)
	assert.InDelta(t, resource.MemoryUsage, parsed.MemoryUsage, 0.01)
}

// TestProcessInfoStructure validates the ProcessInfo struct fields
func TestProcessInfoStructure(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "process-info-struct")

	proc := ProcessInfo{
		PID:    12345,
		Name:   "birdnet-go",
		Status: "running",
		CPU:    2.5,
		Memory: 104857600, // 100 MB
		Uptime: 3600,
	}

	data, err := json.Marshal(proc)
	require.NoError(t, err)

	var parsed ProcessInfo
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, proc.PID, parsed.PID)
	assert.Equal(t, proc.Name, parsed.Name)
	assert.Equal(t, proc.Status, parsed.Status)
	assert.InDelta(t, proc.CPU, parsed.CPU, 0.01)
	assert.Equal(t, proc.Memory, parsed.Memory)
	assert.Equal(t, proc.Uptime, parsed.Uptime)
}

// TestSystemTemperatureStructure validates the SystemTemperature struct fields
func TestSystemTemperatureStructure(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "temperature-struct")

	temp := SystemTemperature{
		Celsius:       45.5,
		IsAvailable:   true,
		SensorDetails: "Source: thermal_zone0, Type: cpu-thermal",
		Message:       "CPU temperature retrieved successfully.",
	}

	data, err := json.Marshal(temp)
	require.NoError(t, err)

	var parsed SystemTemperature
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.InDelta(t, temp.Celsius, parsed.Celsius, 0.01)
	assert.Equal(t, temp.IsAvailable, parsed.IsAvailable)
	assert.Equal(t, temp.SensorDetails, parsed.SensorDetails)
	assert.Equal(t, temp.Message, parsed.Message)
}
