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
func (c *DiskSpaceCheck) Run(ctx context.Context) health.Result {
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
		usage, err := disk.UsageWithContext(ctx, p)
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
func (c *MemoryCheck) Run(ctx context.Context) health.Result {
	start := time.Now()

	vm, err := mem.VirtualMemoryWithContext(ctx)
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

// Temperature display units. These mirror the dashboard "temperatureunit"
// setting in internal/conf (kept as local constants so this package does not
// depend on conf just to compare a string).
const (
	tempUnitCelsius    = "celsius"
	tempUnitFahrenheit = "fahrenheit"
)

// Celsius-to-Fahrenheit conversion factors.
const (
	fahrenheitScale  = 9.0 / 5.0
	fahrenheitOffset = 32.0
)

// TemperatureCheck verifies that the system temperature is within safe bounds.
type TemperatureCheck struct {
	getTemp func() (float64, error)
	getUnit func() string
}

// NewTemperatureCheck creates a TemperatureCheck that uses getTemp to obtain the
// current temperature in Celsius. getUnit returns the configured display unit
// ("celsius" or "fahrenheit") and is read per Run so a live settings change is
// reflected without a restart; a nil getUnit displays Celsius.
func NewTemperatureCheck(getTemp func() (float64, error), getUnit func() string) *TemperatureCheck {
	return &TemperatureCheck{getTemp: getTemp, getUnit: getUnit}
}

// displayTemperature converts a Celsius reading to the configured display unit,
// returning the converted value and its symbol. Warn/critical thresholds are
// always compared in Celsius; only the displayed value is converted.
func (c *TemperatureCheck) displayTemperature(celsius float64) (value float64, symbol string) {
	unit := tempUnitCelsius
	if c.getUnit != nil {
		unit = c.getUnit()
	}
	if unit == tempUnitFahrenheit {
		return celsius*fahrenheitScale + fahrenheitOffset, "F"
	}
	return celsius, "C"
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

	// Thresholds are physical constants compared in Celsius; only the displayed
	// value is converted to the configured unit (matching how other checks embed
	// a display value in the message while keeping raw values in Details).
	displayTemp, unitSymbol := c.displayTemperature(tempC)

	status := health.StatusHealthy
	msg := fmt.Sprintf("Temperature OK (%.1f %s)", displayTemp, unitSymbol)

	switch {
	case tempC >= critTemp:
		status = health.StatusCritical
		msg = fmt.Sprintf("Temperature critical: %.1f %s", displayTemp, unitSymbol)
	case tempC >= warnTemp:
		status = health.StatusWarning
		msg = fmt.Sprintf("Temperature high: %.1f %s", displayTemp, unitSymbol)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			// temperature_c is the raw Celsius reading (source of truth for
			// machine consumers); the *_display/*_unit pair mirrors what the
			// message shows.
			"temperature_c":       tempC,
			"temperature_display": displayTemp,
			"temperature_unit":    unitSymbol,
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

	const warnThreshold = 5 * time.Minute

	status := health.StatusHealthy
	msg := fmt.Sprintf("Uptime OK (%.0f seconds)", uptime.Seconds())

	if uptime < warnThreshold {
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
