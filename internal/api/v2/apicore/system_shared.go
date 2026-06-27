package apicore

import "time"

// MetricsCollectorInterval is the time between metric-collection ticks. It is
// shared substrate: the system domain's metrics-history collector samples on this
// cadence, and the analytics database-overview sparkline math derives its
// per-bucket sample count from it to stay consistent with the collector.
const MetricsCollectorInterval = 5 * time.Second

// Database backend type discriminators used by the v2 database stats and the
// backup/migration prerequisite logic. They live in apicore so the system domain
// (v2 stats) and the package-api backup handlers share one source.
const (
	DBTypeLegacy = "legacy"
	DBTypeV2     = "v2"
)
