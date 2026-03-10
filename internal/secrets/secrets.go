// Package secrets provides secure credential management with support for
// environment variables and file-based secrets (Docker/Kubernetes secrets).
//
// Security Design:
//   - Never logs secret values
//   - Supports multiple secret sources for flexibility
//   - Validates file permissions for security
//   - Clear error messages without exposing secrets
package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const componentSecrets = "secrets"

const (
	// maxSecretFileSize limits secret file reads to prevent memory issues
	// Secrets should be small (tokens, passwords), not large files
	maxSecretFileSize = 64 * 1024 // 64 KB

	// secureFileMode is the most permissive acceptable mode for secret files
	// 0o600 = read/write for owner only
	// 0o400 = read-only for owner only
	secureFileMode = 0o600
)

// ExpandString resolves a string that may contain environment variable references.
// Supports syntax: ${VAR} or ${VAR:-default}
//
// Examples:
//   - "literal" -> "literal"
//   - "${TOKEN}" -> value of TOKEN env var
//   - "${TOKEN:-default}" -> value of TOKEN or "default" if not set
//   - "prefix-${TOKEN}-suffix" -> "prefix-<value>-suffix"
//
// Returns the expanded string or an error if required variables are missing.
func ExpandString(s string) (string, error) {
	if s == "" {
		return "", nil
	}

	// Use os.Expand for variable expansion
	// Track missing variables for better error messages
	var missingVars []string

	expanded := os.Expand(s, func(key string) string {
		// Support ${VAR:-default} syntax (fallback may be empty)
		varName := key
		defaultValue := ""
		fallbackProvided := false

		if before, after, found := strings.Cut(key, ":-"); found {
			varName = before
			defaultValue = after
			fallbackProvided = true
		}

		value := os.Getenv(varName)
		if value == "" {
			if fallbackProvided {
				// Use fallback even if it's an empty string
				return defaultValue
			}
			missingVars = append(missingVars, varName)
			return ""
		}
		return value
	})

	if len(missingVars) > 0 {
		return "", errors.Newf("missing required environment variable(s): %s", strings.Join(missingVars, ", ")).Component(componentSecrets).Category(errors.CategoryConfiguration).Build()
	}

	return expanded, nil
}

// ReadFile reads a secret from a file path.
// Commonly used for Docker secrets (/run/secrets/*) or Kubernetes mounted secrets.
//
// Security features:
//   - Limits file size to prevent memory exhaustion
//   - Warns if file has overly permissive permissions
//   - Trims whitespace (secrets often have trailing newlines)
//
// Returns the file contents (trimmed) or an error.
func ReadFile(path string) (string, error) {
	if path == "" {
		return "", errors.Newf("secret file path is empty").Component(componentSecrets).Category(errors.CategoryValidation).Build()
	}

	// Clean the path to prevent directory traversal
	cleanPath := filepath.Clean(path)

	// Check file exists and get info
	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.Newf("secret file not found: %s", cleanPath).Component(componentSecrets).Category(errors.CategoryFileIO).Build()
		}
		return "", errors.New(err).Component(componentSecrets).Category(errors.CategoryFileIO).Context("operation", "stat_secret_file").Build()
	}

	// Ensure it's a regular file (not a directory or device)
	if !info.Mode().IsRegular() {
		return "", errors.Newf("secret path is not a regular file: %s", cleanPath).Component(componentSecrets).Category(errors.CategoryValidation).Build()
	}

	// Check file size to prevent reading huge files
	if info.Size() > maxSecretFileSize {
		return "", errors.Newf("secret file too large (max %d bytes): %s", maxSecretFileSize, cleanPath).Component(componentSecrets).Category(errors.CategoryValidation).Build()
	}

	// Check file permissions (warn if group/other have any permissions)
	// Acceptable: owner-only (e.g., 0o400, 0o600). Warn otherwise.
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		// Warn about permissive permissions (group/other can read)
		// Use stderr for now - project may want structured logging here
		fmt.Fprintf(os.Stderr, "WARNING: secret file has group/other permissions (perms: %04o): %s\n", perm, cleanPath)
	}

	// Read the file contents
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return "", errors.New(err).Component(componentSecrets).Category(errors.CategoryFileIO).Context("operation", "read_secret_file").Build()
	}

	// Trim only trailing newlines (preserve intentional leading/trailing spaces)
	secret := string(data)
	secret = strings.TrimRight(secret, "\r\n")

	if secret == "" {
		return "", errors.Newf("secret file is empty: %s", cleanPath).Component(componentSecrets).Category(errors.CategoryValidation).Build()
	}

	return secret, nil
}

// Resolve determines the actual secret value from multiple possible sources.
// Precedence (highest to lowest):
//  1. filePath (if provided and readable)
//  2. value with environment variable expansion
//  3. literal value
//
// This allows flexible configuration:
//   - Resolve("", "literal") -> "literal"
//   - Resolve("", "${TOKEN}") -> expand TOKEN
//   - Resolve("/run/secrets/token", "") -> read from file
//   - Resolve("/run/secrets/token", "${TOKEN}") -> prefer file
//
// Returns the resolved secret value or an error.
func Resolve(filePath, value string) (string, error) {
	// Priority 1: File-based secret
	if filePath != "" {
		secret, err := ReadFile(filePath)
		if err != nil {
			return "", errors.New(err).Component(componentSecrets).Category(errors.CategoryFileIO).Context("operation", "resolve_secret_file").Build()
		}
		return secret, nil
	}

	// Priority 2: Environment variable expansion or literal value
	if value != "" {
		expanded, err := ExpandString(value)
		if err != nil {
			return "", err
		}
		return expanded, nil
	}

	// No secret source provided
	return "", nil
}

// MustResolve is like Resolve but returns an error if no secret is provided.
// Use this when a secret is required (not optional).
func MustResolve(fieldName, filePath, value string) (string, error) {
	secret, err := Resolve(filePath, value)
	if err != nil {
		return "", err
	}

	if secret == "" {
		return "", errors.Newf("%s is required but not provided", fieldName).Component(componentSecrets).Category(errors.CategoryValidation).Build()
	}

	return secret, nil
}
