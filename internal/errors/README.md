# Enhanced Error Handling with Telemetry Integration

This package provides centralized error handling with automatic telemetry integration for improved observability and debugging.

## Overview

The enhanced error system automatically reports errors to Sentry with privacy-safe context data and meaningful error titles. This helps with:

- **Better Error Categorization**: Errors are categorized by type (validation, network, database, etc.)
- **Component Identification**: Automatic detection of which component generated the error
- **Privacy-Safe Telemetry**: Sensitive data is scrubbed before being sent to telemetry
- **Meaningful Sentry Titles**: Instead of generic `*errors.errorString`, Sentry receives descriptive titles

## Quick Start

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

Components should match your package structure:

- `datastore`: Database operations
- `imageprovider`: Image fetching and caching
- `weather`: Weather data operations
- `suncalc`: Astronomical calculations
- `birdnet`: AI model operations
- `myaudio`: Audio processing
- `http-controller`: HTTP API operations

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

If you see generic titles like `*errors.errorString`:
1. Ensure you're using the enhanced error system (`errors.New().Build()`)
2. Always specify `Component()` and `Category()`
3. Add descriptive `Context("operation", "...")` data

### Missing Context in Sentry

1. Verify telemetry is enabled and configured
2. Check that context data is not being filtered as sensitive
3. Ensure error is being built with `.Build()` method

### Performance Impact

The enhanced error system is designed to be lightweight:
- Context data is only processed when errors occur
- Telemetry reporting is asynchronous
- Privacy scrubbing uses efficient regex patterns
- Automatic component detection uses call stack inspection minimally