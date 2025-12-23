package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// SecureFileOp provides secure file operations with path validation
type SecureFileOp struct {
	component string
	baseDir   string
}

// NewSecureFileOp creates a new secure file operation helper
func NewSecureFileOp(component string, baseDir ...string) *SecureFileOp {
	var base string
	if len(baseDir) > 0 {
		base = baseDir[0]
	}
	return &SecureFileOp{
		component: component,
		baseDir:   base,
	}
}

// ValidatePath validates and sanitizes a file path for security
// It handles cross-platform path separators and prevents directory traversal
func (s *SecureFileOp) ValidatePath(path string) (string, error) {
	// Clean the path to resolve . and .. elements and handle separators
	cleanPath := filepath.Clean(path)
	
	// Convert to absolute path if it's relative to handle edge cases
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}
	
	// If baseDir is specified, ensure the path is within it
	if s.baseDir != "" {
		absBase, err := filepath.Abs(s.baseDir)
		if err != nil {
			return "", fmt.Errorf("failed to resolve base directory: %w", err)
		}
		
		// Use filepath.Rel to check if path is within base directory
		// This works consistently across platforms
		rel, err := filepath.Rel(absBase, absPath)
		if err != nil {
			return "", fmt.Errorf("path validation failed: %w", err)
		}
		
		// Check if the relative path tries to escape the base directory
		if strings.HasPrefix(rel, "..") || strings.Contains(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path outside allowed directory: %s", path)
		}
	}
	
	// Return the cleaned path (not absolute) to maintain original behavior
	return cleanPath, nil
}

// SecureCreate creates a file with path validation
func (s *SecureFileOp) SecureCreate(path string) (*os.File, string, error) {
	cleanPath, err := s.ValidatePath(path)
	if err != nil {
		return nil, "", errors.New(err).
			Component(s.component).
			Category(errors.CategoryValidation).
			Context("operation", "validate_path").
			Context("original_path", path).
			Build()
	}
	
	file, err := os.Create(cleanPath) // #nosec G304 - path is validated and sanitized
	if err != nil {
		return nil, cleanPath, errors.New(err).
			Component(s.component).
			Category(errors.CategoryFileIO).
			Context("operation", "create_file").
			Context("clean_path", cleanPath).
			Build()
	}
	
	return file, cleanPath, nil
}

// SecureOpen opens a file with path validation
func (s *SecureFileOp) SecureOpen(path string) (*os.File, string, error) {
	cleanPath, err := s.ValidatePath(path)
	if err != nil {
		return nil, "", errors.New(err).
			Component(s.component).
			Category(errors.CategoryValidation).
			Context("operation", "validate_path").
			Context("original_path", path).
			Build()
	}
	
	file, err := os.Open(cleanPath) // #nosec G304 - path is validated and sanitized
	if err != nil {
		return nil, cleanPath, errors.New(err).
			Component(s.component).
			Category(errors.CategoryFileIO).
			Context("operation", "open_file").
			Context("clean_path", cleanPath).
			Build()
	}
	
	return file, cleanPath, nil
}

// SecureReadFile reads a file with path validation
func (s *SecureFileOp) SecureReadFile(path string) (data []byte, cleanPath string, err error) {
	cleanPath, err = s.ValidatePath(path)
	if err != nil {
		return nil, "", errors.New(err).
			Component(s.component).
			Category(errors.CategoryValidation).
			Context("operation", "validate_path").
			Context("original_path", path).
			Build()
	}
	
	data, err = os.ReadFile(cleanPath) // #nosec G304 - path is validated and sanitized
	if err != nil {
		return nil, cleanPath, errors.New(err).
			Component(s.component).
			Category(errors.CategoryFileIO).
			Context("operation", "read_file").
			Context("clean_path", cleanPath).
			Build()
	}
	
	return data, cleanPath, nil
}

// SecureMkdirAll creates directories with path validation and configurable permissions
func (s *SecureFileOp) SecureMkdirAll(path string, perm os.FileMode) (string, error) {
	cleanPath, err := s.ValidatePath(path)
	if err != nil {
		return "", errors.New(err).
			Component(s.component).
			Category(errors.CategoryValidation).
			Context("operation", "validate_path").
			Context("original_path", path).
			Build()
	}
	
	err = os.MkdirAll(cleanPath, perm)
	if err != nil {
		return cleanPath, errors.New(err).
			Component(s.component).
			Category(errors.CategoryFileIO).
			Context("operation", "create_directory").
			Context("clean_path", cleanPath).
			Context("permissions", fmt.Sprintf("0%o", perm)).
			Build()
	}
	
	return cleanPath, nil
}

// FileOpResult holds the result of a file operation with the clean path
type FileOpResult struct {
	File      *os.File
	CleanPath string
	Error     error
}

// SecureFileOpBatch allows batching multiple file operations with consistent path validation
type SecureFileOpBatch struct {
	*SecureFileOp
	pathCache map[string]string // Cache for validated paths
}

// NewSecureFileOpBatch creates a new batch file operation helper
func NewSecureFileOpBatch(component string, baseDir ...string) *SecureFileOpBatch {
	return &SecureFileOpBatch{
		SecureFileOp: NewSecureFileOp(component, baseDir...),
		pathCache:    make(map[string]string),
	}
}

// GetValidatedPath returns a cached validated path or validates and caches it
func (b *SecureFileOpBatch) GetValidatedPath(path string) (string, error) {
	if cleanPath, exists := b.pathCache[path]; exists {
		return cleanPath, nil
	}
	
	cleanPath, err := b.ValidatePath(path)
	if err != nil {
		return "", err
	}
	
	b.pathCache[path] = cleanPath
	return cleanPath, nil
}

// ClearCache clears the path validation cache
func (b *SecureFileOpBatch) ClearCache() {
	b.pathCache = make(map[string]string)
}

// SecureFileCopy copies a file with path validation for both source and destination
func (s *SecureFileOp) SecureFileCopy(srcPath, dstPath string) (cleanSrcPath, cleanDstPath string, err error) {
	srcFile, cleanSrcPath, err := s.SecureOpen(srcPath)
	if err != nil {
		return "", "", err
	}
	defer func() {
		if cerr := srcFile.Close(); cerr != nil {
			// Log error but don't override return error
			_ = cerr
		}
	}()
	
	dstFile, cleanDstPath, err := s.SecureCreate(dstPath)
	if err != nil {
		return cleanSrcPath, "", err
	}
	defer func() {
		if cerr := dstFile.Close(); cerr != nil {
			// Log error but don't override return error
			_ = cerr
		}
	}()
	
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return cleanSrcPath, cleanDstPath, errors.New(err).
			Component(s.component).
			Category(errors.CategoryFileIO).
			Context("operation", "copy_file").
			Context("source_path", cleanSrcPath).
			Context("destination_path", cleanDstPath).
			Build()
	}
	
	return cleanSrcPath, cleanDstPath, nil
}

// DefaultDirectoryPermissions returns secure default permissions for directories
// Uses PermBackupDir for better security while maintaining functionality
func DefaultDirectoryPermissions() os.FileMode {
	return PermBackupDir
}

// DefaultFilePermissions returns secure default permissions for files
func DefaultFilePermissions() os.FileMode {
	return PermBackupFile
}