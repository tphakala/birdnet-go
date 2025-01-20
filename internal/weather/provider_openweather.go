package weather

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
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
	if settings.Realtime.Weather.OpenWeather.APIKey == "" {
		return nil, fmt.Errorf("OpenWeather API key not configured")
	}

	url := fmt.Sprintf("%s?lat=%.3f&lon=%.3f&appid=%s&units=%s&lang=en",
		settings.Realtime.Weather.OpenWeather.Endpoint,
		settings.BirdNET.Latitude,
		settings.BirdNET.Longitude,
		settings.Realtime.Weather.OpenWeather.APIKey,
		settings.Realtime.Weather.OpenWeather.Units,
	)

	client := &http.Client{
		Timeout: RequestTimeout,
	}

	req, err := http.NewRequest("GET", url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("User-Agent", UserAgent)

	var weatherData OpenWeatherResponse
	for i := 0; i < MaxRetries; i++ {
		resp, err := client.Do(req)
		if err != nil {
			if i == MaxRetries-1 {
				return nil, fmt.Errorf("error fetching weather data: %w", err)
			}
			time.Sleep(RetryDelay)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if i == MaxRetries-1 {
				return nil, fmt.Errorf("received non-200 response: %d", resp.StatusCode)
			}
			time.Sleep(RetryDelay)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("error reading response body: %w", err)
		}

		if err := json.Unmarshal(body, &weatherData); err != nil {
			return nil, fmt.Errorf("error unmarshaling weather data: %w", err)
		}

		break
	}

	// Safety check for weather data
	if len(weatherData.Weather) == 0 {
		return nil, fmt.Errorf("no weather conditions returned from API")
	}

	return &WeatherData{
		Time: time.Unix(weatherData.Dt, 0),
		Location: Location{
			Latitude:  weatherData.Coord.Lat,
			Longitude: weatherData.Coord.Lon,
		},
		Temperature: Temperature{
			Current: weatherData.Main.Temp,
		},
		Wind: Wind{
			Speed: weatherData.Wind.Speed,
			Deg:   weatherData.Wind.Deg,
		},
		Clouds:      weatherData.Clouds.All,
		Pressure:    weatherData.Main.Pressure,
		Humidity:    weatherData.Main.Humidity,
		Description: weatherData.Weather[0].Description,
		Icon:        string(GetStandardIconCode(weatherData.Weather[0].Icon, "openweather")),
	}, nil
}
