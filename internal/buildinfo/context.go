// Package buildinfo contains build-time metadata and validation state separate from user configuration
package buildinfo

// BuildInfo provides an interface for accessing build-time metadata.
// This interface makes testing easier and allows for different implementations.
type BuildInfo interface {
	// GetVersion returns the build version string
	GetVersion() string
	// GetBuildDate returns the build date string  
	GetBuildDate() string
	// GetSystemID returns the unique system identifier
	GetSystemID() string
}

// Context contains build-time metadata that is not user-configurable
// This data is injected at application startup and should not be part
// of the configuration system.
type Context struct {
	// Version holds the Git version tag from build
	Version string

	// BuildDate is the time when the binary was built
	BuildDate string

	// SystemID is a unique system identifier for telemetry
	SystemID string
}

// ValidationResult holds validation outcomes separately from configuration
// This prevents mixing validation state with configuration data.
type ValidationResult struct {
	// Warnings are configuration issues that don't prevent startup
	Warnings []string `json:"warnings,omitempty"`

	// Errors are critical issues that should prevent startup
	Errors []string `json:"errors,omitempty"`

	// Valid indicates if the configuration passed validation
	Valid bool `json:"valid"`
}

// AddWarning adds a warning to the validation result
func (r *ValidationResult) AddWarning(message string) {
	r.Warnings = append(r.Warnings, message)
}

// AddError adds an error to the validation result
func (r *ValidationResult) AddError(message string) {
	r.Errors = append(r.Errors, message)
	r.Valid = false
}

// HasIssues returns true if there are any warnings or errors
func (r *ValidationResult) HasIssues() bool {
	return len(r.Warnings) > 0 || len(r.Errors) > 0
}

// NewValidationResult creates a new validation result with Valid set to true
func NewValidationResult() *ValidationResult {
	return &ValidationResult{
		Valid: true,
	}
}

// GetVersion implements BuildInfo.GetVersion
func (c *Context) GetVersion() string {
	if c == nil {
		return "unknown"
	}
	if c.Version == "" {
		return "unknown"
	}
	return c.Version
}

// GetBuildDate implements BuildInfo.GetBuildDate
func (c *Context) GetBuildDate() string {
	if c == nil {
		return "unknown"
	}
	if c.BuildDate == "" {
		return "unknown"
	}
	return c.BuildDate
}

// GetSystemID implements BuildInfo.GetSystemID
func (c *Context) GetSystemID() string {
	if c == nil {
		return "unknown"
	}
	if c.SystemID == "" {
		return "unknown"
	}
	return c.SystemID
}