package logger

import (
	stderrors "errors"
)

// EnhancedErrorInterface defines the methods we expect from internal/errors.EnhancedError.
// This interface avoids importing internal/errors directly to prevent circular dependencies.
type EnhancedErrorInterface interface {
	error
	GetComponent() string
	GetCategory() string
	GetPriority() string
	GetContext() map[string]any
}

// ErrorFields extracts structured fields from an error.
// If the error is an EnhancedError (implements EnhancedErrorInterface),
// it extracts component, category, priority, and context fields.
// Otherwise, it returns just the error field.
func ErrorFields(err error) []Field {
	if err == nil {
		return nil
	}

	// Try to extract enhanced error information
	if ee, ok := stderrors.AsType[EnhancedErrorInterface](err); ok {
		fields := []Field{
			Error(err),
			String("component", ee.GetComponent()),
			String("category", ee.GetCategory()),
		}

		if p := ee.GetPriority(); p != "" {
			fields = append(fields, String("priority", p))
		}

		for k, v := range ee.GetContext() {
			fields = append(fields, Any(k, v))
		}

		return fields
	}

	// Plain error - just return the error field
	return []Field{Error(err)}
}
