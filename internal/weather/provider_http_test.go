package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestMaskURLForLog verifies that the consolidated log-redaction helper masks
// both the API key (appid/apiKey) and the location coordinates (lat/lon), while
// preserving non-sensitive query parameters. Coordinates are PII, so they must
// never reach a log sink in cleartext.
func TestMaskURLForLog(t *testing.T) {
	t.Parallel()

	const secret = "secret123"

	tests := []struct {
		name           string
		url            string
		mustNotContain []string
		mustContain    []string
	}{
		{
			name:           "openweather masks key and coordinates, keeps units",
			url:            "https://api.openweathermap.org/data/2.5/weather?lat=60.170&lon=24.938&appid=" + secret + "&units=metric&lang=en",
			mustNotContain: []string{secret, "60.170", "24.938"},
			mustContain:    []string{"api.openweathermap.org", "MASKED", "units=metric", "lang=en"},
		},
		{
			name:           "wunderground masks apiKey, keeps stationId",
			url:            "https://api.weather.com/v2/pws/observations/current?stationId=KTEST123&format=json&units=m&apiKey=" + secret + "&numericPrecision=decimal",
			mustNotContain: []string{secret},
			mustContain:    []string{"api.weather.com", "MASKED", "stationId=KTEST123", "format=json"},
		},
		{
			name:           "yrno masks bare coordinates",
			url:            "https://api.met.no/weatherapi/locationforecast/2.0/complete?lat=60.170&lon=24.938",
			mustNotContain: []string{"60.170", "24.938"},
			mustContain:    []string{"api.met.no", "MASKED"},
		},
		{
			name:        "url without query is unchanged",
			url:         "https://api.example.com/path",
			mustContain: []string{"https://api.example.com/path"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := maskURLForLog(tt.url)
			for _, s := range tt.mustNotContain {
				assert.NotContains(t, got, s, "masked URL must not leak %q", s)
			}
			for _, s := range tt.mustContain {
				assert.Contains(t, got, s, "masked URL should contain %q", s)
			}
		})
	}
}

// TestSleepWithContext verifies the context-aware retry sleep: it waits for the
// full delay when the context stays live, and returns promptly with the context
// error when cancelled, so a shutdown aborts an in-progress retry backoff.
func TestSleepWithContext(t *testing.T) {
	t.Parallel()

	t.Run("returns nil after the delay elapses", func(t *testing.T) {
		t.Parallel()
		err := sleepWithContext(t.Context(), 10*time.Millisecond)
		require.NoError(t, err)
	})

	t.Run("returns ctx error promptly when already cancelled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // cancel before sleeping

		start := time.Now()
		err := sleepWithContext(ctx, time.Hour)

		require.ErrorIs(t, err, context.Canceled)
		assert.Less(t, time.Since(start), time.Second, "must return promptly on cancellation, not wait the full delay")
	})
}

// TestFetchWeather_ContextCancelAbortsRequest proves the end-to-end #988
// behavior: cancelling the context aborts an in-flight provider fetch promptly
// instead of waiting out the per-request timeout or the retry budget. It points
// the provider at a real server that blocks until the request is cancelled, so
// the real transport (not httpmock) honors the cancellation and the shared
// executor returns context.Canceled.
func TestFetchWeather_ContextCancelAbortsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // block until the client cancels the request
	}))
	defer srv.Close()

	provider := NewOpenWeatherProvider(nil)
	settings := createTestSettings(t, "openweather", func(s *conf.Settings) {
		s.Realtime.Weather.OpenWeather.Endpoint = srv.URL
	})

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	data, err := provider.FetchWeather(ctx, settings)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Nil(t, data)
	require.ErrorIs(t, err, context.Canceled, "a cancelled context must surface as context.Canceled")
	assert.Less(t, elapsed, RequestTimeout, "cancel must abort the fetch promptly, not wait out the timeout or retries")
}
