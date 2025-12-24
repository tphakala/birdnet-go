package datastore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestValidateStartupDiskSpace(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T) string // returns db path to test
		wantErr     bool
		errContains string
	}{
		{
			name: "sufficient disk space - temp directory",
			setup: func(t *testing.T) string {
				t.Helper()
				// Use system temp directory which should have sufficient space
				tempDir := t.TempDir()
				return filepath.Join(tempDir, "test.db")
			},
			wantErr: false,
		},
		{
			name: "current directory - should pass on most systems",
			setup: func(t *testing.T) string {
				t.Helper()
				// Use current directory
				cwd, err := os.Getwd()
				require.NoError(t, err, "Failed to get current directory")
				return filepath.Join(cwd, "test.db")
			},
			wantErr: false,
		},
		{
			name: "invalid path - nonexistent directory",
			setup: func(t *testing.T) string {
				t.Helper()
				// Path that definitely doesn't exist
				return "/this/path/definitely/does/not/exist/nowhere/test.db"
			},
			wantErr:     true,
			errContains: "", // Don't check error message, just verify error is returned
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := tt.setup(t)
			err := ValidateStartupDiskSpace(dbPath)

			if tt.wantErr {
				require.Error(t, err, "ValidateStartupDiskSpace() expected error, got nil")

				// Check error message contains expected substring
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains,
						"ValidateStartupDiskSpace() error should contain expected substring")
				}

				// Verify it's a structured error
				var enhancedErr *errors.EnhancedError
				require.ErrorAs(t, err, &enhancedErr,
					"ValidateStartupDiskSpace() should return EnhancedError")

				// Verify error has critical priority
				assert.Equal(t, errors.PriorityCritical, enhancedErr.GetPriority(),
					"ValidateStartupDiskSpace() error priority mismatch")

				// Verify error has correct category
				assert.Equal(t, string(errors.CategorySystem), enhancedErr.GetCategory(),
					"ValidateStartupDiskSpace() error category mismatch")
			} else {
				assert.NoError(t, err, "ValidateStartupDiskSpace() unexpected error")
			}
		})
	}
}

func TestValidateStartupDiskSpace_ErrorStructure(t *testing.T) {
	// Use invalid path to ensure we always get an error for testing
	dbPath := "/nonexistent/path/to/test.db"

	err := ValidateStartupDiskSpace(dbPath)
	require.Error(t, err, "Expected error for nonexistent path")

	// Verify it's a structured error
	var enhancedErr *errors.EnhancedError
	require.ErrorAs(t, err, &enhancedErr, "Expected EnhancedError")

	// Verify error metadata
	assert.Equal(t, "datastore", enhancedErr.GetComponent(), "Error component mismatch")
	assert.Equal(t, string(errors.CategorySystem), enhancedErr.GetCategory(), "Error category mismatch")
	assert.Equal(t, errors.PriorityCritical, enhancedErr.GetPriority(), "Error priority mismatch")

	// Verify error context fields
	ctx := enhancedErr.GetContext()
	expectedFields := []string{"operation", "path"}
	for _, field := range expectedFields {
		assert.Contains(t, ctx, field, "Error context missing field %q", field)
	}
}

func TestValidateStartupDiskSpace_InsufficientSpace_ErrorFormat(t *testing.T) {
	// This test validates the error format when disk space is insufficient
	// We can't reliably simulate insufficient space across environments,
	// but we verify the constant value and error message format expectations

	// Verify the constant has the expected value of 1GB (1024MB)
	const expectedMinDiskSpace = 1024
	assert.Equal(t, expectedMinDiskSpace, MinDiskSpaceStartup,
		"MinDiskSpaceStartup should be 1GB (1024MB)")

	// Note: Testing actual insufficient space scenario is environment-dependent
	// In a real insufficient space scenario, the error should contain:
	// - "insufficient disk space to start application"
	// - Available MB, required MB (1024), total MB
	// - Context fields: disk_free_mb, disk_required_mb, disk_total_mb, db_path
}
