# Internal/Errors Package Guidelines

## Critical Rules

### Import ONLY This Package

```go
// ✅ CORRECT
import "github.com/tphakala/birdnet-go/internal/errors"

// ❌ DO NOT import standard errors, unless it is to avoid circular dependency
import "errors"  // FORBIDDEN
```

## Component Registration

### Required: Register in init() (errors.go:334-348)

```go
func init() {
    RegisterComponent("packagename", "display-name")
}
```

### Current Components

- birdnet, myaudio, httpcontroller, datastore
- imageprovider, diskmanager, mqtt, weather
- conf, telemetry, birdweather, backup, audiocore

⚠️ **Unregistered components cause incorrect telemetry tagging**

## Quick Patterns

### Basic Error

```go
err := errors.New(originalErr).
    Component("datastore").
    Category(errors.CategoryDatabase).
    Context("operation", "save_detection").
    Build()
```

### Descriptive Error (Preferred)

```go
err := errors.Newf("Wikipedia API missing 'query.pages' structure").
    Component("imageprovider").
    Category(errors.CategoryImageFetch).
    Context("operation", "parse_pages_from_response").
    Context("expected_path", "query.pages").
    Build()
```

### Validation Error

```go
err := errors.Newf("scientific name cannot be empty").
    Component("imageprovider").
    Category(errors.CategoryValidation).
    Build()
```

## Categories (errors.go:23-50)

- **Critical**: CategoryModelInit, CategoryModelLoad, CategoryConfiguration
- **Data**: CategoryDatabase, CategoryFileIO, CategoryFileParsing
- **Network**: CategoryNetwork, CategoryHTTP, CategoryRTSP
- **Service**: CategoryImageFetch, CategoryMQTTConnection, CategoryWeather
- **System**: CategoryDiskUsage, CategorySystem, CategoryProcessing

## Troubleshooting

### Wrong Component in Telemetry?

1. Check component is registered in init()
2. Always use `.Component()` explicitly
3. See README.md:436-465

### Generic Sentry Titles?

- Add `Context("operation", "specific_action")`
- Use descriptive messages with `errors.Newf()`
- See README.md:427-434

### Performance Concerns?

- Event bus handles async (2.5ns overhead when disabled)
- See README.md:569-574

## Full Documentation

For comprehensive details: `internal/errors/README.md`

- Import guidelines: lines 16-32
- Context best practices: lines 163-191
- Component registration: lines 195-265
- Common patterns: lines 283-362
