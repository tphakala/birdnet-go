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
	YrNoBaseURL    = "https://api.met.no/weatherapi/locationforecast/2.0/complete"
	YrNoUserAgent  = "BirdNET-Go https://github.com/tphakala/birdnet-go"
	YrNoMaxRetries = 3
	YrNoRetryDelay = 2 * time.Second
)

// YrResponse represents the structure of the Yr.no API response
type YrResponse struct {
	Properties struct {
		Timeseries []struct {
			Time time.Time `json:"time"`
			Data struct {
				Instant struct {
					Details struct {
						AirPressure    float64 `json:"air_pressure_at_sea_level"`
						AirTemperature float64 `json:"air_temperature"`
						CloudArea      float64 `json:"cloud_area_fraction"`
						RelHumidity    float64 `json:"relative_humidity"`
						WindSpeed      float64 `json:"wind_speed"`
						WindDirection  float64 `json:"wind_from_direction"`
					} `json:"details"`
				} `json:"instant"`
				Next1Hours struct {
					Summary struct {
						SymbolCode string `json:"symbol_code"`
					} `json:"summary"`
					Details struct {
						PrecipitationAmount float64 `json:"precipitation_amount"`
					} `json:"details"`
				} `json:"next_1_hours"`
			} `json:"data"`
		} `json:"timeseries"`
	} `json:"properties"`
}

// FetchWeather implements the Provider interface for YrNoProvider
func (p *YrNoProvider) FetchWeather(settings *conf.Settings) (*WeatherData, error) {
	if !settings.Realtime.Weather.YrNo.Enabled {
		return nil, fmt.Errorf("Yr.no integration is disabled")
	}

	url := fmt.Sprintf("%s?lat=%.6f&lon=%.6f", YrNoBaseURL,
		settings.BirdNET.Latitude,
		settings.BirdNET.Longitude)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("User-Agent", YrNoUserAgent)

	var response YrResponse
	for i := 0; i < YrNoMaxRetries; i++ {
		resp, err := client.Do(req)
		if err != nil {
			if i == YrNoMaxRetries-1 {
				return nil, fmt.Errorf("error fetching weather data: %w", err)
			}
			time.Sleep(YrNoRetryDelay)
			continue
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading response body: %w", err)
		}

		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("error unmarshaling weather data: %w", err)
		}

		break
	}

	if len(response.Properties.Timeseries) == 0 {
		return nil, fmt.Errorf("no weather data available")
	}

	current := response.Properties.Timeseries[0]

	return &WeatherData{
		Time: current.Time,
		Location: Location{
			Latitude:  settings.BirdNET.Latitude,
			Longitude: settings.BirdNET.Longitude,
		},
		Temperature: Temperature{
			Current: current.Data.Instant.Details.AirTemperature,
		},
		Wind: Wind{
			Speed: current.Data.Instant.Details.WindSpeed,
			Deg:   int(current.Data.Instant.Details.WindDirection),
		},
		Precipitation: Precipitation{
			Amount: current.Data.Next1Hours.Details.PrecipitationAmount,
		},
		Clouds:      int(current.Data.Instant.Details.CloudArea),
		Pressure:    int(current.Data.Instant.Details.AirPressure),
		Humidity:    int(current.Data.Instant.Details.RelHumidity),
		Description: current.Data.Next1Hours.Summary.SymbolCode,
	}, nil
}
