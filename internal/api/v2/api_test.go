// api_test.go: Package api provides tests for API v2 endpoints.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestHealthCheck tests the health check endpoint
func TestHealthCheck(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Setup mock expectations for database check
	mockDS.On("GetLastDetections", 1).Return([]datastore.Note{}, nil)

	// Add system metrics to the controller settings
	controller.Settings.Version = "1.2.3"
	controller.Settings.BuildDate = "2023-05-15"

	// Create a request to the health check endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/v2/health", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/health")

	// Record start time to measure response time
	startTime := time.Now()

	// Test
	if assert.NoError(t, controller.HealthCheck(c)) {
		// Calculate response time
		responseTime := time.Since(startTime)

		// Check response time is reasonable (under 100ms for a simple health check)
		assert.Less(t, responseTime.Milliseconds(), int64(100), "Health check should respond quickly")

		// Check response code
		assert.Equal(t, http.StatusOK, rec.Code)

		// Check content type header
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		// Parse response body
		var response map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check required fields
		assert.Equal(t, "healthy", response["status"], "Status should be 'healthy'")
		assert.Equal(t, "1.2.3", response["version"], "Version should match controller settings")
		assert.Equal(t, "2023-05-15", response["build_date"], "Build date should match controller settings")

		// Check database status if present
		if dbStatus, exists := response["database_status"]; exists {
			assert.Equal(t, "connected", dbStatus, "Database status should be 'connected'")
		}

		// Check system metrics if present
		if metrics, exists := response["system"]; exists {
			metricsMap, ok := metrics.(map[string]interface{})
			assert.True(t, ok, "System metrics should be an object")

			// Check for common system metrics
			if cpu, cpuExists := metricsMap["cpu_usage"]; cpuExists {
				cpuValue, ok := cpu.(float64)
				assert.True(t, ok, "CPU usage should be a number")
				assert.GreaterOrEqual(t, cpuValue, float64(0), "CPU usage should be non-negative")
				assert.LessOrEqual(t, cpuValue, float64(100), "CPU usage should be <= 100%")
			}

			if memory, memExists := metricsMap["memory_usage"]; memExists {
				memValue, ok := memory.(float64)
				assert.True(t, ok, "Memory usage should be a number")
				assert.GreaterOrEqual(t, memValue, float64(0), "Memory usage should be non-negative")
			}

			if diskSpace, diskExists := metricsMap["disk_space"]; diskExists {
				diskMap, ok := diskSpace.(map[string]interface{})
				assert.True(t, ok, "Disk space should be an object")

				if total, totalExists := diskMap["total"]; totalExists {
					assert.GreaterOrEqual(t, total.(float64), float64(0), "Total disk space should be non-negative")
				}

				if free, freeExists := diskMap["free"]; freeExists {
					assert.GreaterOrEqual(t, free.(float64), float64(0), "Free disk space should be non-negative")
				}
			}
		}

		// Check uptime if present
		if uptime, exists := response["uptime"]; exists {
			switch v := uptime.(type) {
			case float64:
				assert.GreaterOrEqual(t, v, float64(0), "Uptime should be non-negative")
			case string:
				assert.NotEmpty(t, v, "Uptime string should not be empty")
			default:
				assert.Fail(t, "Uptime should be a number or string")
			}
		}

		// Check for additional fields that might be useful
		if env, exists := response["environment"]; exists {
			assert.IsType(t, "", env, "Environment should be a string")
		}

		if timestamp, exists := response["timestamp"]; exists {
			_, err := time.Parse(time.RFC3339, timestamp.(string))
			assert.NoError(t, err, "Timestamp should be in RFC3339 format")
		}
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
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
