// provider_wunderground.go: WeatherUnderground integration for BirdNET-Go
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	wundergroundBaseURL      = "https://api.weather.com/v2/pws/observations/current"
	wundergroundProviderName = "wunderground"
)

// Thresholds for inferring weather icons. Tune as needed.
const (
	// Precipitation rate in mm/h considered "heavy" for thunderstorm classification (paired with strong gusts).
	ThunderstormPrecipMM = 10.0
	// Wind gust threshold in m/s indicating thunderstorm-level winds (paired with heavy precipitation).
	ThunderstormGustMS = 15.0
	// Solar radiation in W/m^2 at or below this is treated as night.
	NightSolarRadiationThreshold = 5.0
	// Daytime solar radiation above this indicates clear sky.
	DayClearSRThreshold = 600.0
	// Daytime solar radiation range for partly cloudy (inclusive).
	DayPartlyCloudyLowerSR = 200.0
	DayPartlyCloudyUpperSR = 600.0

	// Temperature thresholds for weather conditions
	FreezingPointC     = 0.0  // Celsius freezing point for snow/rain determination
	FogTempThresholdC  = 5.0  // Maximum temperature for fog formation
	FogHumidityPercent = 90.0 // Minimum humidity for fog formation

	// Night humidity thresholds for cloud inference
	NightCloudyHumidityPercent       = 85.0 // Humidity threshold for cloudy conditions at night
	NightPartlyCloudyHumidityPercent = 60.0 // Humidity threshold for partly cloudy at night

	// Unit conversion factors
	KmhToMs = 0.277778 // Convert km/h to m/s (divide by 3.6)

	// wundergroundMetricUnits is the unit system always sent to the WU API.
	// The API returns only the measurement object matching the requested units,
	// so we force metric to ensure obs.Metric is always populated.
	wundergroundMetricUnits = "m"

	// Feels-like temperature thresholds - Metric
	MetricHotTempC        = 27.0         // Temperature above which to use heat index
	MetricColdTempC       = 10.0         // Temperature below which to use wind chill
	MetricWindThresholdMs = 1.3333333333 // Wind speed threshold for wind chill (4.8 km/h)
)

var stationIDRegex = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

// inferNightIcon determines cloud cover icon based on humidity during night.
func inferNightIcon(humidity float64) IconCode {
	switch {
	case humidity >= NightCloudyHumidityPercent:
		return IconCloudy
	case humidity >= NightPartlyCloudyHumidityPercent:
		return IconPartlyCloudy
	default:
		return IconClearSky
	}
}

// inferDaytimeIcon determines cloud cover icon based on solar radiation during day.
func inferDaytimeIcon(solarRadiation float64) IconCode {
	switch {
	case solarRadiation > DayClearSRThreshold:
		return IconClearSky
	case solarRadiation >= DayPartlyCloudyLowerSR:
		return IconPartlyCloudy
	default:
		return IconCloudy
	}
}

// InferWundergroundIcon infers a standardized icon code from Wunderground measurements.
// windGustMS must be provided in m/s.
func InferWundergroundIcon(tempC, precipMM, humidity, solarRadiation, windGustMS float64) IconCode {
	// 1. Thunderstorm: heavy rain + high wind gust
	if precipMM > ThunderstormPrecipMM && windGustMS > ThunderstormGustMS {
		return IconThunderstorm
	}
	// 2. Precipitation type: snow vs rain
	if precipMM > 0 {
		if tempC < FreezingPointC {
			return IconSnow
		}
		return IconRain
	}
	// 3. Fog
	if humidity > FogHumidityPercent && tempC < FogTempThresholdC {
		return IconFog
	}
	// 4. Night: infer clouds by humidity
	if solarRadiation <= NightSolarRadiationThreshold {
		return inferNightIcon(humidity)
	}
	// 5. Daytime: infer clouds by solar radiation
	return inferDaytimeIcon(solarRadiation)
}

// wundergroundErrorResponse represents an error response from Weather Underground API
type wundergroundErrorResponse struct {
	Metadata struct {
		TransactionID string `json:"transaction_id"`
	} `json:"metadata"`
	Success bool `json:"success"`
	Errors  []struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"errors"`
}

// wundergroundMeasurementData holds metric weather measurements from Wunderground API
type wundergroundMeasurementData struct {
	Temp        float64 `json:"temp"`
	HeatIndex   float64 `json:"heatIndex"`
	Dewpt       float64 `json:"dewpt"`
	WindChill   float64 `json:"windChill"`
	WindSpeed   float64 `json:"windSpeed"`
	WindGust    float64 `json:"windGust"`
	Pressure    float64 `json:"pressure"`
	PrecipRate  float64 `json:"precipRate"`
	PrecipTotal float64 `json:"precipTotal"`
	Elev        float64 `json:"elev"`
}

// wundergroundObservation represents a single weather observation from the Weather Underground API
type wundergroundObservation struct {
	StationID      string                      `json:"stationID"`
	ObsTimeUtc     string                      `json:"obsTimeUtc"`
	ObsTimeLocal   string                      `json:"obsTimeLocal"`
	Neighborhood   string                      `json:"neighborhood"`
	SoftwareType   string                      `json:"softwareType"`
	Country        string                      `json:"country"`
	SolarRadiation float64                     `json:"solarRadiation"`
	Lon            float64                     `json:"lon"`
	Lat            float64                     `json:"lat"`
	Uv             float64                     `json:"uv"`
	Winddir        int                         `json:"winddir"`
	Humidity       float64                     `json:"humidity"`
	QcStatus       int                         `json:"qcStatus"`
	Metric         wundergroundMeasurementData `json:"metric"`
}

// wundergroundResponse models the JSON observations response from the Weather Underground API.
type wundergroundResponse struct {
	Observations []wundergroundObservation `json:"observations"`
}

// parseWundergroundError extracts and formats error messages from Weather Underground API responses
func parseWundergroundError(bodyBytes []byte, statusCode int, stationId string, log logger.Logger) string {
	// Try to parse Weather Underground error response
	var errorResp wundergroundErrorResponse

	if err := json.Unmarshal(bodyBytes, &errorResp); err == nil && len(errorResp.Errors) > 0 {
		// Extract the first error message
		errorMessage := errorResp.Errors[0].Error.Message
		errorCode := errorResp.Errors[0].Error.Code

		// Log the structured error
		log.Error("Weather Underground API error",
			logger.Int("status_code", statusCode),
			logger.String("error_code", errorCode),
			logger.String("error_message", errorMessage),
			logger.String("station", stationId))

		// Provide user-friendly error messages based on error code
		switch errorCode {
		case "CDN-0001":
			return "invalid API key - please check your Weather Underground API key"
		case "CDN-0002":
			return "invalid station ID - please verify your Weather Underground station ID"
		case "CDN-0003":
			return "station not found - the specified station ID does not exist"
		case "CDN-0004":
			return "rate limit exceeded - please try again later"
		default:
			if errorMessage == "" {
				return fmt.Sprintf("Weather Underground API error (code: %s)", errorCode)
			}
			return errorMessage
		}
	}

	// Fallback for non-standard error responses
	const maxBodyPreview = 512
	var bodyPreview string
	if len(bodyBytes) > maxBodyPreview {
		bodyPreview = strings.ReplaceAll(string(bodyBytes[:maxBodyPreview]), "\n", " ") + "..."
	} else {
		bodyPreview = strings.ReplaceAll(string(bodyBytes), "\n", " ")
	}

	log.Error("Received non-OK status code",
		logger.Int("status_code", statusCode),
		logger.String("response_preview", bodyPreview))

	// Generic error message based on status code
	switch statusCode {
	case http.StatusUnauthorized:
		return "authentication failed - please check your API key"
	case http.StatusForbidden:
		return "access forbidden - please check your API permissions"
	case http.StatusNotFound:
		return "weather station not found - please verify the station ID"
	case http.StatusTooManyRequests:
		return "rate limit exceeded - please try again later"
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "Weather Underground service is temporarily unavailable - please try again later"
	default:
		return fmt.Sprintf("Weather Underground API error (HTTP %d)", statusCode)
	}
}

// wundergroundConfig holds validated configuration for Wunderground API requests
type wundergroundConfig struct {
	apiKey    string
	stationID string
	endpoint  string
}

// validateWundergroundConfig validates and normalizes Wunderground configuration
func validateWundergroundConfig(settings *conf.Settings) (*wundergroundConfig, error) {
	cfg := &wundergroundConfig{
		apiKey:    settings.Realtime.Weather.Wunderground.APIKey,
		stationID: settings.Realtime.Weather.Wunderground.StationID,
		endpoint:  settings.Realtime.Weather.Wunderground.Endpoint,
	}

	if cfg.endpoint == "" {
		cfg.endpoint = wundergroundBaseURL
	}
	if cfg.apiKey == "" || cfg.stationID == "" {
		return nil, newWeatherError(
			fmt.Errorf("wunderground API key or station ID not configured"),
			errors.CategoryConfiguration, "validate_config", wundergroundProviderName,
		)
	}
	if !stationIDRegex.MatchString(cfg.stationID) {
		return nil, newWeatherError(
			fmt.Errorf("invalid wunderground station ID format"),
			errors.CategoryConfiguration, "validate_station_id", wundergroundProviderName,
		)
	}
	return cfg, nil
}

// buildWundergroundURL constructs the API URL from configuration
func buildWundergroundURL(cfg *wundergroundConfig) (string, error) {
	u, err := url.Parse(cfg.endpoint)
	if err != nil {
		return "", newWeatherError(
			fmt.Errorf("invalid wunderground endpoint: %w", err),
			errors.CategoryConfiguration, "parse_endpoint", wundergroundProviderName,
		)
	}
	q := u.Query()
	q.Set("stationId", cfg.stationID)
	q.Set("format", "json")
	q.Set("units", wundergroundMetricUnits)
	q.Set("apiKey", cfg.apiKey)
	q.Set("numericPrecision", "decimal")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (p *WundergroundProvider) FetchWeather(settings *conf.Settings) (*WeatherData, error) {
	cfg, err := validateWundergroundConfig(settings)
	if err != nil {
		return nil, err
	}

	apiURL, err := buildWundergroundURL(cfg)
	if err != nil {
		return nil, err
	}

	providerLogger := getLogger().With(logger.String("provider", wundergroundProviderName))
	providerLogger.Info("Fetching weather data", logger.String("url", maskAPIKey(apiURL, "apiKey")))

	// Execute request
	body, err := p.executeRequest(apiURL, cfg, providerLogger)
	if err != nil {
		return nil, err
	}

	// Parse and validate response
	wuResp, err := parseWundergroundResponse(body)
	if err != nil {
		return nil, err
	}

	providerLogger.Info("Successfully received and parsed weather data")
	return mapWundergroundResponse(wuResp, providerLogger), nil
}

// executeRequest performs the HTTP request with context timeout
func (p *WundergroundProvider) executeRequest(apiURL string, cfg *wundergroundConfig, log logger.Logger) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, http.NoBody)
	if err != nil {
		return nil, newWeatherError(err, errors.CategoryNetwork, "create_http_request", wundergroundProviderName)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, handleWundergroundRequestError(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// HTTP 204: station exists but has no data available (not an error)
	if resp.StatusCode == http.StatusNoContent {
		log.Info("Weather station returned no data (HTTP 204)",
			logger.String("station", cfg.stationID))
		return nil, ErrWeatherNoData
	}

	// HTTP 401: authentication failed — likely invalid API key
	if resp.StatusCode == http.StatusUnauthorized {
		bodyBytes, _ := io.ReadAll(resp.Body)
		errorMessage := parseWundergroundError(bodyBytes, resp.StatusCode, cfg.stationID, log)
		log.Error("Weather API authentication failed — check your API key",
			logger.String("station", cfg.stationID),
			logger.String("detail", errorMessage))
		return nil, ErrWeatherAuthFailed
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		errorMessage := parseWundergroundError(bodyBytes, resp.StatusCode, cfg.stationID, log)
		return nil, newWeatherError(
			fmt.Errorf("%s", errorMessage),
			errors.CategoryNetwork, "weather_api_response", wundergroundProviderName,
		)
	}

	return io.ReadAll(resp.Body)
}

// handleWundergroundRequestError categorizes HTTP request errors
func handleWundergroundRequestError(ctx context.Context, err error) error {
	var category errors.ErrorCategory
	switch ctx.Err() {
	case context.Canceled:
		category = errors.CategoryTimeout
	case context.DeadlineExceeded:
		category = errors.CategoryTimeout
	default:
		category = errors.CategoryNetwork
	}
	return newWeatherError(err, category, "weather_api_request", wundergroundProviderName)
}

// parseWundergroundResponse parses and validates the JSON response
func parseWundergroundResponse(body []byte) (*wundergroundResponse, error) {
	var wuResp wundergroundResponse
	if err := json.Unmarshal(body, &wuResp); err != nil {
		return nil, newWeatherError(err, errors.CategoryValidation, "unmarshal_weather_data", wundergroundProviderName)
	}
	if len(wuResp.Observations) == 0 {
		return nil, newWeatherError(
			fmt.Errorf("no observations returned from API"),
			errors.CategoryValidation, "validate_weather_response", wundergroundProviderName,
		)
	}
	return &wuResp, nil
}

// mapWundergroundResponse converts wundergroundResponse to WeatherData.
// All data is read from the metric measurement object, since we always
// request units=m from the WU API.
func mapWundergroundResponse(wuResp *wundergroundResponse, log logger.Logger) *WeatherData {
	obs := wuResp.Observations[0]

	obsTime, err := time.Parse(time.RFC3339, obs.ObsTimeUtc)
	if err != nil {
		log.Warn("Failed to parse observation time; using current UTC", logger.String("obs_time_utc", obs.ObsTimeUtc))
		obsTime = time.Now().UTC()
	}

	measurements := extractMeasurements(&obs)
	feelsLike := calculateFeelsLike(measurements)
	precipMMH := getPrecipitationRate(&obs)

	iconCode := InferWundergroundIcon(
		measurements.temp, precipMMH, float64(obs.Humidity), obs.SolarRadiation, measurements.windGust,
	)

	return &WeatherData{
		Time: obsTime,
		Location: Location{
			Latitude:  obs.Lat,
			Longitude: obs.Lon,
			Country:   obs.Country,
			City:      obs.Neighborhood,
		},
		Temperature: Temperature{
			Current:   measurements.temp,
			FeelsLike: feelsLike,
			Min:       measurements.temp,
			Max:       measurements.temp,
		},
		Wind: Wind{
			Speed: measurements.windSpeed,
			Deg:   obs.Winddir,
			Gust:  measurements.windGust,
		},
		Pressure:    int(math.Round(measurements.pressure)),
		Humidity:    int(math.Round(obs.Humidity)),
		Description: IconDescription[iconCode],
		Icon:        string(iconCode),
	}
}

// getPrecipitationRate extracts precipitation rate in mm/h from metric data.
func getPrecipitationRate(obs *wundergroundObservation) float64 {
	if obs.Metric.PrecipRate > 0 {
		return obs.Metric.PrecipRate
	}
	return 0.0
}

// isInvalid checks if a float64 value is NaN (invalid for HeatIndex/WindChill logic)
// Note: WU may use 0 for N/A but we treat only NaN as invalid to avoid dropping legitimate zero values
func isInvalid(val float64) bool {
	return math.IsNaN(val)
}

// weatherMeasurements holds extracted weather data from API response.
// All values are in metric units (Celsius, m/s, hPa).
type weatherMeasurements struct {
	temp      float64
	heatIndex float64
	windChill float64
	windSpeed float64
	windGust  float64
	pressure  float64
}

// extractMeasurements extracts weather measurements from the metric observation data.
// We always request units=m from the WU API, so only obs.Metric is populated.
func extractMeasurements(obs *wundergroundObservation) weatherMeasurements {
	return weatherMeasurements{
		temp:      obs.Metric.Temp,
		heatIndex: obs.Metric.HeatIndex,
		windChill: obs.Metric.WindChill,
		windSpeed: obs.Metric.WindSpeed * KmhToMs,
		windGust:  obs.Metric.WindGust * KmhToMs,
		pressure:  obs.Metric.Pressure,
	}
}

// calculateFeelsLike calculates the feels-like temperature based on conditions.
// All values are in metric units (Celsius temperatures, m/s wind speed).
func calculateFeelsLike(m weatherMeasurements) float64 {
	switch {
	case m.temp >= MetricHotTempC && m.heatIndex > 0 && !isInvalid(m.heatIndex):
		return m.heatIndex
	case m.temp <= MetricColdTempC && m.windSpeed > MetricWindThresholdMs && !isInvalid(m.windChill):
		return m.windChill
	default:
		return m.temp
	}
}
