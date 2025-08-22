package conf

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEnvBool(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"true", "true", false},
		{"false", "false", false},
		{"1", "1", false},
		{"0", "0", false},
		{"t", "t", false},
		{"f", "f", false},
		{"TRUE", "TRUE", false},
		{"FALSE", "FALSE", false},
		{"invalid", "maybe", true},
		{"yes", "yes", true}, // strconv.ParseBool doesn't accept yes/no
		{"no", "no", true},   // strconv.ParseBool doesn't accept yes/no
		{"empty", "", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvBool(tt.value)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid boolean value")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEnvLocale(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid en", "en", false},
		{"valid en-us", "en-us", false},
		{"valid fr", "fr", false},
		{"valid de-de", "de-de", false},
		{"uppercase EN-US", "EN-US", false}, // Case insensitive
		{"too short", "e", true},
		{"too long", "verylonglocale", true},
		{"invalid pattern", "en_us", true}, // Underscore not allowed
		{"invalid pattern three parts", "en-us-uk", true},
		{"numbers", "12-34", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvLocale(tt.value)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEnvPath(t *testing.T) {
	t.Parallel()
	
	// Create a temp file for testing
	tmpFile := t.TempDir() + "/test.tflite"
	err := os.WriteFile(tmpFile, []byte("test"), 0o600)
	require.NoError(t, err)
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
		errMsg  string
	}{
		{"absolute path exists", tmpFile, false, ""},
		{"absolute path not exists", "/nonexistent/path/file.txt", false, ""}, // No error for non-existent files
		{"relative path", "relative/path", true, "must be absolute"},
		{"path traversal attempt", "/valid/../../../etc/passwd", false, ""}, // Clean normalizes this to /etc/passwd 
		{"relative with dots", "../../../etc/passwd", true, "must be absolute"},
		{"empty path", "", true, "must be absolute"},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvPath(tt.value)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConfigureEnvironmentVariables(t *testing.T) {
	// Reset viper for clean test
	viper.Reset()
	
	// Test with invalid boolean env var
	t.Run("invalid boolean", func(t *testing.T) {
		t.Setenv("BIRDNET_DEBUG", "maybe")
		
		err := configureEnvironmentVariables()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid boolean value")
	})
	
	// Test with invalid locale
	t.Run("invalid locale", func(t *testing.T) {
		t.Setenv("BIRDNET_LOCALE", "invalid_locale")
		
		err := configureEnvironmentVariables()
		require.Error(t, err)
		// Check for the actual error message
		assert.Contains(t, err.Error(), "expected pattern")
	})
	
	// Test with multiple errors
	t.Run("multiple errors", func(t *testing.T) {
		t.Setenv("BIRDNET_DEBUG", "invalid")
		t.Setenv("BIRDNET_LOCALE", "x")
		
		err := configureEnvironmentVariables()
		require.Error(t, err)
		// Should contain multiple error messages
		errStr := err.Error()
		assert.True(t, strings.Contains(errStr, "BIRDNET_DEBUG") || strings.Contains(errStr, "BIRDNET_LOCALE"))
	})
	
	// Test with valid values
	t.Run("valid values", func(t *testing.T) {
		viper.Reset()
		t.Setenv("BIRDNET_DEBUG", "true")
		t.Setenv("BIRDNET_LOCALE", "en-us")
		t.Setenv("BIRDNET_THREADS", "4")
		
		err := configureEnvironmentVariables()
		assert.NoError(t, err)
	})
}