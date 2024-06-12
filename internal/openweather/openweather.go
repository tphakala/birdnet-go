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
	var initialDelay time.Duration

	// Get the latest hourly weather entry from the database
	latestWeather, err := db.LatestHourlyWeather()
	if err != nil {
		if err.Error() == "record not found" {
			// If no records are found, poll immediately
			initialDelay = 0
		} else {
			// Log other errors and continue with immediate polling
			log.Printf("Error retrieving latest hourly weather: %v", err)
			initialDelay = 0
		}
	} else {
		// Calculate the time since the latest entry
		timeSinceLastEntry := time.Since(latestWeather.Time)
		// if previsous entry is older than poll interval then poll immediately
		if timeSinceLastEntry > time.Duration(settings.Realtime.OpenWeather.Interval) {
			// If the last entry is older than 1 hour, poll immediately
			initialDelay = 0
		} else {
			// Otherwise, delay until the next full hour
			initialDelay = time.Hour - timeSinceLastEntry
		}
	}

	log.Printf("Starting weather polling with initial delay of %v", initialDelay)

	// Create a ticker with the specified interval
	ticker := time.NewTicker(time.Duration(settings.Realtime.OpenWeather.Interval) * time.Minute)
	defer ticker.Stop()

	// Use a timer for the initial delay
	initialTimer := time.NewTimer(initialDelay)
	defer initialTimer.Stop()

	for {
		select {
		case <-initialTimer.C:
			// Perform the initial fetch and save
			if err := fetchAndSaveWeatherData(settings, db); err != nil {
				log.Printf("Error during initial weather fetch: %v", err)
			}

		case <-ticker.C:
			// Perform the scheduled fetch and save
			if err := fetchAndSaveWeatherData(settings, db); err != nil {
				log.Printf("Error during scheduled weather fetch: %v", err)
			}

		case <-stopChan:
			return
		}
	}
}

// fetchAndSaveWeatherData is a helper function to fetch and save weather data
func fetchAndSaveWeatherData(settings *conf.Settings, db datastore.Interface) error {
	weatherData, err := FetchWeather(settings)
	if err != nil {
		return fmt.Errorf("error fetching weather data: %w", err)
	}

	if err := SaveWeatherData(db, weatherData); err != nil {
		return fmt.Errorf("error saving weather data: %w", err)
	}

	// Log the successful fetch and save
	if settings.Realtime.OpenWeather.Debug {
		log.Printf("Fetched and saved weather data: %v", weatherData)
	}

	return nil
}
