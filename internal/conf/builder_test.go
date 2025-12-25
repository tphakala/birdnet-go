package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSettingsBuilder_NewTestSettings verifies that NewTestSettings creates a valid builder.
func TestSettingsBuilder_NewTestSettings(t *testing.T) {
	t.Parallel()
	builder := NewTestSettings()

	require.NotNil(t, builder, "NewTestSettings() returned nil")
	require.NotNil(t, builder.settings, "NewTestSettings() builder has nil settings")

	// Verify settings have default values
	assert.NotZero(t, builder.settings.BirdNET.Threshold, "Expected non-zero default threshold")
}

// TestSettingsBuilder_WithBirdNET verifies BirdNET configuration.
func TestSettingsBuilder_WithBirdNET(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		threshold float64
		latitude  float64
		longitude float64
	}{
		{
			name:      "standard values",
			threshold: 0.7,
			latitude:  45.5,
			longitude: -122.6,
		},
		{
			name:      "zero threshold",
			threshold: 0.0,
			latitude:  0.0,
			longitude: 0.0,
		},
		{
			name:      "maximum threshold",
			threshold: 1.0,
			latitude:  90.0,
			longitude: 180.0,
		},
		{
			name:      "negative coordinates",
			threshold: 0.5,
			latitude:  -33.9,
			longitude: -73.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := NewTestSettings().
				WithBirdNET(tt.threshold, tt.latitude, tt.longitude).
				Build()

			assert.InDelta(t, tt.threshold, settings.BirdNET.Threshold, 0.0001)
			assert.InDelta(t, tt.latitude, settings.BirdNET.Latitude, 0.0001)
			assert.InDelta(t, tt.longitude, settings.BirdNET.Longitude, 0.0001)
		})
	}
}

// TestSettingsBuilder_WithMQTT verifies MQTT configuration.
//
//nolint:dupl // Similar test structure but tests different builder methods
func TestSettingsBuilder_WithMQTT(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		broker string
		topic  string
	}{
		{
			name:   "standard MQTT settings",
			broker: "tcp://localhost:1883",
			topic:  "birdnet/detections",
		},
		{
			name:   "secure MQTT",
			broker: "ssl://broker.hivemq.com:8883",
			topic:  "birds/detected",
		},
		{
			name:   "empty values",
			broker: "",
			topic:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := NewTestSettings().
				WithMQTT(tt.broker, tt.topic).
				Build()

			assert.True(t, settings.Realtime.MQTT.Enabled, "Expected MQTT to be enabled")
			assert.Equal(t, tt.broker, settings.Realtime.MQTT.Broker)
			assert.Equal(t, tt.topic, settings.Realtime.MQTT.Topic)
		})
	}
}

// TestSettingsBuilder_WithAudioExport verifies audio export configuration.
func TestSettingsBuilder_WithAudioExport(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		path       string
		exportType string
		bitrate    string
	}{
		{
			name:       "MP3 export",
			path:       "/tmp/audio",
			exportType: "mp3",
			bitrate:    "192k",
		},
		{
			name:       "WAV export",
			path:       "/var/lib/audio",
			exportType: "wav",
			bitrate:    "256k",
		},
		{
			name:       "FLAC export",
			path:       "/data/exports",
			exportType: "flac",
			bitrate:    "320k",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := NewTestSettings().
				WithAudioExport(tt.path, tt.exportType, tt.bitrate).
				Build()

			assert.True(t, settings.Realtime.Audio.Export.Enabled, "Expected audio export to be enabled")
			assert.Equal(t, tt.path, settings.Realtime.Audio.Export.Path)
			assert.Equal(t, tt.exportType, settings.Realtime.Audio.Export.Type)
			assert.Equal(t, tt.bitrate, settings.Realtime.Audio.Export.Bitrate)
		})
	}
}

// TestSettingsBuilder_WithSpeciesTracking verifies species tracking configuration.
//
//nolint:dupl // Similar test structure but tests different builder methods
func TestSettingsBuilder_WithSpeciesTracking(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		windowDays   int
		syncInterval int
	}{
		{
			name:         "standard tracking",
			windowDays:   7,
			syncInterval: 3600,
		},
		{
			name:         "long window",
			windowDays:   30,
			syncInterval: 7200,
		},
		{
			name:         "zero values",
			windowDays:   0,
			syncInterval: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := NewTestSettings().
				WithSpeciesTracking(tt.windowDays, tt.syncInterval).
				Build()

			assert.True(t, settings.Realtime.SpeciesTracking.Enabled, "Expected species tracking to be enabled")
			assert.Equal(t, tt.windowDays, settings.Realtime.SpeciesTracking.NewSpeciesWindowDays)
			assert.Equal(t, tt.syncInterval, settings.Realtime.SpeciesTracking.SyncIntervalMinutes)
		})
	}
}

// TestSettingsBuilder_WithRTSPHealthThreshold verifies RTSP health threshold configuration.
func TestSettingsBuilder_WithRTSPHealthThreshold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		seconds int
	}{
		{"30 seconds", 30},
		{"60 seconds", 60},
		{"120 seconds", 120},
		{"zero seconds", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := NewTestSettings().
				WithRTSPHealthThreshold(tt.seconds).
				Build()

			assert.Equal(t, tt.seconds, settings.Realtime.RTSP.Health.HealthyDataThreshold)
		})
	}
}

// TestSettingsBuilder_WithImageProvider verifies image provider configuration.
func TestSettingsBuilder_WithImageProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		provider       string
		fallbackPolicy string
	}{
		{
			name:           "wikimedia with fallback",
			provider:       "wikimedia",
			fallbackPolicy: "all",
		},
		{
			name:           "avicommons no fallback",
			provider:       "avicommons",
			fallbackPolicy: "none",
		},
		{
			name:           "auto with fallback",
			provider:       "auto",
			fallbackPolicy: "all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := NewTestSettings().
				WithImageProvider(tt.provider, tt.fallbackPolicy).
				Build()

			assert.Equal(t, tt.provider, settings.Realtime.Dashboard.Thumbnails.ImageProvider)
			assert.Equal(t, tt.fallbackPolicy, settings.Realtime.Dashboard.Thumbnails.FallbackPolicy)
		})
	}
}

// TestSettingsBuilder_WithSecurity verifies security configuration.
//
//nolint:dupl // Similar test structure but tests different builder methods
func TestSettingsBuilder_WithSecurity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		host    string
		autoTLS bool
	}{
		{
			name:    "localhost no TLS",
			host:    "localhost",
			autoTLS: false,
		},
		{
			name:    "domain with auto TLS",
			host:    "birdnet.example.com",
			autoTLS: true,
		},
		{
			name:    "IP address no TLS",
			host:    "192.168.1.100",
			autoTLS: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := NewTestSettings().
				WithSecurity(tt.host, tt.autoTLS).
				Build()

			assert.Equal(t, tt.host, settings.Security.Host)
			assert.Equal(t, tt.autoTLS, settings.Security.AutoTLS)
		})
	}
}

// TestSettingsBuilder_WithWebServer verifies web server configuration.
//
//nolint:dupl // Similar test structure but tests different builder methods
func TestSettingsBuilder_WithWebServer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		port    string
		enabled bool
	}{
		{
			name:    "standard port enabled",
			port:    "8080",
			enabled: true,
		},
		{
			name:    "custom port disabled",
			port:    "3000",
			enabled: false,
		},
		{
			name:    "https port enabled",
			port:    "443",
			enabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := NewTestSettings().
				WithWebServer(tt.port, tt.enabled).
				Build()

			assert.Equal(t, tt.port, settings.WebServer.Port)
			assert.Equal(t, tt.enabled, settings.WebServer.Enabled)
		})
	}
}

// TestSettingsBuilder_MethodChaining verifies that builder methods can be chained.
func TestSettingsBuilder_MethodChaining(t *testing.T) {
	t.Parallel()
	settings := NewTestSettings().
		WithBirdNET(0.8, 45.0, -122.0).
		WithMQTT("tcp://localhost:1883", "test/topic").
		WithAudioExport("/tmp/audio", "mp3", "192k").
		WithWebServer("8080", true).
		Build()

	// Verify all settings were applied
	assert.InDelta(t, 0.8, settings.BirdNET.Threshold, 0.0001)
	assert.True(t, settings.Realtime.MQTT.Enabled, "Expected MQTT enabled")
	assert.True(t, settings.Realtime.Audio.Export.Enabled, "Expected audio export enabled")
	assert.Equal(t, "8080", settings.WebServer.Port)
}

// TestSettingsBuilder_Build verifies that Build returns a valid settings object.
func TestSettingsBuilder_Build(t *testing.T) {
	t.Parallel()
	settings := NewTestSettings().
		WithBirdNET(0.7, 40.0, -100.0).
		Build()

	require.NotNil(t, settings, "Build() returned nil")

	// Verify it's a separate copy (not just a reference)
	settings.BirdNET.Threshold = 0.9

	newSettings := NewTestSettings().
		WithBirdNET(0.7, 40.0, -100.0).
		Build()

	assert.InDelta(t, 0.7, newSettings.BirdNET.Threshold, 0.0001, "Build() appears to return shared state")
}

// TestSettingsBuilder_Apply verifies that Apply sets global test settings.
func TestSettingsBuilder_Apply(t *testing.T) {
	// Store original settings
	originalSettings := GetSettings()
	t.Cleanup(func() {
		SetTestSettings(originalSettings)
	})

	// Apply new settings
	appliedSettings := NewTestSettings().
		WithBirdNET(0.75, 50.0, -120.0).
		Apply()

	// Verify settings were applied globally
	currentSettings := GetSettings()

	assert.InDelta(t, 0.75, currentSettings.BirdNET.Threshold, 0.0001, "Expected global threshold to be applied")
	assert.InDelta(t, 0.75, appliedSettings.BirdNET.Threshold, 0.0001, "Expected returned threshold to match")
}

// BenchmarkSettingsBuilder_Build benchmarks the builder Build operation.
func BenchmarkSettingsBuilder_Build(b *testing.B) {
	builder := NewTestSettings().
		WithBirdNET(0.7, 45.0, -122.0).
		WithMQTT("tcp://localhost:1883", "test/topic")

	b.ReportAllocs()

	for b.Loop() {
		_ = builder.Build()
	}
}

// BenchmarkSettingsBuilder_FullChain benchmarks a complete builder chain.
func BenchmarkSettingsBuilder_FullChain(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		_ = NewTestSettings().
			WithBirdNET(0.8, 45.0, -122.0).
			WithMQTT("tcp://localhost:1883", "test").
			WithAudioExport("/tmp/audio", "mp3", "192k").
			WithSpeciesTracking(7, 3600).
			WithWebServer("8080", true).
			Build()
	}
}
