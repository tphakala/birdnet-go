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
	KmhToMs              = 0.277778      // Convert km/h to m/s (divide by 3.6)
	MphToMs              = 0.44704       // Convert mph to m/s
	InHgToHPa            = 33.8638866667 // Convert inches of mercury to hectopascals
	
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

// InferWundergroundIcon infers a standardized icon code from Wunderground measurements.
// windGustMS must be provided in m/s.
func InferWundergroundIcon(tempC, precipMM, humidity, solarRadiation, windGustMS float64) IconCode {
	// 1. Thunderstorm: heavy rain + high wind gust (gust normalized to m/s)
	if precipMM > ThunderstormPrecipMM && windGustMS > ThunderstormGustMS {
		return IconThunderstorm
	}
	// 2. Precipitation type: snow vs rain (use temp)
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

	// 4. Night handling: when solar radiation is near zero, infer clouds by humidity
	if solarRadiation <= NightSolarRadiationThreshold {
		if humidity >= NightCloudyHumidityPercent {
			return IconCloudy
		}
		if humidity >= NightPartlyCloudyHumidityPercent {
			return IconPartlyCloudy
		}
		return IconClearSky
	}

	// 5. Daytime cloud cover: estimate from solarRadiation
	if solarRadiation > DayClearSRThreshold {
		return IconClearSky
	}
	if solarRadiation >= DayPartlyCloudyLowerSR && solarRadiation <= DayPartlyCloudyUpperSR {
		return IconPartlyCloudy
	}
	if solarRadiation < DayPartlyCloudyLowerSR {
		return IconCloudy
	}
	return IconFair
}

type WundergroundResponse struct {
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

func (p *WundergroundProvider) FetchWeather(settings *conf.Settings) (*WeatherData, error) {
	apiKey := settings.Realtime.Weather.Wunderground.APIKey
	stationId := settings.Realtime.Weather.Wunderground.StationID
	units := settings.Realtime.Weather.Wunderground.Units
	endpoint := settings.Realtime.Weather.Wunderground.Endpoint
	if endpoint == "" {
		endpoint = wundergroundBaseURL
	}
	if units == "" {
		units = "e"
	}
	// Only allow supported units: "e" (English), "m" (Metric), "h" (Hybrid UK)
	if units != "e" && units != "m" && units != "h" {
		units = "e"
	}
	if apiKey == "" || stationId == "" {
		return nil, errors.New(fmt.Errorf("wunderground API key or station ID not configured")).
			Component("weather").
			Category(errors.CategoryConfiguration).
			Context("provider", wundergroundProviderName).
			Build()
	}

	// Validate stationId format: 3-32 chars, letters, digits, hyphen, underscore
	if !stationIDRegex.MatchString(stationId) {
		return nil, errors.New(fmt.Errorf("invalid wunderground station ID format")).
			Component("weather").
			Category(errors.CategoryConfiguration).
			Context("provider", wundergroundProviderName).
			Build()
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New(fmt.Errorf("invalid wunderground endpoint: %w", err)).
			Component("weather").
			Category(errors.CategoryConfiguration).
			Context("provider", wundergroundProviderName).
			Build()
	}
	q := u.Query()
	q.Set("stationId", stationId)
	q.Set("format", "json")
	q.Set("units", units)
	q.Set("apiKey", apiKey)
	q.Set("numericPrecision", "decimal")
	u.RawQuery = q.Encode()

	apiURL := u.String()

	logger := weatherLogger.With("provider", wundergroundProviderName)
	logger.Info("Fetching weather data", "url", maskAPIKey(apiURL, "apiKey"))

	ctx := context.Background()
	client := &http.Client{Timeout: RequestTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, http.NoBody)
	if err != nil {
		logger.Error("Failed to create HTTP request", "url", maskAPIKey(apiURL, "apiKey"), "error", err)
		return nil, errors.New(err).
			Component("weather").
			Category(errors.CategoryNetwork).
			Context("operation", "create_http_request").
			Context("provider", wundergroundProviderName).
			Build()
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		logger.Error("HTTP request failed", "error", err)
		return nil, errors.New(err).
			Component("weather").
			Category(errors.CategoryNetwork).
			Context("operation", "weather_api_request").
			Context("provider", wundergroundProviderName).
			Build()
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logger.Warn("Failed to close response body", "error", cerr, "operation", "weather_api_close", "provider", wundergroundProviderName)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Error("Received non-OK status code", "status_code", resp.StatusCode, "response_body", string(bodyBytes))
		return nil, errors.New(fmt.Errorf("received non-200 response (%d)", resp.StatusCode)).
			Component("weather").
			Category(errors.CategoryNetwork).
			Context("operation", "weather_api_response").
			Context("provider", wundergroundProviderName).
			Context("status_code", fmt.Sprintf("%d", resp.StatusCode)).
			Build()
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read response body", "error", err)
		return nil, errors.New(err).
			Component("weather").
			Category(errors.CategoryNetwork).
			Context("operation", "read_response_body").
			Context("provider", wundergroundProviderName).
			Build()
	}

	var wuResp WundergroundResponse
	if err := json.Unmarshal(body, &wuResp); err != nil {
		logger.Error("Failed to unmarshal response JSON", "error", err, "response_body", string(body))
		return nil, errors.New(err).
			Component("weather").
			Category(errors.CategoryValidation).
			Context("operation", "unmarshal_weather_data").
			Context("provider", wundergroundProviderName).
			Build()
	}

	if len(wuResp.Observations) == 0 {
		logger.Error("API response contained no observations")
		return nil, errors.New(fmt.Errorf("no observations returned from API")).
			Component("weather").
			Category(errors.CategoryValidation).
			Context("operation", "validate_weather_response").
			Context("provider", wundergroundProviderName).
			Build()
	}

	obs := wuResp.Observations[0]
	obsTime, err := time.Parse(time.RFC3339, obs.ObsTimeUtc)
	if err != nil {
		logger.Warn("Failed to parse observation time; using current UTC", "obs_time_utc", obs.ObsTimeUtc, "error", err, "station", obs.StationID, "index", 0)
		obsTime = time.Now().UTC()
	}

	// Extract weather measurements based on units
	measurements := extractMeasurements(&obs, units)
	
	// Calculate feels-like temperature
	feelsLike := calculateFeelsLike(measurements, units)

	// Normalize wind gust to m/s for icon inference
	gustMS := normalizeWindGust(measurements.windGustRaw, units)

	// Calculate precipitation rate in mm/h for icon inference
	var precipMMH float64
	switch {
	case obs.Metric.PrecipRate > 0:
		precipMMH = obs.Metric.PrecipRate
	case obs.Imperial.PrecipRate > 0:
		// Convert inches/hour to mm/hour (1 inch = 25.4 mm)
		precipMMH = obs.Imperial.PrecipRate * 25.4
	default:
		precipMMH = 0.0
	}

	iconCode := InferWundergroundIcon(
		measurements.temp,
		precipMMH,
		float64(obs.Humidity),
		obs.SolarRadiation,
		gustMS,
	)
	mappedData := &WeatherData{
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
			Min:       measurements.temp, // Not provided, fallback to current
			Max:       measurements.temp, // Not provided, fallback to current
		},
		Wind: Wind{
			Speed: measurements.windSpeed,
			Deg:   obs.Winddir,
			Gust:  measurements.windGust,
		},
		Clouds:      0, // Not provided
		Visibility:  0, // Not provided
		Pressure:    int(math.Round(measurements.pressure)),
		Humidity:    int(math.Round(obs.Humidity)),
		Description: IconDescription[iconCode],
		Icon:        string(iconCode),
	}

	logger.Debug("Mapped API response to WeatherData structure", "station", obs.StationID, "temp", mappedData.Temperature.Current)
	return mappedData, nil
}

// isInvalid checks if a float64 value is zero or NaN (invalid for HeatIndex/WindChill logic)
func isInvalid(val float64) bool {
	return val == 0 || math.IsNaN(val)
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

// extractMeasurements extracts weather measurements based on units configuration
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
	
	switch units {
	case "m":
		// Metric units
		m.temp = obs.Metric.Temp
		m.heatIndex = obs.Metric.HeatIndex
		m.windChill = obs.Metric.WindChill
		// Convert km/h -> m/s for WeatherData
		m.windSpeed = obs.Metric.WindSpeed * KmhToMs
		m.windGust = obs.Metric.WindGust * KmhToMs
		// Keep raw gust (km/h) for icon inference conversion later
		m.windGustRaw = obs.Metric.WindGust
		m.pressure = obs.Metric.Pressure
	case "h":
		// Hybrid (UK): use Metric for temp, Imperial wind converted to m/s
		m.temp = obs.Metric.Temp
		m.heatIndex = obs.Metric.HeatIndex
		m.windChill = obs.Metric.WindChill
		// Convert mph -> m/s for WeatherData
		m.windSpeed = obs.Imperial.WindSpeed * MphToMs
		m.windGust = obs.Imperial.WindGust * MphToMs
		// Keep raw gust (mph) for icon inference conversion later
		m.windGustRaw = obs.Imperial.WindGust
		m.pressure = obs.Metric.Pressure
	default:
		// Imperial units (default)
		m.temp = obs.Imperial.Temp
		m.heatIndex = obs.Imperial.HeatIndex
		m.windChill = obs.Imperial.WindChill
		// Convert mph -> m/s for WeatherData consistency
		m.windSpeed = obs.Imperial.WindSpeed * MphToMs
		m.windGust = obs.Imperial.WindGust * MphToMs
		// Keep raw values (mph) for feels-like calculation and icon inference
		m.windSpeedRaw = obs.Imperial.WindSpeed
		m.windGustRaw = obs.Imperial.WindGust
		m.pressure = obs.Imperial.Pressure * InHgToHPa
	}
	
	return m
}

// calculateFeelsLike calculates the feels-like temperature based on conditions
func calculateFeelsLike(m weatherMeasurements, units string) float64 {
	switch units {
	case "m":
		// Thresholds in metric: hot >=27°C, cold <=10°C, wind >1.333 m/s (4.8 km/h)
		switch {
		case m.temp >= MetricHotTempC && !isInvalid(m.heatIndex):
			return m.heatIndex
		case m.temp <= MetricColdTempC && m.windSpeed > MetricWindThresholdMs && !isInvalid(m.windChill):
			return m.windChill
		default:
			return m.temp
		}
	case "h":
		// Use metric temp thresholds, wind threshold 3 mph => 1.34112 m/s
		switch {
		case m.temp >= MetricHotTempC && !isInvalid(m.heatIndex):
			return m.heatIndex
		case m.temp <= MetricColdTempC && m.windSpeed > HybridWindThresholdMs && !isInvalid(m.windChill):
			return m.windChill
		default:
			return m.temp
		}
	default:
		// Thresholds in imperial: hot >=80°F, cold <=50°F, wind >3 mph
		// Use raw wind speed (mph) for threshold comparison
		switch {
		case m.temp >= ImperialHotTempF && !isInvalid(m.heatIndex):
			return m.heatIndex
		case m.temp <= ImperialColdTempF && m.windSpeedRaw > ImperialWindThresholdMph && !isInvalid(m.windChill):
			return m.windChill
		default:
			return m.temp
		}
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
