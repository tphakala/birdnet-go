// internal/health/check.go
package health

import (
	"context"
	"time"
)

// Status represents the health status of a check.
type Status string

const (
	StatusHealthy  Status = "healthy"
	StatusWarning  Status = "warning"
	StatusCritical Status = "critical"
	StatusUnknown  Status = "unknown"
	StatusSkipped  Status = "skipped"
)

// Category groups related checks together.
type Category string

const (
	CategorySystem   Category = "system"
	CategoryAudio    Category = "audio"
	CategoryAnalysis Category = "analysis"
	CategoryStreams  Category = "streams"
	CategoryDatabase Category = "database"
	CategoryNetwork  Category = "network"
	CategoryConfig   Category = "config"
	CategoryLogs     Category = "logs"
)

// AllCategories returns categories in display order.
func AllCategories() []Category {
	return []Category{
		CategorySystem, CategoryAudio, CategoryAnalysis, CategoryStreams,
		CategoryDatabase, CategoryNetwork, CategoryConfig, CategoryLogs,
	}
}

// Result is the outcome of a single health check.
type Result struct {
	Name       string         `json:"name"`
	Category   Category       `json:"category"`
	Status     Status         `json:"status"`
	Message    string         `json:"message"`
	Details    map[string]any `json:"details,omitempty"`
	DurationMS float64        `json:"duration_ms"`
	Timestamp  time.Time      `json:"timestamp"`
}

// Check is the interface all health checks implement.
type Check interface {
	Name() string
	Category() Category
	Run(ctx context.Context) Result
}

// MultiResultCheck is implemented by checks that produce multiple results
// at run time, such as per-model health checks. The registry detects this
// interface and calls RunMulti instead of Run when present.
type MultiResultCheck interface {
	Check
	RunMulti(ctx context.Context) []Result
}

// WorstStatus returns the most severe actionable status from a slice of results.
// Skipped and unknown results are ignored when actionable results (healthy,
// warning, critical) exist. If every result is non-actionable, it returns
// StatusUnknown when any unknown result is present, otherwise StatusSkipped.
func WorstStatus(results []Result) Status {
	worst := StatusHealthy
	hasActionable := false
	sawUnknown := false
	for _, r := range results {
		if r.Status == StatusSkipped || r.Status == StatusUnknown {
			if r.Status == StatusUnknown {
				sawUnknown = true
			}
			continue
		}
		hasActionable = true
		if Severity(r.Status) > Severity(worst) {
			worst = r.Status
		}
	}
	if !hasActionable && len(results) > 0 {
		if sawUnknown {
			return StatusUnknown
		}
		return StatusSkipped
	}
	return worst
}

// Severity returns the numeric severity of a status for comparison.
// Higher values indicate more severe conditions.
func Severity(s Status) int {
	switch s {
	case StatusHealthy:
		return 0
	case StatusSkipped, StatusUnknown:
		return 1
	case StatusWarning:
		return 2
	case StatusCritical:
		return 3
	default:
		return 0
	}
}
