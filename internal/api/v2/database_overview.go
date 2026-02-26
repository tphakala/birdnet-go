package api

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// DatabaseOverviewResponse is the response for GET /api/v2/system/database/overview.
// It adapts to the active database engine via the Engine field.
type DatabaseOverviewResponse struct {
	Engine           string                     `json:"engine"`           // "sqlite" or "mysql"
	Status           string                     `json:"status"`           // "connected" or "disconnected"
	Location         string                     `json:"location"`         // file path or host:port/db
	SizeBytes        int64                      `json:"size_bytes"`       // total database size
	TotalDetections  int64                      `json:"total_detections"` // total detection count
	TotalTables      int                        `json:"total_tables"`     // number of user tables
	SQLite           *datastore.SQLiteDetails   `json:"sqlite,omitempty"`
	MySQL            *datastore.MySQLDetails    `json:"mysql,omitempty"`
	Tables           []datastore.TableStats     `json:"tables"`
	Performance      datastore.PerformanceStats `json:"performance"`
	DetectionRate24h []datastore.HourlyCount    `json:"detection_rate_24h"`
}

// Database connection status constants.
const (
	dbStatusConnected    = "connected"
	dbStatusDisconnected = "disconnected"
)

// metricsCollectorIntervalSec is the collector interval in seconds, derived from
// metricsCollectorInterval to guarantee consistency.
var metricsCollectorIntervalSec = metricsCollectorInterval.Seconds()

// samplesPerHour is how many ring buffer entries cover one hour at the collector interval.
var samplesPerHour = int(3600 / metricsCollectorIntervalSec)

// detectionRateCacheTTL is how long detection rate query results are cached.
const detectionRateCacheTTL = 5 * time.Minute

// GetDatabaseOverview handles GET /api/v2/system/database/overview.
// It assembles engine metadata, table stats, performance metrics, and detection rates
// into a single response.
func (c *Controller) GetDatabaseOverview(ctx echo.Context) error {
	// Get basic database stats from the store
	basicStats, err := c.DS.GetDatabaseStats()
	if err != nil || basicStats == nil {
		if err != nil {
			c.logDebugIfEnabled("Database stats unavailable", logger.Error(err))
		}
		return ctx.JSON(http.StatusOK, &DatabaseOverviewResponse{
			Status:           dbStatusDisconnected,
			Tables:           []datastore.TableStats{},
			DetectionRate24h: []datastore.HourlyCount{},
		})
	}

	resp := &DatabaseOverviewResponse{
		Engine:           basicStats.Type,
		Location:         basicStats.Location,
		SizeBytes:        basicStats.SizeBytes,
		TotalDetections:  basicStats.TotalDetections,
		Tables:           []datastore.TableStats{},
		DetectionRate24h: []datastore.HourlyCount{},
	}

	if basicStats.Connected {
		resp.Status = dbStatusConnected
	} else {
		resp.Status = dbStatusDisconnected
	}

	// Try to get engine-specific details via DatabaseInspector
	inspector, ok := c.DS.(datastore.DatabaseInspector)
	if ok {
		// Engine details
		if details, err := inspector.GetEngineDetails(); err != nil {
			c.logDebugIfEnabled("Failed to get engine details", logger.Error(err))
		} else {
			resp.SQLite = details.SQLite
			resp.MySQL = details.MySQL
		}

		// Table stats
		if tables, err := inspector.GetTableStats(); err != nil {
			c.logDebugIfEnabled("Failed to get table stats", logger.Error(err))
		} else {
			resp.Tables = tables
			resp.TotalTables = len(tables)
		}

		// Detection rate (24h hourly histogram) — cached to avoid repeated queries
		if rates, err := c.detectionRateCache.GetHourly(inspector.GetDetectionRate24h); err != nil {
			c.logDebugIfEnabled("Failed to get detection rate", logger.Error(err))
		} else {
			resp.DetectionRate24h = rates
		}
	}

	// Assemble performance stats from ring buffer + atomic counters
	resp.Performance = c.assemblePerformanceStats()

	return ctx.JSON(http.StatusOK, resp)
}

// assemblePerformanceStats builds a PerformanceStats from the latest ring buffer
// values and cumulative atomic counters.
func (c *Controller) assemblePerformanceStats() datastore.PerformanceStats {
	stats := datastore.PerformanceStats{}

	if c.metricsStore == nil {
		return stats
	}

	// Current values from latest ring buffer entry
	latest := c.metricsStore.GetLatest()
	if latest != nil {
		if pt, ok := latest["db.read_latency_ms"]; ok {
			stats.ReadLatencyAvgMs = pt.Value
		}
		if pt, ok := latest["db.write_latency_ms"]; ok {
			stats.WriteLatencyAvgMs = pt.Value
		}
		if pt, ok := latest["db.read_latency_max_ms"]; ok {
			stats.ReadLatencyMaxMs = pt.Value
		}
		if pt, ok := latest["db.write_latency_max_ms"]; ok {
			stats.WriteLatencyMaxMs = pt.Value
		}
		if pt, ok := latest["db.queries_per_sec"]; ok {
			stats.QueriesPerSec = pt.Value
		}
	}

	// QueriesLastHour: sum ring buffer entries × collection interval
	samples := c.metricsStore.Get("db.queries_per_sec", samplesPerHour)
	if len(samples) > 0 {
		var total float64
		for _, s := range samples {
			total += s.Value * metricsCollectorIntervalSec
		}
		stats.QueriesLastHour = int64(total)
	}

	// Cumulative counters from the datastore
	if provider, ok := c.DS.(datastore.DBCountersProvider); ok {
		if counters := provider.GetDBCounters(); counters != nil {
			stats.SlowQueryCount = counters.SlowQueryCount.Load()
		}
	}

	return stats
}

// initDatabaseOverviewRoutes registers the database overview endpoint.
func (c *Controller) initDatabaseOverviewRoutes() {
	// Create a database group under system
	dbGroup := c.Group.Group("/system/database")

	// Get the appropriate auth middleware
	authMiddleware := c.authMiddleware

	dbGroup.GET("/overview", c.GetDatabaseOverview, authMiddleware)

	c.logInfoIfEnabled("Database overview route initialized")
}
