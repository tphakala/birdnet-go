package httpcontroller

import (
	"crypto/subtle"
	"fmt"
	"log"
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
	g.GET("/api/v1/oauth2/authorize", s.Handlers.WithErrorHandling(s.OAuth2Server.HandleBasicAuthorize))
	g.POST("/api/v1/oauth2/token", s.Handlers.WithErrorHandling(s.OAuth2Server.HandleBasicAuthToken))
	g.GET("/api/v1/oauth2/callback", s.Handlers.WithErrorHandling(s.OAuth2Server.HandleBasicAuthCallback))

	// Social authentication routes
	g.GET("/api/v1/auth/:provider", s.Handlers.WithErrorHandling(handleGothProvider))
	g.GET("/api/v1/auth/:provider/callback", s.Handlers.WithErrorHandling(handleGothCallback))

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

	// Complete authentication with the provider
	user, err := gothic.CompleteUserAuth(response, request)
	if err != nil {
		// Log the error using the server's logger if available
		if srv := c.Get("server"); srv != nil {
			if server, ok := srv.(*Server); ok {
				server.LogError(c, err, "Social authentication failed during CompleteUserAuth")
			}
		}
		return c.String(http.StatusBadRequest, "Authentication failed: "+err.Error())
	}

	// Log session regeneration attempt
	if err := gothic.Logout(c.Response().Writer, c.Request()); err != nil {
		// Log warning but continue
		// Try server's structured logger first, fallback to standard log
		if srv := c.Get("server"); srv != nil {
			if server, ok := srv.(*Server); ok && server.webLogger != nil {
				server.webLogger.Warn("Error regenerating session after social login (potential fixation risk)",
					"provider", c.Param("provider"), "user_email", user.Email, "error", err)
			} else {
				log.Printf("WARN: [Social Login - %s - %s] Error regenerating session: %v",
					c.Param("provider"), user.Email, err)
			}
		} else {
			log.Printf("WARN: [Social Login - %s - %s] Error regenerating session: %v",
				c.Param("provider"), user.Email, err)
		}
	} else {
		// Log success using server's structured logger or fallback
		if srv := c.Get("server"); srv != nil {
			if server, ok := srv.(*Server); ok && server.webLogger != nil {
				server.webLogger.Info("Successfully regenerated session after social login (fixation prevention)",
					"provider", c.Param("provider"), "user_email", user.Email)
			} else {
				log.Printf("INFO: [Social Login - %s - %s] Successfully regenerated session",
					c.Param("provider"), user.Email)
			}
		} else {
			log.Printf("INFO: [Social Login - %s - %s] Successfully regenerated session",
				c.Param("provider"), user.Email)
		}
	}

	// Store provider and user info in the *new* session
	// Use more specific keys and log potential errors
	providerKey := fmt.Sprintf("%s_userID", c.Param("provider")) // e.g., google_userID
	if err := gothic.StoreInSession(providerKey, user.UserID, c.Request(), c.Response()); err != nil {
		// Log error using server's structured logger or fallback
		if srv := c.Get("server"); srv != nil {
			if server, ok := srv.(*Server); ok && server.webLogger != nil {
				server.webLogger.Error("Failed to store provider user ID in new session",
					"provider", c.Param("provider"), "key", providerKey, "error", err)
			} else {
				log.Printf("ERROR: Failed to store provider user ID (%s) in new session: %v", providerKey, err)
			}
		} else {
			log.Printf("ERROR: Failed to store provider user ID (%s) in new session: %v", providerKey, err)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Session error after social login (storing provider user ID)")
	}
	// Standardize on storing email in 'userEmail' key
	if err := gothic.StoreInSession("userEmail", user.Email, c.Request(), c.Response()); err != nil {
		// Log error using server's structured logger or fallback
		if srv := c.Get("server"); srv != nil {
			if server, ok := srv.(*Server); ok && server.webLogger != nil {
				server.webLogger.Error("Failed to store user email in new session",
					"provider", c.Param("provider"), "error", err)
			} else {
				log.Printf("ERROR: Failed to store user email in new session: %v", err)
			}
		} else {
			log.Printf("ERROR: Failed to store user email in new session: %v", err)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Session error after social login (storing email)")
	}

	// Optionally store raw data if needed, but be mindful of session size
	// rawDataKey := fmt.Sprintf("%s_raw", c.Param("provider"))
	// if err := gothic.StoreInSession(rawDataKey, user.RawData, c.Request(), c.Response()); err != nil {
	// 	 securityLogger.Warn("Failed to store provider raw data in session", "provider": c.Param("provider"), "error": err)
	// 	 // Usually non-fatal if raw data isn't strictly required
	// }

	// Get redirect URL from session state or default
	// Note: Goth's default state handling might need configuration or custom implementation
	// if you need specific redirect logic after social login.
	redirectURL := "/" // Default redirect
	// Example of how you might retrieve state if set during BeginAuthHandler
	// if stateRedirect, err := gothic.GetFromSession("oauth_redirect", request); err == nil && stateRedirect != "" {
	// 	 if isValidRedirect(stateRedirect) { // Validate!
	// 		 redirectURL = stateRedirect
	// 	 }
	// 	 // Clear the state from session after use
	// 	 _ = gothic.StoreInSession("oauth_redirect", "", request, c.Response())
	// }

	// Log success using server logger or fallback
	if srv := c.Get("server"); srv != nil {
		if server, ok := srv.(*Server); ok && server.webLogger != nil {
			server.webLogger.Info("Social login successful, redirecting",
				"provider", c.Param("provider"), "user_email", user.Email, "redirect_to", redirectURL)
		} else {
			// Create msg here for fallback logging
			successMsg := fmt.Sprintf("Social login successful, redirecting to %s", redirectURL)
			log.Printf("INFO: [Social Login - %s - %s] %s",
				c.Param("provider"), user.Email, successMsg)
		}
	} else {
		// Create msg here for fallback logging
		successMsg := fmt.Sprintf("Social login successful, redirecting to %s", redirectURL)
		log.Printf("INFO: [Social Login - %s - %s] %s",
			c.Param("provider"), user.Email, successMsg)
	}

	return c.Redirect(http.StatusTemporaryRedirect, redirectURL)
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
			"CSRFToken":     c.Get(CSRFContextKey),
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
	redirectURL := fmt.Sprintf("/api/v1/oauth2/callback?code=%s&redirect=%s", authCode, redirect)
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

	return c.Redirect(http.StatusFound, "/")
}
