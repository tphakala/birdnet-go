package weather

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	YrNoBaseURL        = "https://api.met.no/weatherapi/locationforecast/2.0/complete"
	yrNoProviderName   = "yrno"
	maxBodyPreviewSize = 200 // Maximum characters to show in error logs

	// yrNoCoordPrecision is the number of decimal places used when formatting
	// latitude/longitude into the request URL (~110 m resolution), matching the
	// OpenWeather provider.
	yrNoCoordPrecision = 3
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

// handleYrNoResponse processes a single HTTP response and returns the result.
// It owns closing resp.Body (per the executeWeatherRequest handler contract); a
// single deferred close covers every return path instead of one per branch.
func handleYrNoResponse(resp *http.Response, log logger.Logger, isLastAttempt bool) (*yrNoRequestResult, error) {
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Debug("Failed to close response body", logger.Error(closeErr))
		}
	}()

	result := &yrNoRequestResult{}

	// Handle Not Modified
	if resp.StatusCode == http.StatusNotModified {
		result.notModified = true
		return result, nil
	}

	// Handle non-OK status
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
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

	// yr.no reports a precipitation amount but no explicit type; derive the type
	// from the standardized icon, and only when precipitation is actually present.
	// Clamp a negative amount to zero (defensive against API/sensor anomalies),
	// matching the Wunderground provider.
	precipAmount := max(0, current.Data.Next1Hours.Details.PrecipitationAmount)
	precipType := ""
	if precipAmount > 0 {
		precipType = precipTypeFromIconCode(iconCode)
	}

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
			Amount: precipAmount,
			Type:   precipType,
		},
		Clouds:      int(current.Data.Instant.Details.CloudArea),
		Pressure:    int(current.Data.Instant.Details.AirPressure),
		Humidity:    int(current.Data.Instant.Details.RelHumidity),
		WeatherMain: weatherMainFromIconCode(iconCode),
		Description: current.Data.Next1Hours.Summary.SymbolCode,
		Icon:        string(iconCode),
	}
}

// FetchWeather implements the Provider interface for YrNoProvider
func (p *YrNoProvider) FetchWeather(ctx context.Context, settings *conf.Settings) (*WeatherData, error) {
	// Build the query with url.Values so the coordinates are properly escaped,
	// matching buildOpenWeatherURL and buildWundergroundURL. yr.no (api.met.no)
	// needs no API key, so only the coordinates go in the query. YrNoBaseURL is a
	// constant with no existing query, so appending "?" + encoded is safe.
	query := url.Values{
		"lat": {strconv.FormatFloat(settings.BirdNET.Latitude, 'f', yrNoCoordPrecision, 64)},
		"lon": {strconv.FormatFloat(settings.BirdNET.Longitude, 'f', yrNoCoordPrecision, 64)},
	}
	apiURL := YrNoBaseURL + "?" + query.Encode()

	providerLogger := getLogger().WithContext(ctx).With(logger.String("provider", yrNoProviderName))
	// Mask the URL before logging: its query carries the user's lat/lon, which
	// are PII (yr.no needs no API key, so coordinates are the sensitive part).
	providerLogger.Info("Fetching weather data", logger.String("url", maskURLForLog(apiURL)))

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, http.NoBody)
	if err != nil {
		// Scrub before wrapping: http.NewRequestWithContext can return a
		// *url.Error that embeds apiURL, which carries the user's lat/lon coordinates.
		return nil, newWeatherError(privacy.WrapError(err), errors.CategoryNetwork, "create_http_request", yrNoProviderName)
	}
	req.Header.Set("User-Agent", UserAgent())
	req.Header.Set("Accept-Encoding", "gzip")
	p.mu.Lock()
	if p.lastModified != "" {
		req.Header.Set("If-Modified-Since", p.lastModified)
	}
	p.mu.Unlock()

	// Execute request with retry via the shared executor.
	body, err := executeWeatherRequest(ctx, p.httpClient, req, yrNoProviderName, providerLogger, p.handleResponse)
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

// handleResponse adapts the yr.no per-status handling (conditional GET, gzip
// decode, Last-Modified tracking) to the shared retry executor. A 304 returns
// the not-modified sentinel; a transient non-OK asks for a retry; a 200 records
// the new Last-Modified value for the next conditional request and returns the
// body.
func (p *YrNoProvider) handleResponse(resp *http.Response, attemptLog logger.Logger, isLastAttempt bool) (body []byte, retry bool, err error) {
	result, err := handleYrNoResponse(resp, attemptLog, isLastAttempt)
	if err != nil {
		return nil, false, err
	}

	if result.notModified {
		p.mu.Lock()
		lm := p.lastModified
		p.mu.Unlock()
		attemptLog.Info("Weather data not modified since last fetch", logger.String("last_modified", lm))
		return nil, false, ErrWeatherDataNotModified
	}

	if result.shouldRetry {
		return nil, true, nil
	}

	// Success: persist the Last-Modified value for the next conditional request.
	if result.lastMod != "" {
		p.mu.Lock()
		p.lastModified = result.lastMod
		p.mu.Unlock()
	}
	return result.body, false, nil
}
