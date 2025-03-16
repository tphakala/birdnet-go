// api_test.go: Package api provides tests for API v2 endpoints.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

// TestHealthCheck tests the health check endpoint
func TestHealthCheck(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Create a request to the health check endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/v2/health", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/health")

	// Test
	if assert.NoError(t, controller.HealthCheck(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check response content
		assert.Equal(t, "healthy", response["status"])

		// Future extensions - these fields may be added later
		// If they exist, they should have the correct type
		if version, exists := response["version"]; exists {
			assert.IsType(t, "", version, "version should be a string")
		}

		if env, exists := response["environment"]; exists {
			assert.IsType(t, "", env, "environment should be a string")
		}

		if uptime, exists := response["uptime"]; exists {
			// Uptime could be represented as a number (seconds) or as a formatted string
			switch v := uptime.(type) {
			case float64:
				assert.GreaterOrEqual(t, v, float64(0), "uptime should be non-negative")
			case string:
				assert.NotEmpty(t, v, "uptime string should not be empty")
			default:
				assert.Fail(t, "uptime should be a number or string")
			}
		}

		// If additional system metrics are added
		if metrics, exists := response["metrics"]; exists {
			assert.IsType(t, map[string]interface{}{}, metrics, "metrics should be an object")
		}
	}
}

// TestHandleError tests error handling functionality
func TestHandleError(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Create a request context
	req := httptest.NewRequest(http.MethodGet, "/api/v2/health", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Test error handling
	err := controller.HandleError(c, echo.NewHTTPError(http.StatusBadRequest, "Test error"),
		"Error message", http.StatusBadRequest)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Parse response body
	var response ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Check response content
	assert.Equal(t, "code=400, message=Test error", response.Error)
	assert.Equal(t, "Error message", response.Message)
	assert.Equal(t, http.StatusBadRequest, response.Code)
}
