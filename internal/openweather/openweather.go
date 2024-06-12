package openweather

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// WeatherData represents the structure of weather data returned by the OpenWeather API
type WeatherData struct {
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
	Base string `json:"base"`
	Main struct {
		Temp      float64 `json:"temp"`
		FeelsLike float64 `json:"feels_like"`
		TempMin   float64 `json:"temp_min"`
		TempMax   float64 `json:"temp_max"`
		Pressure  int     `json:"pressure"`
		Humidity  int     `json:"humidity"`
		SeaLevel  int     `json:"sea_level"`
		GrndLevel int     `json:"grnd_level"`
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
		Type    int    `json:"type"`
		ID      int    `json:"id"`
		Country string `json:"country"`
		Sunrise int64  `json:"sunrise"`
		Sunset  int64  `json:"sunset"`
	} `json:"sys"`
	Timezone int    `json:"timezone"`
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Cod      int    `json:"cod"`
}

// FetchWeather fetches weather data from the OpenWeather API
func FetchWeather(settings *conf.Settings) (*WeatherData, error) {
	if !settings.Realtime.OpenWeather.Enabled {
		return nil, fmt.Errorf("OpenWeather integration is disabled")
	}

	// Construct the URL for the OpenWeather API requests
	url := fmt.Sprintf("%s?lat=%f&lon=%f&appid=%s&units=%s&lang=%s",
		settings.Realtime.OpenWeather.Endpoint,
		settings.BirdNET.Latitude,
		settings.BirdNET.Longitude,
		settings.Realtime.OpenWeather.APIKey,
		settings.Realtime.OpenWeather.Units,
		settings.Realtime.OpenWeather.Language,
	)

	// Fetch weather data from the OpenWeather API
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("error fetching weather data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-200 response: %d", resp.StatusCode)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	// Unmarshal the JSON response into the WeatherData struct
	var weatherData WeatherData
	if err := json.Unmarshal(body, &weatherData); err != nil {
		return nil, fmt.Errorf("error unmarshaling weather data: %w", err)
	}

	return &weatherData, nil
}

// SaveWeatherData saves the fetched weather data into the database
func SaveWeatherData(db datastore.Interface, weatherData *WeatherData) error {
	dailyEvents := &datastore.DailyEvents{
		Date:     time.Unix(weatherData.Dt, 0).Format("2006-01-02"),
		Sunrise:  weatherData.Sys.Sunrise,
		Sunset:   weatherData.Sys.Sunset,
		Country:  weatherData.Sys.Country,
		CityName: weatherData.Name,
	}

	// Save daily events data
	if err := db.SaveDailyEvents(dailyEvents); err != nil {
		return fmt.Errorf("failed to save daily events: %w", err)
	}

	// Create hourly weather data
	hourlyWeather := &datastore.HourlyWeather{
		DailyEventsID: dailyEvents.ID,
		Time:          time.Unix(weatherData.Dt, 0),
		Temperature:   weatherData.Main.Temp,
		FeelsLike:     weatherData.Main.FeelsLike,
		TempMin:       weatherData.Main.TempMin,
		TempMax:       weatherData.Main.TempMax,
		Pressure:      weatherData.Main.Pressure,
		Humidity:      weatherData.Main.Humidity,
		Visibility:    weatherData.Visibility,
		WindSpeed:     weatherData.Wind.Speed,
		WindDeg:       weatherData.Wind.Deg,
		WindGust:      weatherData.Wind.Gust,
		Clouds:        weatherData.Clouds.All,
		WeatherMain:   weatherData.Weather[0].Main,
		WeatherDesc:   weatherData.Weather[0].Description,
		WeatherIcon:   weatherData.Weather[0].Icon,
	}

	// Save hourly weather data
	if err := db.SaveHourlyWeather(hourlyWeather); err != nil {
		return fmt.Errorf("failed to save hourly weather: %w", err)
	}

	return nil
}

// StartWeatherPolling starts a ticker to fetch weather data at the configured interval
func StartWeatherPolling(settings *conf.Settings, db datastore.Interface, stopChan <-chan struct{}) {
	ticker := time.NewTicker(time.Duration(settings.Realtime.OpenWeather.Interval) * time.Minute)
	defer ticker.Stop()

	log.Printf("Starting weather polling every %d minutes", settings.Realtime.OpenWeather.Interval)

	for {
		select {
		case <-ticker.C:
			weatherData, err := FetchWeather(settings)
			if err != nil {
				fmt.Printf("Error fetching weather data: %v\n", err)
				continue
			}

			// Save fetched weather data to the database
			if err := SaveWeatherData(db, weatherData); err != nil {
				fmt.Printf("Error saving weather data: %v\n", err)
				continue
			}

			// Log the successful fetch and save
			if settings.Realtime.OpenWeather.Debug {
				fmt.Printf("Fetched and saved weather data: %v\n", weatherData)
			}

		case <-stopChan:
			return
		}
	}
}
