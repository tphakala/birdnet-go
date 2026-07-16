package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
)

func TestTemperatureCheck_Run(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		celsius     float64
		unit        string // "" => nil getUnit provider
		nilUnit     bool
		wantStatus  health.Status
		wantMessage string
		wantDisplay float64
		wantSymbol  string
	}{
		{
			name:        "celsius healthy",
			celsius:     45.0,
			unit:        tempUnitCelsius,
			wantStatus:  health.StatusHealthy,
			wantMessage: "Temperature OK (45.0 C)",
			wantDisplay: 45.0,
			wantSymbol:  "C",
		},
		{
			name:        "nil unit provider defaults to celsius",
			celsius:     45.0,
			nilUnit:     true,
			wantStatus:  health.StatusHealthy,
			wantMessage: "Temperature OK (45.0 C)",
			wantDisplay: 45.0,
			wantSymbol:  "C",
		},
		{
			name:        "fahrenheit healthy converts display only",
			celsius:     45.0,
			unit:        tempUnitFahrenheit,
			wantStatus:  health.StatusHealthy,
			wantMessage: "Temperature OK (113.0 F)",
			wantDisplay: 113.0,
			wantSymbol:  "F",
		},
		{
			// 80C is above the 75C warn threshold but below the 85C critical
			// threshold. In Fahrenheit that is 176F; if the thresholds were
			// wrongly compared in Fahrenheit this would read Critical. Asserting
			// Warning proves the comparison stays in Celsius.
			name:        "fahrenheit warning still compared in celsius",
			celsius:     80.0,
			unit:        tempUnitFahrenheit,
			wantStatus:  health.StatusWarning,
			wantMessage: "Temperature high: 176.0 F",
			wantDisplay: 176.0,
			wantSymbol:  "F",
		},
		{
			name:        "celsius critical",
			celsius:     90.0,
			unit:        tempUnitCelsius,
			wantStatus:  health.StatusCritical,
			wantMessage: "Temperature critical: 90.0 C",
			wantDisplay: 90.0,
			wantSymbol:  "C",
		},
		// Threshold boundaries: the switch uses tempC >= 75 (warn) and
		// tempC >= 85 (critical). Exact-boundary and just-below cases lock the
		// >= comparisons against an off-by-one regression.
		{
			name:        "just below warn stays healthy",
			celsius:     74.9,
			unit:        tempUnitCelsius,
			wantStatus:  health.StatusHealthy,
			wantMessage: "Temperature OK (74.9 C)",
			wantDisplay: 74.9,
			wantSymbol:  "C",
		},
		{
			name:        "exact warn boundary is warning",
			celsius:     75.0,
			unit:        tempUnitCelsius,
			wantStatus:  health.StatusWarning,
			wantMessage: "Temperature high: 75.0 C",
			wantDisplay: 75.0,
			wantSymbol:  "C",
		},
		{
			name:        "just below critical stays warning",
			celsius:     84.9,
			unit:        tempUnitCelsius,
			wantStatus:  health.StatusWarning,
			wantMessage: "Temperature high: 84.9 C",
			wantDisplay: 84.9,
			wantSymbol:  "C",
		},
		{
			name:        "exact critical boundary is critical",
			celsius:     85.0,
			unit:        tempUnitCelsius,
			wantStatus:  health.StatusCritical,
			wantMessage: "Temperature critical: 85.0 C",
			wantDisplay: 85.0,
			wantSymbol:  "C",
		},
		{
			// An unrecognized/empty unit falls back to Celsius, matching the
			// dashboard default.
			name:        "unknown unit falls back to celsius",
			celsius:     45.0,
			unit:        "kelvin",
			wantStatus:  health.StatusHealthy,
			wantMessage: "Temperature OK (45.0 C)",
			wantDisplay: 45.0,
			wantSymbol:  "C",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var getUnit func() string
			if !tt.nilUnit {
				unit := tt.unit
				getUnit = func() string { return unit }
			}
			check := NewTemperatureCheck(func() (float64, error) { return tt.celsius, nil }, getUnit)

			result := check.Run(t.Context())

			assert.Equal(t, "temperature", result.Name)
			assert.Equal(t, health.CategorySystem, result.Category)
			assert.Equal(t, tt.wantStatus, result.Status)
			assert.Equal(t, tt.wantMessage, result.Message)

			// temperature_c is always the raw Celsius reading, regardless of unit.
			require.Contains(t, result.Details, "temperature_c")
			assert.InDelta(t, tt.celsius, result.Details["temperature_c"], 0.0001)

			require.Contains(t, result.Details, "temperature_display")
			assert.InDelta(t, tt.wantDisplay, result.Details["temperature_display"], 0.001)

			assert.Equal(t, tt.wantSymbol, result.Details["temperature_unit"])
		})
	}
}

func TestTemperatureCheck_Run_NilProvider(t *testing.T) {
	t.Parallel()

	check := NewTemperatureCheck(nil, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestTemperatureCheck_Run_Error(t *testing.T) {
	t.Parallel()

	check := NewTemperatureCheck(
		func() (float64, error) { return 0, errors.NewStd("no sensor") },
		func() string { return tempUnitCelsius },
	)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusUnknown, result.Status)
	assert.Contains(t, result.Message, "Temperature unavailable")
}

// TestTemperatureCheck_Run_UnitHotReload proves the display unit is read per Run,
// not captured at construction: flipping the unit between two Run calls must
// change the rendered symbol without recreating the check.
func TestTemperatureCheck_Run_UnitHotReload(t *testing.T) {
	t.Parallel()

	unit := tempUnitCelsius
	check := NewTemperatureCheck(
		func() (float64, error) { return 45.0, nil },
		func() string { return unit },
	)

	first := check.Run(t.Context())
	assert.Equal(t, "Temperature OK (45.0 C)", first.Message)
	assert.Equal(t, "C", first.Details["temperature_unit"])

	unit = tempUnitFahrenheit // simulate a live settings change
	second := check.Run(t.Context())
	assert.Equal(t, "Temperature OK (113.0 F)", second.Message)
	assert.Equal(t, "F", second.Details["temperature_unit"])
}
