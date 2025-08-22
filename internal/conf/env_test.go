package conf

import (
	"os"
	"path/filepath"
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
	tmpFile := filepath.Join(t.TempDir(), "test.tflite")
	err := os.WriteFile(tmpFile, []byte("test"), 0o600)
	require.NoError(t, err)
	
	// Create portable absolute paths for testing
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "nonexistent", "path", "file.txt")
	pathTraversalAttempt := filepath.Join(tempDir, "valid", "..", "..", "..", "etc", "passwd")
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
		errMsg  string
	}{
		{"absolute path exists", tmpFile, false, ""},
		{"absolute path not exists", nonExistentPath, false, ""}, // No error for non-existent files
		{"relative path", filepath.Join("relative", "path"), true, "must be absolute"},
		{"path traversal attempt", pathTraversalAttempt, false, ""}, // Clean normalizes this 
		{"relative with dots", filepath.Join("..", "..", "..", "etc", "passwd"), true, "must be absolute"},
		{"empty path", "", true, "must not be empty"},
		{"whitespace only", "   ", true, "must not be empty"},
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

func TestConfigureEnvironmentVariables_EmptyValuesValidated(t *testing.T) {
	// Reset viper for clean test
	viper.Reset()
	
	// Test that empty-but-present env vars are validated
	t.Run("empty threads value", func(t *testing.T) {
		t.Setenv("BIRDNET_THREADS", "")
		
		err := configureEnvironmentVariables()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid threads")
	})
}

func TestValidateAndNormalizeRangeFilterModel(t *testing.T) {
	// Test direct validation function
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty string", "", true},
		{"latest", "latest", false},
		{"legacy", "legacy", false},
		{"invalid", "invalid", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvRangeFilterModel(tt.value)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
	
	// Test integration with configureEnvironmentVariables
	integrationTests := []struct {
		name     string
		envValue string
		expected string
	}{
		{"latest model", "latest", "latest"},
		{"legacy model", "legacy", "legacy"},
	}
	
	for _, tt := range integrationTests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Setenv("BIRDNET_RANGEFILTER_MODEL", tt.envValue)
			
			err := configureEnvironmentVariables()
			require.NoError(t, err)
			
			actual := viper.GetString("birdnet.rangefilter.model")
			assert.Equal(t, tt.expected, actual)
		})
	}
}