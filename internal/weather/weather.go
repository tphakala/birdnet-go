package weather

import (
	"context"
	"crypto/sha256"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

var (
	globalServiceMu sync.RWMutex
	globalService   *Service
)

// RegisterService stores the weather service instance for package-level access.
// Called during audio pipeline startup.
func RegisterService(s *Service) {
	globalServiceMu.Lock()
	globalService = s
	globalServiceMu.Unlock()
}

// UnregisterService clears the stored weather service instance.
func UnregisterService() {
	globalServiceMu.Lock()
	globalService = nil
	globalServiceMu.Unlock()
}

// GetStatus returns the health status of the weather service. Returns
// (ok, message) suitable for health check consumption. Returns
// (false, "Weather service not started") when no service has been registered;
// the diagnostics weather check only consults this once the provider is
// configured (not "none"), so a missing service at that point is unhealthy.
func GetStatus() (ok bool, msg string) {
	globalServiceMu.RLock()
	svc := globalService
	globalServiceMu.RUnlock()

	if svc == nil {
		return false, "Weather service not started"
	}
	return svc.Status()
}

// getLogger returns the weather service logger.
// Fetched dynamically to ensure it uses the current centralized logger.
func getLogger() logger.Logger {
	return logger.Global().Module("weather")
}

// Provider represents a weather data provider interface
type Provider interface {
	// FetchWeather retrieves current weather. The context lets a shutdown or an
	// aborted on-demand request cancel an in-flight HTTP call and its retry
	// backoff instead of blocking until the request finishes.
	FetchWeather(ctx context.Context, settings *conf.Settings) (*WeatherData, error)
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
	// Note: authDisabled is NOT reset here. While auth is disabled shouldSkip()
	// returns true and no fetch runs, so a success path can never reach this to
	// clear it. Re-enabling is driven by a config change via clearAuthDisabled().
}

// clearAuthDisabled re-enables fetching after auth was disabled, clearing the
// auth-failure counter. Called when the auth-relevant config (provider, API key,
// endpoint) changes, so a user who fixes the key in the UI recovers on the next
// cycle without a restart.
func (b *backoffState) clearAuthDisabled() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.authDisabled = false
	b.consecutiveAuthFails = 0
}

// recordAuthFailure increments the auth failure counter and returns true
// if the threshold has been reached and retrying should stop.
//
// Auth failures intentionally do not set nextAllowedFetchTime: the first few
// 401s retry on the normal poll cadence (no extra spacing) so a key fixed in
// the UI recovers quickly, and only after maxConsecutiveAuthFailures does the
// service stop retrying until the config changes. This is deliberately
// asymmetric with recordFailure, which backs off transient errors immediately.
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

// inBackoffLocked reports whether the transient-failure backoff window is still
// open. The caller must hold b.mu. shouldSkip and snapshot share it so the
// in-backoff predicate cannot drift between them.
func (b *backoffState) inBackoffLocked() bool {
	return !b.nextAllowedFetchTime.IsZero() && time.Now().Before(b.nextAllowedFetchTime)
}

// shouldSkip returns true if auth is disabled or the backoff window is still open.
func (b *backoffState) shouldSkip() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.authDisabled {
		return true
	}
	return b.inBackoffLocked()
}

// isAuthDisabled returns whether auth-based retrying is currently stopped.
// This is cleared automatically by clearAuthDisabled() on a config change.
func (b *backoffState) isAuthDisabled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.authDisabled
}

// snapshot returns a consistent view of the backoff state under a single lock,
// so callers do not re-derive the in-backoff predicate inline (which can drift
// from shouldSkip).
func (b *backoffState) snapshot() (authDisabled bool, failures int, inBackoff bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.authDisabled, b.consecutiveFailures, b.inBackoffLocked()
}

// Service handles weather data operations
type Service struct {
	provider Provider
	// providerName is pinned at construction so logs and metrics always
	// identify the actual provider implementation in use. Switching providers
	// through the UI requires a service restart anyway (the provider interface
	// implementation is selected once in NewService), so reading the provider
	// string from the latest settings snapshot could misreport what the HTTP
	// calls are actually hitting.
	providerName string
	db           datastore.Interface
	settings     *conf.Settings
	metrics      *metrics.WeatherMetrics
	startupDelay time.Duration
	backoff      backoffState

	// fetchMu serializes fetchAndSave so the exported Poll() and the StartPolling
	// ticker cannot run a fetch concurrently. It also guards the hot-reload state
	// below (sunCalc and authConfigKey), all of which is read/updated per cycle
	// inside fetchAndSave.
	fetchMu sync.Mutex
	// sunCalc is rebuilt when the configured coordinates change between cycles so
	// sunrise/sunset track the current location after a UI location change.
	sunCalc *suncalc.SunCalc
	// sunCalcLat/sunCalcLon record the coordinates sunCalc was last built with.
	sunCalcLat float64
	sunCalcLon float64
	// authConfigKey is the auth-relevant config fingerprint (SHA-256 digest)
	// captured when auth was disabled; a change re-enables fetching (hot-reload
	// of the API key).
	authConfigKey [32]byte
}

// weatherAuthConfigKey builds a fingerprint of the auth-relevant weather config
// (provider plus the active provider's key/station/endpoint). A change between
// cycles signals the user updated credentials in the UI, which re-enables
// fetching after an auth lockout. It returns a SHA-256 digest rather than the
// raw values so the API keys are not retained verbatim in the Service struct
// (s.authConfigKey); the digest is only ever compared, never logged. [32]byte
// arrays are directly comparable, so no string conversion is needed.
func weatherAuthConfigKey(settings *conf.Settings) [32]byte {
	w := &settings.Realtime.Weather
	// Only the active provider's auth fields matter. Including the inactive
	// provider's credentials would let an unrelated edit (e.g. changing the
	// Wunderground key while OpenWeather is locked out) clear the lockout and
	// trigger a fresh round of 401s against the still-broken active provider.
	var fields []string
	switch w.Provider {
	case openWeatherProviderName:
		fields = []string{w.Provider, w.OpenWeather.APIKey, w.OpenWeather.Endpoint}
	case wundergroundProviderName:
		fields = []string{w.Provider, w.Wunderground.APIKey, w.Wunderground.StationID, w.Wunderground.Endpoint}
	default:
		// yr.no (and "none"/unset) have no API key, so the provider name alone
		// is the whole auth-relevant config.
		fields = []string{w.Provider}
	}
	return sha256.Sum256([]byte(strings.Join(fields, "\x00")))
}

// currentSettings returns the latest settings snapshot so the service picks
// up changes made through the UI (e.g. a new BirdNET latitude/longitude)
// without requiring a restart.
func (s *Service) currentSettings() *conf.Settings {
	return conf.CurrentOrFallback(s.settings)
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
	var (
		provider     Provider
		providerName string
	)

	// One HTTP client is shared across every fetch cycle and retry attempt for
	// the service's lifetime, replacing the per-request clients the providers
	// used to allocate. It is injected into whichever provider is selected.
	weatherClient := newDefaultHTTPClient()

	// Select weather provider based on configuration
	switch conf.WeatherProvider(settings.Realtime.Weather.Provider) {
	case conf.WeatherYrNo:
		provider = NewYrNoProvider(weatherClient)
		providerName = yrNoProviderName
	case conf.WeatherOpenWeather:
		provider = NewOpenWeatherProvider(weatherClient)
		providerName = openWeatherProviderName
	case conf.WeatherWunderground:
		provider = NewWundergroundProvider(weatherClient)
		providerName = wundergroundProviderName
	case "":
		// Not configured - default to yr.no
		provider = NewYrNoProvider(weatherClient)
		providerName = yrNoProviderName
	case conf.WeatherNone:
		// Explicitly disabled
		getLogger().Info("Weather provider set to none, weather service disabled")
		return nil, ErrWeatherDisabled
	default:
		// Unrecognized provider: warn and treat as disabled rather than
		// raising an error to Sentry (this is a user configuration issue)
		getLogger().Warn("Unrecognized weather provider, weather service disabled",
			logger.String("provider", settings.Realtime.Weather.Provider))
		return nil, ErrWeatherDisabled
	}

	return &Service{
		provider:     provider,
		providerName: providerName,
		db:           db,
		settings:     settings,
		metrics:      weatherMetrics,
		sunCalc:      suncalc.NewSunCalc(settings.BirdNET.Latitude, settings.BirdNET.Longitude),
		sunCalcLat:   settings.BirdNET.Latitude,
		sunCalcLon:   settings.BirdNET.Longitude,
		startupDelay: DefaultStartupDelay,
	}, nil
}

// saveWeatherData saves the weather data to the database.
//
// It reads s.sunCalc, which reconcileConfig rebuilds under s.fetchMu when the
// configured location changes, so it must be called with s.fetchMu held. It is
// unexported for that reason; the only caller, fetchAndSave, holds the lock.
func (s *Service) saveWeatherData(data *WeatherData) error {
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
	// but continue: the upsert will succeed on the next hourly poll, and we
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
			// No existing row: this is likely the first fetch of the day and the
			// initial save hit a transient error (e.g., SQLITE_BUSY). Brief pause
			// to let the transient lock clear, then retry once.
			getLogger().Info("No existing daily events row found, retrying save after brief delay",
				logger.String("date", localDate))
			time.Sleep(100 * time.Millisecond)
			if retryErr := s.db.SaveDailyEvents(dailyEvents); retryErr != nil {
				// Retry also failed: skip saving weather data for this cycle.
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
	// Poll interval is read once at startup; the ticker cadence is not
	// hot-reloadable without a service restart.
	interval := time.Duration(s.settings.Realtime.Weather.PollInterval) * time.Minute
	if interval <= 0 {
		// PollInterval is normally validated to >= 15 minutes (conf
		// validate_realtime), but StartPolling reads the raw setting and
		// time.NewTicker panics on a non-positive interval, which would crash
		// this long-lived goroutine. Fall back to the default instead.
		getLogger().Warn("Invalid weather poll interval, using default",
			logger.Int("configured_minutes", s.settings.Realtime.Weather.PollInterval),
			logger.Int("default_minutes", conf.DefaultWeatherPollInterval))
		interval = time.Duration(conf.DefaultWeatherPollInterval) * time.Minute
	}

	// Derive a context that is cancelled when stopChan closes (or when this
	// method returns), and thread it through each fetch so a shutdown cancels an
	// in-flight provider HTTP call and its retry backoff. StartPolling keeps its
	// channel-based signature (the sole caller has no context to pass); the
	// bridge goroutine exits via ctx.Done() once the deferred cancel fires, so
	// it cannot leak.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stopChan:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Use the dedicated weather logger
	getLogger().Info("Starting weather polling service",
		logger.String("provider", s.providerName),
		logger.Int("interval_minutes", s.settings.Realtime.Weather.PollInterval))

	// Delay initial fetch to reduce startup DB contention with other services
	if s.startupDelay > 0 {
		getLogger().Info("Delaying initial weather fetch to reduce startup DB contention",
			logger.String("delay", s.startupDelay.String()))
		// time.After is leak-free here: on Go 1.23+ an unreferenced timer is
		// garbage collected even if it has not fired or been stopped.
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
	s.safeFetchAndSave(ctx)

	for {
		select {
		case <-ticker.C:
			getLogger().Debug("Polling weather data...")
			// Errors logged within fetchAndSave
			s.safeFetchAndSave(ctx)
		case <-stopChan:
			getLogger().Info("Stopping weather polling service")
			return
		}
	}
}

// safeFetchAndSave runs fetchAndSave with panic recovery so a panic in a
// provider, the response mapping, or the persistence path degrades only the
// weather service instead of crashing the whole process. Errors are already
// logged inside fetchAndSave, so the return value is intentionally discarded.
func (s *Service) safeFetchAndSave(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			// Record a failure so a repeatedly panicking fetch surfaces as
			// degraded via Status()/GetStatus() and backs off, instead of being
			// silently recovered while diagnostics keep reporting healthy.
			// fetchAndSave's deferred unlock has already released fetchMu during
			// the panic unwind, so recordFailure only takes backoff.mu here.
			backoff, failures := s.backoff.recordFailure()
			getLogger().Error("Weather poll cycle panicked, recovering and backing off",
				logger.String("provider", s.providerName),
				logger.Any("panic", r),
				logger.String("backoff", backoff.String()),
				logger.Int("consecutive_failures", failures),
				logger.String("stack", string(debug.Stack())))
		}
	}()
	_ = s.fetchAndSave(ctx)
}

// Poll fetches weather data once and saves it to the database.
// This is useful for on-demand updates or testing the fetch-save cycle.
// Returns nil on success or if data is not modified (304 response).
// Returns ErrWeatherAuthFailed when auth is currently disabled after repeated
// 401s; that lockout clears automatically on a config change (see reconcileConfig).
//
// Unlike the StartPolling loop, Poll does not wrap the fetch in panic recovery:
// it is a synchronous on-demand call, so a panic propagates to the caller.
func (s *Service) Poll(ctx context.Context) error {
	// Run a fetch cycle. fetchAndSave reconciles hot-reloaded config first, so a
	// lockout cleared by a config change recovers even on a manual Poll; the
	// auth check therefore happens after reconciliation, not before it. While
	// auth is disabled and unchanged, fetchAndSave skips without a network call.
	if err := s.fetchAndSave(ctx); err != nil {
		return err
	}
	// fetchAndSave returns nil when it skipped because auth is still disabled;
	// surface that to the on-demand caller.
	if s.backoff.isAuthDisabled() {
		return ErrWeatherAuthFailed
	}
	return nil
}

// reconcileConfig applies hot-reloadable settings changes detected between poll
// cycles so the weather service honors UI edits without a restart. It rebuilds
// sunCalc when the configured coordinates change (so sunrise/sunset track the
// new location) and clears an auth lockout when the auth-relevant config (API
// key, station, endpoint, or provider) changes (so a corrected key recovers on
// the next cycle). Must be called under s.fetchMu, which guards sunCalc and
// authConfigKey.
func (s *Service) reconcileConfig(settings *conf.Settings) {
	// Rebuild sunCalc on a location change. Coordinates are PII, so the change
	// is logged without the values themselves.
	lat, lon := settings.BirdNET.Latitude, settings.BirdNET.Longitude
	if lat != s.sunCalcLat || lon != s.sunCalcLon {
		getLogger().Info("Weather location changed, rebuilding sun time calculator",
			logger.String("provider", s.providerName))
		s.sunCalc = suncalc.NewSunCalc(lat, lon)
		s.sunCalcLat = lat
		s.sunCalcLon = lon
	}

	// Re-enable fetching if auth was disabled and the auth-relevant config has
	// changed since the lockout. While authDisabled is set, shouldSkip() returns
	// true and no fetch runs, so a config delta is the only recovery trigger.
	if s.backoff.isAuthDisabled() && weatherAuthConfigKey(settings) != s.authConfigKey {
		getLogger().Info("Weather API configuration changed, re-enabling fetches after auth lockout",
			logger.String("provider", s.providerName))
		s.backoff.clearAuthDisabled()
	}
}

// fetchAndSave fetches weather data and saves it to the database.
// It tracks consecutive failures and applies exponential backoff for transient
// errors. For repeated authentication failures (HTTP 401), it pauses retrying
// after maxConsecutiveAuthFailures until the auth-relevant config changes
// (handled by reconcileConfig), at which point it resumes automatically.
func (s *Service) fetchAndSave(ctx context.Context) error {
	// Serialize fetch cycles. Both the exported Poll() and the StartPolling
	// ticker call this; the lock keeps the skip -> fetch -> reset sequence and
	// the SQLite writes atomic, and guards the per-cycle hot-reload state
	// (sunCalc, authConfigKey) reconciled below.
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()

	// Read a fresh settings snapshot so coordinate changes made via the
	// settings UI take effect without restarting the weather service. The
	// snapshot is captured once per cycle and passed to the provider so that
	// coordinate/API-key reads inside the provider see a consistent view.
	// Provider implementation stays pinned to what NewService selected:
	// switching providers still requires a service restart, and s.providerName
	// records the actually-used one for logs and metrics.
	currentSettings := s.currentSettings()

	// Apply hot-reloadable config changes detected since the last cycle before
	// the backoff check, so a corrected API key clears the auth lockout that
	// shouldSkip() would otherwise honor.
	s.reconcileConfig(currentSettings)

	// Check if we should skip this cycle due to backoff
	if s.backoff.shouldSkip() {
		if s.backoff.isAuthDisabled() {
			// Already logged when auth was disabled; emit periodic reminder at Debug level
			getLogger().Debug("Skipping weather fetch, API authentication disabled due to repeated 401 errors",
				logger.String("provider", s.providerName))
		} else {
			getLogger().Debug("Skipping weather fetch, backing off after previous failures",
				logger.String("provider", s.providerName))
		}
		return nil
	}

	// Track fetch duration
	fetchStart := time.Now()

	// FetchWeather should now internally log its start/end/errors
	data, err := s.provider.FetchWeather(ctx, currentSettings)

	if err != nil {
		// A cancelled context means the service is shutting down (or an
		// on-demand caller aborted): treat it as benign so shutdown neither logs
		// an error nor trips the failure backoff. context.DeadlineExceeded is
		// intentionally excluded; that is a real request timeout worth a backoff.
		if errors.Is(err, context.Canceled) {
			// Cancellation is a shutdown or an aborted on-demand request, not a
			// provider failure, so skip the failure/backoff bookkeeping below. Return
			// the error rather than swallowing it so an on-demand Poll(ctx) caller
			// still observes the cancellation; the StartPolling loop ignores
			// fetchAndSave's return value, so a background shutdown stays benign.
			getLogger().Debug("Weather fetch cancelled",
				logger.String("provider", s.providerName))
			return err
		}

		// Handle "not modified" as a success case: no new data to save.
		// Check before recording metrics so these expected conditions are
		// not counted as errors.
		if errors.Is(err, ErrWeatherDataNotModified) {
			if s.metrics != nil {
				s.metrics.RecordWeatherFetchDuration(s.providerName, time.Since(fetchStart).Seconds())
				s.metrics.RecordWeatherFetch(s.providerName, "not_modified")
			}
			getLogger().Debug("Weather data not modified since last fetch",
				logger.String("provider", s.providerName))
			s.backoff.reset()
			return nil
		}

		// Handle "no data" (HTTP 204): station exists but has no observations.
		// This is a valid API response, not an error condition.
		if errors.Is(err, ErrWeatherNoData) {
			if s.metrics != nil {
				s.metrics.RecordWeatherFetchDuration(s.providerName, time.Since(fetchStart).Seconds())
				s.metrics.RecordWeatherFetch(s.providerName, "no_data")
			}
			getLogger().Debug("Weather station has no data available, will retry next cycle",
				logger.String("provider", s.providerName))
			// HTTP 204 is a successful round-trip, so clear any transient-failure
			// backoff like the 304 and success paths do. A station that starts
			// returning 204 should not stay stuck in "degraded" after earlier
			// transient errors. Auth state is intentionally left untouched.
			s.backoff.reset()
			return nil
		}
	}

	// Record fetch metrics for real errors and successes
	if s.metrics != nil {
		s.metrics.RecordWeatherFetchDuration(s.providerName, time.Since(fetchStart).Seconds())
		if err != nil {
			s.metrics.RecordWeatherFetch(s.providerName, "error")
			s.metrics.RecordWeatherFetchError(s.providerName, "fetch_error")
		} else {
			s.metrics.RecordWeatherFetch(s.providerName, "success")
		}
	}

	if err != nil {
		// Handle authentication failure (HTTP 401)
		if errors.Is(err, ErrWeatherAuthFailed) {
			stopped := s.backoff.recordAuthFailure()
			if stopped {
				// Capture the auth-relevant config at lockout so reconcileConfig
				// can re-enable fetches automatically once the user corrects it.
				s.authConfigKey = weatherAuthConfigKey(currentSettings)
				getLogger().Error("Weather API authentication failed repeatedly, pausing retries. "+
					"Update your API key in the settings and fetching resumes automatically on the next cycle (no restart needed).",
					logger.String("provider", s.providerName),
					logger.Int("consecutive_failures", maxConsecutiveAuthFailures))
			} else {
				getLogger().Warn("Weather API authentication failed, will retry",
					logger.String("provider", s.providerName),
					logger.Error(err))
			}
			return ErrWeatherAuthFailed
		}

		// General failure: apply exponential backoff
		backoff, failures := s.backoff.recordFailure()
		getLogger().Error("Failed to fetch weather data from provider, backing off",
			logger.String("provider", s.providerName),
			logger.String("backoff", backoff.String()),
			logger.Int("consecutive_failures", failures),
			logger.Error(err))

		return errors.New(err).
			Component("weather").
			Category(errors.CategoryNetwork).
			Context("operation", "fetch_weather_data").
			Context("provider", s.providerName).
			Build()
	}

	// Success: reset backoff state
	s.backoff.reset()

	// Convert to local time for logging. saveWeatherData handles its own
	// timezone conversion for storage.
	localTimeForLog := data.Time.In(time.Local)

	getLogger().Info("Successfully fetched weather data",
		logger.String("provider", s.providerName),
		logger.String("time", localTimeForLog.Format("2006-01-02 15:04:05-07:00")),
		logger.Float64("temp_c", data.Temperature.Current),
		logger.Float64("wind_mps", data.Wind.Speed),
		logger.Int("humidity_pct", data.Humidity),
		logger.Int("pressure_hpa", data.Pressure),
		logger.String("description", data.Description),
		logger.String("city", data.Location.City))

	// Errors logged within saveWeatherData
	return s.saveWeatherData(data)
}

// Status reports the health of the weather service by inspecting backoff state.
// Returns (ok, statusMessage).
func (s *Service) Status() (ok bool, msg string) {
	authDisabled, failures, inBackoff := s.backoff.snapshot()

	if authDisabled {
		return false, fmt.Sprintf("Weather provider %s auth disabled", s.providerName)
	}

	if inBackoff {
		return false, fmt.Sprintf("Weather provider %s backing off (%d failures)", s.providerName, failures)
	}

	if failures > 0 {
		return false, fmt.Sprintf("Weather provider %s degraded (%d failures)", s.providerName, failures)
	}

	return true, fmt.Sprintf("Weather provider %s healthy", s.providerName)
}
