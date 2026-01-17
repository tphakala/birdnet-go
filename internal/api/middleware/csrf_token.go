// Package middleware provides HTTP middleware for the BirdNET-Go API server.
package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Note: CSRF constants (csrfCookieName, csrfCookieMaxAge, csrfTokenLength)
// are defined in csrf.go and shared within this package.

// setCSRFCookie creates and sets a CSRF cookie with the given token value.
// This helper ensures consistent cookie configuration across all code paths.
func setCSRFCookie(ctx echo.Context, token string) {
	isSecure := IsSecureRequest(ctx.Request())
	cookie := &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   csrfCookieMaxAge,
		HttpOnly: false, // Allow JS to read for SPA usage
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
	}
	ctx.SetCookie(cookie)
}

// GenerateCSRFToken creates a cryptographically secure CSRF token.
// Returns a base64-encoded random token suitable for use as a CSRF token.
func GenerateCSRFToken() (string, error) {
	bytes := make([]byte, csrfTokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(bytes), nil
}

// EnsureCSRFToken checks if a CSRF token exists in context/cookie,
// generates one if missing, and returns the token.
//
// This function is designed for endpoints that need to PROVIDE tokens
// (like /api/v2/app/config) rather than just validate them.
//
// Echo v4.15.0 introduced Sec-Fetch-Site header-based CSRF protection which
// may skip token generation for "safe" same-origin requests. This function
// ensures a token is always available for endpoints that need to return one.
//
// The function follows this priority:
//  1. Use token from Echo context (set by CSRF middleware)
//  2. Use token from existing CSRF cookie
//  3. Generate a new token and set the cookie
func EnsureCSRFToken(ctx echo.Context) (string, error) {
	// First, check if middleware already set a token
	if token, ok := ctx.Get(CSRFContextKey).(string); ok && token != "" {
		return token, nil
	}

	// Check if there's an existing cookie we can use
	if cookie, err := ctx.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		// Reuse existing token from cookie
		ctx.Set(CSRFContextKey, cookie.Value)

		// Refresh cookie expiration (sliding window) to prevent
		// token expiry during active user sessions
		setCSRFCookie(ctx, cookie.Value)

		GetLogger().Debug("CSRF cookie expiration refreshed",
			logger.String("source", "EnsureCSRFToken"))

		return cookie.Value, nil
	}

	// Generate a new token
	token, err := GenerateCSRFToken()
	if err != nil {
		return "", err
	}

	// Set the cookie and context
	setCSRFCookie(ctx, token)
	ctx.Set(CSRFContextKey, token)

	GetLogger().Debug("CSRF token generated",
		logger.String("source", "EnsureCSRFToken"),
		logger.Bool("secure_cookie", IsSecureRequest(ctx.Request())))

	return token, nil
}
