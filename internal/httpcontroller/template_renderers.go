package httpcontroller

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// LocaleData represents a locale with its code and full name.
type LocaleData struct {
	Code string
	Name string
}

// PageData represents data for rendering a page.
type PageData struct {
	C               echo.Context   // The Echo context for the current request
	Page            string         // The name or identifier of the current page
	Title           string         // The title of the page
	Settings        *conf.Settings // Application settings
	Locales         []LocaleData   // List of available locales
	Charts          template.HTML  // HTML content for charts, if any
	PreloadFragment string         // The preload route for the current page
}

// TemplateRenderer is a custom HTML template renderer for Echo framework.
type TemplateRenderer struct {
	templates *template.Template
	logger    *logger.Logger
	debug     bool
}

// validateErrorTemplates checks if all required error templates exist
func (t *TemplateRenderer) validateErrorTemplates() error {
	requiredTemplates := []string{"error-404", "error-500", "error-default"}
	for _, name := range requiredTemplates {
		if tmpl := t.templates.Lookup(name); tmpl == nil {
			return fmt.Errorf("required error template not found: %s", name)
		}
	}
	return nil
}

// Render renders a template with the given data.
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	// Create a buffer to capture any template execution errors
	var buf bytes.Buffer
	err := t.templates.ExecuteTemplate(&buf, name, data)
	if err != nil {
		// Use structured logging with template name as a field
		if t.logger != nil {
			t.logger.Error("Error executing template",
				"template", name,
				"error", err)
		} else {
			log.Printf("Error executing template %s: %v", name, err)
		}
		return err
	}

	// If execution was successful, write the result to the original writer
	_, err = buf.WriteTo(w)
	if err != nil {
		// Use structured logging for write errors
		if t.logger != nil {
			t.logger.Error("Error writing template result", "error", err)
		} else {
			log.Printf("Error writing template result: %v", err)
		}
	}
	return err
}

// setupTemplateRenderer configures the template renderer for the server
func (s *Server) setupTemplateRenderer() {
	// Get the template functions
	funcMap := s.GetTemplateFunctions()

	// Setup a component-specific logger
	var componentLogger *logger.Logger
	if s.Logger != nil {
		componentLogger = s.Logger.Named("templates")
	}

	// Parse all templates from the ViewsFs
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(ViewsFs, "views/*/*.html", "views/*/*/*.html")
	if err != nil {
		if componentLogger != nil {
			componentLogger.Fatal("Failed to parse templates", "error", err)
		} else {
			s.Echo.Logger.Fatal(err)
		}
	}

	// Create the renderer
	renderer := &TemplateRenderer{
		templates: tmpl,
		logger:    componentLogger,
		debug:     s.isDevMode(),
	}

	// Validate that all required error templates exist
	if err := renderer.validateErrorTemplates(); err != nil {
		if componentLogger != nil {
			componentLogger.Fatal("Error template validation failed", "error", err)
		} else {
			s.Echo.Logger.Fatal(err)
		}
	}

	// Set the custom renderer
	s.Echo.Renderer = renderer

	// Log successful initialization
	if componentLogger != nil {
		componentLogger.Info("Template renderer initialized successfully")
	}
}

// RenderContent renders the content template with the given data
func (s *Server) RenderContent(data interface{}) (template.HTML, error) {
	// Get a component-specific logger
	var renderLogger *logger.Logger
	if s.Logger != nil {
		renderLogger = s.Logger.Named("templates.render")
	}

	// Assert that the data is of the expected type
	d, ok := data.(RenderData)
	if !ok {
		// Return an error if the data type is invalid
		errMsg := fmt.Sprintf("invalid data type: %s", data)
		if renderLogger != nil {
			renderLogger.Error("Invalid render data type", "data_type", fmt.Sprintf("%T", data))
		}
		return "", fmt.Errorf(errMsg)
	}

	// Extract the context from the data
	c := d.C

	// Get the current path from the context
	path := c.Path()

	// Look up the route for the current path
	_, isPageRoute := s.pageRoutes[path]
	_, isFragment := s.partialRoutes[path]

	// Is a login route, set isLoginRoute to true
	isLoginRoute := path == "/login"

	if !isPageRoute && !isFragment && !isLoginRoute {
		// Return an error if no route is found for the path
		errMsg := fmt.Sprintf("no route found for path: %s", path)
		if renderLogger != nil {
			renderLogger.Error("No route found", "path", path)
		}
		return "", fmt.Errorf(errMsg)
	}

	// Create a buffer to store the rendered template
	buf := new(bytes.Buffer)

	// Render the template using the Echo renderer
	err := s.Echo.Renderer.Render(buf, d.Page, d, c)
	if err != nil {
		// Return an error if template rendering fails
		if renderLogger != nil {
			renderLogger.Error("Template rendering failed",
				"template", d.Page,
				"error", err)
		}
		return "", err
	}

	// Log successful render in debug mode
	if renderLogger != nil && s.isDevMode() {
		renderLogger.Debug("Template rendered successfully",
			"template", d.Page,
			"path", path)
	}

	// Return the rendered template as HTML
	return template.HTML(buf.String()), nil
}

// renderSettingsContent returns the appropriate content template for a given settings page
func (s *Server) renderSettingsContent(c echo.Context) (template.HTML, error) {
	// Get a component-specific logger
	var settingsLogger *logger.Logger
	if s.Logger != nil {
		settingsLogger = s.Logger.Named("templates.settings")
	}

	// Extract the settings type from the path
	path := c.Path()
	settingsType := strings.TrimPrefix(path, "/settings/")
	templateName := fmt.Sprintf("%sSettings", settingsType)

	// Check for CSRF token with proper structured logging
	csrfToken := c.Get(CSRFContextKey)
	if csrfToken == nil {
		if settingsLogger != nil {
			settingsLogger.Warn("CSRF token not found in context",
				"page", templateName,
				"path", path)
		} else {
			log.Printf("Warning: 🚨 CSRF token not found in context for settings page: %s", path)
		}
		csrfToken = ""
	} else {
		if settingsLogger != nil && s.isDevMode() {
			settingsLogger.Debug("CSRF token found in context",
				"page", templateName,
				"path", path)
		} else if s.isDevMode() {
			log.Printf("Debug: ✅ CSRF token found in context for settings page: %s", path)
		}
	}

	// Prepare the data for the template
	data := map[string]interface{}{
		"Settings":       s.Settings,             // Application settings
		"Locales":        s.prepareLocalesData(), // Prepare locales data for the UI
		"EqFilterConfig": conf.EqFilterConfig,    // Equalizer filter configuration for the UI
		"TemplateName":   templateName,
		"CSRFToken":      csrfToken,
	}

	// Log species settings in debug mode
	if templateName == "speciesSettings" && s.isDevMode() {
		if settingsLogger != nil {
			settingsLogger.Debug("Species Config for template",
				"config", s.Settings.Realtime.Species.Config)
		} else {
			log.Printf("Debug: Species Config being passed to template: %+v", s.Settings.Realtime.Species.Config)
		}
	}

	// Render the template
	var buf bytes.Buffer
	err := s.Echo.Renderer.Render(&buf, templateName, data, c)

	// Handle rendering errors with improved structured logging
	if err != nil {
		if settingsLogger != nil {
			settingsLogger.Error("Failed to render settings content",
				"template", templateName,
				"error", err,
				"data_keys", getDataKeys(data))
		} else {
			log.Printf("ERROR: Failed to render settings content: %v", err)
			log.Printf("ERROR: Template data dump: %+v", data)
		}
		return "", err
	}

	// Log successful render in debug mode
	if settingsLogger != nil && s.isDevMode() {
		settingsLogger.Debug("Settings template rendered successfully",
			"template", templateName)
	}

	// Return the rendered HTML
	return template.HTML(buf.String()), nil
}

// getDataKeys is a helper function that extracts the keys from template data for logging
func getDataKeys(data map[string]interface{}) []string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return keys
}
