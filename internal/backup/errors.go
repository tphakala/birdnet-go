// Package backup provides functionality for backing up application data
package backup

import (
	"errors"
	"fmt"
)

// ErrorCode represents specific backup error types
type ErrorCode int

const (
	// ErrUnknown represents an unknown error
	ErrUnknown ErrorCode = iota
	// ErrConfig represents a configuration error
	ErrConfig
	// ErrIO represents an I/O error
	ErrIO
	// ErrMedia represents a media-related error (SD card, etc.)
	ErrMedia
	// ErrDatabase represents a database-related error
	ErrDatabase
	// ErrCorruption represents data corruption
	ErrCorruption
	// ErrNotFound represents a missing resource
	ErrNotFound
	// ErrLocked represents a resource being locked
	ErrLocked
	// ErrInsufficientSpace represents insufficient storage space
	ErrInsufficientSpace
	// ErrTimeout represents an operation timeout
	ErrTimeout
	// ErrCanceled represents a canceled operation
	ErrCanceled
	// ErrValidation represents a validation error
	ErrValidation
	// ErrEncryption represents an encryption/decryption error
	ErrEncryption
	// ErrSecurity represents a security-related error
	ErrSecurity
)

// Error represents a backup operation error
type Error struct {
	Code    ErrorCode // Error classification
	Message string    // Human-readable error message
	Err     error     // Original error if any
}

// getErrorPrefix returns the appropriate emoji prefix based on error code
func (e *Error) getErrorPrefix() string {
	switch e.Code {
	case ErrUnknown:
		return "❌" // General error
	case ErrConfig:
		return "⚠️" // Warning for config issues
	case ErrIO:
		return "❌" // General error for I/O issues
	case ErrMedia:
		return "🚨" // Critical for media failures
	case ErrDatabase:
		return "🚨" // Critical for database issues
	case ErrCorruption:
		return "🚨" // Critical for data corruption
	case ErrNotFound:
		return "⚠️" // Warning for missing resources
	case ErrLocked:
		return "⚠️" // Warning for locked resources
	case ErrInsufficientSpace:
		return "🚨" // Critical for space issues
	case ErrTimeout:
		return "⚠️" // Warning for timeouts
	case ErrCanceled:
		return "ℹ️" // Info for cancellations
	case ErrValidation:
		return "⚠️" // Warning for validation issues
	case ErrEncryption:
		return "🚨" // Critical for encryption issues
	case ErrSecurity:
		return "🚨" // Critical for security issues
	default:
		return "❌" // Default to general error
	}
}

// Error returns the error message
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s %s: %v", e.getErrorPrefix(), e.Message, e.Err)
	}
	return fmt.Sprintf("%s %s", e.getErrorPrefix(), e.Message)
}

// Unwrap returns the underlying error
func (e *Error) Unwrap() error {
	return e.Err
}

// NewError creates a new backup error
func NewError(code ErrorCode, message string, err error) error {
	return &Error{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// IsErrorCode checks if an error is a backup error with the specified code
func IsErrorCode(err error, code ErrorCode) bool {
	var backupErr *Error
	if err == nil {
		return false
	}
	if errors.As(err, &backupErr) {
		return backupErr.Code == code
	}
	return false
}

// IsMediaError checks if an error is a media-related error
func IsMediaError(err error) bool {
	return IsErrorCode(err, ErrMedia)
}

// IsTimeoutError checks if an error is a timeout error
func IsTimeoutError(err error) bool {
	return IsErrorCode(err, ErrTimeout)
}

// IsCanceledError checks if an error is a cancellation error
func IsCanceledError(err error) bool {
	return IsErrorCode(err, ErrCanceled)
}

// IsCorruptionError checks if an error is a data corruption error
func IsCorruptionError(err error) bool {
	return IsErrorCode(err, ErrCorruption)
}

// IsInsufficientSpaceError checks if an error is an insufficient space error
func IsInsufficientSpaceError(err error) bool {
	return IsErrorCode(err, ErrInsufficientSpace)
}

// IsSecurityError checks if an error is a security-related error
func IsSecurityError(err error) bool {
	return IsErrorCode(err, ErrSecurity)
}

// IsDatabaseError checks if an error is database-related
func IsDatabaseError(err error) bool {
	return IsErrorCode(err, ErrDatabase)
}
