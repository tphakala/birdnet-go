// internal/api/v2/diagnostics.go
package api

import (
	"context"
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
	"github.com/tphakala/birdnet-go/internal/inference"
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
	if c.healthErrors == nil {
		c.healthErrors = health.NewErrorRingBuffer(health.DefaultErrorBufferSize)
	}
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
		checks.NewBufferDropsCheck(c.buildDropStatsProvider()),
		checks.NewAudioLevelCheck(c.buildAudioLevelProvider()),
		checks.NewBufferOverrunCheck(c.buildOverrunStatsProvider()),
		checks.NewCaptureBufferCheck(c.buildCaptureBufferHealthProvider()),

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
			stride := 3.0 - c.currentSettings().BirdNET.Overlap
			if stride <= 0 {
				stride = 3.0
			}
			windowMS = stride * 1000.0
			return avgMS, p99MS, windowMS
		}),
		checks.NewDetectionRateCheck(func(ctx context.Context, hours int) (int, error) {
			ds := c.DS
			if ds == nil {
				return 0, errors.NewStd("datastore unavailable")
			}
			since := time.Now().Add(-time.Duration(hours) * time.Hour)
			return ds.CountDetectionsSince(ctx, since)
		}),
		checks.NewQueueDepthCheck(func() (int, int) {
			q := classifier.ResultsQueue
			return len(q), cap(q)
		}),
		checks.NewORTAvailabilityCheck(func() (available, initialized bool, version, libraryPath, errMsg string) {
			status := inference.CheckORTAvailability(c.currentSettings().BirdNET.ONNXRuntimePath)
			return status.Available, status.Initialized, status.Version, status.LibraryPath, status.Error
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
			if c.DS == nil {
				return false, "", errors.NewStd("datastore unavailable")
			}
			dbType := "sqlite"
			if c.currentSettings().Output.MySQL.Enabled {
				dbType = "mysql"
			}
			return true, dbType + " (auto-migrated at startup)", nil
		}),
		checks.NewDatabasePerformanceCheck(func(ctx context.Context) (time.Duration, error) {
			ds := c.DS
			if ds == nil {
				return 0, errors.NewStd("datastore unavailable")
			}
			return ds.PingWithLatency(ctx)
		}),

		// Network checks
		checks.NewMQTTCheck(
			func() bool { return c.currentSettings().Realtime.MQTT.Enabled },
			func() bool {
				proc := c.Processor
				if proc == nil {
					return false
				}
				client := proc.GetMQTTClient()
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
// atomically loads c.engine at call time because it is set after Controller init.
func (c *Controller) buildStreamHealthProvider() func() []checks.StreamHealthInfo {
	return func() []checks.StreamHealthInfo {
		eng := c.engine.Load()
		if eng == nil {
			return nil
		}
		mgr := eng.FFmpegManager()
		if mgr == nil {
			return nil
		}
		healthMap := mgr.AllStreamHealth()
		if len(healthMap) == 0 {
			return nil
		}
		registry := eng.Registry()
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

// buildDropStatsProvider returns a closure that aggregates per-source frame
// drop counts from the audio router. Returns nil before the engine starts.
func (c *Controller) buildDropStatsProvider() func() checks.DropStats {
	return func() checks.DropStats {
		eng := c.engine.Load()
		if eng == nil {
			return nil
		}
		router := eng.Router()
		if router == nil {
			return nil
		}
		sourceIDs := router.ActiveSourceIDs()
		if len(sourceIDs) == 0 {
			return nil
		}
		stats := make(checks.DropStats, len(sourceIDs))
		for _, sid := range sourceIDs {
			var totalDrops int64
			for _, ri := range router.Routes(sid) {
				totalDrops += ri.Drops
			}
			stats[sid] = totalDrops
		}
		return stats
	}
}

// buildAudioLevelProvider returns a closure that reads the latest audio level
// per source from the global audio level manager, filtered to active sources.
func (c *Controller) buildAudioLevelProvider() func() []checks.AudioLevelInfo {
	return func() []checks.AudioLevelInfo {
		levels := LatestAudioLevels()
		if len(levels) == 0 {
			return nil
		}

		// Filter to active sources so removed sources don't produce stale data
		var activeSources map[string]struct{}
		if eng := c.engine.Load(); eng != nil {
			if router := eng.Router(); router != nil {
				ids := router.ActiveSourceIDs()
				activeSources = make(map[string]struct{}, len(ids))
				for _, id := range ids {
					activeSources[id] = struct{}{}
				}
			}
		}

		infos := make([]checks.AudioLevelInfo, 0, len(levels))
		for _, l := range levels {
			if activeSources != nil {
				if _, ok := activeSources[l.Source]; !ok {
					continue
				}
			}
			infos = append(infos, checks.AudioLevelInfo{
				Source:   l.Source,
				Level:    l.Level,
				Clipping: l.Clipping,
			})
		}
		if len(infos) == 0 {
			return nil
		}
		return infos
	}
}

// buildOverrunStatsProvider returns a closure that aggregates per-source write
// error counts from the audio router. Write errors indicate downstream
// processing could not keep up (overrun condition).
func (c *Controller) buildOverrunStatsProvider() func() checks.OverrunStats {
	return func() checks.OverrunStats {
		eng := c.engine.Load()
		if eng == nil {
			return nil
		}
		router := eng.Router()
		if router == nil {
			return nil
		}
		sourceIDs := router.ActiveSourceIDs()
		if len(sourceIDs) == 0 {
			return nil
		}
		stats := make(checks.OverrunStats, len(sourceIDs))
		for _, sid := range sourceIDs {
			var totalErrors int64
			for _, ri := range router.Routes(sid) {
				totalErrors += ri.Errors
			}
			stats[sid] = totalErrors
		}
		return stats
	}
}

// buildCaptureBufferHealthProvider returns a closure that reads capture buffer
// utilization from the buffer manager.
func (c *Controller) buildCaptureBufferHealthProvider() func() []checks.CaptureBufferInfo {
	return func() []checks.CaptureBufferInfo {
		eng := c.engine.Load()
		if eng == nil {
			return nil
		}
		mgr := eng.BufferManager()
		if mgr == nil {
			return nil
		}
		snapshots := mgr.CaptureBufferHealthAll()
		if len(snapshots) == 0 {
			return nil
		}
		infos := make([]checks.CaptureBufferInfo, 0, len(snapshots))
		for _, s := range snapshots {
			infos = append(infos, checks.CaptureBufferInfo{
				SourceID:  s.SourceID,
				Capacity:  s.Capacity,
				Used:      s.Used,
				FillRatio: s.FillRatio,
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
