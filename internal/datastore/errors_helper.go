// Package datastore provides error handling helpers for database operations
package datastore

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

var (
	// Compiled regex patterns for performance
	onceRegex         sync.Once
	diskFullPattern   *regexp.Regexp
	deadlockPattern   *regexp.Regexp
	corruptionPattern *regexp.Regexp
	lockPattern       *regexp.Regexp
	constraintPattern *regexp.Regexp
)

// Note: As of Go 1.20, the global rand functions are automatically seeded.
// The jitter calculation using rand.Float64() is sufficient for retry jitter
// as we only need basic randomization to prevent thundering herd effect.
// Cryptographic randomness is not required since the jitter is just used
// to spread out retry attempts across instances.

func initRegexPatterns() {
	onceRegex.Do(func() {
		diskFullPattern = regexp.MustCompile(`(?i)(disk full|no space|out of space)`)
		deadlockPattern = regexp.MustCompile(`(?i)(deadlock detected|lock wait timeout|deadlock found)`)
		corruptionPattern = regexp.MustCompile(`(?i)(corrupt|malformed|database disk image is malformed|file is not a database)`)
		lockPattern = regexp.MustCompile(`(?i)(locked|database is locked|resource busy)`)
		constraintPattern = regexp.MustCompile(`(?i)(constraint|duplicate|unique constraint|foreign key)`)
	})
}

// dbError creates a properly categorized database error with context
func dbError(err error, operation, priority string, context ...any) error {
	builder := errors.New(err).
		Component("datastore").
		Category(errors.CategoryDatabase).
		Context("operation", operation)

	if priority != "" {
		builder = builder.Priority(priority)
	}

	// Validate context pairs and add them
	if len(context)%2 != 0 {
		getLogger().Warn("Odd number of context parameters in dbError",
			logger.String("operation", operation),
			logger.Int("context_length", len(context)))
		// Drop the last unpaired element
		context = context[:len(context)-1]
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
func validationError(message, field string, value any) error {
	// Standardize validation error format
	standardizedMessage := fmt.Sprintf("invalid %s: %v", field, message)
	return errors.Newf("%s", standardizedMessage).
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
	initRegexPatterns()
	if diskFullPattern.MatchString(err.Error()) {
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
func stateError(err error, operation, stateType string, context ...any) error {
	priority := errors.PriorityMedium

	// Escalate priority for critical state errors
	initRegexPatterns()
	if deadlockPattern.MatchString(err.Error()) || corruptionPattern.MatchString(err.Error()) {
		priority = errors.PriorityHigh
	}

	builder := errors.New(err).
		Component("datastore").
		Category(errors.CategoryState).
		Priority(priority).
		Context("operation", operation).
		Context("state_type", stateType)

	// Validate and add additional context pairs
	if len(context)%2 != 0 {
		getLogger().Warn("Odd number of context parameters in stateError",
			logger.String("operation", operation),
			logger.String("state_type", stateType),
			logger.Int("context_length", len(context)))
		context = context[:len(context)-1]
	}

	// Add additional context pairs
	for i := 0; i < len(context)-1; i += 2 {
		if key, ok := context[i].(string); ok {
			builder = builder.Context(key, context[i+1])
		}
	}

	return builder.Build()
}

// conflictError creates a conflict error for constraint violations
func conflictError(err error, operation, conflictType string, context ...any) error {
	builder := errors.New(err).
		Component("datastore").
		Category(errors.CategoryConflict).
		Priority(errors.PriorityMedium).
		Context("operation", operation).
		Context("conflict_type", conflictType)

	// Validate and add additional context pairs
	if len(context)%2 != 0 {
		getLogger().Warn("Odd number of context parameters in conflictError",
			logger.String("operation", operation),
			logger.String("conflict_type", conflictType),
			logger.Int("context_length", len(context)))
		context = context[:len(context)-1]
	}

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
func criticalError(err error, operation, reason string, context ...any) error {
	builder := errors.New(err).
		Component("datastore").
		Category(errors.CategoryDatabase).
		Priority(errors.PriorityCritical).
		Context("operation", operation).
		Context("critical_reason", reason)

	// Validate and add additional context pairs
	if len(context)%2 != 0 {
		getLogger().Warn("Odd number of context parameters in criticalError",
			logger.String("operation", operation),
			logger.String("critical_reason", reason),
			logger.Int("context_length", len(context)))
		context = context[:len(context)-1]
	}

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

	initRegexPatterns()
	return corruptionPattern.MatchString(err.Error())
}

// isDeadlock checks if an error indicates deadlock conditions
func isDeadlock(err error) bool {
	if err == nil {
		return false
	}

	initRegexPatterns()
	return deadlockPattern.MatchString(err.Error())
}

// isDatabaseLocked checks if an error indicates database lock conditions
func isDatabaseLocked(err error) bool {
	if err == nil {
		return false
	}

	initRegexPatterns()
	return lockPattern.MatchString(err.Error())
}

// Note: isConstraintViolation is already defined in logger_helpers.go

// CategoryState vs CategoryDatabase distinction:
// - CategoryDatabase: Errors related to database connectivity, corruption, schema issues
// - CategoryState: Errors related to application state management (locks, transactions, concurrency)
// This distinction helps with error routing and debugging different failure modes

// getUserFriendlyMessage creates user-friendly error messages for common scenarios
func getUserFriendlyMessage(operation string, err error) string {
	initRegexPatterns()

	switch {
	case diskFullPattern.MatchString(err.Error()):
		return "Storage space is full. Please free up disk space to continue."
	case lockPattern.MatchString(err.Error()):
		return "The database is currently busy. Please try again in a moment."
	case constraintPattern.MatchString(err.Error()):
		return "This operation conflicts with existing data. Please check for duplicates."
	case strings.Contains(strings.ToLower(err.Error()), "timeout"):
		return "The operation took too long. Please try again or contact support if the issue persists."
	case strings.Contains(strings.ToLower(err.Error()), "not found"):
		return "The requested item could not be found."
	case corruptionPattern.MatchString(err.Error()):
		return "Database integrity issue detected. Please contact support immediately."
	default:
		return fmt.Sprintf("Failed to %s. Please try again or contact support if the issue persists.", operation)
	}
}
