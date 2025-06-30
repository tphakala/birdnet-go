// weather_testing.go provides HTTP handlers for weather provider testing functionality
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/weather"
)

// Weather test stage constants
const (
	stageWeatherAPIConnectivity = "API Connectivity"
	stageWeatherAuthentication  = "Authentication"
	stageWeatherDataFetch       = "Weather Data Fetch"
	stageWeatherDataParsing     = "Data Parsing"
)

// WeatherTestResult represents the result of a weather provider test
type WeatherTestResult struct {
	Success    bool   `json:"success"`
	Stage      string `json:"stage"`
	Message    string `json:"message"`
	Error      string `json:"error,omitempty"`
	IsProgress bool   `json:"isProgress,omitempty"`
	State      string `json:"state,omitempty"`
	Timestamp  string `json:"timestamp,omitempty"`
	ResultID   string `json:"resultId,omitempty"`
	Provider   string `json:"provider,omitempty"`
}

// TestWeather handles requests to test weather provider connectivity and functionality
// API: GET/POST /api/v1/weather/test
func (h *Handlers) TestWeather(c echo.Context) error {
	// Define a struct for the test configuration
	type TestConfig struct {
		Provider     string `json:"provider"`
		Debug        bool   `json:"debug"`
		PollInterval int    `json:"pollInterval"`
		OpenWeather  struct {
			APIKey   string `json:"apiKey"`
			Endpoint string `json:"endpoint"`
			Units    string `json:"units"`
			Language string `json:"language"`
		} `json:"openWeather"`
	}

	var testConfig TestConfig
	var settings *conf.Settings

	// If this is a POST request, use the provided test configuration
	if c.Request().Method == "POST" {
		if err := c.Bind(&testConfig); err != nil {
			return h.NewHandlerError(err, "Invalid test configuration", http.StatusBadRequest)
		}

		// Create temporary settings for the test
		settings = &conf.Settings{
			BirdNET: conf.BirdNETConfig{
				Latitude:  h.Settings.BirdNET.Latitude,  // Use actual configured latitude
				Longitude: h.Settings.BirdNET.Longitude, // Use actual configured longitude
			},
			Realtime: conf.RealtimeSettings{
				Weather: conf.WeatherSettings{
					Provider:     testConfig.Provider,
					Debug:        testConfig.Debug,
					PollInterval: testConfig.PollInterval,
					OpenWeather: conf.OpenWeatherSettings{
						APIKey:   testConfig.OpenWeather.APIKey,
						Endpoint: testConfig.OpenWeather.Endpoint,
						Units:    testConfig.OpenWeather.Units,
						Language: testConfig.OpenWeather.Language,
					},
				},
			},
		}
	} else {
		// For GET requests, use the current settings
		settings = h.Settings
	}

	// Check if a valid weather provider is selected
	if settings.Realtime.Weather.Provider == "" || settings.Realtime.Weather.Provider == "none" {
		return h.NewHandlerError(
			nil,
			"No weather provider selected",
			http.StatusBadRequest,
		)
	}

	// Validate provider-specific requirements
	if settings.Realtime.Weather.Provider == "openweather" && settings.Realtime.Weather.OpenWeather.APIKey == "" {
		return h.NewHandlerError(
			nil,
			"OpenWeather API key is not configured",
			http.StatusBadRequest,
		)
	}

	// Create context with timeout for the test
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Set up streaming response
	c.Response().Header().Set("Content-Type", "application/x-ndjson")
	c.Response().WriteHeader(http.StatusOK)

	// Create a channel to receive test results
	resultChan := make(chan WeatherTestResult)

	// Start test in a goroutine
	go func() {
		defer close(resultChan)
		h.testWeatherProvider(ctx, settings, resultChan)
	}()

	// Stream results to client
	enc := json.NewEncoder(c.Response())
	for result := range resultChan {
		// Process the result
		h.processWeatherTestResult(&result)

		// Send the processed result
		if err := enc.Encode(result); err != nil {
			// If we can't write to the response, client probably disconnected
			return nil
		}
		c.Response().Flush()
	}

	return nil
}

// testWeatherProvider performs a multi-stage test of the weather provider
func (h *Handlers) testWeatherProvider(ctx context.Context, settings *conf.Settings, resultChan chan<- WeatherTestResult) {
	// Helper function to send a result
	sendResult := func(result WeatherTestResult) {
		// Mark progress messages
		result.IsProgress = strings.Contains(strings.ToLower(result.Message), "running") ||
			strings.Contains(strings.ToLower(result.Message), "testing") ||
			strings.Contains(strings.ToLower(result.Message), "establishing") ||
			strings.Contains(strings.ToLower(result.Message), "initializing")

		// Set state based on result
		switch {
		case result.State != "":
			// Keep existing state if explicitly set
		case result.Error != "":
			result.State = "failed"
			result.Success = false
			result.IsProgress = false
		case result.IsProgress:
			result.State = "running"
		case result.Success:
			result.State = "completed"
		case strings.Contains(strings.ToLower(result.Error), "timeout") ||
			strings.Contains(strings.ToLower(result.Error), "deadline exceeded"):
			result.State = "timeout"
		default:
			result.State = "failed"
		}

		// Add timestamp
		result.Timestamp = time.Now().Format(time.RFC3339)

		// Add provider info
		result.Provider = settings.Realtime.Weather.Provider

		// Send result to channel
		select {
		case <-ctx.Done():
			return
		case resultChan <- result:
		}
	}

	// Start with the explicit "Starting Test" stage
	sendResult(WeatherTestResult{
		Success:    true,
		Stage:      "Starting Test",
		Message:    fmt.Sprintf("Initializing %s Weather Provider Test...", getProviderDisplayName(settings.Realtime.Weather.Provider)),
		State:      "running",
		IsProgress: true,
	})

	// Validate that we can create a weather service (but we'll use the provider directly for testing)
	_, err := weather.NewService(settings, h.DS)
	if err != nil {
		sendResult(WeatherTestResult{
			Success: false,
			Stage:   "Starting Test",
			Message: "Failed to initialize weather service",
			Error:   err.Error(),
			State:   "failed",
		})
		return
	}

	// Mark "Starting Test" as completed
	sendResult(WeatherTestResult{
		Success:    true,
		Stage:      "Starting Test",
		Message:    "Initialization complete, starting tests",
		State:      "completed",
		IsProgress: false,
	})

	// Stage 1: API Connectivity (basic connectivity test)
	sendResult(WeatherTestResult{
		Success:    true,
		Stage:      stageWeatherAPIConnectivity,
		Message:    fmt.Sprintf("Testing connectivity to %s API...", getProviderDisplayName(settings.Realtime.Weather.Provider)),
		State:      "running",
		IsProgress: true,
	})

	// For API connectivity, we'll do a simple check based on provider
	connectivityResult := h.testWeatherAPIConnectivity(ctx, settings)
	sendResult(connectivityResult)
	if !connectivityResult.Success {
		return
	}

	// Stage 2: Authentication (if applicable)
	if settings.Realtime.Weather.Provider == "openweather" {
		sendResult(WeatherTestResult{
			Success:    true,
			Stage:      stageWeatherAuthentication,
			Message:    "Verifying API key authentication...",
			State:      "running",
			IsProgress: true,
		})

		authResult := h.testWeatherAuthentication(ctx, settings)
		sendResult(authResult)
		if !authResult.Success {
			return
		}
	}

	// Stage 3: Weather Data Fetch
	sendResult(WeatherTestResult{
		Success:    true,
		Stage:      stageWeatherDataFetch,
		Message:    "Fetching current weather data...",
		State:      "running",
		IsProgress: true,
	})

	// Actually fetch weather data using the provider
	var provider weather.Provider
	switch settings.Realtime.Weather.Provider {
	case "yrno":
		provider = weather.NewYrNoProvider()
	case "openweather":
		provider = weather.NewOpenWeatherProvider()
	default:
		sendResult(WeatherTestResult{
			Success: false,
			Stage:   stageWeatherDataFetch,
			Message: "Unknown weather provider",
			Error:   fmt.Sprintf("Provider '%s' is not supported", settings.Realtime.Weather.Provider),
			State:   "failed",
		})
		return
	}

	weatherData, err := provider.FetchWeather(settings)
	if err != nil {
		sendResult(WeatherTestResult{
			Success: false,
			Stage:   stageWeatherDataFetch,
			Message: "Failed to fetch weather data",
			Error:   err.Error(),
			State:   "failed",
		})
		return
	}

	if weatherData == nil {
		sendResult(WeatherTestResult{
			Success: false,
			Stage:   stageWeatherDataFetch,
			Message: "Failed to fetch weather data",
			Error:   "No data returned from weather provider",
			State:   "failed",
		})
		return
	}

	// Success message with weather data summary
	fetchMessage := fmt.Sprintf("Successfully fetched weather data for %s, %s. Temperature: %.1f°, Conditions: %s",
		weatherData.Location.City,
		weatherData.Location.Country,
		weatherData.Temperature.Current,
		weatherData.Description)

	sendResult(WeatherTestResult{
		Success: true,
		Stage:   stageWeatherDataFetch,
		Message: fetchMessage,
		State:   "completed",
	})

	// Stage 4: Data Parsing and Validation
	sendResult(WeatherTestResult{
		Success:    true,
		Stage:      stageWeatherDataParsing,
		Message:    "Validating weather data structure...",
		State:      "running",
		IsProgress: true,
	})

	// Validate the weather data
	validationResult := h.validateWeatherData(weatherData)
	sendResult(validationResult)
}

// testWeatherAPIConnectivity tests basic connectivity to the weather API
func (h *Handlers) testWeatherAPIConnectivity(ctx context.Context, settings *conf.Settings) WeatherTestResult {
	var testURL string
	provider := settings.Realtime.Weather.Provider

	switch provider {
	case "yrno":
		testURL = "https://api.met.no/weatherapi/locationforecast/2.0/status"
	case "openweather":
		// For OpenWeather, we'll test the base domain
		testURL = "https://api.openweathermap.org"
	default:
		return WeatherTestResult{
			Success: false,
			Stage:   stageWeatherAPIConnectivity,
			Message: "Unknown weather provider",
			Error:   fmt.Sprintf("Provider '%s' is not supported", provider),
			State:   "failed",
		}
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", testURL, http.NoBody)
	if err != nil {
		return WeatherTestResult{
			Success: false,
			Stage:   stageWeatherAPIConnectivity,
			Message: "Failed to create test request",
			Error:   err.Error(),
			State:   "failed",
		}
	}

	req.Header.Set("User-Agent", "BirdNET-Go Weather Test")

	resp, err := client.Do(req)
	if err != nil {
		return WeatherTestResult{
			Success: false,
			Stage:   stageWeatherAPIConnectivity,
			Message: fmt.Sprintf("Failed to connect to %s API", getProviderDisplayName(provider)),
			Error:   err.Error(),
			State:   "failed",
		}
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("warning: failed to close response body: %v", err)
		}
	}()

	// For connectivity test, we just check if we got a response
	log.Printf("✅ Successfully connected to %s API (status: %d)", getProviderDisplayName(provider), resp.StatusCode)

	return WeatherTestResult{
		Success: true,
		Stage:   stageWeatherAPIConnectivity,
		Message: fmt.Sprintf("Successfully connected to %s API", getProviderDisplayName(provider)),
		State:   "completed",
	}
}

// testWeatherAuthentication tests authentication with the weather API (for providers that require it)
func (h *Handlers) testWeatherAuthentication(ctx context.Context, settings *conf.Settings) WeatherTestResult {
	// This is specific to OpenWeather which requires an API key
	apiKey := settings.Realtime.Weather.OpenWeather.APIKey
	testURL := fmt.Sprintf("%s?lat=0&lon=0&appid=%s",
		settings.Realtime.Weather.OpenWeather.Endpoint,
		apiKey)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", testURL, http.NoBody)
	if err != nil {
		return WeatherTestResult{
			Success: false,
			Stage:   stageWeatherAuthentication,
			Message: "Failed to create authentication test request",
			Error:   err.Error(),
			State:   "failed",
		}
	}

	req.Header.Set("User-Agent", "BirdNET-Go Weather Test")

	resp, err := client.Do(req)
	if err != nil {
		return WeatherTestResult{
			Success: false,
			Stage:   stageWeatherAuthentication,
			Message: "Failed to authenticate with OpenWeather API",
			Error:   err.Error(),
			State:   "failed",
		}
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("warning: failed to close response body: %v", err)
		}
	}()

	switch resp.StatusCode {
	case 401:
		log.Printf("❌ OpenWeather authentication failed: invalid API key")
		return WeatherTestResult{
			Success: false,
			Stage:   stageWeatherAuthentication,
			Message: "Authentication failed: Invalid API key",
			State:   "failed",
		}
	case 200:
		log.Printf("✅ Successfully authenticated with OpenWeather API")
		return WeatherTestResult{
			Success: true,
			Stage:   stageWeatherAuthentication,
			Message: "Successfully authenticated with OpenWeather API",
			State:   "completed",
		}
	default:
		log.Printf("❌ OpenWeather authentication failed: unexpected status code %d", resp.StatusCode)
		return WeatherTestResult{
			Success: false,
			Stage:   stageWeatherAuthentication,
			Message: fmt.Sprintf("Authentication failed: Unexpected response (status %d)", resp.StatusCode),
			State:   "failed",
		}
	}
}

// validateWeatherData validates the structure and content of weather data
func (h *Handlers) validateWeatherData(data *weather.WeatherData) WeatherTestResult {
	var issues []string

	// Check temperature validity
	if data.Temperature.Current < -273.15 {
		issues = append(issues, "Invalid temperature (below absolute zero)")
	}

	// Check required location data
	if data.Location.Latitude == 0 && data.Location.Longitude == 0 {
		issues = append(issues, "Missing location coordinates")
	}

	// Check for basic weather information
	if data.Description == "" {
		issues = append(issues, "Missing weather description")
	}

	if len(issues) > 0 {
		return WeatherTestResult{
			Success: false,
			Stage:   stageWeatherDataParsing,
			Message: "Weather data validation failed",
			Error:   strings.Join(issues, "; "),
			State:   "failed",
		}
	}

	// Build a summary of the validated data
	summary := fmt.Sprintf("Data validation successful. Location: %.3f, %.3f | Temp: %.1f° | Humidity: %d%% | Wind: %.1f m/s | Pressure: %d hPa",
		data.Location.Latitude,
		data.Location.Longitude,
		data.Temperature.Current,
		data.Humidity,
		data.Wind.Speed,
		data.Pressure)

	return WeatherTestResult{
		Success: true,
		Stage:   stageWeatherDataParsing,
		Message: summary,
		State:   "completed",
	}
}

// processWeatherTestResult processes a weather test result and adds useful information
func (h *Handlers) processWeatherTestResult(result *WeatherTestResult) {
	// Log the result with emoji
	emoji := "❌"
	if result.Success {
		emoji = "✅"
	}

	// Format the log message
	logMsg := result.Message
	if !result.Success && result.Error != "" {
		logMsg = fmt.Sprintf("%s: %s", result.Message, result.Error)
	}
	log.Printf("%s %s: %s", emoji, result.Stage, logMsg)

	// Add troubleshooting hints for failures
	if !result.Success && result.Error != "" {
		hint := generateWeatherTroubleshootingHint(result)
		if hint != "" {
			result.Message = fmt.Sprintf("%s %s %s", result.Message, result.Error, hint)
		}
		result.Error = "" // Clear the error field as we've incorporated it into the message
	}
}

// generateWeatherTroubleshootingHint provides context-specific troubleshooting suggestions
func generateWeatherTroubleshootingHint(result *WeatherTestResult) string {
	if result.Success {
		return ""
	}

	switch result.Stage {
	case stageWeatherAPIConnectivity:
		if strings.Contains(result.Error, "timeout") {
			return "Check your internet connection and ensure the weather service is accessible from your network."
		}
		if strings.Contains(result.Error, "no such host") || strings.Contains(result.Error, "DNS") {
			return "Could not resolve the weather service hostname. Check your DNS configuration and internet connectivity."
		}
		return "Unable to connect to the weather API. Verify your internet connection and network settings."

	case stageWeatherAuthentication:
		if result.Provider == "openweather" {
			return "Please verify that your OpenWeather API key is correct and active. You can check your API key status at https://home.openweathermap.org/api_keys"
		}
		return "Authentication failed. Check your API credentials."

	case stageWeatherDataFetch:
		if strings.Contains(result.Error, "rate limit") {
			return "API rate limit exceeded. Please wait before trying again or check your API plan limits."
		}
		return "Failed to fetch weather data. The service might be temporarily unavailable or your location settings might be incorrect."

	case stageWeatherDataParsing:
		return "The weather service returned invalid or incomplete data. This might be a temporary issue with the service."

	default:
		return "Something went wrong with the weather test. Check your connection settings and try again."
	}
}

// getProviderDisplayName returns a user-friendly name for the weather provider
func getProviderDisplayName(provider string) string {
	switch provider {
	case "yrno":
		return "Yr.no"
	case "openweather":
		return "OpenWeather"
	default:
		return provider
	}
}
