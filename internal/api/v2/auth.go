// internal/api/v2/auth.go
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	// Removed: "github.com/tphakala/birdnet-go/internal/api/v2/auth"
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

	// Get auth service
	authService := c.getAuthService(ctx)
	if authService == nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Authentication service unavailable",
				"error", "auth service not available",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return c.HandleError(ctx, fmt.Errorf("authentication service not available"),
			"Authentication service not available", http.StatusInternalServerError)
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

	// Authenticate using basic auth
	authenticated := authService.AuthenticateBasic(ctx, req.Username, req.Password)

	if !authenticated {
		// Add a short delay to prevent brute force attacks
		time.Sleep(500 * time.Millisecond)

		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed login attempt",
				"username", req.Username,
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
				"error", "Invalid credentials",
			)
		}

		return ctx.JSON(http.StatusUnauthorized, AuthResponse{
			Success:   false,
			Message:   "Invalid credentials",
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
	// Get auth service
	authService := c.getAuthService(ctx)
	if authService == nil {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Logout requested but auth service is not available",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		// Return success anyway as the session is effectively ended
		return ctx.JSON(http.StatusOK, AuthResponse{
			Success:   true,
			Message:   "Logged out",
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
	isAuthenticated := false
	if authVal := ctx.Get("isAuthenticated"); authVal != nil {
		if val, ok := authVal.(bool); ok {
			isAuthenticated = val
		}
	}

	username := ""
	if userVal := ctx.Get("username"); userVal != nil {
		if val, ok := userVal.(string); ok {
			username = val
		}
	}

	authMethod := "unknown"
	if methodVal := ctx.Get("authMethod"); methodVal != nil {
		if val, ok := methodVal.(string); ok {
			authMethod = val
		}
	}

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
