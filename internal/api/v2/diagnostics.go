// internal/api/v2/diagnostics.go
package api

import (
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/health/checks"
	"github.com/tphakala/birdnet-go/internal/privacy"
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

	getStreamHealthInfos := c.buildStreamHealthProvider()

	c.healthRegistry.RegisterAll(
		// System checks
		checks.NewDiskSpaceCheck(c.getDataPaths()),
		checks.NewMemoryCheck(),
		checks.NewCPULoadCheck(GetCachedCPUUsage),
		checks.NewTemperatureCheck(func() (float64, error) {
			temps, err := host.SensorsTemperatures()
			// gopsutil returns partial results with warnings when some sensors
			// are unreadable (common on RPi). Use data if available.
			if len(temps) == 0 {
				if err != nil {
					return 0, err
				}
				return 0, errNoTempSensors
			}
			var maxTemp float64
			for _, t := range temps {
				if t.Temperature > maxTemp {
					maxTemp = t.Temperature
				}
			}
			return maxTemp, nil
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
			func() bool { return true },
			func() string { return c.currentSettings().BirdNET.ModelPath },
		),
		checks.NewInferenceLatencyCheck(func() (avgMS, p99MS, windowMS float64) {
			counters := classifier.GetInferenceCounters()
			snapshots := counters.PeekAll()
			if len(snapshots) == 0 {
				return 0, 0, 0
			}
			var totalUs, maxUs, count int64
			for _, s := range snapshots {
				totalUs += s.InvokeTotalUs
				count += s.InvokeCount
				if s.InvokeMaxUs > maxUs {
					maxUs = s.InvokeMaxUs
				}
			}
			if count == 0 {
				return 0, 0, 0
			}
			avgMS = float64(totalUs) / float64(count) / 1000.0
			// max used as p99 approximation; true p99 requires histogram data
			p99MS = float64(maxUs) / 1000.0
			windowMS = c.currentSettings().BirdNET.Overlap * 1000.0
			return avgMS, p99MS, windowMS
		}),
		checks.NewDetectionRateCheck(nil), // TODO: wire to datastore (PR 2)
		checks.NewQueueDepthCheck(func() (int, int) {
			q := classifier.ResultsQueue
			return len(q), cap(q)
		}),

		// Stream checks
		checks.NewStreamConnectivityCheck(getStreamHealthInfos),
		checks.NewStreamErrorRateCheck(getStreamHealthInfos),
		checks.NewFFmpegHealthCheck(getStreamHealthInfos),

		// Database checks
		checks.NewDatabaseSizeCheck(func() string {
			return c.currentSettings().Output.SQLite.Path
		}),
		checks.NewMigrationStatusCheck(func() (bool, string, error) {
			return true, "current", nil // TODO: wire to migration checker
		}),
		checks.NewDatabasePerformanceCheck(func() (time.Duration, error) {
			return c.DS.PingWithLatency()
		}),

		// Network checks
		checks.NewMQTTCheck(
			func() bool { return c.currentSettings().Realtime.MQTT.Enabled },
			func() bool {
				if c.Processor == nil {
					return false
				}
				client := c.Processor.GetMQTTClient()
				if client == nil {
					return false
				}
				return client.IsConnected()
			},
		),
		checks.NewBirdWeatherCheck(
			func() bool { return c.currentSettings().Realtime.Birdweather.Enabled },
			nil, // TODO: wire actual BirdWeather status
		),
		checks.NewNotificationProvidersCheck(),
		checks.NewWeatherCheck(func() bool {
			p := c.currentSettings().Realtime.Weather.Provider
			return p != "" && p != string(conf.WeatherNone)
		}),

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

// errNoTempSensors is returned when no temperature sensors are found on the system.
var errNoTempSensors = errors.NewStd("no temperature sensors found")

// buildStreamHealthProvider returns a closure that bridges the FFmpegManager's
// stream health data to the checks.StreamHealthInfo format. The closure
// nil-checks c.engine at call time because it is set after Controller init.
func (c *Controller) buildStreamHealthProvider() func() []checks.StreamHealthInfo {
	return func() []checks.StreamHealthInfo {
		if c.engine == nil {
			return nil
		}
		mgr := c.engine.FFmpegManager()
		if mgr == nil {
			return nil
		}
		healthMap := mgr.AllStreamHealth()
		if len(healthMap) == 0 {
			return nil
		}
		registry := c.engine.Registry()
		infos := make([]checks.StreamHealthInfo, 0, len(healthMap))
		for sourceID, sh := range healthMap {
			url := sourceID
			if registry != nil {
				if connStr, ok := registry.ConnectionStringByID(sourceID); ok {
					url = privacy.SanitizeStreamUrl(connStr)
				}
			}
			errMsg := ""
			if sh.Error != nil {
				errMsg = sh.Error.Error()
			}
			infos = append(infos, checks.StreamHealthInfo{
				URL:          url,
				IsHealthy:    sh.IsHealthy,
				ProcessState: sh.ProcessState.String(),
				RestartCount: sh.RestartCount,
				Error:        errMsg,
			})
		}
		return infos
	}
}

// getDataPaths returns filesystem paths that should be monitored for disk space.
func (c *Controller) getDataPaths() []string {
	settings := c.currentSettings()
	seen := make(map[string]struct{})
	var paths []string
	addPath := func(p string) {
		dir := filepath.Dir(p)
		if _, ok := seen[dir]; !ok {
			seen[dir] = struct{}{}
			paths = append(paths, dir)
		}
	}
	if settings.Output.SQLite.Path != "" {
		addPath(settings.Output.SQLite.Path)
	}
	if settings.Logging.FileOutput != nil && settings.Logging.FileOutput.Path != "" {
		addPath(settings.Logging.FileOutput.Path)
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
