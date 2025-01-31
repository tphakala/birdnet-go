package httpcontroller

import (
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// initAuthRoutes initializes all authentication related routes
func (s *Server) initAuthRoutes() {
	// Add rate limiter for auth and login routes
	g := s.Echo.Group("")
	g.Use(middleware.RateLimiter(middleware.NewRateLimiterMemoryStore(10)))

	// OAuth2 routes
	g.GET("/oauth2/authorize", s.Handlers.WithErrorHandling(s.OAuth2Server.HandleBasicAuthorize))
	g.POST("/oauth2/token", s.Handlers.WithErrorHandling(s.OAuth2Server.HandleBasicAuthToken))
	g.GET("/callback", s.Handlers.WithErrorHandling(s.OAuth2Server.HandleBasicAuthCallback))

	// Social authentication routes
	g.GET("/auth/:provider", s.Handlers.WithErrorHandling(handleGothProvider))
	g.GET("/auth/:provider/callback", s.Handlers.WithErrorHandling(handleGothCallback))

	// Basic authentication routes
	g.GET("/login", s.Handlers.WithErrorHandling(s.handleLoginPage))
	g.POST("/login", s.handleBasicAuthLogin)
	g.GET("/logout", s.Handlers.WithErrorHandling(s.handleLogout))
}

func handleGothProvider(c echo.Context) error {
	provider := c.Param("provider")
	if provider == "" {
		return c.String(http.StatusBadRequest, "Provider not specified")
	}

	query := c.Request().URL.Query()
	query.Add("provider", c.Param("provider"))
	c.Request().URL.RawQuery = query.Encode()

	request := c.Request()
	response := c.Response().Writer
	if gothUser, err := gothic.CompleteUserAuth(response, request); err == nil {
		return c.JSON(http.StatusOK, gothUser)
	}
	gothic.BeginAuthHandler(response, request)
	return nil
}

// handleGothCallback handles callbacks from OAuth2 providers
func handleGothCallback(c echo.Context) error {
	request := c.Request()
	response := c.Response().Writer
	user, err := gothic.CompleteUserAuth(response, request)
	if err != nil {
		return c.String(http.StatusBadRequest, "Authentication failed")
	}

	// Store provider and user info in session
	if err := gothic.StoreInSession(c.Param("provider"), user.UserID, c.Request(), c.Response()); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to store provider to session")
	}
	if err := gothic.StoreInSession("userId", user.Email, c.Request(), c.Response()); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to store user to session")
	}

	return c.Redirect(http.StatusTemporaryRedirect, "/")
}

// handleLoginPage renders the login modal content
func (s *Server) handleLoginPage(c echo.Context) error {
	// If the request is a partial request, render the login modal
	if c.Request().Header.Get("HX-Request") == "true" {
		redirect := c.QueryParam("redirect")

		// Validate the redirect parameter
		if !isValidRedirect(redirect) {
			redirect = "/"
		}

		// If no redirect is provided, redirect to the dashboard
		if redirect == "" {
			redirect = "/"
		}

		return c.Render(http.StatusOK, "login", map[string]interface{}{
			"RedirectURL":   redirect,
			"BasicEnabled":  s.Settings.Security.BasicAuth.Enabled,
			"GoogleEnabled": s.Settings.Security.GoogleAuth.Enabled,
			"GithubEnabled": s.Settings.Security.GithubAuth.Enabled,
		})
	}

	// Otherwise, render the dashboard and let the client open the modal
	return c.Render(http.StatusOK, "index", RenderData{
		C:        c,
		Page:     "dashboard",
		Title:    "Dashboard",
		Settings: s.Settings,
	})
}

// isValidRedirect ensures the redirect path is safe and internal
func isValidRedirect(redirectPath string) bool {
	return conf.IsSafePath(redirectPath)
}

// handleBasicAuthLogin handles password login POST request
func (s *Server) handleBasicAuthLogin(c echo.Context) error {
	password := c.FormValue("password")
	storedPassword := s.Settings.Security.BasicAuth.Password

	if subtle.ConstantTimeCompare([]byte(password), []byte(storedPassword)) != 1 {
		return c.HTML(http.StatusUnauthorized, "<div class='text-red-500'>Invalid password</div>")
	}

	authCode, err := s.Handlers.OAuth2Server.GenerateAuthCode()
	if err != nil {
		return c.HTML(http.StatusUnauthorized, "<div class='text-red-500'>Unable to login at this time</div>")
	}
	redirect := c.FormValue("redirect")
	if !isValidRedirect(redirect) {
		redirect = "/"
	}
	redirectURL := fmt.Sprintf("/callback?code=%s&redirect=%s", authCode, redirect)
	c.Response().Header().Set("HX-Redirect", redirectURL)
	return c.String(http.StatusOK, "")
}

// handleLogout logs the user out from all providers
func (s *Server) handleLogout(c echo.Context) error {
	// Logout from all providers
	gothic.StoreInSession("userId", "", c.Request(), c.Response())       //nolint:errcheck // session errors during logout can be ignored
	gothic.StoreInSession("access_token", "", c.Request(), c.Response()) //nolint:errcheck // session errors during logout can be ignored

	// Logout from gothic session
	gothic.Logout(c.Response(), c.Request()) //nolint:errcheck // gothic logout errors can be ignored during cleanup

	// Handle Cloudflare logout if enabled
	if s.CloudflareAccess.IsEnabled(c) {
		return s.CloudflareAccess.Logout(c)
	}

	return c.Redirect(http.StatusFound, "/")
}
