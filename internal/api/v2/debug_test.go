package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

func TestDebugSystemStatus(t *testing.T) {
	// Skip parallel since we need to initialize telemetry
	
	// Initialize settings
	settings := &conf.Settings{
		Debug: true,
		Sentry: conf.SentrySettings{
			Enabled: false, // Use mock mode
		},
	}
	
	// Initialize telemetry system
	err := telemetry.Initialize(settings)
	require.NoError(t, err, "Failed to initialize telemetry")
	
	// Verify that GetGlobalInitCoordinator works
	coord := telemetry.GetGlobalInitCoordinator()
	assert.NotNil(t, coord, "GetGlobalInitCoordinator should return coordinator after initialization")
	
	// Get health status directly
	health := coord.HealthCheck()
	assert.NotNil(t, health, "HealthCheck should return status")
	assert.NotEmpty(t, health.Components, "Should have components")
}

func TestDebugTriggerError(t *testing.T) {
	t.Parallel()
	
	// Create test controller with debug enabled
	e := echo.New()
	c := &Controller{
		Settings: &conf.Settings{
			Debug: true,
		},
		apiLogger: nil,
	}
	
	// Test cases
	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{
			name:     "Valid error trigger",
			body:     `{"component":"test","category":"system","message":"Test error"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "Empty body uses defaults",
			body:     `{}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "Invalid JSON",
			body:     `{invalid}`,
			wantCode: http.StatusBadRequest,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			req := httptest.NewRequest(http.MethodPost, "/api/v2/debug/trigger-error", strings.NewReader(tt.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			
			err := c.DebugTriggerError(ctx)
			
			if tt.wantCode == http.StatusOK {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCode, rec.Code)
				
				var resp DebugResponse
				err = json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.True(t, resp.Success)
			} else {
				assert.Equal(t, tt.wantCode, rec.Code)
			}
		})
	}
}

func TestDebugEndpointsRequireDebugMode(t *testing.T) {
	t.Parallel()
	
	// Create controller with debug disabled
	e := echo.New()
	c := &Controller{
		Settings: &conf.Settings{
			Debug: false, // Debug mode disabled
		},
		apiLogger: nil,
	}
	
	t.Run("DebugTriggerError", func(t *testing.T) {
		t.Parallel()
		
		req := httptest.NewRequest(http.MethodPost, "/api/v2/debug/trigger-error", strings.NewReader(`{}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		
		err := c.DebugTriggerError(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		
		var resp map[string]string
		err = json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, "Debug mode not enabled", resp["error"])
	})
	
	t.Run("DebugTriggerNotification", func(t *testing.T) {
		t.Parallel()
		
		req := httptest.NewRequest(http.MethodPost, "/api/v2/debug/trigger-notification", strings.NewReader(`{}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		
		err := c.DebugTriggerNotification(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
	
	t.Run("DebugSystemStatus", func(t *testing.T) {
		t.Parallel()
		
		req := httptest.NewRequest(http.MethodGet, "/api/v2/debug/status", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		
		err := c.DebugSystemStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}