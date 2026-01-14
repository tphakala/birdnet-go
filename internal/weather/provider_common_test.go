package weather

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestFahrenheitToCelsius(t *testing.T) {
	tests := []struct {
		name       string
		fahrenheit float64
		wantC      float64
	}{
		{
			name:       "freezing point",
			fahrenheit: 32.0,
			wantC:      0.0,
		},
		{
			name:       "boiling point",
			fahrenheit: 212.0,
			wantC:      100.0,
		},
		{
			name:       "room temperature",
			fahrenheit: 68.0,
			wantC:      20.0,
		},
		{
			name:       "body temperature",
			fahrenheit: 98.6,
			wantC:      37.0,
		},
		{
			name:       "negative fahrenheit",
			fahrenheit: -40.0,
			wantC:      -40.0, // -40 is same in both scales
		},
		{
			name:       "absolute zero fahrenheit",
			fahrenheit: -459.67,
			wantC:      -273.15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FahrenheitToCelsius(tt.fahrenheit)
			assert.InDelta(t, tt.wantC, got, 0.01, "FahrenheitToCelsius(%v) = %v, want %v", tt.fahrenheit, got, tt.wantC)
		})
	}
}

func TestKelvinToCelsius(t *testing.T) {
	tests := []struct {
		name   string
		kelvin float64
		wantC  float64
	}{
		{
			name:   "absolute zero",
			kelvin: 0.0,
			wantC:  -273.15,
		},
		{
			name:   "freezing point of water",
			kelvin: 273.15,
			wantC:  0.0,
		},
		{
			name:   "boiling point of water",
			kelvin: 373.15,
			wantC:  100.0,
		},
		{
			name:   "room temperature",
			kelvin: 293.15,
			wantC:  20.0,
		},
		{
			name:   "typical summer day",
			kelvin: 298.15,
			wantC:  25.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := KelvinToCelsius(tt.kelvin)
			assert.InDelta(t, tt.wantC, got, 0.001, "KelvinToCelsius(%v) = %v, want %v", tt.kelvin, got, tt.wantC)
		})
	}
}

func TestTemperatureConversionRoundTrip(t *testing.T) {
	// Test that conversions are mathematically consistent
	// Fahrenheit -> Celsius -> back should be close to original
	tests := []float64{-40, 0, 32, 68, 100, 212}

	for _, f := range tests {
		t.Run(fmt.Sprintf("%.0fF", f), func(t *testing.T) {
			c := FahrenheitToCelsius(f)
			// Convert back: C * 9/5 + 32 = F
			backToF := c*9.0/5.0 + 32.0
			assert.InDelta(t, f, backToF, 0.001, "Round trip conversion failed for %v°F", f)
		})
	}
}

func TestKelvinCelsiusConsistency(t *testing.T) {
	// Verify that Kelvin conversion is consistent with known reference points
	t.Run("water triple point", func(t *testing.T) {
		// Water triple point is exactly 273.16 K = 0.01°C
		got := KelvinToCelsius(273.16)
		assert.InDelta(t, 0.01, got, 0.001)
	})

	t.Run("standard temperature", func(t *testing.T) {
		// Standard temperature is 298.15 K = 25°C
		got := KelvinToCelsius(298.15)
		assert.InDelta(t, 25.0, got, 0.001)
	})
}

func TestNewWeatherError(t *testing.T) {
	baseErr := fmt.Errorf("connection timeout")

	err := newWeatherError(baseErr, errors.CategoryNetwork, "fetch_data", "openweather")

	require.Error(t, err)

	// Verify the error message contains expected information
	errStr := err.Error()
	assert.Contains(t, errStr, "connection timeout")
}

func TestNewWeatherErrorWithRetries(t *testing.T) {
	baseErr := fmt.Errorf("server unavailable")

	err := newWeatherErrorWithRetries(baseErr, errors.CategoryNetwork, "api_request", "yrno")

	require.Error(t, err)

	// Verify the error message contains expected information
	errStr := err.Error()
	assert.Contains(t, errStr, "server unavailable")
}

func TestConstants(t *testing.T) {
	// Verify that constants are set to expected values
	t.Run("request timeout is reasonable", func(t *testing.T) {
		assert.GreaterOrEqual(t, RequestTimeout.Seconds(), 5.0, "Request timeout should be at least 5 seconds")
		assert.LessOrEqual(t, RequestTimeout.Seconds(), 30.0, "Request timeout should be at most 30 seconds")
	})

	t.Run("max retries is reasonable", func(t *testing.T) {
		assert.GreaterOrEqual(t, MaxRetries, 1, "Should have at least 1 retry")
		assert.LessOrEqual(t, MaxRetries, 5, "Should have at most 5 retries")
	})

	t.Run("retry delay is reasonable", func(t *testing.T) {
		assert.GreaterOrEqual(t, RetryDelay.Seconds(), 1.0, "Retry delay should be at least 1 second")
		assert.LessOrEqual(t, RetryDelay.Seconds(), 10.0, "Retry delay should be at most 10 seconds")
	})

	t.Run("user agent is set", func(t *testing.T) {
		assert.NotEmpty(t, UserAgent)
		assert.Contains(t, UserAgent, "BirdNET-Go")
	})
}

func TestTemperatureEdgeCases(t *testing.T) {
	t.Run("fahrenheit handles NaN", func(t *testing.T) {
		result := FahrenheitToCelsius(math.NaN())
		assert.True(t, math.IsNaN(result), "NaN input should produce NaN output")
	})

	t.Run("fahrenheit handles positive infinity", func(t *testing.T) {
		result := FahrenheitToCelsius(math.Inf(1))
		assert.True(t, math.IsInf(result, 1), "Positive infinity should produce positive infinity")
	})

	t.Run("fahrenheit handles negative infinity", func(t *testing.T) {
		result := FahrenheitToCelsius(math.Inf(-1))
		assert.True(t, math.IsInf(result, -1), "Negative infinity should produce negative infinity")
	})

	t.Run("kelvin handles NaN", func(t *testing.T) {
		result := KelvinToCelsius(math.NaN())
		assert.True(t, math.IsNaN(result), "NaN input should produce NaN output")
	})

	t.Run("kelvin handles positive infinity", func(t *testing.T) {
		result := KelvinToCelsius(math.Inf(1))
		assert.True(t, math.IsInf(result, 1), "Positive infinity should produce positive infinity")
	})
}
