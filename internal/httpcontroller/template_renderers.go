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
	"github.com/tphakala/birdnet-go/internal/imageprovider"
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
	logger    echo.Logger
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
		t.logger.Errorf("Error executing template %s: %v", name, err)
		return err
	}

	// If execution was successful, write the result to the original writer
	_, err = buf.WriteTo(w)
	if err != nil {
		t.logger.Errorf("Error writing template result: %v", err)
	}
	return err
}

// setupTemplateRenderer configures the template renderer for the server
func (s *Server) setupTemplateRenderer() {
	// Get the template functions
	funcMap := s.GetTemplateFunctions()

	// Parse all templates from the ViewsFs
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(ViewsFs, "views/*/*.html", "views/*/*/*.html")
	if err != nil {
		s.Echo.Logger.Fatal(err)
	}

	// Create the renderer
	renderer := &TemplateRenderer{
		templates: tmpl,
		logger:    s.Echo.Logger,
	}

	// Validate that all required error templates exist
	if err := renderer.validateErrorTemplates(); err != nil {
		s.Echo.Logger.Fatal(err)
	}

	// Set the custom renderer
	s.Echo.Renderer = renderer
}

// RenderContent renders the content template with the given data
func (s *Server) RenderContent(data interface{}) (template.HTML, error) {
	// Assert that the data is of the expected type
	d, ok := data.(RenderData)
	if !ok {
		// Return an error if the data type is invalid
		return "", fmt.Errorf("invalid data type: %s", data)
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
		return "", fmt.Errorf("no route found for path: %s", path)
	}

	// Create a buffer to store the rendered template
	buf := new(bytes.Buffer)

	// Render the template using the Echo renderer
	err := s.Echo.Renderer.Render(buf, d.Page, d, c)
	if err != nil {
		// Return an error if template rendering fails
		return "", err
	}

	// Return the rendered template as HTML
	return template.HTML(buf.String()), nil
}

// renderSettingsContent returns the appropriate content template for a given settings page
func (s *Server) renderSettingsContent(c echo.Context) (template.HTML, error) {
	// Extract the settings type from the path
	path := c.Path()
	settingsType := strings.TrimPrefix(path, "/settings/")
	templateName := fmt.Sprintf("%sSettings", settingsType)

	// DEBUG do we have CSRF token?
	csrfToken := c.Get(CSRFContextKey)
	if csrfToken == nil {
		log.Printf("Warning: 🚨 CSRF token not found in context for settings page: %s", path)
		csrfToken = ""
	} else {
		log.Printf("Debug: ✅ CSRF token found in context for settings page: %s", path)
	}

	// Prepare image provider options for the template
	providerOptions := map[string]string{
		"auto": "Auto (Default)", // Always add auto
	}
	multipleProvidersAvailable := false
	providerCount := 0
	if registry := s.Handlers.BirdImageCache.GetRegistry(); registry != nil {
		registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
			// Simple capitalization for display name
			displayName := strings.ToUpper(name[:1]) + name[1:]
			providerOptions[name] = displayName
			providerCount++
			return true // Continue ranging
		})
		multipleProvidersAvailable = providerCount > 1
	} else {
		log.Println("Warning: ImageProviderRegistry is nil, cannot get provider names.")
	}

	// Prepare the data for the template
	data := map[string]interface{}{
		"Settings":                   s.Settings,             // Application settings
		"Locales":                    s.prepareLocalesData(), // Prepare locales data for the UI
		"EqFilterConfig":             conf.EqFilterConfig,    // Equalizer filter configuration for the UI
		"TemplateName":               templateName,
		"CSRFToken":                  csrfToken,
		"ImageProviderOptions":       providerOptions,            // Add provider options map
		"MultipleProvidersAvailable": multipleProvidersAvailable, // Add flag
	}

	// DEBUG Log the species settings
	//log.Printf("Species Settings: %+v", s.Settings.Realtime.Species)

	if templateName == "speciesSettings" {
		log.Printf("Debug: Species Config being passed to template: %+v", s.Settings.Realtime.Species.Config)
	}

	// Render the template
	var buf bytes.Buffer
	err := s.Echo.Renderer.Render(&buf, templateName, data, c)

	// Handle rendering errors
	if err != nil {
		log.Printf("ERROR: Failed to render settings content: %v", err)
		// Log the template data that caused the error
		log.Printf("ERROR: Template data dump: %+v", data)
		return "", err
	}

	// Return the rendered HTML
	return template.HTML(buf.String()), nil
}
