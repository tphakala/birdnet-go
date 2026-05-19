package checks

import (
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// sanitizeID converts a model ID into a safe check-name suffix by
// lowercasing and replacing non-alphanumeric characters with underscores.
func sanitizeID(id string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r - 'A' + 'a'
		}
		return '_'
	}, id)
}

// worstResult returns the result with the most severe status, or a skipped
// result if the slice is empty. Used by MultiResultCheck.Run() implementations.
func worstResult(name string, cat health.Category, results []health.Result) health.Result {
	if len(results) == 0 {
		return skippedResult(name, cat, time.Now())
	}
	worst := results[0]
	for _, r := range results[1:] {
		if health.Severity(r.Status) > health.Severity(worst.Status) {
			worst = r
		}
	}
	return worst
}

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
