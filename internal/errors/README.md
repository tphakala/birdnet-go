# Enhanced Error Handling with Telemetry Integration

This package provides centralized error handling with automatic telemetry integration for improved observability and debugging.

## Overview

The enhanced error system automatically reports errors to Sentry with privacy-safe context data and meaningful error titles. This helps with:

- **Better Error Categorization**: Errors are categorized by type (validation, network, database, etc.)
- **Component Identification**: Automatic detection of which component generated the error
- **Privacy-Safe Telemetry**: Sensitive data is scrubbed before being sent to telemetry
- **Meaningful Sentry Titles**: Instead of generic `*errors.errorString`, Sentry receives descriptive titles

## Quick Start

### Important: Import Guidelines

**DO NOT** import the standard `errors` package alongside this custom errors package. This custom package provides passthrough functions for standard error operations:

```go
// ❌ WRONG - Do not import both
import (
    "errors"  // Don't do this
    "github.com/tphakala/birdnet-go/internal/errors"
)

// ✅ CORRECT - Import only the custom errors package
import "github.com/tphakala/birdnet-go/internal/errors"

// The custom package provides passthrough functions:
// errors.Is(), errors.As(), errors.Unwrap() are all available
```

### Basic Usage

```go
import "github.com/tphakala/birdnet-go/internal/errors"

// Simple error with automatic categorization
err := errors.New(fmt.Errorf("failed to connect to database")).
    Component("datastore").
    Category(errors.CategoryDatabase).
    Build()

// Error with context data
err := errors.New(originalErr).
    Component("imageprovider").
    Category(errors.CategoryImageFetch).
    Context("provider", "wikipedia").
    Context("operation", "fetch_thumbnail").
    Context("scientific_name", scientificName).
    Build()

// Creating descriptive error messages (recommended pattern)
err := errors.Newf("Wikipedia API response missing expected 'query.pages' structure").
    Component("imageprovider").
    Category(errors.CategoryImageFetch).
    Context("provider", "wikipedia").
    Context("operation", "parse_pages_from_response").
    Context("expected_path", "query.pages").
    Context("error_detail", originalErr.Error()).
    Build()
```

### Best Practices for Telemetry

#### 1. Always Specify Component and Category

```go
// Good - Clear component and category
err := errors.New(originalErr).
    Component("imageprovider").
    Category(errors.CategoryImageFetch).
    Build()

// Avoid - Missing component/category leads to poor Sentry titles
err := errors.New(originalErr).Build()
```

#### 2. Add Operation Context

The `operation` context field is used to generate meaningful Sentry titles:

```go
// This will create Sentry title: "Imageprovider Image Fetch Error Parse Pages From Response"
err := errors.New(originalErr).
    Component("imageprovider").
    Category(errors.CategoryImageFetch).
    Context("operation", "parse_pages_from_response").
    Build()
```

#### 3. Use Descriptive Operation Names

```go
// Good - Descriptive operation names
Context("operation", "save_daily_events")
Context("operation", "query_thumbnail")
Context("operation", "validate_scientific_name")

// Avoid - Generic or unclear operations
Context("operation", "process")
Context("operation", "handle")
```

#### 4. Create Descriptive Error Messages

Replace generic errors with specific, actionable messages:

```go
// Good - Specific error with details
var descriptiveMessage string
if err.Error() == "key not found" {
    descriptiveMessage = "Wikipedia API response missing expected 'query.pages' structure"
} else {
    descriptiveMessage = fmt.Sprintf("failed to parse Wikipedia API response: %s", err.Error())
}

err := errors.Newf("%s", descriptiveMessage).
    Component("imageprovider").
    Category(errors.CategoryImageFetch).
    Context("expected_path", "query.pages").
    Context("error_detail", err.Error()).
    Build()

// Avoid - Generic error messages
err := errors.New(err).
    Component("imageprovider").
    Category(errors.CategoryImageFetch).
    Build()
```

#### 5. Add Expected Path Context for API Errors

When parsing API responses, always include what was expected:

```go
// Good - Shows exactly what field was missing
Context("expected_path", "query.pages")
Context("expected_path", "thumbnail.source") 
Context("expected_path", "imageinfo[0].extmetadata")

// Also include the original error details
Context("error_detail", originalErr.Error())
```

## Error Categories

Use appropriate categories for better error grouping:

- `CategoryValidation`: Input validation errors
- `CategoryImageFetch`: Image fetching/download errors
- `CategoryImageCache`: Image caching errors
- `CategoryImageProvider`: Image provider errors
- `CategoryNetwork`: Network connectivity errors
- `CategoryDatabase`: Database operation errors
- `CategoryFileIO`: File system operations
- `CategoryModelInit`: Model initialization errors
- `CategoryModelLoad`: Model loading errors
- `CategoryConfiguration`: Configuration errors
- `CategorySystem`: System resource errors

## Context Data Guidelines

### Safe Context Data

These context types are safe and helpful for debugging:

```go
Context("operation", "fetch_thumbnail")        // Operation name
Context("provider", "wikipedia")               // Provider type
Context("table", "detections")                // Database table
Context("status_code", 404)                   // HTTP status
Context("retry_count", 3)                     // Retry attempts
Context("timeout_seconds", 30)                // Timeout values
```

### Sensitive Data Handling

The system automatically scrubs sensitive data, but follow these guidelines:

```go
// Good - Non-sensitive identifiers
Context("request_id", requestID)              // Short request ID
Context("file_extension", ".wav")             // File extension only
Context("url_category", "https-endpoint")     // URL category, not full URL

// Avoid - Full sensitive data (though it will be scrubbed)
Context("full_file_path", "/home/user/secret/file.txt")  // Will be scrubbed
Context("full_url", "https://api.example.com/secret")    // Will be scrubbed
```

## Component Guidelines

### Component Registry

Components must be registered in the error package's `init()` function to ensure proper telemetry tagging. When a component is not registered, the error system falls back to searching the entire call stack, which can lead to incorrect component attribution.

#### How Component Detection Works

1. **Explicit Component Setting**: When you use `.Component("name")`, this is the preferred method
2. **Automatic Detection**: If no component is explicitly set, the system:
   - First checks if the package name exists in the component registry
   - If not found, searches the entire call stack for any registered pattern
   - This can lead to incorrect tagging if another component appears in the call stack

#### Registering New Components

To register a new component, add it to the `init()` function in `/internal/errors/errors.go`:

```go
func init() {
    RegisterComponent("birdnet", "birdnet")
    RegisterComponent("myaudio", "myaudio")
    RegisterComponent("httpcontroller", "http-controller")
    RegisterComponent("datastore", "datastore")
    RegisterComponent("imageprovider", "imageprovider")
    RegisterComponent("diskmanager", "diskmanager")
    RegisterComponent("mqtt", "mqtt")
    RegisterComponent("weather", "weather")
    RegisterComponent("conf", "configuration")
    RegisterComponent("telemetry", "telemetry")
    RegisterComponent("birdweather", "birdweather")  // Add new components here
}
```

The `RegisterComponent` function takes two parameters:
- `packagePattern`: The pattern to match in the call stack (usually the package name)
- `componentName`: The name to use for telemetry (should match what you use with `.Component()`)

### Available Components

Components should match your package structure:

- `datastore`: Database operations
- `imageprovider`: Image fetching and caching
- `weather`: Weather data operations
- `suncalc`: Astronomical calculations
- `birdnet`: AI model operations
- `myaudio`: Audio processing
- `http-controller`: HTTP API operations
- `birdweather`: BirdWeather integration
- `diskmanager`: Disk space management
- `mqtt`: MQTT messaging
- `telemetry`: Telemetry reporting
- `configuration`: Configuration management

### Component Tagging Best Practices

1. **Always explicitly set the component** when creating errors:
   ```go
   err := errors.New(originalErr).
       Component("birdweather").  // Explicit is better than implicit
       Category(errors.CategoryNetwork).
       Build()
   ```

2. **Ensure your component is registered** if you rely on automatic detection:
   - Check that your package name is in the registry
   - Test that errors from your component are properly tagged

3. **Avoid relying on automatic detection** in shared code:
   - Handlers that process multiple components should always set component explicitly
   - Utility functions should let callers set the component

## Advanced Usage

### Wrapping Existing Errors

```go
// Wrap an existing error with enhanced context
if err != nil {
    return errors.New(err).
        Component("datastore").
        Category(errors.CategoryDatabase).
        Context("operation", "save_detection").
        Context("table", "detections").
        Build()
}
```

### Common Error Patterns

#### API Response Parsing Errors

```go
// Pattern for handling missing API fields
pages, err := resp.GetObjectArray("query", "pages")
if err != nil {
    var descriptiveMessage string
    if err.Error() == "key not found" {
        descriptiveMessage = "Wikipedia API response missing expected 'query.pages' structure"
    } else {
        descriptiveMessage = fmt.Sprintf("failed to parse Wikipedia API response pages: %s", err.Error())
    }
    
    return errors.Newf("%s", descriptiveMessage).
        Component("imageprovider").
        Category(errors.CategoryImageFetch).
        Context("provider", "wikipedia").
        Context("operation", "parse_pages_from_response").
        Context("expected_path", "query.pages").
        Context("error_detail", err.Error()).
        Build()
}
```

#### Database Operation Errors

```go
// Pattern for database errors with context
if err != nil {
    return errors.New(err).
        Component("datastore").
        Category(errors.CategoryDatabase).
        Context("operation", "save_detection").
        Context("table", "detections").
        Context("detection_id", detectionID).
        Build()
}
```

#### Validation Errors

```go
// Pattern for validation errors
if scientificName == "" {
    return errors.Newf("scientific name cannot be empty").
        Component("imageprovider").
        Category(errors.CategoryValidation).
        Context("provider", providerName).
        Context("operation", "validate_input").
        Build()
}
```

### Convenience Functions

```go
// For validation errors
err := errors.ValidationError("scientific name cannot be empty")

// For file errors with automatic privacy protection
err := errors.FileError(originalErr, filePath, fileSize)

// For network errors with timeout context
err := errors.NetworkError(originalErr, url, timeout)
```

### Performance Timing

```go
start := time.Now()
// ... do work ...
if err != nil {
    return errors.New(err).
        Component("datastore").
        Category(errors.CategoryDatabase).
        Timing("save_detection", time.Since(start)).
        Build()
}
```

## Sentry Integration

### Error Titles

The system generates meaningful Sentry titles based on:
1. Component name (e.g., "Imageprovider")
2. Category (e.g., "Image Fetch Error")
3. Operation (e.g., "Parse Pages From Response")

Result: "Imageprovider Image Fetch Error Parse Pages From Response"

### Error Levels

Errors are automatically assigned appropriate Sentry levels:
- `CategoryValidation`, `CategoryDatabase`: Error level
- `CategoryNetwork`, `CategoryFileIO`: Warning level
- `CategoryModelInit`, `CategoryModelLoad`: Error level (critical)

### Context Data

All context data is automatically added to Sentry with privacy scrubbing applied.

## Migration from Standard Errors

### Before
```go
return fmt.Errorf("failed to save detection: %w", err)
```

### After
```go
return errors.New(err).
    Component("datastore").
    Category(errors.CategoryDatabase).
    Context("operation", "save_detection").
    Build()
```

## Testing

The enhanced error system preserves standard error interfaces:

```go
// Standard error checking still works
if errors.Is(err, ErrNotFound) { ... }

// Enhanced error checking
var enhancedErr *errors.EnhancedError
if errors.As(err, &enhancedErr) {
    component := enhancedErr.GetComponent()
    category := enhancedErr.GetCategory()
}
```

## Privacy and Security

- **Automatic Scrubbing**: File paths, URLs, and other sensitive data are automatically scrubbed
- **Context Filtering**: Only safe context data is included in telemetry
- **No Secrets**: Never include passwords, API keys, or other secrets in context data
- **Anonymization**: Personal identifiers are removed or anonymized

## Troubleshooting

### Poor Sentry Titles

If you see generic titles like `*errors.errorString` or `*errors.SentryError`:
1. Ensure you're using the enhanced error system (`errors.New().Build()`)
2. Always specify `Component()` and `Category()`
3. Add descriptive `Context("operation", "...")` data
4. The system now automatically generates titles like "Imageprovider Image Fetch Error Parse Pages From Response"

### Incorrect Component Attribution

If errors are being attributed to the wrong component (e.g., birdweather errors tagged as imageprovider):
1. **Check component registration**: Ensure your component is registered in the `init()` function
2. **Use explicit component setting**: Always use `.Component("your-component")` instead of relying on auto-detection
3. **Debug auto-detection**: The system searches the call stack when no component is set, which can pick up the wrong component if:
   - Your component isn't registered
   - The error passes through code from another registered component
   - Shared handlers process errors from multiple components

Example of the issue:
```go
// If "birdweather" is not registered, and this error passes through
// code that contains "imageprovider", it will be incorrectly tagged
err := errors.New(originalErr).
    Component("birdweather").  // This won't work if not registered!
    Category(errors.CategoryNetwork).
    Build()
```

Solution:
```go
// 1. Add to init() in errors.go:
RegisterComponent("birdweather", "birdweather")

// 2. Always set component explicitly:
err := errors.New(originalErr).
    Component("birdweather").
    Category(errors.CategoryNetwork).
    Build()
```

### Generic Error Messages

If your error messages are too generic (like "key not found"):
1. Create descriptive error messages that specify what was expected
2. Use the pattern: check for specific error types and provide context
3. Add `expected_path` context for API parsing errors
4. Include `error_detail` context with the original error

```go
// Good example
if err.Error() == "key not found" {
    descriptiveMessage = "Wikipedia API response missing expected 'query.pages' structure"
} else {
    descriptiveMessage = fmt.Sprintf("failed to parse Wikipedia API response: %s", err.Error())
}
```

### Missing Context in Sentry

1. Verify telemetry is enabled and configured
2. Check that context data is not being filtered as sensitive
3. Ensure error is being built with `.Build()` method
4. Check that the component and category are being set correctly

### Performance Impact

The enhanced error system is designed to be lightweight:
- Context data is only processed when errors occur
- Telemetry reporting is asynchronous
- Privacy scrubbing uses efficient regex patterns
- Automatic component detection uses call stack inspection minimally

## Import Best Practices

### Standard Library Integration

This package provides all necessary error handling functions as passthrough methods, so you should **never** import the standard `errors` package alongside it:

```go
// ❌ WRONG - Creates import conflicts and confusion
import (
    stderrors "errors"  // Don't alias the standard package
    "github.com/tphakala/birdnet-go/internal/errors"
)

// ✅ CORRECT - Use only the custom errors package
import "github.com/tphakala/birdnet-go/internal/errors"

// Available passthrough functions:
errors.Is(err, target)     // Standard error checking
errors.As(err, &target)    // Standard error unwrapping
errors.Unwrap(err)         // Standard error unwrapping
errors.Join(errs...)       // Standard error joining
```

### Function Availability

The custom errors package provides:
- **Enhanced Functions**: `errors.New()`, `errors.Newf()` with telemetry integration
- **Standard Functions**: `errors.Is()`, `errors.As()`, `errors.Unwrap()`, `errors.Join()`
- **Specialized Functions**: Component detection, context building, privacy scrubbing

### Migration Checklist

When updating existing code:
1. ✅ Remove any `import "errors"` or `import stderrors "errors"`
2. ✅ Ensure `import "github.com/tphakala/birdnet-go/internal/errors"` is present
3. ✅ Replace `fmt.Errorf()` with `errors.Newf()` where enhanced telemetry is needed
4. ✅ Add `.Component()`, `.Category()`, and `.Context()` calls
5. ✅ End with `.Build()` to create the enhanced error

This approach ensures consistent error handling throughout the codebase while maintaining compatibility with standard Go error interfaces.