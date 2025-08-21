// internal/api/v2/integrations.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/weather"
)

// MQTTStatus represents the current status of the MQTT connection
type MQTTStatus struct {
	Connected bool   `json:"connected"`            // Whether the MQTT client is currently connected to the broker
	Broker    string `json:"broker"`               // The URI of the MQTT broker (e.g., tcp://mqtt.example.com:1883)
	Topic     string `json:"topic"`                // The topic pattern used for publishing/subscribing to MQTT messages
	ClientID  string `json:"client_id"`            // The unique identifier used by this client when connecting to the broker
	LastError string `json:"last_error,omitempty"` // Most recent error message, if any connection issues occurred
}

// MQTTTestResult represents the result of an MQTT connection test
type MQTTTestResult struct {
	Success     bool   `json:"success"`                   // Whether the connection test was successful
	Message     string `json:"message"`                   // Human-readable description of the test result
	ElapsedTime int64  `json:"elapsed_time_ms,omitempty"` // Time taken to complete the test in milliseconds
}

// BirdWeatherStatus represents the current status of the BirdWeather integration
type BirdWeatherStatus struct {
	Enabled          bool    `json:"enabled"`              // Whether BirdWeather integration is enabled
	StationID        string  `json:"station_id"`           // The BirdWeather station ID
	Threshold        float64 `json:"threshold"`            // The confidence threshold for reporting detections
	LocationAccuracy float64 `json:"location_accuracy"`    // The location accuracy in meters
	LastError        string  `json:"last_error,omitempty"` // Most recent error message, if any issues occurred
}

// initIntegrationsRoutes registers all integration-related API endpoints
func (c *Controller) initIntegrationsRoutes() {
	if c.apiLogger != nil {
		c.apiLogger.Info("Initializing integrations routes")
	}

	// Create integrations API group with auth middleware
	integrationsGroup := c.Group.Group("/integrations", c.AuthMiddleware)

	// MQTT routes
	mqttGroup := integrationsGroup.Group("/mqtt")
	mqttGroup.GET("/status", c.GetMQTTStatus)
	mqttGroup.POST("/test", c.TestMQTTConnection)

	// BirdWeather routes
	bwGroup := integrationsGroup.Group("/birdweather")
	bwGroup.GET("/status", c.GetBirdWeatherStatus)
	bwGroup.POST("/test", c.TestBirdWeatherConnection)

	// Weather routes
	weatherGroup := integrationsGroup.Group("/weather")
	weatherGroup.POST("/test", c.TestWeatherConnection)

	// Other integration routes could be added here:
	// - External media storage

	if c.apiLogger != nil {
		c.apiLogger.Info("Integrations routes initialized successfully")
	}
}

// GetMQTTStatus handles GET /api/v2/integrations/mqtt/status
func (c *Controller) GetMQTTStatus(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting MQTT status", "path", path, "ip", ip)
	}

	// Get MQTT configuration from settings
	mqttConfig := c.Settings.Realtime.MQTT

	// Prepare status response
	status := MQTTStatus{
		Connected: false, // Default to not connected
		Broker:    mqttConfig.Broker,
		Topic:     mqttConfig.Topic,
		ClientID:  c.Settings.Main.Name, // Use the application name as client ID
	}

	// If MQTT is not enabled, return status as-is
	if !mqttConfig.Enabled {
		if c.apiLogger != nil {
			c.apiLogger.Info("MQTT is disabled, returning status", "path", path, "ip", ip)
		}
		return ctx.JSON(http.StatusOK, status)
	}

	// Check connection status using a temporary client
	if c.apiLogger != nil {
		c.apiLogger.Debug("Checking MQTT connection status", "path", path, "ip", ip)
	}
	connected, checkErr := c.checkMQTTConnectionStatus(ctx.Request().Context())
	status.Connected = connected
	if checkErr != "" {
		status.LastError = checkErr
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved MQTT status successfully",
			"connected", status.Connected,
			"broker", status.Broker,
			"last_error", status.LastError,
			"path", path,
			"ip", ip,
		)
	}
	return ctx.JSON(http.StatusOK, status)
}

// checkMQTTConnectionStatus attempts to connect to the MQTT broker using a temporary client
// to determine the current connection status.
// Returns true if connected, false otherwise, along with any error message encountered.
func (c *Controller) checkMQTTConnectionStatus(parentCtx context.Context) (connected bool, lastError string) {
	// Use the injected metrics instance
	if c.metrics == nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Metrics instance not available for MQTT status check")
		}
		return false, "error:metrics:not_initialized"
	}

	tempClient, err := mqtt.NewClient(c.Settings, c.metrics)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to create temporary MQTT client for status check", "error", err)
		}
		return false, fmt.Sprintf("error:client:mqtt_client_creation:%s", err.Error())
	}
	defer tempClient.Disconnect() // Ensure temporary client is disconnected

	// Use a short timeout for the connection attempt
	connectCtx, cancel := context.WithTimeout(parentCtx, 3*time.Second)
	defer cancel()

	// Try to connect
	err = tempClient.Connect(connectCtx)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Temporary MQTT client connection failed during status check", "error", err, "broker", c.Settings.Realtime.MQTT.Broker)
		}
		return false, fmt.Sprintf("error:connection:mqtt_broker:%s", err.Error())
	}

	// Check if genuinely connected
	if !tempClient.IsConnected() {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Temporary MQTT client connected but IsConnected() returned false", "broker", c.Settings.Realtime.MQTT.Broker)
		}
		// Consider this a failure for status purposes, though connection might be flapping
		return false, "error:connection:mqtt_connection_unstable"
	}

	if c.apiLogger != nil {
		c.apiLogger.Debug("Temporary MQTT client connected successfully for status check", "broker", c.Settings.Realtime.MQTT.Broker)
	}
	return true, "" // Connected successfully
}

// GetBirdWeatherStatus handles GET /api/v2/integrations/birdweather/status
func (c *Controller) GetBirdWeatherStatus(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting BirdWeather status", "path", path, "ip", ip)
	}

	// Get BirdWeather configuration from settings
	bwConfig := c.Settings.Realtime.Birdweather

	// Prepare status response
	status := BirdWeatherStatus{
		Enabled:          bwConfig.Enabled,
		StationID:        bwConfig.ID,
		Threshold:        bwConfig.Threshold,
		LocationAccuracy: bwConfig.LocationAccuracy,
	}

	// For now, we just return the configuration status
	// In the future, we could add checks for client status here
	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved BirdWeather status successfully",
			"enabled", status.Enabled,
			"station_id", status.StationID,
			"threshold", status.Threshold,
			"path", path,
			"ip", ip,
		)
	}

	return ctx.JSON(http.StatusOK, status)
}

// TestMQTTConnection handles POST /api/v2/integrations/mqtt/test
func (c *Controller) TestMQTTConnection(ctx echo.Context) error {
	// Get MQTT configuration from settings
	mqttConfig := c.Settings.Realtime.MQTT

	if !mqttConfig.Enabled {
		return ctx.JSON(http.StatusOK, MQTTTestResult{
			Success: false,
			Message: "MQTT is not enabled in settings",
		})
	}

	// Validate MQTT configuration
	if mqttConfig.Broker == "" {
		return ctx.JSON(http.StatusBadRequest, MQTTTestResult{
			Success: false,
			Message: "MQTT broker not configured",
		})
	}

	// Use the injected metrics instance
	if c.metrics == nil {
		return ctx.JSON(http.StatusInternalServerError, MQTTTestResult{
			Success: false,
			Message: "Metrics instance not available for MQTT test",
		})
	}

	// Create test MQTT client with the current configuration
	client, err := mqtt.NewClient(c.Settings, c.metrics)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, MQTTTestResult{
			Success: false,
			Message: fmt.Sprintf("Failed to create MQTT client: %v", err),
		})
	}

	// Prepare for testing
	ctx.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	ctx.Response().WriteHeader(http.StatusOK)

	// Channel for test results
	resultChan := make(chan mqtt.TestResult)

	// Create a done channel to signal when the client disconnects
	doneChan := make(chan struct{})

	// Use sync.Once to ensure doneChan is closed exactly once
	var closeOnce sync.Once
	// Helper function to safely close the doneChan
	safeDoneClose := func() {
		closeOnce.Do(func() {
			close(doneChan)
		})
	}

	// Mutex for safe writing to response
	var writeMu sync.Mutex

	// Create context with timeout that also gets cancelled if HTTP client disconnects
	httpCtx := ctx.Request().Context()
	testCtx, cancel := context.WithTimeout(httpCtx, 20*time.Second)
	defer cancel()

	// Run the test in a goroutine
	go func() {
		defer close(resultChan)
		startTime := time.Now()

		// Start the test
		client.TestConnection(testCtx, resultChan)

		// Calculate elapsed time
		elapsedTime := time.Since(startTime).Milliseconds()

		// Disconnect client when done
		client.Disconnect()

		// Send final result with elapsed time if the client is still connected
		select {
		case <-doneChan:
			// HTTP client has disconnected, no need to send final result
			c.Debug("HTTP client disconnected, skipping final result")
		case <-testCtx.Done():
			// Test timed out or was cancelled
			c.Debug("Test context cancelled: %v", testCtx.Err())
		default:
			// Still connected, send final result
			writeMu.Lock()
			defer writeMu.Unlock()

			// Format final response
			finalResult := map[string]interface{}{
				"elapsed_time_ms": elapsedTime,
				"state":           "completed",
			}

			// Write final result to response if possible
			if err := c.writeJSONResponse(ctx, finalResult); err != nil {
				c.logger.Printf("Error writing final MQTT test result: %v", err)
			}
		}
	}()

	// Feed streaming results to client
	encoder := json.NewEncoder(ctx.Response())

	// Stream results to client until done
	for result := range resultChan {
		writeMu.Lock()
		if err := encoder.Encode(result); err != nil {
			c.logger.Printf("Error encoding MQTT test result: %v", err)
			writeMu.Unlock()

			// Signal that the HTTP client has disconnected using sync.Once
			safeDoneClose()

			// Cancel the test context to stop ongoing tests
			cancel()
			return nil
		}
		ctx.Response().Flush()
		writeMu.Unlock()

		// Check if HTTP context is done (client disconnected)
		select {
		case <-httpCtx.Done():
			c.Debug("HTTP client disconnected during test")
			// Use sync.Once to safely close the channel
			safeDoneClose()
			cancel() // Cancel the test context
			return nil
		default:
			// Continue processing
		}
	}

	return nil
}

// TestBirdWeatherConnection handles POST /api/v2/integrations/birdweather/test
func (c *Controller) TestBirdWeatherConnection(ctx echo.Context) error {
	var request BirdWeatherTestRequest
	if err := ctx.Bind(&request); err != nil {
		return c.HandleError(ctx, err, "Invalid BirdWeather test request", http.StatusBadRequest)
	}

	// Validate BirdWeather configuration from the request
	if !request.Enabled {
		return ctx.JSON(http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "BirdWeather integration is not enabled",
			"state":   "failed",
		})
	}

	// Validate BirdWeather configuration
	if request.ID == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"message": "BirdWeather station ID not configured",
			"state":   "failed",
		})
	}

	// Create temporary settings for the test
	testSettings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Birdweather: conf.BirdweatherSettings{
				Enabled:          request.Enabled,
				ID:               request.ID,
				Threshold:        request.Threshold,
				LocationAccuracy: request.LocationAccuracy,
				Debug:            request.Debug,
			},
		},
	}
	// Copy main settings
	testSettings.Main = c.Settings.Main

	// Create test BirdWeather client with the test configuration
	client, err := birdweather.New(testSettings)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Failed to create BirdWeather client: %v", err),
			"state":   "failed",
		})
	}

	// Prepare for testing
	ctx.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	ctx.Response().WriteHeader(http.StatusOK)

	// Channel for test results
	resultChan := make(chan birdweather.TestResult)

	// Create a done channel to signal when the client disconnects
	doneChan := make(chan struct{})

	// Use sync.Once to ensure doneChan is closed exactly once
	var closeOnce sync.Once
	// Helper function to safely close the doneChan
	safeDoneClose := func() {
		closeOnce.Do(func() {
			close(doneChan)
		})
	}

	// Mutex for safe writing to response
	var writeMu sync.Mutex

	// Create context with timeout that also gets cancelled if HTTP client disconnects
	httpCtx := ctx.Request().Context()
	testCtx, cancel := context.WithTimeout(httpCtx, 30*time.Second)
	defer cancel()

	// Run the test in a goroutine
	go func() {
		defer close(resultChan)
		startTime := time.Now()

		// Start the test
		client.TestConnection(testCtx, resultChan)

		// Calculate elapsed time
		elapsedTime := time.Since(startTime).Milliseconds()

		// Clean up client resources
		client.Close()

		// Send final result with elapsed time if the client is still connected
		select {
		case <-doneChan:
			// HTTP client has disconnected, no need to send final result
			c.Debug("HTTP client disconnected, skipping final result")
		case <-testCtx.Done():
			// Test timed out or was cancelled
			c.Debug("Test context cancelled: %v", testCtx.Err())
		default:
			// Still connected, send final result
			writeMu.Lock()
			defer writeMu.Unlock()

			// Format final response
			finalResult := map[string]interface{}{
				"elapsed_time_ms": elapsedTime,
				"state":           "completed",
			}

			// Write final result to response if possible
			if err := c.writeJSONResponse(ctx, finalResult); err != nil {
				c.logger.Printf("Error writing final BirdWeather test result: %v", err)
			}
		}
	}()

	// Feed streaming results to client
	encoder := json.NewEncoder(ctx.Response())

	// Stream results to client until done
	for result := range resultChan {
		writeMu.Lock()
		if err := encoder.Encode(result); err != nil {
			c.logger.Printf("Error encoding BirdWeather test result: %v", err)
			writeMu.Unlock()

			// Signal that the HTTP client has disconnected using sync.Once
			safeDoneClose()

			// Cancel the test context to stop ongoing tests
			cancel()
			return nil
		}
		ctx.Response().Flush()
		writeMu.Unlock()

		// Check if HTTP context is done (client disconnected)
		select {
		case <-httpCtx.Done():
			c.Debug("HTTP client disconnected during test")
			// Use sync.Once to safely close the channel
			safeDoneClose()
			cancel() // Cancel the test context
			return nil
		default:
			// Continue processing
		}
	}

	return nil
}

// BirdWeatherTestRequest represents a request to test BirdWeather connectivity
type BirdWeatherTestRequest struct {
	Enabled          bool    `json:"enabled"`
	ID               string  `json:"id"`
	Threshold        float64 `json:"threshold"`
	LocationAccuracy float64 `json:"locationAccuracy"`
	Debug            bool    `json:"debug"`
}

// WeatherTestRequest represents a request to test weather provider connectivity
type WeatherTestRequest struct {
	Provider     string                    `json:"provider"`
	PollInterval int                       `json:"pollInterval"`
	Debug        bool                      `json:"debug"`
	OpenWeather  conf.OpenWeatherSettings  `json:"openWeather"`
	Wunderground conf.WundergroundSettings `json:"wunderground"`
}

// WeatherTestStage represents the result of a weather test stage
type WeatherTestStage struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"` // pending, in_progress, completed, error
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// TestWeatherConnection handles POST /api/v2/integrations/weather/test
func (c *Controller) TestWeatherConnection(ctx echo.Context) error {
	var request WeatherTestRequest
	if err := ctx.Bind(&request); err != nil {
		return c.HandleError(ctx, err, "Invalid weather test request", http.StatusBadRequest)
	}

	// Validate provider
	if request.Provider == "" || request.Provider == "none" {
		return c.HandleError(ctx, nil, "No weather provider selected", http.StatusBadRequest)
	}

	// Validate OpenWeather specific requirements
	if request.Provider == "openweather" && request.OpenWeather.APIKey == "" {
		return c.HandleError(ctx, nil, "OpenWeather API key is required", http.StatusBadRequest)
	}

	// Set up streaming response
	ctx.Response().Header().Set("Content-Type", "application/x-ndjson")
	ctx.Response().Header().Set("Cache-Control", "no-cache")
	ctx.Response().Header().Set("Connection", "keep-alive")
	ctx.Response().WriteHeader(http.StatusOK)

	// Create test settings from request
	testSettings := &conf.Settings{
		BirdNET: conf.BirdNETConfig{
			Latitude:  c.Settings.BirdNET.Latitude,
			Longitude: c.Settings.BirdNET.Longitude,
		},
		Realtime: conf.RealtimeSettings{
			Weather: conf.WeatherSettings{
				Provider:     request.Provider,
				Debug:        request.Debug,
				PollInterval: request.PollInterval,
				OpenWeather:  request.OpenWeather,
				Wunderground: request.Wunderground,
			},
		},
	}

	// Create test context with timeout
	testCtx, cancel := context.WithTimeout(ctx.Request().Context(), 30*time.Second)
	defer cancel()

	// Create encoder for streaming results
	encoder := json.NewEncoder(ctx.Response())

	// Helper function to send stage results
	sendStage := func(stage WeatherTestStage) error {
		if err := encoder.Encode(stage); err != nil {
			return err
		}
		ctx.Response().Flush()
		return nil
	}

	// Run weather test stages
	stages := []struct {
		id    string
		title string
		test  func() (string, error)
	}{
		{"starting", "Starting Test", func() (string, error) {
			return fmt.Sprintf("Initializing %s weather provider test...", getProviderDisplayName(request.Provider)), nil
		}},
		{"connectivity", "API Connectivity", func() (string, error) {
			return c.testWeatherAPIConnectivity(testCtx, testSettings)
		}},
		{"authentication", "Authentication", func() (string, error) {
			return c.testWeatherAuthentication(testCtx, testSettings)
		}},
		{"fetch", "Weather Data Fetch", func() (string, error) {
			return c.testWeatherDataFetch(testCtx, testSettings)
		}},
		{"parse", "Data Parsing", func() (string, error) {
			return "Weather data validated successfully", nil
		}},
	}

	// Execute each stage
	for _, stage := range stages {
		// Send in-progress status
		if err := sendStage(WeatherTestStage{
			ID:     stage.id,
			Title:  stage.title,
			Status: "in_progress",
		}); err != nil {
			return nil // Client disconnected
		}

		// Run the test
		message, err := stage.test()
		if err != nil {
			// Send error status
			return sendStage(WeatherTestStage{
				ID:      stage.id,
				Title:   stage.title,
				Status:  "error",
				Message: message,
				Error:   err.Error(),
			})
		}

		// Send completed status
		if err := sendStage(WeatherTestStage{
			ID:      stage.id,
			Title:   stage.title,
			Status:  "completed",
			Message: message,
		}); err != nil {
			return nil // Client disconnected
		}

		// Small delay between stages for UX
		time.Sleep(200 * time.Millisecond)
	}

	return nil
}

// testWeatherAPIConnectivity tests basic connectivity to the weather API
func (c *Controller) testWeatherAPIConnectivity(ctx context.Context, settings *conf.Settings) (string, error) {
	var testURL string
	provider := settings.Realtime.Weather.Provider

	switch provider {
	case "yrno":
		testURL = "https://api.met.no/weatherapi/locationforecast/2.0/status"
	case "openweather":
		testURL = "https://api.openweathermap.org"
	case "wunderground":
		testURL = "https://api.weather.com"
	default:
		return "", fmt.Errorf("unsupported weather provider: %s", provider)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", testURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "BirdNET-Go Weather Test")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to connect to %s API: %w", getProviderDisplayName(provider), err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.logger.Printf("warning: failed to close response body: %v", err)
		}
	}()

	return fmt.Sprintf("Successfully connected to %s API", getProviderDisplayName(provider)), nil
}

// testWeatherAuthentication tests authentication with the weather API
func (c *Controller) testWeatherAuthentication(ctx context.Context, settings *conf.Settings) (string, error) {
	provider := settings.Realtime.Weather.Provider
	
	switch provider {
	case "openweather":
		apiKey := settings.Realtime.Weather.OpenWeather.APIKey
		endpoint := settings.Realtime.Weather.OpenWeather.Endpoint
		if endpoint == "" {
			endpoint = "https://api.openweathermap.org/data/2.5/weather"
		}

		testURL := fmt.Sprintf("%s?lat=0&lon=0&appid=%s", endpoint, apiKey)

		client := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequestWithContext(ctx, "GET", testURL, http.NoBody)
		if err != nil {
			return "", fmt.Errorf("failed to create authentication request: %w", err)
		}

		req.Header.Set("User-Agent", "BirdNET-Go Weather Test")
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("failed to authenticate with OpenWeather API: %w", err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				c.logger.Printf("warning: failed to close response body: %v", err)
			}
		}()

		if resp.StatusCode == 401 {
			return "", fmt.Errorf("invalid API key - please check your OpenWeather API key")
		}

		return "Successfully authenticated with OpenWeather API", nil
		
	case "wunderground":
		// For Weather Underground, authentication is tested in the data fetch stage
		// since there's no separate auth endpoint
		return "Authentication will be verified during data fetch", nil
		
	default:
		return "Authentication not required for this provider", nil
	}
}

// testWeatherDataFetch tests fetching actual weather data
func (c *Controller) testWeatherDataFetch(ctx context.Context, settings *conf.Settings) (string, error) {
	var provider weather.Provider
	switch settings.Realtime.Weather.Provider {
	case "yrno":
		provider = weather.NewYrNoProvider()
	case "openweather":
		provider = weather.NewOpenWeatherProvider()
	case "wunderground":
		provider = weather.NewWundergroundProvider(nil)
	default:
		return "", fmt.Errorf("unsupported weather provider: %s", settings.Realtime.Weather.Provider)
	}

	weatherData, err := provider.FetchWeather(settings)
	if err != nil {
		// Extract the actual error message instead of wrapping it
		// The provider already returns detailed error messages
		return "", err
	}

	if weatherData == nil {
		return "", fmt.Errorf("no weather data returned from provider")
	}

	// Return weather data summary
	return fmt.Sprintf("Successfully fetched weather data for %s, %s. Temperature: %.1f°, Conditions: %s",
		weatherData.Location.City,
		weatherData.Location.Country,
		weatherData.Temperature.Current,
		weatherData.Description), nil
}

// getProviderDisplayName returns a user-friendly name for the weather provider
func getProviderDisplayName(provider string) string {
	switch provider {
	case "yrno":
		return "Yr.no"
	case "openweather":
		return "OpenWeather"
	case "wunderground":
		return "Weather Underground"
	default:
		// Simple capitalization for unknown providers
		if provider != "" {
			return strings.ToUpper(provider[:1]) + provider[1:]
		}
		return provider
	}
}

// writeJSONResponse writes a JSON response to the client
// NOTE: For most cases, consider using Echo's built-in ctx.JSON(httpStatus, data) instead
// This function is primarily useful for streaming or special encoding scenarios
func (c *Controller) writeJSONResponse(ctx echo.Context, data interface{}) error {
	encoder := json.NewEncoder(ctx.Response())
	return encoder.Encode(data)
}
