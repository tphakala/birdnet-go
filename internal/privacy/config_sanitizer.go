// Package privacy provides privacy-focused utility functions for handling sensitive data
package privacy

import (
	"fmt"
	"reflect"
	"strings"
	
	"github.com/spf13/viper"
)

// ConfigSanitizer handles intelligent sanitization of configuration data
type ConfigSanitizer struct {
	sensitiveFields map[string]bool
}

// NewConfigSanitizer creates a new configuration sanitizer with predefined sensitive fields
func NewConfigSanitizer() *ConfigSanitizer {
	cs := &ConfigSanitizer{
		sensitiveFields: make(map[string]bool),
	}
	
	// Define explicitly sensitive field paths that should always be redacted
	// These are full dot-notation paths from the root of the config
	sensitiveFields := []string{
		// API Keys and Tokens
		"realtime.birdweather.id",
		"realtime.ebird.apikey",
		"realtime.weather.openweather.apikey",
		"realtime.weather.wunderground.apikey",
		"realtime.weather.wunderground.stationid",
		
		// MQTT Credentials (but NOT topic or broker which need special handling)
		"realtime.mqtt.username",
		"realtime.mqtt.password",
		
		// Database Credentials
		"output.mysql.username",
		"output.mysql.password",
		"output.mysql.host",
		
		// OAuth Secrets (NOT client IDs which are public)
		"security.basicauth.password",
		"security.googleauth.clientsecret",
		"security.googleauth.userid",
		"security.githubauth.clientsecret",
		"security.githubauth.userid",
		
		// Session and Encryption Secrets
		"security.sessionsecret",
		"backup.encryption_key",
		
		// Sentry DSN
		"sentry.dsn",
		
		// Location data (only if not default 0.000)
		"birdnet.latitude",
		"birdnet.longitude",
	}
	
	for _, field := range sensitiveFields {
		cs.sensitiveFields[field] = true
	}
	
	return cs
}

// SanitizeConfig intelligently sanitizes configuration data
func (cs *ConfigSanitizer) SanitizeConfig(config map[string]interface{}) map[string]interface{} {
	return cs.sanitizeMap(config, "")
}

// sanitizeMap recursively sanitizes a configuration map
func (cs *ConfigSanitizer) sanitizeMap(data map[string]interface{}, prefix string) map[string]interface{} {
	result := make(map[string]interface{})
	
	for key, value := range data {
		// Build the full path for this key
		fullPath := key
		if prefix != "" {
			fullPath = prefix + "." + key
		}
		
		// Special handling for specific fields
		switch fullPath {
		case "realtime.mqtt.broker":
			// Sanitize MQTT broker URL to remove credentials but keep the URL
			if str, ok := value.(string); ok && str != "" {
				result[key] = SanitizeURL(str) // Use generic URL sanitizer
			} else {
				result[key] = value
			}
		case "realtime.rtsp.urls":
			// Sanitize RTSP URLs to remove credentials but keep URLs
			switch v := value.(type) {
			case []interface{}:
				sanitized := make([]interface{}, len(v))
				for i, url := range v {
					if urlStr, ok := url.(string); ok {
						sanitized[i] = SanitizeURL(urlStr) // Use generic URL sanitizer
					} else {
						sanitized[i] = url
					}
				}
				result[key] = sanitized
			case string:
				result[key] = SanitizeURL(v)
			default:
				result[key] = value
			}
		default:
			// Regular sensitive field check
			if cs.shouldRedact(fullPath, value) {
				result[key] = "[REDACTED]"
			} else {
				// Recursively process nested structures
				switch v := value.(type) {
				case map[string]interface{}:
					result[key] = cs.sanitizeMap(v, fullPath)
				case []interface{}:
					result[key] = cs.sanitizeSlice(v, fullPath)
				default:
					result[key] = value
				}
			}
		}
	}
	
	return result
}

// sanitizeSlice recursively sanitizes a slice
func (cs *ConfigSanitizer) sanitizeSlice(data []interface{}, prefix string) []interface{} {
	result := make([]interface{}, len(data))
	
	for i, item := range data {
		switch v := item.(type) {
		case map[string]interface{}:
			// For slices of maps, use the same prefix (don't add index)
			result[i] = cs.sanitizeMap(v, prefix)
		case []interface{}:
			result[i] = cs.sanitizeSlice(v, prefix)
		default:
			if cs.shouldRedact(prefix, item) {
				result[i] = "[REDACTED]"
			} else {
				result[i] = item
			}
		}
	}
	
	return result
}

// shouldRedact determines if a value should be redacted
func (cs *ConfigSanitizer) shouldRedact(path string, value interface{}) bool {
	// Check if this is an explicitly sensitive field
	if cs.sensitiveFields[path] {
		// Check if value is empty - never redact empty values
		if isEmpty(value) {
			return false
		}
		
		// Special handling for location data
		if path == "birdnet.latitude" || path == "birdnet.longitude" {
			// Don't redact default coordinates (0.000)
			if floatVal, ok := toFloat64(value); ok {
				// Check if it's effectively zero (within floating point tolerance)
				if floatVal >= -0.001 && floatVal <= 0.001 {
					return false
				}
			}
			// Always redact non-zero coordinates
			return true
		}
		
		// For MySQL credentials, check against default values
		if path == "output.mysql.username" || path == "output.mysql.password" {
			// Get the default value from viper
			defaultVal := viper.Get(path)
			if defaultVal != nil {
				// Check if value equals the default
				if reflect.DeepEqual(value, defaultVal) {
					return false // Don't redact default values
				}
			}
		}
		
		// For other sensitive fields, redact if not empty
		return true
	}
	
	return false
}

// isEmpty checks if a value is considered empty
func isEmpty(value interface{}) bool {
	if value == nil {
		return true
	}
	
	switch v := value.(type) {
	case string:
		return v == ""
	case []interface{}:
		return len(v) == 0
	case []string:
		return len(v) == 0
	case map[string]interface{}:
		return len(v) == 0
	case int, int32, int64:
		return false // Numbers are never considered "empty" for our purposes
	case float32, float64:
		return false // Numbers are never considered "empty" for our purposes
	case bool:
		return false // Booleans are never considered "empty"
	default:
		// Use reflection for other types
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.Slice, reflect.Map, reflect.Array:
			return rv.Len() == 0
		case reflect.Ptr, reflect.Interface:
			return rv.IsNil()
		default:
			// For all other types (Chan, Func, Struct, etc.), consider them non-empty
			return false
		}
	}
}

// toFloat64 attempts to convert a value to float64
func toFloat64(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}


// SanitizeConfigValue sanitizes a single configuration value with context
// This is useful for sanitizing individual values during runtime
func SanitizeConfigValue(key string, value interface{}) interface{} {
	cs := NewConfigSanitizer()
	if cs.shouldRedact(key, value) {
		return "[REDACTED]"
	}
	return value
}

// AddSensitiveField adds a field path to the list of sensitive fields
func (cs *ConfigSanitizer) AddSensitiveField(fieldPath string) {
	cs.sensitiveFields[fieldPath] = true
}

// RemoveSensitiveField removes a field path from the list of sensitive fields
func (cs *ConfigSanitizer) RemoveSensitiveField(fieldPath string) {
	delete(cs.sensitiveFields, fieldPath)
}

// IsSensitiveField checks if a field path is marked as sensitive
func (cs *ConfigSanitizer) IsSensitiveField(fieldPath string) bool {
	return cs.sensitiveFields[fieldPath]
}

// SanitizeForDisplay prepares configuration for display, showing redacted values
// but preserving structure and non-sensitive information
func (cs *ConfigSanitizer) SanitizeForDisplay(config map[string]interface{}) string {
	sanitized := cs.SanitizeConfig(config)
	return formatConfigForDisplay(sanitized, "", 0)
}

// formatConfigForDisplay formats the configuration map for human-readable display
func formatConfigForDisplay(data interface{}, prefix string, indent int) string {
	var result strings.Builder
	indentStr := strings.Repeat("  ", indent)
	
	switch v := data.(type) {
	case map[string]interface{}:
		for key, value := range v {
			result.WriteString(fmt.Sprintf("%s%s:\n", indentStr, key))
			result.WriteString(formatConfigForDisplay(value, "", indent+1))
		}
	case []interface{}:
		for i, item := range v {
			result.WriteString(fmt.Sprintf("%s[%d]:\n", indentStr, i))
			result.WriteString(formatConfigForDisplay(item, "", indent+1))
		}
	default:
		result.WriteString(fmt.Sprintf("%s%v\n", indentStr, v))
	}
	
	return result.String()
}