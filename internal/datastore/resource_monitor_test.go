package datastore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
				if err != nil {
					t.Fatalf("Failed to get current directory: %v", err)
				}
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
				if err == nil {
					t.Error("ValidateStartupDiskSpace() expected error, got nil")
					return
				}

				// Check error message contains expected substring
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateStartupDiskSpace() error = %v, want error containing %q", err, tt.errContains)
				}

				// Verify it's a structured error
				var enhancedErr *errors.EnhancedError
				if !errors.As(err, &enhancedErr) {
					t.Error("ValidateStartupDiskSpace() should return EnhancedError")
					return
				}

				// Verify error has critical priority
				if enhancedErr.GetPriority() != errors.PriorityCritical {
					t.Errorf("ValidateStartupDiskSpace() error priority = %v, want %v",
						enhancedErr.GetPriority(), errors.PriorityCritical)
				}

				// Verify error has correct category
				if enhancedErr.GetCategory() != string(errors.CategorySystem) {
					t.Errorf("ValidateStartupDiskSpace() error category = %v, want %v",
						enhancedErr.GetCategory(), errors.CategorySystem)
				}
			} else if err != nil {
				t.Errorf("ValidateStartupDiskSpace() unexpected error = %v", err)
			}
		})
	}
}

func TestValidateStartupDiskSpace_ErrorStructure(t *testing.T) {
	// Use invalid path to ensure we always get an error for testing
	dbPath := "/nonexistent/path/to/test.db"

	err := ValidateStartupDiskSpace(dbPath)
	if err == nil {
		t.Fatal("Expected error for nonexistent path")
	}

	// Verify it's a structured error
	var enhancedErr *errors.EnhancedError
	if !errors.As(err, &enhancedErr) {
		t.Fatal("Expected EnhancedError")
	}

	// Verify error metadata
	if enhancedErr.GetComponent() != "datastore" {
		t.Errorf("Error component = %v, want 'datastore'", enhancedErr.GetComponent())
	}

	if enhancedErr.GetCategory() != string(errors.CategorySystem) {
		t.Errorf("Error category = %v, want %v", enhancedErr.GetCategory(), errors.CategorySystem)
	}

	if enhancedErr.GetPriority() != errors.PriorityCritical {
		t.Errorf("Error priority = %v, want %v", enhancedErr.GetPriority(), errors.PriorityCritical)
	}

	// Verify error context fields
	ctx := enhancedErr.GetContext()
	expectedFields := []string{"operation", "path"}
	for _, field := range expectedFields {
		if _, ok := ctx[field]; !ok {
			t.Errorf("Error context missing field %q", field)
		}
	}
}

func TestValidateStartupDiskSpace_InsufficientSpace_ErrorFormat(t *testing.T) {
	// This test validates the error format when disk space is insufficient
	// We can't reliably simulate insufficient space across environments,
	// but we verify the constant value and error message format expectations

	// Verify the constant has the expected value of 1GB (1024MB)
	const expectedMinDiskSpace = 1024
	if MinDiskSpaceStartup != expectedMinDiskSpace {
		t.Errorf("MinDiskSpaceStartup = %v, want %v (1GB)", MinDiskSpaceStartup, expectedMinDiskSpace)
	}

	// Note: Testing actual insufficient space scenario is environment-dependent
	// In a real insufficient space scenario, the error should contain:
	// - "insufficient disk space to start application"
	// - Available MB, required MB (1024), total MB
	// - Context fields: disk_free_mb, disk_required_mb, disk_total_mb, db_path
}
