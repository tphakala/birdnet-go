// internal/api/v2/api.go
package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/filesystem"
	"github.com/tphakala/birdnet-go/internal/api/v2/models"
	rangeapi "github.com/tphakala/birdnet-go/internal/api/v2/range"
	"github.com/tphakala/birdnet-go/internal/api/v2/species"
	"github.com/tphakala/birdnet-go/internal/api/v2/support"
	tlsapi "github.com/tphakala/birdnet-go/internal/api/v2/tls"
	"github.com/tphakala/birdnet-go/internal/api/v2/weather"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/imports"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// apiV2Prefix is the base path prefix for all v2 API routes (the Echo group
// prefix; see Controller.Group). It is the single source of truth used to
// compose full route paths for PrivateMode exemption matching, so the exempt
// allow-list in isPrivateModeExempt cannot drift from the registered routes.
const apiV2Prefix = "/api/v2"

// Controller manages the API routes and handlers. It embeds the shared
// substrate *apicore.Core BY POINTER so every exported Core member (deps,
// settings accessors, error/log helpers, shared middleware, SSE hub and
// broadcasters) promotes onto *Controller and external callers keep working.
// Core holds atomic/lock-bearing fields, so it MUST be embedded by pointer and
// never copied by value; a single Core is constructed once in NewWithOptions
// and shared by this pointer.
type Controller struct {
	*apicore.Core

	// Domain handlers. Each owns a slice of the API surface and embeds the same
	// *apicore.Core; the facade constructs them once and calls their
	// RegisterRoutes in the deterministic order initRoutes defines.
	weather *weather.Handler

	// tlsHandler serves the /api/v2/tls/* certificate endpoints. It is named
	// tlsHandler (not tls) and the domain package is imported as tlsapi to avoid
	// colliding with the crypto/tls standard package. Unlike weather it also holds
	// the facade's settings-save machinery (see NewWithOptions) because TLS writes
	// mutate persisted settings.
	tlsHandler *tlsapi.Handler

	// rangeHandler serves the /api/v2/range/* endpoints (range-filter status,
	// species scores/count/list/CSV, test, rebuild). It is named rangeHandler
	// because "range" is a Go reserved word; the domain package is imported as
	// rangeapi. Like weather it needs only the shared *apicore.Core.
	rangeHandler *rangeapi.Handler

	// species serves the /api/v2/species/* and /api/v2/taxonomy/* endpoints
	// (species info, rarity, the all-species picker, the dictionary, thumbnails,
	// and genus/family/tree lookups). Besides the shared *apicore.Core it receives
	// two facade-owned function dependencies: a read accessor over the shared
	// scientific-to-common name map (loadCommonNameMap) and the media domain's
	// species-image proxy handler (ServeSpeciesImageProxy), both still owned by
	// package api until their domains are extracted.
	species *species.Handler

	// models serves the /api/v2/models/* endpoints (listing enabled classifier
	// models, browsing the gallery catalog, and install/reinstall/uninstall plus
	// streamed download progress). Like weather it needs only the shared
	// *apicore.Core (ModelManager, settings, error/log helpers, goroutine plumbing).
	models *models.Handler

	// support serves the /api/v2/support/* endpoints (generating a diagnostic
	// support dump, downloading it, and reporting telemetry status). Like weather
	// it needs only the shared *apicore.Core (settings, datastore, V2 manager,
	// error/log helpers, goroutine plumbing).
	support *support.Handler

	// filesystem serves the /api/v2/filesystem/* endpoints (the secure
	// file-browser endpoint backing the frontend directory picker). Like weather
	// it needs only the shared *apicore.Core (the media SecureFS sandbox, the auth
	// middleware, and the error/log helpers all promote from it).
	filesystem *filesystem.Handler

	controlChan chan string

	// DisableSaveSettings prevents persisting settings changes to disk.
	// When set to true, all settings modifications remain in memory only.
	// This is primarily used in testing but can be used in production for read-only mode.
	// Thread-safe: should be set before controller initialization.
	DisableSaveSettings bool         // disables disk persistence of settings
	isGlobalOwner       bool         // true when this controller owns the global settings singleton
	settingsMutex       sync.RWMutex // Serializes the read-modify-write in settings update handlers; reads are lock-free via the atomic Settings pointer

	startTime            *time.Time
	spectrogramGenerator *spectrogram.Generator // Shared spectrogram generator (initialized after SFS)

	// authService is the authentication service injected from server.
	authService auth.Service

	// notificationService is the notification service this controller uses. It is
	// nil in production, where getNotificationService() falls back to the
	// process-global singleton (notification.GetService()). Tests inject an
	// isolated per-test instance via WithNotificationService so each test gets its
	// own config and store without touching the global singleton.
	notificationService *notification.Service

	// Audio processing fields
	processingCache     *processingCache
	processingSemaphore chan struct{}

	// probeStreamInfo probes a live stream's audio characteristics for the
	// stream-test endpoint. Nil in production, where TestStream falls back to
	// ffmpeg.ProbeStreamInfo; tests set it to stub probing without ffprobe.
	probeStreamInfo probeStreamInfoFunc

	// Legacy cleanup state tracker
	cleanupStatus *CleanupStatus

	// Test synchronization fields (only populated when initializeRoutes is true)
	// goroutinesStarted signals when all background goroutines have successfully started.
	// This is primarily used in testing to ensure proper setup before assertions.
	// Only created when routes are initialized (production mode or specific tests).
	goroutinesStarted chan struct{} // signals when all background goroutines have started (nil if routes not initialized)

	// sourceRestarter restarts a single audio source by ID. Set during
	// pipeline Start() and called by the restart-source control endpoint.
	sourceRestarter atomic.Pointer[SourceRestarterFunc]

	// externalMediaEnv and externalMediaProbe are injectable dependencies for
	// the GET /api/v2/system/external-media endpoint. Both default to the real
	// sysinfo implementations at request time when nil.
	externalMediaEnv   sysinfo.EnvGetter
	externalMediaProbe sysinfo.MountProber

	// importMgr manages the one-at-a-time import lifecycle.
	importMgr *importManager

	// importSourceRoot is the directory under which import source paths must resolve.
	// Defaults to sysinfo.DefaultExternalMountPath when empty.
	importSourceRoot string

	// importSourceFactory builds an import Source from a resolved path.
	// Defaults to a BirdNET-Pi adapter when nil. Overridable in tests.
	importSourceFactory func(path string) (imports.Source, error)

	// ntfyCheckTimeoutOverride overrides the per-scheme ntfy connectivity probe
	// timeout used by CheckNtfyServer. Zero in production, where the probe uses
	// ntfyServerCheckTimeout. Tests set a short timeout so the unreachable-host
	// path returns quickly instead of waiting the full default for each scheme.
	ntfyCheckTimeoutOverride time.Duration

	// audioWaitTimeoutOverride overrides the server-side wait for an in-progress
	// audio encoding used by waitForAudioFile. Zero in production, where the wait
	// uses audioWaitTimeout. Tests set a short timeout to exercise the
	// 503-after-timeout path without waiting the full default.
	audioWaitTimeoutOverride time.Duration

	// Audio level channel for SSE streaming
	// TODO: Consider moving to a dedicated audio manager
	audioLevelChan chan audiocore.AudioLevelData

	// Application metadata repository (initialized lazily in initAppRoutes)
	appMetadataRepo repository.AppMetadataRepository

	// Alerting fields (initialized lazily in initAlertRoutes)
	alertRuleRepo repository.AlertRuleRepository
	alertEngine   *alerting.Engine

	// Insights fields (initialized lazily in initInsightsRoutes)
	insightsRepo repository.InsightsRepository
	nameMaps     atomic.Value // stores *nameMaps; see internal/api/v2/insights.go
	// nameResolver is the authoritative localized name source shared with the
	// classifier orchestrator. Overrides label-derived names in the cached maps.
	nameResolver atomic.Pointer[datastore.SpeciesNameResolver]

	// Health check infrastructure for the diagnostics endpoints (initialized lazily
	// in initDiagnosticsRoutes; healthErrors may be injected via WithHealthErrorBuffer).
	// These stay on the facade: the diagnostics and metrics-history handlers own
	// them, and HealthMetricsStore()/HealthEventBuffer() expose them to the analysis
	// pipeline as exported accessor methods (which would collide with exported fields).
	healthRegistry     *health.Registry
	healthReports      *health.ReportStore
	healthErrors       *health.ErrorRingBuffer
	healthMetricsStore *observability.HealthMetricsStore
	healthEvents       *observability.HealthEventBuffer
}

// SourceRestarterFunc restarts a single audio source identified by sourceID.
type SourceRestarterFunc func(sourceID string) error

// Option is a functional option for configuring the Controller.
type Option func(*Controller)

// WithAuthMiddleware sets the authentication middleware for the controller.
func WithAuthMiddleware(mw echo.MiddlewareFunc) Option {
	return func(c *Controller) {
		c.AuthMiddleware = mw
	}
}

// WithAuthService sets the authentication service for the controller.
func WithAuthService(svc auth.Service) Option {
	return func(c *Controller) {
		c.authService = svc
	}
}

// WithNotificationService injects the notification service the controller should
// use, overriding the process-global singleton. Production leaves this unset and
// falls back to notification.GetService(); tests use it to give each test an
// isolated service instance.
func WithNotificationService(svc *notification.Service) Option {
	return func(c *Controller) {
		c.notificationService = svc
	}
}

// WithMetricsStore sets the system metrics history store for the controller.
// This enables the metrics history and streaming endpoints.
func WithMetricsStore(store observability.MetricsStore) Option {
	return func(c *Controller) {
		c.MetricsStore = store
	}
}

// WithV2Manager sets the v2 database manager for the controller.
// This enables v2 database stats and backup endpoints.
func WithV2Manager(mgr datastoreV2.Manager) Option {
	return func(c *Controller) {
		c.V2Manager = mgr
	}
}

// WithAudioEngine sets the AudioEngine for audio subsystem access.
func WithAudioEngine(e *engine.AudioEngine) Option {
	return func(c *Controller) {
		c.Engine.Store(e)
	}
}

// WithModelManager sets the ModelManager for model gallery operations.
func WithModelManager(mm *classifier.ModelManager) Option {
	return func(c *Controller) {
		c.ModelManager = mm
		// Wire the topology-changed callback so model add/remove broadcasts over
		// the metrics SSE stream. The method value binds c; c.MetricsStore is read
		// lazily at call time, so option ordering is irrelevant.
		mm.SetTopologyChangedCallback(c.BroadcastInferenceTopologyChanged)
	}
}

// WithHealthErrorBuffer injects a shared ErrorRingBuffer created at startup.
// When set, initDiagnosticsRoutes uses this buffer instead of creating its own,
// enabling the logger to feed errors into the same buffer the health checks read.
func WithHealthErrorBuffer(buf *health.ErrorRingBuffer) Option {
	return func(c *Controller) {
		c.healthErrors = buf
	}
}

// New creates a new API controller, returning an error if initialization fails.
// The controller owns the global settings singleton: it reads from the current
// atomic snapshot on each request and publishes updates back via StoreSettings.
func New(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string,
	metrics *observability.Metrics, opts ...Option) (*Controller, error) {
	// Refresh from the global atomic pointer so the controller starts with
	// the latest snapshot, not a pointer captured before out-of-band updates
	// (range filter rebuild, ShouldUpdateRangeFilterToday, etc.).
	if global := conf.GetSettings(); global != nil {
		settings = global
	}
	c, err := NewWithOptions(e, ds, settings, birdImageCache, sunCalc, controlChan, metrics, true, opts...)
	if err != nil {
		return nil, err
	}
	// Force true: the pointer identity check in NewWithOptions can fail when
	// an out-of-band StoreSettings call (range filter rebuild at startup)
	// replaces the global pointer before this constructor runs.
	c.isGlobalOwner = true
	return c, nil
}

// NewWithOptions creates a new API controller with optional route initialization.
// Set initializeRoutes to false for testing to avoid starting background goroutines.
func NewWithOptions(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string,
	metrics *observability.Metrics, initializeRoutes bool, opts ...Option) (*Controller, error) {

	// Build the shared core (SecureFS, lifecycle context, IP extractor, loggers,
	// taxonomy, eBird client, SSE manager). The PrivateMode exempt allow-list is
	// supplied by the facade so it stays colocated with the domain route-path
	// constants.
	core, err := apicore.NewCore(e, ds, settings, birdImageCache, sunCalc, metrics, isPrivateModeExempt)
	if err != nil {
		return nil, err
	}

	c := &Controller{
		Core:          core,
		controlChan:   controlChan,
		isGlobalOwner: settings == conf.GetSettings(),
		importMgr:     newImportManager(),
	}

	// Construct domain handlers around the shared core. They hold the same
	// *apicore.Core pointer and register their routes in initRoutes.
	c.weather = weather.New(c.Core)
	c.rangeHandler = rangeapi.New(c.Core)
	// The TLS handler needs the facade's settings-save machinery: the shared
	// settingsMutex (passed by pointer so TLS certificate writes serialize against
	// the main settings update handlers) and the bound method values for reading,
	// publishing/persisting, and post-processing settings changes. c is fully
	// constructed here, so the &c.settingsMutex address and the method values are
	// stable for the controller's lifetime.
	c.tlsHandler = tlsapi.New(c.Core, &c.settingsMutex,
		c.getSettingsOrFallback, c.publishAndSaveSettings, c.handleSettingsChanges)
	// The species handler delegates to two facade-owned dependencies that have not
	// been extracted into their own domains yet: loadCommonNameMap (the shared
	// name-map read accessor) and ServeSpeciesImageProxy (the media image proxy the
	// thumbnail endpoint forwards to). They are passed as bound method values; c is
	// fully constructed here, so the method values are stable for its lifetime.
	c.species = species.New(c.Core, c.loadCommonNameMap, c.ServeSpeciesImageProxy)
	// The models handler needs only the shared core (ModelManager and the
	// settings/error/log/goroutine helpers all promote from it).
	c.models = models.New(c.Core)
	// The support handler needs only the shared core (settings, datastore, V2
	// manager, and the error/log/goroutine helpers all promote from it).
	c.support = support.New(c.Core)
	// The filesystem handler needs only the shared core (the media SecureFS
	// sandbox, the auth middleware, and the error/log helpers all promote from it).
	c.filesystem = filesystem.New(c.Core)

	// Initialize audio processing cache and concurrency limiter
	cacheDir := filepath.Join(c.SFS.BaseDir(), ".processing-cache")
	c.processingCache = newProcessingCache(cacheDir, processingCacheMaxFiles)
	c.processingSemaphore = make(chan struct{}, 2)

	// Start cache cleanup goroutine (tracked by the core wait group)
	c.Go(func() {
		ticker := time.NewTicker(processingCacheTickerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-c.Context().Done():
				return
			case <-ticker.C:
				c.processingCache.cleanExpired()
			}
		}
	})

	// Spectrogram generator (needs the media SecureFS)
	c.spectrogramGenerator = spectrogram.NewGenerator(settings, c.SFS, getSpectrogramLogger())

	// Apply functional options (auth middleware and service injected from server)
	for _, opt := range opts {
		opt(c)
	}

	// Log auth configuration status
	log := GetLogger()
	if c.AuthMiddleware != nil {
		log.Info("Auth middleware configured via functional options")
	} else {
		log.Warn("Auth middleware not configured")
	}

	// Create v2 API group
	c.Group = e.Group(apiV2Prefix)

	// Configure middlewares (applied once, in this order)
	c.Group.Use(middleware.Recover())          // Recover should be early
	c.Group.Use(c.TunnelDetectionMiddleware()) // Add tunnel detection **before** logging
	// c.Group.Use(middleware.Logger())        // Removed: Use custom LoggingMiddleware below for structured logging
	// NOTE: CORS middleware is configured at the global Echo level in server.go
	// Removing duplicate CORS here to avoid conflicts with global CORS configuration
	c.Group.Use(middleware.BodyLimit("1M")) // Limit request body to 1MB to prevent DoS attacks
	c.Group.Use(c.LoggingMiddleware())      // Use custom structured logging middleware
	c.Group.Use(c.PrivateModeAuth)          // Gate all API endpoints behind auth when PrivateMode is enabled

	// NOTE: CSRF token is provided by the /app/config endpoint using middleware.EnsureCSRFToken()
	// which handles Echo v4.15.0's Sec-Fetch-Site optimization that may skip token generation
	// for same-origin requests. Global CSRF middleware in server.go handles validation.

	// Initialize start time for uptime tracking
	now := time.Now()
	c.startTime = &now

	// Initialize routes if requested (skip in tests to avoid starting background goroutines)
	if initializeRoutes {
		// Initialize synchronization channel for testing
		c.goroutinesStarted = make(chan struct{})
		c.initRoutes()
		// Signal that all goroutines have started
		close(c.goroutinesStarted)
	}

	return c, nil // Return controller and nil error
}

// initRoutes registers all API endpoints
func (c *Controller) initRoutes() {
	// Health check endpoint - publicly accessible
	c.Group.GET("/health", c.HealthCheck)

	// Ping endpoint - ultra-lightweight connectivity check, publicly accessible
	c.Group.GET("/ping", c.Ping)

	// Initialize route groups with proper error handling and logging
	routeInitializers := []struct {
		name string
		fn   func()
	}{
		{"app routes", c.initAppRoutes},
		{"search routes", c.initSearchRoutes},
		{"detection routes", c.initDetectionRoutes},
		{"analytics routes", c.initAnalyticsRoutes},
		{"weather routes", func() { c.weather.RegisterRoutes(c.Group) }},
		{"system routes", c.initSystemRoutes},
		{"terminal routes", c.initTerminalRoutes},
		{"settings routes", c.initSettingsRoutes},
		{"filesystem routes", func() { c.filesystem.RegisterRoutes(c.Group) }},
		{"stream health routes", c.initStreamHealthRoutes},
		{"stream test routes", c.initStreamTestRoutes},
		{"audio health routes", c.initAudioHealthRoutes},
		{"quiet hours routes", c.initQuietHoursRoutes},
		{"audio level routes", c.initAudioLevelRoutes},
		{"hls streaming routes", c.initHLSRoutes},
		{"integration routes", c.initIntegrationsRoutes},
		{"control routes", c.initControlRoutes},
		{"auth routes", c.initAuthRoutes},
		{"media routes", c.initMediaRoutes},
		{"range routes", func() { c.rangeHandler.RegisterRoutes(c.Group) }},
		{"heatmap routes", c.initHeatmapRoutes},
		{"sse routes", c.initSSERoutes},
		{"diagnostics routes", c.initDiagnosticsRoutes},
		{"metrics history routes", c.initMetricsHistoryRoutes},
		{"notification routes", c.initNotificationRoutes},
		{"support routes", func() { c.support.RegisterRoutes(c.Group) }},
		{"debug routes", c.initDebugRoutes},
		{"species routes", func() { c.species.RegisterRoutes(c.Group) }},
		{"dynamic threshold routes", c.initDynamicThresholdRoutes},
		{"alert routes", c.initAlertRoutes},
		{"model routes", func() { c.models.RegisterRoutes(c.Group) }},
		{"insights routes", c.initInsightsRoutes},
		{"tls routes", func() { c.tlsHandler.RegisterRoutes(c.Group) }},
		{"import routes", c.initImportRoutes},
	}

	for _, initializer := range routeInitializers {
		c.Debug("Initializing %s...", initializer.name)

		// Use a deferred function to recover from panics during route initialization
		func() {
			defer func() {
				if r := recover(); r != nil {
					GetLogger().Error("PANIC during route initialization",
						logger.String("route", initializer.name),
						logger.Any("panic", r),
					)
				}
			}()

			// Call the initializer
			initializer.fn()

			c.Debug("Successfully initialized %s", initializer.name)
		}()
	}
}

// HealthCheck handles the API health check endpoint
func (c *Controller) HealthCheck(ctx echo.Context) error {
	// Read version/build/debug from this controller's own snapshot (nil-safe for
	// standalone test controllers).
	var version, buildDate string
	debug := false
	if settings := c.ControllerSettings(); settings != nil {
		version = settings.Version
		buildDate = settings.BuildDate
		debug = settings.WebServer.Debug
	}

	// Create response structure
	response := map[string]any{
		"status":     "healthy",
		"version":    version,
		"build_date": buildDate,
		"timestamp":  time.Now().Format(time.RFC3339),
	}

	// Add environment based on debug mode
	if debug {
		response["environment"] = "development"
	} else {
		response["environment"] = "production"
	}

	// Check database connectivity - simple check if we can access the datastore.
	// c.DS may be nil in the "datastore disabled" mode (see NewWithOptions); report
	// that instead of dereferencing a nil datastore.
	dbStatus := "connected"
	var dbError string

	if c.DS == nil {
		dbStatus = "unavailable"
	} else if _, dbErr := c.DS.GetLastDetections(1); dbErr != nil {
		// Try a simple database operation to check connectivity
		dbStatus = "disconnected"
		dbError = dbErr.Error()
		// If database is critical, we might want to change the overall status
		// response["status"] = "degraded"
	}

	response["database_status"] = dbStatus
	if dbError != "" {
		response["database_error"] = dbError
	}

	// Add uptime if available
	if c.startTime != nil {
		uptime := time.Since(*c.startTime)
		response["uptime"] = uptime.String()
		response["uptime_seconds"] = uptime.Seconds()
	}

	// Add system metrics
	systemMetrics := make(map[string]any)

	// CPU usage from cached background sampler
	cpuPercent := GetCachedCPUUsage()
	if len(cpuPercent) > 0 {
		systemMetrics["cpu_usage"] = cpuPercent[0]
	} else {
		systemMetrics["cpu_usage"] = 0.0
	}

	// Memory usage via gopsutil
	memoryMetrics := map[string]any{
		"used_percent": 0.0,
		"total_mb":     0.0,
		"used_mb":      0.0,
	}
	if memInfo, err := mem.VirtualMemory(); err == nil {
		memoryMetrics["used_percent"] = memInfo.UsedPercent
		memoryMetrics["total_mb"] = float64(memInfo.Total) / 1024 / 1024
		memoryMetrics["used_mb"] = float64(memInfo.Used) / 1024 / 1024
	}
	systemMetrics["memory"] = memoryMetrics

	// Disk usage via gopsutil (root partition)
	diskMetrics := map[string]any{
		"total_gb":     0.0,
		"free_gb":      0.0,
		"used_percent": 0.0,
	}
	if diskInfo, err := disk.Usage("/"); err == nil {
		diskMetrics["total_gb"] = float64(diskInfo.Total) / 1024 / 1024 / 1024
		diskMetrics["free_gb"] = float64(diskInfo.Free) / 1024 / 1024 / 1024
		diskMetrics["used_percent"] = diskInfo.UsedPercent
	}
	systemMetrics["disk_space"] = diskMetrics

	// Add system metrics to response
	response["system"] = systemMetrics

	return ctx.JSON(http.StatusOK, response)
}

// Shutdown performs cleanup of all resources used by the API controller
// This should be called when the application is shutting down
func (c *Controller) Shutdown() {
	// Close all SSE clients first so echo.Shutdown() has no active
	// connections to wait for. SSE handlers block on request context
	// which only closes when echo shuts down, creating a circular wait.
	if c.SSEManager != nil {
		c.SSEManager.CloseAllClients()
	}

	// Stop alerting engine background goroutines and event bus
	if c.alertEngine != nil {
		c.alertEngine.Stop()
	}
	if bus := alerting.GetGlobalBus(); bus != nil {
		bus.Stop()
	}

	// Cancel context to stop all goroutines, then wait for them to finish.
	c.Cancel()
	c.Wait()

	// Release the media SecureFS sandbox handle (an open os.Root): otherwise it
	// leaks across controller restarts, and on Windows the open directory handle
	// blocks t.TempDir() cleanup of the export dir in tests. This runs before
	// echo.Shutdown() drains in-flight HTTP requests, so a request still reading
	// the media filesystem in the brief shutdown window can observe os.ErrClosed
	// (surfaced as a 5xx, no panic) - acceptable for a shutting-down process.
	// Draining first was rejected because it would delay cancelling
	// controller-context-bound streaming handlers.
	if c.SFS != nil {
		if err := c.SFS.Close(); err != nil {
			GetLogger().Error("Error closing media SecureFS", logger.Error(err))
		}
	}

	// Shutdown the backup job manager to stop its cleanup goroutine
	if backupJobManager != nil {
		backupJobManager.Shutdown()
	}

	// Flush all log writers (the main writer plus every module writer, including
	// the security module added for auth events). A module logger's Flush is a
	// no-op, so flush the central logger to actually persist buffered logs on
	// shutdown.
	if err := logger.Global().Flush(); err != nil {
		GetLogger().Error("Error flushing logs", logger.Error(err))
	}

	// TODO: The go-cache library's janitor goroutine cannot be stopped.
	// Consider migrating to a context-aware cache implementation.
	if c.DetectionCache != nil {
		c.DetectionCache.Flush()
	}

	c.Debug("API Controller shutdown complete")
}

// HandleErrorForTest constructs and returns an echo.HTTPError for testing purposes
// This method is used in tests where echo.HTTPError is expected for error assertions
func (c *Controller) HandleErrorForTest(ctx echo.Context, err error, message string, code int) error {
	// Include the original error message for better test assertions
	fullMessage := message
	if err != nil {
		fullMessage = fmt.Sprintf("%s: %v", message, err)
	}
	return echo.NewHTTPError(code, fullMessage)
}

// InitializeAPI creates a new API controller and registers all routes.
// Auth middleware and service should be passed via functional options.
func InitializeAPI(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string, proc *processor.Processor,
	metrics *observability.Metrics, opts ...Option) *Controller {

	// Create API controller with metrics and functional options
	apiController, err := New(e, ds, settings, birdImageCache, sunCalc, controlChan, metrics, opts...)
	if err != nil {
		GetLogger().Error("Failed to initialize API", logger.Error(err))
		os.Exit(1)
	}

	// Assign processor after initialization
	apiController.Processor = proc

	// Log initialization
	apiController.LogInfoIfEnabled("API v2 initialized",
		logger.String("version", settings.Version),
		logger.String("build_date", settings.BuildDate),
	)

	return apiController
}
