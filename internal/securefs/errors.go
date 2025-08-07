// Package securefs provides a secure file system implementation
// with path validation and sandboxing.
package securefs

import (
	"errors"
)

// Sentinel errors for the securefs package.
// These errors can be used with errors.Is to check for specific error conditions.
var (
	// ErrPathTraversal indicates an attempt to access a path outside the allowed directory
	// via relative path traversal (e.g., using "../" to escape the directory).
	ErrPathTraversal = errors.New("security error: path attempts to traverse outside base directory")

	// ErrInvalidPath indicates an invalid path specification (e.g., absolute path when relative is required)
	ErrInvalidPath = errors.New("security error: invalid path specification")

	// ErrAccessDenied indicates a permission error when accessing a file or directory
	ErrAccessDenied = errors.New("security error: access denied")

	// ErrNotRegularFile indicates an attempt to access something that is not a regular file
	ErrNotRegularFile = errors.New("security error: not a regular file")
)
