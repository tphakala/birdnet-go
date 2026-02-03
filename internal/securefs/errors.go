// Package securefs provides a secure file system implementation
// with path validation and sandboxing.
package securefs

import (
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Sentinel errors for the securefs package.
// These errors can be used with errors.Is to check for specific error conditions.
var (
	// ErrPathTraversal indicates an attempt to access a path outside the allowed directory
	// via relative path traversal (e.g., using "../" to escape the directory).
	ErrPathTraversal = errors.NewStd("security error: path attempts to traverse outside base directory")

	// ErrInvalidPath indicates an invalid path specification (e.g., absolute path when relative is required)
	ErrInvalidPath = errors.NewStd("security error: invalid path specification")

	// ErrAccessDenied indicates a permission error when accessing a file or directory
	ErrAccessDenied = errors.NewStd("security error: access denied")

	// ErrNotRegularFile indicates an attempt to access something that is not a regular file
	ErrNotRegularFile = errors.NewStd("security error: not a regular file")
)
