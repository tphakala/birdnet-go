// Package datastore provides error handling helpers for database operations
package datastore

import (
	"fmt"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// dbError creates a properly categorized database error with context
func dbError(err error, operation, priority string, context ...interface{}) error {
	builder := errors.New(err).
		Component("datastore").
		Category(errors.CategoryDatabase).
		Context("operation", operation)

	if priority != "" {
		builder = builder.Priority(priority)
	}

	// Add context pairs
	for i := 0; i < len(context)-1; i += 2 {
		if key, ok := context[i].(string); ok {
			builder = builder.Context(key, context[i+1])
		}
	}

	return builder.Build()
}

// validationError creates a validation error (not sent to users by default)
func validationError(message, field string, value interface{}) error {
	return errors.Newf("%s", message).
		Component("datastore").
		Category(errors.CategoryValidation).
		Context("field", field).
		Context("value", fmt.Sprintf("%v", value)).
		Build()
}

// resourceError creates a resource error with appropriate priority
func resourceError(err error, operation, resourceType string) error {
	priority := errors.PriorityMedium
	category := errors.CategorySystem

	// Escalate priority for critical resources
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "disk full") ||
		strings.Contains(errStr, "no space") ||
		strings.Contains(errStr, "out of space") {
		priority = errors.PriorityCritical
		category = errors.CategoryDiskUsage
	}

	return errors.New(err).
		Component("datastore").
		Category(category).
		Priority(priority).
		Context("operation", operation).
		Context("resource_type", resourceType).
		Build()
}

// stateError creates a state management error (locks, transactions)
func stateError(err error, operation, stateType string, context ...interface{}) error {
	priority := errors.PriorityMedium
	errStr := strings.ToLower(err.Error())

	// Escalate priority for critical state errors
	if strings.Contains(errStr, "deadlock") ||
		strings.Contains(errStr, "corrupt") ||
		strings.Contains(errStr, "malformed") {
		priority = errors.PriorityHigh
	}

	builder := errors.New(err).
		Component("datastore").
		Category(errors.CategoryState).
		Priority(priority).
		Context("operation", operation).
		Context("state_type", stateType)

	// Add additional context pairs
	for i := 0; i < len(context)-1; i += 2 {
		if key, ok := context[i].(string); ok {
			builder = builder.Context(key, context[i+1])
		}
	}

	return builder.Build()
}

// conflictError creates a conflict error for constraint violations
func conflictError(err error, operation, conflictType string, context ...interface{}) error {
	builder := errors.New(err).
		Component("datastore").
		Category(errors.CategoryConflict).
		Priority(errors.PriorityMedium).
		Context("operation", operation).
		Context("conflict_type", conflictType)

	// Add additional context pairs
	for i := 0; i < len(context)-1; i += 2 {
		if key, ok := context[i].(string); ok {
			builder = builder.Context(key, context[i+1])
		}
	}

	return builder.Build()
}

// notFoundError creates a not found error (low priority, not shown to users)
func notFoundError(resource, identifier string) error {
	return errors.Newf("%s not found", resource).
		Component("datastore").
		Category(errors.CategoryNotFound).
		Context("resource", resource).
		Context("identifier", identifier).
		Build()
}

// criticalError creates a critical system error
func criticalError(err error, operation, reason string, context ...interface{}) error {
	builder := errors.New(err).
		Component("datastore").
		Category(errors.CategoryDatabase).
		Priority(errors.PriorityCritical).
		Context("operation", operation).
		Context("critical_reason", reason)

	// Add additional context pairs
	for i := 0; i < len(context)-1; i += 2 {
		if key, ok := context[i].(string); ok {
			builder = builder.Context(key, context[i+1])
		}
	}

	return builder.Build()
}

// isDatabaseLocked checks if an error indicates database lock conditions
// This is already defined in interfaces.go, so we'll extend it here
func isDatabaseCorruption(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "malformed") ||
		strings.Contains(errStr, "corrupt") ||
		strings.Contains(errStr, "database disk image is malformed") ||
		strings.Contains(errStr, "file is not a database")
}

// Note: isConstraintViolation is already defined in logger_helpers.go

// getUserFriendlyMessage creates user-friendly error messages for common scenarios
func getUserFriendlyMessage(operation string, err error) string {
	errStr := strings.ToLower(err.Error())

	switch {
	case strings.Contains(errStr, "disk full") || strings.Contains(errStr, "no space"):
		return "Storage space is full. Please free up disk space to continue."
	case strings.Contains(errStr, "locked") || strings.Contains(errStr, "database is locked"):
		return "The database is currently busy. Please try again in a moment."
	case strings.Contains(errStr, "constraint") || strings.Contains(errStr, "duplicate"):
		return "This operation conflicts with existing data. Please check for duplicates."
	case strings.Contains(errStr, "timeout"):
		return "The operation took too long. Please try again or contact support if the issue persists."
	case strings.Contains(errStr, "not found"):
		return "The requested item could not be found."
	case strings.Contains(errStr, "corrupt") || strings.Contains(errStr, "malformed"):
		return "Database integrity issue detected. Please contact support immediately."
	default:
		return fmt.Sprintf("Failed to %s. Please try again or contact support if the issue persists.", operation)
	}
}