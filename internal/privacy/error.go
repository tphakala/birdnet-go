// Package privacy provides privacy-focused utility functions for handling sensitive data.
package privacy

// SanitizedError wraps an error while providing a sanitized message for logging.
// The original error is preserved for programmatic access via Unwrap(),
// but the Error() method returns a sanitized version safe for logging.
type SanitizedError struct {
	original     error
	sanitizedMsg string
}

// Error returns the sanitized error message, safe for logging.
func (e *SanitizedError) Error() string {
	return e.sanitizedMsg
}

// Unwrap returns the original error, allowing errors.Is() and errors.As() to work.
func (e *SanitizedError) Unwrap() error {
	return e.original
}

// WrapError sanitizes an error message using ScrubMessage.
// Returns nil if the input error is nil.
// The returned error preserves the original error chain via Unwrap().
//
// Example usage:
//
//	if err := doSomething(); err != nil {
//	    return privacy.WrapError(err) // Safe to log
//	}
func WrapError(err error) error {
	if err == nil {
		return nil
	}
	return &SanitizedError{
		original:     err,
		sanitizedMsg: ScrubMessage(err.Error()),
	}
}
