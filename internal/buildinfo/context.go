// Package buildinfo contains build-time metadata and validation state separate from user configuration
package buildinfo

const (
	// UnknownValue is the fallback value used when build information is not available
	UnknownValue = "unknown"
)

// Compile-time assertion to ensure Context implements the BuildInfo interface
var _ BuildInfo = (*Context)(nil)

// BuildInfo provides an interface for accessing build-time metadata.
// This interface makes testing easier and allows for different implementations.
type BuildInfo interface {
	// Version returns the build version string
	Version() string
	// BuildDate returns the build date string  
	BuildDate() string
	// SystemID returns the unique system identifier
	SystemID() string
}

// Context contains build-time metadata that is not user-configurable
// This data is injected at application startup and should not be part
// of the configuration system.
type Context struct {
	// version holds the Git version tag from build
	version string

	// buildDate is the time when the binary was built
	buildDate string

	// systemID is a unique system identifier for telemetry
	systemID string
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

// Version implements BuildInfo.Version
func (c *Context) Version() string {
	if c == nil {
		return UnknownValue
	}
	if c.version == "" {
		return UnknownValue
	}
	return c.version
}

// BuildDate implements BuildInfo.BuildDate
func (c *Context) BuildDate() string {
	if c == nil {
		return UnknownValue
	}
	if c.buildDate == "" {
		return UnknownValue
	}
	return c.buildDate
}

// SystemID implements BuildInfo.SystemID
func (c *Context) SystemID() string {
	if c == nil {
		return UnknownValue
	}
	if c.systemID == "" {
		return UnknownValue
	}
	return c.systemID
}

// GetVersion provides backward compatibility - deprecated, use Version() instead
func (c *Context) GetVersion() string {
	return c.Version()
}

// GetBuildDate provides backward compatibility - deprecated, use BuildDate() instead  
func (c *Context) GetBuildDate() string {
	return c.BuildDate()
}

// GetSystemID provides backward compatibility - deprecated, use SystemID() instead
func (c *Context) GetSystemID() string {
	return c.SystemID()
}

// NewContext creates a new Context with the given version, build date, and system ID
func NewContext(version, buildDate, systemID string) *Context {
	return &Context{
		version:   version,
		buildDate: buildDate,
		systemID:  systemID,
	}
}