# Privacy Package

The `internal/privacy` package provides privacy-focused utility functions for handling sensitive data in BirdNET-Go. This package consolidates privacy and data scrubbing functions to ensure sensitive information like credentials, IP addresses, and personal data are properly anonymized before being transmitted in telemetry or logs.

## Overview

This package implements **Privacy by Design** principles by providing:

- **URL Anonymization**: Convert URLs to anonymized forms while preserving debugging value
- **Message Scrubbing**: Remove or anonymize sensitive information from text messages
- **RTSP URL Sanitization**: Remove credentials and paths from RTSP URLs for display purposes
- **System ID Generation**: Create unique, privacy-safe system identifiers

## Core Functions

### Message and URL Privacy

#### `ScrubMessage(message string) string`

Removes or anonymizes sensitive information from telemetry messages by finding URLs and replacing them with anonymized versions.

```go
original := "Failed to connect to rtsp://admin:password@192.168.1.100:554/stream1"
scrubbed := privacy.ScrubMessage(original)
// Result: "Failed to connect to url-a1b2c3d4e5f6g7h8"
```

#### `AnonymizeURL(rawURL string) string`

Converts a URL to an anonymized form while preserving debugging value. Maintains URL structure but removes sensitive information like credentials, hostnames, and paths.

```go
url := "rtsp://admin:password@192.168.1.100:554/stream1"
anonymized := privacy.AnonymizeURL(url)
// Result: "url-a1b2c3d4e5f6g7h8" (consistent hash)
```

#### `SanitizeRTSPUrl(source string) string`

Removes sensitive information from RTSP URLs for display purposes. Strips credentials and path information while preserving the host and port for debugging.

```go
rtspURL := "rtsp://admin:password@192.168.1.100:554/stream1/channel1"
sanitized := privacy.SanitizeRTSPUrl(rtspURL)
// Result: "rtsp://192.168.1.100:554"
```

### System Identification

#### `GenerateSystemID() (string, error)`

Creates a unique system identifier that is URL-safe and case-insensitive. Format: `XXXX-XXXX-XXXX` (14 characters total with hyphens).

```go
id, err := privacy.GenerateSystemID()
if err != nil {
    // handle error
}
// Result: "A1B2-C3D4-E5F6"
```

#### `IsValidSystemID(id string) bool`

Validates that a system ID has the correct format.

```go
valid := privacy.IsValidSystemID("A1B2-C3D4-E5F6") // true
invalid := privacy.IsValidSystemID("invalid-id")   // false
```

## Privacy Protection Features

### URL Anonymization Process

1. **URL Parsing**: Parse the URL to extract components
2. **Host Categorization**: Categorize hosts while preserving useful information:
   - `localhost` for local connections
   - `private-ip` for internal networks
   - `public-ip` for internet addresses
   - `domain-com` for .com domains (TLD only)
3. **Consistent Hashing**: SHA-256 creates anonymous but consistent identifiers
4. **Structure Preservation**: Maintains debugging value without exposing sensitive data

### Host Categorization

The package categorizes hosts to preserve debugging information while protecting privacy:

- **Localhost**: `localhost`, `127.0.0.1`, `::1`
- **Private IPs**: RFC 1918 addresses (`10.x.x.x`, `192.168.x.x`, `172.16-31.x.x`)
- **Link-local**: `169.254.x.x`, `fe80::`
- **Public IPs**: Any other IP addresses
- **Domains**: Preserve only the TLD (e.g., `domain-com`, `domain-org`)

### Path Anonymization

Path segments are processed to maintain structure while removing sensitive information:

- **Common streams**: Recognized patterns like "stream", "live", "camera" are preserved as generic "stream"
- **Numeric segments**: Numbers are categorized as "numeric"
- **Other segments**: Hashed to maintain structure while hiding content

## Performance Characteristics

The package is optimized for performance with pre-compiled regex patterns to avoid the memory leak patterns identified in issue #825:

```go
// Pre-compiled patterns for better performance
var (
    urlPattern  = regexp.MustCompile(`\b(?:https?|rtsp|rtmp)://\S+`)
    ipv4Pattern = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)
)
```

### Benchmark Results

Typical performance characteristics on modern hardware:

- **ScrubMessage**: ~30,000 ns/op for complex messages with multiple URLs
- **AnonymizeURL**: ~2,100 ns/op for single URL anonymization
- **SanitizeRTSPUrl**: ~60 ns/op for RTSP URL sanitization (string operations)
- **GenerateSystemID**: ~1,300 ns/op including cryptographic random generation
- **IsValidSystemID**: ~112 ns/op for validation (zero allocations)

## Security Considerations

### Cryptographic Functions

- **System ID Generation**: Uses `crypto/rand` for cryptographically secure random number generation
- **URL Hashing**: Uses SHA-256 for consistent, secure hash generation
- **No Secrets in Output**: All functions ensure no sensitive data appears in output

### Consistent Anonymization

The same input will always produce the same anonymized output, allowing for:

- Error correlation across time
- Debugging with consistent identifiers
- Privacy protection without losing debugging capability

## Usage Examples

### Basic Message Scrubbing

```go
package main

import (
    "fmt"
    "github.com/tphakala/birdnet-go/internal/privacy"
)

func main() {
    // Scrub sensitive information from error messages
    errorMsg := "Connection failed to rtsp://admin:secret@192.168.1.100:554/stream1"
    cleanMsg := privacy.ScrubMessage(errorMsg)
    fmt.Println(cleanMsg) // "Connection failed to url-a1b2c3d4e5f6g7h8"

    // Generate system ID for telemetry
    systemID, err := privacy.GenerateSystemID()
    if err != nil {
        panic(err)
    }
    fmt.Println("System ID:", systemID) // "A1B2-C3D4-E5F6"
}
```

### RTSP URL Sanitization for Display

```go
func displayConnectionInfo(rtspURL string) {
    // Show sanitized URL to user (remove credentials but keep host for debugging)
    displayURL := privacy.SanitizeRTSPUrl(rtspURL)
    fmt.Printf("Connecting to: %s\n", displayURL)

    // Log anonymized version for telemetry
    anonymizedURL := privacy.AnonymizeURL(rtspURL)
    fmt.Printf("Telemetry ID: %s\n", anonymizedURL)
}
```

## Testing

The package includes comprehensive unit tests and benchmarks:

```bash
# Run all tests
go test ./internal/privacy -v

# Run benchmarks
go test ./internal/privacy -bench=. -benchmem

# Run tests with race detection
go test ./internal/privacy -race
```

## Integration with Other Packages

This package is designed to be imported by:

- **Telemetry Package**: For anonymizing error messages and URLs in Sentry reports
- **Logging Package**: For scrubbing sensitive data from log messages
- **Configuration Package**: For sanitizing URLs in configuration validation
- **Support Package**: For cleaning data in support bundles

### Migration from Existing Functions

This package consolidates privacy functions previously scattered across:

- `internal/telemetry/sentry.go`: `ScrubMessage()` and `anonymizeURL()`
- `internal/telemetry/systemid.go`: `GenerateSystemID()` and related functions
- `internal/conf/utils.go`: `SanitizeRTSPUrl()`

## Privacy Compliance

This package supports compliance with privacy regulations:

- **GDPR**: Implements data minimization and anonymization
- **CCPA**: Provides tools for protecting personal information
- **General Privacy**: Follows privacy-by-design principles

All functions are designed to remove or anonymize personal data while maintaining debugging capability and system functionality.
