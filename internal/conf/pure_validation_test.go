package conf

import (
	"testing"
)

// TestValidateBirdNETSettings_Valid verifies valid BirdNET configurations pass.
func TestValidateBirdNETSettings_Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		config BirdNETConfig
	}{
		{
			name: "all valid values",
			config: BirdNETConfig{
				Sensitivity: 1.0,
				Threshold:   0.7,
				Overlap:     1.5,
				Latitude:    45.0,
				Longitude:   -122.0,
				Threads:     4,
				RangeFilter: RangeFilterSettings{
					Model:     "",
					Threshold: 0.03,
				},
			},
		},
		{
			name: "legacy range filter",
			config: BirdNETConfig{
				Sensitivity: 0.5,
				Threshold:   0.5,
				Overlap:     0.0,
				Latitude:    0.0,
				Longitude:   0.0,
				Threads:     0,
				RangeFilter: RangeFilterSettings{
					Model:     "legacy",
					Threshold: 0.5,
				},
			},
		},
		{
			name: "maximum values",
			config: BirdNETConfig{
				Sensitivity: 1.5,
				Threshold:   1.0,
				Overlap:     2.99,
				Latitude:    90.0,
				Longitude:   180.0,
				Threads:     16,
				RangeFilter: RangeFilterSettings{
					Model:     "",
					Threshold: 1.0,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateBirdNETSettings(&tt.config)
			assertValidationPasses(t, result)
		})
	}
}

// TestValidateBirdNETSettings_Invalid verifies invalid configurations are rejected.
func TestValidateBirdNETSettings_Invalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		config      BirdNETConfig
		expectError string
	}{
		{
			name: "sensitivity too low",
			config: BirdNETConfig{
				Sensitivity: -0.1,
			},
			expectError: "sensitivity must be between 0 and 1.5",
		},
		{
			name: "sensitivity too high",
			config: BirdNETConfig{
				Sensitivity: 1.6,
			},
			expectError: "sensitivity must be between 0 and 1.5",
		},
		{
			name: "threshold too low",
			config: BirdNETConfig{
				Threshold: -0.1,
			},
			expectError: "threshold must be between 0 and 1",
		},
		{
			name: "threshold too high",
			config: BirdNETConfig{
				Threshold: 1.1,
			},
			expectError: "threshold must be between 0 and 1",
		},
		{
			name: "overlap too low",
			config: BirdNETConfig{
				Overlap: -0.1,
			},
			expectError: "overlap value must be between 0 and 2.99 seconds",
		},
		{
			name: "overlap too high",
			config: BirdNETConfig{
				Overlap: 3.0,
			},
			expectError: "overlap value must be between 0 and 2.99 seconds",
		},
		{
			name: "latitude too low",
			config: BirdNETConfig{
				Latitude: -91.0,
			},
			expectError: "latitude must be between -90 and 90",
		},
		{
			name: "latitude too high",
			config: BirdNETConfig{
				Latitude: 91.0,
			},
			expectError: "latitude must be between -90 and 90",
		},
		{
			name: "longitude too low",
			config: BirdNETConfig{
				Longitude: -181.0,
			},
			expectError: "longitude must be between -180 and 180",
		},
		{
			name: "longitude too high",
			config: BirdNETConfig{
				Longitude: 181.0,
			},
			expectError: "longitude must be between -180 and 180",
		},
		{
			name: "negative threads",
			config: BirdNETConfig{
				Threads: -1,
			},
			expectError: "threads must be at least 0",
		},
		{
			name: "invalid range filter model",
			config: BirdNETConfig{
				RangeFilter: RangeFilterSettings{
					Model: "invalid",
				},
			},
			expectError: "RangeFilter model must be either empty (v2 default), 'latest', or 'legacy'",
		},
		{
			name: "range filter threshold too low",
			config: BirdNETConfig{
				RangeFilter: RangeFilterSettings{
					Threshold: -0.1,
				},
			},
			expectError: "RangeFilter threshold must be between 0 and 1",
		},
		{
			name: "range filter threshold too high",
			config: BirdNETConfig{
				RangeFilter: RangeFilterSettings{
					Threshold: 1.1,
				},
			},
			expectError: "RangeFilter threshold must be between 0 and 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateBirdNETSettings(&tt.config)
			assertValidationFails(t, result)
			assertErrorContains(t, result, tt.expectError)
		})
	}
}

// TestValidateBirdweatherSettings_Valid verifies valid Birdweather configurations.
func TestValidateBirdweatherSettings_Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings BirdweatherSettings
	}{
		{
			name: "disabled",
			settings: BirdweatherSettings{
				Enabled: false,
			},
		},
		{
			name: "enabled with valid ID",
			settings: BirdweatherSettings{
				Enabled:          true,
				ID:               "abcdef123456789012345678",
				Threshold:        0.7,
				LocationAccuracy: 100,
			},
		},
		{
			name: "minimum threshold",
			settings: BirdweatherSettings{
				Enabled:   true,
				ID:        "ABCDEF123456789012345678",
				Threshold: 0.0,
			},
		},
		{
			name: "maximum threshold",
			settings: BirdweatherSettings{
				Enabled:   true,
				ID:        "1234567890abcdefABCDEF12",
				Threshold: 1.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateBirdweatherSettings(&tt.settings)
			assertValidationPasses(t, result)
		})
	}
}

// TestValidateBirdweatherSettings_Invalid verifies invalid Birdweather configurations.
func TestValidateBirdweatherSettings_Invalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		settings    BirdweatherSettings
		expectError string
	}{
		{
			name: "enabled without ID",
			settings: BirdweatherSettings{
				Enabled: true,
				ID:      "",
			},
			expectError: "Birdweather ID is required",
		},
		{
			name: "invalid ID too short",
			settings: BirdweatherSettings{
				Enabled: true,
				ID:      "short",
			},
			expectError: "Invalid Birdweather ID format",
		},
		{
			name: "invalid ID too long",
			settings: BirdweatherSettings{
				Enabled: true,
				ID:      "abcdef123456789012345678extra",
			},
			expectError: "Invalid Birdweather ID format",
		},
		{
			name: "invalid ID with special characters",
			settings: BirdweatherSettings{
				Enabled: true,
				ID:      "abcdef12345678901234567!",
			},
			expectError: "Invalid Birdweather ID format",
		},
		{
			name: "threshold too low",
			settings: BirdweatherSettings{
				Enabled:   true,
				ID:        "abcdef123456789012345678",
				Threshold: -0.1,
			},
			expectError: "threshold must be between 0 and 1",
		},
		{
			name: "threshold too high",
			settings: BirdweatherSettings{
				Enabled:   true,
				ID:        "abcdef123456789012345678",
				Threshold: 1.1,
			},
			expectError: "threshold must be between 0 and 1",
		},
		{
			name: "negative location accuracy",
			settings: BirdweatherSettings{
				Enabled:          true,
				ID:               "abcdef123456789012345678",
				LocationAccuracy: -10,
			},
			expectError: "location accuracy must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateBirdweatherSettings(&tt.settings)
			assertValidationFails(t, result)
			assertErrorContains(t, result, tt.expectError)
		})
	}
}

// TestValidateWebhookProvider_Valid verifies valid webhook configurations.
func TestValidateWebhookProvider_Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider PushProviderConfig
	}{
		{
			name: "disabled provider",
			provider: PushProviderConfig{
				Name:    "test",
				Enabled: false,
			},
		},
		{
			name: "enabled with valid endpoint",
			provider: PushProviderConfig{
				Name:    "webhook1",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{
						URL: "https://example.com/webhook",
					},
				},
			},
		},
		{
			name: "multiple endpoints",
			provider: PushProviderConfig{
				Name:    "multi",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{URL: "https://api.example.com/hook1"},
					{URL: "https://api.example.com/hook2"},
				},
			},
		},
		{
			name: "with custom headers",
			provider: PushProviderConfig{
				Name:    "custom",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{
						URL: "https://example.com/webhook",
						Headers: map[string]string{
							"Authorization": "Bearer token",
							"Content-Type":  "application/json",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateWebhookProvider(&tt.provider)
			assertValidationPasses(t, result)
		})
	}
}

// TestValidateWebhookProvider_Invalid verifies invalid webhook configurations.
// This achieves 100% coverage of webhook validation error paths.
func TestValidateWebhookProvider_Invalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		provider    PushProviderConfig
		expectError string
	}{
		{
			name: "enabled without endpoints",
			provider: PushProviderConfig{
				Name:      "test",
				Enabled:   true,
				Endpoints: []WebhookEndpointConfig{},
			},
			expectError: "requires at least one endpoint",
		},
		{
			name: "invalid template syntax",
			provider: PushProviderConfig{
				Name:     "test",
				Enabled:  true,
				Template: "{{.Invalid}",
				Endpoints: []WebhookEndpointConfig{
					{URL: "https://example.com"},
				},
			},
			expectError: "invalid template syntax",
		},
		{
			name: "empty URL",
			provider: PushProviderConfig{
				Name:    "test",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{URL: ""},
				},
			},
			expectError: "URL is required",
		},
		{
			name: "URL without http/https prefix",
			provider: PushProviderConfig{
				Name:    "test",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{URL: "ftp://example.com/webhook"},
				},
			},
			expectError: "URL must start with http:// or https://",
		},
		{
			name: "URL with no protocol",
			provider: PushProviderConfig{
				Name:    "test",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{URL: "example.com/webhook"},
				},
			},
			expectError: "URL must start with http:// or https://",
		},
		{
			name: "invalid HTTP method",
			provider: PushProviderConfig{
				Name:    "test",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{
						URL:    "https://example.com/webhook",
						Method: "GET",
					},
				},
			},
			expectError: "method must be POST, PUT, or PATCH",
		},
		{
			name: "invalid HTTP method DELETE",
			provider: PushProviderConfig{
				Name:    "test",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{
						URL:    "https://example.com/webhook",
						Method: "DELETE",
					},
				},
			},
			expectError: "method must be POST, PUT, or PATCH",
		},
		{
			name: "negative timeout",
			provider: PushProviderConfig{
				Name:    "test",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{
						URL:     "https://example.com/webhook",
						Timeout: -1000,
					},
				},
			},
			expectError: "timeout must be non-negative",
		},
		{
			name: "multiple validation errors in single endpoint",
			provider: PushProviderConfig{
				Name:    "test",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{
						URL:     "ftp://example.com",
						Method:  "GET",
						Timeout: -5000,
					},
				},
			},
			expectError: "URL must start with http:// or https://",
		},
		{
			name: "multiple endpoints with mixed validity",
			provider: PushProviderConfig{
				Name:    "test",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{URL: "https://valid.com/hook"},
					{URL: ""},
					{URL: "ftp://invalid.com"},
				},
			},
			expectError: "URL is required",
		},
		{
			name: "whitespace-only URL",
			provider: PushProviderConfig{
				Name:    "test",
				Enabled: true,
				Endpoints: []WebhookEndpointConfig{
					{URL: "   "},
				},
			},
			expectError: "URL is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateWebhookProvider(&tt.provider)

			assertValidationFails(t, result)
			assertErrorContains(t, result, tt.expectError)
		})
	}
}

// TestValidateMQTTSettings_Valid verifies valid MQTT configurations.
func TestValidateMQTTSettings_Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings MQTTSettings
	}{
		{
			name: "disabled",
			settings: MQTTSettings{
				Enabled: false,
			},
		},
		{
			name: "enabled with broker and topic",
			settings: MQTTSettings{
				Enabled: true,
				Broker:  "tcp://localhost:1883",
				Topic:   "birdnet/detections",
			},
		},
		{
			name: "with retry settings",
			settings: MQTTSettings{
				Enabled: true,
				Broker:  "ssl://broker.hivemq.com:8883",
				Topic:   "birds",
				RetrySettings: RetrySettings{
					Enabled:          true,
					MaxRetries:       5,
					InitialDelay:     1000,
					MaxDelay:         30000,
					BackoffMultiplier: 2.0,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateMQTTSettings(&tt.settings)
			assertValidationPasses(t, result)
		})
	}
}

// TestValidateMQTTSettings_Invalid verifies invalid MQTT configurations.
func TestValidateMQTTSettings_Invalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		settings    MQTTSettings
		expectError string
	}{
		{
			name: "enabled without broker",
			settings: MQTTSettings{
				Enabled: true,
				Broker:  "",
				Topic:   "test",
			},
			expectError: "MQTT broker URL is required",
		},
		{
			name: "enabled without topic",
			settings: MQTTSettings{
				Enabled: true,
				Broker:  "tcp://localhost:1883",
				Topic:   "",
			},
			expectError: "MQTT topic is required",
		},
		{
			name: "negative max retries",
			settings: MQTTSettings{
				Enabled: true,
				Broker:  "tcp://localhost:1883",
				Topic:   "test",
				RetrySettings: RetrySettings{
					Enabled:    true,
					MaxRetries: -1,
				},
			},
			expectError: "max retries must be non-negative",
		},
		{
			name: "negative initial delay",
			settings: MQTTSettings{
				Enabled: true,
				Broker:  "tcp://localhost:1883",
				Topic:   "test",
				RetrySettings: RetrySettings{
					Enabled:      true,
					InitialDelay: -1000,
				},
			},
			expectError: "initial delay must be non-negative",
		},
		{
			name: "max delay less than initial delay",
			settings: MQTTSettings{
				Enabled: true,
				Broker:  "tcp://localhost:1883",
				Topic:   "test",
				RetrySettings: RetrySettings{
					Enabled:      true,
					InitialDelay: 5000,
					MaxDelay:     1000,
				},
			},
			expectError: "max delay must be greater than or equal to initial delay",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateMQTTSettings(&tt.settings)
			assertValidationFails(t, result)
			assertErrorContains(t, result, tt.expectError)
		})
	}
}

// TestValidateWebServerSettings_Valid verifies valid web server configurations.
func TestValidateWebServerSettings_Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings WebServerSettings
	}{
		{
			name: "disabled",
			settings: WebServerSettings{
				Enabled: false,
				LiveStream: LiveStreamSettings{
					BitRate:       128,
					SegmentLength: 5,
				},
			},
		},
		{
			name: "enabled with valid port",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "8080",
				LiveStream: LiveStreamSettings{
					BitRate:       128,
					SegmentLength: 5,
				},
			},
		},
		{
			name: "minimum port",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "1",
				LiveStream: LiveStreamSettings{
					BitRate:       16,
					SegmentLength: 1,
				},
			},
		},
		{
			name: "maximum port",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "65535",
				LiveStream: LiveStreamSettings{
					BitRate:       320,
					SegmentLength: 30,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateWebServerSettings(&tt.settings)
			assertValidationPasses(t, result)
		})
	}
}

// TestValidateWebServerSettings_Invalid verifies invalid web server configurations.
func TestValidateWebServerSettings_Invalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		settings    WebServerSettings
		expectError string
	}{
		{
			name: "enabled without port",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "",
			},
			expectError: "port is required",
		},
		{
			name: "invalid port non-numeric",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "abc",
			},
			expectError: "port must be a number between 1 and 65535",
		},
		{
			name: "port too low",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "0",
			},
			expectError: "port must be a number between 1 and 65535",
		},
		{
			name: "port too high",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "65536",
			},
			expectError: "port must be a number between 1 and 65535",
		},
		{
			name: "livestream bitrate too low",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "8080",
				LiveStream: LiveStreamSettings{
					BitRate:       15,
					SegmentLength: 5,
				},
			},
			expectError: "bitrate must be between 16 and 320 kbps",
		},
		{
			name: "livestream bitrate too high",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "8080",
				LiveStream: LiveStreamSettings{
					BitRate:       321,
					SegmentLength: 5,
				},
			},
			expectError: "bitrate must be between 16 and 320 kbps",
		},
		{
			name: "livestream segment too short",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "8080",
				LiveStream: LiveStreamSettings{
					BitRate:       128,
					SegmentLength: 0,
				},
			},
			expectError: "segment length must be between 1 and 30 seconds",
		},
		{
			name: "livestream segment too long",
			settings: WebServerSettings{
				Enabled: true,
				Port:    "8080",
				LiveStream: LiveStreamSettings{
					BitRate:       128,
					SegmentLength: 31,
				},
			},
			expectError: "segment length must be between 1 and 30 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateWebServerSettings(&tt.settings)
			assertValidationFails(t, result)
			assertErrorContains(t, result, tt.expectError)
		})
	}
}

// BenchmarkValidateBirdNETSettings benchmarks BirdNET validation.
func BenchmarkValidateBirdNETSettings(b *testing.B) {
	cfg := &BirdNETConfig{
		Sensitivity: 1.0,
		Threshold:   0.7,
		Overlap:     1.5,
		Latitude:    45.0,
		Longitude:   -122.0,
		Threads:     4,
		RangeFilter: RangeFilterSettings{
			Model:     "",
			Threshold: 0.03,
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateBirdNETSettings(cfg)
	}
}

// BenchmarkValidateBirdweatherSettings benchmarks Birdweather validation.
func BenchmarkValidateBirdweatherSettings(b *testing.B) {
	settings := &BirdweatherSettings{
		Enabled:   true,
		ID:        "abcdef123456789012345678",
		Threshold: 0.7,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateBirdweatherSettings(settings)
	}
}

// BenchmarkValidateMQTTSettings benchmarks MQTT validation.
func BenchmarkValidateMQTTSettings(b *testing.B) {
	settings := &MQTTSettings{
		Enabled: true,
		Broker:  "tcp://localhost:1883",
		Topic:   "test/topic",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateMQTTSettings(settings)
	}
}
