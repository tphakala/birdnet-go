package httpcontroller

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
)

// initAuthRoutes initializes all authentication related routes
func (s *Server) initAuthRoutes() {
	// OAuth2 routes
	s.Echo.GET("/oauth2/authorize", s.Handlers.WithErrorHandling(s.OAuth2Server.HandleBasicAuthorize))
	s.Echo.POST("/oauth2/token", s.Handlers.WithErrorHandling(s.OAuth2Server.HandleBasicAuthToken))
	s.Echo.GET("/callback", s.Handlers.WithErrorHandling(s.OAuth2Server.HandleBasicAuthCallback))

	// Social authentication routes
	s.Echo.GET("/auth/:provider", s.Handlers.WithErrorHandling(handleGothProvider))
	s.Echo.GET("/auth/:provider/callback", s.Handlers.WithErrorHandling(handleGothCallback))

	// Basic authentication routes
	s.Echo.GET("/login", s.Handlers.WithErrorHandling(s.handleLoginPage))
	s.Echo.POST("/login", s.handleBasicAuthLogin)
	s.Echo.GET("/logout", s.Handlers.WithErrorHandling(s.handleLogout))
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
		return c.String(http.StatusBadRequest, err.Error())
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

		// If no redirect is provided, redirect to the main settings page
		if redirect == "" {
			redirect = "/settings/main"
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

// handleBasicAuthLogin handles password login POST request
func (s *Server) handleBasicAuthLogin(c echo.Context) error {
	password := c.FormValue("password")
	storedPassword := s.Settings.Security.BasicAuth.Password

	if password != storedPassword {
		return c.HTML(http.StatusUnauthorized, "<div class='text-red-500'>Invalid password</div>")
	}

	authCode, err := s.Handlers.OAuth2Server.GenerateAuthCode()
	if err != nil {
		return c.HTML(http.StatusUnauthorized, "<div class='text-red-500'>Unable to login at this time</div>")
	}
	redirectURL := fmt.Sprintf("/callback?code=%s&redirect=%s", authCode, c.FormValue("redirect"))
	c.Response().Header().Set("HX-Redirect", redirectURL)
	return c.String(http.StatusOK, "")
}

// handleLogout logs the user out from all providers
func (s *Server) handleLogout(c echo.Context) error {
	// Logout from all providers
	gothic.StoreInSession("userId", "", c.Request(), c.Response())       //nolint:errcheck
	gothic.StoreInSession("access_token", "", c.Request(), c.Response()) //nolint:errcheck

	// Logout from gothic session
	gothic.Logout(c.Response(), c.Request()) //nolint:errcheck

	// Handle Cloudflare logout if enabled
	if s.Settings.Security.AllowCloudflareBypass && s.CloudflareAccess.IsEnabled(c) {
		return s.CloudflareAccess.Logout(c)
	}

	return c.Redirect(http.StatusFound, "/")
}
