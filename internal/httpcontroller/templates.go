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
)

// LocaleData represents a locale with its code and full name.
type LocaleData struct {
	Code string
	Name string
}

// PageData represents data for rendering a page.
type PageData struct {
	C        echo.Context
	Page     string
	Title    string
	Settings *conf.Settings
	Locales  []LocaleData
	Charts   template.HTML
}

// TemplateRenderer is a custom HTML template renderer for Echo framework.
type TemplateRenderer struct {
	templates *template.Template
}

// Render renders a template with the given data.
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

// setupTemplateRenderer configures the template renderer for the server
func (s *Server) setupTemplateRenderer() {
	funcMap := s.GetTemplateFunctions()

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(ViewsFs, "views/*.html", "views/**/*.html")
	if err != nil {
		s.Echo.Logger.Fatal(err)
	}
	s.Echo.Renderer = &TemplateRenderer{templates: tmpl}
}

// RenderContent renders the content template with the given data
func (s *Server) RenderContent(data interface{}) (template.HTML, error) {
	d, ok := data.(struct {
		C               echo.Context
		Page            string
		Title           string
		Settings        *conf.Settings
		Locales         []LocaleData
		Charts          template.HTML
		ContentTemplate string
	})
	if !ok {
		return "", fmt.Errorf("invalid data type")
	}

	c := d.C // Extracted context
	path := c.Path()
	route, exists := s.pageRoutes[path]
	if !exists {
		return "", fmt.Errorf("no route found for path: %s", path)
	}

	buf := new(bytes.Buffer)
	err := s.Echo.Renderer.Render(buf, route.TemplateName, d, c)
	if err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// getSettingsContentTemplate returns the appropriate content template name for a given settings page
func (s *Server) renderSettingsContent(c echo.Context) (template.HTML, error) {
	path := c.Path()
	settingsType := strings.TrimPrefix(path, "/settings/")
	templateName := fmt.Sprintf("%sSettings", settingsType)

	data := map[string]interface{}{
		"Settings": s.Settings,
		"Locales":  s.prepareLocalesData(),
	}

	if templateName == "detectionfiltersSettings" ||
		templateName == "speciesSettings" {
		data["PreparedSpecies"] = s.prepareSpeciesData()
	}

	log.Printf("Species Settings: %+v", s.Settings.Realtime.Species)

	var buf bytes.Buffer
	err := s.Echo.Renderer.Render(&buf, templateName, data, c)

	if err != nil {
		log.Printf("Error rendering settings content: %v", err)
		return "", err
	}
	return template.HTML(buf.String()), nil
}
