package httpcontroller

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/security"
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

// storeInGothicSession handles storing a key-value pair in the gothic session,
// including logging and basic error handling. Returns true on success, false on failure.
// It logs errors using the security logger.
func storeInGothicSession(c echo.Context, key, value, userEmail, providerName string) bool {
	err := gothic.StoreInSession(key, value, c.Request(), c.Response())
	if err != nil {
		errMsg := fmt.Sprintf("Failed to store '%s' in new session for user '%s'", key, userEmail)
		// Log security-sensitive session storage failure
		security.LogError(errMsg,
			"provider", providerName,
			"user_email", userEmail,
			"session_key", key,
			"error", err.Error(),
		)
		return false // Indicate failure
	}
	return true // Indicate success
}

// handleGothCallback handles callbacks from OAuth2 providers
func handleGothCallback(c echo.Context) error {
	request := c.Request()
	response := c.Response().Writer
	providerName := c.Param("provider") // Get provider early

	// Complete authentication with the provider
	user, err := gothic.CompleteUserAuth(response, request)
	if err != nil {
		logCompleteUserAuthError(providerName, err) // Use helper with structured log
		// Use echo.NewHTTPError for consistent error handling
		return echo.NewHTTPError(http.StatusBadRequest, "Authentication failed. See server logs for details.") // More generic user message
	}

	// Log session regeneration attempt (Security relevant: Session Fixation Mitigation)
	if err := gothic.Logout(c.Response().Writer, c.Request()); err != nil {
		// Log warning but continue - Use Security Logger
		security.LogWarn("Error during gothic.Logout (session regeneration step 1)",
			"provider", providerName,
			"user_email", user.Email,
			"error", err.Error())
	} else {
		// Attempt to explicitly save the session immediately after logout to force regeneration
		// This helps ensure a new session ID is used before storing new data.
		session, err := gothic.Store.Get(request, gothic.SessionName)
		if err == nil {
			session.Options.MaxAge = -1 // Ensure cookie deletion on client
			err = session.Save(request, response)
		}

		// Log success or failure of explicit regeneration step (Security relevant)
		if err != nil {
			security.LogWarn("Failed to explicitly save cleared session after logout (potential fixation risk)",
				"provider", providerName,
				"user_email", user.Email,
				"error", err.Error())
		} else {
			security.LogInfo("Successfully cleared old session state (session fixation mitigation)",
				"provider", providerName,
				"user_email", user.Email)
		}
	}

	// Store provider and user info in the *new* session
	// Use more specific keys and log potential errors
	providerKey := fmt.Sprintf("%s_userID", providerName) // e.g., google_userID
	if !storeInGothicSession(c, providerKey, user.UserID, user.Email, providerName) {
		// --- ROLLBACK SESSION ---
		security.LogError("Rolling back session due to failure storing providerUserID",
			"provider", providerName,
			"user_email", user.Email,
			"key_failed", providerKey,
		)
		err := gothic.Logout(c.Response().Writer, c.Request()) // Attempt to clear the session
		if err != nil {
			security.LogError("Failed to logout session during rollback after providerUserID failure",
				"provider", providerName,
				"user_email", user.Email,
				"rollback_error", err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Session error after social login (code: PUID)")
	}

	// Standardize on storing email in 'userEmail' key
	if !storeInGothicSession(c, "userEmail", user.Email, user.Email, providerName) {
		// --- ROLLBACK SESSION ---
		security.LogError("Rolling back session due to failure storing userEmail",
			"provider", providerName,
			"user_email", user.Email,
			"key_failed", "userEmail",
			"prior_key_stored", providerKey, // Note which key *was* stored
		)
		err := gothic.Logout(c.Response().Writer, c.Request()) // Attempt to clear the session
		if err != nil {
			security.LogError("Failed to logout session during rollback after userEmail failure",
				"provider", providerName,
				"user_email", user.Email,
				"rollback_error", err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Session error after social login (code: EMAIL)")
	}

	// Optional: Store raw data (Consider logging this via security.LogInfo if enabled)
	// rawDataKey := fmt.Sprintf("%s_raw", providerName)
	// if err := gothic.StoreInSession(rawDataKey, user.RawData, request, response); err != nil {
	//  security.LogWarn("Failed to store provider raw data in session", "provider", providerName, "user_email", user.Email, "error", err.Error())
	//  // Usually non-fatal
	// } else {
	//  security.LogInfo("Stored provider raw data in session", "provider", providerName, "user_email", user.Email, "key", rawDataKey)
	// }

	// Get redirect URL from session state or default
	// Note: Goth's default state handling might need configuration or custom implementation
	// if you need specific redirect logic after social login.
	redirectURL := "/" // Default redirect
	// Example retrieval (ensure validation)
	// if stateRedirect, err := gothic.GetFromSession("oauth_redirect", request); err == nil && stateRedirect != "" {
	//  if isValidRedirect(stateRedirect) { // Validate!
	//      redirectURL = stateRedirect
	//      security.LogInfo("Using redirect URL from session state", "provider", providerName, "user_email", user.Email, "redirect_url", redirectURL)
	//  } else {
	//      security.LogWarn("Invalid redirect URL found in session state, using default", "provider", providerName, "user_email", user.Email, "invalid_url", stateRedirect)
	//  }
	//  // Clear the state from session after use
	//  _ = gothic.StoreInSession("oauth_redirect", "", request, response) // Log error? Maybe security.LogWarn
	// }

	// Log successful social login (Security relevant)
	security.LogInfo("Social login successful, redirecting",
		"provider", providerName,
		"user_email", user.Email,
		"redirect_to", redirectURL,
	)

	return c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

// logCompleteUserAuthError is a helper to log errors from gothic.CompleteUserAuth using security logger
func logCompleteUserAuthError(providerName string, authErr error) {
	security.LogError("Social authentication failed during CompleteUserAuth",
		"provider", providerName,
		"error", authErr.Error(), // Use Error() method for message
		// Consider adding more context if available, e.g., parts of the request? IP?
	)
}

// handleLoginPage renders the login modal content
func (s *Server) handleLoginPage(c echo.Context) error {
	// If the request is a partial request, render the login modal
	if c.Request().Header.Get("HX-Request") == "true" {
		redirect := c.QueryParam("redirect")

		// Validate the redirect parameter
		if !security.IsValidRedirect(redirect) {
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

// handleBasicAuthLogin handles password login POST request
func (s *Server) handleBasicAuthLogin(c echo.Context) error {
	password := c.FormValue("password")
	storedPassword := s.Settings.Security.BasicAuth.Password
	username := "basic_auth_user" // Define a username for logging

	// Log basic auth attempt
	security.LogInfo("Basic authentication login attempt", "username", username)

	// Hash passwords before comparison for constant-time behavior
	passwordHash := sha256.Sum256([]byte(password))
	storedPasswordHash := sha256.Sum256([]byte(storedPassword))

	if subtle.ConstantTimeCompare(passwordHash[:], storedPasswordHash[:]) != 1 {
		// Log failed basic auth attempt
		security.LogWarn("Basic authentication failed: Invalid password", "username", username)
		return c.HTML(http.StatusUnauthorized, "<div class='text-red-500'>Invalid password</div>")
	}

	// Log successful basic auth attempt
	security.LogInfo("Basic authentication successful", "username", username)

	// Generate OAuth2 authorization code after successful basic authentication
	authCode, err := s.OAuth2Server.GenerateAuthCode()
	if err != nil {
		// Log internal error during auth code generation
		security.LogError("Failed to generate OAuth2 auth code after basic auth success", "username", username, "error", err.Error())
		return c.HTML(http.StatusInternalServerError, "<div class='text-red-500'>Unable to complete login at this time (Code: GEN)</div>")
	}

	redirect := c.FormValue("redirect")
	if !security.IsValidRedirect(redirect) {
		security.LogWarn("Invalid redirect path provided during basic auth login, using default '/'", "provided_redirect", redirect, "username", username)
		redirect = "/"
	}
	redirectURL := fmt.Sprintf("/api/v1/oauth2/callback?code=%s&redirect=%s", authCode, redirect)
	c.Response().Header().Set("HX-Redirect", redirectURL)
	security.LogInfo("Redirecting user after successful basic auth", "username", username, "redirect_url", redirectURL)
	return c.String(http.StatusOK, "")
}

// handleLogout logs the user out from all providers
func (s *Server) handleLogout(c echo.Context) error {
	// Attempt to get user identifier from session before clearing it
	var userIdentifier string
	var provider string

	// Check common keys - adapt if using different/multiple keys
	if email, err := gothic.GetFromSession("userEmail", c.Request()); err == nil && email != "" {
		userIdentifier = email
		provider = "session(email)"
	} else if userID, err := gothic.GetFromSession("userId", c.Request()); err == nil && userID != "" {
		// Fallback or alternative identifier
		userIdentifier = userID
		provider = "session(userId)"
	} else {
		userIdentifier = "unknown"
		provider = "unknown"
	}

	security.LogInfo("Logout initiated", "user_identifier", userIdentifier, "provider", provider)

	// Logout from all providers - suppress individual errors during mass logout
	_ = gothic.StoreInSession("userId", "", c.Request(), c.Response())
	_ = gothic.StoreInSession("access_token", "", c.Request(), c.Response())
	_ = gothic.StoreInSession("userEmail", "", c.Request(), c.Response()) // Clear email too

	// Logout from gothic session
	err := gothic.Logout(c.Response().Writer, c.Request())
	if err != nil {
		// Log if the main gothic logout fails
		security.LogWarn("Error during main gothic.Logout on user logout",
			"user_identifier", userIdentifier,
			"provider", provider,
			"error", err.Error())
	}

	security.LogInfo("Logout completed, redirecting", "user_identifier", userIdentifier, "provider", provider)
	return c.Redirect(http.StatusFound, "/")
}
