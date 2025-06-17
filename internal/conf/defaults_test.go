package conf

import (
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestGetDefaults(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected interface{}
		wantType string
	}{
		{"default port", "port", 8080, "int"},
		{"default host", "host", "localhost", "string"},
		{"default timeout", "timeout", 30 * time.Second, "time.Duration"},
		{"default debug mode", "debug", false, "bool"},
		{"default log level", "log_level", "info", "string"},
		{"default max connections", "max_connections", 100, "int"},
		{"default read timeout", "read_timeout", 15 * time.Second, "time.Duration"},
		{"default write timeout", "write_timeout", 15 * time.Second, "time.Duration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defaults := GetDefaults()
			if defaults == nil {
				t.Fatal("GetDefaults() returned nil")
			}

			value, exists := defaults[tt.key]
			if !exists {
				t.Errorf("expected key %q to exist in defaults", tt.key)
				return
			}

			if !reflect.DeepEqual(value, tt.expected) {
				t.Errorf("GetDefaults()[%q] = %v, want %v", tt.key, value, tt.expected)
			}

			expectedType := reflect.TypeOf(tt.expected).String()
			actualType := reflect.TypeOf(value).String()
			if actualType != expectedType {
				t.Errorf("GetDefaults()[%q] type = %s, want %s", tt.key, actualType, expectedType)
			}
		})
	}
}

func TestValidateDefaults(t *testing.T) {
	tests := []struct {
		name        string
		config      map[string]interface{}
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid config",
			config:      map[string]interface{}{"port": 8080, "host": "localhost"},
			expectError: false,
		},
		{
			name:        "invalid port - negative",
			config:      map[string]interface{}{"port": -1},
			expectError: true,
			errorMsg:    "port must be positive",
		},
		{
			name:        "invalid port - too large",
			config:      map[string]interface{}{"port": 99999},
			expectError: true,
			errorMsg:    "port out of range",
		},
		{
			name:        "invalid host - empty",
			config:      map[string]interface{}{"host": ""},
			expectError: true,
			errorMsg:    "host cannot be empty",
		},
		{
			name:        "invalid timeout - negative",
			config:      map[string]interface{}{"timeout": -1 * time.Second},
			expectError: true,
			errorMsg:    "timeout must be positive",
		},
		{
			name:        "nil config",
			config:      nil,
			expectError: false,
		},
		{
			name:        "empty config",
			config:      map[string]interface{}{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.config)

			if tt.expectError {
				if err == nil {
					t.Errorf("ValidateConfig() expected error but got nil")
					return
				}
				if tt.errorMsg != "" && !contains(err.Error(), tt.errorMsg) {
					t.Errorf("ValidateConfig() error = %v, want error containing %q", err, tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateConfig() unexpected error = %v", err)
				}
			}
		})
	}
}

// Helper function for string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && s[:len(substr)] == substr) ||
		(len(s) > len(substr) && s[len(s)-len(substr):] == substr) ||
		(len(s) > len(substr) && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestEnvironmentOverrides(t *testing.T) {
	tests := []struct {
		name      string
		envKey    string
		envValue  string
		configKey string
		expected  interface{}
	}{
		{"port override", "APP_PORT", "9090", "port", 9090},
		{"host override", "APP_HOST", "0.0.0.0", "host", "0.0.0.0"},
		{"debug override", "APP_DEBUG", "true", "debug", true},
		{"timeout override", "APP_TIMEOUT", "45s", "timeout", 45 * time.Second},
		{"log level override", "APP_LOG_LEVEL", "debug", "log_level", "debug"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envKey, tt.envValue)

			config := LoadConfigWithDefaults()
			if config == nil {
				t.Fatal("LoadConfigWithDefaults() returned nil")
			}

			value, exists := config[tt.configKey]
			if !exists {
				t.Errorf("expected key %q to exist in config", tt.configKey)
				return
			}

			if !reflect.DeepEqual(value, tt.expected) {
				t.Errorf("config[%q] = %v (type %T), want %v (type %T)",
					tt.configKey, value, value, tt.expected, tt.expected)
			}
		})
	}
}

func TestInvalidEnvironmentValues(t *testing.T) {
	tests := []struct {
		name       string
		envKey     string
		envValue   string
		useDefault bool
	}{
		{"invalid port", "APP_PORT", "invalid", true},
		{"invalid boolean", "APP_DEBUG", "maybe", true},
		{"invalid timeout", "APP_TIMEOUT", "not-a-duration", true},
		{"empty value", "APP_HOST", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envKey, tt.envValue)

			config := LoadConfigWithDefaults()
			if config == nil {
				t.Fatal("LoadConfigWithDefaults() returned nil")
			}

			if tt.useDefault {
				defaults := GetDefaults()
				for key, defaultValue := range defaults {
					if configValue, exists := config[key]; exists {
						if reflect.DeepEqual(configValue, defaultValue) {
							continue
						}
					}
				}
			}
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	const numGoroutines = 100
	const numIterations = 1000

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numIterations; j++ {
				defaults := GetDefaults()
				if defaults == nil {
					errors <- fmt.Errorf("goroutine %d: GetDefaults() returned nil", id)
					return
				}

				config := LoadConfigWithDefaults()
				if config == nil {
					errors <- fmt.Errorf("goroutine %d: LoadConfigWithDefaults() returned nil", id)
					return
				}

				if _, exists := defaults["port"]; !exists {
					errors <- fmt.Errorf("goroutine %d: port not found in defaults", id)
					return
				}
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		close(errors)
		for err := range errors {
			t.Error(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Test timed out - possible deadlock")
	}
}

func TestConfigPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		setupEnv    map[string]string
		fileConfig  map[string]interface{}
		expectedKey string
		expectedVal interface{}
		description string
	}{
		{
			name:        "env overrides file",
			setupEnv:    map[string]string{"APP_PORT": "9999"},
			fileConfig:  map[string]interface{}{"port": 8888},
			expectedKey: "port",
			expectedVal: 9999,
			description: "environment variable should override file config",
		},
		{
			name:        "file overrides defaults",
			setupEnv:    map[string]string{},
			fileConfig:  map[string]interface{}{"port": 7777},
			expectedKey: "port",
			expectedVal: 7777,
			description: "file config should override defaults",
		},
		{
			name:        "defaults used when nothing else set",
			setupEnv:    map[string]string{},
			fileConfig:  map[string]interface{}{},
			expectedKey: "port",
			expectedVal: 8080,
			description: "should use default when no overrides",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key := range tt.setupEnv {
				os.Unsetenv(key)
			}

			for key, value := range tt.setupEnv {
				t.Setenv(key, value)
			}

			config := MergeConfigs(GetDefaults(), tt.fileConfig, LoadEnvConfig())
			if config == nil {
				t.Fatal("MergeConfigs() returned nil")
			}

			value, exists := config[tt.expectedKey]
			if !exists {
				t.Errorf("expected key %q to exist in merged config", tt.expectedKey)
				return
			}

			if !reflect.DeepEqual(value, tt.expectedVal) {
				t.Errorf("%s: config[%q] = %v, want %v",
					tt.description, tt.expectedKey, value, tt.expectedVal)
			}
		})
	}
}

func BenchmarkGetDefaults(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = GetDefaults()
	}
}

func BenchmarkLoadConfigWithDefaults(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = LoadConfigWithDefaults()
	}
}

func BenchmarkValidateConfig(b *testing.B) {
	config := map[string]interface{}{
		"port":    8080,
		"host":    "localhost",
		"timeout": 30 * time.Second,
		"debug":   false,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateConfig(config)
	}
}

func BenchmarkConcurrentConfigAccess(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			config := GetDefaults()
			_ = config["port"]
			_ = config["host"]
			_ = config["timeout"]
		}
	})
}

func FuzzConfigValidation(f *testing.F) {
	f.Add(8080, "localhost", int64(30))
	f.Add(3000, "0.0.0.0", int64(60))
	f.Add(80, "example.com", int64(5))

	f.Fuzz(func(t *testing.T, port int, host string, timeoutSecs int64) {
		config := map[string]interface{}{
			"port":    port,
			"host":    host,
			"timeout": time.Duration(timeoutSecs) * time.Second,
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("ValidateConfig panicked with input port=%d, host=%q, timeout=%ds: %v",
					port, host, timeoutSecs, r)
			}
		}()

		_ = ValidateConfig(config)
	})
}

// Test helpers
func setupTestConfig(t *testing.T) (*map[string]interface{}, func()) {
	t.Helper()

	originalEnv := make(map[string]string)
	envKeys := []string{"APP_PORT", "APP_HOST", "APP_DEBUG", "APP_TIMEOUT", "APP_LOG_LEVEL"}

	for _, key := range envKeys {
		if value, exists := os.LookupEnv(key); exists {
			originalEnv[key] = value
		}
	}

	config := &map[string]interface{}{
		"port":    8080,
		"host":    "localhost",
		"debug":   false,
		"timeout": 30 * time.Second,
	}

	cleanup := func() {
		for _, key := range envKeys {
			os.Unsetenv(key)
		}
		for key, value := range originalEnv {
			os.Setenv(key, value)
		}
	}

	return config, cleanup
}

func assertConfigValue(t *testing.T, config map[string]interface{}, key string, expected interface{}) {
	t.Helper()

	value, exists := config[key]
	if !exists {
		t.Errorf("expected key %q to exist in config", key)
		return
	}

	if !reflect.DeepEqual(value, expected) {
		t.Errorf("config[%q] = %v (type %T), want %v (type %T)",
			key, value, value, expected, expected)
	}
}

func createTestConfigMap(overrides map[string]interface{}) map[string]interface{} {
	defaults := map[string]interface{}{
		"port":            8080,
		"host":            "localhost",
		"debug":           false,
		"timeout":         30 * time.Second,
		"log_level":       "info",
		"max_connections": 100,
		"read_timeout":    15 * time.Second,
		"write_timeout":   15 * time.Second,
	}

	for key, value := range overrides {
		defaults[key] = value
	}

	return defaults
}

func TestCompleteConfigurationFlow(t *testing.T) {
	t.Run("complete flow with all sources", func(t *testing.T) {
		_, cleanup := setupTestConfig(t)
		defer cleanup()

		t.Setenv("APP_PORT", "9090")
		t.Setenv("APP_DEBUG", "true")

		finalConfig := LoadConfigWithDefaults()

		assertConfigValue(t, finalConfig, "port", 9090)
		assertConfigValue(t, finalConfig, "debug", true)
		assertConfigValue(t, finalConfig, "host", "localhost")
		assertConfigValue(t, finalConfig, "timeout", 30*time.Second)
	})

	t.Run("error handling", func(t *testing.T) {
		errorTests := []struct {
			name   string
			config map[string]interface{}
		}{
			{"nil values", map[string]interface{}{"port": nil}},
			{"wrong types", map[string]interface{}{"port": "not-a-number"}},
			{"negative values", map[string]interface{}{"max_connections": -10}},
		}

		for _, tt := range errorTests {
			t.Run(tt.name, func(t *testing.T) {
				err := ValidateConfig(tt.config)
				if err == nil {
					merged := MergeConfigs(GetDefaults(), tt.config, nil)
					if merged == nil {
						t.Error("expected non-nil config even with invalid input")
					}
				}
			})
		}
	})

	t.Run("stress test", func(t *testing.T) {
		for i := 0; i < 1000; i++ {
			config := GetDefaults()
			if config == nil {
				t.Fatalf("GetDefaults() returned nil at iteration %d", i)
			}
		}
	})
}

func TestAllEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*testing.T)
		validation  func(*testing.T, map[string]interface{})
		expectError bool
	}{
		{
			name: "maximum valid values",
			setup: func(t *testing.T) {
				t.Setenv("APP_PORT", "65535")
				t.Setenv("APP_MAX_CONNECTIONS", "10000")
			},
			validation: func(t *testing.T, config map[string]interface{}) {
				assertConfigValue(t, config, "port", 65535)
				assertConfigValue(t, config, "max_connections", 10000)
			},
		},
		{
			name: "minimum valid values",
			setup: func(t *testing.T) {
				t.Setenv("APP_PORT", "1")
				t.Setenv("APP_TIMEOUT", "1s")
			},
			validation: func(t *testing.T, config map[string]interface{}) {
				assertConfigValue(t, config, "port", 1)
				assertConfigValue(t, config, "timeout", 1*time.Second)
			},
		},
		{
			name: "unicode and special characters",
			setup: func(t *testing.T) {
				t.Setenv("APP_HOST", "🚀.example.com")
			},
			validation: func(t *testing.T, config map[string]interface{}) {
				assertConfigValue(t, config, "host", "🚀.example.com")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t)
			}

			config := LoadConfigWithDefaults()
			if config == nil {
				if !tt.expectError {
					t.Fatal("LoadConfigWithDefaults() returned nil")
				}
				return
			}

			if tt.validation != nil {
				tt.validation(t, config)
			}
		})
	}
}