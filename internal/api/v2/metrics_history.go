// internal/api/v2/metrics_history.go
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// MetricsHistoryMaxPoints is the maximum number of data points retained per
// metric in the ring buffer. It also serves as the default and cap for the
// "points" query parameter on the history endpoint.
const MetricsHistoryMaxPoints = 360

// metricsSSEHeartbeatInterval is the keepalive interval for the metrics SSE stream.
const metricsSSEHeartbeatInterval = 30 * time.Second

// MetricsHistoryResponse is the JSON envelope for the history endpoint.
type MetricsHistoryResponse struct {
	Metrics map[string][]observability.MetricPoint `json:"metrics"`
}

// GetMetricsHistory returns historical metric data for sparkline rendering.
//
//	GET /api/v2/system/metrics/history?metrics=cpu.total,memory.used_percent&points=60
func (c *Controller) GetMetricsHistory(ctx echo.Context) error {
	if c.metricsStore == nil {
		return c.HandleError(ctx, nil, "Metrics history not available", http.StatusServiceUnavailable)
	}

	points := MetricsHistoryMaxPoints
	if raw := ctx.QueryParam("points"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > MetricsHistoryMaxPoints {
			return c.HandleError(ctx, err, "Invalid 'points' parameter", http.StatusBadRequest)
		}
		points = parsed
	}

	var result map[string][]observability.MetricPoint

	if filter := ctx.QueryParam("metrics"); filter != "" {
		names := strings.Split(filter, ",")
		result = make(map[string][]observability.MetricPoint, len(names))
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if pts := c.metricsStore.Get(name, points); pts != nil {
				result[name] = pts
			}
		}
	} else {
		result = c.metricsStore.GetAll(points)
	}

	c.logDebugIfEnabled("Metrics history retrieved",
		logger.Int("metrics_count", len(result)),
		logger.Int("points_requested", points),
	)

	return ctx.JSON(http.StatusOK, MetricsHistoryResponse{Metrics: result})
}

// StreamMetrics provides an SSE stream of live metric updates.
//
//	GET /api/v2/system/metrics/stream?metrics=cpu.total,memory.used_percent
func (c *Controller) StreamMetrics(ctx echo.Context) error {
	if c.metricsStore == nil {
		return c.HandleError(ctx, nil, "Metrics stream not available", http.StatusServiceUnavailable)
	}

	// Parse optional metrics filter
	var filterSet map[string]struct{}
	if filter := ctx.QueryParam("metrics"); filter != "" {
		names := strings.Split(filter, ",")
		filterSet = make(map[string]struct{}, len(names))
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name != "" {
				filterSet[name] = struct{}{}
			}
		}
	}

	// Subscribe to metric updates
	ch, cancel := c.metricsStore.Subscribe()
	defer cancel()

	// Set SSE headers
	setSSEHeaders(ctx)

	clientID := generateCorrelationID()
	c.logInfoIfEnabled("Metrics SSE client connected",
		logger.String("client_id", clientID),
		logger.String("ip", ctx.RealIP()),
	)

	// Send initial connection message
	if err := c.sendSSEMessage(ctx, "connected", map[string]string{
		"clientId": clientID,
		"message":  "Connected to metrics stream",
	}); err != nil {
		return err
	}

	heartbeatTicker := time.NewTicker(metricsSSEHeartbeatInterval)
	defer heartbeatTicker.Stop()

	reqCtx := ctx.Request().Context()

	for {
		select {
		case <-reqCtx.Done():
			c.logInfoIfEnabled("Metrics SSE client disconnected",
				logger.String("client_id", clientID),
			)
			return nil

		case snapshot, ok := <-ch:
			if !ok {
				return nil // channel closed
			}

			// Apply filter if specified
			data := snapshot
			if filterSet != nil {
				data = make(map[string]observability.MetricPoint, len(filterSet))
				for name, point := range snapshot {
					if _, wanted := filterSet[name]; wanted {
						data[name] = point
					}
				}
			}

			if len(data) > 0 {
				if err := c.sendSSEMessage(ctx, "metrics", data); err != nil {
					c.logDebugIfEnabled("Metrics SSE send failed, client likely disconnected",
						logger.String("client_id", clientID),
						logger.Error(err),
					)
					return err
				}
			}

		case <-heartbeatTicker.C:
			if err := c.sendSSEMessage(ctx, "heartbeat", map[string]any{
				"timestamp": time.Now().Unix(),
			}); err != nil {
				c.logDebugIfEnabled("Metrics SSE heartbeat failed",
					logger.String("client_id", clientID),
					logger.Error(err),
				)
				return err
			}
		}
	}
}

// initMetricsHistoryRoutes registers the metrics history endpoints.
func (c *Controller) initMetricsHistoryRoutes() {
	if c.metricsStore == nil {
		c.logWarnIfEnabled("Metrics store not configured, skipping metrics history routes")
		return
	}

	c.logInfoIfEnabled("Initializing metrics history routes")

	systemGroup := c.Group.Group("/system")
	authMiddleware := c.authMiddleware

	// Rate limiter for metrics SSE connections (10 requests per minute per IP)
	rateLimiterConfig := middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      10,
				ExpiresIn: 1 * time.Minute,
			},
		),
		IdentifierExtractor: middleware.DefaultRateLimiterConfig.IdentifierExtractor,
		ErrorHandler: func(context echo.Context, err error) error {
			return context.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Rate limit exceeded for metrics SSE connections",
			})
		},
		DenyHandler: func(context echo.Context, identifier string, err error) error {
			return context.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Too many metrics SSE connection attempts, please wait before trying again",
			})
		},
	}

	metricsGroup := systemGroup.Group("/metrics", authMiddleware)
	metricsGroup.GET("/history", c.GetMetricsHistory)
	metricsGroup.GET("/stream", c.StreamMetrics, middleware.RateLimiterWithConfig(rateLimiterConfig))

	// Start the collector background goroutine
	c.wg.Go(func() {
		collector := observability.NewCollector(
			c.metricsStore,
			metricsCollectorInterval,
			c.getCPUUsageFunc(),
		)

		// Wire database counters if the datastore supports them
		if provider, ok := c.DS.(datastore.DBCountersProvider); ok {
			if counters := provider.GetDBCounters(); counters != nil {
				collector.SetDBCounters(counters)
			}
		}

		collector.Start(c.ctx)
	})

	c.logInfoIfEnabled(fmt.Sprintf("Metrics history routes initialized (collector interval: %s)", metricsCollectorInterval))
}

// metricsCollectorInterval is the time between metric collection ticks.
const metricsCollectorInterval = 5 * time.Second

// getCPUUsageFunc returns a function that reads from the existing CPUCache.
func (c *Controller) getCPUUsageFunc() observability.CPUUsageFunc {
	return func() float64 {
		values := GetCachedCPUUsage()
		if len(values) > 0 {
			return values[0]
		}
		return 0
	}
}
