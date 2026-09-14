package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	pirateWeatherBaseURL      = "https://api.pirateweather.net/forecast"
	pirateWeatherProviderName = "pirateweather"

	// pirateWeatherCoordPrecision is the number of decimal places used when
	// formatting latitude/longitude into the request path (~110 m resolution).
	pirateWeatherCoordPrecision = 3

	// metersPerKm converts the API's kilometre visibility into the meters
	// WeatherData.Visibility uses (matching OpenWeather's convention).
	metersPerKm = 1000
)

// PirateWeatherResponse represents the structure of weather data returned by
// the Pirate Weather API, a Dark Sky-API-compatible service. Only the
// "currently" block is populated: buildPirateWeatherURL excludes the
// minutely/hourly/daily/alerts blocks since only current conditions are used.
type PirateWeatherResponse struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Currently struct {
		Time                int64   `json:"time"`
		Summary             string  `json:"summary"`
		Icon                string  `json:"icon"`
		PrecipIntensity     float64 `json:"precipIntensity"`
		PrecipType          string  `json:"precipType"` // "rain", "snow", "sleet", or "none"
		Temperature         float64 `json:"temperature"`
		ApparentTemperature float64 `json:"apparentTemperature"`
		Humidity            float64 `json:"humidity"` // fraction 0-1
		Pressure            float64 `json:"pressure"` // hPa
		WindSpeed           float64 `json:"windSpeed"`
		WindGust            float64 `json:"windGust"`
		WindBearing         int     `json:"windBearing"`
		CloudCover          float64 `json:"cloudCover"` // fraction 0-1
		Visibility          float64 `json:"visibility"` // km
	} `json:"currently"`
}

// buildPirateWeatherURL constructs the Pirate Weather API request URL. Unlike
// OpenWeather/Wunderground, the API key and coordinates are part of the URL
// PATH for this API (/forecast/{apikey}/{lat},{lon}), not query parameters, so
// they are escaped as path segments rather than added via url.Values.
func buildPirateWeatherURL(settings *conf.Settings, apiKey string) (string, error) {
	// Fall back to the default base URL when the configured endpoint is empty or
	// whitespace-only; otherwise url.Parse succeeds with a relative URL and the
	// request later fails. Mirrors buildOpenWeatherURL/validateWundergroundConfig.
	endpoint := strings.TrimSpace(settings.Realtime.Weather.PirateWeather.Endpoint)
	if endpoint == "" {
		endpoint = pirateWeatherBaseURL
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", newWeatherError(
			fmt.Errorf("invalid pirateweather endpoint: %w", err),
			errors.CategoryConfiguration, "parse_endpoint", pirateWeatherProviderName,
		)
	}
	if err := validateEndpointScheme(u, pirateWeatherProviderName); err != nil {
		return "", err
	}

	lat := strconv.FormatFloat(settings.BirdNET.Latitude, 'f', pirateWeatherCoordPrecision, 64)
	lon := strconv.FormatFloat(settings.BirdNET.Longitude, 'f', pirateWeatherCoordPrecision, 64)
	// Escape lat/lon individually and join with a literal comma: url.PathEscape
	// would otherwise percent-encode the comma too, which still works but no
	// longer matches Pirate Weather's documented /lat,lon path format.
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + url.PathEscape(apiKey) + "/" + url.PathEscape(lat) + "," + url.PathEscape(lon)

	q := u.Query()
	// Always request SI units (Celsius, m/s, hPa, km) regardless of any
	// user-facing unit preference: WeatherData stores everything in these
	// units internally, matching how Wunderground always requests metric.
	q.Set("units", "si")
	// Only the "currently" block is used, so exclude the rest to keep the
	// response small.
	q.Set("exclude", "minutely,hourly,daily,alerts")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// maskPirateWeatherURL returns a log-safe representation of a Pirate Weather
// request URL. The shared maskURLForLog only redacts query parameters, but
// this API's API key and coordinates live in the URL path
// (/forecast/{apikey}/{lat},{lon}), so the whole path is replaced here,
// keeping only the scheme and host for debugging.
func maskPirateWeatherURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return maskedURLOnError
	}
	parsed.Path = "/forecast/" + redactedValue + "/" + redactedValue
	parsed.RawQuery = ""
	return parsed.String()
}

// FetchWeather implements the Provider interface for PirateWeatherProvider
func (p *PirateWeatherProvider) FetchWeather(ctx context.Context, settings *conf.Settings) (*WeatherData, error) {
	apiKey := settings.Realtime.Weather.PirateWeather.APIKey
	if apiKey == "" {
		return nil, newWeatherError(
			fmt.Errorf("pirate weather API key not configured"),
			errors.CategoryConfiguration,
			"validate_config",
			pirateWeatherProviderName,
		)
	}

	apiURL, err := buildPirateWeatherURL(settings, apiKey)
	if err != nil {
		return nil, err
	}

	providerLogger := getLogger().WithContext(ctx).With(logger.String("provider", pirateWeatherProviderName))
	providerLogger.Info("Fetching weather data", logger.String("url", maskPirateWeatherURL(apiURL)))

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, http.NoBody)
	if err != nil {
		// Scrub before wrapping: http.NewRequestWithContext can return a
		// *url.Error that embeds apiURL, which carries the API key and
		// coordinates in its path.
		return nil, newWeatherError(privacy.WrapError(err), errors.CategoryNetwork, "create_http_request", pirateWeatherProviderName)
	}
	req.Header.Set("User-Agent", UserAgent())

	// Execute request with retry via the shared executor.
	body, err := executeWeatherRequest(ctx, p.httpClient, req, pirateWeatherProviderName, providerLogger, standardHandleResponse(pirateWeatherProviderName))
	if err != nil {
		return nil, err
	}

	// Parse response
	var weatherData PirateWeatherResponse
	if err := json.Unmarshal(body, &weatherData); err != nil {
		return nil, newWeatherError(err, errors.CategoryValidation, "unmarshal_weather_data", pirateWeatherProviderName)
	}

	providerLogger.Info("Successfully received and parsed weather data")
	mappedData := mapPirateWeatherResponse(&weatherData)
	providerLogger.Debug("Mapped API response to WeatherData structure",
		logger.Float64("temp", mappedData.Temperature.Current))
	return mappedData, nil
}

// mapPirateWeatherResponse converts PirateWeatherResponse to WeatherData
func mapPirateWeatherResponse(data *PirateWeatherResponse) *WeatherData {
	iconCode := GetStandardIconCode(data.Currently.Icon, pirateWeatherProviderName)

	// precipIntensity is already zero when there is no precipitation; clamp
	// defensively against a negative value, matching the yr.no/Wunderground
	// providers. precipType is "none" (not empty) when there is none.
	precipAmount := max(0, data.Currently.PrecipIntensity)
	precipType := data.Currently.PrecipType
	if precipType == "none" {
		precipType = ""
	}

	return &WeatherData{
		Time: time.Unix(data.Currently.Time, 0),
		Location: Location{
			Latitude:  data.Latitude,
			Longitude: data.Longitude,
		},
		Temperature: Temperature{
			Current:   data.Currently.Temperature,
			FeelsLike: data.Currently.ApparentTemperature,
		},
		Wind: Wind{
			Speed: data.Currently.WindSpeed,
			Deg:   data.Currently.WindBearing,
			Gust:  data.Currently.WindGust,
		},
		Precipitation: Precipitation{
			Amount: precipAmount,
			Type:   precipType,
		},
		Clouds:      int(math.Round(data.Currently.CloudCover * 100)),
		Visibility:  int(math.Round(data.Currently.Visibility * metersPerKm)),
		Pressure:    int(math.Round(data.Currently.Pressure)),
		Humidity:    int(math.Round(data.Currently.Humidity * 100)),
		WeatherMain: weatherMainFromIconCode(iconCode),
		Description: data.Currently.Summary,
		Icon:        string(iconCode),
	}
}
