package api

import (
	"fmt"
	"strconv"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// =============================================================================
// SSE Metrics Helpers
// =============================================================================

// recordSSEConnectionStart records an SSE connection start if metrics are available
func (c *Controller) recordSSEConnectionStart(endpoint string) {
	if c.Metrics != nil && c.Metrics.HTTP != nil {
		c.Metrics.HTTP.SSEConnectionStarted(endpoint)
	}
}

// recordSSEConnectionClose records an SSE connection close if metrics are available
func (c *Controller) recordSSEConnectionClose(endpoint string, duration float64, reason string) {
	if c.Metrics != nil && c.Metrics.HTTP != nil {
		c.Metrics.HTTP.SSEConnectionClosed(endpoint, duration, reason)
	}
}

// =============================================================================
// Settings Validation Helpers
// =============================================================================

// validateFloatInRange validates a float64 field is within a range
func validateFloatInRange(m map[string]any, key string, minVal, maxVal float64, label string) error {
	val, exists := m[key]
	if !exists {
		return nil
	}
	floatVal, ok := val.(float64)
	if !ok {
		return nil // Type mismatch handled elsewhere
	}
	if floatVal < minVal || floatVal > maxVal {
		return fmt.Errorf("%s must be between %.0f and %.0f", label, minVal, maxVal)
	}
	return nil
}

// validateNonEmptyString validates a string field is non-empty with max length
func validateNonEmptyString(m map[string]any, key string, maxLen int, label string) error {
	val, exists := m[key]
	if !exists {
		return nil
	}
	str, ok := val.(string)
	if !ok {
		return fmt.Errorf("%s must be a string", label)
	}
	if str == "" {
		return fmt.Errorf("%s cannot be empty", label)
	}
	if maxLen > 0 && len(str) > maxLen {
		return fmt.Errorf("%s must not exceed %d characters", label, maxLen)
	}
	return nil
}

// validateBoolField validates a boolean field
func validateBoolField(m map[string]any, key, label string) error {
	val, exists := m[key]
	if !exists {
		return nil
	}
	if _, ok := val.(bool); !ok {
		return fmt.Errorf("%s must be a boolean value", label)
	}
	return nil
}

// validatePortField validates a port field (string or int, 1-65535)
func validatePortField(m map[string]any, key string) error {
	val, exists := m[key]
	if !exists {
		return nil
	}

	var port int
	switch v := val.(type) {
	case string:
		var err error
		port, err = strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("port must be a valid number")
		}
	case int:
		port = v
	case float64:
		port = int(v)
	default:
		return fmt.Errorf("port must be a number")
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

// validateRequiredStringWhenEnabled validates required string fields when a provider is enabled
func validateRequiredStringWhenEnabled(providerMap map[string]any, fieldName, providerName string) error {
	val, exists := providerMap[fieldName]
	if !exists {
		return fmt.Errorf("%s.%s is required when enabled", providerName, fieldName)
	}
	str, ok := val.(string)
	if !ok || str == "" {
		return fmt.Errorf("%s.%s is required when enabled", providerName, fieldName)
	}
	return nil
}

// =============================================================================
// Logging Helpers
// =============================================================================

// GetLogger returns a logger instance for the API v2 package.
// This provides consistent logging with module identification.
func GetLogger() logger.Logger {
	return logger.Global().Module("api")
}

// log returns a logger for the Controller methods.
// This is a convenience helper for controller-level logging.
func (c *Controller) log() logger.Logger {
	return GetLogger()
}
