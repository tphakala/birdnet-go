// api_test.go: Package api provides tests for API v2 endpoints.

package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// healthResponse mirrors the fields HealthCheck emits. CPUUsage is a pointer so
// an absent key reads as nil rather than zero.
type healthResponse struct {
	Status         string `json:"status"`
	Version        string `json:"version"`
	BuildDate      string `json:"build_date"`
	Timestamp      string `json:"timestamp"`
	Environment    string `json:"environment"`
	DatabaseStatus string `json:"database_status"`
	System         struct {
		CPUUsage  *float64           `json:"cpu_usage"`
		Memory    map[string]float64 `json:"memory"`
		DiskSpace map[string]float64 `json:"disk_space"`
	} `json:"system"`
}

// TestHealthCheck covers the three database branches of HealthCheck and the
// response keys it emits.
func TestHealthCheck(t *testing.T) {
	const (
		testVersion   = "1.2.3"
		testBuildDate = "2023-05-15"
	)

	tests := []struct {
		name         string
		setup        func(controller *Controller, mockDS *mocks.MockInterface)
		wantDBStatus string
	}{
		{
			name: "database connected",
			setup: func(_ *Controller, mockDS *mocks.MockInterface) {
				mockDS.On("GetLastDetections", 1).Return([]datastore.Note{}, nil).Once()
			},
			wantDBStatus: "connected",
		},
		{
			name: "database disconnected",
			setup: func(_ *Controller, mockDS *mocks.MockInterface) {
				mockDS.On("GetLastDetections", 1).Return([]datastore.Note(nil), errors.NewStd("connection refused")).Once()
			},
			wantDBStatus: "disconnected",
		},
		{
			name: "datastore disabled",
			setup: func(controller *Controller, _ *mocks.MockInterface) {
				controller.DS = nil
			},
			wantDBStatus: "unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, mockDS, controller := setupTestEnvironment(t)
			tt.setup(controller, mockDS)

			settings := controller.Settings.Load()
			settings.Version = testVersion
			settings.BuildDate = testBuildDate

			req := httptest.NewRequest(http.MethodGet, apiV2Prefix+"/health", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath(apiV2Prefix + "/health")

			// Response time is deliberately not asserted: the system metrics come
			// from gopsutil syscalls whose latency depends on the runner, so a
			// wall-clock limit would measure the environment, not correctness.
			require.NoError(t, controller.HealthCheck(c))
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var resp healthResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

			assert.Equal(t, "healthy", resp.Status)
			assert.Equal(t, testVersion, resp.Version)
			assert.Equal(t, testBuildDate, resp.BuildDate)
			assert.Equal(t, "production", resp.Environment)
			assert.Equal(t, tt.wantDBStatus, resp.DatabaseStatus)
			_, err := time.Parse(time.RFC3339, resp.Timestamp)
			require.NoError(t, err, "timestamp should be RFC3339")

			require.NotNil(t, resp.System.CPUUsage, "system.cpu_usage should be present")
			assert.ElementsMatch(t, []string{"used_percent", "total_mb", "used_mb"}, slices.Collect(maps.Keys(resp.System.Memory)))
			assert.ElementsMatch(t, []string{"total_gb", "free_gb", "used_percent"}, slices.Collect(maps.Keys(resp.System.DiskSpace)))
		})
	}
}

// TestHandleError tests error handling functionality
func TestHandleError(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Create a request context
	req := httptest.NewRequest(http.MethodGet, apiV2Prefix+"/health", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Test error handling
	err := controller.HandleError(c, echo.NewHTTPError(http.StatusBadRequest, "Test error"),
		"Error message", http.StatusBadRequest)

	// Assertions
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Parse response body
	var response ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Check response content — in non-debug mode, Error field uses sanitized message
	// instead of raw err.Error() to prevent leaking internal details
	assert.Equal(t, "Error message", response.Error)
	assert.Equal(t, "Error message", response.Message)
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

// TestNewErrorResponseDebugMode verifies that raw err.Error() is exposed in the
// Error field when web server debug mode is enabled, for developer diagnostics.
func TestNewErrorResponseDebugMode(t *testing.T) {
	testErr := echo.NewHTTPError(http.StatusBadRequest, "Test error")

	// Test 1: Non-debug mode — Error field should use sanitized message
	c := &Controller{Core: &apicore.Core{}}
	c.Settings.Store(&conf.Settings{
		WebServer: conf.WebServerSettings{Debug: false},
	})

	resp := c.NewErrorResponse(testErr, "Safe message", http.StatusBadRequest)
	assert.Equal(t, "Safe message", resp.Error, "Non-debug mode should use sanitized message")
	assert.Equal(t, "Safe message", resp.Message)

	// Test 2: Debug mode — Error field should expose raw err.Error()
	c.Settings.Load().WebServer.Debug = true

	resp = c.NewErrorResponse(testErr, "Safe message", http.StatusBadRequest)
	assert.Equal(t, "code=400, message=Test error", resp.Error, "Debug mode should expose raw error")
	assert.Equal(t, "Safe message", resp.Message)
}
