// internal/api/v2/diagnostics.go
package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/health/checks"
)

// diagnosticsStatusResponse is the quick health summary returned by GET /status.
type diagnosticsStatusResponse struct {
	Status     health.Status                     `json:"status"`
	Categories map[health.Category]health.Status `json:"categories"`
	LastRun    *time.Time                        `json:"last_run"`
}

// initDiagnosticsRoutes initializes health check infrastructure and registers
// the diagnostics API endpoints.
func (c *Controller) initDiagnosticsRoutes() {
	c.healthReports = health.NewReportStore(10)
	c.healthErrors = health.NewErrorRingBuffer(500)
	c.healthRegistry = health.NewRegistry()

	c.registerHealthChecks()

	diagnosticsGroup := c.Group.Group("/system/diagnostics", c.authMiddleware)
	diagnosticsGroup.GET("/status", c.GetDiagnosticsStatus)
	diagnosticsGroup.POST("/run", c.RunDiagnostics)
	diagnosticsGroup.GET("/report/:id", c.GetDiagnosticsReport)
	diagnosticsGroup.GET("/errors", c.GetRecentErrors)
}

// registerHealthChecks registers all health checks with dependency injection closures.
func (c *Controller) registerHealthChecks() {
	snapshotProvider := func() []audiocore.SourceHealthSnapshot {
		w := c.audioWatchdog.Load()
		if w == nil {
			return nil
		}
		return w.Snapshot()
	}

	c.healthRegistry.RegisterAll(
		// System checks
		checks.NewDiskSpaceCheck(c.getDataPaths()),
		checks.NewMemoryCheck(),
		checks.NewCPULoadCheck(GetCachedCPUUsage),
		checks.NewTemperatureCheck(func() (float64, error) {
			return 0, fmt.Errorf("not available")
		}),
		checks.NewUptimeCheck(c.startTime),

		// Audio checks
		checks.NewSourceStatusCheck(snapshotProvider),
		checks.NewPipelineLivenessCheck(snapshotProvider),
		checks.NewBufferDropsCheck(),
		checks.NewAudioLevelCheck(),
		checks.NewBufferOverrunCheck(),
		checks.NewCaptureBufferCheck(),

		// Analysis checks
		checks.NewModelLoadedCheck(
			func() bool { return true }, // model is always loaded if server is running
			func() string { return c.currentSettings().BirdNET.ModelPath },
		),
		checks.NewInferenceLatencyCheck(func() (float64, float64, float64) {
			return 0, 0, 0 // TODO: wire to actual inference stats
		}),
		checks.NewDetectionRateCheck(nil), // TODO: wire to datastore
		checks.NewQueueDepthCheck(func() (int, int) {
			return 0, 100 // TODO: wire to actual queue
		}),

		// Stream checks
		checks.NewStreamConnectivityCheck(func() []checks.StreamHealthInfo {
			return nil // TODO: wire to RTSP manager
		}),
		checks.NewStreamErrorRateCheck(func() []checks.StreamHealthInfo {
			return nil // TODO: wire to RTSP manager
		}),
		checks.NewFFmpegHealthCheck(func() []checks.StreamHealthInfo {
			return nil // TODO: wire to RTSP manager
		}),

		// Database checks
		checks.NewDatabaseSizeCheck(func() string {
			return c.currentSettings().Output.SQLite.Path
		}),
		checks.NewMigrationStatusCheck(func() (bool, string, error) {
			return true, "current", nil // TODO: wire to migration checker
		}),
		checks.NewDatabasePerformanceCheck(nil), // TODO: wire to datastore

		// Network checks
		checks.NewMQTTCheck(
			func() bool { return c.currentSettings().Realtime.MQTT.Enabled },
			nil, // TODO: wire actual MQTT connection status
		),
		checks.NewBirdWeatherCheck(
			func() bool { return c.currentSettings().Realtime.Birdweather.Enabled },
			nil, // TODO: wire actual BirdWeather status
		),
		checks.NewNotificationProvidersCheck(),
		checks.NewWeatherCheck(func() bool { return false }), // TODO: wire to weather config

		// Config checks
		checks.NewPathAccessCheck(map[string]string{
			"data": filepath.Dir(c.currentSettings().Output.SQLite.Path),
		}),
		checks.NewConfigConsistencyCheck(func() []string { return nil }),
		checks.NewDiskBudgetCheck(
			func() bool { return false }, // TODO: wire when disk budget feature exists
			func() (int64, int64) { return 0, 0 },
		),

		// Log checks
		checks.NewRecentErrorsCheck(c.healthErrors),
		checks.NewErrorTrendCheck(c.healthErrors),
		checks.NewCriticalEventsCheck(c.healthErrors),
	)
}

// getDataPaths returns filesystem paths that should be monitored for disk space.
func (c *Controller) getDataPaths() []string {
	settings := c.currentSettings()
	var paths []string
	if settings.Output.SQLite.Path != "" {
		paths = append(paths, filepath.Dir(settings.Output.SQLite.Path))
	}
	// Add log file directory if file output is configured
	if settings.Logging.FileOutput != nil && settings.Logging.FileOutput.Path != "" {
		paths = append(paths, filepath.Dir(settings.Logging.FileOutput.Path))
	}
	if len(paths) == 0 {
		paths = append(paths, ".")
	}
	return paths
}

// GetDiagnosticsStatus returns a quick health summary from the latest stored report.
func (c *Controller) GetDiagnosticsStatus(ctx echo.Context) error {
	latest := c.healthReports.Latest()
	if latest == nil {
		return ctx.JSON(http.StatusOK, diagnosticsStatusResponse{
			Status:     health.StatusUnknown,
			Categories: map[health.Category]health.Status{},
			LastRun:    nil,
		})
	}

	return ctx.JSON(http.StatusOK, diagnosticsStatusResponse{
		Status:     latest.Status,
		Categories: latest.Summary,
		LastRun:    &latest.StartedAt,
	})
}

// RunDiagnostics executes all registered health checks and stores the report.
func (c *Controller) RunDiagnostics(ctx echo.Context) error {
	id := uuid.New().String()
	startedAt := time.Now()

	results := c.healthRegistry.RunAll(ctx.Request().Context())
	report := health.NewReport(id, startedAt, results)
	c.healthReports.Save(report)

	return ctx.JSON(http.StatusOK, report)
}

// GetDiagnosticsReport retrieves a stored diagnostics report by ID.
func (c *Controller) GetDiagnosticsReport(ctx echo.Context) error {
	id := ctx.Param("id")
	report, ok := c.healthReports.Get(id)
	if !ok {
		return c.HandleError(ctx, nil, "report not found", http.StatusNotFound)
	}
	return ctx.JSON(http.StatusOK, report)
}

// GetRecentErrors returns recent error log entries from the error ring buffer.
func (c *Controller) GetRecentErrors(ctx echo.Context) error {
	const defaultLimit = 50
	const maxLimit = 200

	limit := defaultLimit
	if limitStr := ctx.QueryParam("limit"); limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	entries := c.healthErrors.Recent(limit)
	return ctx.JSON(http.StatusOK, entries)
}
