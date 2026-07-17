// system_test.go: system-domain endpoint tests (extracted from package api).
package system

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/restart"
)

// setupSystemTestEnvironment creates a test environment for system API tests
func setupSystemTestEnvironment(t *testing.T) (*echo.Echo, *Handler) {
	t.Helper()

	e := echo.New()

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Source: "test-device",
			},
		},
	}

	controller := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")}}
	controller.Settings.Store(settings)

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

// TestNormalizeProcessCPU verifies per-process CPU is normalized to a share of
// total system capacity and clamped to [0, 100].
func TestNormalizeProcessCPU(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "process-info")

	numCPU := float64(runtime.NumCPU())

	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "zero stays zero", in: 0, want: 0},
		{name: "negative clamps to zero", in: -10, want: 0},
		{name: "single core share", in: numCPU * 25, want: 25},
		{name: "full machine", in: numCPU * 100, want: 100},
		{name: "over-full clamps to 100", in: numCPU * 250, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeProcessCPU(tt.in)
			assert.InDelta(t, tt.want, got, 0.001)
			assert.GreaterOrEqual(t, got, float64(0), "result must be >= 0")
			assert.LessOrEqual(t, got, float64(maxPercentage), "result must be <= 100")
		})
	}
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

// TestGetJobQueueStats tests the GetJobQueueStats endpoint error handling
func TestGetJobQueueStats(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "job-queue")

	t.Run("Nil processor", func(t *testing.T) {
		e, controller := setupSystemTestEnvironment(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/jobs", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/jobs")

		err := controller.GetJobQueueStats(c)
		require.NoError(t, err) // HandleError returns nil after writing response
		assert.Equal(t, http.StatusInternalServerError, rec.Code)

		var response map[string]any
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Contains(t, response["message"], "Processor not available")
	})

	t.Run("Processor with job queue returns stats", func(t *testing.T) {
		e, controller := setupSystemTestEnvironment(t)

		// Create a real processor with a job queue
		jq := jobqueue.NewJobQueue()
		controller.Processor = &processor.Processor{
			JobQueue: jq,
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/jobs", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/jobs")

		err := controller.GetJobQueueStats(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var statsMap map[string]any
		err = json.Unmarshal(rec.Body.Bytes(), &statsMap)
		require.NoError(t, err)
		assert.Contains(t, statsMap, "queue", "response should contain job queue stats")
	})

	t.Run("Processor without job queue", func(t *testing.T) {
		e, controller := setupSystemTestEnvironment(t)

		controller.Processor = &processor.Processor{}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/jobs", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/jobs")

		err := controller.GetJobQueueStats(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)

		var response map[string]any
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Contains(t, response["message"], "Job queue not available")
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

// TestGetNetworkInterfaces tests the network interfaces endpoint
func TestGetNetworkInterfaces(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "network-interfaces")

	e, controller := setupSystemTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/network-interfaces", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := controller.GetNetworkInterfaces(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response struct {
		Interfaces []struct {
			Address string `json:"address"`
			Name    string `json:"name"`
			Label   string `json:"label"`
			Status  string `json:"status"`
		} `json:"interfaces"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should always have at least the wildcard entry
	require.NotEmpty(t, response.Interfaces, "should have at least one interface")

	// First entry should always be the all-interfaces wildcard
	assert.Equal(t, "0.0.0.0", response.Interfaces[0].Address)
	assert.Equal(t, "all", response.Interfaces[0].Name)
	assert.Equal(t, "up", response.Interfaces[0].Status)

	// Should contain loopback (status may vary in restricted CI environments)
	var hasLoopback bool
	for _, iface := range response.Interfaces {
		if iface.Address == "127.0.0.1" {
			hasLoopback = true
			assert.Contains(t, []string{"up", "down"}, iface.Status,
				"loopback status should be valid")
			break
		}
	}
	assert.True(t, hasLoopback, "should include loopback interface")

	// All entries should have non-empty address and name
	for _, iface := range response.Interfaces {
		assert.NotEmpty(t, iface.Address, "address should not be empty")
		assert.NotEmpty(t, iface.Name, "name should not be empty")
		assert.Contains(t, []string{"up", "down"}, iface.Status, "status should be up or down")
	}

	// No duplicate addresses
	seen := make(map[string]bool)
	for _, iface := range response.Interfaces {
		assert.False(t, seen[iface.Address], "duplicate address: %s", iface.Address)
		seen[iface.Address] = true
	}
}

// TestGetRestartStatus tests the GetRestartStatus endpoint with no pending restart.
func TestGetRestartStatus(t *testing.T) {
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "restart-status")

	e, controller := setupSystemTestEnvironment(t)
	restart.Reset()
	t.Cleanup(restart.Reset)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/restart-status", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/system/restart-status")

	require.NoError(t, controller.GetRestartStatus(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	var result RestartStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	// In test environment, shutdownRequester is not wired, so restart is not available
	assert.False(t, result.BinaryRestartAvailable)
	assert.False(t, result.RestartRequired)
	assert.Empty(t, result.RestartReasons)
}

// TestGetRestartStatusWithPendingRestart tests the endpoint when a restart is pending.
func TestGetRestartStatusWithPendingRestart(t *testing.T) {
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "restart-status")

	e, controller := setupSystemTestEnvironment(t)
	restart.Reset()
	t.Cleanup(restart.Reset)

	restart.MarkRestartRequired("Port changed")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/restart-status", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, controller.GetRestartStatus(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	var result RestartStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.True(t, result.RestartRequired)
	assert.Equal(t, []string{"Port changed"}, result.RestartReasons)
}

// selfProcessForTest returns a real *process.Process for the running test binary.
// The CPU sampler needs a live PID whose create time the OS will actually report.
func selfProcessForTest(t *testing.T) (proc *process.Process, createTime int64) {
	t.Helper()
	proc, err := process.NewProcess(int32(os.Getpid()))
	require.NoError(t, err)
	createTime, err = proc.CreateTime()
	require.NoError(t, err)
	return proc, createTime
}

// The process table lists processes afresh on every request, and a freshly listed
// instance's CPUPercent() is the lifetime average since the process started. Interval
// usage requires diffing against a sample the *same* instance stored earlier, so the
// handler must retain its own instance and ignore the caller's.
func TestProcessCPUPercent_RetainsSamplerAcrossRequests(t *testing.T) {
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "process-info")

	c := &Handler{}
	first, createTime := selfProcessForTest(t)

	// First sight of the PID has nothing to diff against, so it primes and reports 0.
	got, err := c.processCPUPercent(first, createTime)
	require.NoError(t, err)
	assert.InDelta(t, 0.0, got, 0.001, "first sample should prime rather than report a value")
	require.Contains(t, c.procSamples, first.Pid)
	assert.Same(t, first, c.procSamples[first.Pid].proc)

	// A later request hands over a different instance for the same process; the retained
	// sampler must survive, or every request would re-prime and report 0 forever.
	second, _ := selfProcessForTest(t)
	require.NotSame(t, first, second, "each listing builds a new instance")

	_, err = c.processCPUPercent(second, createTime)
	require.NoError(t, err)
	assert.Same(t, first, c.procSamples[first.Pid].proc, "retained sampler must not be replaced")
}

// PIDs are recycled. A retained sampler's history belongs to the dead process, so
// diffing a new process against it would report nonsense; the create time is what
// distinguishes them.
func TestProcessCPUPercent_ReplacesRecycledPID(t *testing.T) {
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "process-info")

	c := &Handler{}
	p, createTime := selfProcessForTest(t)

	stale := &process.Process{Pid: p.Pid}
	c.procSamples = map[int32]*procSample{
		p.Pid: {proc: stale, createTime: createTime - 1000},
	}

	got, err := c.processCPUPercent(p, createTime)
	require.NoError(t, err)

	assert.NotSame(t, stale, c.procSamples[p.Pid].proc, "stale sampler must be discarded")
	assert.Same(t, p, c.procSamples[p.Pid].proc)
	assert.Equal(t, createTime, c.procSamples[p.Pid].createTime)
	assert.InDelta(t, 0.0, got, 0.001, "a replaced entry re-primes")
}

// Without pruning the map would retain an entry for every process that ever ran.
func TestPruneProcSamples_DropsDeadPIDs(t *testing.T) {
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "process-info")

	live, createTime := selfProcessForTest(t)
	const deadPID int32 = 999999

	c := &Handler{
		procSamples: map[int32]*procSample{
			live.Pid: {proc: live, createTime: createTime},
			deadPID:  {proc: &process.Process{Pid: deadPID}, createTime: 1},
		},
	}

	c.pruneProcSamples([]*process.Process{live})

	assert.Contains(t, c.procSamples, live.Pid, "a running process keeps its sampler")
	assert.NotContains(t, c.procSamples, deadPID, "a dead PID must not be retained")
}

// A process hidden by the table's relevance filter is still running, so pruning against
// the full listing must not evict it — otherwise it would re-prime and report 0 on
// every request that later shows it.
func TestPruneProcSamples_KeepsFilteredButLiveProcesses(t *testing.T) {
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "process-info")

	live, createTime := selfProcessForTest(t)
	other := &process.Process{Pid: live.Pid + 1}

	c := &Handler{
		procSamples: map[int32]*procSample{
			live.Pid:  {proc: live, createTime: createTime},
			other.Pid: {proc: other, createTime: 1},
		},
	}

	// Both are in the full listing, even though the table may only render one.
	c.pruneProcSamples([]*process.Process{live, other})

	assert.Contains(t, c.procSamples, live.Pid)
	assert.Contains(t, c.procSamples, other.Pid)
}
