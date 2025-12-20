package api

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
)

//go:embed templates/spa.html
var spaTemplateFS embed.FS

// SPAHandler handles serving the Single Page Application HTML shell.
type SPAHandler struct {
	settings *conf.Settings
	template *template.Template
}

// spaTemplateData holds the data passed to the SPA template.
type spaTemplateData struct {
	CSRFToken       string
	SecurityEnabled bool
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

	// Prepare template data
	data := spaTemplateData{
		CSRFToken:       csrfToken,
		SecurityEnabled: securityEnabled,
		Version:         h.settings.Version,
	}

	// Render template to buffer
	var buf bytes.Buffer
	if err := h.template.Execute(&buf, data); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to render page")
	}

	return c.HTML(http.StatusOK, buf.String())
}
