package checks

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/health"
)

func TestBirdWeatherCheck_NilGetStatus(t *testing.T) {
	t.Parallel()
	check := NewBirdWeatherCheck(func() bool { return true }, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestBirdWeatherCheck_Disabled(t *testing.T) {
	t.Parallel()
	check := NewBirdWeatherCheck(func() bool { return false }, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Contains(t, result.Message, "disabled")
}

func TestBirdWeatherCheck_Connected(t *testing.T) {
	t.Parallel()
	check := NewBirdWeatherCheck(
		func() bool { return true },
		func() (bool, string) { return true, "BirdWeather API connected" },
	)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
}

func TestBirdWeatherCheck_Disconnected(t *testing.T) {
	t.Parallel()
	check := NewBirdWeatherCheck(
		func() bool { return true },
		func() (bool, string) { return false, "circuit open" },
	)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
}

func TestMQTTCheck_NilEnabled(t *testing.T) {
	t.Parallel()
	check := NewMQTTCheck(nil, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestMQTTCheck_Connected(t *testing.T) {
	t.Parallel()
	check := NewMQTTCheck(func() bool { return true }, func() bool { return true })
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
}

func TestNotificationProvidersCheck_NilClosure(t *testing.T) {
	t.Parallel()
	check := NewNotificationProvidersCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestNotificationProvidersCheck_HealthCheckDisabled(t *testing.T) {
	t.Parallel()
	check := NewNotificationProvidersCheck(func() (int, int, string) {
		return 0, 0, ""
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Contains(t, result.Message, "health checks disabled")
}

func TestNotificationProvidersCheck_AllHealthy(t *testing.T) {
	t.Parallel()
	check := NewNotificationProvidersCheck(func() (int, int, string) {
		return 3, 3, "All 3 providers healthy"
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "All 3 providers healthy")
}

func TestNotificationProvidersCheck_SomeFailing(t *testing.T) {
	t.Parallel()
	check := NewNotificationProvidersCheck(func() (int, int, string) {
		return 3, 1, "2 of 3 providers unhealthy"
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
}

func TestNotificationProvidersCheck_AllFailing(t *testing.T) {
	t.Parallel()
	check := NewNotificationProvidersCheck(func() (int, int, string) {
		return 2, 0, "All 2 providers failing"
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status)
}

func TestWeatherCheck_NilEnabled(t *testing.T) {
	t.Parallel()
	check := NewWeatherCheck(nil, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestWeatherCheck_Disabled(t *testing.T) {
	t.Parallel()
	check := NewWeatherCheck(func() bool { return false }, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Contains(t, result.Message, "disabled")
}

func TestWeatherCheck_NilGetStatus(t *testing.T) {
	t.Parallel()
	check := NewWeatherCheck(func() bool { return true }, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestWeatherCheck_Healthy(t *testing.T) {
	t.Parallel()
	check := NewWeatherCheck(
		func() bool { return true },
		func() (bool, string) { return true, "Weather provider yrno healthy" },
	)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "healthy")
}

func TestWeatherCheck_Degraded(t *testing.T) {
	t.Parallel()
	check := NewWeatherCheck(
		func() bool { return true },
		func() (bool, string) {
			return false, fmt.Sprintf("Weather provider yrno backing off (%d failures)", 3)
		},
	)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "backing off")
}
