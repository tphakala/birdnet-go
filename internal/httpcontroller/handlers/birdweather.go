// birdweather.go provides HTTP handlers for BirdWeather-related functionality
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// BirdWeather test stage constants
const (
	stageAPIConnectivity  = "API Connectivity"
	stageAuthentication   = "Authentication"
	stageSoundscapeUpload = "Soundscape Upload"
	stageDetectionPost    = "Detection Post"
)

// TestBirdWeather handles requests to test BirdWeather connectivity and functionality
// API: GET/POST /api/v1/birdweather/test
func (h *Handlers) TestBirdWeather(c echo.Context) error {
	// Define a struct for the test configuration
	type TestConfig struct {
		Enabled          bool    `json:"enabled"`
		ID               string  `json:"id"`
		Threshold        float64 `json:"threshold"`
		LocationAccuracy float64 `json:"locationAccuracy"`
		Debug            bool    `json:"debug"`
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
				Latitude:  45.0, // Default test values for location
				Longitude: -75.0,
			},
			Realtime: conf.RealtimeSettings{
				Birdweather: conf.BirdweatherSettings{
					Enabled:          testConfig.Enabled,
					ID:               testConfig.ID,
					Threshold:        testConfig.Threshold,
					LocationAccuracy: testConfig.LocationAccuracy,
					Debug:            testConfig.Debug,
				},
			},
		}
	} else {
		// For GET requests, use the current settings
		settings = h.Settings
	}

	// Check if BirdWeather is enabled
	if !settings.Realtime.Birdweather.Enabled {
		return h.NewHandlerError(
			nil,
			"BirdWeather is not enabled in settings",
			http.StatusBadRequest,
		)
	}

	// Check if the station ID is provided
	if settings.Realtime.Birdweather.ID == "" {
		return h.NewHandlerError(
			nil,
			"BirdWeather station ID is not configured",
			http.StatusBadRequest,
		)
	}

	// Create a temporary BirdWeather client for testing
	bwClient, err := birdweather.New(settings)
	if err != nil {
		return h.NewHandlerError(err, "Failed to create BirdWeather client", http.StatusInternalServerError)
	}

	// Create context with timeout for the test
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Set up streaming response
	c.Response().Header().Set("Content-Type", "application/x-ndjson")
	c.Response().WriteHeader(http.StatusOK)

	// Create a channel to receive test results
	resultChan := make(chan birdweather.TestResult)

	// Start test in a goroutine
	go func() {
		defer close(resultChan)
		bwClient.TestConnection(ctx, resultChan)
	}()

	// Stream results to client
	enc := json.NewEncoder(c.Response())
	for result := range resultChan {
		// Process the result
		h.processBirdWeatherTestResult(&result)

		// Send the processed result
		if err := enc.Encode(result); err != nil {
			// If we can't write to the response, client probably disconnected
			return nil
		}
		c.Response().Flush()
	}

	// Clean up
	bwClient.Close()

	return nil
}

// processBirdWeatherTestResult processes a BirdWeather test result and adds useful information
func (h *Handlers) processBirdWeatherTestResult(result *birdweather.TestResult) {
	// Modify the result enhancement to handle progress messages
	if !result.Success {
		// Log the error with the full details for debugging
		if result.Error != "" {
			log.Printf("BirdWeather test error in stage %s: %s", result.Stage, result.Error)

			// Check for rate limit error with expiry timestamp
			if result.Stage == "Starting Test" && strings.Contains(result.Error, "rate limit exceeded") {
				// Try to parse expiry timestamp
				parts := strings.Split(result.Error, "|")
				if len(parts) > 1 {
					// Extract expiry timestamp
					expiryStr := strings.TrimSpace(parts[1])
					expiry, err := strconv.ParseInt(expiryStr, 10, 64)
					if err == nil {
						result.RateLimitExpiry = expiry
						// Update error to remove timestamp part
						result.Error = parts[0]
						// Update the message to be more user-friendly
						result.Message = "Rate limit exceeded: Tests can only be run once per minute."
					}
				}
			}
		}

		// Generate a user-friendly troubleshooting hint
		hint := generateBirdWeatherTroubleshootingHint(result)

		// Format the error message for the UI
		// For 404 errors, make sure to show the URL that was attempted
		switch {
		case strings.Contains(result.Error, "404") || strings.Contains(result.Error, "not found"):
			// Extract URL from error message if present
			url := extractURLFromError(result.Error)
			errorMsg := result.Error
			if url != "" {
				// Make it clear what URL was attempted
				errorMsg = fmt.Sprintf("URL not found (404): %s", url)
			}

			result.Message = fmt.Sprintf("%s %s %s",
				result.Message,
				errorMsg,
				hint)
		case strings.Contains(result.Error, "Failed to connect to BirdWeather API") && strings.Contains(result.Error, "Could not resolve"):
			// Handle detailed DNS resolution error specially
			parts := strings.Split(result.Error, " - ")
			if len(parts) > 1 {
				detailedMessage := parts[1]
				result.Message = fmt.Sprintf("%s: %s %s",
					result.Message,
					detailedMessage,
					hint)
			} else {
				result.Message = fmt.Sprintf("%s %s %s",
					result.Message,
					result.Error,
					hint)
			}
		case hint != "":
			result.Message = fmt.Sprintf("%s %s %s",
				result.Message,
				result.Error,
				hint)
		}
		result.Error = "" // Clear the error field as we've incorporated it into the message
	} else {
		// Explicitly mark progress messages
		result.IsProgress = strings.Contains(strings.ToLower(result.Message), "running") ||
			strings.Contains(strings.ToLower(result.Message), "testing") ||
			strings.Contains(strings.ToLower(result.Message), "establishing")

		// Add a formatted note for the successful completion stages to make them stand out
		if !result.IsProgress && (result.Stage == stageSoundscapeUpload || result.Stage == stageDetectionPost) {
			// Add an extra formatting to highlight the verification instructions
			if strings.Contains(result.Message, "This recording should appear") ||
				strings.Contains(result.Message, "should be visible on your BirdWeather dashboard") {
				// HTML is already properly formatted in the message from the testing module
				// Just add a verification tip that stands out
				result.Message = fmt.Sprintf("%s Visit your BirdWeather station page to verify this test submission was received.",
					result.Message)
			}
		}
	}
}

// generateBirdWeatherTroubleshootingHint provides context-specific troubleshooting suggestions
func generateBirdWeatherTroubleshootingHint(result *birdweather.TestResult) string {
	if result.Success {
		return "" // No hint needed for successful tests
	}

	// Check for rate limiting errors first
	if result.Stage == "Starting Test" && strings.Contains(result.Error, "rate limit exceeded") {
		return "To prevent abuse of the BirdWeather service, we limit how often tests can be run."
	}

	// Return troubleshooting hints based on the stage and error message
	switch result.Stage {
	case stageAPIConnectivity:
		switch {
		case strings.Contains(result.Error, "404"):
			return "The BirdWeather API endpoint returned a 404 error. This may indicate that the BirdWeather service has made changes to their API structure. Please check for any announcements from BirdWeather about API changes, or try again later as the service might be experiencing temporary issues."
		case strings.Contains(result.Error, "fallback DNS"):
			return "We attempted to connect using both your system's DNS resolver and public DNS services (Cloudflare, Google, and Quad9), but all attempts failed. This could indicate a more serious connectivity issue or that the BirdWeather service is currently unavailable. Please check your internet connection and try again later."
		case strings.Contains(result.Error, "Fallback DNS resolved the hostname"):
			return "Your system's DNS resolver failed to resolve the BirdWeather hostname, but our fallback DNS servers succeeded. This indicates a problem with your DNS configuration. Consider changing your DNS servers to public DNS providers like Google (8.8.8.8, 8.8.4.4) or Cloudflare (1.1.1.1, 1.0.0.1)."
		case strings.Contains(result.Error, "system DNS is incorrectly configured"):
			return "Your system's DNS resolver failed to resolve the BirdWeather hostname, but our fallback DNS servers succeeded. This indicates a problem with your DNS configuration. Consider changing your DNS servers to public DNS providers like Google (8.8.8.8, 8.8.4.4) or Cloudflare (1.1.1.1, 1.0.0.1)."
		case strings.Contains(result.Error, "timeout") || strings.Contains(result.Error, "i/o timeout"):
			return "Check your internet connection and ensure that app.birdweather.com is accessible from your network. Consider checking for any firewall rules that might be blocking outbound connections."
		case strings.Contains(result.Error, "no such host") || strings.Contains(result.Error, "DNS") || strings.Contains(result.Error, "name resolution"):
			return "Could not resolve the BirdWeather API hostname. We attempted to use alternative DNS servers as a fallback, but all attempts failed. Please check your DNS configuration and internet connectivity."
		default:
			return "Unable to connect to the BirdWeather API. Verify your internet connection and network settings."
		}

	case stageAuthentication:
		switch {
		case strings.Contains(result.Error, "fallback"):
			return "We attempted multiple methods to authenticate with BirdWeather but all failed. This could indicate network issues or that the BirdWeather service is currently experiencing problems. Please check your internet connection and try again later."
		case strings.Contains(result.Error, "404"):
			return "The BirdWeather station could not be found (404 error). Please verify that your BirdWeather station ID is correct and that your station is still registered and active on the BirdWeather platform."
		case strings.Contains(result.Error, "status code 401") || strings.Contains(result.Error, "status code 403") || strings.Contains(result.Error, "invalid station ID"):
			return "Authentication failed. Please verify that your BirdWeather Station ID is correct and that your station is registered and active on the BirdWeather platform."
		default:
			return "Failed to authenticate with BirdWeather. Check your BirdWeather token and ensure your station is properly registered on the BirdWeather platform."
		}

	case stageSoundscapeUpload:
		return "Failed to upload the test soundscape. This could be due to network issues, authentication problems, or the BirdWeather service might be experiencing issues. Try again later or check your BirdWeather station settings."

	case stageDetectionPost:
		return "Failed to post the test detection. Make sure your BirdWeather station is properly configured to accept detection uploads."

	default:
		return "Something went wrong with the BirdWeather test. Check your connection settings and try again."
	}
}

// extractURLFromError extracts a URL from an error message
func extractURLFromError(errorMessage string) string {
	// Common URL prefixes to look for
	urlPrefixes := []string{"http://", "https://"}

	// Check for https:// or http:// in the error message
	for _, prefix := range urlPrefixes {
		if idx := strings.Index(errorMessage, prefix); idx >= 0 {
			// Extract everything from the prefix start until a space, newline, or end of string
			urlStart := idx
			urlEnd := len(errorMessage)

			// Find the end of the URL (space, newline, closing parenthesis, etc.)
			for i := urlStart; i < len(errorMessage); i++ {
				char := errorMessage[i]
				if char == ' ' || char == '\n' || char == '\r' || char == ')' || char == '"' || char == '\'' {
					urlEnd = i
					break
				}
			}

			return errorMessage[urlStart:urlEnd]
		}
	}

	// Look for specific patterns like "URL: something" or "at something"
	patterns := []string{"URL: ", "at ", "endpoint: ", "tried URL: "}
	for _, pattern := range patterns {
		if idx := strings.Index(errorMessage, pattern); idx >= 0 {
			start := idx + len(pattern)
			end := len(errorMessage)

			// Find the end of the URL
			for i := start; i < len(errorMessage); i++ {
				char := errorMessage[i]
				if char == ' ' || char == '\n' || char == '\r' || char == ')' || char == '"' || char == '\'' {
					end = i
					break
				}
			}

			return errorMessage[start:end]
		}
	}

	return ""
}
