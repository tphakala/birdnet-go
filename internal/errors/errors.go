// Package errors provides centralized error handling with optional telemetry integration
package errors

import (
	stderrors "errors"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// ErrorCategory represents the type of error for better categorization
type ErrorCategory string

const (
	CategoryModelInit     ErrorCategory = "model-initialization"
	CategoryModelLoad     ErrorCategory = "model-loading"
	CategoryLabelLoad     ErrorCategory = "label-loading"
	CategoryValidation    ErrorCategory = "validation"
	CategoryFileIO        ErrorCategory = "file-io"
	CategoryNetwork       ErrorCategory = "network"
	CategoryAudio         ErrorCategory = "audio-processing"
	CategoryRTSP          ErrorCategory = "rtsp-connection"
	CategoryDatabase      ErrorCategory = "database"
	CategoryHTTP          ErrorCategory = "http-request"
	CategoryConfiguration ErrorCategory = "configuration"
	CategorySystem        ErrorCategory = "system-resource"
	CategoryGeneric       ErrorCategory = "generic"
)

// EnhancedError wraps an error with additional context and metadata
type EnhancedError struct {
	Err       error                  // Original error
	Component string                 // Component where error occurred (auto-detected if empty)
	Category  ErrorCategory          // Error category for better grouping
	Context   map[string]interface{} // Additional context data
	Timestamp time.Time              // When the error occurred
	reported  bool                   // Whether telemetry has been sent
}

// Error implements the error interface
func (ee *EnhancedError) Error() string {
	return ee.Err.Error()
}

// Unwrap implements the error unwrapping interface
func (ee *EnhancedError) Unwrap() error {
	return ee.Err
}

// Is implements error type checking
func (ee *EnhancedError) Is(target error) bool {
	if ee2, ok := target.(*EnhancedError); ok {
		return ee.Category == ee2.Category
	}
	return ee.Err == target
}

// GetComponent returns the component name
func (ee *EnhancedError) GetComponent() string {
	return ee.Component
}

// GetCategory returns the error category
func (ee *EnhancedError) GetCategory() ErrorCategory {
	return ee.Category
}

// GetContext returns the error context data
func (ee *EnhancedError) GetContext() map[string]interface{} {
	return ee.Context
}

// GetTimestamp returns when the error occurred
func (ee *EnhancedError) GetTimestamp() time.Time {
	return ee.Timestamp
}

// MarkReported marks this error as reported to telemetry
func (ee *EnhancedError) MarkReported() {
	ee.reported = true
}

// IsReported returns whether this error has been reported
func (ee *EnhancedError) IsReported() bool {
	return ee.reported
}

// ErrorBuilder provides a fluent interface for creating enhanced errors
type ErrorBuilder struct {
	err       error
	component string
	category  ErrorCategory
	context   map[string]interface{}
}

// New creates a new error with enhanced context
func New(err error) *ErrorBuilder {
	return &ErrorBuilder{
		err:     err,
		context: make(map[string]interface{}),
	}
}

// Newf creates a new formatted error with enhanced context
func Newf(format string, args ...interface{}) *ErrorBuilder {
	return New(fmt.Errorf(format, args...))
}

// Component sets the component name (auto-detected if not set)
func (eb *ErrorBuilder) Component(component string) *ErrorBuilder {
	eb.component = component
	return eb
}

// Category sets the error category for better grouping
func (eb *ErrorBuilder) Category(category ErrorCategory) *ErrorBuilder {
	eb.category = category
	return eb
}

// Context adds context data to the error
func (eb *ErrorBuilder) Context(key string, value interface{}) *ErrorBuilder {
	eb.context[key] = value
	return eb
}

// ModelContext adds model-specific context
func (eb *ErrorBuilder) ModelContext(modelPath, modelVersion string) *ErrorBuilder {
	if modelPath != "" {
		eb.context["model_path_type"] = categorizeModelPath(modelPath)
	}
	if modelVersion != "" {
		eb.context["model_version"] = modelVersion
	}
	return eb
}

// FileContext adds file-specific context (path is anonymized)
func (eb *ErrorBuilder) FileContext(filePath string, fileSize int64) *ErrorBuilder {
	if filePath != "" {
		eb.context["file_type"] = categorizeFilePath(filePath)
		eb.context["file_extension"] = getFileExtension(filePath)
	}
	if fileSize > 0 {
		eb.context["file_size_category"] = categorizeFileSize(fileSize)
	}
	return eb
}

// NetworkContext adds network-specific context (URLs are anonymized)
func (eb *ErrorBuilder) NetworkContext(url string, timeout time.Duration) *ErrorBuilder {
	if url != "" {
		eb.context["url_category"] = categorizeURL(url)
	}
	if timeout > 0 {
		eb.context["timeout_seconds"] = timeout.Seconds()
	}
	return eb
}

// Timing adds performance timing context
func (eb *ErrorBuilder) Timing(operation string, duration time.Duration) *ErrorBuilder {
	eb.context["operation"] = operation
	eb.context["duration_ms"] = duration.Milliseconds()
	return eb
}

// Build creates the EnhancedError and triggers optional telemetry reporting
func (eb *ErrorBuilder) Build() *EnhancedError {
	// Auto-detect component if not set
	if eb.component == "" {
		eb.component = detectComponent()
	}

	// Auto-detect category if not set
	if eb.category == "" {
		eb.category = detectCategory(eb.err, eb.component)
	}

	ee := &EnhancedError{
		Err:       eb.err,
		Component: eb.component,
		Category:  eb.category,
		Context:   eb.context,
		Timestamp: time.Now(),
	}

	// Report to telemetry if available and enabled
	reportToTelemetry(ee)

	return ee
}

// Helper functions for auto-detection and categorization

// detectComponent automatically detects the component based on the call stack
func detectComponent() string {
	pc, _, _, ok := runtime.Caller(4) // Skip New -> Build -> detectComponent -> reportToTelemetry
	if !ok {
		return "unknown"
	}

	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return "unknown"
	}

	funcName := fn.Name()

	// Extract component from package path
	if strings.Contains(funcName, "birdnet") {
		return "birdnet"
	}
	if strings.Contains(funcName, "myaudio") {
		return "myaudio"
	}
	if strings.Contains(funcName, "httpcontroller") {
		return "http-controller"
	}
	if strings.Contains(funcName, "datastore") {
		return "datastore"
	}

	// Extract from package path
	parts := strings.Split(funcName, "/")
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]
		if dotIndex := strings.Index(lastPart, "."); dotIndex > 0 {
			return lastPart[:dotIndex]
		}
	}

	return "unknown"
}

// detectCategory automatically detects error category based on error message and component
func detectCategory(err error, component string) ErrorCategory {
	errorMsg := strings.ToLower(err.Error())

	// Model-related errors
	if strings.Contains(errorMsg, "model") {
		if strings.Contains(errorMsg, "load") || strings.Contains(errorMsg, "read") {
			return CategoryModelLoad
		}
		if strings.Contains(errorMsg, "init") || strings.Contains(errorMsg, "create") {
			return CategoryModelInit
		}
	}

	// Label-related errors
	if strings.Contains(errorMsg, "label") {
		return CategoryLabelLoad
	}

	// File I/O errors
	if strings.Contains(errorMsg, "file") || strings.Contains(errorMsg, "read") || strings.Contains(errorMsg, "open") {
		return CategoryFileIO
	}

	// Network errors
	if strings.Contains(errorMsg, "connection") || strings.Contains(errorMsg, "timeout") || strings.Contains(errorMsg, "rtsp") {
		if component == "myaudio" || strings.Contains(errorMsg, "rtsp") {
			return CategoryRTSP
		}
		return CategoryNetwork
	}

	// Validation errors
	if strings.Contains(errorMsg, "validation") || strings.Contains(errorMsg, "mismatch") || strings.Contains(errorMsg, "invalid") {
		return CategoryValidation
	}

	// Component-based detection
	switch component {
	case "birdnet":
		return CategoryModelInit
	case "myaudio":
		return CategoryAudio
	case "datastore":
		return CategoryDatabase
	case "http-controller":
		return CategoryHTTP
	}

	return CategoryGeneric
}

// categorizeModelPath anonymizes model file paths while preserving useful info
func categorizeModelPath(path string) string {
	if path == "" {
		return "embedded"
	}
	if strings.Contains(strings.ToLower(path), "birdnet") {
		return "external-birdnet"
	}
	return "external-custom"
}

// categorizeFilePath anonymizes file paths while preserving useful structure info
func categorizeFilePath(path string) string {
	if strings.Contains(path, "/") || strings.Contains(path, "\\") {
		return "absolute-path"
	}
	return "relative-path"
}

// getFileExtension extracts file extension for categorization
func getFileExtension(path string) string {
	if lastDot := strings.LastIndex(path, "."); lastDot > 0 && lastDot < len(path)-1 {
		return strings.ToLower(path[lastDot+1:])
	}
	return "none"
}

// categorizeFileSize groups file sizes into categories
func categorizeFileSize(size int64) string {
	switch {
	case size < 1024: // < 1KB
		return "tiny"
	case size < 1024*1024: // < 1MB
		return "small"
	case size < 10*1024*1024: // < 10MB
		return "medium"
	case size < 100*1024*1024: // < 100MB
		return "large"
	default:
		return "very-large"
	}
}

// categorizeURL anonymizes URLs while preserving protocol and basic structure
func categorizeURL(url string) string {
	url = strings.ToLower(url)
	switch {
	case strings.HasPrefix(url, "rtsp://"):
		return "rtsp-stream"
	case strings.HasPrefix(url, "http://"):
		return "http-endpoint"
	case strings.HasPrefix(url, "https://"):
		return "https-endpoint"
	default:
		return "other-protocol"
	}
}

// Convenience functions for common error patterns

// Wrap wraps an existing error with enhanced context
func Wrap(err error) *ErrorBuilder {
	return New(err)
}

// ModelError creates a model-related error with appropriate context
func ModelError(err error, modelPath, modelVersion string) *EnhancedError {
	return New(err).
		Category(CategoryModelInit).
		ModelContext(modelPath, modelVersion).
		Build()
}

// FileError creates a file I/O error with appropriate context
func FileError(err error, filePath string, fileSize int64) *EnhancedError {
	return New(err).
		Category(CategoryFileIO).
		FileContext(filePath, fileSize).
		Build()
}

// NetworkError creates a network error with appropriate context
func NetworkError(err error, url string, timeout time.Duration) *EnhancedError {
	return New(err).
		Category(CategoryNetwork).
		NetworkContext(url, timeout).
		Build()
}

// ValidationError creates a validation error
func ValidationError(message string) *EnhancedError {
	return New(fmt.Errorf("%s", message)).
		Category(CategoryValidation).
		Build()
}

// Standard library passthrough functions
// These allow this package to be a drop-in replacement for the standard errors package

// NewStd creates a new standard error (passthrough to standard library)
func NewStd(text string) error {
	return stderrors.New(text)
}

// Is reports whether any error in err's tree matches target (passthrough to standard library)
func Is(err, target error) bool {
	return stderrors.Is(err, target)
}

// As finds the first error in err's tree that matches target (passthrough to standard library)
func As(err error, target interface{}) bool {
	return stderrors.As(err, target)
}

// Unwrap returns the result of calling the Unwrap method on err (passthrough to standard library)
func Unwrap(err error) error {
	return stderrors.Unwrap(err)
}
