package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	openWeatherBaseURL      = "https://api.openweathermap.org/data/2.5/weather"
	openWeatherProviderName = "openweather"

	// openWeatherCoordPrecision is the number of decimal places used when
	// formatting latitude/longitude into the request URL (~110 m resolution).
	openWeatherCoordPrecision = 3
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
	// Rain and Snow carry the precipitation volume for the last hour ("1h").
	// OpenWeather omits each object entirely when there is no precipitation of
	// that type, so a zero value means "none".
	Rain struct {
		OneHour float64 `json:"1h"`
	} `json:"rain"`
	Snow struct {
		OneHour float64 `json:"1h"`
	} `json:"snow"`
	Dt  int64 `json:"dt"`
	Sys struct {
		Country string `json:"country"`
		Sunrise int64  `json:"sunrise"`
		Sunset  int64  `json:"sunset"`
	} `json:"sys"`
	Name string `json:"name"`
}

// buildOpenWeatherURL constructs the OpenWeather API request URL using
// url.Values so the API key and other parameters are properly escaped. Building
// the query with fmt.Sprintf risked producing a malformed URL when a value
// required escaping; http.NewRequest would then reject it and return a
// *url.Error carrying the raw key. Escaping at build time avoids that leak path
// and mirrors buildWundergroundURL.
func buildOpenWeatherURL(settings *conf.Settings, apiKey string) (string, error) {
	// Fall back to the default base URL when the configured endpoint is empty or
	// whitespace-only; otherwise url.Parse succeeds with a relative URL and the
	// request later fails. Mirrors validateWundergroundConfig.
	endpoint := strings.TrimSpace(settings.Realtime.Weather.OpenWeather.Endpoint)
	if endpoint == "" {
		endpoint = openWeatherBaseURL
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", newWeatherError(
			fmt.Errorf("invalid openweather endpoint: %w", err),
			errors.CategoryConfiguration, "parse_endpoint", openWeatherProviderName,
		)
	}
	if err := validateEndpointScheme(u, openWeatherProviderName); err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("lat", strconv.FormatFloat(settings.BirdNET.Latitude, 'f', openWeatherCoordPrecision, 64))
	q.Set("lon", strconv.FormatFloat(settings.BirdNET.Longitude, 'f', openWeatherCoordPrecision, 64))
	q.Set("appid", apiKey)
	q.Set("units", settings.Realtime.Weather.OpenWeather.Units)
	q.Set("lang", "en")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// FetchWeather implements the Provider interface for OpenWeatherProvider
func (p *OpenWeatherProvider) FetchWeather(ctx context.Context, settings *conf.Settings) (*WeatherData, error) {
	apiKey := settings.Realtime.Weather.OpenWeather.APIKey
	if apiKey == "" {
		return nil, newWeatherError(
			fmt.Errorf("OpenWeather API key not configured"),
			errors.CategoryConfiguration,
			"validate_config",
			openWeatherProviderName,
		)
	}

	apiURL, err := buildOpenWeatherURL(settings, apiKey)
	if err != nil {
		return nil, err
	}

	providerLogger := getLogger().WithContext(ctx).With(logger.String("provider", openWeatherProviderName))
	providerLogger.Info("Fetching weather data", logger.String("url", maskURLForLog(apiURL)))

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, http.NoBody)
	if err != nil {
		// Scrub before wrapping: http.NewRequestWithContext can return a
		// *url.Error that embeds apiURL, which carries the appid API key query
		// parameter.
		return nil, newWeatherError(privacy.WrapError(err), errors.CategoryNetwork, "create_http_request", openWeatherProviderName)
	}
	req.Header.Set("User-Agent", UserAgent())

	// Execute request with retry via the shared executor.
	body, err := executeWeatherRequest(ctx, p.httpClient, req, openWeatherProviderName, providerLogger, p.handleResponse)
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

// handleResponse classifies a single OpenWeather HTTP response for the shared
// retry executor. A 401 maps to the auth-failed sentinel without retrying; any
// other non-200 retries until the final attempt; a 200 returns the body.
func (p *OpenWeatherProvider) handleResponse(resp *http.Response, attemptLog logger.Logger, isLastAttempt bool) (body []byte, retry bool, err error) {
	// Close the body on every return path; the error-status branches still drain
	// it first so the shared keep-alive connection can be reused. This also keeps
	// the body closed if a read panics, matching WundergroundProvider.executeRequest.
	defer func() { _ = resp.Body.Close() }()

	// HTTP 401: authentication failed — don't retry, return sentinel.
	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.ReadAll(resp.Body)
		attemptLog.Error("Weather API authentication failed — check your API key")
		return nil, false, ErrWeatherAuthFailed
	}

	if resp.StatusCode != http.StatusOK {
		_, _ = io.ReadAll(resp.Body)
		attemptLog.Warn("Received non-OK status code", logger.Int("status_code", resp.StatusCode))
		if isLastAttempt {
			return nil, false, newWeatherErrorWithRetries(
				fmt.Errorf("received non-200 response (%d)", resp.StatusCode),
				errors.CategoryNetwork,
				"weather_api_response",
				openWeatherProviderName,
			)
		}
		return nil, true, nil
	}

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, newWeatherError(err, errors.CategoryNetwork, "read_response_body", openWeatherProviderName)
	}
	return body, false, nil
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

	// OpenWeather reports rain and snow volume separately. Snow takes precedence
	// when both are present in the same hour so the persisted type matches the
	// dominant winter condition.
	precipAmount, precipType := openWeatherPrecipitation(data)

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
		Precipitation: Precipitation{
			Amount: precipAmount,
			Type:   precipType,
		},
		Clouds:      data.Clouds.All,
		Visibility:  data.Visibility,
		Pressure:    data.Main.Pressure,
		Humidity:    data.Main.Humidity,
		WeatherMain: data.Weather[0].Main,
		Description: data.Weather[0].Description,
		Icon:        string(GetStandardIconCode(data.Weather[0].Icon, openWeatherProviderName)),
	}
}

// openWeatherPrecipitation extracts the precipitation amount (mm in the last
// hour) and type from an OpenWeather response. Snow is preferred over rain when
// both are reported in the same hour.
func openWeatherPrecipitation(data *OpenWeatherResponse) (amount float64, precipType string) {
	switch {
	case data.Snow.OneHour > 0:
		return data.Snow.OneHour, "snow"
	case data.Rain.OneHour > 0:
		return data.Rain.OneHour, "rain"
	default:
		return 0, ""
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
