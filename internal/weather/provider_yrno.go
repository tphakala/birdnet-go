package weather

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	YrNoBaseURL        = "https://api.met.no/weatherapi/locationforecast/2.0/complete"
	yrNoProviderName   = "yrno"
	maxBodyPreviewSize = 200 // Maximum characters to show in error logs
)

// Sentinel errors for weather operations
var (
	ErrWeatherDataNotModified = errors.Newf("weather data not modified").Component("weather").Category(errors.CategoryNotFound).Build()
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
						WindGust       float64 `json:"wind_speed_of_gust"`
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

// yrNoRequestResult holds the result of a single HTTP request attempt
type yrNoRequestResult struct {
	body        []byte
	lastMod     string
	notModified bool
	shouldRetry bool
}

// readYrNoResponseBody reads and optionally decompresses the response body
func readYrNoResponseBody(resp *http.Response, log logger.Logger) ([]byte, error) {
	var reader io.Reader = resp.Body
	var gzReader *gzip.Reader

	if resp.Header.Get("Content-Encoding") == "gzip" {
		log.Debug("Response is gzip encoded, creating reader")
		var err error
		gzReader, err = gzip.NewReader(resp.Body)
		if err != nil {
			return nil, errors.New(err).
				Component("weather").
				Category(errors.CategoryNetwork).
				Context("operation", "create_gzip_reader").
				Context("provider", yrNoProviderName).
				Build()
		}
		reader = gzReader
	}

	body, err := io.ReadAll(reader)
	if gzReader != nil {
		if closeErr := gzReader.Close(); closeErr != nil {
			log.Debug("Failed to close gzip reader", logger.Error(closeErr))
		}
	}
	if err != nil {
		return nil, errors.New(err).
			Component("weather").
			Category(errors.CategoryNetwork).
			Context("operation", "read_response_body").
			Context("provider", yrNoProviderName).
			Build()
	}

	return body, nil
}

// handleYrNoResponse processes a single HTTP response and returns the result
func handleYrNoResponse(resp *http.Response, log logger.Logger, isLastAttempt bool) (*yrNoRequestResult, error) {
	result := &yrNoRequestResult{}

	// Handle Not Modified
	if resp.StatusCode == http.StatusNotModified {
		if err := resp.Body.Close(); err != nil {
			log.Debug("Failed to close response body", logger.Error(err))
		}
		result.notModified = true
		return result, nil
	}

	// Handle non-OK status
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		if err := resp.Body.Close(); err != nil {
			log.Debug("Failed to close response body", logger.Error(err))
		}
		responseBodyStr := truncateBodyPreview(string(bodyBytes))
		log.Warn("Received non-OK status code",
			logger.Int("status_code", resp.StatusCode),
			logger.String("response_body", responseBodyStr))

		if isLastAttempt {
			return nil, errors.Newf("received non-OK response (%d) after %d retries", resp.StatusCode, MaxRetries).
				Component("weather").
				Category(errors.CategoryNetwork).
				Context("operation", "weather_api_response").
				Context("provider", yrNoProviderName).
				Context("status_code", fmt.Sprintf("%d", resp.StatusCode)).
				Context("max_retries", fmt.Sprintf("%d", MaxRetries)).
				Build()
		}
		result.shouldRetry = true
		return result, nil
	}

	// Status is OK - read body
	result.lastMod = resp.Header.Get("Last-Modified")
	body, err := readYrNoResponseBody(resp, log)
	if closeErr := resp.Body.Close(); closeErr != nil {
		log.Debug("Failed to close response body", logger.Error(closeErr))
	}
	if err != nil {
		return nil, err
	}
	result.body = body
	return result, nil
}

// truncateBodyPreview truncates response body for logging
func truncateBodyPreview(body string) string {
	if len(body) > maxBodyPreviewSize {
		return body[:maxBodyPreviewSize] + "... (truncated)"
	}
	return body
}

// mapYrResponseToWeatherData converts YrResponse to WeatherData
func mapYrResponseToWeatherData(response *YrResponse, settings *conf.Settings) *WeatherData {
	current := response.Properties.Timeseries[0]
	iconCode := GetStandardIconCode(current.Data.Next1Hours.Summary.SymbolCode, yrNoProviderName)

	return &WeatherData{
		Time: current.Time,
		Location: Location{
			Latitude:  settings.BirdNET.Latitude,
			Longitude: settings.BirdNET.Longitude,
		},
		Temperature: Temperature{
			Current: current.Data.Instant.Details.AirTemperature,
		},
		Wind: Wind{
			Speed: current.Data.Instant.Details.WindSpeed,
			Deg:   int(current.Data.Instant.Details.WindDirection),
			Gust:  current.Data.Instant.Details.WindGust,
		},
		Precipitation: Precipitation{
			Amount: current.Data.Next1Hours.Details.PrecipitationAmount,
		},
		Clouds:      int(current.Data.Instant.Details.CloudArea),
		Pressure:    int(current.Data.Instant.Details.AirPressure),
		Humidity:    int(current.Data.Instant.Details.RelHumidity),
		Description: current.Data.Next1Hours.Summary.SymbolCode,
		Icon:        string(iconCode),
	}
}

// FetchWeather implements the Provider interface for YrNoProvider
func (p *YrNoProvider) FetchWeather(settings *conf.Settings) (*WeatherData, error) {
	apiURL := fmt.Sprintf("%s?lat=%.3f&lon=%.3f", YrNoBaseURL,
		settings.BirdNET.Latitude,
		settings.BirdNET.Longitude)

	providerLogger := getLogger().With(logger.String("provider", yrNoProviderName))
	providerLogger.Info("Fetching weather data", logger.String("url", apiURL))

	req, err := http.NewRequest("GET", apiURL, http.NoBody)
	if err != nil {
		return nil, newWeatherError(err, errors.CategoryNetwork, "create_http_request", yrNoProviderName)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept-Encoding", "gzip")
	if p.lastModified != "" {
		req.Header.Set("If-Modified-Since", p.lastModified)
	}

	// Execute request with retry
	body, err := p.executeWithRetry(req, providerLogger)
	if err != nil {
		return nil, err
	}

	// Parse response
	var response YrResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, newWeatherError(err, errors.CategoryValidation, "unmarshal_weather_data", yrNoProviderName)
	}

	if len(response.Properties.Timeseries) == 0 {
		return nil, newWeatherError(
			fmt.Errorf("no weather data available in timeseries"),
			errors.CategoryValidation,
			"validate_weather_response",
			yrNoProviderName,
		)
	}

	providerLogger.Info("Successfully received and parsed weather data")
	mappedData := mapYrResponseToWeatherData(&response, settings)
	providerLogger.Debug("Mapped API response to WeatherData structure",
		logger.Time("time", mappedData.Time),
		logger.Float64("temp", mappedData.Temperature.Current))
	return mappedData, nil
}

// executeWithRetry executes the HTTP request with retry logic
func (p *YrNoProvider) executeWithRetry(req *http.Request, log logger.Logger) ([]byte, error) {
	client := &http.Client{Timeout: RequestTimeout}

	for i := range MaxRetries {
		isLastAttempt := i == MaxRetries-1
		attemptLogger := log.With(
			logger.Int("attempt", i+1),
			logger.Int("max_attempts", MaxRetries))

		resp, err := client.Do(req)
		if err != nil {
			attemptLogger.Warn("HTTP request failed", logger.Error(err))
			if isLastAttempt {
				return nil, newWeatherErrorWithRetries(err, errors.CategoryNetwork, "weather_api_request", yrNoProviderName)
			}
			time.Sleep(RetryDelay)
			continue
		}

		attemptLogger.Debug("Received HTTP response", logger.Int("status_code", resp.StatusCode))
		result, err := handleYrNoResponse(resp, attemptLogger, isLastAttempt)
		if err != nil {
			return nil, err
		}

		if result.notModified {
			log.Info("Weather data not modified since last fetch", logger.String("last_modified", p.lastModified))
			return nil, ErrWeatherDataNotModified
		}

		if result.shouldRetry {
			time.Sleep(RetryDelay)
			continue
		}

		// Success
		if result.lastMod != "" {
			p.lastModified = result.lastMod
		}
		return result.body, nil
	}

	return nil, newWeatherErrorWithRetries(
		fmt.Errorf("max retries exceeded"),
		errors.CategoryNetwork,
		"weather_api_request",
		yrNoProviderName,
	)
}
