package apicore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/patrickmn/go-cache"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Cache tuning constants for the shared detection caches owned by Core.
const (
	detectionCacheExpiry  = 5 * time.Minute  // Default detection-query cache expiration
	detectionCacheCleanup = 10 * time.Minute // Detection-query cache cleanup interval
	detectionRateCacheTTL = 5 * time.Minute  // Detection-rate cache TTL (database overview)
)

// Default path and permission constants used while resolving the media export root.
const (
	// defaultExportPath is the default audio clip export directory, matching internal/conf/defaults.go.
	defaultExportPath = "clips/"
	// mediaDirPerm is the permission used when creating the media export directory.
	mediaDirPerm = 0o755
)

// ShutdownRequester allows triggering a programmatic shutdown.
type ShutdownRequester interface {
	RequestShutdown()
}

// Core is the shared substrate of the API v2 controller. The api facade embeds
// *Core by pointer so every exported Core member promotes onto *api.Controller
// (and, in later phases, onto each domain handler type). Core holds atomic and
// lock-bearing fields, so it must NEVER be copied by value: embed *Core, never
// Core. The single instance is created once in NewCore and shared by pointer.
type Core struct {
	Echo  *echo.Echo
	Group *echo.Group
	DS    datastore.Interface           // Deprecated: Use Repo for new detection operations
	Repo  datastore.DetectionRepository // New: Preferred for detection CRUD operations
	// Settings holds this controller's settings snapshot as a lock-free atomic
	// pointer (copy-on-write). Update handlers publish a fresh *conf.Settings via
	// Settings.Store under the facade settings mutex; every reader Loads it, directly or
	// through the CurrentSettings()/ControllerSettings() accessors. Storing it
	// atomically (rather than as a plain *conf.Settings field) is what lets
	// newErrorResponse read it from HandleError while UpdateSettings holds
	// the settings write lock without deadlocking on a non-reentrant RLock. The sibling
	// Engine and AudioWatchdog fields use the same atomic-pointer pattern.
	Settings       atomic.Pointer[conf.Settings]
	BirdImageCache *imageprovider.BirdImageCache
	SunCalc        *suncalc.SunCalc
	Processor      *processor.Processor
	EBirdClient    *ebird.Client
	TaxonomyDB     *classifier.TaxonomyDatabase
	SFS            *securefs.SecureFS     // SecureFS instance for media
	APILogger      logger.Logger          // Structured logger for API operations
	Metrics        *observability.Metrics // Shared metrics instance

	// AuthMiddleware is the authentication middleware function (injected from server
	// via WithAuthMiddleware). Read by PrivateModeAuth, GetAuthMiddleware, and the
	// per-route protected-group registrations.
	AuthMiddleware echo.MiddlewareFunc

	// MetricsStore holds the metrics history store for sparkline data and the
	// inference-topology broadcast (BroadcastInferenceTopologyChanged).
	MetricsStore observability.MetricsStore

	// DetectionCache caches detection queries.
	DetectionCache *cache.Cache
	// DetectionRateCache caches detection rate results for the database overview endpoint.
	DetectionRateCache *datastore.DetectionRateCache

	// SSEManager manages Server-Sent Events connections.
	SSEManager *SSEManager

	// V2Manager provides access to the v2 normalized database for stats and backup.
	V2Manager datastoreV2.Manager

	// ModelManager backs the model gallery operations.
	ModelManager *classifier.ModelManager

	// Engine provides access to the unified audio subsystem (sources, buffers, routing).
	// Stored atomically: written once via WithAudioEngine after Core init, read
	// concurrently by HTTP handlers.
	Engine atomic.Pointer[engine.AudioEngine]

	// AudioWatchdog provides liveness state for audio health endpoints. Stored
	// atomically because it is set during pipeline Start() and read concurrently by HTTP handlers.
	AudioWatchdog atomic.Pointer[audiocore.LivenessWatchdog]

	// securityLogger is scoped to the "security" module for authentication events.
	securityLogger logger.Logger

	// shutdownRequester is the programmatic shutdown trigger (e.g., for restart).
	shutdownRequester ShutdownRequester
	shutdownMu        sync.RWMutex // protects shutdownRequester

	// Goroutine lifecycle management.
	ctx    context.Context    // Context for managing goroutines
	cancel context.CancelFunc // Cancel function for graceful shutdown
	wg     sync.WaitGroup     // tracks background goroutines for clean shutdown

	// privateModeExempt reports whether a (method, path) pair is exempt from the
	// PrivateMode auth gate. Injected by the facade so the exempt allow-list stays
	// colocated with the domain route-path constants and registrations.
	privateModeExempt func(method, path string) bool
}

// NewCore builds the shared API v2 substrate. It resolves the media export root,
// creates the SecureFS sandbox and the cancellation context, wires the
// trusted-proxy IP extractor, loads the taxonomy database, initializes the eBird
// client (when enabled) and the SSE manager. The functional options (auth
// middleware, audio engine, etc.) and the echo Group + group middleware are
// applied by the facade after construction.
func NewCore(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	metrics *observability.Metrics,
	privateModeExempt func(method, path string) bool) (*Core, error) {

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

	c := &Core{
		Echo:               e,
		DS:                 ds,
		Repo:               repo, // Bridge to new domain model (nil if datastore disabled)
		BirdImageCache:     birdImageCache,
		SunCalc:            sunCalc,
		DetectionCache:     cache.New(detectionCacheExpiry, detectionCacheCleanup),
		SFS:                sfs, // Assign SecureFS instance
		Metrics:            metrics,
		ctx:                ctx,
		cancel:             cancel,
		DetectionRateCache: datastore.NewDetectionRateCache(detectionRateCacheTTL),
		privateModeExempt:  privateModeExempt,
	}
	// Publish the initial settings snapshot so ControllerSettings() reads it
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
	e.IPExtractor = newTrustedProxyIPExtractor(c.ControllerSettings)
	GetLogger().Info("Configured trusted-proxy-gated IP extractor (forwarded client IP honored only from trusted proxies)")

	// Propagate the derived FFprobe path from config validation to the
	// ffmpeg package so executeFFprobe can find it without PATH lookup.
	// Always set (even if empty) so repeated controller inits clear stale state.
	ffmpeg.SetFFprobePath(settings.Realtime.Audio.FfprobePath)

	// Initialize structured logger for API requests
	c.APILogger = logger.Global().Module("api")

	// Authentication events (form login/logout, OAuth callback) log to the
	// "security" module so they are co-located with the OAuth and provider-init
	// logging, where admins look when debugging auth.
	c.securityLogger = logger.Global().Module("security")

	// Load local taxonomy database for fast species lookups
	taxonomyDB, err := classifier.LoadTaxonomyDatabase()
	if err != nil {
		c.LogWarnIfEnabled("Failed to load taxonomy database", logger.Error(err))
		c.LogWarnIfEnabled("Species taxonomy lookups will fall back to eBird API")
		// Continue without taxonomy database - eBird API fallback will be used
		c.TaxonomyDB = nil
	} else {
		c.TaxonomyDB = taxonomyDB
		stats := taxonomyDB.Stats()
		c.LogInfoIfEnabled("Loaded taxonomy database",
			logger.Any("genus_count", stats["genus_count"]),
			logger.Any("family_count", stats["family_count"]),
			logger.Any("species_count", stats["species_count"]),
			logger.String("version", taxonomyDB.Version),
			logger.String("updated_at", taxonomyDB.UpdatedAt),
		)
	}

	// Initialize SSE manager
	c.SSEManager = NewSSEManager()

	// Initialize eBird client if enabled
	log := GetLogger()
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
		if err := os.MkdirAll(path, mediaDirPerm); err != nil {
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

// Context returns the controller lifecycle context. Background goroutines
// derive their cancellation from it so they stop on Shutdown.
func (c *Core) Context() context.Context {
	return c.ctx
}

// SetTestContext installs a lifecycle context and cancel function on a Core that
// was built directly (not via NewCore). It exists solely so tests can drive the
// context-cancellation paths of handlers that read Context(); production code
// installs these inside NewCore. A nil cancel is permitted (Cancel becomes a no-op).
func (c *Core) SetTestContext(ctx context.Context, cancel context.CancelFunc) {
	c.ctx = ctx
	c.cancel = cancel
}

// Go runs fn in a tracked goroutine so Shutdown can wait for it to finish.
func (c *Core) Go(fn func()) {
	c.wg.Go(fn)
}

// Cancel cancels the controller lifecycle context, signalling background
// goroutines to stop. Safe to call when no context was created.
func (c *Core) Cancel() {
	if c.cancel != nil {
		c.cancel()
	}
}

// Wait blocks until all goroutines started via Go have returned.
func (c *Core) Wait() {
	c.wg.Wait()
}

// Debug logs debug messages when debug mode is enabled.
//
// Reads the debug flag from the process-global snapshot via conf.GetSettings()
// (a lock-free atomic.Load), so process-wide debug logging follows the latest
// published global settings. Returns silently when the global snapshot has not
// been set (e.g. in unit tests with a standalone Core); the per-controller
// Settings snapshot is deliberately not consulted here.
func (c *Core) Debug(format string, v ...any) {
	settings := conf.GetSettings()
	if settings == nil {
		// Skip debug logging when the global snapshot hasn't been set
		// (e.g. in unit tests with a standalone Core).
		return
	}
	if settings.WebServer.Debug {
		msg := fmt.Sprintf(format, v...)
		c.LogDebugIfEnabled(msg)
	}
}

// SetShutdownRequester sets the shutdown requester for programmatic restart.
// Thread-safe: may be called after the HTTP server starts accepting requests.
func (c *Core) SetShutdownRequester(sr ShutdownRequester) {
	c.shutdownMu.Lock()
	defer c.shutdownMu.Unlock()
	c.shutdownRequester = sr
}

// GetShutdownRequester returns the current shutdown requester, or nil.
func (c *Core) GetShutdownRequester() ShutdownRequester {
	c.shutdownMu.RLock()
	defer c.shutdownMu.RUnlock()
	return c.shutdownRequester
}
