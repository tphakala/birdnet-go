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

// Error returns the error message
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
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

// IsInsufficientSpace checks if an error is due to insufficient space
func IsInsufficientSpace(err error) bool {
	return IsErrorCode(err, ErrInsufficientSpace)
}

// IsDatabaseError checks if an error is database-related
func IsDatabaseError(err error) bool {
	return IsErrorCode(err, ErrDatabase)
}

// IsCorruption checks if an error indicates data corruption
func IsCorruption(err error) bool {
	return IsErrorCode(err, ErrCorruption)
}

// IsLocked checks if an error indicates a locked resource
func IsLocked(err error) bool {
	return IsErrorCode(err, ErrLocked)
}

// IsTimeout checks if an error indicates a timeout
func IsTimeout(err error) bool {
	return IsErrorCode(err, ErrTimeout)
}

// IsCanceled checks if an error indicates a canceled operation
func IsCanceled(err error) bool {
	return IsErrorCode(err, ErrCanceled)
}
