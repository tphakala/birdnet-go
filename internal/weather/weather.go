package weather

import (
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// getLogger returns the weather service logger.
// Fetched dynamically to ensure it uses the current centralized logger.
func getLogger() logger.Logger {
	return logger.Global().Module("weather")
}

// Provider represents a weather data provider interface
type Provider interface {
	FetchWeather(settings *conf.Settings) (*WeatherData, error)
}

// backoffState tracks consecutive failures and backoff timing for the polling loop.
type backoffState struct {
	mu                   sync.Mutex
	consecutiveFailures  int
	consecutiveAuthFails int
	currentBackoff       time.Duration
	authDisabled         bool // true when auth failures exceed threshold
	nextAllowedFetchTime time.Time
}

// reset clears all backoff state after a successful fetch.
func (b *backoffState) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures = 0
	b.consecutiveAuthFails = 0
	b.currentBackoff = 0
	b.nextAllowedFetchTime = time.Time{}
	// Note: authDisabled is NOT reset here — a successful fetch from
	// a different path (e.g., after config change) would need explicit re-enable.
}

// recordAuthFailure increments the auth failure counter and returns true
// if the threshold has been reached and retrying should stop.
func (b *backoffState) recordAuthFailure() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveAuthFails++
	if b.consecutiveAuthFails >= maxConsecutiveAuthFailures {
		b.authDisabled = true
		return true
	}
	return false
}

// recordFailure increments the general failure counter and computes the next backoff.
// Returns the backoff duration and the current consecutive failure count.
func (b *backoffState) recordFailure() (backoff time.Duration, failures int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures++
	if b.currentBackoff == 0 {
		b.currentBackoff = initialBackoffDuration
	} else {
		b.currentBackoff *= backoffMultiplier
	}
	if b.currentBackoff > maxBackoffDuration {
		b.currentBackoff = maxBackoffDuration
	}
	b.nextAllowedFetchTime = time.Now().Add(b.currentBackoff)
	return b.currentBackoff, b.consecutiveFailures
}

// shouldSkip returns true if the current time is before the next allowed fetch time.
func (b *backoffState) shouldSkip() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.authDisabled {
		return true
	}
	if b.nextAllowedFetchTime.IsZero() {
		return false
	}
	return time.Now().Before(b.nextAllowedFetchTime)
}

// isAuthDisabled returns whether auth-based retrying has been permanently stopped.
func (b *backoffState) isAuthDisabled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.authDisabled
}

// Service handles weather data operations
type Service struct {
	provider     Provider
	db           datastore.Interface
	settings     *conf.Settings
	metrics      *metrics.WeatherMetrics
	sunCalc      *suncalc.SunCalc
	startupDelay time.Duration
	backoff      backoffState
}

// WeatherData represents the common structure for weather data across providers
type WeatherData struct {
	Time          time.Time
	Location      Location
	Temperature   Temperature
	Wind          Wind
	Precipitation Precipitation
	Clouds        int
	Visibility    int
	Pressure      int
	Humidity      int
	Description   string
	Icon          string
}

type Location struct {
	Latitude  float64
	Longitude float64
	Country   string
	City      string
}

type Temperature struct {
	Current   float64
	FeelsLike float64
	Min       float64
	Max       float64
}

type Wind struct {
	Speed float64
	Deg   int
	Gust  float64
}

type Precipitation struct {
	Amount float64
	Type   string // rain, snow, etc.
}

// NewService creates a new weather service with the specified provider.
// Returns ErrWeatherDisabled when the provider is empty or unrecognized,
// which the caller should treat as "weather disabled" (no service to start).
func NewService(settings *conf.Settings, db datastore.Interface, weatherMetrics *metrics.WeatherMetrics) (*Service, error) {
	var provider Provider

	// Select weather provider based on configuration
	switch settings.Realtime.Weather.Provider {
	case "yrno":
		provider = NewYrNoProvider()
	case "openweather":
		provider = NewOpenWeatherProvider()
	case "wunderground":
		provider = NewWundergroundProvider(nil)
	case "", "none":
		// Explicitly disabled or not configured — treat as disabled
		getLogger().Info("Weather provider not configured, weather service disabled")
		return nil, ErrWeatherDisabled
	default:
		// Unrecognized provider — warn and treat as disabled rather than
		// raising an error to Sentry (this is a user configuration issue)
		getLogger().Warn("Unrecognized weather provider, weather service disabled",
			logger.String("provider", settings.Realtime.Weather.Provider))
		return nil, ErrWeatherDisabled
	}

	return &Service{
		provider:     provider,
		db:           db,
		settings:     settings,
		metrics:      weatherMetrics,
		sunCalc:      suncalc.NewSunCalc(settings.BirdNET.Latitude, settings.BirdNET.Longitude),
		startupDelay: DefaultStartupDelay,
	}, nil
}

// SaveWeatherData saves the weather data to the database
func (s *Service) SaveWeatherData(data *WeatherData) error {
	// Track operation duration
	start := time.Now()
	defer func() {
		if s.metrics != nil {
			s.metrics.RecordWeatherDbDuration("save_weather_data", time.Since(start).Seconds())
		}
	}()

	// Store weather time in UTC for consistent database comparisons.
	// SQLite compares timestamps as strings, so mixing timezone offsets
	// (e.g., +13:00 vs +00:00) produces incorrect results.
	// The read path (buildHourlyWeatherResponse) converts to local time for display.
	utcTime := data.Time.UTC()

	// Use local time only for the date string used by DailyEvents
	localDate := utcTime.In(time.Local).Format(time.DateOnly)

	// Create daily events data
	dailyEvents := &datastore.DailyEvents{
		Date:     localDate,
		Country:  data.Location.Country,
		CityName: data.Location.City,
	}

	// Populate sunrise/sunset from suncalc so that daily weather responses
	// include correct local-timezone sun times.
	if s.sunCalc != nil {
		if sunTimes, sunErr := s.sunCalc.GetSunEventTimes(data.Time); sunErr == nil {
			dailyEvents.Sunrise = sunTimes.Sunrise.Unix()
			dailyEvents.Sunset = sunTimes.Sunset.Unix()
		} else {
			getLogger().Warn("Failed to calculate sun times for daily events",
				logger.Error(sunErr),
				logger.String("date", localDate))
		}
	}

	// Compute moon phase for this date (location-independent, pure math)
	moonData := suncalc.GetMoonPhase(data.Time)
	dailyEvents.MoonPhase = moonData.Phase
	dailyEvents.MoonIllumination = moonData.Illumination

	// Save daily events data. If this fails (e.g., SQLITE_BUSY), log the error
	// but continue — the upsert will succeed on the next hourly poll, and we
	// can still save hourly weather if a daily_events row already exists.
	var dailyEventsFailed bool
	if err := s.db.SaveDailyEvents(dailyEvents); err != nil {
		dailyEventsFailed = true
		getLogger().Warn("Failed to save daily events to database, will attempt fallback",
			logger.Error(err),
			logger.String("date", dailyEvents.Date),
			logger.String("city", dailyEvents.CityName))
		if s.metrics != nil {
			s.metrics.RecordWeatherDbError("save_daily_events", "database_error")
		}
	} else if s.metrics != nil {
		s.metrics.RecordWeatherDbOperation("save_daily_events", "success")
	}

	// If daily events save failed, try to look up the existing row for the FK
	if dailyEventsFailed {
		existing, lookupErr := s.db.GetDailyEvents(localDate)
		if lookupErr != nil || existing.ID == 0 {
			// No existing row — this is likely the first fetch of the day and the
			// initial save hit a transient error (e.g., SQLITE_BUSY). Brief pause
			// to let the transient lock clear, then retry once.
			getLogger().Info("No existing daily events row found, retrying save after brief delay",
				logger.String("date", localDate))
			time.Sleep(100 * time.Millisecond)
			if retryErr := s.db.SaveDailyEvents(dailyEvents); retryErr != nil {
				// Retry also failed — skip saving weather data for this cycle.
				// The next hourly poll will try again. Log at debug level to
				// avoid flooding Sentry with transient errors.
				getLogger().Debug("Skipping weather save: daily events record unavailable after retry",
					logger.Error(retryErr),
					logger.String("date", localDate))
				if s.metrics != nil {
					s.metrics.RecordWeatherDbError("save_daily_events_retry", "database_error")
				}
				return nil
			}
			getLogger().Info("Retry of SaveDailyEvents succeeded",
				logger.String("date", localDate),
				logger.Any("id", dailyEvents.ID))
			if s.metrics != nil {
				s.metrics.RecordWeatherDbOperation("save_daily_events_retry", "success")
			}
		} else {
			dailyEvents.ID = existing.ID
			getLogger().Info("Using existing daily events row after save failure",
				logger.String("date", localDate),
				logger.Any("id", dailyEvents.ID))
		}
	}

	// Create hourly weather data
	hourlyWeather := &datastore.HourlyWeather{
		DailyEventsID: dailyEvents.ID,
		Time:          utcTime,
		Temperature:   data.Temperature.Current,
		FeelsLike:     data.Temperature.FeelsLike,
		TempMin:       data.Temperature.Min,
		TempMax:       data.Temperature.Max,
		Pressure:      data.Pressure,
		Humidity:      data.Humidity,
		Visibility:    data.Visibility,
		WindSpeed:     data.Wind.Speed,
		WindDeg:       data.Wind.Deg,
		WindGust:      data.Wind.Gust,
		Clouds:        data.Clouds,
		WeatherDesc:   data.Description,
		WeatherIcon:   data.Icon,
	}

	// Basic validation
	if err := validateWeatherData(hourlyWeather); err != nil {
		return err
	}

	// Save hourly weather data
	if err := s.db.SaveHourlyWeather(hourlyWeather); err != nil {
		// Log the error before returning
		getLogger().Error("Failed to save hourly weather to database",
			logger.Error(err),
			logger.Time("time", hourlyWeather.Time))
		if s.metrics != nil {
			s.metrics.RecordWeatherDbError("save_hourly_weather", "database_error")
		}
		// Return the error directly as SaveHourlyWeather already wraps it properly
		return err
	}
	if s.metrics != nil {
		s.metrics.RecordWeatherDbOperation("save_hourly_weather", "success")
		// Update current weather gauges
		s.metrics.UpdateWeatherGauges(
			data.Temperature.Current,
			float64(data.Humidity),
			float64(data.Pressure),
			data.Wind.Speed,
			float64(data.Visibility),
		)
	}

	getLogger().Debug("Successfully saved weather data to database",
		logger.Time("time", utcTime),
		logger.String("city", data.Location.City))
	return nil
}

// DefaultStartupDelay is the delay before the initial weather fetch to reduce
// startup DB contention with other services (image cache warm-up, threshold cleanup).
const DefaultStartupDelay = 10 * time.Second

// absoluteZeroCelsius is the lowest possible temperature in Celsius
const absoluteZeroCelsius = -273.15

// validateWeatherData performs basic validation on weather data
func validateWeatherData(data *datastore.HourlyWeather) error {
	if data.Temperature < absoluteZeroCelsius {
		return errors.Newf("temperature cannot be below absolute zero: %f", data.Temperature).
			Component("weather").
			Category(errors.CategoryValidation).
			Context("temperature", fmt.Sprintf("%.2f", data.Temperature)).
			Build()
	}
	if data.WindSpeed < 0 {
		return errors.Newf("wind speed cannot be negative: %f", data.WindSpeed).
			Component("weather").
			Category(errors.CategoryValidation).
			Context("wind_speed", fmt.Sprintf("%.2f", data.WindSpeed)).
			Build()
	}
	return nil
}

// StartPolling starts the weather polling service
func (s *Service) StartPolling(stopChan <-chan struct{}) {
	interval := time.Duration(s.settings.Realtime.Weather.PollInterval) * time.Minute

	// Use the dedicated weather logger
	getLogger().Info("Starting weather polling service",
		logger.String("provider", s.settings.Realtime.Weather.Provider),
		logger.Int("interval_minutes", s.settings.Realtime.Weather.PollInterval))

	// Delay initial fetch to reduce startup DB contention with other services
	if s.startupDelay > 0 {
		getLogger().Info("Delaying initial weather fetch to reduce startup DB contention",
			logger.String("delay", s.startupDelay.String()))
		select {
		case <-time.After(s.startupDelay):
			// Delay elapsed, proceed with fetch
		case <-stopChan:
			return
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial fetch (errors logged within fetchAndSave)
	_ = s.fetchAndSave()

	for {
		select {
		case <-ticker.C:
			getLogger().Debug("Polling weather data...")
			// Errors logged within fetchAndSave
			_ = s.fetchAndSave()
		case <-stopChan:
			getLogger().Info("Stopping weather polling service")
			return
		}
	}
}

// Poll fetches weather data once and saves it to the database.
// This is useful for on-demand updates or testing the fetch-save cycle.
// Returns nil on success or if data is not modified (304 response).
// Returns ErrWeatherAuthFailed if the weather API key is invalid and retrying
// has been permanently disabled.
func (s *Service) Poll() error {
	if s.backoff.isAuthDisabled() {
		return ErrWeatherAuthFailed
	}
	return s.fetchAndSave()
}

// fetchAndSave fetches weather data and saves it to the database.
// It tracks consecutive failures and applies exponential backoff for transient
// errors. For persistent authentication failures (HTTP 401), it stops retrying
// entirely after maxConsecutiveAuthFailures.
func (s *Service) fetchAndSave() error {
	// Check if we should skip this cycle due to backoff
	if s.backoff.shouldSkip() {
		if s.backoff.isAuthDisabled() {
			// Already logged when auth was disabled; emit periodic reminder at Debug level
			getLogger().Debug("Skipping weather fetch — API authentication disabled due to repeated 401 errors",
				logger.String("provider", s.settings.Realtime.Weather.Provider))
		} else {
			getLogger().Debug("Skipping weather fetch — backing off after previous failures",
				logger.String("provider", s.settings.Realtime.Weather.Provider))
		}
		return nil
	}

	// Track fetch duration
	fetchStart := time.Now()

	// FetchWeather should now internally log its start/end/errors
	data, err := s.provider.FetchWeather(s.settings)

	if err != nil {
		// Handle "not modified" as a success case — no new data to save.
		// Check before recording metrics so these expected conditions are
		// not counted as errors.
		if errors.Is(err, ErrWeatherDataNotModified) {
			if s.metrics != nil {
				s.metrics.RecordWeatherFetchDuration(s.settings.Realtime.Weather.Provider, time.Since(fetchStart).Seconds())
				s.metrics.RecordWeatherFetch(s.settings.Realtime.Weather.Provider, "not_modified")
			}
			getLogger().Debug("Weather data not modified since last fetch",
				logger.String("provider", s.settings.Realtime.Weather.Provider))
			s.backoff.reset()
			return nil
		}

		// Handle "no data" (HTTP 204) — station exists but has no observations.
		// This is a valid API response, not an error condition.
		if errors.Is(err, ErrWeatherNoData) {
			if s.metrics != nil {
				s.metrics.RecordWeatherFetchDuration(s.settings.Realtime.Weather.Provider, time.Since(fetchStart).Seconds())
				s.metrics.RecordWeatherFetch(s.settings.Realtime.Weather.Provider, "no_data")
			}
			getLogger().Debug("Weather station has no data available, will retry next cycle",
				logger.String("provider", s.settings.Realtime.Weather.Provider))
			// Don't backoff — this is a normal condition for inactive stations
			return nil
		}
	}

	// Record fetch metrics for real errors and successes
	if s.metrics != nil {
		s.metrics.RecordWeatherFetchDuration(s.settings.Realtime.Weather.Provider, time.Since(fetchStart).Seconds())
		if err != nil {
			s.metrics.RecordWeatherFetch(s.settings.Realtime.Weather.Provider, "error")
			s.metrics.RecordWeatherFetchError(s.settings.Realtime.Weather.Provider, "fetch_error")
		} else {
			s.metrics.RecordWeatherFetch(s.settings.Realtime.Weather.Provider, "success")
		}
	}

	if err != nil {
		// Handle authentication failure (HTTP 401)
		if errors.Is(err, ErrWeatherAuthFailed) {
			stopped := s.backoff.recordAuthFailure()
			if stopped {
				getLogger().Error("Weather API authentication failed repeatedly — stopping retries. "+
					"Please check your API key in the settings and restart the service.",
					logger.String("provider", s.settings.Realtime.Weather.Provider),
					logger.Int("consecutive_failures", maxConsecutiveAuthFailures))
			} else {
				getLogger().Warn("Weather API authentication failed — will retry",
					logger.String("provider", s.settings.Realtime.Weather.Provider),
					logger.Error(err))
			}
			return ErrWeatherAuthFailed
		}

		// General failure: apply exponential backoff
		backoff, failures := s.backoff.recordFailure()
		getLogger().Error("Failed to fetch weather data from provider, backing off",
			logger.String("provider", s.settings.Realtime.Weather.Provider),
			logger.String("backoff", backoff.String()),
			logger.Int("consecutive_failures", failures),
			logger.Error(err))

		return errors.New(err).
			Component("weather").
			Category(errors.CategoryNetwork).
			Context("operation", "fetch_weather_data").
			Context("provider", s.settings.Realtime.Weather.Provider).
			Build()
	}

	// Success — reset backoff state
	s.backoff.reset()

	// Convert to local time for logging. SaveWeatherData handles its own
	// timezone conversion for storage.
	localTimeForLog := data.Time.In(time.Local)

	getLogger().Info("Successfully fetched weather data",
		logger.String("provider", s.settings.Realtime.Weather.Provider),
		logger.String("time", localTimeForLog.Format("2006-01-02 15:04:05-07:00")),
		logger.Float64("temp_c", data.Temperature.Current),
		logger.Float64("wind_mps", data.Wind.Speed),
		logger.Int("humidity_pct", data.Humidity),
		logger.Int("pressure_hpa", data.Pressure),
		logger.String("description", data.Description),
		logger.String("city", data.Location.City))

	// Errors logged within SaveWeatherData
	return s.SaveWeatherData(data)
}
