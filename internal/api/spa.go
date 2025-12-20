package api

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/conf"
)

//go:embed templates/spa.html
var spaTemplateFS embed.FS

// SPAHandler handles serving the Single Page Application HTML shell.
type SPAHandler struct {
	settings    *conf.Settings
	template    *template.Template
	authService auth.Service
}

// spaTemplateData holds the data passed to the SPA template.
type spaTemplateData struct {
	CSRFToken       string
	SecurityEnabled bool
	AccessAllowed   bool
	Version         string
}

// NewSPAHandler creates a new SPA handler.
func NewSPAHandler(settings *conf.Settings) *SPAHandler {
	// Parse the embedded template
	tmpl := template.Must(template.ParseFS(spaTemplateFS, "templates/spa.html"))

	return &SPAHandler{
		settings: settings,
		template: tmpl,
	}
}

// SetAuthService sets the auth service for determining access status.
// This should be called after auth is initialized at server level.
func (h *SPAHandler) SetAuthService(svc auth.Service) {
	h.authService = svc
}

// ServeApp serves the SPA HTML shell for all frontend routes.
func (h *SPAHandler) ServeApp(c echo.Context) error {
	// Get CSRF token from context if available
	csrfToken := ""
	if token, ok := c.Get("csrf").(string); ok {
		csrfToken = token
	}

	// Determine security state
	securityEnabled := h.settings.Security.BasicAuth.Enabled ||
		h.settings.Security.GoogleAuth.Enabled ||
		h.settings.Security.GithubAuth.Enabled

	// Determine access status using auth service
	accessAllowed := h.determineAccessAllowed(c, securityEnabled)

	// Prepare template data
	data := spaTemplateData{
		CSRFToken:       csrfToken,
		SecurityEnabled: securityEnabled,
		AccessAllowed:   accessAllowed,
		Version:         h.settings.Version,
	}

	// Render template to buffer
	var buf bytes.Buffer
	if err := h.template.Execute(&buf, data); err != nil {
		httpErr := echo.NewHTTPError(http.StatusInternalServerError, "Failed to render page")
		httpErr.Internal = err
		return httpErr
	}

	return c.HTML(http.StatusOK, buf.String())
}

// determineAccessAllowed checks if the current request is authenticated.
// Returns true if:
// - Security is disabled (no auth required)
// - Auth service says the request is authenticated (session, token, or subnet bypass)
func (h *SPAHandler) determineAccessAllowed(c echo.Context, securityEnabled bool) bool {
	// If security is not enabled, allow access
	if !securityEnabled {
		return true
	}

	// If auth service is not configured, deny access (fail closed)
	if h.authService == nil {
		return false
	}

	// Use auth service to check authentication status
	// This checks: subnet bypass, token auth, and session auth
	return h.authService.IsAuthenticated(c)
}
