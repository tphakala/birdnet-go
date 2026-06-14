// internal/api/v2/diagnostics.go
package api

import (
	"context"
	"fmt"
	"math"
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
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/health/checks"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/weather"
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
	c.healthMetricsStore = observability.NewHealthMetricsStore()
	c.healthEvents = observability.NewHealthEventBuffer(observability.DefaultEventBufferCapacity)

	c.registerHealthChecks()

	diagnosticsGroup := c.Group.Group("/system/diagnostics", c.authMiddleware)
	diagnosticsGroup.GET("/status", c.GetDiagnosticsStatus)
	diagnosticsGroup.POST("/run", c.RunDiagnostics)
	diagnosticsGroup.GET("/report/:id", c.GetDiagnosticsReport)
	diagnosticsGroup.GET("/errors", c.GetRecentErrors)
}

// HealthMetricsStore returns the diagnostics health metrics store, or nil if
// the diagnostics subsystem has not been initialized. It lets other subsystems
// (e.g. the analysis pipeline) record health counters into the same store the
// health checks read, avoiding an import cycle on internal/api/v2.
func (c *Controller) HealthMetricsStore() *observability.HealthMetricsStore {
	return c.healthMetricsStore
}

// HealthEventBuffer returns the diagnostics health event buffer, or nil if the
// diagnostics subsystem has not been initialized. Paired with HealthMetricsStore
// so recorded counters can also surface as recent events on the System Health page.
func (c *Controller) HealthEventBuffer() *observability.HealthEventBuffer {
	return c.healthEvents
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
			maxTemp := math.Inf(-1)
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
		checks.NewBufferDropsCheck(c.healthMetricsStore, c.healthEvents.Recent),
		checks.NewAudioLevelCheck(c.buildAudioLevelProvider()),
		checks.NewBufferOverrunCheck(c.healthMetricsStore, c.healthEvents.Recent),
		checks.NewCaptureBufferCheck(c.buildCaptureBufferHealthProvider()),

		// Analysis checks (multi-model aware)
		checks.NewModelsLoadedCheck(c.buildModelLoadInfoProvider()),
		checks.NewPerModelInferenceLatencyCheck(c.buildPerModelInferenceProvider()),
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
		checks.NewResultsQueueDropCheck(c.healthMetricsStore, c.healthEvents.Recent),
		checks.NewORTAvailabilityCheck(func() (available, initialized bool, version, libraryPath, errMsg string) {
			status := inference.CheckORTAvailability(c.currentSettings().BirdNET.ONNXRuntimePath)
			return status.Available, status.Initialized, status.Version, status.LibraryPath, status.Error
		}),
		checks.NewRangeFilterCheck(func() checks.RangeFilterStatusInfo {
			orch, err := c.getBirdNETInstance()
			if err != nil || orch == nil {
				return checks.RangeFilterStatusInfo{}
			}
			st := orch.RangeFilterStatus()
			return checks.RangeFilterStatusInfo{
				LocationConfigured: st.LocationConfigured,
				Active:             st.Active,
				FellBack:           st.FellBack,
				GeomodelActive:     st.Geomodel != nil,
				MappedSpecies:      st.MappedSpecies,
			}
		}),

		// Stream checks
		checks.NewStreamConnectivityCheck(getStreamHealthInfos),
		checks.NewStreamErrorRateCheck(c.healthMetricsStore, c.healthEvents.Recent),
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
		checks.NewDatabaseIntegrityCheck(func() (string, bool) {
			ds := c.DS
			if ds == nil {
				return "", false
			}
			sqliteStore, ok := ds.(*datastore.SQLiteStore)
			if !ok || sqliteStore == nil {
				return "", false
			}
			return sqliteStore.IntegrityResult()
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
			func() (bool, string) {
				proc := c.Processor
				if proc == nil {
					return false, "Processor unavailable"
				}
				bw := proc.GetBwClient()
				if bw == nil {
					return false, "BirdWeather client not initialized"
				}
				return bw.Status()
			},
		),
		checks.NewNotificationProvidersCheck(func() (int, int, string) {
			providers := notification.GetAllPushProviderHealth()
			if len(providers) == 0 {
				return 0, 0, ""
			}
			total := len(providers)
			healthy := 0
			for i := range providers {
				if providers[i].Healthy {
					healthy++
				}
			}
			unhealthy := total - healthy
			var msg string
			switch unhealthy {
			case 0:
				msg = fmt.Sprintf("All %d providers healthy", total)
			case total:
				msg = fmt.Sprintf("All %d providers failing", total)
			default:
				msg = fmt.Sprintf("%d of %d providers unhealthy", unhealthy, total)
			}
			return total, healthy, msg
		}),
		checks.NewWeatherCheck(
			func() bool {
				p := c.currentSettings().Realtime.Weather.Provider
				return p != string(conf.WeatherNone)
			},
			weather.GetStatus,
		),

		// Config checks
		checks.NewToolAvailabilityCheck(func() []checks.ToolInfo {
			s := c.currentSettings()
			return []checks.ToolInfo{
				{
					Name:    "FFmpeg",
					Path:    s.Realtime.Audio.FfmpegPath,
					Version: s.Realtime.Audio.FfmpegVersion,
				},
				{
					Name: "Sox",
					Path: s.Realtime.Audio.SoxPath,
				},
			}
		}),
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

// buildModelLoadInfoProvider returns a closure that queries the orchestrator for
// all loaded models and converts them to the health check's ModelLoadInfo format.
func (c *Controller) buildModelLoadInfoProvider() func() []checks.ModelLoadInfo {
	return func() []checks.ModelLoadInfo {
		p := c.Processor
		if p == nil {
			return nil
		}
		bn := p.GetBirdNET()
		if bn == nil {
			return nil
		}
		infos := bn.ModelInfos()
		result := make([]checks.ModelLoadInfo, 0, len(infos))
		for i := range infos {
			m := &infos[i]
			result = append(result, checks.ModelLoadInfo{
				ID:      m.ID,
				Name:    m.DisplayName(),
				Loaded:  true,
				Backend: m.Backend,
				SpecInfo: fmt.Sprintf("%dkHz, %ss clips",
					m.Spec.SampleRate/1000,
					strconv.FormatFloat(m.Spec.ClipLength.Seconds(), 'f', -1, 64)),
			})
		}
		return result
	}
}

// buildPerModelInferenceProvider returns a closure that queries per-model
// inference counters and model specs to produce per-model latency stats.
// Each model's analysis window is derived from its own BufferInterval
// (ClipLength / 2), not from a global setting.
func (c *Controller) buildPerModelInferenceProvider() func() []checks.ModelInferenceInfo {
	return func() []checks.ModelInferenceInfo {
		p := c.Processor
		if p == nil {
			return nil
		}
		bn := p.GetBirdNET()
		if bn == nil {
			return nil
		}
		counters := classifier.GetInferenceCounters()
		snapshots := counters.PeekAll()
		if len(snapshots) == 0 {
			return nil
		}
		infos := bn.ModelInfos()
		infoMap := make(map[string]*classifier.ModelInfo, len(infos))
		for i := range infos {
			infoMap[infos[i].ID] = &infos[i]
		}
		result := make([]checks.ModelInferenceInfo, 0, len(snapshots))
		for modelID, s := range snapshots {
			mi, ok := infoMap[modelID]
			if !ok {
				continue
			}
			var avgMS, p99MS float64
			if s.InvokeCount > 0 {
				avgMS = float64(s.InvokeTotalUs) / float64(s.InvokeCount) / 1000.0
			}
			p99MS = float64(s.InvokeMaxUs) / 1000.0
			windowMS := float64(mi.Spec.BufferInterval().Milliseconds())
			result = append(result, checks.ModelInferenceInfo{
				ModelID:   modelID,
				ModelName: mi.DisplayName(),
				AvgMS:     avgMS,
				P99MS:     p99MS,
				WindowMS:  windowMS,
			})
		}
		return result
	}
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

// buildAudioRouterSnapshotProvider returns a closure that reads cumulative
// audio counter values from the audio router for the health metrics collector.
func (c *Controller) buildAudioRouterSnapshotProvider() func() []observability.AudioRouterSnapshot {
	return func() []observability.AudioRouterSnapshot {
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
		snaps := make([]observability.AudioRouterSnapshot, 0, len(sourceIDs))
		for _, sid := range sourceIDs {
			var totalDrops, totalErrors int64
			for _, ri := range router.Routes(sid) {
				totalDrops += ri.Drops
				totalErrors += ri.Errors
			}
			snaps = append(snaps, observability.AudioRouterSnapshot{
				SourceID: sid,
				Drops:    totalDrops,
				Errors:   totalErrors,
			})
		}
		return snaps
	}
}

// buildStreamHealthSnapshotProvider returns a closure that reads cumulative
// stream restart counts for the health metrics collector.
func (c *Controller) buildStreamHealthSnapshotProvider() func() []observability.StreamHealthSnapshot {
	return func() []observability.StreamHealthSnapshot {
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
		snaps := make([]observability.StreamHealthSnapshot, 0, len(healthMap))
		for sourceID, sh := range healthMap {
			snaps = append(snaps, observability.StreamHealthSnapshot{
				SourceID:     sourceID,
				RestartCount: sh.RestartCount,
			})
		}
		return snaps
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

		// Require engine/router so checks skip cleanly before startup and after teardown.
		eng := c.engine.Load()
		if eng == nil {
			return nil
		}
		router := eng.Router()
		if router == nil {
			return nil
		}
		ids := router.ActiveSourceIDs()
		if len(ids) == 0 {
			return nil
		}
		activeSources := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			activeSources[id] = struct{}{}
		}

		infos := make([]checks.AudioLevelInfo, 0, len(levels))
		for _, l := range levels {
			if _, ok := activeSources[l.Source]; !ok {
				continue
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
				SourceID:    s.SourceID,
				Capacity:    s.Capacity,
				Initialized: s.Initialized,
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

// validWindows maps accepted window parameter values to durations.
var validWindows = map[string]time.Duration{
	"15m": 15 * time.Minute,
	"30m": 30 * time.Minute,
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// parseWindow converts a window query parameter to a duration.
// Returns the default (1h) for empty input, or an error for invalid values.
func parseWindow(s string) (time.Duration, error) {
	if s == "" {
		return time.Hour, nil
	}
	d, ok := validWindows[s]
	if !ok {
		return 0, fmt.Errorf("invalid window %q: valid values are 15m, 30m, 1h, 6h, 24h, 7d", s)
	}
	return d, nil
}

// RunDiagnostics executes all registered health checks and stores the report.
func (c *Controller) RunDiagnostics(ctx echo.Context) error {
	window, err := parseWindow(ctx.QueryParam("window"))
	if err != nil {
		return c.HandleError(ctx, err, err.Error(), http.StatusBadRequest)
	}

	id := uuid.New().String()
	startedAt := time.Now()

	results := c.healthRegistry.RunAllWithWindow(ctx.Request().Context(), window)
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
