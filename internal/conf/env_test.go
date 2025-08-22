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
		// Whitespace handling tests
		{"true with spaces", " true ", false},
		{"false with spaces", "  false  ", false},
		{"true with tab", "\ttrue", false},
		{"false with newline", "false\n", false},
		{"true with mixed whitespace", " \t true \n ", false},
		{"1 with spaces", " 1 ", false},
		{"0 with tab", "\t0\t", false},
		// Numeric-like edge cases (should fail for boolean)
		{"decimal 0.5", "0.5", true},
		{"decimal with spaces", " 0.5 ", true},
		{"decimal 1.0", "1.0", true},
		{"decimal 0.0", "0.0", true},
		{"negative number", "-1", true},
		{"large number", "123", true},
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
		viper.Reset()
		t.Setenv("BIRDNET_DEBUG", "maybe")
		
		err := configureEnvironmentVariables()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid boolean value")
	})
	
	// Test with invalid locale
	t.Run("invalid locale", func(t *testing.T) {
		viper.Reset()
		t.Setenv("BIRDNET_LOCALE", "invalid_locale")
		
		err := configureEnvironmentVariables()
		require.Error(t, err)
		// Check for the actual error message
		assert.Contains(t, err.Error(), "expected pattern")
	})
	
	// Test with multiple errors
	t.Run("multiple errors", func(t *testing.T) {
		viper.Reset()
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
		viper.Reset()
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

func TestLocaleCanonicalizations(t *testing.T) {
	// Test that locale gets canonicalized to lowercase on successful validation
	t.Run("uppercase locale gets canonicalized", func(t *testing.T) {
		viper.Reset()
		t.Setenv("BIRDNET_LOCALE", "EN-US")
		
		err := configureEnvironmentVariables()
		require.NoError(t, err)
		
		// Check that the locale was canonicalized to lowercase
		actual := viper.GetString("birdnet.locale")
		assert.Equal(t, "en-us", actual)
	})
	
	t.Run("mixed case locale gets canonicalized", func(t *testing.T) {
		viper.Reset()
		t.Setenv("BIRDNET_LOCALE", "De-DE")
		
		err := configureEnvironmentVariables()
		require.NoError(t, err)
		
		// Check that the locale was canonicalized to lowercase
		actual := viper.GetString("birdnet.locale")
		assert.Equal(t, "de-de", actual)
	})
}

func TestValidateEnvLatitude(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid zero", "0", false},
		{"valid positive", "45.5", false},
		{"valid negative", "-45.5", false},
		{"valid max", "90", false},
		{"valid min", "-90", false},
		{"valid decimal", "12.34567", false},
		// Whitespace handling
		{"valid with spaces", " 45.5 ", false},
		{"valid with tab", "\t-30.5\t", false},
		{"valid with newline", "60.0\n", false},
		{"valid with mixed whitespace", " \t 45.5 \n ", false},
		// Edge cases and errors
		{"too high", "90.1", true},
		{"too low", "-90.1", true},
		{"way too high", "180", true},
		{"way too low", "-180", true},
		{"not a number", "abc", true},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"decimal with spaces out of range", " 91.0 ", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvLatitude(tt.value)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEnvLongitude(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid zero", "0", false},
		{"valid positive", "120.5", false},
		{"valid negative", "-120.5", false},
		{"valid max", "180", false},
		{"valid min", "-180", false},
		{"valid decimal", "123.456789", false},
		// Whitespace handling
		{"valid with spaces", " 120.5 ", false},
		{"valid with tab", "\t-150.5\t", false},
		{"valid with newline", "90.0\n", false},
		{"valid with mixed whitespace", " \t -120.5 \n ", false},
		// Edge cases and errors
		{"too high", "180.1", true},
		{"too low", "-180.1", true},
		{"way too high", "360", true},
		{"way too low", "-360", true},
		{"not a number", "xyz", true},
		{"empty", "", true},
		{"whitespace only", "\t\n  ", true},
		{"decimal with spaces out of range", " -181.0 ", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvLongitude(tt.value)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEnvSensitivity(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid min", "0.1", false},
		{"valid max", "1.5", false},
		{"valid middle", "1.0", false},
		{"valid decimal", "0.75", false},
		// Whitespace handling
		{"valid with spaces", " 1.0 ", false},
		{"valid with tab", "\t0.5\t", false},
		{"valid with newline", "1.2\n", false},
		{"valid with mixed whitespace", " \t 0.8 \n ", false},
		// Edge cases and errors
		{"too low", "0.09", true},
		{"too high", "1.51", true},
		{"zero", "0", true},
		{"negative", "-0.5", true},
		{"not a number", "high", true},
		{"empty", "", true},
		{"whitespace only", "  \t  ", true},
		{"decimal with spaces out of range", " 2.0 ", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvSensitivity(tt.value)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEnvThreshold(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid min", "0.0", false},
		{"valid max", "1.0", false},
		{"valid middle", "0.5", false},
		{"valid decimal", "0.75", false},
		// Whitespace handling
		{"valid with spaces", " 0.5 ", false},
		{"valid with tab", "\t0.8\t", false},
		{"valid with newline", "0.3\n", false},
		{"valid with mixed whitespace", " \t 0.9 \n ", false},
		// Edge cases and errors
		{"too low", "-0.1", true},
		{"too high", "1.1", true},
		{"negative", "-0.5", true},
		{"greater than one", "2.0", true},
		{"not a number", "medium", true},
		{"empty", "", true},
		{"whitespace only", "\n\t  ", true},
		{"decimal with spaces out of range", " 1.5 ", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvThreshold(tt.value)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEnvOverlap(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid min", "0.0", false},
		{"valid max", "2.9", false},
		{"valid middle", "1.5", false},
		{"valid decimal", "2.5", false},
		// Whitespace handling
		{"valid with spaces", " 1.5 ", false},
		{"valid with tab", "\t2.0\t", false},
		{"valid with newline", "1.8\n", false},
		{"valid with mixed whitespace", " \t 2.2 \n ", false},
		// Edge cases and errors
		{"too low", "-0.1", true},
		{"too high", "3.0", true},
		{"negative", "-1.0", true},
		{"way too high", "5.0", true},
		{"not a number", "large", true},
		{"empty", "", true},
		{"whitespace only", " \t \n ", true},
		{"decimal with spaces out of range", " 3.5 ", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvOverlap(tt.value)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEnvThreads(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid zero", "0", false},
		{"valid positive", "4", false},
		{"valid large", "16", false},
		{"valid very large", "128", false},
		// Whitespace handling
		{"valid with spaces", " 8 ", false},
		{"valid with tab", "\t4\t", false},
		{"valid with newline", "12\n", false},
		{"valid with mixed whitespace", " \t 6 \n ", false},
		// Edge cases and errors
		{"negative", "-1", true},
		{"negative large", "-10", true},
		{"decimal", "4.5", true},
		{"decimal with spaces", " 8.2 ", true},
		{"not a number", "many", true},
		{"empty", "", true},
		{"whitespace only", "  \t\n  ", true},
		{"float notation", "1e2", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvThreads(tt.value)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEnvRangeFilterThreshold(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid min", "0.0", false},
		{"valid max", "1.0", false},
		{"valid middle", "0.5", false},
		{"valid decimal", "0.85", false},
		// Whitespace handling
		{"valid with spaces", " 0.7 ", false},
		{"valid with tab", "\t0.3\t", false},
		{"valid with newline", "0.9\n", false},
		{"valid with mixed whitespace", " \t 0.4 \n ", false},
		// Edge cases and errors  
		{"too low", "-0.1", true},
		{"too high", "1.1", true},
		{"negative", "-0.5", true},
		{"greater than one", "2.0", true},
		{"not a number", "auto", true},
		{"empty", "", true},
		{"whitespace only", "\t \n   ", true},
		{"decimal with spaces out of range", " 1.8 ", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvRangeFilterThreshold(tt.value)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValueCanonicalization(t *testing.T) {
	// Test that values are canonicalized to correct types after validation
	tests := []struct {
		name     string
		envVar   string
		envValue string
		configKey string
		expectedType string
		expectedValue interface{}
	}{
		// Boolean canonicalization
		{"boolean true", "BIRDNET_DEBUG", "true", "birdnet.debug", "bool", true},
		{"boolean false", "BIRDNET_DEBUG", "false", "birdnet.debug", "bool", false},
		{"boolean with spaces", "BIRDNET_DEBUG", " TRUE ", "birdnet.debug", "bool", true},
		{"boolean uppercase", "BIRDNET_USEXNNPACK", "FALSE", "birdnet.usexnnpack", "bool", false},
		
		// Integer canonicalization  
		{"threads zero", "BIRDNET_THREADS", "0", "birdnet.threads", "int", 0},
		{"threads positive", "BIRDNET_THREADS", "8", "birdnet.threads", "int", 8},
		{"threads with spaces", "BIRDNET_THREADS", " 4 ", "birdnet.threads", "int", 4},
		
		// Float canonicalization
		{"latitude", "BIRDNET_LATITUDE", "45.5", "birdnet.latitude", "float64", 45.5},
		{"longitude with spaces", "BIRDNET_LONGITUDE", " -120.5 ", "birdnet.longitude", "float64", -120.5},
		{"sensitivity", "BIRDNET_SENSITIVITY", "1.2", "birdnet.sensitivity", "float64", 1.2},
		{"threshold", "BIRDNET_THRESHOLD", "0.8", "birdnet.threshold", "float64", 0.8},
		{"overlap", "BIRDNET_OVERLAP", "2.5", "birdnet.overlap", "float64", 2.5},
		
		// String canonicalization
		{"locale lowercase", "BIRDNET_LOCALE", "EN-US", "birdnet.locale", "string", "en-us"},
		{"locale with spaces", "BIRDNET_LOCALE", " de-DE ", "birdnet.locale", "string", "de-de"},
		{"model path trimmed", "BIRDNET_MODELPATH", " /path/to/model ", "birdnet.modelpath", "string", "/path/to/model"},
		{"range filter model", "BIRDNET_RANGEFILTER_MODEL", " latest ", "birdnet.rangefilter.model", "string", "latest"},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Setenv(tt.envVar, tt.envValue)
			
			err := configureEnvironmentVariables()
			require.NoError(t, err)
			
			// Check that the value was stored with the correct type
			actual := viper.Get(tt.configKey)
			require.NotNil(t, actual)
			
			// Verify type
			switch tt.expectedType {
			case "bool":
				assert.IsType(t, bool(false), actual, "Expected bool type")
				assert.Equal(t, tt.expectedValue, actual)
			case "int":
				assert.IsType(t, int(0), actual, "Expected int type")
				assert.Equal(t, tt.expectedValue, actual)
			case "float64":
				assert.IsType(t, float64(0), actual, "Expected float64 type")
				assert.Equal(t, tt.expectedValue, actual)
			case "string":
				assert.IsType(t, "", actual, "Expected string type")
				assert.Equal(t, tt.expectedValue, actual)
			default:
				t.Fatalf("Unknown expected type: %s", tt.expectedType)
			}
		})
	}
}