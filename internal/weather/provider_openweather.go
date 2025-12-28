package weather

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
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
		return nil, newWeatherError(
			fmt.Errorf("OpenWeather API key not configured"),
			errors.CategoryConfiguration,
			"validate_config",
			openWeatherProviderName,
		)
	}

	apiURL := fmt.Sprintf("%s?lat=%.3f&lon=%.3f&appid=%s&units=%s&lang=en",
		settings.Realtime.Weather.OpenWeather.Endpoint,
		settings.BirdNET.Latitude,
		settings.BirdNET.Longitude,
		apiKey,
		settings.Realtime.Weather.OpenWeather.Units,
	)

	providerLogger := getLogger().With(logger.String("provider", openWeatherProviderName))
	providerLogger.Info("Fetching weather data", logger.String("url", maskAPIKey(apiURL, "appid")))

	req, err := http.NewRequest("GET", apiURL, http.NoBody)
	if err != nil {
		return nil, newWeatherError(err, errors.CategoryNetwork, "create_http_request", openWeatherProviderName)
	}
	req.Header.Set("User-Agent", UserAgent)

	// Execute request with retry
	body, err := executeOpenWeatherRequest(req, providerLogger)
	if err != nil {
		return nil, err
	}

	// Parse response
	var weatherData OpenWeatherResponse
	if err := json.Unmarshal(body, &weatherData); err != nil {
		return nil, newWeatherError(err, errors.CategoryValidation, "unmarshal_weather_data", openWeatherProviderName)
	}

	if len(weatherData.Weather) == 0 {
		return nil, newWeatherError(
			fmt.Errorf("no weather conditions returned from API"),
			errors.CategoryValidation,
			"validate_weather_response",
			openWeatherProviderName,
		)
	}

	providerLogger.Info("Successfully received and parsed weather data")
	mappedData := mapOpenWeatherResponse(&weatherData, settings)
	providerLogger.Debug("Mapped API response to WeatherData structure",
		logger.String("city", mappedData.Location.City),
		logger.Float64("temp", mappedData.Temperature.Current))
	return mappedData, nil
}

// executeOpenWeatherRequest executes HTTP request with retry logic
func executeOpenWeatherRequest(req *http.Request, log logger.Logger) ([]byte, error) {
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
				return nil, newWeatherErrorWithRetries(err, errors.CategoryNetwork, "weather_api_request", openWeatherProviderName)
			}
			time.Sleep(RetryDelay)
			continue
		}

		attemptLogger.Debug("Received HTTP response", logger.Int("status_code", resp.StatusCode))

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			attemptLogger.Warn("Received non-OK status code", logger.Int("status_code", resp.StatusCode))
			if isLastAttempt {
				return nil, newWeatherErrorWithRetries(
					fmt.Errorf("received non-200 response (%d)", resp.StatusCode),
					errors.CategoryNetwork,
					"weather_api_response",
					openWeatherProviderName,
				)
			}
			_ = bodyBytes // Suppress unused warning
			time.Sleep(RetryDelay)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, newWeatherError(err, errors.CategoryNetwork, "read_response_body", openWeatherProviderName)
		}
		return body, nil
	}

	return nil, newWeatherErrorWithRetries(
		fmt.Errorf("max retries exceeded"),
		errors.CategoryNetwork,
		"weather_api_request",
		openWeatherProviderName,
	)
}

// mapOpenWeatherResponse converts OpenWeatherResponse to WeatherData
func mapOpenWeatherResponse(data *OpenWeatherResponse, settings *conf.Settings) *WeatherData {
	temp, feelsLike, tempMin, tempMax := convertOpenWeatherTemps(
		data.Main.Temp,
		data.Main.FeelsLike,
		data.Main.TempMin,
		data.Main.TempMax,
		settings.Realtime.Weather.OpenWeather.Units,
	)

	return &WeatherData{
		Time: time.Unix(data.Dt, 0),
		Location: Location{
			Latitude:  data.Coord.Lat,
			Longitude: data.Coord.Lon,
			Country:   data.Sys.Country,
			City:      data.Name,
		},
		Temperature: Temperature{
			Current:   temp,
			FeelsLike: feelsLike,
			Min:       tempMin,
			Max:       tempMax,
		},
		Wind: Wind{
			Speed: data.Wind.Speed,
			Deg:   data.Wind.Deg,
			Gust:  data.Wind.Gust,
		},
		Clouds:      data.Clouds.All,
		Visibility:  data.Visibility,
		Pressure:    data.Main.Pressure,
		Humidity:    data.Main.Humidity,
		Description: data.Weather[0].Description,
		Icon:        string(GetStandardIconCode(data.Weather[0].Icon, openWeatherProviderName)),
	}
}

// convertOpenWeatherTemps converts OpenWeather temperatures to Celsius.
// OpenWeather unit systems:
// - "metric": Already Celsius, no conversion needed
// - "imperial": Fahrenheit, convert to Celsius
// - "standard": Kelvin, convert to Celsius
func convertOpenWeatherTemps(temp, feelsLike, tempMin, tempMax float64, units string) (tempC, feelsLikeC, tempMinC, tempMaxC float64) {
	switch units {
	case "imperial":
		// Fahrenheit to Celsius
		return FahrenheitToCelsius(temp),
			FahrenheitToCelsius(feelsLike),
			FahrenheitToCelsius(tempMin),
			FahrenheitToCelsius(tempMax)
	case "standard":
		// Kelvin to Celsius
		return KelvinToCelsius(temp),
			KelvinToCelsius(feelsLike),
			KelvinToCelsius(tempMin),
			KelvinToCelsius(tempMax)
	default:
		// metric: already Celsius
		return temp, feelsLike, tempMin, tempMax
	}
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
