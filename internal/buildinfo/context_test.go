package buildinfo

import (
	"testing"
)

// Test Context methods
func TestContext_Version(t *testing.T) {
	tests := []struct {
		name string
		ctx  *Context
		want string
	}{
		{
			name: "nil context",
			ctx:  nil,
			want: UnknownValue,
		},
		{
			name: "empty version",
			ctx:  NewContext("", "2023-01-01", "test-system"),
			want: UnknownValue,
		},
		{
			name: "valid version",
			ctx:  NewContext("1.0.0", "2023-01-01", "test-system"),
			want: "1.0.0",
		},
		{
			name: "version with pre-release tag",
			ctx:  NewContext("1.0.0-beta.1", "2023-01-01", "test-system"),
			want: "1.0.0-beta.1",
		},
		{
			name: "version with build metadata",
			ctx:  NewContext("1.0.0+build.123", "2023-01-01", "test-system"),
			want: "1.0.0+build.123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ctx.Version()
			if got != tt.want {
				t.Errorf("Context.Version() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContext_BuildDate(t *testing.T) {
	tests := []struct {
		name string
		ctx  *Context
		want string
	}{
		{
			name: "nil context",
			ctx:  nil,
			want: UnknownValue,
		},
		{
			name: "empty build date",
			ctx:  NewContext("1.0.0", "", "test-system"),
			want: UnknownValue,
		},
		{
			name: "valid build date",
			ctx:  NewContext("1.0.0", "2023-01-01T12:00:00Z", "test-system"),
			want: "2023-01-01T12:00:00Z",
		},
		{
			name: "build date with timezone",
			ctx:  NewContext("1.0.0", "2023-01-01 12:00:00 UTC", "test-system"),
			want: "2023-01-01 12:00:00 UTC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ctx.BuildDate()
			if got != tt.want {
				t.Errorf("Context.BuildDate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContext_SystemID(t *testing.T) {
	tests := []struct {
		name string
		ctx  *Context
		want string
	}{
		{
			name: "nil context",
			ctx:  nil,
			want: UnknownValue,
		},
		{
			name: "empty system ID",
			ctx:  NewContext("1.0.0", "2023-01-01", ""),
			want: UnknownValue,
		},
		{
			name: "valid system ID",
			ctx:  NewContext("1.0.0", "2023-01-01", "test-system-123"),
			want: "test-system-123",
		},
		{
			name: "UUID system ID",
			ctx:  NewContext("1.0.0", "2023-01-01", "550e8400-e29b-41d4-a716-446655440000"),
			want: "550e8400-e29b-41d4-a716-446655440000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ctx.SystemID()
			if got != tt.want {
				t.Errorf("Context.SystemID() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test NewContext constructor
func TestNewContext(t *testing.T) {
	version := "1.2.3"
	buildDate := "2023-12-25T10:30:00Z"
	systemID := "test-system-456"

	ctx := NewContext(version, buildDate, systemID)

	if ctx == nil {
		t.Fatal("NewContext() returned nil")
	}

	if got := ctx.Version(); got != version {
		t.Errorf("Version() = %v, want %v", got, version)
	}

	if got := ctx.BuildDate(); got != buildDate {
		t.Errorf("BuildDate() = %v, want %v", got, buildDate)
	}

	if got := ctx.SystemID(); got != systemID {
		t.Errorf("SystemID() = %v, want %v", got, systemID)
	}
}

// Test deprecated methods for backward compatibility
func TestContext_DeprecatedMethods(t *testing.T) {
	ctx := NewContext("1.0.0", "2023-01-01", "test-system")

	// Test that deprecated methods return the same values as new methods
	if got := ctx.GetVersion(); got != ctx.Version() {
		t.Errorf("GetVersion() = %v, want %v (should match Version())", got, ctx.Version())
	}

	if got := ctx.GetBuildDate(); got != ctx.BuildDate() {
		t.Errorf("GetBuildDate() = %v, want %v (should match BuildDate())", got, ctx.BuildDate())
	}

	if got := ctx.GetSystemID(); got != ctx.SystemID() {
		t.Errorf("GetSystemID() = %v, want %v (should match SystemID())", got, ctx.SystemID())
	}
}

// Test deprecated methods with nil context
func TestContext_DeprecatedMethods_NilContext(t *testing.T) {
	var ctx *Context

	if got := ctx.GetVersion(); got != UnknownValue {
		t.Errorf("GetVersion() on nil context = %v, want %v", got, UnknownValue)
	}

	if got := ctx.GetBuildDate(); got != UnknownValue {
		t.Errorf("GetBuildDate() on nil context = %v, want %v", got, UnknownValue)
	}

	if got := ctx.GetSystemID(); got != UnknownValue {
		t.Errorf("GetSystemID() on nil context = %v, want %v", got, UnknownValue)
	}
}

// Test interface compliance
func TestContext_ImplementsBuildInfo(t *testing.T) {
	var _ BuildInfo = (*Context)(nil)
	
	// Test that a Context instance can be used as BuildInfo
	ctx := NewContext("1.0.0", "2023-01-01", "test-system")
	var info BuildInfo = ctx

	if got := info.Version(); got != "1.0.0" {
		t.Errorf("BuildInfo.Version() = %v, want %v", got, "1.0.0")
	}

	if got := info.BuildDate(); got != "2023-01-01" {
		t.Errorf("BuildInfo.BuildDate() = %v, want %v", got, "2023-01-01")
	}

	if got := info.SystemID(); got != "test-system" {
		t.Errorf("BuildInfo.SystemID() = %v, want %v", got, "test-system")
	}
}

// Test ValidationResult methods
func TestNewValidationResult(t *testing.T) {
	result := NewValidationResult()

	if result == nil {
		t.Fatal("NewValidationResult() returned nil")
	}

	if !result.Valid {
		t.Error("NewValidationResult() should create a valid result")
	}

	if result.HasIssues() {
		t.Error("NewValidationResult() should not have issues initially")
	}

	if len(result.Warnings) != 0 {
		t.Errorf("NewValidationResult() warnings length = %d, want 0", len(result.Warnings))
	}

	if len(result.Errors) != 0 {
		t.Errorf("NewValidationResult() errors length = %d, want 0", len(result.Errors))
	}
}

func TestValidationResult_AddWarning(t *testing.T) {
	result := NewValidationResult()

	// Initially no issues
	if result.HasIssues() {
		t.Error("ValidationResult should not have issues initially")
	}

	// Add warning
	result.AddWarning("test warning")

	if !result.HasIssues() {
		t.Error("ValidationResult should have issues after adding warning")
	}

	if !result.Valid {
		t.Error("ValidationResult should still be valid after adding warning")
	}

	if len(result.Warnings) != 1 {
		t.Errorf("Warnings length = %d, want 1", len(result.Warnings))
	}

	if result.Warnings[0] != "test warning" {
		t.Errorf("Warning = %v, want %v", result.Warnings[0], "test warning")
	}

	// Add another warning
	result.AddWarning("second warning")

	if len(result.Warnings) != 2 {
		t.Errorf("Warnings length = %d, want 2", len(result.Warnings))
	}
}

func TestValidationResult_AddError(t *testing.T) {
	result := NewValidationResult()

	// Initially no issues and valid
	if result.HasIssues() {
		t.Error("ValidationResult should not have issues initially")
	}

	if !result.Valid {
		t.Error("ValidationResult should be valid initially")
	}

	// Add error
	result.AddError("test error")

	if !result.HasIssues() {
		t.Error("ValidationResult should have issues after adding error")
	}

	if result.Valid {
		t.Error("ValidationResult should not be valid after adding error")
	}

	if len(result.Errors) != 1 {
		t.Errorf("Errors length = %d, want 1", len(result.Errors))
	}

	if result.Errors[0] != "test error" {
		t.Errorf("Error = %v, want %v", result.Errors[0], "test error")
	}

	// Add another error
	result.AddError("second error")

	if len(result.Errors) != 2 {
		t.Errorf("Errors length = %d, want 2", len(result.Errors))
	}

	// Valid should still be false
	if result.Valid {
		t.Error("ValidationResult should remain invalid after adding multiple errors")
	}
}

func TestValidationResult_HasIssues(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(*ValidationResult)
		want      bool
	}{
		{
			name:      "no issues",
			setupFunc: func(r *ValidationResult) {},
			want:      false,
		},
		{
			name: "with warning",
			setupFunc: func(r *ValidationResult) {
				r.AddWarning("test warning")
			},
			want: true,
		},
		{
			name: "with error",
			setupFunc: func(r *ValidationResult) {
				r.AddError("test error")
			},
			want: true,
		},
		{
			name: "with both warning and error",
			setupFunc: func(r *ValidationResult) {
				r.AddWarning("test warning")
				r.AddError("test error")
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewValidationResult()
			tt.setupFunc(result)

			got := result.HasIssues()
			if got != tt.want {
				t.Errorf("ValidationResult.HasIssues() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test edge cases and boundary conditions
func TestContext_EdgeCases(t *testing.T) {
	t.Run("all empty strings", func(t *testing.T) {
		ctx := NewContext("", "", "")

		if got := ctx.Version(); got != UnknownValue {
			t.Errorf("Version() with empty string = %v, want %v", got, UnknownValue)
		}

		if got := ctx.BuildDate(); got != UnknownValue {
			t.Errorf("BuildDate() with empty string = %v, want %v", got, UnknownValue)
		}

		if got := ctx.SystemID(); got != UnknownValue {
			t.Errorf("SystemID() with empty string = %v, want %v", got, UnknownValue)
		}
	})

	t.Run("whitespace-only strings", func(t *testing.T) {
		ctx := NewContext(" ", "\t", "\n")

		// Whitespace-only strings should be preserved (not treated as empty)
		if got := ctx.Version(); got != " " {
			t.Errorf("Version() with whitespace = %v, want %v", got, " ")
		}

		if got := ctx.BuildDate(); got != "\t" {
			t.Errorf("BuildDate() with whitespace = %v, want %v", got, "\t")
		}

		if got := ctx.SystemID(); got != "\n" {
			t.Errorf("SystemID() with whitespace = %v, want %v", got, "\n")
		}
	})
}

// Benchmark tests for performance
func BenchmarkContext_Version(b *testing.B) {
	ctx := NewContext("1.0.0", "2023-01-01", "test-system")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ctx.Version()
	}
}

func BenchmarkContext_Version_Nil(b *testing.B) {
	var ctx *Context
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ctx.Version()
	}
}

func BenchmarkNewContext(b *testing.B) {
	version := "1.2.3"
	buildDate := "2023-12-25T10:30:00Z"
	systemID := "test-system-456"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = NewContext(version, buildDate, systemID)
	}
}