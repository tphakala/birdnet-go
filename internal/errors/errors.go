// Package errors provides centralized error handling with optional telemetry integration
package errors

import (
	stderrors "errors"
	"fmt"
	"maps"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ErrorCategory represents the type of error for better categorization
type ErrorCategory string

// CategorizedError is an interface for errors that can specify their own category
type CategorizedError interface {
	error
	ErrorCategory() ErrorCategory
}

const (
	CategoryModelInit      ErrorCategory = "model-initialization"
	CategoryModelLoad      ErrorCategory = "model-loading"
	CategoryLabelLoad      ErrorCategory = "label-loading"
	CategoryValidation     ErrorCategory = "validation"
	CategoryFileIO         ErrorCategory = "file-io"
	CategoryNetwork        ErrorCategory = "network"
	CategoryAudio          ErrorCategory = "audio-processing"
	CategoryAudioSource    ErrorCategory = "audio-source"
	CategoryRTSP           ErrorCategory = "rtsp-connection"
	CategoryDatabase       ErrorCategory = "database"
	CategoryHTTP           ErrorCategory = "http-request"
	CategoryConfiguration  ErrorCategory = "configuration"
	CategorySystem         ErrorCategory = "system-resource"
	CategoryDiskUsage      ErrorCategory = "disk-usage"
	CategoryDiskCleanup    ErrorCategory = "disk-cleanup"
	CategoryFileParsing    ErrorCategory = "file-parsing"
	CategoryPolicyConfig   ErrorCategory = "policy-config"
	CategoryMQTTConnection ErrorCategory = "mqtt-connection"
	CategoryMQTTPublish    ErrorCategory = "mqtt-publish"
	CategoryMQTTAuth       ErrorCategory = "mqtt-authentication"
	CategoryImageFetch     ErrorCategory = "image-fetch"
	CategoryImageCache     ErrorCategory = "image-cache"
	CategoryImageProvider  ErrorCategory = "image-provider"
	CategoryGeneric        ErrorCategory = "generic"
	CategoryNotFound       ErrorCategory = "not-found"
	CategoryConflict       ErrorCategory = "conflict"
	CategoryProcessing     ErrorCategory = "processing"
	CategoryState          ErrorCategory = "state"
	CategoryLimit          ErrorCategory = "limit"
	CategoryResource       ErrorCategory = "resource"

	// Analysis package specific categories
	CategoryAudioAnalysis    ErrorCategory = "audio-analysis"    // BirdNET prediction/analysis errors
	CategoryBuffer           ErrorCategory = "audio-buffer"      // Audio buffer management
	CategoryWorker           ErrorCategory = "worker-pool"       // Worker pool operations
	CategoryJobQueue         ErrorCategory = "job-queue"         // Job queue operations
	CategoryThreshold        ErrorCategory = "threshold-mgmt"    // Dynamic threshold management
	CategoryEventTracking    ErrorCategory = "event-tracking"    // Event tracking operations
	CategorySpeciesTracking  ErrorCategory = "species-tracking"  // Species tracking operations
	CategorySoundLevel       ErrorCategory = "sound-level"       // Sound level monitoring
	CategoryCommandExecution ErrorCategory = "command-execution" // External command execution

	// General categories useful across packages
	CategoryTimeout      ErrorCategory = "timeout"      // Operation timeouts
	CategoryCancellation ErrorCategory = "cancellation" // Cancelled operations
	CategoryRetry        ErrorCategory = "retry"        // Retry-related errors
	CategoryBroadcast    ErrorCategory = "broadcast"    // SSE/broadcast operations
	CategoryIntegration  ErrorCategory = "integration"  // Third-party integrations
)

// Priority constants for error prioritization
const (
	PriorityLow      = "low"
	PriorityMedium   = "medium"
	PriorityHigh     = "high"
	PriorityCritical = "critical"
)

// ComponentUnknown is used when the component cannot be determined.
const ComponentUnknown = "unknown"

// EnhancedError wraps an error with additional context and metadata
type EnhancedError struct {
	Err       error          // Original error
	component string         // Component where error occurred (lazily detected)
	Category  ErrorCategory  // Error category for better grouping
	Priority  string         // Explicit priority override (optional)
	Context   map[string]any // Additional context data
	Timestamp time.Time      // When the error occurred
	reported  bool           // Whether telemetry has been sent
	mu        sync.RWMutex   // Mutex to protect concurrent access
	detected  bool           // Whether component has been auto-detected
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
	return Is(ee.Err, target)
}

// GetComponent returns the component name, detecting it lazily if needed
func (ee *EnhancedError) GetComponent() string {
	// Fast path: try read lock first for already detected components
	ee.mu.RLock()
	if ee.detected || ee.component != "" {
		component := ee.component
		ee.mu.RUnlock()
		return component
	}
	ee.mu.RUnlock()

	// Slow path: need to detect component, use full lock
	ee.mu.Lock()
	defer ee.mu.Unlock()

	// Double-check in case another goroutine detected it while we were waiting
	if ee.component == "" && !ee.detected {
		ee.component = detectComponent()
		ee.detected = true
		// Set to "unknown" if detection failed
		if ee.component == "" {
			ee.component = ComponentUnknown
		}
	}

	return ee.component
}

// GetCategory returns the error category
func (ee *EnhancedError) GetCategory() string {
	return string(ee.Category)
}

// GetPriority returns the explicit priority if set, empty string otherwise
func (ee *EnhancedError) GetPriority() string {
	return ee.Priority
}

// GetContext returns the error context
func (ee *EnhancedError) GetContext() map[string]any {
	ee.mu.RLock()
	defer ee.mu.RUnlock()

	// Return a copy to prevent external modification
	if ee.Context == nil {
		return nil
	}

	contextCopy := make(map[string]any, len(ee.Context))
	maps.Copy(contextCopy, ee.Context)
	return contextCopy
}

// GetTimestamp returns when the error occurred
func (ee *EnhancedError) GetTimestamp() time.Time {
	return ee.Timestamp
}

// GetError returns the underlying error
func (ee *EnhancedError) GetError() error {
	return ee.Err
}

// GetMessage returns the error message
func (ee *EnhancedError) GetMessage() string {
	if ee.Err != nil {
		return ee.Err.Error()
	}
	return ""
}

// MarkReported marks this error as reported to telemetry
func (ee *EnhancedError) MarkReported() {
	ee.mu.Lock()
	defer ee.mu.Unlock()
	ee.reported = true
}

// IsReported returns whether this error has been reported
func (ee *EnhancedError) IsReported() bool {
	ee.mu.RLock()
	defer ee.mu.RUnlock()
	return ee.reported
}

// ErrorBuilder provides a fluent interface for creating enhanced errors
type ErrorBuilder struct {
	err       error
	component string
	category  ErrorCategory
	priority  string
	context   map[string]any
}

// New creates a new error with enhanced context
func New(err error) *ErrorBuilder {
	return &ErrorBuilder{
		err: err,
		// context is lazily initialized when needed
	}
}

// Newf creates a new formatted error with enhanced context
func Newf(format string, args ...any) *ErrorBuilder {
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

// Priority sets the explicit priority override for the error
func (eb *ErrorBuilder) Priority(priority string) *ErrorBuilder {
	// Validate priority value
	switch priority {
	case PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical:
		eb.priority = priority
	default:
		// Invalid priority value - use medium as safe default
		if priority != "" {
			// Note: We can't use structured logging here to avoid circular dependencies
			// If an invalid priority is provided, silently fall back to medium
			eb.priority = PriorityMedium
		}
	}
	return eb
}

// Context adds context data to the error
func (eb *ErrorBuilder) Context(key string, value any) *ErrorBuilder {
	if eb.context == nil {
		eb.context = make(map[string]any)
	}
	eb.context[key] = value
	return eb
}

// ModelContext adds model-specific context
func (eb *ErrorBuilder) ModelContext(modelPath, modelVersion string) *ErrorBuilder {
	if modelPath != "" {
		if eb.context == nil {
			eb.context = make(map[string]any)
		}
		eb.context["model_path_type"] = categorizeModelPath(modelPath)
	}
	if modelVersion != "" {
		if eb.context == nil {
			eb.context = make(map[string]any)
		}
		eb.context["model_version"] = modelVersion
	}
	return eb
}

// FileContext adds file-specific context (path is anonymized)
func (eb *ErrorBuilder) FileContext(filePath string, fileSize int64) *ErrorBuilder {
	if filePath != "" {
		if eb.context == nil {
			eb.context = make(map[string]any)
		}
		eb.context["file_type"] = categorizeFilePath(filePath)
		eb.context["file_extension"] = getFileExtension(filePath)
	}
	if fileSize > 0 {
		if eb.context == nil {
			eb.context = make(map[string]any)
		}
		eb.context["file_size_category"] = categorizeFileSize(fileSize)
	}
	return eb
}

// NetworkContext adds network-specific context (URLs are anonymized)
func (eb *ErrorBuilder) NetworkContext(url string, timeout time.Duration) *ErrorBuilder {
	if url != "" {
		if eb.context == nil {
			eb.context = make(map[string]any)
		}
		eb.context["url_category"] = categorizeURL(url)
	}
	if timeout > 0 {
		if eb.context == nil {
			eb.context = make(map[string]any)
		}
		eb.context["timeout_seconds"] = timeout.Seconds()
	}
	return eb
}

// Timing adds performance timing context
func (eb *ErrorBuilder) Timing(operation string, duration time.Duration) *ErrorBuilder {
	if eb.context == nil {
		eb.context = make(map[string]any)
	}
	eb.context["operation"] = operation
	eb.context["duration_ms"] = duration.Milliseconds()
	return eb
}

// Build creates the EnhancedError and triggers optional telemetry reporting
func (eb *ErrorBuilder) Build() *EnhancedError {
	// Fast path - skip expensive operations if no reporting is active
	if !hasActiveReporting.Load() {
		ee := &EnhancedError{
			Err:       eb.err,
			component: eb.component, // Use provided or empty
			Category:  eb.category,  // Use provided or empty
			Priority:  eb.priority,  // Use provided or empty
			Context:   eb.context,
			Timestamp: time.Now(),
			detected:  eb.component != "", // Mark as detected if component was provided
		}
		// Set defaults without expensive detection
		if ee.component == "" {
			ee.component = ComponentUnknown
			ee.detected = true
		}
		if ee.Category == "" {
			ee.Category = CategoryGeneric
		}
		return ee
	}

	// Full path - perform auto-detection when reporting is active
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
		component: eb.component,
		Category:  eb.category,
		Priority:  eb.priority,
		Context:   eb.context,
		Timestamp: time.Now(),
		detected:  true, // Mark as detected since we just detected it
	}

	// Report to telemetry if available and enabled
	reportToTelemetry(ee)

	return ee
}

// Component registry for dynamic component detection
var (
	componentRegistry = make(map[string]string)
	registryMutex     sync.RWMutex
)

// RegisterComponent registers a package path pattern with a component name
func RegisterComponent(packagePattern, componentName string) {
	registryMutex.Lock()
	defer registryMutex.Unlock()
	componentRegistry[packagePattern] = componentName
}

// init registers default component mappings
func init() {
	// Core components
	RegisterComponent("birdnet", "birdnet")
	RegisterComponent("myaudio", "myaudio")
	RegisterComponent("ffmpeg-manager", "ffmpeg-manager")
	RegisterComponent("ffmpeg-stream", "ffmpeg-stream")
	RegisterComponent("datastore", "datastore")
	RegisterComponent("imageprovider", "imageprovider")
	RegisterComponent("diskmanager", "diskmanager")
	RegisterComponent("ebird", "ebird")
	RegisterComponent("mqtt", "mqtt")
	RegisterComponent("weather", "weather")
	RegisterComponent("conf", "configuration")
	RegisterComponent("telemetry", "telemetry")
	RegisterComponent("birdweather", "birdweather")
	RegisterComponent("backup", "backup")
	RegisterComponent("api", "api")

	// Analysis package components - use slash-separated paths for subpackages
	RegisterComponent("analysis", "analysis")
	RegisterComponent("analysis/processor", "analysis.processor")
	RegisterComponent("analysis/jobqueue", "analysis.jobqueue")
	// Components in main analysis package - use function name patterns
	RegisterComponent("soundlevel", "analysis.soundlevel")
	RegisterComponent("buffer", "analysis.buffer")
	RegisterComponent("worker", "analysis.worker")
	RegisterComponent("threshold", "analysis.threshold")
	RegisterComponent("tracker", "analysis.tracker")
}

// Helper functions for auto-detection and categorization

// quickComponentLookup tries to detect component from a specific caller depth
func quickComponentLookup(depth int) string {
	// Try to get caller at specific depth
	pc, _, _, ok := runtime.Caller(depth)
	if !ok {
		return ""
	}

	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return ""
	}

	funcName := fn.Name()

	// Skip if it's our own error package
	if strings.Contains(funcName, "github.com/tphakala/birdnet-go/internal/errors") {
		return ""
	}

	return lookupComponent(funcName)
}

// detectComponent automatically detects the component based on the call stack
func detectComponent() string {
	// First try common call depths for performance (adjust based on profiling)
	// Typical depths: 4-6 for direct error creation, 6-8 for wrapped errors
	for _, depth := range []int{4, 5, 6, 7} {
		if component := quickComponentLookup(depth); component != "" && component != ComponentUnknown {
			return component
		}
	}

	// Fall back to full stack walk if quick lookup failed
	return detectComponentFull()
}

// detectComponentFull walks the entire call stack to find the component
func detectComponentFull() string {
	// Walk the entire call stack to find the first recognizable component
	// This is more robust than hardcoded depths as it adapts to different call chains
	// Start with smaller buffer and grow if needed
	pcs := make([]uintptr, 16)   // Start with 16 frames
	n := runtime.Callers(2, pcs) // Skip runtime.Callers and detectComponentFull

	// If we filled the buffer, try again with larger size
	if n == len(pcs) {
		pcs = make([]uintptr, 32)
		n = runtime.Callers(2, pcs)
	}

	for i := range n {
		pc := pcs[i]
		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}

		funcName := fn.Name()

		// Skip internal error package functions
		if strings.Contains(funcName, "github.com/tphakala/birdnet-go/internal/errors") {
			continue
		}

		if component := lookupComponent(funcName); component != ComponentUnknown {
			return component
		}
	}

	return ComponentUnknown
}

// lookupComponent searches the registry for a matching component
func lookupComponent(funcName string) string {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	// Check registered patterns
	for pattern, component := range componentRegistry {
		if strings.Contains(funcName, pattern) {
			return component
		}
	}

	// Fallback: extract from package path
	parts := strings.Split(funcName, "/")
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]
		if dotIndex := strings.Index(lastPart, "."); dotIndex > 0 {
			return lastPart[:dotIndex]
		}
	}

	return ComponentUnknown
}

// detectCategory automatically detects error category based on error message and component
func detectCategory(err error, component string) ErrorCategory {
	// First check if the error implements CategorizedError interface
	if catErr, ok := stderrors.AsType[CategorizedError](err); ok {
		return catErr.ErrorCategory()
	}

	// Check if it's already an EnhancedError with a category
	if enhErr, ok := stderrors.AsType[*EnhancedError](err); ok && enhErr.Category != "" {
		return enhErr.Category
	}

	// Fall back to string-based heuristics
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
	case "imageprovider":
		if strings.Contains(errorMsg, "cache") {
			return CategoryImageCache
		}
		if strings.Contains(errorMsg, "fetch") || strings.Contains(errorMsg, "download") || strings.Contains(errorMsg, "url") {
			return CategoryImageFetch
		}
		return CategoryImageProvider
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
	return New(NewStd(message)).
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
func As(err error, target any) bool {
	return stderrors.As(err, target)
}

// Unwrap returns the result of calling the Unwrap method on err (passthrough to standard library)
func Unwrap(err error) error {
	return stderrors.Unwrap(err)
}

// Join returns an error that wraps the given errors (passthrough to standard library)
func Join(errs ...error) error {
	return stderrors.Join(errs...)
}

// IsCategory checks if an error is an EnhancedError with the specified category.
// This is a convenience function to reduce boilerplate when checking error categories.
func IsCategory(err error, category ErrorCategory) bool {
	var enhancedErr *EnhancedError
	return As(err, &enhancedErr) && enhancedErr.Category == category
}

// IsNotFound checks if an error is an EnhancedError with CategoryNotFound.
// This is commonly used for expected conditions like unknown species or missing resources.
func IsNotFound(err error) bool {
	return IsCategory(err, CategoryNotFound)
}
