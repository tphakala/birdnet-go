// provider_wunderground.go: WeatherUnderground integration for BirdNET-Go
package weather

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	wundergroundBaseURL      = "https://api.weather.com/v2/pws/observations/current"
	wundergroundProviderName = "wunderground"
)

// InferWundergroundIcon infers a standardized icon code from Wunderground measurements.
func InferWundergroundIcon(tempC, precipMM, windMS, humidity, solarRadiation float64) IconCode {
	switch {
	case precipMM > 0:
		if tempC < 0 {
			return IconSnow
		}
		return IconRain
	case windMS > 15:
		return IconThunderstorm
	case humidity > 90 && tempC < 5:
		return IconFog
	case solarRadiation > 600:
		return IconClearSky
	case solarRadiation >= 200 && solarRadiation <= 600:
		return IconPartlyCloudy
	case solarRadiation < 200:
		return IconCloudy
	default:
		return IconFair
	}
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
		Humidity       int     `json:"humidity"`
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

	apiURL := fmt.Sprintf("%s?stationId=%s&format=json&units=%s&apiKey=%s",
		endpoint, stationId, units, apiKey)

	logger := weatherLogger.With("provider", wundergroundProviderName)
	logger.Info("Fetching weather data", "url", maskAPIKey(apiURL, "apiKey"))

	client := &http.Client{Timeout: RequestTimeout}
	req, err := http.NewRequest("GET", apiURL, http.NoBody)
	if err != nil {
		logger.Error("Failed to create HTTP request", "url", apiURL, "error", err)
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
	defer resp.Body.Close()

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
	obsTime, _ := time.Parse(time.RFC3339, obs.ObsTimeUtc)
	var temp, feelsLike, windSpeed, windGust, pressure float64
	if units == "m" {
		temp = obs.Metric.Temp
		feelsLike = obs.Metric.HeatIndex
		windSpeed = obs.Metric.WindSpeed
		windGust = obs.Metric.WindGust
		pressure = obs.Metric.Pressure
	} else if units == "h" {
		// Hybrid (UK): use Metric for temp, Imperial for wind
		temp = obs.Metric.Temp
		feelsLike = obs.Metric.HeatIndex
		windSpeed = obs.Imperial.WindSpeed
		windGust = obs.Imperial.WindGust
		pressure = obs.Metric.Pressure
	} else {
		temp = obs.Imperial.Temp
		feelsLike = obs.Imperial.HeatIndex
		windSpeed = obs.Imperial.WindSpeed
		windGust = obs.Imperial.WindGust
		pressure = obs.Imperial.Pressure
	}

	iconCode := InferWundergroundIcon(
		temp,
		obs.Metric.PrecipRate,
		windSpeed,
		float64(obs.Humidity),
		obs.SolarRadiation,
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
		Pressure:    int(pressure),
		Humidity:    obs.Humidity,
		Description: IconDescription[iconCode],
		Icon:        string(iconCode),
	}

	logger.Debug("Mapped API response to WeatherData structure", "station", obs.StationID, "temp", mappedData.Temperature.Current)
	return mappedData, nil
}
