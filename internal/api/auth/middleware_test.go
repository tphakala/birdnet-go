package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/security/securitytest"
)

func TestAuthenticateMalformedHeaderAbortsChain(t *testing.T) {
	// Setup mock auth service
	settings := &conf.Settings{}
	settings.Security.BasicAuth.Enabled = true
	settings.Security.BasicAuth.ClientID = "admin"
	settings.Security.BasicAuth.Password = "correct-horse"
	server := securitytest.NewOAuth2ServerForTesting(t, settings)
	adapter := NewSecurityAdapter(server)

	// Create middleware
	mw := NewMiddleware(adapter)

	e := echo.New()

	// Track whether handler was executed
	handlerExecuted := false

	// Create protected endpoint
	protectedHandler := func(c echo.Context) error {
		handlerExecuted = true
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}

	// Create test request with malformed authorization header
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/detections/999", http.NoBody)
	// Malformed auth header (Basic instead of Bearer)
	req.Header.Set("Authorization", "Basic YWRtaW46Y29ycmVjdC1ob3JzZQ==")

	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Execute middleware chain
	handler := mw.Authenticate(protectedHandler)
	err := handler(c)

	// Validate response
	require.NoError(t, err) // Middleware chain execution should not return an unhandled error
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Handler should NOT have been executed
	assert.False(t, handlerExecuted, "Handler should not execute after authentication failure")
}
