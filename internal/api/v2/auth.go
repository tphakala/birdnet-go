// internal/api/v2/auth.go
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
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
		return c.HandleError(ctx, err, "Invalid login request", http.StatusBadRequest)
	}

	// If authentication is not enabled, return success
	server := ctx.Get("server")
	if server == nil {
		return c.HandleError(ctx, fmt.Errorf("server not available in context"),
			"Authentication service not available", http.StatusInternalServerError)
	}

	// Try to use server's authentication methods
	var authenticated bool
	authServer, ok := server.(interface {
		IsAccessAllowed(c echo.Context) bool
		isAuthenticationEnabled(c echo.Context) bool
		AuthenticateBasic(c echo.Context, username, password string) bool
	})

	if !ok {
		return c.HandleError(ctx, fmt.Errorf("server does not support authentication interface"),
			"Authentication service not available", http.StatusInternalServerError)
	}

	// If authentication is not enabled, act as if the login was successful
	if !authServer.isAuthenticationEnabled(ctx) {
		return ctx.JSON(http.StatusOK, AuthResponse{
			Success:   true,
			Message:   "Authentication is not required on this server",
			Username:  req.Username,
			Timestamp: time.Now(),
		})
	}

	// Authenticate using basic auth
	authenticated = authServer.AuthenticateBasic(ctx, req.Username, req.Password)

	if !authenticated {
		// Add a short delay to prevent brute force attacks
		time.Sleep(500 * time.Millisecond)

		return ctx.JSON(http.StatusUnauthorized, AuthResponse{
			Success:   false,
			Message:   "Invalid credentials",
			Timestamp: time.Now(),
		})
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
	// Get the server from context
	server := ctx.Get("server")
	if server == nil {
		// If no server in context, we can't properly logout
		// But we'll return success anyway since the client is ending their session
		return ctx.JSON(http.StatusOK, AuthResponse{
			Success:   true,
			Message:   "Logged out",
			Timestamp: time.Now(),
		})
	}

	// Try to use server's logout method if available
	if logoutServer, ok := server.(interface {
		Logout(c echo.Context) error
	}); ok {
		if err := logoutServer.Logout(ctx); err != nil {
			return c.HandleError(ctx, err, "Logout failed", http.StatusInternalServerError)
		}
	}

	return ctx.JSON(http.StatusOK, AuthResponse{
		Success:   true,
		Message:   "Logged out successfully",
		Timestamp: time.Now(),
	})
}

// GetAuthStatus handles GET /api/v2/auth/status
func (c *Controller) GetAuthStatus(ctx echo.Context) error {
	// This endpoint is protected by AuthMiddleware, so if we get here,
	// the user is authenticated.

	// Initialize default response
	status := AuthStatus{
		Authenticated: true,
		Method:        "session", // Default to session-based auth
	}

	// Try to get username from server if available
	server := ctx.Get("server")
	if server != nil {
		if userServer, ok := server.(interface {
			GetUsername(c echo.Context) string
			GetAuthMethod(c echo.Context) string
		}); ok {
			status.Username = userServer.GetUsername(ctx)
			status.Method = userServer.GetAuthMethod(ctx)
		}
	}

	return ctx.JSON(http.StatusOK, status)
}
