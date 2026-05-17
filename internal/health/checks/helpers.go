package checks

import (
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
