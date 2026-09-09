// internal/api/pprof_test.go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mw "github.com/tphakala/birdnet-go/internal/api/middleware"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

const (
	testProfilingToken = "test-profiling-token-value-0123456789"
	heapPath           = PprofBasePath + "/heap"
)

// newPprofTestServer builds a Server with only the pieces the pprof routes
// touch, registers the routes, and publishes the settings globally so the
// gate's currentSettings() lookup sees them. The previous global snapshot is
// restored on cleanup.
//
// Tests using this must not run in parallel: the settings singleton is global.
func newPprofTestServer(t *testing.T, settings *conf.Settings) *Server {
	t.Helper()

	previous := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(previous) })
	conftest.SetTestSettings(settings)

	s := &Server{
		echo:     echo.New(),
		config:   DefaultConfig(),
		settings: settings,
		slogger:  GetLogger(),
	}
	s.echo.HideBanner = true
	s.registerPprofRoutes()

	return s
}

// profilingSettings returns settings with profiling configured as requested and
// no authentication provider, which is the default home-LAN shape.
func profilingSettings(enabled bool, token string) *conf.Settings {
	settings := conftest.NewTestSettings().Build()
	settings.Diagnostics.Profiling.Enabled = enabled
	settings.Diagnostics.Profiling.Token = token
	return settings
}

// doPprofRequest issues a GET against the server's Echo instance.
func doPprofRequest(t *testing.T, s *Server, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	return rec
}

// gatedRoute is one registered pprof route, as (method, path).
type gatedRoute struct {
	method string
	path   string
}

// gatedRoutes enumerates EVERY route registerPprofRoutes mounts.
//
// It is a single shared list precisely so the refusal tests cannot drift into
// covering a subset. The earlier version of this file drove only /heap through
// the gate, which meant deleting the gate from /trace or /symbol left the whole
// suite green while republishing an execution trace and the symbol table
// unauthenticated. Any route added to registerPprofRoutes must be added here,
// and TestProfilingRoutesAreAllGated fails if the two ever disagree in the
// direction that matters.
var gatedRoutes = []gatedRoute{
	{http.MethodGet, PprofBasePath},
	{http.MethodGet, PprofBasePath + "/"},
	{http.MethodGet, PprofBasePath + "/cmdline"},
	{http.MethodGet, PprofBasePath + "/profile"},
	{http.MethodGet, PprofBasePath + "/trace"},
	{http.MethodGet, PprofBasePath + "/symbol"},
	{http.MethodPost, PprofBasePath + "/symbol"},
	{http.MethodGet, PprofBasePath + "/heap"},
	{http.MethodGet, PprofBasePath + "/allocs"},
	{http.MethodGet, PprofBasePath + "/goroutine"},
	{http.MethodGet, PprofBasePath + "/block"},
	{http.MethodGet, PprofBasePath + "/mutex"},
	{http.MethodGet, PprofBasePath + "/threadcreate"},
}

// doGatedRequest issues one route's request with an optional query.
func doGatedRequest(t *testing.T, s *Server, r gatedRoute, query string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(r.method, r.path+query, http.NoBody)
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	return rec
}

// TestProfilingGate_DisabledReturns404 covers the acceptance criterion that a
// disabled feature must not advertise itself: 404, never 403. Every registered
// route, not a sample.
func TestProfilingGate_DisabledReturns404(t *testing.T) {
	s := newPprofTestServer(t, profilingSettings(false, testProfilingToken))

	for _, r := range gatedRoutes {
		t.Run(r.method+r.path, func(t *testing.T) {
			rec := doGatedRequest(t, s, r, "")
			assert.Equal(t, http.StatusNotFound, rec.Code,
				"disabled profiling must be indistinguishable from an unrouted path")
		})
	}
}

// TestProfilingRoutesAreAllGated drives every registered route with no
// credential on an ENABLED instance. This is the fail-open guard: a route that
// lost its gate would answer 200 here.
func TestProfilingRoutesAreAllGated(t *testing.T) {
	s := newPprofTestServer(t, profilingSettings(true, testProfilingToken))

	for _, r := range gatedRoutes {
		t.Run(r.method+r.path, func(t *testing.T) {
			rec := doGatedRequest(t, s, r, "")
			assert.Equal(t, http.StatusForbidden, rec.Code,
				"every pprof route must refuse a request carrying no token")
			assert.NotContains(t, rec.Body.String(), "runtime.",
				"a refused request must not carry profile content")
		})
	}
}

// TestProfilingGate_DisabledIgnoresValidToken guards against a gate that checks
// the credential before the enable flag.
func TestProfilingGate_DisabledIgnoresValidToken(t *testing.T) {
	s := newPprofTestServer(t, profilingSettings(false, testProfilingToken))

	rec := doPprofRequest(t, s, heapPath+"?token="+testProfilingToken)
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"a valid token must not open a disabled endpoint")
}

// TestProfilingGate_TokenAuth covers the no-auth-provider path, which is the
// common home-LAN default.
func TestProfilingGate_TokenAuth(t *testing.T) {
	tests := []struct {
		name            string
		configuredToken string
		query           string
		wantStatus      int
	}{
		{
			name:            "correct token is accepted",
			configuredToken: testProfilingToken,
			query:           "?token=" + testProfilingToken,
			wantStatus:      http.StatusOK,
		},
		{
			name:            "missing token is refused",
			configuredToken: testProfilingToken,
			query:           "",
			wantStatus:      http.StatusForbidden,
		},
		{
			name:            "wrong token is refused",
			configuredToken: testProfilingToken,
			query:           "?token=not-the-token",
			wantStatus:      http.StatusForbidden,
		},
		{
			name:            "token that is a prefix of the real one is refused",
			configuredToken: testProfilingToken,
			query:           "?token=" + testProfilingToken[:10],
			wantStatus:      http.StatusForbidden,
		},
		{
			name:            "empty configured token refuses an empty presented token",
			configuredToken: "",
			query:           "?token=",
			wantStatus:      http.StatusForbidden,
		},
		{
			name:            "empty configured token refuses any presented token",
			configuredToken: "",
			query:           "?token=anything",
			wantStatus:      http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newPprofTestServer(t, profilingSettings(true, tt.configuredToken))

			rec := doPprofRequest(t, s, heapPath+tt.query)
			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantStatus == http.StatusOK {
				assert.NotEmpty(t, rec.Body.Bytes(), "a served heap profile must have a body")
			}
		})
	}
}

// TestProfilingGate_DelegatesToAuthMiddleware verifies that a configured
// authentication provider makes the server's own auth middleware the gate, and
// that the token is not consulted as a second way in.
func TestProfilingGate_DelegatesToAuthMiddleware(t *testing.T) {
	const authHeader = "X-Test-Authenticated"

	tests := []struct {
		name        string
		header      string
		query       string
		wantStatus  int
		wantReached bool
	}{
		{
			name:        "authenticated request is served",
			header:      "yes",
			wantStatus:  http.StatusOK,
			wantReached: true,
		},
		{
			name:       "unauthenticated request is refused",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "profiling token is not a bypass when auth is configured",
			query:      "?token=" + testProfilingToken,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := profilingSettings(true, testProfilingToken)
			settings.Security.BasicAuth.Enabled = true
			require.True(t, settings.IsAuthProviderConfigured(),
				"test setup must produce a configured auth provider")

			previous := conf.GetSettings()
			t.Cleanup(func() { conf.StoreSettings(previous) })
			conftest.SetTestSettings(settings)

			var middlewareCalled bool
			s := &Server{
				echo:     echo.New(),
				config:   DefaultConfig(),
				settings: settings,
				slogger:  GetLogger(),
				authMiddleware: func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						middlewareCalled = true
						if c.Request().Header.Get(authHeader) == "" {
							return echo.NewHTTPError(http.StatusUnauthorized)
						}
						return next(c)
					}
				},
			}
			s.echo.HideBanner = true
			s.registerPprofRoutes()

			req := httptest.NewRequest(http.MethodGet, heapPath+tt.query, http.NoBody)
			if tt.header != "" {
				req.Header.Set(authHeader, tt.header)
			}
			rec := httptest.NewRecorder()
			s.echo.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.True(t, middlewareCalled,
				"the auth middleware must gate the route whenever a provider is configured")
			if tt.wantReached {
				assert.NotEmpty(t, rec.Body.Bytes())
			}
		})
	}
}

// TestProfilingGate_AuthConfiguredButMiddlewareMissing verifies the fail-closed
// branch. No token is minted when auth is configured, so falling through to the
// token check would leave the endpoint permanently open.
func TestProfilingGate_AuthConfiguredButMiddlewareMissing(t *testing.T) {
	settings := profilingSettings(true, testProfilingToken)
	settings.Security.BasicAuth.Enabled = true

	s := newPprofTestServer(t, settings)
	require.Nil(t, s.authMiddleware, "test setup must leave the middleware unwired")

	rec := doPprofRequest(t, s, heapPath+"?token="+testProfilingToken)
	assert.Equal(t, http.StatusForbidden, rec.Code,
		"an unwired auth middleware must fail closed, not fall back to the token")
}

// TestProfilingGate_HotReload verifies the setting takes effect in both
// directions without re-registering routes or restarting the server.
func TestProfilingGate_HotReload(t *testing.T) {
	settings := profilingSettings(false, testProfilingToken)
	s := newPprofTestServer(t, settings)

	target := heapPath + "?token=" + testProfilingToken

	rec := doPprofRequest(t, s, target)
	require.Equal(t, http.StatusNotFound, rec.Code, "profiling starts disabled")

	// Publish an enabled snapshot the way a settings save does (copy-on-write),
	// rather than mutating the one already published.
	enabled := conf.CloneSettings(settings)
	enabled.Diagnostics.Profiling.Enabled = true
	conf.StoreSettings(enabled)

	rec = doPprofRequest(t, s, target)
	assert.Equal(t, http.StatusOK, rec.Code, "enabling must take effect without a restart")

	disabled := conf.CloneSettings(enabled)
	disabled.Diagnostics.Profiling.Enabled = false
	conf.StoreSettings(disabled)

	rec = doPprofRequest(t, s, target)
	assert.Equal(t, http.StatusNotFound, rec.Code, "disabling must take effect without a restart")
}

// TestProfilingRoutes_NamedProfilesAndHandlers checks that the wildcard route
// really does dispatch the named runtime profiles, and that the four handlers
// which are not runtime profiles have their own routes.
func TestProfilingRoutes_NamedProfilesAndHandlers(t *testing.T) {
	s := newPprofTestServer(t, profilingSettings(true, testProfilingToken))

	// trace and profile are excluded: both sample for seconds before responding.
	for _, path := range []string{
		PprofBasePath + "/",
		PprofBasePath + "/heap",
		PprofBasePath + "/allocs",
		PprofBasePath + "/goroutine",
		PprofBasePath + "/block",
		PprofBasePath + "/mutex",
		PprofBasePath + "/threadcreate",
		PprofBasePath + "/cmdline",
		PprofBasePath + "/symbol",
	} {
		t.Run(path, func(t *testing.T) {
			rec := doPprofRequest(t, s, path+"?token="+testProfilingToken)
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}

// TestProfilingIndexRedirectsToSlashedPath guards a bug that static review
// missed and only rendering the page exposes.
//
// pprof.Index emits RELATIVE links. Served at /debug/pprof they resolve against
// /debug/ and every link on the index 404s; served at /debug/pprof/ they
// resolve correctly. net/http.ServeMux issued this redirect implicitly, so the
// endpoints never needed it in their previous home and the regression is
// invisible unless something actually follows a link.
func TestProfilingIndexRedirectsToSlashedPath(t *testing.T) {
	s := newPprofTestServer(t, profilingSettings(true, testProfilingToken))

	rec := doPprofRequest(t, s, PprofBasePath+"?token="+testProfilingToken)

	require.Equal(t, http.StatusFound, rec.Code,
		"the unslashed index path must redirect, not render links that 404")

	location := rec.Header().Get("Location")
	assert.Equal(t, PprofBasePath+"/?token="+testProfilingToken, location,
		"the redirect must keep the query, or the hop drops the token into a 403")
}

// TestProfilingRefusalIsUniform pins the anti-oracle property the gate's own
// comment commits to: absent, malformed, and simply-wrong tokens must be
// indistinguishable to a caller probing the endpoint. Asserting the status code
// alone would not notice a helpful "token missing" vs "token invalid" split.
func TestProfilingRefusalIsUniform(t *testing.T) {
	s := newPprofTestServer(t, profilingSettings(true, testProfilingToken))

	queries := []string{
		"",
		"?token=",
		"?token=wrong",
		"?token=" + testProfilingToken[:10],
		"?token=" + testProfilingToken + "x",
	}

	var first string
	for i, q := range queries {
		rec := doPprofRequest(t, s, heapPath+q)
		require.Equal(t, http.StatusForbidden, rec.Code, "query %q", q)
		if i == 0 {
			first = rec.Body.String()
			continue
		}
		assert.Equal(t, first, rec.Body.String(),
			"refusal bodies must be byte-identical; query %q differs and is an oracle", q)
	}
}

// TestPprofSkippersMatchMountPath couples the two duplicated path constants.
//
// middleware.pprofBasePath is a hand-copy of api.PprofBasePath (the api package
// imports middleware, so the dependency cannot be reversed). Nothing else makes
// them agree, and a silent divergence would re-enable CSRF on the symbol POST
// that go tool pprof needs and start double-gzipping every profile, with no
// other test failing. This is the coupling.
func TestPprofSkippersMatchMountPath(t *testing.T) {
	t.Parallel()

	e := echo.New()
	for _, path := range []string{PprofBasePath, PprofBasePath + "/heap", PprofBasePath + "/symbol"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
			c := e.NewContext(req, httptest.NewRecorder())

			assert.True(t, mw.PprofSkipper(c),
				"the gzip skipper must recognise the path the api package actually mounts")
			assert.True(t, mw.DefaultCSRFSkipper(c),
				"the CSRF skipper must recognise the path the api package actually mounts")
		})
	}
}

// TestProfilingTokenMatches covers the comparison helper directly, including the
// fail-closed empty cases that the gate depends on.
func TestProfilingTokenMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		presented  string
		want       bool
	}{
		{name: "equal tokens match", configured: "secret", presented: "secret", want: true},
		{name: "different tokens do not match", configured: "secret", presented: "other"},
		{name: "empty configured never matches", configured: "", presented: "secret"},
		{name: "empty presented never matches", configured: "secret", presented: ""},
		{name: "both empty never matches", configured: "", presented: ""},
		{name: "prefix does not match", configured: "secretvalue", presented: "secret"},
		{name: "case differences do not match", configured: "Secret", presented: "secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, profilingTokenMatches(tt.configured, tt.presented))
		})
	}
}
