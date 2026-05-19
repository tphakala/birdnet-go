package checks

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// skippedResult builds a StatusSkipped Result for checks without available data.
func skippedResult(name string, category health.Category, start time.Time) health.Result {
	return health.Result{
		Name:       name,
		Category:   category,
		Status:     health.StatusSkipped,
		Message:    "Metrics not yet available",
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// counterStatsConfig parameterises evalCounterStats so that structurally
// identical drop/overrun checks can share one implementation.
type counterStatsConfig struct {
	warnThreshold int64
	critThreshold int64
	totalKey      string
	healthyMsg    string // expects %d (sources)
	warnMsg       string // expects %d (total), %d (sources)
	critMsg       string // expects %d (total), %d (sources)
}

// evalCounterStats aggregates per-source int64 counters and returns a Result
// using the thresholds and message templates in cfg.
func evalCounterStats(
	name string, category health.Category,
	stats map[string]int64, cfg *counterStatsConfig,
	start time.Time,
) health.Result {
	var total int64
	for _, v := range stats {
		total += v
	}

	status := health.StatusHealthy
	msg := fmt.Sprintf(cfg.healthyMsg, len(stats))

	switch {
	case total >= cfg.critThreshold:
		status = health.StatusCritical
		msg = fmt.Sprintf(cfg.critMsg, total, len(stats))
	case total >= cfg.warnThreshold:
		status = health.StatusWarning
		msg = fmt.Sprintf(cfg.warnMsg, total, len(stats))
	}

	return health.Result{
		Name:     name,
		Category: category,
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			cfg.totalKey: total,
			"sources":    len(stats),
			"per_source": stats,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
