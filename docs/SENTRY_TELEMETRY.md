# Sentry Telemetry Integration

BirdNET-Go includes optional Sentry integration for error tracking and telemetry. This feature is **disabled by default** and must be explicitly enabled by the user, respecting EU privacy laws and user consent.

## Privacy-First Design

The Sentry integration is designed with privacy in mind:

- **Opt-in only**: Telemetry is disabled by default
- **No personal data**: User information, device details, and system information are filtered out
- **Minimal data collection**: Only essential error information is sent
- **GDPR compliant**: Requires explicit user consent

## Configuration

To enable Sentry telemetry, add the following to your `config.yaml`:

```yaml
sentry:
  enabled: true          # Must be explicitly set to true
  dsn: "YOUR_SENTRY_DSN" # Get this from your Sentry project
  samplerate: 1.0        # Error sampling rate (0.0 to 1.0)
  debug: false           # Enable Sentry debug logging
```

### Configuration Options

- `enabled`: Must be set to `true` to enable telemetry (default: `false`)
- `dsn`: Your Sentry Data Source Name (required when enabled)
- `samplerate`: Percentage of errors to capture (1.0 = 100%, 0.1 = 10%)
- `debug`: Enable debug logging for Sentry operations

## Getting Started

1. Create a Sentry account at [sentry.io](https://sentry.io)
2. Create a new project for BirdNET-Go
3. Copy the DSN from your project settings
4. Update your `config.yaml` with the DSN
5. Set `enabled: true` to opt-in to telemetry

## What Data is Collected

When enabled, only the following information is sent:

- **Error messages**: The actual error that occurred
- **Error types**: The type of error (e.g., database error, file not found)
- **Component names**: Which part of the application had the error
- **Stack traces**: Disabled by default for privacy

### What is NOT Collected

- User IP addresses
- Device information
- Operating system details
- File paths or personal data
- Audio recordings or bird detection data
- Location information

## Using Sentry in Development

For developers adding new features, use the provided helper functions:

```go
import "github.com/tphakala/birdnet-go/internal/telemetry"

// Capture an error
if err != nil {
    telemetry.CaptureError(err, "component-name")
}

// Capture a message
telemetry.CaptureMessage("Important event", sentry.LevelInfo, "component-name")
```

## Compliance

This implementation complies with:

- GDPR (General Data Protection Regulation)
- CCPA (California Consumer Privacy Act)
- Other privacy regulations requiring explicit consent

Users maintain full control over their data and can disable telemetry at any time by setting `enabled: false` in the configuration.