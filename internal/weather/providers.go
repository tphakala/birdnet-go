package weather

import (
	"net/http"
	"sync"
	"time"
)

// newUnguardedTestClient builds the fallback HTTP client used when a provider is
// constructed with a nil client, which in practice happens only in tests. It is
// deliberately UNGUARDED: it leaves Transport nil so it uses http.DefaultTransport,
// which keeps it interceptable by httpmock, and bounds each request with
// RequestTimeout. Production code MUST inject an SSRF-guarded client via
// httpclient.NewGuardedHTTPClient (see weather.NewService and the integrations
// weather probes) rather than relying on this unguarded fallback.
func newUnguardedTestClient() *http.Client {
	return &http.Client{Timeout: RequestTimeout}
}

// NewYrNoProvider creates a new Yr.no weather provider with a shared HTTP client.
// A nil client falls back to a default client (see newUnguardedTestClient).
func NewYrNoProvider(client *http.Client) Provider {
	if client == nil {
		client = newUnguardedTestClient()
	}
	return &YrNoProvider{httpClient: client}
}

// NewOpenWeatherProvider creates a new OpenWeather provider with a shared HTTP
// client. A nil client falls back to a default client (see newUnguardedTestClient).
func NewOpenWeatherProvider(client *http.Client) Provider {
	if client == nil {
		client = newUnguardedTestClient()
	}
	return &OpenWeatherProvider{httpClient: client}
}

// NewWundergroundProvider creates a new WeatherUnderground provider with a shared
// HTTP client. A nil client falls back to a default client (see
// newUnguardedTestClient).
func NewWundergroundProvider(client *http.Client) Provider {
	if client == nil {
		client = newUnguardedTestClient()
	}
	return &WundergroundProvider{httpClient: client}
}

// NewPirateWeatherProvider creates a new Pirate Weather provider with a shared
// HTTP client. A nil client falls back to a default client (see
// newUnguardedTestClient).
func NewPirateWeatherProvider(client *http.Client) Provider {
	if client == nil {
		client = newUnguardedTestClient()
	}
	return &PirateWeatherProvider{httpClient: client}
}

// NewTempestProvider creates a new Tempest/WeatherFlow local UDP provider.
// Unlike the HTTP-based providers above, it takes no shared http.Client:
// observations arrive via an unauthenticated local UDP broadcast rather than
// an outbound HTTP request (see provider_tempest.go). listenAddress is the
// local UDP address to listen on; an empty string defaults to ":50222",
// WeatherFlow's fixed, non-configurable broadcast port.
func NewTempestProvider(listenAddress string) Provider {
	return &TempestProvider{listenAddress: listenAddress}
}

// Provider implementations
type YrNoProvider struct {
	httpClient   *http.Client
	mu           sync.Mutex
	lastModified string
}

// OpenWeatherProvider implements the Provider interface for OpenWeather.
type OpenWeatherProvider struct {
	httpClient *http.Client
}

// WundergroundProvider implements the Provider interface for WeatherUnderground
type WundergroundProvider struct {
	httpClient *http.Client
}

// PirateWeatherProvider implements the Provider interface for Pirate Weather.
type PirateWeatherProvider struct {
	httpClient *http.Client
}

// TempestProvider implements the Provider and Lifecycler interfaces for a
// local Tempest/WeatherFlow weather station. See provider_tempest.go for the
// UDP listener, packet parsing, and FetchWeather implementation.
type TempestProvider struct {
	listenAddress string

	startOnce sync.Once

	mu         sync.RWMutex
	latest     *WeatherData
	receivedAt time.Time
	// extras holds sensor readings that have no WeatherData equivalent
	// (illuminance, UV, solar radiation, lightning). Selected fields are
	// exposed live and persisted with hourly weather rows.
	extras *TempestExtras
}
