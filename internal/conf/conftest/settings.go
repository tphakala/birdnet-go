// Package conftest provides shared test-only helpers for constructing and
// publishing *conf.Settings instances. It lives in its own package (rather than
// in internal/conf) so that test helpers, which would otherwise be compiled
// into the production binary, stay out of non-test builds while remaining
// importable from other packages' tests.
package conftest

import "github.com/tphakala/birdnet-go/internal/conf"

// SetTestSettings allows tests to inject their own settings instance.
// Subsequent conf.GetSettings()/conf.Setting() calls observe the new snapshot
// atomically. Passing nil clears the snapshot, causing the next Setting() call
// to re-Load() from disk. Intended for testing only.
func SetTestSettings(settings *conf.Settings) {
	conf.StoreSettings(settings)
}

// GetTestSettings returns a copy of default settings suitable for testing.
// This creates isolated settings that won't affect the global configuration.
func GetTestSettings() *conf.Settings {
	settings := &conf.Settings{}

	// Initialize with defaults
	settings.Debug = false
	settings.Main.Name = "BirdNET-Go-Test"
	settings.Main.TimeAs24h = true

	// Set up minimal test configuration
	settings.BirdNET.Sensitivity = 1.0
	settings.BirdNET.Threshold = 0.8
	settings.BirdNET.Overlap = 0.0
	settings.BirdNET.Locale = "en"

	// Dashboard settings with thumbnails
	settings.Realtime.Dashboard.Thumbnails.Debug = false
	settings.Realtime.Dashboard.Thumbnails.Summary = false
	settings.Realtime.Dashboard.Thumbnails.Recent = true
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = "avicommons"
	settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = conf.RetentionPolicyNone

	// Other realtime settings
	settings.Realtime.Interval = 15
	settings.Realtime.ProcessingTime = false

	// Web server settings
	settings.WebServer.Enabled = false
	settings.WebServer.Port = "8080"
	settings.WebServer.LiveStream.BitRate = conf.DefaultLiveStreamBitRate
	settings.WebServer.LiveStream.SegmentLength = conf.DefaultLiveStreamSegmentLength
	settings.WebServer.LiveStream.SampleRate = conf.DefaultLiveStreamSampleRate

	// Security settings
	settings.Security.SessionSecret = "test-secret-for-unit-tests"
	settings.Security.SessionDuration = conf.DefaultSessionDuration

	// Weather settings
	settings.Realtime.Weather.PollInterval = conf.DefaultWeatherPollInterval

	// Output settings
	settings.Output.SQLite.Enabled = false
	settings.Output.SQLite.Path = ":memory:"

	return settings
}

// SettingsBuilder provides a fluent interface for constructing test settings.
// It simplifies test setup by providing convenient methods for common configuration patterns.
//
// Example usage:
//
//	settings := conftest.NewTestSettings().
//	    WithBirdNET(0.9, 45.0, -122.0).
//	    WithMQTT("tcp://localhost:1883", "test").
//	    Build()
type SettingsBuilder struct {
	settings *conf.Settings
}

// NewTestSettings creates a new SettingsBuilder initialized with default test settings.
func NewTestSettings() *SettingsBuilder {
	return &SettingsBuilder{
		settings: GetTestSettings(),
	}
}

// WithBirdNET configures BirdNET-specific settings.
func (b *SettingsBuilder) WithBirdNET(threshold, latitude, longitude float64) *SettingsBuilder {
	b.settings.BirdNET.Threshold = threshold
	b.settings.BirdNET.Latitude = latitude
	b.settings.BirdNET.Longitude = longitude
	return b
}

// WithMQTT configures MQTT settings and enables MQTT.
func (b *SettingsBuilder) WithMQTT(broker, topic string) *SettingsBuilder {
	b.settings.Realtime.MQTT.Enabled = true
	b.settings.Realtime.MQTT.Broker = broker
	b.settings.Realtime.MQTT.Topic = topic
	return b
}

// WithAudioExport configures audio export settings and enables audio export.
func (b *SettingsBuilder) WithAudioExport(path, exportType, bitrate string) *SettingsBuilder {
	b.settings.Realtime.Audio.Export.Enabled = true
	b.settings.Realtime.Audio.Export.Path = path
	b.settings.Realtime.Audio.Export.Type = exportType
	b.settings.Realtime.Audio.Export.Bitrate = bitrate
	return b
}

// WithSpeciesTracking configures species tracking settings and enables species tracking.
func (b *SettingsBuilder) WithSpeciesTracking(windowDays, syncInterval int) *SettingsBuilder {
	b.settings.Realtime.SpeciesTracking.Enabled = true
	b.settings.Realtime.SpeciesTracking.NewSpeciesWindowDays = windowDays
	b.settings.Realtime.SpeciesTracking.SyncIntervalMinutes = syncInterval
	return b
}

// WithRTSPHealthThreshold configures RTSP health monitoring threshold.
func (b *SettingsBuilder) WithRTSPHealthThreshold(seconds int) *SettingsBuilder {
	b.settings.Realtime.RTSP.Health.HealthyDataThreshold = seconds
	return b
}

// WithImageProvider configures thumbnail image provider settings.
func (b *SettingsBuilder) WithImageProvider(provider, fallbackPolicy string) *SettingsBuilder {
	b.settings.Realtime.Dashboard.Thumbnails.ImageProvider = provider
	b.settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = fallbackPolicy
	return b
}

// WithSpeciesGuideProvider configures species guide provider settings and enables the feature.
func (b *SettingsBuilder) WithSpeciesGuideProvider(provider, fallbackPolicy string) *SettingsBuilder {
	b.settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
	b.settings.Realtime.Dashboard.SpeciesGuide.Provider = provider
	b.settings.Realtime.Dashboard.SpeciesGuide.FallbackPolicy = fallbackPolicy
	return b
}

// WithWebServer configures web server settings.
func (b *SettingsBuilder) WithWebServer(port string, enabled bool) *SettingsBuilder {
	b.settings.WebServer.Port = port
	b.settings.WebServer.Enabled = enabled
	return b
}

// Build returns the constructed settings without modifying global state.
// Use this when you need the settings object for manual manipulation.
func (b *SettingsBuilder) Build() *conf.Settings {
	return b.settings
}

// Apply sets the built settings as the global test settings.
// This is equivalent to calling SetTestSettings() with the built settings.
func (b *SettingsBuilder) Apply() *conf.Settings {
	SetTestSettings(b.settings)
	return b.settings
}
