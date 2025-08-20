// provider_wunderground.go: WeatherUnderground integration for BirdNET-Go
package weather

import (
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
		if tempC < 0 {
			return IconSnow
		}
		return IconRain
	}

	// 3. Fog
	if humidity > 90 && tempC < 5 {
		return IconFog
	}

	// 4. Night handling: when solar radiation is near zero, infer clouds by humidity
	if solarRadiation <= NightSolarRadiationThreshold {
		if humidity >= 85 {
			return IconCloudy
		}
		if humidity >= 60 {
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
		return nil, errors.New(fmt.Errorf("Wunderground API key or Station ID not configured")).
			Component("weather").
			Category(errors.CategoryConfiguration).
			Context("provider", wundergroundProviderName).
			Build()
	}

	// Validate stationId format: 3-32 chars, letters, digits, hyphen, underscore
	if !stationIDRegex.MatchString(stationId) {
		return nil, errors.New(fmt.Errorf("invalid Wunderground Station ID format")).
			Component("weather").
			Category(errors.CategoryConfiguration).
			Context("provider", wundergroundProviderName).
			Build()
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New(fmt.Errorf("invalid Wunderground endpoint: %w", err)).
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

	client := &http.Client{Timeout: RequestTimeout}
	req, err := http.NewRequest("GET", apiURL, http.NoBody)
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
			logger.Error("Failed to close response body", "error", cerr, "operation", "weather_api_close", "provider", wundergroundProviderName)
			_ = errors.New(cerr).
				Component("weather").
				Category(errors.CategoryNetwork).
				Context("operation", "weather_api_close").
				Context("provider", wundergroundProviderName).
				Build()
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
	var temp, feelsLike, windSpeed, windGust, pressure float64
	var heatIndex, windChill float64
	// Keep a raw gust (provider units) for icon inference normalization
	var windGustRaw float64

	if units == "m" {
		temp = obs.Metric.Temp
		heatIndex = obs.Metric.HeatIndex
		windChill = obs.Metric.WindChill
		// Convert km/h -> m/s for WeatherData
		windSpeed = obs.Metric.WindSpeed / 3.6
		windGust = obs.Metric.WindGust / 3.6
		// Keep raw gust (km/h) for icon inference conversion later
		windGustRaw = obs.Metric.WindGust
		pressure = obs.Metric.Pressure
		// Thresholds in metric: hot >=27°C, cold <=10°C, wind >1.333 m/s (4.8 km/h)
		if temp >= 27 && !isInvalid(heatIndex) {
			feelsLike = heatIndex
		} else if temp <= 10 && windSpeed > 1.3333333333 && !isInvalid(windChill) {
			feelsLike = windChill
		} else {
			feelsLike = temp
		}
	} else if units == "h" {
		// Hybrid (UK): use Metric for temp, Imperial wind converted to m/s
		temp = obs.Metric.Temp
		heatIndex = obs.Metric.HeatIndex
		windChill = obs.Metric.WindChill
		// Convert mph -> m/s for WeatherData
		windSpeed = obs.Imperial.WindSpeed * 0.44704
		windGust = obs.Imperial.WindGust * 0.44704
		// Keep raw gust (mph) for icon inference conversion later
		windGustRaw = obs.Imperial.WindGust
		pressure = obs.Metric.Pressure
		// Use metric temp thresholds, wind threshold 3 mph => 1.34112 m/s
		if temp >= 27 && !isInvalid(heatIndex) {
			feelsLike = heatIndex
		} else if temp <= 10 && windSpeed > 1.34112 && !isInvalid(windChill) {
			feelsLike = windChill
		} else {
			feelsLike = temp
		}
	} else {
		temp = obs.Imperial.Temp
		heatIndex = obs.Imperial.HeatIndex
		windChill = obs.Imperial.WindChill
		windSpeed = obs.Imperial.WindSpeed
		windGust = obs.Imperial.WindGust
		// Keep raw gust (mph) for icon inference conversion later
		windGustRaw = obs.Imperial.WindGust
		pressure = obs.Imperial.Pressure * 33.8638866667
		// Thresholds in imperial: hot >=80°F, cold <=50°F, wind >3 mph
		if temp >= 80 && !isInvalid(heatIndex) {
			feelsLike = heatIndex
		} else if temp <= 50 && windSpeed > 3 && !isInvalid(windChill) {
			feelsLike = windChill
		} else {
			feelsLike = temp
		}
	}

	// Normalize wind gust to m/s for icon inference thresholds
	const (
		mphToMs = 0.44704
		kmhToMs = 0.277778
	)
	gustMS := 0.0
	if !math.IsNaN(windGustRaw) && windGustRaw != 0 {
		switch units {
		case "m":
			// WU metric wind is in km/h
			gustMS = windGustRaw * kmhToMs
		case "h", "e":
			// WU imperial wind (and hybrid uses imperial for wind) is in mph
			gustMS = windGustRaw * mphToMs
		default:
			gustMS = windGustRaw * mphToMs
		}
	}

	iconCode := InferWundergroundIcon(
		temp,
		obs.Metric.PrecipRate,
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
			Current:   temp,
			FeelsLike: feelsLike,
			Min:       temp, // Not provided, fallback to current
			Max:       temp, // Not provided, fallback to current
		},
		Wind: Wind{
			Speed: windSpeed,
			Deg:   obs.Winddir,
			Gust:  windGust,
		},
		Clouds:      0, // Not provided
		Visibility:  0, // Not provided
		Pressure:    int(math.Round(pressure)),
		Humidity:    int(math.Round(obs.Humidity)),
		Description: IconDescription[iconCode],
		Icon:        string(iconCode),
	}

	logger.Debug("Mapped API response to WeatherData structure", "station", obs.StationID, "temp", mappedData.Temperature.Current)
	return mappedData, nil
}

// isInvalid checks if a float64 value is zero or NaN (invalid for HeatIndex/WindChill logic)
func isInvalid(val float64) bool {
	return val == 0 || val != val
}
