// Package checks provides concrete health check implementations for BirdNET-Go.
package checks

import (
	"context"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/health"
)

// DiskSpaceCheck verifies that monitored filesystem paths have sufficient free space.
type DiskSpaceCheck struct {
	paths []string
}

// NewDiskSpaceCheck creates a DiskSpaceCheck that monitors the given filesystem paths.
func NewDiskSpaceCheck(paths []string) *DiskSpaceCheck {
	return &DiskSpaceCheck{paths: paths}
}

// Name returns the check identifier.
func (c *DiskSpaceCheck) Name() string { return "disk_space" }

// Category returns the system category.
func (c *DiskSpaceCheck) Category() health.Category { return health.CategorySystem }

// Run executes the disk space check against all configured paths.
func (c *DiskSpaceCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if len(c.paths) == 0 {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusSkipped,
			Message:    "No paths configured",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	type pathInfo struct {
		Path    string  `json:"path"`
		Total   uint64  `json:"total_bytes"`
		Used    uint64  `json:"used_bytes"`
		Free    uint64  `json:"free_bytes"`
		Percent float64 `json:"percent_used"`
	}

	worst := health.StatusHealthy
	var messages []string
	pathDetails := make([]pathInfo, 0, len(c.paths))

	for _, p := range c.paths {
		usage, err := disk.Usage(p)
		if err != nil {
			if health.StatusCritical != worst {
				worst = health.StatusWarning
			}
			messages = append(messages, fmt.Sprintf("%s: unable to read (%v)", p, err))
			continue
		}

		info := pathInfo{
			Path:    p,
			Total:   usage.Total,
			Used:    usage.Used,
			Free:    usage.Free,
			Percent: usage.UsedPercent,
		}
		pathDetails = append(pathDetails, info)

		freePercent := 100 - usage.UsedPercent
		switch {
		case freePercent < 5:
			worst = health.StatusCritical
			messages = append(messages, fmt.Sprintf("%s: critical (%.1f%% free)", p, freePercent))
		case freePercent < 10:
			if health.StatusCritical != worst {
				worst = health.StatusWarning
			}
			messages = append(messages, fmt.Sprintf("%s: low (%.1f%% free)", p, freePercent))
		}
	}

	msg := "Disk space OK"
	if len(messages) > 0 {
		msg = messages[0]
		if len(messages) > 1 {
			msg = fmt.Sprintf("%s (and %d more)", msg, len(messages)-1)
		}
	}

	details := map[string]any{"paths": pathDetails}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     worst,
		Message:    msg,
		Details:    details,
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// MemoryCheck verifies that the system has sufficient available memory.
type MemoryCheck struct{}

// NewMemoryCheck creates a MemoryCheck.
func NewMemoryCheck() *MemoryCheck { return &MemoryCheck{} }

// Name returns the check identifier.
func (c *MemoryCheck) Name() string { return "memory" }

// Category returns the system category.
func (c *MemoryCheck) Category() health.Category { return health.CategorySystem }

// Run executes the memory check.
func (c *MemoryCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	vm, err := mem.VirtualMemory()
	if err != nil {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    fmt.Sprintf("Unable to read memory stats: %v", err),
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	const (
		warnPercent  = 85.0
		critPercent  = 95.0
		warnMinBytes = 256 * 1024 * 1024 // 256 MB
		critMinBytes = 128 * 1024 * 1024 // 128 MB
	)

	status := health.StatusHealthy
	msg := "Memory OK"

	switch {
	case vm.UsedPercent >= critPercent || vm.Available < critMinBytes:
		status = health.StatusCritical
		msg = fmt.Sprintf("Memory critical: %.1f%% used, %d MB available",
			vm.UsedPercent, vm.Available/1024/1024)
	case vm.UsedPercent >= warnPercent || vm.Available < warnMinBytes:
		status = health.StatusWarning
		msg = fmt.Sprintf("Memory warning: %.1f%% used, %d MB available",
			vm.UsedPercent, vm.Available/1024/1024)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"total_bytes":     vm.Total,
			"used_bytes":      vm.Used,
			"available_bytes": vm.Available,
			"percent_used":    vm.UsedPercent,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// CPULoadCheck verifies that CPU utilization is within acceptable bounds.
type CPULoadCheck struct {
	getCPU func() []float64
}

// NewCPULoadCheck creates a CPULoadCheck that uses getCPU to obtain per-core utilization values.
func NewCPULoadCheck(getCPU func() []float64) *CPULoadCheck {
	return &CPULoadCheck{getCPU: getCPU}
}

// Name returns the check identifier.
func (c *CPULoadCheck) Name() string { return "cpu_load" }

// Category returns the system category.
func (c *CPULoadCheck) Category() health.Category { return health.CategorySystem }

// Run executes the CPU load check.
func (c *CPULoadCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getCPU == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	percents := c.getCPU()
	if len(percents) == 0 {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    "No CPU data available",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	var sum float64
	for _, p := range percents {
		sum += p
	}
	avg := sum / float64(len(percents))

	const warnPercent = 80.0
	const critPercent = 95.0

	status := health.StatusHealthy
	msg := fmt.Sprintf("CPU load OK (avg %.1f%%)", avg)

	switch {
	case avg >= critPercent:
		status = health.StatusCritical
		msg = fmt.Sprintf("CPU overloaded: avg %.1f%%", avg)
	case avg >= warnPercent:
		status = health.StatusWarning
		msg = fmt.Sprintf("CPU load high: avg %.1f%%", avg)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"per_core_percent": percents,
			"average_percent":  avg,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// TemperatureCheck verifies that the system temperature is within safe bounds.
type TemperatureCheck struct {
	getTemp func() (float64, error)
}

// NewTemperatureCheck creates a TemperatureCheck that uses getTemp to obtain the current temperature in Celsius.
func NewTemperatureCheck(getTemp func() (float64, error)) *TemperatureCheck {
	return &TemperatureCheck{getTemp: getTemp}
}

// Name returns the check identifier.
func (c *TemperatureCheck) Name() string { return "temperature" }

// Category returns the system category.
func (c *TemperatureCheck) Category() health.Category { return health.CategorySystem }

// Run executes the temperature check.
func (c *TemperatureCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getTemp == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	tempC, err := c.getTemp()
	if err != nil {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    fmt.Sprintf("Temperature unavailable: %v", err),
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	const warnTemp = 75.0
	const critTemp = 85.0

	status := health.StatusHealthy
	msg := fmt.Sprintf("Temperature OK (%.1f C)", tempC)

	switch {
	case tempC >= critTemp:
		status = health.StatusCritical
		msg = fmt.Sprintf("Temperature critical: %.1f C", tempC)
	case tempC >= warnTemp:
		status = health.StatusWarning
		msg = fmt.Sprintf("Temperature high: %.1f C", tempC)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"temperature_c": tempC,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// UptimeCheck reports concerns when the process has recently started, which may
// indicate a crash-restart loop.
type UptimeCheck struct {
	startTime *time.Time
}

// NewUptimeCheck creates an UptimeCheck using the given process start time pointer.
func NewUptimeCheck(startTime *time.Time) *UptimeCheck {
	return &UptimeCheck{startTime: startTime}
}

// Name returns the check identifier.
func (c *UptimeCheck) Name() string { return "uptime" }

// Category returns the system category.
func (c *UptimeCheck) Category() health.Category { return health.CategorySystem }

// Run executes the uptime check.
func (c *UptimeCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.startTime == nil {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    "Start time not available",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	uptime := time.Since(*c.startTime)

	const critThreshold = 5 * time.Minute
	const warnThreshold = time.Hour

	status := health.StatusHealthy
	msg := fmt.Sprintf("Uptime OK (%.0f seconds)", uptime.Seconds())

	switch {
	case uptime < critThreshold:
		status = health.StatusCritical
		msg = fmt.Sprintf("Process started very recently (%.0f seconds ago), possible crash loop", uptime.Seconds())
	case uptime < warnThreshold:
		status = health.StatusWarning
		msg = fmt.Sprintf("Process started recently (%.0f seconds ago)", uptime.Seconds())
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"uptime_seconds": uptime.Seconds(),
			"started_at":     c.startTime.Format(time.RFC3339),
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
