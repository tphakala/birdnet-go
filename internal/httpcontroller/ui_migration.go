package httpcontroller

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// UIMigrationConfig manages the gradual migration from HTMX to Svelte UI
type UIMigrationConfig struct {
	// EnableNewUI globally enables/disables the new UI routes
	EnableNewUI bool

	// MigratedPages lists which pages have been migrated to Svelte
	MigratedPages map[string]bool

	// RedirectToNewUI automatically redirects old routes to new UI routes
	RedirectToNewUI bool
}

// DefaultUIMigrationConfig returns the default migration configuration
func DefaultUIMigrationConfig() *UIMigrationConfig {
	return &UIMigrationConfig{
		EnableNewUI: true,
		MigratedPages: map[string]bool{
			"notifications": true,
			// Add more pages as they are migrated:
			// "dashboard": false,
			// "analytics": false,
			// "search": false,
		},
		RedirectToNewUI: false, // Set to true when ready to redirect users
	}
}

// SetupUIRedirects configures automatic redirects from old to new UI
func (s *Server) SetupUIRedirects(config *UIMigrationConfig) {
	if !config.EnableNewUI || !config.RedirectToNewUI {
		return
	}

	// Set up redirects for migrated pages
	for page, isMigrated := range config.MigratedPages {
		if isMigrated {
			oldPath := "/" + page
			newPath := "/ui/" + page

			// Override the old route with a redirect
			s.Echo.GET(oldPath, func(c echo.Context) error {
				return c.Redirect(http.StatusTemporaryRedirect, newPath)
			})
		}
	}
}

// AddSvelteRoute adds a new Svelte-based UI route
func (s *Server) AddSvelteRoute(path, title string, authorized bool) {
	route := PageRouteConfig{
		Path:         "/ui" + path,
		TemplateName: "svelte-standalone",
		Title:        title,
		Authorized:   authorized,
	}

	s.pageRoutes[route.Path] = route

	if route.Authorized {
		s.Echo.GET(route.Path, s.Handlers.WithErrorHandling(s.handlePageRequest), s.AuthMiddleware)
	} else {
		s.Echo.GET(route.Path, s.Handlers.WithErrorHandling(s.handlePageRequest))
	}
}
