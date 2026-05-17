package checks

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// DatabaseSizeCheck reports the size of the SQLite database file and warns when it is large.
type DatabaseSizeCheck struct {
	getDBPath func() string
}

// NewDatabaseSizeCheck creates a DatabaseSizeCheck using the given path provider.
func NewDatabaseSizeCheck(getDBPath func() string) *DatabaseSizeCheck {
	return &DatabaseSizeCheck{getDBPath: getDBPath}
}

// Name returns the check identifier.
func (c *DatabaseSizeCheck) Name() string { return "database_size" }

// Category returns the database category.
func (c *DatabaseSizeCheck) Category() health.Category { return health.CategoryDatabase }

// warnSizeBytes is the threshold above which a size warning is issued (1 GB).
const warnSizeBytes int64 = 1 << 30

// Run checks the database file size and warns if it exceeds 1 GB.
func (c *DatabaseSizeCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	path := c.getDBPath()
	if path == "" {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    "Database path not configured",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    fmt.Sprintf("Unable to stat database file: %v", err),
			Details:    map[string]any{"path": path},
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	sizeBytes := info.Size()
	sizeMB := float64(sizeBytes) / (1 << 20)

	status := health.StatusHealthy
	msg := fmt.Sprintf("Database size OK (%.1f MB)", sizeMB)
	if sizeBytes > warnSizeBytes {
		status = health.StatusWarning
		msg = fmt.Sprintf("Database file is large (%.1f MB)", sizeMB)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"path":       path,
			"size_bytes": sizeBytes,
			"size_mb":    sizeMB,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// MigrationStatusCheck verifies that the database schema is up to date.
type MigrationStatusCheck struct {
	checkMigration func() (bool, string, error)
}

// NewMigrationStatusCheck creates a MigrationStatusCheck using the given migration predicate.
// The function must return (upToDate, version, err).
func NewMigrationStatusCheck(checkMigration func() (bool, string, error)) *MigrationStatusCheck {
	return &MigrationStatusCheck{checkMigration: checkMigration}
}

// Name returns the check identifier.
func (c *MigrationStatusCheck) Name() string { return "migration_status" }

// Category returns the database category.
func (c *MigrationStatusCheck) Category() health.Category { return health.CategoryDatabase }

// Run verifies that all database migrations have been applied.
func (c *MigrationStatusCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	upToDate, version, err := c.checkMigration()
	if err != nil {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusCritical,
			Message:    fmt.Sprintf("Migration check failed: %v", err),
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	if !upToDate {
		return health.Result{
			Name:     c.Name(),
			Category: c.Category(),
			Status:   health.StatusCritical,
			Message:  "Database schema is not up to date",
			Details: map[string]any{
				"version": version,
			},
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   health.StatusHealthy,
		Message:  fmt.Sprintf("Database schema is up to date (version %s)", version),
		Details: map[string]any{
			"version": version,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// DatabasePerformanceCheck measures a simple database query latency and reports degradation.
type DatabasePerformanceCheck struct {
	queryTimer func() (time.Duration, error)
}

// NewDatabasePerformanceCheck creates a DatabasePerformanceCheck using the given latency probe.
func NewDatabasePerformanceCheck(queryTimer func() (time.Duration, error)) *DatabasePerformanceCheck {
	return &DatabasePerformanceCheck{queryTimer: queryTimer}
}

// Name returns the check identifier.
func (c *DatabasePerformanceCheck) Name() string { return "database_performance" }

// Category returns the database category.
func (c *DatabasePerformanceCheck) Category() health.Category { return health.CategoryDatabase }

// warnQueryLatency is the threshold above which a performance warning is issued.
const warnQueryLatency = 100 * time.Millisecond

// critQueryLatency is the threshold above which the check is marked critical.
const critQueryLatency = 500 * time.Millisecond

// Run measures query latency and reports degradation.
func (c *DatabasePerformanceCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	elapsed, err := c.queryTimer()
	if err != nil {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusCritical,
			Message:    fmt.Sprintf("Database query failed: %v", err),
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	latencyMS := float64(elapsed.Microseconds()) / 1000

	status := health.StatusHealthy
	msg := fmt.Sprintf("Database query latency OK (%.1f ms)", latencyMS)

	switch {
	case elapsed >= critQueryLatency:
		status = health.StatusCritical
		msg = fmt.Sprintf("Database query latency critical (%.1f ms)", latencyMS)
	case elapsed >= warnQueryLatency:
		status = health.StatusWarning
		msg = fmt.Sprintf("Database query latency elevated (%.1f ms)", latencyMS)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"latency_ms": latencyMS,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
