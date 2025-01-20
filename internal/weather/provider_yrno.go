package weather

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	YrNoBaseURL = "https://api.met.no/weatherapi/locationforecast/2.0/complete"
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
	url := fmt.Sprintf("%s?lat=%.3f&lon=%.3f", YrNoBaseURL,
		settings.BirdNET.Latitude,
		settings.BirdNET.Longitude)

	client := &http.Client{
		Timeout: RequestTimeout,
	}

	req, err := http.NewRequest("GET", url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	if p.lastModified != "" {
		req.Header.Set("If-Modified-Since", p.lastModified)
	}

	var response YrResponse
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

		if resp.StatusCode == http.StatusNotModified {
			resp.Body.Close()
			return nil, fmt.Errorf("no new data available")
		}

		if lastMod := resp.Header.Get("Last-Modified"); lastMod != "" {
			p.lastModified = lastMod
		}

		// Handle gzip compression
		var reader io.Reader = resp.Body
		if resp.Header.Get("Content-Encoding") == "gzip" {
			gzReader, err := gzip.NewReader(resp.Body)
			if err != nil {
				resp.Body.Close()
				return nil, fmt.Errorf("error creating gzip reader: %w", err)
			}
			reader = gzReader
		}

		body, err := io.ReadAll(reader)
		if resp.Header.Get("Content-Encoding") == "gzip" {
			reader.(*gzip.Reader).Close()
		}
		resp.Body.Close()
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
		Clouds:      int(current.Data.Instant.Details.CloudArea),
		Pressure:    int(current.Data.Instant.Details.AirPressure),
		Humidity:    int(current.Data.Instant.Details.RelHumidity),
		Description: current.Data.Next1Hours.Summary.SymbolCode,
		Icon:        string(GetStandardIconCode(current.Data.Next1Hours.Summary.SymbolCode, "yrno")),
	}, nil
}
