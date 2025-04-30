package weather

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	YrNoBaseURL      = "https://api.met.no/weatherapi/locationforecast/2.0/complete"
	yrNoProviderName = "yrno"
)

// YrResponse represents the structure of the Yr.no API response
type YrResponse struct {
	Properties struct {
		Timeseries []struct {
			Time time.Time `json:"time"`
			Data struct {
				Instant struct {
					Details struct {
						AirPressure    float64 `json:"air_pressure_at_sea_level"`
						AirTemperature float64 `json:"air_temperature"`
						CloudArea      float64 `json:"cloud_area_fraction"`
						RelHumidity    float64 `json:"relative_humidity"`
						WindSpeed      float64 `json:"wind_speed"`
						WindDirection  float64 `json:"wind_from_direction"`
					} `json:"details"`
				} `json:"instant"`
				Next1Hours struct {
					Summary struct {
						SymbolCode string `json:"symbol_code"`
					} `json:"summary"`
					Details struct {
						PrecipitationAmount float64 `json:"precipitation_amount"`
					} `json:"details"`
				} `json:"next_1_hours"`
			} `json:"data"`
		} `json:"timeseries"`
	} `json:"properties"`
}

// FetchWeather implements the Provider interface for YrNoProvider
func (p *YrNoProvider) FetchWeather(settings *conf.Settings) (*WeatherData, error) {
	url := fmt.Sprintf("%s?lat=%.3f&lon=%.3f", YrNoBaseURL,
		settings.BirdNET.Latitude,
		settings.BirdNET.Longitude)

	logger := weatherLogger.With("provider", yrNoProviderName)
	logger.Info("Fetching weather data", "url", url)

	client := &http.Client{
		Timeout: RequestTimeout,
	}

	req, err := http.NewRequest("GET", url, http.NoBody)
	if err != nil {
		logger.Error("Failed to create HTTP request", "url", url, "error", err)
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept-Encoding", "gzip") // Prefer gzip only

	if p.lastModified != "" {
		logger.Debug("Adding If-Modified-Since header", "value", p.lastModified)
		req.Header.Set("If-Modified-Since", p.lastModified)
	}

	var response YrResponse
	var resp *http.Response // Declare outside loop

	for i := 0; i < MaxRetries; i++ {
		attemptLogger := logger.With("attempt", i+1, "max_attempts", MaxRetries)
		attemptLogger.Debug("Sending HTTP request")
		resp, err = client.Do(req)
		if err != nil {
			attemptLogger.Warn("HTTP request failed", "error", err)
			if i == MaxRetries-1 {
				logger.Error("Failed to fetch weather data after max retries", "error", err)
				return nil, fmt.Errorf("error fetching weather data after %d retries: %w", MaxRetries, err)
			}
			time.Sleep(RetryDelay)
			continue // Retry
		}

		// Log status code regardless
		attemptLogger.Debug("Received HTTP response", "status_code", resp.StatusCode)

		// Handle Not Modified specifically
		if resp.StatusCode == http.StatusNotModified {
			resp.Body.Close()
			logger.Info("Weather data not modified since last fetch", "status_code", http.StatusNotModified, "last_modified", p.lastModified)
			// Returning a specific error might be better, but for now, let upstream handle nil data
			// For now, treat as non-error, but signal no new data maybe?
			// Let's return nil, nil for now, indicating success but no new data.
			// The calling code in weather.go needs to handle this case (currently doesn't explicitly)
			// TODO: Refactor polling logic to handle (nil, nil) from FetchWeather gracefully.
			return nil, nil // Indicate success, but no new data
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body) // Try reading body for context
			resp.Body.Close()                     // Close body even on error
			responseBodyStr := string(bodyBytes)
			if len(responseBodyStr) > 200 {
				responseBodyStr = responseBodyStr[:200] + "... (truncated)"
			}
			attemptLogger.Warn("Received non-OK status code", "status_code", resp.StatusCode, "response_body", responseBodyStr)
			if i == MaxRetries-1 {
				logger.Error("Failed to fetch weather data due to non-OK status after max retries", "status_code", resp.StatusCode, "response_body", responseBodyStr)
				return nil, fmt.Errorf("received non-OK response (%d) after %d retries", resp.StatusCode, MaxRetries)
			}
			time.Sleep(RetryDelay)
			continue // Retry
		}

		// Status is OK
		if lastMod := resp.Header.Get("Last-Modified"); lastMod != "" {
			logger.Debug("Updating Last-Modified header from response", "new_value", lastMod)
			p.lastModified = lastMod
		}

		// Handle gzip compression
		var reader io.Reader = resp.Body
		var gzReader *gzip.Reader // Keep reference for closing
		if resp.Header.Get("Content-Encoding") == "gzip" {
			attemptLogger.Debug("Response is gzip encoded, creating reader")
			gzReader, err = gzip.NewReader(resp.Body)
			if err != nil {
				resp.Body.Close()
				logger.Error("Failed to create gzip reader", "error", err)
				return nil, fmt.Errorf("error creating gzip reader: %w", err)
			}
			reader = gzReader
		}

		body, err := io.ReadAll(reader)
		if gzReader != nil {
			gzReader.Close() // Close gzip reader immediately after reading
		}
		resp.Body.Close() // Close original body now
		if err != nil {
			logger.Error("Failed to read response body", "status_code", resp.StatusCode, "error", err)
			return nil, fmt.Errorf("error reading response body: %w", err)
		}

		if err := json.Unmarshal(body, &response); err != nil {
			logger.Error("Failed to unmarshal response JSON", "status_code", resp.StatusCode, "error", err, "response_body", string(body))
			return nil, fmt.Errorf("error unmarshaling weather data: %w", err)
		}

		// Success!
		logger.Info("Successfully received and parsed weather data", "status_code", resp.StatusCode)
		break // Exit retry loop
	}

	if len(response.Properties.Timeseries) == 0 {
		logger.Error("API response parsed successfully but contained no timeseries data")
		return nil, fmt.Errorf("no weather data available in timeseries")
	}

	current := response.Properties.Timeseries[0]
	iconCode := GetStandardIconCode(current.Data.Next1Hours.Summary.SymbolCode, yrNoProviderName)

	mappedData := &WeatherData{
		Time: current.Time,
		Location: Location{
			Latitude:  settings.BirdNET.Latitude, // Consider getting from API if available
			Longitude: settings.BirdNET.Longitude,
			// Country/City might be available elsewhere in Yr.no API or need separate lookup
		},
		Temperature: Temperature{
			Current: current.Data.Instant.Details.AirTemperature,
			// FeelsLike, Min, Max might be available in other parts of Yr.no timeseries
		},
		Wind: Wind{
			Speed: current.Data.Instant.Details.WindSpeed,
			Deg:   int(current.Data.Instant.Details.WindDirection),
			// Gust might be available
		},
		Precipitation: Precipitation{
			Amount: current.Data.Next1Hours.Details.PrecipitationAmount,
			// Type (rain/snow) might be inferred from symbol_code or temp
		},
		Clouds:      int(current.Data.Instant.Details.CloudArea),
		Pressure:    int(current.Data.Instant.Details.AirPressure),
		Humidity:    int(current.Data.Instant.Details.RelHumidity),
		Description: current.Data.Next1Hours.Summary.SymbolCode, // Keep original provider desc for now
		Icon:        string(iconCode),
		// Visibility might be available
	}

	logger.Debug("Mapped API response to WeatherData structure", "time", mappedData.Time, "temp", mappedData.Temperature.Current)
	return mappedData, nil
}
