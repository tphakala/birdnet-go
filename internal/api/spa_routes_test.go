package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

const (
	htmlAcceptHeader = "text/html,application/xhtml+xml"
	// loginRedirectPrefix is what the auth middleware redirects browsers to;
	// it appends the URL-encoded origin path as the redirect value.
	loginRedirectPrefix = "/login?redirect="
)

// stubAuthService is an auth.Service that implements only the calls the
// Authenticate middleware makes. Any other method panics through the nil
// embedded interface, which flags an unexpected dependency.
type stubAuthService struct {
	auth.Service
	authRequired  bool
	hasValidLogin bool
}

func (s *stubAuthService) IsAuthRequired(echo.Context) bool { return s.authRequired }

func (s *stubAuthService) CheckAccess(echo.Context) error {
	if s.hasValidLogin {
		return nil
	}
	return auth.ErrSessionNotFound
}

func (s *stubAuthService) GetAuthMethod(echo.Context) auth.AuthMethod { return auth.AuthMethodNone }
func (s *stubAuthService) GetUsername(echo.Context) string            { return "" }

// newSPARoutesTestServer registers the SPA routes with the real auth
// middleware backed by svc; a nil svc leaves the middleware unwired
// (authentication not configured).
func newSPARoutesTestServer(t *testing.T, svc auth.Service) *Server {
	t.Helper()

	settings := conftest.NewTestSettings().Build()
	previous := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(previous) })
	conftest.SetTestSettings(settings)

	s := &Server{
		echo:       echo.New(),
		config:     DefaultConfig(),
		settings:   settings,
		slogger:    GetLogger(),
		spaHandler: NewSPAHandler(false, ""),
	}
	s.echo.HideBanner = true
	if svc != nil {
		s.authMiddleware = auth.NewMiddleware(svc).Authenticate
	}
	s.registerSPARoutes()
	return s
}

func doSPARequest(s *Server, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	req.Header.Set(echo.HeaderAccept, htmlAcceptHeader)
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	return rec
}

// protectedUIPaths are the SPA paths that must be gated by the auth
// middleware: the system and settings pages, including deep links.
var protectedUIPaths = []string{
	"/ui/system",
	"/ui/system/database",
	"/ui/system/terminal",
	"/ui/system/inference",
	"/ui/system/import-export",
	"/ui/system/health",
	"/ui/system/unknown-section",
	"/ui/settings",
	"/ui/settings/main",
	"/ui/settings/analysis",
}

// TestSPARoutes_ProtectedPathsRedirectWhenUnauthenticated verifies that deep
// links to system and settings pages redirect an unauthenticated browser to
// /login instead of serving the SPA shell.
func TestSPARoutes_ProtectedPathsRedirectWhenUnauthenticated(t *testing.T) {
	s := newSPARoutesTestServer(t, &stubAuthService{authRequired: true})

	for _, path := range protectedUIPaths {
		t.Run(path, func(t *testing.T) {
			rec := doSPARequest(s, path)
			require.Equal(t, http.StatusFound, rec.Code, "unauthenticated browser must be redirected")
			assert.Equal(t, loginRedirectPrefix+url.QueryEscape(path), rec.Header().Get(echo.HeaderLocation),
				"redirect must carry the requested path so login returns the user to it")
		})
	}
}

// TestSPARoutes_ProtectedPathsServeShellWhenAuthenticated verifies an
// authenticated browser gets the SPA shell on the protected paths.
func TestSPARoutes_ProtectedPathsServeShellWhenAuthenticated(t *testing.T) {
	s := newSPARoutesTestServer(t, &stubAuthService{authRequired: true, hasValidLogin: true})

	for _, path := range protectedUIPaths {
		t.Run(path, func(t *testing.T) {
			rec := doSPARequest(s, path)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Header().Get(echo.HeaderContentType), echo.MIMETextHTML)
		})
	}
}

// TestSPARoutes_ProtectedPathsServeShellWithoutAuth verifies the shell is
// served when no authentication is configured (nil middleware) and when the
// client bypasses authentication (for example a trusted subnet).
func TestSPARoutes_ProtectedPathsServeShellWithoutAuth(t *testing.T) {
	servers := map[string]*Server{
		"auth not configured": newSPARoutesTestServer(t, nil),
		"auth not required":   newSPARoutesTestServer(t, &stubAuthService{authRequired: false}),
	}

	for name, s := range servers {
		for _, path := range protectedUIPaths {
			t.Run(name+path, func(t *testing.T) {
				rec := doSPARequest(s, path)
				assert.Equal(t, http.StatusOK, rec.Code)
			})
		}
	}
}

// TestSPARoutes_PublicPathsServeShellWhenUnauthenticated verifies the public
// shell routes and the public catch-all stay reachable so the SPA can render
// the login form.
func TestSPARoutes_PublicPathsServeShellWhenUnauthenticated(t *testing.T) {
	s := newSPARoutesTestServer(t, &stubAuthService{authRequired: true})

	for _, path := range []string{"/login", "/ui/dashboard", "/ui/about", "/ui/help", "/ui/live-stream"} {
		t.Run(path, func(t *testing.T) {
			rec := doSPARequest(s, path)
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}
