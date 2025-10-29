package conf

import (
	"testing"
)

// TestSettingsBuilder_NewTestSettings verifies that NewTestSettings creates a valid builder.
func TestSettingsBuilder_NewTestSettings(t *testing.T) {
	t.Parallel()
	builder := NewTestSettings()

	if builder == nil {
		t.Fatal("NewTestSettings() returned nil")
	}

	if builder.settings == nil {
		t.Fatal("NewTestSettings() builder has nil settings")
	}

	// Verify settings have default values
	if builder.settings.BirdNET.Threshold == 0 {
		t.Error("Expected non-zero default threshold")
	}
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

			if settings.BirdNET.Threshold != tt.threshold {
				t.Errorf("Expected threshold %v, got %v", tt.threshold, settings.BirdNET.Threshold)
			}
			if settings.BirdNET.Latitude != tt.latitude {
				t.Errorf("Expected latitude %v, got %v", tt.latitude, settings.BirdNET.Latitude)
			}
			if settings.BirdNET.Longitude != tt.longitude {
				t.Errorf("Expected longitude %v, got %v", tt.longitude, settings.BirdNET.Longitude)
			}
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

			if !settings.Realtime.MQTT.Enabled {
				t.Error("Expected MQTT to be enabled")
			}
			if settings.Realtime.MQTT.Broker != tt.broker {
				t.Errorf("Expected broker %q, got %q", tt.broker, settings.Realtime.MQTT.Broker)
			}
			if settings.Realtime.MQTT.Topic != tt.topic {
				t.Errorf("Expected topic %q, got %q", tt.topic, settings.Realtime.MQTT.Topic)
			}
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

			if !settings.Realtime.Audio.Export.Enabled {
				t.Error("Expected audio export to be enabled")
			}
			if settings.Realtime.Audio.Export.Path != tt.path {
				t.Errorf("Expected path %q, got %q", tt.path, settings.Realtime.Audio.Export.Path)
			}
			if settings.Realtime.Audio.Export.Type != tt.exportType {
				t.Errorf("Expected type %q, got %q", tt.exportType, settings.Realtime.Audio.Export.Type)
			}
			if settings.Realtime.Audio.Export.Bitrate != tt.bitrate {
				t.Errorf("Expected bitrate %q, got %q", tt.bitrate, settings.Realtime.Audio.Export.Bitrate)
			}
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

			if !settings.Realtime.SpeciesTracking.Enabled {
				t.Error("Expected species tracking to be enabled")
			}
			if settings.Realtime.SpeciesTracking.NewSpeciesWindowDays != tt.windowDays {
				t.Errorf("Expected windowDays %d, got %d", tt.windowDays, settings.Realtime.SpeciesTracking.NewSpeciesWindowDays)
			}
			if settings.Realtime.SpeciesTracking.SyncIntervalMinutes != tt.syncInterval {
				t.Errorf("Expected syncInterval %d, got %d", tt.syncInterval, settings.Realtime.SpeciesTracking.SyncIntervalMinutes)
			}
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

			if settings.Realtime.RTSP.Health.HealthyDataThreshold != tt.seconds {
				t.Errorf("Expected threshold %d, got %d", tt.seconds, settings.Realtime.RTSP.Health.HealthyDataThreshold)
			}
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

			if settings.Realtime.Dashboard.Thumbnails.ImageProvider != tt.provider {
				t.Errorf("Expected provider %q, got %q", tt.provider, settings.Realtime.Dashboard.Thumbnails.ImageProvider)
			}
			if settings.Realtime.Dashboard.Thumbnails.FallbackPolicy != tt.fallbackPolicy {
				t.Errorf("Expected fallbackPolicy %q, got %q", tt.fallbackPolicy, settings.Realtime.Dashboard.Thumbnails.FallbackPolicy)
			}
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

			if settings.Security.Host != tt.host {
				t.Errorf("Expected host %q, got %q", tt.host, settings.Security.Host)
			}
			if settings.Security.AutoTLS != tt.autoTLS {
				t.Errorf("Expected autoTLS %v, got %v", tt.autoTLS, settings.Security.AutoTLS)
			}
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

			if settings.WebServer.Port != tt.port {
				t.Errorf("Expected port %q, got %q", tt.port, settings.WebServer.Port)
			}
			if settings.WebServer.Enabled != tt.enabled {
				t.Errorf("Expected enabled %v, got %v", tt.enabled, settings.WebServer.Enabled)
			}
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
	if settings.BirdNET.Threshold != 0.8 {
		t.Errorf("Expected threshold 0.8, got %v", settings.BirdNET.Threshold)
	}
	if !settings.Realtime.MQTT.Enabled {
		t.Error("Expected MQTT enabled")
	}
	if !settings.Realtime.Audio.Export.Enabled {
		t.Error("Expected audio export enabled")
	}
	if settings.WebServer.Port != "8080" {
		t.Errorf("Expected port 8080, got %q", settings.WebServer.Port)
	}
}

// TestSettingsBuilder_Build verifies that Build returns a valid settings object.
func TestSettingsBuilder_Build(t *testing.T) {
	t.Parallel()
	settings := NewTestSettings().
		WithBirdNET(0.7, 40.0, -100.0).
		Build()

	if settings == nil {
		t.Fatal("Build() returned nil")
	}

	// Verify it's a separate copy (not just a reference)
	settings.BirdNET.Threshold = 0.9

	newSettings := NewTestSettings().
		WithBirdNET(0.7, 40.0, -100.0).
		Build()

	if newSettings.BirdNET.Threshold != 0.7 {
		t.Error("Build() appears to return shared state")
	}
}

// TestSettingsBuilder_Apply verifies that Apply sets global test settings.
func TestSettingsBuilder_Apply(t *testing.T) {
	// Store original settings
	originalSettings := GetSettings()
	defer SetTestSettings(originalSettings)

	// Apply new settings
	appliedSettings := NewTestSettings().
		WithBirdNET(0.75, 50.0, -120.0).
		Apply()

	// Verify settings were applied globally
	currentSettings := GetSettings()

	if currentSettings.BirdNET.Threshold != 0.75 {
		t.Errorf("Expected global threshold 0.75, got %v", currentSettings.BirdNET.Threshold)
	}

	if appliedSettings.BirdNET.Threshold != 0.75 {
		t.Errorf("Expected returned threshold 0.75, got %v", appliedSettings.BirdNET.Threshold)
	}
}

// BenchmarkSettingsBuilder_Build benchmarks the builder Build operation.
func BenchmarkSettingsBuilder_Build(b *testing.B) {
	builder := NewTestSettings().
		WithBirdNET(0.7, 45.0, -122.0).
		WithMQTT("tcp://localhost:1883", "test/topic")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = builder.Build()
	}
}

// BenchmarkSettingsBuilder_FullChain benchmarks a complete builder chain.
func BenchmarkSettingsBuilder_FullChain(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewTestSettings().
			WithBirdNET(0.8, 45.0, -122.0).
			WithMQTT("tcp://localhost:1883", "test").
			WithAudioExport("/tmp/audio", "mp3", "192k").
			WithSpeciesTracking(7, 3600).
			WithWebServer("8080", true).
			Build()
	}
}
