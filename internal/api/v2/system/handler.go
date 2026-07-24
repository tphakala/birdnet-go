// Package system implements the v2 API system-information domain: the
// /api/v2/system/* information endpoints (host info, resources, disks, job
// queue, processes, CPU temperature, database stats/backup, network interfaces,
// restart status, active models, inference status), the detection/operational
// events endpoints, the diagnostics/health endpoints, the metrics-history
// endpoints, and the browser terminal websocket.
//
// The handler embeds *apicore.Core by pointer so the shared deps (DS, Settings,
// Processor, Metrics, MetricsStore, Engine, error/log helpers, goroutine
// plumbing) promote directly onto it. Beyond the core it owns the diagnostics
// health infrastructure (registry/report-store/error-buffer/metrics-store/event-
// buffer) and receives two facade-owned values by injection: the controller
// start time (for the uptime health check) and the optional shared health error
// ring buffer (WithHealthErrorBuffer).
package system

import (
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// Handler serves the system-domain endpoints around the shared core.
type Handler struct {
	*apicore.Core

	// startTime is the controller construction time, injected from the facade. It
	// backs the diagnostics uptime health check (NewUptimeCheck). It is distinct
	// from the package-level processStartTime var (process start) used by
	// GetSystemInfo's app-start/app-uptime fields.
	startTime *time.Time

	// audioLevelProvider returns the latest per-source audio levels for the
	// diagnostics audio-level health check. It is injected from the facade
	// (LatestAudioLevels) because the audio-level manager belongs to the
	// audio/streaming domain, which has not been extracted yet.
	audioLevelProvider func() []audiocore.AudioLevelData

	// Health check infrastructure for the diagnostics endpoints. healthErrors may
	// be seeded from the facade (WithHealthErrorBuffer) so the logger and the
	// health checks share one ring buffer; the rest are created in
	// RegisterDiagnosticsRoutes. HealthMetricsStore()/HealthEventBuffer() expose
	// the metrics store and event buffer to the analysis pipeline through facade
	// delegators.
	healthRegistry     *health.Registry
	healthReports      *health.ReportStore
	healthErrors       *health.ErrorRingBuffer
	healthMetricsStore *observability.HealthMetricsStore
	healthEvents       *observability.HealthEventBuffer

	// selfProc caches the *process.Process for the current PID, created lazily on
	// first use. Reusing one instance lets Percent(0) report CPU consumed since
	// the previous sample (interval usage) instead of the lifetime average a
	// freshly created instance's CPUPercent() returns. selfProcMu guards it
	// because Percent(0) mutates the instance's stored sample and resource
	// requests may run concurrently.
	selfProc   *process.Process
	selfProcMu sync.Mutex

	// procSamples caches one *process.Process per PID for the process table, for the
	// same reason selfProc exists: process.Processes() hands back a freshly built
	// instance on every request, and a fresh instance's CPUPercent() is the lifetime
	// average since the process started, not what it is using now. Holding an instance
	// across requests is what lets Percent(0) report interval usage, so the table can
	// agree with the system CPU gauge instead of reporting a long-run average beside a
	// live reading.
	//
	// Entries are validated against the process create time because the OS reuses PIDs:
	// a recycled PID would otherwise be diffed against the dead process's CPU history.
	// pruneProcSamples drops dead PIDs so the map stays bounded by the live process
	// count. procSamplesMu guards both because Percent(0) mutates the instance's stored
	// sample and requests may run concurrently.
	procSamples   map[int32]*procSample
	procSamplesMu sync.Mutex
}

// procSample is a retained per-PID CPU sampler plus the create time that identifies
// which process the sampler's history belongs to.
type procSample struct {
	proc       *process.Process
	createTime int64
}

// New constructs the system handler from the shared core, the facade controller
// start time (for the diagnostics uptime check), an optional health error ring
// buffer seed (WithHealthErrorBuffer; nil when not injected, in which case
// RegisterDiagnosticsRoutes allocates its own) and the audio-level provider used
// by the diagnostics audio-level health check.
func New(core *apicore.Core, startTime *time.Time, healthErrorBuf *health.ErrorRingBuffer, audioLevelProvider func() []audiocore.AudioLevelData) *Handler {
	return &Handler{
		Core:               core,
		startTime:          startTime,
		healthErrors:       healthErrorBuf,
		audioLevelProvider: audioLevelProvider,
	}
}

// RegisterSystemRoutes registers the /api/v2/system/* information endpoints and
// the /system/events/* endpoints, and starts the background CPU usage sampler.
// It preserves the exact routes and per-route auth middleware from the original
// initSystemRoutes (minus the cross-domain audio/external-media/database routes,
// which the facade registers in its trimmed initSystemRoutes).
func (c *Handler) RegisterSystemRoutes(g *echo.Group) {
	c.LogInfoIfEnabled("Initializing system routes")

	// Start CPU usage monitoring in background with the controller's context for
	// controlled shutdown (Go 1.25 WaitGroup.Go()).
	c.Go(func() {
		apicore.UpdateCPUCache(c.Context())
	})
	c.LogInfoIfEnabled("Started CPU usage monitoring")

	systemGroup := g.Group("/system")
	authMiddleware := c.AuthMiddleware
	protectedGroup := systemGroup.Group("", authMiddleware)

	protectedGroup.GET("/info", c.GetSystemInfo)
	protectedGroup.GET("/resources", c.GetResourceInfo)
	protectedGroup.GET("/disks", c.GetDiskInfo)
	protectedGroup.GET("/jobs", c.GetJobQueueStats)
	protectedGroup.GET("/processes", c.GetProcessInfo)
	protectedGroup.GET("/temperature/cpu", c.GetSystemCPUTemperature)
	protectedGroup.GET("/database/stats", c.GetDatabaseStats)
	protectedGroup.GET("/database/v2/stats", c.GetV2DatabaseStats)
	protectedGroup.POST("/database/backup", c.DownloadDatabaseBackup)
	protectedGroup.GET("/network-interfaces", c.GetNetworkInterfaces)
	protectedGroup.GET("/restart-status", c.GetRestartStatus)
	protectedGroup.GET("/models", c.GetActiveModels)
	protectedGroup.GET("/inference", c.GetInferenceStatus)
	protectedGroup.GET("/update-check", c.GetUpdateCheck)

	// Events routes (detection lifecycle + operational logs).
	c.registerEventsRoutes(protectedGroup)

	c.LogInfoIfEnabled("System routes initialized successfully")
}

// RegisterTerminalRoutes registers the browser terminal websocket endpoint,
// preserving the exact route and auth middleware from the original
// initTerminalRoutes.
func (c *Handler) RegisterTerminalRoutes(g *echo.Group) {
	c.LogInfoIfEnabled("Initializing terminal routes")

	terminalGroup := g.Group("/terminal")
	protectedGroup := terminalGroup.Group("", c.AuthMiddleware)
	protectedGroup.GET("/ws", c.HandleTerminalWS)

	c.LogInfoIfEnabled("Terminal routes initialized successfully")
}
