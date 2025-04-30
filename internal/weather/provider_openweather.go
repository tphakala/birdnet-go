package weather

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	openWeatherBaseURL      = "https://api.openweathermap.org/data/2.5/weather"
	openWeatherProviderName = "openweather"
)

// OpenWeatherResponse represents the structure of weather data returned by the OpenWeather API
type OpenWeatherResponse struct {
	Coord struct {
		Lon float64 `json:"lon"`
		Lat float64 `json:"lat"`
	} `json:"coord"`
	Weather []struct {
		ID          int    `json:"id"`
		Main        string `json:"main"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
	} `json:"weather"`
	Main struct {
		Temp      float64 `json:"temp"`
		FeelsLike float64 `json:"feels_like"`
		TempMin   float64 `json:"temp_min"`
		TempMax   float64 `json:"temp_max"`
		Pressure  int     `json:"pressure"`
		Humidity  int     `json:"humidity"`
	} `json:"main"`
	Visibility int `json:"visibility"`
	Wind       struct {
		Speed float64 `json:"speed"`
		Deg   int     `json:"deg"`
		Gust  float64 `json:"gust"`
	} `json:"wind"`
	Clouds struct {
		All int `json:"all"`
	} `json:"clouds"`
	Dt  int64 `json:"dt"`
	Sys struct {
		Country string `json:"country"`
		Sunrise int64  `json:"sunrise"`
		Sunset  int64  `json:"sunset"`
	} `json:"sys"`
	Name string `json:"name"`
}

// FetchWeather implements the Provider interface for OpenWeatherProvider
func (p *OpenWeatherProvider) FetchWeather(settings *conf.Settings) (*WeatherData, error) {
	apiKey := settings.Realtime.Weather.OpenWeather.APIKey
	if apiKey == "" {
		weatherLogger.Error("OpenWeather API key is missing", "provider", openWeatherProviderName)
		return nil, fmt.Errorf("OpenWeather API key not configured")
	}

	apiURL := fmt.Sprintf("%s?lat=%.3f&lon=%.3f&appid=%s&units=%s&lang=en",
		settings.Realtime.Weather.OpenWeather.Endpoint,
		settings.BirdNET.Latitude,
		settings.BirdNET.Longitude,
		apiKey,
		settings.Realtime.Weather.OpenWeather.Units,
	)

	safeURL := maskAPIKey(apiURL, "appid")
	logger := weatherLogger.With("provider", openWeatherProviderName)
	logger.Info("Fetching weather data", "url", safeURL)

	client := &http.Client{
		Timeout: RequestTimeout,
	}

	req, err := http.NewRequest("GET", apiURL, http.NoBody)
	if err != nil {
		logger.Error("Failed to create HTTP request", "url", safeURL, "error", err)
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("User-Agent", UserAgent)

	var weatherData OpenWeatherResponse
	var resp *http.Response

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
			continue
		}

		attemptLogger.Debug("Received HTTP response", "status_code", resp.StatusCode)

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			attemptLogger.Warn("Received non-OK status code", "status_code", resp.StatusCode, "response_body", string(bodyBytes))
			if i == MaxRetries-1 {
				logger.Error("Failed to fetch weather data due to non-OK status after max retries", "status_code", resp.StatusCode, "response_body", string(bodyBytes))
				return nil, fmt.Errorf("received non-200 response (%d) after %d retries", resp.StatusCode, MaxRetries)
			}
			time.Sleep(RetryDelay)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			logger.Error("Failed to read response body", "status_code", resp.StatusCode, "error", err)
			return nil, fmt.Errorf("error reading response body: %w", err)
		}

		if err := json.Unmarshal(body, &weatherData); err != nil {
			logger.Error("Failed to unmarshal response JSON", "status_code", resp.StatusCode, "error", err, "response_body", string(body))
			return nil, fmt.Errorf("error unmarshaling weather data: %w", err)
		}

		logger.Info("Successfully received and parsed weather data", "status_code", resp.StatusCode)
		break
	}

	if len(weatherData.Weather) == 0 {
		logger.Error("API response parsed successfully but contained no weather conditions")
		return nil, fmt.Errorf("no weather conditions returned from API")
	}

	mappedData := &WeatherData{
		Time: time.Unix(weatherData.Dt, 0),
		Location: Location{
			Latitude:  weatherData.Coord.Lat,
			Longitude: weatherData.Coord.Lon,
			Country:   weatherData.Sys.Country,
			City:      weatherData.Name,
		},
		Temperature: Temperature{
			Current:   weatherData.Main.Temp,
			FeelsLike: weatherData.Main.FeelsLike,
			Min:       weatherData.Main.TempMin,
			Max:       weatherData.Main.TempMax,
		},
		Wind: Wind{
			Speed: weatherData.Wind.Speed,
			Deg:   weatherData.Wind.Deg,
			Gust:  weatherData.Wind.Gust,
		},
		Clouds:      weatherData.Clouds.All,
		Visibility:  weatherData.Visibility,
		Pressure:    weatherData.Main.Pressure,
		Humidity:    weatherData.Main.Humidity,
		Description: weatherData.Weather[0].Description,
		Icon:        string(GetStandardIconCode(weatherData.Weather[0].Icon, openWeatherProviderName)),
	}

	logger.Debug("Mapped API response to WeatherData structure", "city", mappedData.Location.City, "temp", mappedData.Temperature.Current)
	return mappedData, nil
}

func maskAPIKey(rawURL, keyParamName string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	queryParams := parsedURL.Query()
	if queryParams.Has(keyParamName) {
		queryParams.Set(keyParamName, "***MASKED***")
	}
	parsedURL.RawQuery = queryParams.Encode()
	return parsedURL.String()
}

func getPrimaryWeatherDescription(weather []struct {
	ID          int    `json:"id"`
	Main        string `json:"main"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}) string {
	if len(weather) > 0 {
		return weather[0].Description
	}
	return "N/A"
}

func getPrimaryWeatherIcon(weather []struct {
	ID          int    `json:"id"`
	Main        string `json:"main"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}, providerName string) string {
	if len(weather) > 0 {
		return string(GetStandardIconCode(weather[0].Icon, providerName))
	}
	return string(IconUnknown)
}
