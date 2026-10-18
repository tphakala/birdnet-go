package checks

import (
	"context"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/health"
)

// Messages of the storage persistence check.
const (
	msgStorageNotContainer  = "Not running in a Docker or Podman container"
	msgStorageNoPaths       = "No storage paths to check"
	msgStorageReadFailed    = "Mount table could not be read"
	msgStorageUnjudged      = "Storage paths could not be judged"
	msgStorageAllPersistent = "All data paths are on persistent volumes"
	msgStorageLostPrefix    = "Lost when the container is recreated: "
	msgStorageAnonPrefix    = "In an anonymous volume: "
	msgStorageJoiner        = "; "
	labelJoiner             = ", "
)

// StoragePersistenceReport is the storage persistence evaluation the check
// reports on. It is produced by the API layer so the health package does not
// read settings or the mount table itself.
type StoragePersistenceReport struct {
	// Applicable is false outside Docker and Podman containers.
	Applicable bool
	// Err is set when the mount table could not be read.
	Err error
	// Verdicts holds one verdict per key path, in label order.
	Verdicts []diagnostics.StorageVerdict
}

// StoragePersistenceCheck reports key paths (config, database, models, clips)
// that sit on a container's own writable layer, on a memory filesystem, or in an
// anonymous volume, where the data is lost or seems lost when the container is
// recreated.
type StoragePersistenceCheck struct {
	get func() StoragePersistenceReport
}

// NewStoragePersistenceCheck creates the check around a report provider that is
// called on every run, so setting changes take effect without a restart.
func NewStoragePersistenceCheck(get func() StoragePersistenceReport) *StoragePersistenceCheck {
	return &StoragePersistenceCheck{get: get}
}

// Name returns the check identifier.
func (c *StoragePersistenceCheck) Name() string { return "storage_persistence" }

// Category returns the config category.
func (c *StoragePersistenceCheck) Category() health.Category { return health.CategoryConfig }

// Run evaluates the report. Unknown paths never raise a warning.
func (c *StoragePersistenceCheck) Run(_ context.Context) health.Result {
	start := time.Now()
	result := func(status health.Status, message string, details map[string]any) health.Result {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     status,
			Message:    message,
			Details:    details,
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	if c.get == nil {
		return result(health.StatusSkipped, msgStorageNotContainer, nil)
	}
	report := c.get()
	switch {
	case !report.Applicable:
		return result(health.StatusSkipped, msgStorageNotContainer, nil)
	case report.Err != nil:
		return result(health.StatusUnknown, msgStorageReadFailed, nil)
	case len(report.Verdicts) == 0:
		return result(health.StatusSkipped, msgStorageNoPaths, nil)
	}

	var lost, anonymous []string
	worst := diagnostics.StorageSeverityNone
	judged := false
	details := make([]map[string]any, 0, len(report.Verdicts))
	for i := range report.Verdicts {
		v := &report.Verdicts[i]
		if v.Class != diagnostics.PersistenceUnknown {
			judged = true
		}
		switch {
		case v.Lost():
			lost = append(lost, v.Label)
		case v.Class == diagnostics.PersistenceAnonymousVolume:
			anonymous = append(anonymous, v.Label)
		}
		worst = max(worst, v.Severity())
		details = append(details, map[string]any{
			"label":         v.Label,
			"path":          v.Path,
			"resolved_path": v.ResolvedPath,
			"mount_point":   v.MountPoint,
			"fstype":        v.FSType,
			"class":         string(v.Class),
		})
	}
	detailMap := map[string]any{"paths": details}

	if !judged {
		return result(health.StatusUnknown, msgStorageUnjudged, detailMap)
	}

	var parts []string
	if len(lost) > 0 {
		parts = append(parts, msgStorageLostPrefix+strings.Join(lost, labelJoiner))
	}
	if len(anonymous) > 0 {
		parts = append(parts, msgStorageAnonPrefix+strings.Join(anonymous, labelJoiner))
	}

	switch worst {
	case diagnostics.StorageSeverityCritical:
		return result(health.StatusCritical, strings.Join(parts, msgStorageJoiner), detailMap)
	case diagnostics.StorageSeverityWarning:
		return result(health.StatusWarning, strings.Join(parts, msgStorageJoiner), detailMap)
	case diagnostics.StorageSeverityNone:
		return result(health.StatusHealthy, msgStorageAllPersistent, detailMap)
	default:
		return result(health.StatusHealthy, msgStorageAllPersistent, detailMap)
	}
}
