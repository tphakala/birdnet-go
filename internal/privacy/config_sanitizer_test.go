package privacy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigSanitizer_SanitizeConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "should_redact_api_keys",
			input: map[string]interface{}{
				"realtime": map[string]interface{}{
					"birdweather": map[string]interface{}{
						"id":        "station-123",
						"enabled":   true,
						"threshold": 0.7,
					},
					"ebird": map[string]interface{}{
						"apikey":  "secret-api-key",
						"enabled": false,
					},
				},
			},
			expected: map[string]interface{}{
				"realtime": map[string]interface{}{
					"birdweather": map[string]interface{}{
						"id":        "[REDACTED]",
						"enabled":   true,
						"threshold": 0.7,
					},
					"ebird": map[string]interface{}{
						"apikey":  "[REDACTED]",
						"enabled": false,
					},
				},
			},
		},
		{
			name: "should_not_redact_empty_api_keys",
			input: map[string]interface{}{
				"realtime": map[string]interface{}{
					"birdweather": map[string]interface{}{
						"id": "",
					},
					"ebird": map[string]interface{}{
						"apikey": "",
					},
				},
			},
			expected: map[string]interface{}{
				"realtime": map[string]interface{}{
					"birdweather": map[string]interface{}{
						"id": "",
					},
					"ebird": map[string]interface{}{
						"apikey": "",
					},
				},
			},
		},
		{
			name: "should_not_redact_default_coordinates",
			input: map[string]interface{}{
				"birdnet": map[string]interface{}{
					"latitude":  0.000,
					"longitude": 0.000,
				},
			},
			expected: map[string]interface{}{
				"birdnet": map[string]interface{}{
					"latitude":  0.000,
					"longitude": 0.000,
				},
			},
		},
		{
			name: "should_redact_non_default_coordinates",
			input: map[string]interface{}{
				"birdnet": map[string]interface{}{
					"latitude":  37.7749,
					"longitude": -122.4194,
				},
			},
			expected: map[string]interface{}{
				"birdnet": map[string]interface{}{
					"latitude":  "[REDACTED]",
					"longitude": "[REDACTED]",
				},
			},
		},
		{
			name: "should_not_redact_equalizer_settings",
			input: map[string]interface{}{
				"realtime": map[string]interface{}{
					"audio": map[string]interface{}{
						"equalizer": map[string]interface{}{
							"enabled": false,
							"filters": []interface{}{
								map[string]interface{}{
									"type":      "HighPass",
									"frequency": 100,
									"q":         0.7,
									"passes":    0,
									"width":     10,
								},
								map[string]interface{}{
									"type":      "LowPass",
									"frequency": 15000,
									"q":         0.7,
									"passes":    0,
									"width":     20,
								},
							},
						},
					},
				},
			},
			expected: map[string]interface{}{
				"realtime": map[string]interface{}{
					"audio": map[string]interface{}{
						"equalizer": map[string]interface{}{
							"enabled": false,
							"filters": []interface{}{
								map[string]interface{}{
									"type":      "HighPass",
									"frequency": 100,
									"q":         0.7,
									"passes":    0,
									"width":     10,
								},
								map[string]interface{}{
									"type":      "LowPass",
									"frequency": 15000,
									"q":         0.7,
									"passes":    0,
									"width":     20,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "should_not_redact_dashboard_settings",
			input: map[string]interface{}{
				"realtime": map[string]interface{}{
					"dashboard": map[string]interface{}{
						"locale":       "en",
						"newui":        false,
						"summarylimit": 30,
						"thumbnails": map[string]interface{}{
							"debug":          false,
							"fallbackpolicy": "all",
							"imageprovider":  "avicommons",
							"recent":         false,
							"summary":        false,
						},
					},
				},
			},
			expected: map[string]interface{}{
				"realtime": map[string]interface{}{
					"dashboard": map[string]interface{}{
						"locale":       "en",
						"newui":        false,
						"summarylimit": 30,
						"thumbnails": map[string]interface{}{
							"debug":          false,
							"fallbackpolicy": "all",
							"imageprovider":  "avicommons",
							"recent":         false,
							"summary":        false,
						},
					},
				},
			},
		},
		{
			name: "should_not_redact_dogbarkfilter_settings",
			input: map[string]interface{}{
				"realtime": map[string]interface{}{
					"dogbarkfilter": map[string]interface{}{
						"confidence": 0.1,
						"debug":      false,
						"enabled":    false,
						"remember":   5,
						"species":    []interface{}{},
					},
				},
			},
			expected: map[string]interface{}{
				"realtime": map[string]interface{}{
					"dogbarkfilter": map[string]interface{}{
						"confidence": 0.1,
						"debug":      false,
						"enabled":    false,
						"remember":   5,
						"species":    []interface{}{},
					},
				},
			},
		},
		{
			name: "should_not_redact_dynamicthreshold_settings",
			input: map[string]interface{}{
				"realtime": map[string]interface{}{
					"dynamicthreshold": map[string]interface{}{
						"debug":      false,
						"enabled":    false,
						"min":        0.2,
						"trigger":    0.9,
						"validhours": 24,
					},
				},
			},
			expected: map[string]interface{}{
				"realtime": map[string]interface{}{
					"dynamicthreshold": map[string]interface{}{
						"debug":      false,
						"enabled":    false,
						"min":        0.2,
						"trigger":    0.9,
						"validhours": 24,
					},
				},
			},
		},
		{
			name: "should_sanitize_mqtt_broker_and_redact_credentials",
			input: map[string]interface{}{
				"realtime": map[string]interface{}{
					"mqtt": map[string]interface{}{
						"enabled":  true,
						"broker":   "tcp://user:pass@broker.example.com:1883",
						"username": "mqtt_user",
						"password": "mqtt_secret",
						"topic":    "birdnet/detections",
					},
				},
			},
			expected: map[string]interface{}{
				"realtime": map[string]interface{}{
					"mqtt": map[string]interface{}{
						"enabled":  true,
						"broker":   "tcp://broker.example.com:1883", // Credentials removed from URL
						"username": "[REDACTED]",
						"password": "[REDACTED]",
						"topic":    "birdnet/detections", // Topic NOT redacted
					},
				},
			},
		},
		{
			name: "should_redact_oauth_secrets_but_not_clientids",
			input: map[string]interface{}{
				"security": map[string]interface{}{
					"googleauth": map[string]interface{}{
						"enabled":       true,
						"clientid":      "google-client-id",
						"clientsecret":  "google-secret",
						"userid":        "user@example.com",
						"redirecturi":   "/settings",
					},
					"githubauth": map[string]interface{}{
						"clientid":      "github-client-id",
						"clientsecret":  "github-secret",
						"userid":        "github-user",
					},
				},
			},
			expected: map[string]interface{}{
				"security": map[string]interface{}{
					"googleauth": map[string]interface{}{
						"enabled":       true,
						"clientid":      "google-client-id", // NOT redacted
						"clientsecret":  "[REDACTED]",
						"userid":        "[REDACTED]",
						"redirecturi":   "/settings",
					},
					"githubauth": map[string]interface{}{
						"clientid":      "github-client-id", // NOT redacted
						"clientsecret":  "[REDACTED]",
						"userid":        "[REDACTED]",
					},
				},
			},
		},
		{
			name: "should_sanitize_rtsp_urls_removing_credentials",
			input: map[string]interface{}{
				"realtime": map[string]interface{}{
					"rtsp": map[string]interface{}{
						"urls": []interface{}{
							"rtsp://admin:password@192.168.1.100:554/stream",
							"rtsp://camera.local/stream1",
						},
					},
				},
			},
			expected: map[string]interface{}{
				"realtime": map[string]interface{}{
					"rtsp": map[string]interface{}{
						"urls": []interface{}{
							"rtsp://192.168.1.100:554/stream", // Credentials removed
							"rtsp://camera.local/stream1",      // No credentials to remove
						},
					},
				},
			},
		},
		{
			name: "should_handle_mixed_sensitive_and_non_sensitive",
			input: map[string]interface{}{
				"main": map[string]interface{}{
					"name":     "BirdNET-Go",
					"timeas24h": true,
				},
				"output": map[string]interface{}{
					"mysql": map[string]interface{}{
						"enabled":  true,
						"username": "dbuser",
						"password": "dbpass",
						"database": "birdnet",
						"host":     "localhost",
						"port":     3306,
					},
				},
			},
			expected: map[string]interface{}{
				"main": map[string]interface{}{
					"name":     "BirdNET-Go",
					"timeas24h": true,
				},
				"output": map[string]interface{}{
					"mysql": map[string]interface{}{
						"enabled":  true,
						"username": "[REDACTED]",
						"password": "[REDACTED]",
						"database": "birdnet",
						"host":     "[REDACTED]",
						"port":     3306,
					},
				},
			},
		},
		{
			name: "should_redact_new_sensitive_fields",
			input: map[string]interface{}{
				"backup": map[string]interface{}{
					"encryption_key": "super-secret-key",
				},
				"security": map[string]interface{}{
					"sessionsecret": "session-secret-value",
				},
			},
			expected: map[string]interface{}{
				"backup": map[string]interface{}{
					"encryption_key": "[REDACTED]",
				},
				"security": map[string]interface{}{
					"sessionsecret": "[REDACTED]",
				},
			},
		},
		{
			name: "should_not_redact_empty_sensitive_fields",
			input: map[string]interface{}{
				"backup": map[string]interface{}{
					"encryption_key": "",
				},
				"security": map[string]interface{}{
					"sessionsecret": "",
					"basicauth": map[string]interface{}{
						"password": "",
					},
				},
			},
			expected: map[string]interface{}{
				"backup": map[string]interface{}{
					"encryption_key": "",
				},
				"security": map[string]interface{}{
					"sessionsecret": "",
					"basicauth": map[string]interface{}{
						"password": "",
					},
				},
			},
		},
		{
			name: "should_not_redact_weather_provider_selection",
			input: map[string]interface{}{
				"realtime": map[string]interface{}{
					"weather": map[string]interface{}{
						"provider": "yrno",
						"openweather": map[string]interface{}{
							"apikey": "",
						},
					},
				},
			},
			expected: map[string]interface{}{
				"realtime": map[string]interface{}{
					"weather": map[string]interface{}{
						"provider": "yrno",
						"openweather": map[string]interface{}{
							"apikey": "",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := NewConfigSanitizer()
			result := cs.SanitizeConfig(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfigSanitizer_AddRemoveSensitiveField(t *testing.T) {
	cs := NewConfigSanitizer()
	
	// Test adding a new sensitive field
	customField := "custom.sensitive.field"
	assert.False(t, cs.IsSensitiveField(customField))
	
	cs.AddSensitiveField(customField)
	assert.True(t, cs.IsSensitiveField(customField))
	
	// Test that it actually redacts the custom field
	config := map[string]interface{}{
		"custom": map[string]interface{}{
			"sensitive": map[string]interface{}{
				"field": "secret-value",
			},
		},
	}
	
	result := cs.SanitizeConfig(config)
	expected := map[string]interface{}{
		"custom": map[string]interface{}{
			"sensitive": map[string]interface{}{
				"field": "[REDACTED]",
			},
		},
	}
	assert.Equal(t, expected, result)
	
	// Test removing the sensitive field
	cs.RemoveSensitiveField(customField)
	assert.False(t, cs.IsSensitiveField(customField))
	
	// After removal, it should not redact
	result = cs.SanitizeConfig(config)
	assert.Equal(t, config, result)
}


func TestConfigSanitizer_isEmpty(t *testing.T) {
	tests := []struct {
		name     string
		value    interface{}
		expected bool
	}{
		{"nil_value", nil, true},
		{"empty_string", "", true},
		{"non_empty_string", "value", false},
		{"empty_slice", []interface{}{}, true},
		{"non_empty_slice", []interface{}{"item"}, false},
		{"empty_map", map[string]interface{}{}, true},
		{"non_empty_map", map[string]interface{}{"key": "value"}, false},
		{"zero_int", 0, false},
		{"non_zero_int", 42, false},
		{"zero_float", 0.0, false},
		{"non_zero_float", 3.14, false},
		{"false_bool", false, false},
		{"true_bool", true, false},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEmpty(tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfigSanitizer_toFloat64(t *testing.T) {
	tests := []struct {
		name     string
		value    interface{}
		expected float64
		ok       bool
	}{
		{"float64", float64(3.14), 3.14, true},
		{"float32", float32(2.5), 2.5, true},
		{"int", int(42), 42.0, true},
		{"int32", int32(100), 100.0, true},
		{"int64", int64(200), 200.0, true},
		{"string", "not_a_number", 0, false},
		{"bool", true, 0, false},
		{"nil", nil, 0, false},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := toFloat64(tt.value)
			assert.Equal(t, tt.ok, ok)
			if ok {
				// Use InEpsilon for non-zero values, InDelta for zero
				if tt.expected == 0 {
					assert.InDelta(t, tt.expected, result, 0.001)
				} else {
					assert.InEpsilon(t, tt.expected, result, 0.001)
				}
			}
		})
	}
}

func TestSanitizeConfigValue(t *testing.T) {
	// Test that standalone function works
	tests := []struct {
		name     string
		key      string
		value    interface{}
		expected interface{}
	}{
		{
			name:     "should_redact_sensitive_key",
			key:      "realtime.birdweather.id",
			value:    "station-123",
			expected: "[REDACTED]",
		},
		{
			name:     "should_not_redact_empty_sensitive_key",
			key:      "realtime.birdweather.id",
			value:    "",
			expected: "",
		},
		{
			name:     "should_not_redact_non_sensitive_key",
			key:      "realtime.dashboard.locale",
			value:    "en",
			expected: "en",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeConfigValue(tt.key, tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfigSanitizer_ComplexNestedStructure(t *testing.T) {
	// Test a complex nested structure with various data types
	input := map[string]interface{}{
		"level1": map[string]interface{}{
			"level2": map[string]interface{}{
				"normalField": "value",
				"level3": map[string]interface{}{
					"deepField": 123,
				},
			},
			"arrayField": []interface{}{
				map[string]interface{}{
					"item": "one",
				},
				map[string]interface{}{
					"item": "two",
				},
			},
		},
		"realtime": map[string]interface{}{
			"birdweather": map[string]interface{}{
				"id": "secret-id",
				"nested": map[string]interface{}{
					"deeper": map[string]interface{}{
						"value": "should-be-redacted",
					},
				},
			},
		},
	}
	
	cs := NewConfigSanitizer()
	result := cs.SanitizeConfig(input)
	
	// Check that normal fields are preserved
	level1 := result["level1"].(map[string]interface{})
	level2 := level1["level2"].(map[string]interface{})
	assert.Equal(t, "value", level2["normalField"])
	
	level3 := level2["level3"].(map[string]interface{})
	assert.Equal(t, 123, level3["deepField"])
	
	// Check that sensitive field is redacted
	realtime := result["realtime"].(map[string]interface{})
	birdweather := realtime["birdweather"].(map[string]interface{})
	assert.Equal(t, "[REDACTED]", birdweather["id"])
	
	// Check that nested fields under sensitive parent are preserved
	nested := birdweather["nested"].(map[string]interface{})
	deeper := nested["deeper"].(map[string]interface{})
	assert.Equal(t, "should-be-redacted", deeper["value"])
}


func TestConfigSanitizer_SanitizeForDisplay(t *testing.T) {
	cs := NewConfigSanitizer()
	
	config := map[string]interface{}{
		"main": map[string]interface{}{
			"name": "BirdNET-Go",
		},
		"realtime": map[string]interface{}{
			"birdweather": map[string]interface{}{
				"id": "secret-id",
			},
		},
	}
	
	result := cs.SanitizeForDisplay(config)
	
	// Check that the display format contains expected elements
	assert.Contains(t, result, "main:")
	assert.Contains(t, result, "name:")
	assert.Contains(t, result, "BirdNET-Go")
	assert.Contains(t, result, "realtime:")
	assert.Contains(t, result, "birdweather:")
	assert.Contains(t, result, "[REDACTED]")
}