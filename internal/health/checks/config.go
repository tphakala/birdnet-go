package checks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// PathAccessCheck verifies write access to configured filesystem paths.
type PathAccessCheck struct {
	paths map[string]string // label -> path
}

// NewPathAccessCheck creates a PathAccessCheck for the given labelled paths.
// The paths map keys are human-readable labels; values are filesystem paths to test.
func NewPathAccessCheck(paths map[string]string) *PathAccessCheck {
	return &PathAccessCheck{paths: paths}
}

// Name returns the check identifier.
func (c *PathAccessCheck) Name() string { return "path_access" }

// Category returns the config category.
func (c *PathAccessCheck) Category() health.Category { return health.CategoryConfig }

// Run tests write access to each configured path by creating and removing a temporary file.
func (c *PathAccessCheck) Run(_ context.Context) health.Result {
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

	type pathResult struct {
		Label  string `json:"label"`
		Path   string `json:"path"`
		Access string `json:"access"`
	}

	results := make([]pathResult, 0, len(c.paths))
	var failed []string

	for label, path := range c.paths {
		access := "ok"
		tmpPath := filepath.Join(path, ".health_check_tmp")
		f, err := os.CreateTemp(path, ".health_check_tmp_*")
		if err != nil {
			access = "inaccessible"
			failed = append(failed, label)
		} else {
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
			_ = tmpPath // suppress unused warning
		}
		results = append(results, pathResult{Label: label, Path: path, Access: access})
	}

	status := health.StatusHealthy
	msg := "All configured paths are accessible"
	if len(failed) > 0 {
		status = health.StatusWarning
		msg = fmt.Sprintf("Path(s) not writable: %s", strings.Join(failed, ", "))
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"paths": results,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// ConfigConsistencyCheck validates the application configuration for logical inconsistencies.
type ConfigConsistencyCheck struct {
	validate func() []string
}

// NewConfigConsistencyCheck creates a ConfigConsistencyCheck using the given validator.
// validate must return a slice of human-readable issue descriptions; an empty slice means healthy.
func NewConfigConsistencyCheck(validate func() []string) *ConfigConsistencyCheck {
	return &ConfigConsistencyCheck{validate: validate}
}

// Name returns the check identifier.
func (c *ConfigConsistencyCheck) Name() string { return "config_consistency" }

// Category returns the config category.
func (c *ConfigConsistencyCheck) Category() health.Category { return health.CategoryConfig }

// Run validates the current configuration and reports any issues found.
func (c *ConfigConsistencyCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	issues := c.validate()
	if len(issues) == 0 {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusHealthy,
			Message:    "Configuration is consistent",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   health.StatusWarning,
		Message:  fmt.Sprintf("Configuration has %d issue(s)", len(issues)),
		Details: map[string]any{
			"issues": issues,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// DiskBudgetCheck monitors disk usage relative to a configured storage budget.
type DiskBudgetCheck struct {
	isEnabled func() bool
	getUsage  func() (usedBytes, budgetBytes int64)
}

// NewDiskBudgetCheck creates a DiskBudgetCheck using the given enable predicate and usage provider.
// getUsage must return (usedBytes, budgetBytes).
func NewDiskBudgetCheck(isEnabled func() bool, getUsage func() (int64, int64)) *DiskBudgetCheck {
	return &DiskBudgetCheck{isEnabled: isEnabled, getUsage: getUsage}
}

// Name returns the check identifier.
func (c *DiskBudgetCheck) Name() string { return "disk_budget" }

// Category returns the config category.
func (c *DiskBudgetCheck) Category() health.Category { return health.CategoryConfig }

// warnBudgetPercent is the usage ratio above which a warning is issued.
const warnBudgetPercent = 0.80

// critBudgetPercent is the usage ratio above which the check is marked critical.
const critBudgetPercent = 0.95

// Run checks disk usage against the configured budget.
func (c *DiskBudgetCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if !c.isEnabled() {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusSkipped,
			Message:    "Disk budget is disabled",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	usedBytes, budgetBytes := c.getUsage()
	if budgetBytes <= 0 {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    "Disk budget not configured",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	ratio := float64(usedBytes) / float64(budgetBytes)
	percent := ratio * 100
	usedMB := float64(usedBytes) / (1 << 20)
	budgetMB := float64(budgetBytes) / (1 << 20)

	status := health.StatusHealthy
	msg := fmt.Sprintf("Disk usage OK (%.1f MB / %.1f MB, %.0f%%)", usedMB, budgetMB, percent)

	switch {
	case ratio >= critBudgetPercent:
		status = health.StatusCritical
		msg = fmt.Sprintf("Disk usage critical (%.1f MB / %.1f MB, %.0f%%)", usedMB, budgetMB, percent)
	case ratio >= warnBudgetPercent:
		status = health.StatusWarning
		msg = fmt.Sprintf("Disk usage elevated (%.1f MB / %.1f MB, %.0f%%)", usedMB, budgetMB, percent)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"used_bytes":   usedBytes,
			"budget_bytes": budgetBytes,
			"percent":      percent,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
