// internal/api/v2/api.go
package api

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/patrickmn/go-cache"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/imports"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// Tunnel provider constant for unknown providers
const tunnelProviderUnknown = "unknown"

// apiV2Prefix is the base path prefix for all v2 API routes (the Echo group
// prefix; see Controller.Group). It is the single source of truth used to
// compose full route paths for PrivateMode exemption matching, so the exempt
// allow-list in isPrivateModeExempt cannot drift from the registered routes.
const apiV2Prefix = "/api/v2"

// Controller manages the API routes and handlers
type Controller struct {
	Echo  *echo.Echo
	Group *echo.Group
	DS    datastore.Interface           // Deprecated: Use Repo for new detection operations
	Repo  datastore.DetectionRepository // New: Preferred for detection CRUD operations
	// Settings holds this controller's settings snapshot as a lock-free atomic
	// pointer (copy-on-write). Update handlers publish a fresh *conf.Settings via
	// Settings.Store under settingsMutex; every reader Loads it, directly or
	// through the currentSettings()/controllerSettings() accessors. Storing it
	// atomically (rather than as a plain *conf.Settings field) is what lets
	// newErrorResponse read it from HandleError while UpdateSettings holds
	// settingsMutex.Lock without deadlocking on a non-reentrant RLock. The sibling
	// engine and audioWatchdog fields use the same atomic-pointer pattern.
	Settings          atomic.Pointer[conf.Settings]
	BirdImageCache    *imageprovider.BirdImageCache
	SunCalc           *suncalc.SunCalc
	Processor         *processor.Processor
	EBirdClient       *ebird.Client
	TaxonomyDB        *classifier.TaxonomyDatabase
	controlChan       chan string
	shutdownRequester ShutdownRequester // programmatic shutdown trigger (e.g., for restart)
	shutdownMu        sync.RWMutex      // protects shutdownRequester
	// DisableSaveSettings prevents persisting settings changes to disk.
	// When set to true, all settings modifications remain in memory only.
	// This is primarily used in testing but can be used in production for read-only mode.
	// Thread-safe: should be set before controller initialization.
	DisableSaveSettings  bool         // disables disk persistence of settings
	isGlobalOwner        bool         // true when this controller owns the global settings singleton
	settingsMutex        sync.RWMutex // Serializes the read-modify-write in settings update handlers; reads are lock-free via the atomic Settings pointer
	detectionCache       *cache.Cache // Cache for detection queries
	startTime            *time.Time
	SFS                  *securefs.SecureFS     // Add SecureFS instance
	apiLogger            logger.Logger          // Structured logger for API operations
	securityLogger       logger.Logger          // Logger scoped to the "security" module for authentication events
	metrics              *observability.Metrics // Shared metrics instance
	spectrogramGenerator *spectrogram.Generator // Shared spectrogram generator (initialized after SFS)

	// Auth related fields (injected from server via functional options)
	authService    auth.Service        // Authentication service (injected from server)
	authMiddleware echo.MiddlewareFunc // Authentication middleware function (injected from server)

	// notificationService is the notification service this controller uses. It is
	// nil in production, where getNotificationService() falls back to the
	// process-global singleton (notification.GetService()). Tests inject an
	// isolated per-test instance via WithNotificationService so each test gets its
	// own config and store without touching the global singleton.
	notificationService *notification.Service

	// Metrics history store for sparkline data
	metricsStore observability.MetricsStore

	// Detection rate cache for database overview endpoint
	detectionRateCache *datastore.DetectionRateCache

	// SSE related fields
	sseManager *SSEManager // Manager for Server-Sent Events connections

	// Cleanup related fields
	ctx    context.Context    // Context for managing goroutines
	cancel context.CancelFunc // Cancel function for graceful shutdown

	// Goroutine lifecycle management
	wg sync.WaitGroup // tracks background goroutines for clean shutdown

	// Audio level channel for SSE streaming
	// TODO: Consider moving to a dedicated audio manager
	audioLevelChan chan audiocore.AudioLevelData

	// engine provides access to the unified audio subsystem (sources, buffers, routing).
	// Stored atomically: written once via WithAudioEngine after Controller init,
	// read concurrently by HTTP handlers.
	engine atomic.Pointer[engine.AudioEngine]

	// V2Manager provides access to the v2 normalized database for stats and backup
	V2Manager datastoreV2.Manager

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

	// Model gallery fields
	ModelManager *classifier.ModelManager

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

	// audioWatchdog provides liveness state for audio health endpoints.
	// Stored atomically because it is set during pipeline Start() and read
	// concurrently by HTTP handlers.
	audioWatchdog atomic.Pointer[audiocore.LivenessWatchdog]

	// Health check infrastructure for the diagnostics endpoints.
	healthRegistry     *health.Registry
	healthReports      *health.ReportStore
	healthErrors       *health.ErrorRingBuffer
	healthMetricsStore *observability.HealthMetricsStore
	healthEvents       *observability.HealthEventBuffer

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
}

// SourceRestarterFunc restarts a single audio source identified by sourceID.
type SourceRestarterFunc func(sourceID string) error

// ShutdownRequester allows triggering a programmatic shutdown.
type ShutdownRequester interface {
	RequestShutdown()
}

// currentSettings returns the latest settings snapshot so UI changes
// take effect in API responses without restarting the service.
//
// It resolves the lock-free global atomic snapshot first and only falls back
// to this controller's own snapshot when no global snapshot has been published
// (standalone unit-test controllers). Both reads are lock-free (the per-controller
// fallback is an atomic Load), so the accessor is race-free against the
// Settings.Store the update handlers perform under c.settingsMutex.
func (c *Controller) currentSettings() *conf.Settings {
	if latest := conf.GetSettings(); latest != nil {
		return latest
	}
	return c.Settings.Load()
}

// controllerSettings returns this controller's own settings snapshot, read
// lock-free from the atomic Settings pointer that the update handlers publish on
// every save. Unlike currentSettings(), it deliberately does NOT consult the
// process-global atomic snapshot: use it for reads whose result is asserted
// per-controller (e.g. debug-gated response verbosity), where the shared global
// snapshot would couple otherwise-independent parallel tests.
//
// Loading the atomic pointer (rather than reading a plain field under
// settingsMutex.RLock) is what makes this safe to call from newErrorResponse,
// which is reached from HandleError while UpdateSettings already holds
// settingsMutex.Lock: a non-reentrant RLock there would deadlock. The snapshot is
// published under that same write lock, so the read sees a consistent value. The
// returned snapshot is immutable (copy-on-write), so callers may dereference its
// fields freely. Returns nil only on a controller that never stored settings
// (standalone tests); callers that may hit that path nil-check or fall back.
func (c *Controller) controllerSettings() *conf.Settings {
	return c.Settings.Load()
}

// Option is a functional option for configuring the Controller.
type Option func(*Controller)

// WithAuthMiddleware sets the authentication middleware for the controller.
func WithAuthMiddleware(mw echo.MiddlewareFunc) Option {
	return func(c *Controller) {
		c.authMiddleware = mw
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
		c.metricsStore = store
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
		c.engine.Store(e)
	}
}

// WithModelManager sets the ModelManager for model gallery operations.
func WithModelManager(mm *classifier.ModelManager) Option {
	return func(c *Controller) {
		c.ModelManager = mm
		// Wire the topology-changed callback so model add/remove broadcasts over
		// the metrics SSE stream. The method value binds c; c.metricsStore is read
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

// parseIPFromHeader attempts to parse a valid IP from a header value.
// Returns the IP string if valid, empty string otherwise.
func parseIPFromHeader(headerValue string) string {
	if headerValue == "" {
		return ""
	}
	// Strip IPv6 zone ID (e.g., %wlan0) before parsing.
	// net.ParseIP does not handle zone identifiers, and iOS Safari
	// commonly connects via IPv6 link-local addresses with zone IDs.
	if before, _, found := strings.Cut(headerValue, "%"); found {
		headerValue = before
	}
	ip := net.ParseIP(headerValue)
	if ip != nil {
		return ip.String()
	}
	return ""
}

// TunnelDetectionMiddleware inspects headers to determine if the request is likely proxied
// and sets context values for logging.
func (c *Controller) TunnelDetectionMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			req := ctx.Request()
			tunneled := false
			provider := tunnelProviderUnknown

			// Only classify the request as tunneled when the IP extractor actually
			// honored a forwarded header, i.e. the resolved client IP differs from
			// the immediate connection peer. That happens only for a trusted proxy,
			// so a directly-connected client cannot spoof a "tunneled" label by
			// sending forwarded headers from an untrusted address.
			if peerIP, _ := peerAddrFromRequest(req); peerIP != nil && peerIP.String() != ctx.RealIP() {
				switch {
				case req.Header.Get(headerCFConnectingIP) != "":
					tunneled = true
					provider = "cloudflare"
				case req.Header.Get(echo.HeaderXForwardedFor) != "" || req.Header.Get(echo.HeaderXRealIP) != "":
					// Other proxy headers present: tunneled, but provider is generic.
					tunneled = true
					provider = "generic"
				}
			}

			ctx.Set("is_tunneled", tunneled)
			ctx.Set("tunnel_provider", provider)

			return next(ctx)
		}
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

// resolveAndValidateMediaPath resolves a potentially relative media path and ensures it exists as a directory.
// Returns the absolute path and any error encountered.
func resolveAndValidateMediaPath(configPath string) (string, error) {
	mediaPath := configPath
	if mediaPath == "" {
		mediaPath = defaultExportPath
		GetLogger().Warn("Audio export path is empty, using default",
			logger.String("default_path", defaultExportPath),
		)
	}

	// Resolve relative path to absolute based on working directory
	if !filepath.IsAbs(mediaPath) {
		workDir, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get working directory to resolve relative media path: %w", err)
		}
		mediaPath = filepath.Join(workDir, mediaPath)
		GetLogger().Debug("Resolved relative media export path",
			logger.String("config_path", configPath),
			logger.String("absolute_path", mediaPath),
		)
	}

	// Ensure directory exists, creating if necessary
	if err := ensureDirectoryExists(mediaPath); err != nil {
		return "", err
	}

	return mediaPath, nil
}

// ensureDirectoryExists checks that a path exists and is a directory, creating it if needed.
func ensureDirectoryExists(path string) error {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, FilePermExecutable); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", path, err)
		}
		fi, err = os.Stat(path)
	}
	if err != nil {
		return fmt.Errorf("error checking path %q: %w", path, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("path is not a directory: %q", path)
	}
	return nil
}

// NewWithOptions creates a new API controller with optional route initialization.
// Set initializeRoutes to false for testing to avoid starting background goroutines.
func NewWithOptions(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string,
	metrics *observability.Metrics, initializeRoutes bool, opts ...Option) (*Controller, error) {

	// Validate and resolve media export path
	mediaPath, err := resolveAndValidateMediaPath(settings.Realtime.Audio.Export.Path)
	if err != nil {
		return nil, err
	}

	sfs, err := securefs.New(mediaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize secure filesystem for media: %w", err)
	}

	// Create context for managing goroutines
	ctx, cancel := context.WithCancel(context.Background())

	// Only create DetectionRepository if datastore is available.
	// This prevents nil pointer dereference when datastore is disabled.
	var repo datastore.DetectionRepository
	if ds != nil {
		repo = datastore.NewDetectionRepository(ds, nil)
	}

	c := &Controller{
		Echo:                 e,
		DS:                   ds,
		Repo:                 repo, // Bridge to new domain model (nil if datastore disabled)
		isGlobalOwner:        settings == conf.GetSettings(),
		BirdImageCache:       birdImageCache,
		SunCalc:              sunCalc,
		controlChan:          controlChan,
		detectionCache:       cache.New(detectionCacheExpiry, detectionCacheCleanup),
		SFS:                  sfs, // Assign SecureFS instance
		metrics:              metrics,
		ctx:                  ctx,
		cancel:               cancel,
		spectrogramGenerator: spectrogram.NewGenerator(settings, sfs, getSpectrogramLogger()),
		detectionRateCache:   datastore.NewDetectionRateCache(detectionRateCacheTTL),
		importMgr:            newImportManager(),
	}
	// Publish the initial settings snapshot so controllerSettings() reads it
	// lock-free. Every save republishes via Settings.Store; see the Settings
	// field doc and the settings update handlers.
	c.Settings.Store(settings)

	// Configure the trusted-proxy-gated IP extractor. Forwarded client-IP headers
	// (CF-Connecting-IP, X-Forwarded-For, X-Real-IP) are honored only when the
	// connection peer is a trusted proxy (loopback/link-local/private by default,
	// plus Security.TrustedProxies); otherwise the real peer address is used. It
	// reads this controller's own settings snapshot per request (published above
	// and on every save), so it honors the controller's TrustedProxies and
	// hot-reloads without a restart.
	e.IPExtractor = newTrustedProxyIPExtractor(c.controllerSettings)
	GetLogger().Info("Configured trusted-proxy-gated IP extractor (forwarded client IP honored only from trusted proxies)")

	// Propagate the derived FFprobe path from config validation to the
	// ffmpeg package so executeFFprobe can find it without PATH lookup.
	// Always set (even if empty) so repeated controller inits clear stale state.
	ffmpeg.SetFFprobePath(settings.Realtime.Audio.FfprobePath)

	// Initialize audio processing cache and concurrency limiter
	cacheDir := filepath.Join(c.SFS.BaseDir(), ".processing-cache")
	c.processingCache = newProcessingCache(cacheDir, processingCacheMaxFiles)
	c.processingSemaphore = make(chan struct{}, 2)

	// Start cache cleanup goroutine
	c.wg.Go(func() {
		ticker := time.NewTicker(processingCacheTickerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.processingCache.cleanExpired()
			}
		}
	})

	// Initialize structured logger for API requests
	c.apiLogger = logger.Global().Module("api")

	// Authentication events (form login/logout, OAuth callback) log to the
	// "security" module so they are co-located with the OAuth and provider-init
	// logging, where admins look when debugging auth.
	c.securityLogger = logger.Global().Module("security")

	// Load local taxonomy database for fast species lookups
	taxonomyDB, err := classifier.LoadTaxonomyDatabase()
	if err != nil {
		c.logWarnIfEnabled("Failed to load taxonomy database", logger.Error(err))
		c.logWarnIfEnabled("Species taxonomy lookups will fall back to eBird API")
		// Continue without taxonomy database - eBird API fallback will be used
		c.TaxonomyDB = nil
	} else {
		c.TaxonomyDB = taxonomyDB
		stats := taxonomyDB.Stats()
		c.logInfoIfEnabled("Loaded taxonomy database",
			logger.Any("genus_count", stats["genus_count"]),
			logger.Any("family_count", stats["family_count"]),
			logger.Any("species_count", stats["species_count"]),
			logger.String("version", taxonomyDB.Version),
			logger.String("updated_at", taxonomyDB.UpdatedAt),
		)
	}

	// Apply functional options (auth middleware and service injected from server)
	for _, opt := range opts {
		opt(c)
	}

	// Log auth configuration status
	log := GetLogger()
	if c.authMiddleware != nil {
		log.Info("Auth middleware configured via functional options")
	} else {
		log.Warn("Auth middleware not configured")
	}

	// Create v2 API group
	c.Group = e.Group(apiV2Prefix)

	// Configure middlewares
	c.Group.Use(middleware.Recover())          // Recover should be early
	c.Group.Use(c.TunnelDetectionMiddleware()) // Add tunnel detection **before** logging
	// c.Group.Use(middleware.Logger())        // Removed: Use custom LoggingMiddleware below for structured logging
	// NOTE: CORS middleware is configured at the global Echo level in server.go
	// Removing duplicate CORS here to avoid conflicts with global CORS configuration
	c.Group.Use(middleware.BodyLimit("1M")) // Limit request body to 1MB to prevent DoS attacks
	c.Group.Use(c.LoggingMiddleware())      // Use custom structured logging middleware
	c.Group.Use(c.privateModeAuth)          // Gate all API endpoints behind auth when PrivateMode is enabled

	// NOTE: CSRF token is provided by the /app/config endpoint using middleware.EnsureCSRFToken()
	// which handles Echo v4.15.0's Sec-Fetch-Site optimization that may skip token generation
	// for same-origin requests. Global CSRF middleware in server.go handles validation.

	// Initialize start time for uptime tracking
	now := time.Now()
	c.startTime = &now

	// Initialize SSE manager
	c.sseManager = NewSSEManager()

	// Initialize eBird client if enabled
	if settings.Realtime.EBird.Enabled {
		if settings.Realtime.EBird.APIKey == "" {
			// Create notification for missing API key
			// The Build() method automatically publishes to the event bus for notifications
			_ = errors.Newf("eBird integration enabled but API key not configured").
				Category(errors.CategoryConfiguration).
				Context("setting", "realtime.ebird.apikey").
				Component("ebird").
				Build()
			log.Warn("eBird integration enabled but API key not configured")
		} else {
			ebirdConfig := ebird.Config{
				APIKey:   settings.Realtime.EBird.APIKey,
				CacheTTL: time.Duration(settings.Realtime.EBird.CacheTTL) * time.Hour,
			}
			ebirdClient, err := ebird.NewClient(ebirdConfig)
			if err != nil {
				// Initialization error - already enhanced by ebird.NewClient
				log.Warn("Failed to initialize eBird client", logger.Error(err))
				// Continue without eBird client - it's not critical
			} else {
				c.EBirdClient = ebirdClient
				log.Info("Initialized eBird API client")
			}
		}
	} else {
		log.Debug("eBird integration disabled")
	}

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

// LoggingMiddleware creates a middleware function that logs API requests
func (c *Controller) LoggingMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			start := time.Now()

			// Process the request
			err := next(ctx)

			// Skip logging if apiLogger is not initialized
			if c.apiLogger == nil {
				return err
			}

			// Extract request information
			req := ctx.Request()
			res := ctx.Response()

			// Determine the actual status code. When a handler returns an
			// *echo.HTTPError, Echo's centralized error handler has not yet
			// executed at this point in the middleware chain, so res.Status
			// is still the default 200. Extract the real code from the error.
			status := res.Status
			if err != nil {
				var he *echo.HTTPError
				if errors.As(err, &he) {
					status = he.Code
				} else if status < http.StatusBadRequest {
					// Non-HTTP errors (e.g. database errors) won't have a
					// status set yet; Echo's error handler runs after this
					// middleware. Default to 500 to avoid logging failures
					// as successes.
					status = http.StatusInternalServerError
				}
			}

			// Get tunnel info from context
			isTunneled, _ := ctx.Get("is_tunneled").(bool)
			tunnelProvider, _ := ctx.Get("tunnel_provider").(string)

			// Log the request with structured data
			fields := []logger.Field{
				logger.String("method", req.Method),
				logger.String("path", req.URL.Path),
				logger.String("query", req.URL.RawQuery),
				logger.Int("status", status),
				logger.String("ip", ctx.RealIP()), // Uses custom extractor
				logger.Bool("tunneled", isTunneled),
				logger.String("tunnel_provider", tunnelProvider),
				logger.String("user_agent", req.UserAgent()),
				logger.Int64("latency_ms", time.Since(start).Milliseconds()),
			}
			if err != nil {
				fields = append(fields, logger.Error(err))
			}

			c.apiLogger.Info("API Request", fields...)

			return err
		}
	}
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
		{"weather routes", c.initWeatherRoutes},
		{"system routes", c.initSystemRoutes},
		{"terminal routes", c.initTerminalRoutes},
		{"settings routes", c.initSettingsRoutes},
		{"filesystem routes", c.initFileSystemRoutes},
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
		{"range routes", c.initRangeRoutes},
		{"heatmap routes", c.initHeatmapRoutes},
		{"sse routes", c.initSSERoutes},
		{"diagnostics routes", c.initDiagnosticsRoutes},
		{"metrics history routes", c.initMetricsHistoryRoutes},
		{"notification routes", c.initNotificationRoutes},
		{"support routes", c.initSupportRoutes},
		{"debug routes", c.initDebugRoutes},
		{"species routes", c.initSpeciesRoutes},
		{"dynamic threshold routes", c.initDynamicThresholdRoutes},
		{"alert routes", c.initAlertRoutes},
		{"model routes", c.initModelRoutes},
		{"insights routes", c.initInsightsRoutes},
		{"tls routes", c.initTLSRoutes},
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

// errDatastoreUnavailable is returned by DS-dependent handlers when the controller was
// constructed without a datastore. NewWithOptions permits a nil datastore ("datastore
// disabled" mode) and initRoutes skips registering the detection and media route groups
// in that mode; requireDatastore is defense in depth for any such handler reached anyway.
var errDatastoreUnavailable = errors.NewStd("datastore is not available")

// requireDatastore writes a 503 Service Unavailable response and returns the non-nil
// errDatastoreUnavailable when the controller has no datastore, so handlers can guard with:
//
//	if err := c.requireDatastore(ctx); err != nil {
//	    return err
//	}
//
// It returns the sentinel (not HandleError's nil) so the guard actually short-circuits the
// caller; the 503 body is already written, so echo's error handler skips the committed
// response. This honors the constructor's advertised "datastore disabled" mode instead of
// letting a nil c.DS dereference panic.
func (c *Controller) requireDatastore(ctx echo.Context) error {
	if c.DS == nil {
		_ = c.HandleError(ctx, errDatastoreUnavailable, "Datastore is not available", http.StatusServiceUnavailable)
		return errDatastoreUnavailable
	}
	return nil
}

// HealthCheck handles the API health check endpoint
func (c *Controller) HealthCheck(ctx echo.Context) error {
	// Read version/build/debug from this controller's own snapshot (nil-safe for
	// standalone test controllers).
	var version, buildDate string
	debug := false
	if settings := c.controllerSettings(); settings != nil {
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
	if c.sseManager != nil {
		c.sseManager.CloseAllClients()
	}

	// Stop alerting engine background goroutines and event bus
	if c.alertEngine != nil {
		c.alertEngine.Stop()
	}
	if bus := alerting.GetGlobalBus(); bus != nil {
		bus.Stop()
	}

	// Cancel context to stop all goroutines
	if c.cancel != nil {
		c.cancel()
	}

	// Wait for all goroutines to finish
	c.wg.Wait()

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
	if c.detectionCache != nil {
		c.detectionCache.Flush()
	}

	c.Debug("API Controller shutdown complete")
}

// SetShutdownRequester sets the shutdown requester for programmatic restart.
// Thread-safe: may be called after the HTTP server starts accepting requests.
func (c *Controller) SetShutdownRequester(sr ShutdownRequester) {
	c.shutdownMu.Lock()
	defer c.shutdownMu.Unlock()
	c.shutdownRequester = sr
}

// getShutdownRequester returns the current shutdown requester, or nil.
func (c *Controller) getShutdownRequester() ShutdownRequester {
	c.shutdownMu.RLock()
	defer c.shutdownMu.RUnlock()
	return c.shutdownRequester
}

// Error response structure
type ErrorResponse struct {
	Error         string         `json:"error"`
	Message       string         `json:"message"`
	Code          int            `json:"code"`
	CorrelationID string         `json:"correlation_id"`         // Unique identifier for tracking this error
	ErrorKey      string         `json:"error_key,omitempty"`    // i18n translation key for frontend
	ErrorParams   map[string]any `json:"error_params,omitempty"` // Interpolation parameters for error_key
}

// newErrorResponse creates a new API error response using the controller's
// injected settings to decide whether to expose raw error details.
func (c *Controller) newErrorResponse(err error, message string, code int) *ErrorResponse {
	// Generate a random correlation ID (8 characters should be sufficient)
	correlationID := generateCorrelationID()

	// Only expose raw err.Error() in debug mode: it can contain internal
	// paths, SQL errors, stack traces, etc. In production, use the
	// sanitized message parameter instead.
	var errorStr string
	// Read the controller's own debug flag via the lock-free atomic Settings pointer: this
	// is reached from HandleError while UpdateSettings holds c.settingsMutex, so
	// it must not acquire the lock (a non-reentrant RLock would deadlock), and the
	// flag must remain per-controller (handlers assert debug-gated error verbosity
	// per controller, not via the shared global snapshot). controllerSettings()
	// reads the per-controller snapshot published under that same write lock, so
	// the read is race-free. See controllerSettings.
	settings := c.controllerSettings()
	if err != nil && settings != nil && settings.WebServer.Debug {
		errorStr = err.Error()
	} else {
		errorStr = message
	}

	return &ErrorResponse{
		Error:         errorStr,
		Message:       message,
		Code:          code,
		CorrelationID: correlationID,
	}
}

// generateCorrelationID creates a unique identifier for error tracking using cryptographic randomness
// for better security and uniqueness guarantees across all platforms
func generateCorrelationID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 8

	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a default ID if crypto/rand fails
		return "ERR-RAND"
	}

	// Map the random bytes to charset characters
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

// handleErrorInternal is the shared implementation for HandleError and HandleErrorWithKey.
func (c *Controller) handleErrorInternal(ctx echo.Context, err error, message string, code int, errorKey string, errorParams map[string]any) error {
	errorResp := c.newErrorResponse(err, message, code)
	errorResp.ErrorKey = errorKey
	errorResp.ErrorParams = errorParams

	// Determine IP to log using the request context
	ip := ctx.RealIP()

	// Get tunnel info from context
	isTunneled, _ := ctx.Get("is_tunneled").(bool)
	tunnelProvider, _ := ctx.Get("tunnel_provider").(string)

	// Build error string for logging
	var errorStr string
	if err != nil {
		errorStr = err.Error()
	} else {
		errorStr = message
	}

	// Build log fields
	fields := []logger.Field{
		logger.String("correlation_id", errorResp.CorrelationID),
		logger.String("message", message),
		logger.String("error", errorStr),
		logger.Int("code", code),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("method", ctx.Request().Method),
		logger.String("ip", ip),
		logger.Bool("tunneled", isTunneled),
		logger.String("tunnel_provider", tunnelProvider),
	}
	if errorKey != "" {
		fields = append(fields, logger.String("error_key", errorKey))
	}

	c.logErrorIfEnabled("API Error", fields...)

	// Report server-side errors (5xx) to Sentry telemetry.
	// 4xx errors are client mistakes (bad input, not found), not bugs, and are excluded.
	if code >= http.StatusInternalServerError {
		c.reportErrorToTelemetry(ctx, err, message, code)
	}

	return ctx.JSON(code, errorResp)
}

// reportErrorToTelemetry reports server-side errors (5xx) to Sentry telemetry.
// Errors already reported by lower layers (e.g., datastore) are skipped to avoid
// duplicate Sentry events. Privacy scrubbing and opt-in checks are handled by the
// internal/errors and internal/telemetry packages.
func (c *Controller) reportErrorToTelemetry(ctx echo.Context, err error, message string, code int) {
	// Skip if the underlying error was already reported by a lower layer.
	if err != nil {
		var ee *errors.EnhancedError
		if errors.As(err, &ee) && ee.IsReported() {
			return
		}
	}

	// Client disconnects and request timeouts are not server bugs.
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return
	}

	path := ctx.Request().URL.Path
	method := ctx.Request().Method

	var builder *errors.ErrorBuilder
	if err != nil {
		builder = errors.New(err)
	} else {
		builder = errors.Newf("%s", message)
	}

	_ = builder.
		Component("api").
		Category(errors.CategoryHTTP).
		Context("http_status", code).
		Context("endpoint", path).
		Context("method", method).
		Build()
}

// HandleError constructs and returns an appropriate error response
func (c *Controller) HandleError(ctx echo.Context, err error, message string, code int) error {
	return c.handleErrorInternal(ctx, err, message, code, "", nil)
}

// HandleErrorWithKey constructs and returns an error response with an i18n translation key.
// The errorKey and errorParams allow the frontend to display translated error messages.
func (c *Controller) HandleErrorWithKey(ctx echo.Context, err error, message string, code int, errorKey string, errorParams map[string]any) error {
	return c.handleErrorInternal(ctx, err, message, code, errorKey, errorParams)
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

// Debug logs debug messages when debug mode is enabled.
//
// Reads the debug flag from the process-global snapshot via conf.GetSettings()
// (a lock-free atomic.Load), so process-wide debug logging follows the latest
// published global settings. Returns silently when the global snapshot has not
// been set (e.g. in unit tests with a standalone Controller); the per-controller
// c.Settings snapshot is deliberately not consulted here.
func (c *Controller) Debug(format string, v ...any) {
	settings := conf.GetSettings()
	if settings == nil {
		// Skip debug logging when the global snapshot hasn't been set
		// (e.g. in unit tests with a standalone Controller).
		return
	}
	if settings.WebServer.Debug {
		msg := fmt.Sprintf(format, v...)
		c.logDebugIfEnabled(msg)
	}
}

// logAPIRequest is a helper to log API requests with common context fields.
func (c *Controller) logAPIRequest(ctx echo.Context, level logger.LogLevel, msg string, fields ...logger.Field) {
	if c.apiLogger == nil {
		return // Do nothing if logger isn't initialized
	}

	// Extract common context info
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Create base fields with preallocated capacity
	baseFields := make([]logger.Field, 0, 2+len(fields))
	baseFields = append(baseFields,
		logger.String("path", path),
		logger.String("ip", ip),
	)

	// Append specific fields to base fields
	baseFields = append(baseFields, fields...)

	// Log at the specified level
	c.apiLogger.Log(level, msg, baseFields...)
}

// GetAuthMiddleware returns the authentication middleware function injected from server.
// This replaces the previous getEffectiveAuthMiddleware that had fallback logic.
//
// Returns nil if no middleware was configured via WithAuthMiddleware option.
// Callers should be aware that applying nil middleware to Echo routes is a no-op
// (the routes become unprotected). A warning is logged during initialization
// if auth middleware is not configured.
func (c *Controller) GetAuthMiddleware() echo.MiddlewareFunc {
	return c.authMiddleware
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
	apiController.logInfoIfEnabled("API v2 initialized",
		logger.String("version", settings.Version),
		logger.String("build_date", settings.BuildDate),
	)

	return apiController
}
