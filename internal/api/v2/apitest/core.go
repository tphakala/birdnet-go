package apitest

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Default coordinates for the test SunCalc (Helsinki, Finland).
const (
	testLatitude  = 60.1699
	testLongitude = 24.9384
)

// apiV2Prefix is the echo route-group prefix the facade registers domain routes
// under. NewCore sets core.Group to echo.Group(apiV2Prefix) so domain handler
// tests register routes the same way the facade does. It mirrors the facade's own
// apiV2Prefix; apitest cannot import package api to share the literal.
const apiV2Prefix = "/api/v2"

// coreConfig holds the resolved inputs for NewCore. All fields are optional; any
// left unset get a test default in NewCore.
type coreConfig struct {
	echo              *echo.Echo
	ds                datastore.Interface
	dsSet             bool
	settings          *conf.Settings
	settingsFuncs     []func(*conf.Settings)
	birdImageCache    *imageprovider.BirdImageCache
	sunCalc           *suncalc.SunCalc
	metrics           *observability.Metrics
	privateModeExempt func(method, path string) bool
	publishSettings   bool
}

// CoreOption configures NewCore.
type CoreOption func(*coreConfig)

// WithEcho injects an existing Echo instance instead of echo.New().
func WithEcho(e *echo.Echo) CoreOption { return func(c *coreConfig) { c.echo = e } }

// WithDatastore injects a datastore (typically a mocks.MockInterface a test sets
// expectations on). Passing nil exercises the datastore-disabled path. When this
// option is omitted, NewCore creates a fresh mocks.NewMockInterface(t).
func WithDatastore(ds datastore.Interface) CoreOption {
	return func(c *coreConfig) { c.ds = ds; c.dsSet = true }
}

// WithSettings injects a *conf.Settings instead of NewValidTestSettings(). The
// media export path is still overridden to a per-test t.TempDir() for isolation
// unless a WithSettingsFunc restores it.
func WithSettings(s *conf.Settings) CoreOption { return func(c *coreConfig) { c.settings = s } }

// WithSettingsFunc registers a mutator applied to the resolved settings after the
// t.TempDir() export-path override, so a test can tweak (or deliberately replace)
// fields. Multiple mutators run in registration order.
func WithSettingsFunc(fn func(*conf.Settings)) CoreOption {
	return func(c *coreConfig) { c.settingsFuncs = append(c.settingsFuncs, fn) }
}

// WithBirdImageCache injects a bird image cache instead of NewMockBirdImageCache.
func WithBirdImageCache(cache *imageprovider.BirdImageCache) CoreOption {
	return func(c *coreConfig) { c.birdImageCache = cache }
}

// WithSunCalc injects a SunCalc instead of the default Helsinki-coordinate one.
func WithSunCalc(sc *suncalc.SunCalc) CoreOption { return func(c *coreConfig) { c.sunCalc = sc } }

// WithMetrics injects an observability.Metrics instead of NewTestMetrics(t).
func WithMetrics(m *observability.Metrics) CoreOption { return func(c *coreConfig) { c.metrics = m } }

// WithPrivateModeExempt injects the PrivateMode exempt predicate consulted by the
// core's PrivateModeAuth middleware. The default exempts nothing (returns false).
func WithPrivateModeExempt(fn func(method, path string) bool) CoreOption {
	return func(c *coreConfig) { c.privateModeExempt = fn }
}

// WithoutSettingsPublish skips publishing the settings to the process-global
// snapshot. Use it for parallel tests that do not read CurrentSettings() from the
// global, or that manage the global snapshot themselves.
func WithoutSettingsPublish() CoreOption { return func(c *coreConfig) { c.publishSettings = false } }

// NewCore builds a fully wired *apicore.Core for tests and registers cleanup. It
// honors the apitest isolation contract: the media export path is a per-test
// t.TempDir(), the datastore is a mock (no shared DB file), and the Echo server
// is in-memory (no fixed TCP port). Future domain tests build their handler from
// it directly, for example:
//
//	h := analytics.New(apitest.NewCore(t))
//
// Common overrides go through the CoreOption functions; tests needing mock
// expectations pass their own mock via WithDatastore and keep the reference (the
// default mock is reachable as core.DS). The returned core has its v2 route group
// (core.Group) wired so a domain test can call h.RegisterRoutes(core.Group); the
// facade's group-level middleware is not applied (see the wiring comment below).
//
// By default NewCore publishes its settings to the process-global snapshot so
// handlers reading CurrentSettings() observe them. That snapshot is process-wide,
// not per-core: building two publishing cores in one test leaves the second core's
// settings as the global, so the first core's CurrentSettings() would observe the
// second's. Build the second with WithoutSettingsPublish, and a test that publishes
// must not call t.Parallel.
//
// The core's DetectionCache (a patrickmn/go-cache) starts a janitor goroutine that
// cannot be stopped (cleanup only flushes it). A domain test package that adds a
// goleak gate must ignore "github.com/patrickmn/go-cache.(*janitor).Run", the same
// way package api's TestMain does.
func NewCore(t *testing.T, opts ...CoreOption) *apicore.Core {
	t.Helper()

	cfg := &coreConfig{
		publishSettings:   true,
		privateModeExempt: func(_, _ string) bool { return false },
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.privateModeExempt == nil {
		// A nil predicate would panic in PrivateModeAuth; restore the safe default
		// (exempt nothing) if an option cleared it.
		cfg.privateModeExempt = func(_, _ string) bool { return false }
	}

	if cfg.settings == nil {
		cfg.settings = NewValidTestSettings()
	}
	// Isolation: scratch media export path under t.TempDir() so parallel package
	// test binaries never share a media directory or contend on a fixed path.
	cfg.settings.Realtime.Audio.Export.Path = t.TempDir()
	for _, fn := range cfg.settingsFuncs {
		fn(cfg.settings)
	}

	if cfg.echo == nil {
		cfg.echo = echo.New()
	}
	if !cfg.dsSet {
		cfg.ds = mocks.NewMockInterface(t)
	}
	if cfg.birdImageCache == nil {
		cfg.birdImageCache = NewMockBirdImageCache(t)
	}
	if cfg.sunCalc == nil {
		cfg.sunCalc = suncalc.NewSunCalc(testLatitude, testLongitude)
	}
	if cfg.metrics == nil {
		cfg.metrics = NewTestMetrics(t)
	}

	core, err := apicore.NewCore(cfg.echo, cfg.ds, cfg.settings, cfg.birdImageCache,
		cfg.sunCalc, cfg.metrics, cfg.privateModeExempt)
	require.NoError(t, err, "apitest: building apicore.Core")

	// Register the v2 route group the same way the facade does so domain handler
	// tests can call h.RegisterRoutes(core.Group). apicore.NewCore deliberately
	// leaves Group nil (the facade owns it); apitest plays the facade's role here.
	// The facade's group-level middleware (tunnel detection, body limit, logging,
	// PrivateMode auth) is NOT applied: those are facade concerns, and a minimal
	// test core exercises a handler directly. A test that needs them applies them
	// to core.Group itself.
	core.Group = cfg.echo.Group(apiV2Prefix)

	if cfg.publishSettings {
		PublishTestSettings(t, cfg.settings)
	}

	// Tear down the core's lifecycle and release resources. apitest.NewCore
	// returns a *apicore.Core, not an *api.Controller, so api.Controller.Shutdown
	// never runs for domain tests; this cleanup mirrors the Core-level teardown it
	// performs, in the same order. Closing SFS is mandatory: the media SecureFS
	// holds an open os.Root directory handle, and on Windows that handle blocks
	// t.TempDir() removal (t.Cleanup is LIFO, so SFS.Close runs before t.TempDir's
	// RemoveAll).
	t.Cleanup(func() {
		// Close SSE clients before cancelling, matching api.Controller.Shutdown: a
		// streaming handler can block on its request context, so releasing clients
		// first avoids a circular wait if a future test drives one via core.Go.
		if core.SSEManager != nil {
			core.SSEManager.CloseAllClients()
		}
		core.Cancel()
		core.Wait()
		if core.SFS != nil {
			_ = core.SFS.Close()
		}
		if core.DetectionCache != nil {
			core.DetectionCache.Flush()
		}
	})

	return core
}
