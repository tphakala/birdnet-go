package weather

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	OpenWeatherRequestTimeout = 10 * time.Second
	OpenWeatherUserAgent      = "BirdNET-Go"
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
	if !settings.Realtime.OpenWeather.Enabled {
		return nil, fmt.Errorf("OpenWeather integration is disabled")
	}

	url := fmt.Sprintf("%s?lat=%f&lon=%f&appid=%s&units=%s&lang=%s",
		settings.Realtime.OpenWeather.Endpoint,
		settings.BirdNET.Latitude,
		settings.BirdNET.Longitude,
		settings.Realtime.OpenWeather.APIKey,
		settings.Realtime.OpenWeather.Units,
		settings.Realtime.OpenWeather.Language,
	)

	client := &http.Client{
		Timeout: OpenWeatherRequestTimeout,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("User-Agent", OpenWeatherUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching weather data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-200 response: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	var weatherData OpenWeatherResponse
	if err := json.Unmarshal(body, &weatherData); err != nil {
		return nil, fmt.Errorf("error unmarshaling weather data: %w", err)
	}

	return &WeatherData{
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
		Icon:        weatherData.Weather[0].Icon,
	}, nil
}
