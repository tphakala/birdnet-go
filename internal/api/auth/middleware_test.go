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

// newBasicAuthMiddleware builds a Middleware backed by a real security adapter
// with basic auth enabled, matching the failing scenario from issue #3929.
//
// Note: these tests are intentionally NOT parallel. NewOAuth2ServerForTesting
// publishes settings into the global conf snapshot via conf.StoreSettings, so
// running them concurrently with other settings-mutating tests would race.
func newBasicAuthMiddleware(t *testing.T) *Middleware {
	t.Helper()
	settings := &conf.Settings{}
	settings.Security.BasicAuth.Enabled = true
	settings.Security.BasicAuth.ClientID = "admin"
	settings.Security.BasicAuth.Password = "correct-horse"
	server := securitytest.NewOAuth2ServerForTesting(t, settings)
	return NewMiddleware(NewSecurityAdapter(server))
}

// TestAuthenticateAbortsChainOnAuthFailure verifies that every auth-failure path
// in the middleware writes its response AND terminates the chain, so the route
// handler never runs after a failed authentication. This is the regression guard
// for issue #3929, where c.JSON returning nil let the handler execute past a 401
// (an unauthenticated mutation on routes like DELETE /api/v2/detections/:id).
func TestAuthenticateAbortsChainOnAuthFailure(t *testing.T) {
	tests := []struct {
		name       string
		middleware func(t *testing.T) *Middleware
		authHeader string
		wantStatus int
	}{
		{
			name:       "malformed header (Basic instead of Bearer)",
			middleware: newBasicAuthMiddleware,
			authHeader: "Basic YWRtaW46Y29ycmVjdC1ob3JzZQ==",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid bearer token",
			middleware: newBasicAuthMiddleware,
			authHeader: "Bearer not-a-real-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "nil auth service",
			middleware: func(t *testing.T) *Middleware {
				t.Helper()
				return NewMiddleware(nil)
			},
			authHeader: "Bearer not-a-real-token",
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mw := tt.middleware(t)
			e := echo.New()

			// Track whether the protected handler was executed.
			handlerExecuted := false
			protectedHandler := func(c echo.Context) error {
				handlerExecuted = true
				return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
			}

			req := httptest.NewRequest(http.MethodDelete, "/api/v2/detections/999", http.NoBody)
			req.Header.Set("Authorization", tt.authHeader)

			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := mw.Authenticate(protectedHandler)(c)

			// The middleware writes the response itself and returns nil, so the
			// chain should not surface an unhandled error to Echo.
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.False(t, handlerExecuted, "handler must not execute after authentication failure")
		})
	}
}
