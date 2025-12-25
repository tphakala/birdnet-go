// provider_wunderground.go: WeatherUnderground integration for BirdNET-Go
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
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
	KmhToMs     = 0.277778      // Convert km/h to m/s (divide by 3.6)
	MphToMs     = 0.44704       // Convert mph to m/s
	InHgToHPa   = 33.8638866667 // Convert inches of mercury to hectopascals
	InchesToMm  = 25.4          // Convert inches to millimeters
	
	// Feels-like temperature thresholds - Metric
	MetricHotTempC          = 27.0         // Temperature above which to use heat index
	MetricColdTempC         = 10.0         // Temperature below which to use wind chill
	MetricWindThresholdMs   = 1.3333333333 // Wind speed threshold for wind chill (4.8 km/h)
	
	// Feels-like temperature thresholds - Hybrid (UK)
	HybridWindThresholdMs   = 1.34112      // Wind speed threshold for wind chill (3 mph)
	
	// Feels-like temperature thresholds - Imperial
	ImperialHotTempF        = 80.0         // Temperature above which to use heat index
	ImperialColdTempF       = 50.0         // Temperature below which to use wind chill
	ImperialWindThresholdMph = 3.0         // Wind speed threshold for wind chill
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

// wundergroundResponse models the JSON observations response from the Weather Underground API.
type wundergroundResponse struct {
	Observations []struct {
		StationID      string  `json:"stationID"`
		ObsTimeUtc     string  `json:"obsTimeUtc"`
		ObsTimeLocal   string  `json:"obsTimeLocal"`
		Neighborhood   string  `json:"neighborhood"`
		SoftwareType   string  `json:"softwareType"`
		Country        string  `json:"country"`
		SolarRadiation float64 `json:"solarRadiation"`
		Lon            float64 `json:"lon"`
		Lat            float64 `json:"lat"`
		Uv             float64 `json:"uv"`
		Winddir        int     `json:"winddir"`
		Humidity       float64 `json:"humidity"`
		QcStatus       int     `json:"qcStatus"`
		// Optional/extra fields for improved icon inference:
		Imperial struct {
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
		} `json:"imperial"`
		Metric struct {
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
		} `json:"metric"`
	} `json:"observations"`
}

// parseWundergroundError extracts and formats error messages from Weather Underground API responses
func parseWundergroundError(bodyBytes []byte, statusCode int, stationId, units string, logger *slog.Logger) string {
	// Try to parse Weather Underground error response
	var errorResp wundergroundErrorResponse
	
	if err := json.Unmarshal(bodyBytes, &errorResp); err == nil && len(errorResp.Errors) > 0 {
		// Extract the first error message
		errorMessage := errorResp.Errors[0].Error.Message
		errorCode := errorResp.Errors[0].Error.Code
		
		// Log the structured error
		logger.Error("Weather Underground API error", 
			"status_code", statusCode,
			"error_code", errorCode,
			"error_message", errorMessage,
			"station", stationId,
			"units", units)
		
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
	
	logger.Error("Received non-OK status code", "status_code", statusCode, "response_preview", bodyPreview)
	
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
	units     string
	endpoint  string
}

// validateWundergroundConfig validates and normalizes Wunderground configuration
func validateWundergroundConfig(settings *conf.Settings) (*wundergroundConfig, error) {
	cfg := &wundergroundConfig{
		apiKey:    settings.Realtime.Weather.Wunderground.APIKey,
		stationID: settings.Realtime.Weather.Wunderground.StationID,
		units:     settings.Realtime.Weather.Wunderground.Units,
		endpoint:  settings.Realtime.Weather.Wunderground.Endpoint,
	}

	if cfg.endpoint == "" {
		cfg.endpoint = wundergroundBaseURL
	}
	if cfg.units == "" {
		cfg.units = "e"
	}
	if cfg.units != "e" && cfg.units != "m" && cfg.units != "h" {
		weatherLogger.Warn("Invalid units value, falling back to imperial", "units", cfg.units)
		cfg.units = "e"
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
	q.Set("units", cfg.units)
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

	logger := weatherLogger.With("provider", wundergroundProviderName)
	logger.Info("Fetching weather data", "url", maskAPIKey(apiURL, "apiKey"))

	// Execute request
	body, err := p.executeRequest(apiURL, cfg, logger)
	if err != nil {
		return nil, err
	}

	// Parse and validate response
	wuResp, err := parseWundergroundResponse(body)
	if err != nil {
		return nil, err
	}

	logger.Info("Successfully received and parsed weather data")
	return mapWundergroundResponse(wuResp, cfg.units, logger), nil
}

// executeRequest performs the HTTP request with context timeout
func (p *WundergroundProvider) executeRequest(apiURL string, cfg *wundergroundConfig, logger *slog.Logger) ([]byte, error) {
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
		return nil, handleWundergroundRequestError(err, ctx)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		errorMessage := parseWundergroundError(bodyBytes, resp.StatusCode, cfg.stationID, cfg.units, logger)
		return nil, newWeatherError(
			fmt.Errorf("%s", errorMessage),
			errors.CategoryNetwork, "weather_api_response", wundergroundProviderName,
		)
	}

	return io.ReadAll(resp.Body)
}

// handleWundergroundRequestError categorizes HTTP request errors
func handleWundergroundRequestError(err error, ctx context.Context) error {
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

// mapWundergroundResponse converts wundergroundResponse to WeatherData
func mapWundergroundResponse(wuResp *wundergroundResponse, units string, logger *slog.Logger) *WeatherData {
	obs := wuResp.Observations[0]

	obsTime, err := time.Parse(time.RFC3339, obs.ObsTimeUtc)
	if err != nil {
		logger.Warn("Failed to parse observation time; using current UTC", "obs_time_utc", obs.ObsTimeUtc)
		obsTime = time.Now().UTC()
	}

	measurements := extractMeasurements(&obs, units)
	feelsLike := calculateFeelsLike(measurements, units)
	gustMS := normalizeWindGust(measurements.windGustRaw, units)
	precipMMH := getPrecipitationRate(&obs)

	iconCode := InferWundergroundIcon(
		measurements.temp, precipMMH, float64(obs.Humidity), obs.SolarRadiation, gustMS,
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

// getPrecipitationRate extracts precipitation rate in mm/h
func getPrecipitationRate(obs *struct {
	StationID      string  `json:"stationID"`
	ObsTimeUtc     string  `json:"obsTimeUtc"`
	ObsTimeLocal   string  `json:"obsTimeLocal"`
	Neighborhood   string  `json:"neighborhood"`
	SoftwareType   string  `json:"softwareType"`
	Country        string  `json:"country"`
	SolarRadiation float64 `json:"solarRadiation"`
	Lon            float64 `json:"lon"`
	Lat            float64 `json:"lat"`
	Uv             float64 `json:"uv"`
	Winddir        int     `json:"winddir"`
	Humidity       float64 `json:"humidity"`
	QcStatus       int     `json:"qcStatus"`
	Imperial       struct {
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
	} `json:"imperial"`
	Metric struct {
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
	} `json:"metric"`
}) float64 {
	switch {
	case obs.Metric.PrecipRate > 0:
		return obs.Metric.PrecipRate
	case obs.Imperial.PrecipRate > 0:
		return obs.Imperial.PrecipRate * InchesToMm
	default:
		return 0.0
	}
}

// isInvalid checks if a float64 value is NaN (invalid for HeatIndex/WindChill logic)
// Note: WU may use 0 for N/A but we treat only NaN as invalid to avoid dropping legitimate zero values
func isInvalid(val float64) bool {
	return math.IsNaN(val)
}

// weatherMeasurements holds extracted weather data from API response
type weatherMeasurements struct {
	temp         float64
	heatIndex    float64
	windChill    float64
	windSpeed    float64
	windGust     float64
	windGustRaw  float64
	windSpeedRaw float64 // Raw wind speed in original units for feels-like calculation
	pressure     float64
}

// extractMeasurements extracts weather measurements based on units configuration.
// IMPORTANT: All temperatures (temp, heatIndex, windChill) are ALWAYS returned in Celsius
// regardless of the API units setting. This ensures consistent storage and display.
func extractMeasurements(obs *struct {
	StationID      string  `json:"stationID"`
	ObsTimeUtc     string  `json:"obsTimeUtc"`
	ObsTimeLocal   string  `json:"obsTimeLocal"`
	Neighborhood   string  `json:"neighborhood"`
	SoftwareType   string  `json:"softwareType"`
	Country        string  `json:"country"`
	SolarRadiation float64 `json:"solarRadiation"`
	Lon            float64 `json:"lon"`
	Lat            float64 `json:"lat"`
	Uv             float64 `json:"uv"`
	Winddir        int     `json:"winddir"`
	Humidity       float64 `json:"humidity"`
	QcStatus       int     `json:"qcStatus"`
	Imperial struct {
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
	} `json:"imperial"`
	Metric struct {
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
	} `json:"metric"`
}, units string) weatherMeasurements {
	m := weatherMeasurements{}

	// Always use Metric struct for temperature values (already in Celsius)
	// This ensures consistent storage regardless of API units setting.
	// Wind speed and pressure handling varies by units for optimal API data usage.
	m.temp = obs.Metric.Temp
	m.heatIndex = obs.Metric.HeatIndex
	m.windChill = obs.Metric.WindChill

	switch units {
	case "m":
		// Metric units: km/h wind, hPa pressure
		m.windSpeed = obs.Metric.WindSpeed * KmhToMs
		m.windGust = obs.Metric.WindGust * KmhToMs
		m.windGustRaw = obs.Metric.WindGust
		m.pressure = obs.Metric.Pressure
	case "h":
		// Hybrid (UK): Celsius temp (already set above), mph wind
		m.windSpeed = obs.Imperial.WindSpeed * MphToMs
		m.windGust = obs.Imperial.WindGust * MphToMs
		m.windGustRaw = obs.Imperial.WindGust
		m.pressure = obs.Metric.Pressure
	default:
		// Imperial units (default): Use imperial wind/pressure but metric temp
		m.windSpeed = obs.Imperial.WindSpeed * MphToMs
		m.windGust = obs.Imperial.WindGust * MphToMs
		m.windSpeedRaw = obs.Imperial.WindSpeed
		m.windGustRaw = obs.Imperial.WindGust
		m.pressure = obs.Imperial.Pressure * InHgToHPa
	}

	return m
}

// calculateFeelsLike calculates the feels-like temperature based on conditions.
// NOTE: All temperatures (temp, heatIndex, windChill) are in Celsius.
// The units parameter only affects wind speed threshold comparison.
func calculateFeelsLike(m weatherMeasurements, units string) float64 {
	// All temperature thresholds are in Celsius since extractMeasurements
	// now always returns temperatures in Celsius.
	// Hot threshold: >=27°C, Cold threshold: <=10°C

	// Determine wind threshold based on API units (for threshold comparison only)
	var windExceedsThreshold bool
	switch units {
	case "m":
		// Metric: wind threshold 4.8 km/h = 1.333 m/s
		windExceedsThreshold = m.windSpeed > MetricWindThresholdMs
	case "h":
		// Hybrid UK: wind threshold 3 mph = 1.34112 m/s
		windExceedsThreshold = m.windSpeed > HybridWindThresholdMs
	default:
		// Imperial: use raw wind speed (mph) for threshold comparison
		windExceedsThreshold = m.windSpeedRaw > ImperialWindThresholdMph
	}

	switch {
	case m.temp >= MetricHotTempC && !isInvalid(m.heatIndex):
		return m.heatIndex
	case m.temp <= MetricColdTempC && windExceedsThreshold && !isInvalid(m.windChill):
		return m.windChill
	default:
		return m.temp
	}
}

// normalizeWindGust converts wind gust to m/s for icon inference
func normalizeWindGust(windGustRaw float64, units string) float64 {
	if math.IsNaN(windGustRaw) || windGustRaw == 0 {
		return 0.0
	}
	
	switch units {
	case "m":
		// WU metric wind is in km/h
		return windGustRaw * KmhToMs
	case "h", "e":
		// WU imperial wind (and hybrid uses imperial for wind) is in mph
		return windGustRaw * MphToMs
	default:
		return windGustRaw * MphToMs
	}
}
