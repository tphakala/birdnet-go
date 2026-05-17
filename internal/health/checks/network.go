package checks

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// MQTTCheck verifies connectivity to the MQTT broker when MQTT is enabled.
type MQTTCheck struct {
	isEnabled   func() bool
	isConnected func() bool
}

// NewMQTTCheck creates an MQTTCheck using the given enable and connection predicates.
func NewMQTTCheck(isEnabled, isConnected func() bool) *MQTTCheck {
	return &MQTTCheck{isEnabled: isEnabled, isConnected: isConnected}
}

// Name returns the check identifier.
func (c *MQTTCheck) Name() string { return "mqtt" }

// Category returns the network category.
func (c *MQTTCheck) Category() health.Category { return health.CategoryNetwork }

// Run verifies MQTT broker connectivity when MQTT is enabled.
func (c *MQTTCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if !c.isEnabled() {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusSkipped,
			Message:    "MQTT is disabled",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	if !c.isConnected() {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusWarning,
			Message:    "MQTT broker is not connected",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     health.StatusHealthy,
		Message:    "MQTT broker connected",
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// BirdWeatherCheck verifies connectivity to the BirdWeather service when it is enabled.
type BirdWeatherCheck struct {
	isEnabled func() bool
	getStatus func() (bool, string)
}

// NewBirdWeatherCheck creates a BirdWeatherCheck using the given enable predicate and status provider.
// getStatus must return (connected, statusMessage).
func NewBirdWeatherCheck(isEnabled func() bool, getStatus func() (bool, string)) *BirdWeatherCheck {
	return &BirdWeatherCheck{isEnabled: isEnabled, getStatus: getStatus}
}

// Name returns the check identifier.
func (c *BirdWeatherCheck) Name() string { return "birdweather" }

// Category returns the network category.
func (c *BirdWeatherCheck) Category() health.Category { return health.CategoryNetwork }

// Run verifies BirdWeather service connectivity when the integration is enabled.
func (c *BirdWeatherCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if !c.isEnabled() {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusSkipped,
			Message:    "BirdWeather integration is disabled",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	connected, statusMsg := c.getStatus()
	if !connected {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusWarning,
			Message:    statusMsg,
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     health.StatusHealthy,
		Message:    statusMsg,
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// NotificationProvidersCheck reports the connectivity state of configured notification providers.
// Probe infrastructure is not yet available; this check always returns StatusSkipped.
type NotificationProvidersCheck struct{}

// NewNotificationProvidersCheck creates a NotificationProvidersCheck.
func NewNotificationProvidersCheck() *NotificationProvidersCheck {
	return &NotificationProvidersCheck{}
}

// Name returns the check identifier.
func (c *NotificationProvidersCheck) Name() string { return "notification_providers" }

// Category returns the network category.
func (c *NotificationProvidersCheck) Category() health.Category { return health.CategoryNetwork }

// Run returns StatusSkipped because notification probe infrastructure is not yet available.
func (c *NotificationProvidersCheck) Run(_ context.Context) health.Result {
	return skippedResult(c.Name(), c.Category(), time.Now())
}

// WeatherCheck verifies connectivity to the configured weather provider.
// Probe infrastructure is not yet available; enabled instances return StatusSkipped.
type WeatherCheck struct {
	isEnabled func() bool
}

// NewWeatherCheck creates a WeatherCheck using the given enable predicate.
func NewWeatherCheck(isEnabled func() bool) *WeatherCheck {
	return &WeatherCheck{isEnabled: isEnabled}
}

// Name returns the check identifier.
func (c *WeatherCheck) Name() string { return "weather" }

// Category returns the network category.
func (c *WeatherCheck) Category() health.Category { return health.CategoryNetwork }

// Run returns StatusSkipped when disabled, or StatusSkipped with a not-yet-available message when enabled.
func (c *WeatherCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if !c.isEnabled() {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusSkipped,
			Message:    "Weather integration is disabled",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     health.StatusSkipped,
		Message:    "Probe infrastructure not yet available",
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
