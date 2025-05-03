// internal/api/v2/auth.go
package api

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	auth "github.com/tphakala/birdnet-go/internal/api/v2/auth"
)

// AuthRequest represents the login request structure
type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthResponse represents the login response structure
type AuthResponse struct {
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	Username  string    `json:"username,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	// In a real token-based auth system, we would return tokens here
	// Token     string    `json:"token,omitempty"`
	// ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// AuthStatus represents the current authentication status
type AuthStatus struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
	Method        string `json:"auth_method,omitempty"`
}

// initAuthRoutes registers all authentication-related API endpoints
func (c *Controller) initAuthRoutes() {
	// Create auth API group
	authGroup := c.Group.Group("/auth")

	// Routes that don't require authentication
	authGroup.POST("/login", c.Login)

	// Routes that require authentication
	protectedGroup := authGroup.Group("", c.AuthMiddleware)
	protectedGroup.POST("/logout", c.Logout)
	protectedGroup.GET("/status", c.GetAuthStatus)
}

// Login handles POST /api/v2/auth/login
func (c *Controller) Login(ctx echo.Context) error {
	// Parse login request
	var req AuthRequest
	if err := ctx.Bind(&req); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid login request",
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return c.HandleError(ctx, err, "Invalid login request", http.StatusBadRequest)
	}

	// Use the stored auth service instance
	authService := c.AuthService
	if authService == nil {
		// Handle case where auth might not be configured but login endpoint is hit
		if c.apiLogger != nil {
			c.apiLogger.Error("Login attempt but AuthService is nil (auth not configured?)",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		// Return a generic error, perhaps indicating auth isn't enabled
		return c.HandleError(ctx, errors.New("authentication not configured"),
			"Authentication service unavailable", http.StatusInternalServerError)
	}

	// If authentication is not required, act as if the login was successful
	if !authService.IsAuthRequired(ctx) {
		if c.apiLogger != nil {
			c.apiLogger.Info("Authentication not required",
				"username", req.Username,
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return ctx.JSON(http.StatusOK, AuthResponse{
			Success:   true,
			Message:   "Authentication is not required on this server",
			Username:  req.Username,
			Timestamp: time.Now(),
		})
	}

	// Check for empty credentials before calling the auth service
	if req.Username == "" || req.Password == "" {
		// Add a short, randomized delay to mitigate timing attacks on username enumeration
		randomDelay(ctx.Request().Context(), 50, 150)

		if c.apiLogger != nil {
			c.apiLogger.Warn("Login attempt with missing credentials",
				"username_present", req.Username != "",
				"password_present", req.Password != "",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}

		return ctx.JSON(http.StatusBadRequest, AuthResponse{
			Success:   false,
			Message:   "Username and password are required",
			Timestamp: time.Now(),
		})
	}

	// Authenticate using basic auth
	authErr := authService.AuthenticateBasic(ctx, req.Username, req.Password)

	if authErr != nil {
		// Add a short, randomized delay to mitigate brute force/timing attacks
		randomDelay(ctx.Request().Context(), 50, 150)

		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed login attempt",
				"username", req.Username,
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
				"error", authErr.Error(), // Use the error from the service
			)
		}

		// Use the error message from the sentinel error if appropriate
		message := "Invalid credentials"
		if errors.Is(authErr, auth.ErrInvalidCredentials) {
			message = auth.ErrInvalidCredentials.Error()
		}

		return ctx.JSON(http.StatusUnauthorized, AuthResponse{
			Success:   false,
			Message:   message,
			Timestamp: time.Now(),
		})
	}

	// Successful login
	if c.apiLogger != nil {
		c.apiLogger.Info("Successful login",
			"username", req.Username,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// In a token-based auth system, we would generate and return tokens here
	// For now, we'll rely on the server's session-based auth

	return ctx.JSON(http.StatusOK, AuthResponse{
		Success:   true,
		Message:   "Login successful",
		Username:  req.Username,
		Timestamp: time.Now(),
	})
}

// Logout handles POST /api/v2/auth/logout
func (c *Controller) Logout(ctx echo.Context) error {
	// Use the stored auth service instance
	authService := c.AuthService
	if authService == nil {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Logout requested but AuthService is nil (auth not configured?)",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		// Return success even if service isn't available, as logout intent is met.
		return ctx.JSON(http.StatusOK, AuthResponse{
			Success:   true,
			Message:   "Logged out (auth service unavailable)",
			Timestamp: time.Now(),
		})
	}

	// Try to perform logout via auth service
	if err := authService.Logout(ctx); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Logout failed",
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return c.HandleError(ctx, err, "Logout failed", http.StatusInternalServerError)
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("User logged out",
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	return ctx.JSON(http.StatusOK, AuthResponse{
		Success:   true,
		Message:   "Logged out successfully",
		Timestamp: time.Now(),
	})
}

// GetAuthStatus handles GET /api/v2/auth/status
func (c *Controller) GetAuthStatus(ctx echo.Context) error {
	// Read authentication status details set by the AuthMiddleware in the context.
	isAuthenticated := boolFromCtx(ctx, "isAuthenticated", false)
	username := stringFromCtx(ctx, "username", "")
	// Read the method as a string from context for now.
	// Downstream consumers comparing this value might need updates if they
	// relied on specific string literals. The middleware now sets the context
	// value using the string representation of the new AuthMethod constants.
	authMethod := stringFromCtx(ctx, "authMethod", auth.AuthMethodUnknown.String())

	// Construct the response based on context values
	status := AuthStatus{
		Authenticated: isAuthenticated,
		Username:      username,
		Method:        authMethod,
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Auth status check",
			"authenticated", status.Authenticated,
			"username", status.Username,
			"method", status.Method,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
			"user_agent", ctx.Request().Header.Get("User-Agent"),
		)
	}

	return ctx.JSON(http.StatusOK, status)
}

// --- Context Helper Functions ---

// boolFromCtx safely retrieves a boolean value from the Echo context.
// Returns the defaultValue if the key is not found or the type assertion fails.
func boolFromCtx(ctx echo.Context, key string, defaultValue bool) bool {
	val := ctx.Get(key)
	if val == nil {
		return defaultValue
	}
	if boolVal, ok := val.(bool); ok {
		return boolVal
	}
	return defaultValue
}

// stringFromCtx safely retrieves a string value from the Echo context.
// Returns the defaultValue if the key is not found or the type assertion fails.
// It specifically handles values of type auth.AuthMethod by converting them to string.
func stringFromCtx(ctx echo.Context, key, defaultValue string) string {
	val := ctx.Get(key)
	if val == nil {
		return defaultValue
	}

	// Check if it's already a string
	if stringVal, ok := val.(string); ok {
		return stringVal
	}

	// Check if it's an auth.AuthMethod type
	if authMethodVal, ok := val.(auth.AuthMethod); ok {
		return authMethodVal.String()
	}

	// If neither string nor auth.AuthMethod, return default
	return defaultValue
}

// --- End Context Helper Functions ---

// randomDelay introduces a random sleep duration within the specified range [minMs, maxMs).
// It accepts a context to allow cancellation of the delay.
func randomDelay(ctx context.Context, minMs, maxMs int64) {
	if maxMs <= minMs {
		minMs = 50  // Default min
		maxMs = 150 // Default max
	}
	rangeSize := big.NewInt(maxMs - minMs)
	n, err := rand.Int(rand.Reader, rangeSize)
	var delayMs int64
	if err != nil {
		// Fallback to min delay if random generation fails
		delayMs = minMs
		// Optionally log the error
		// log.Printf("Error generating random delay: %v, using fallback %dms", err, delayMs)
	} else {
		delayMs = n.Int64() + minMs
	}

	delayDuration := time.Duration(delayMs) * time.Millisecond

	// Create a timer for the delay
	timer := time.NewTimer(delayDuration)
	defer timer.Stop() // Ensure timer resources are released

	// Wait for the timer or context cancellation
	select {
	case <-timer.C:
		// Delay completed normally
	case <-ctx.Done():
		// No need to call timer.Stop() here, defer handles it.
		// Context was cancelled, delay aborted
		// Optionally log context cancellation
		// log.Printf("Random delay cancelled by context: %v", ctx.Err())
	}
}
