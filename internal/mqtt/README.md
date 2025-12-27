# MQTT Package

## Overview

The `mqtt` package provides a robust MQTT client implementation for BirdNET-Go, enabling real-time streaming of bird detection data to MQTT brokers. This package is designed with reliability, observability, and proper error handling as core principles.

## Package Purpose

This package serves as the MQTT integration layer for BirdNET-Go, allowing:

- Real-time publishing of bird detection events
- Integration with home automation systems (Home Assistant, Node-RED, etc.)
- Reliable message delivery with automatic reconnection
- Comprehensive metrics and observability
- Connection testing and diagnostics

## Architecture

### Core Components

1. **Client Interface** (`mqtt.go`):
   - Defines the public API contract for MQTT operations
   - Provides configuration structures and defaults
   - Manages package-level logging with dynamic log levels

2. **Client Implementation** (`client.go`):
   - Implements the Client interface using Eclipse Paho MQTT client
   - Handles connection management with cooldown periods
   - Provides thread-safe publish operations
   - Implements automatic reconnection with exponential backoff
   - Integrates with the observability system for metrics

3. **Testing Utilities** (`testing.go`):
   - Provides comprehensive connection testing functionality
   - Supports multi-stage testing (DNS, TCP, MQTT, Publishing)
   - Includes test mode with artificial delays and failures
   - Implements proper timeout handling for each test stage

4. **Test Suite** (`client_test.go`):
   - Comprehensive unit and integration tests
   - Tests basic functionality, error scenarios, and edge cases
   - Validates metrics collection and reconnection behavior
   - Supports both local and remote MQTT brokers

## Dependencies

### External Dependencies

- `github.com/eclipse/paho.mqtt.golang`: MQTT client library
- `github.com/prometheus/client_golang`: Metrics collection

### Internal Dependencies

- `internal/conf`: Configuration management
- `internal/errors`: Enhanced error handling system
- `internal/logging`: Centralized logging infrastructure
- `internal/notification`: Integration failure notifications
- `internal/observability`: Metrics and telemetry
- `internal/datastore`: Data structures (for test messages)

## Key Features

### Connection Management

- **Automatic Reconnection**: Configurable reconnection with exponential backoff
- **Connection Cooldown**: Prevents rapid reconnection attempts
- **DNS Resolution**: Pre-flight DNS checks with proper error handling
- **Thread Safety**: All operations are protected by appropriate locking

### Error Handling

- **Enhanced Errors**: All errors include component, category, and context
- **Error Categories**:
  - `CategoryMQTTConnection`: Connection-related errors
  - `CategoryMQTTPublish`: Publishing errors
  - `CategoryNetwork`: Network and DNS errors
  - `CategoryConfiguration`: Configuration issues
  - `CategoryValidation`: Data validation errors

### Observability

- **Metrics Collected**:
  - Connection status (gauge)
  - Messages delivered (counter)
  - Message size (histogram)
  - Publish latency (histogram with timer)
  - Errors by category (counter with labels)
  - Reconnection attempts (counter)

### Logging

- **Centralized Logger**: Uses `internal/logger` package with module path `mqtt`
- **Dynamic Log Levels**: Can be changed at runtime
- **Debug Mode**: Detailed logging for troubleshooting
- **Structured Logging**: Type-safe field constructors (`logger.String()`, `logger.Error()`, etc.)

## Configuration

### Settings Structure

```go
type Config struct {
    Broker            string        // MQTT broker URL (e.g., "tcp://localhost:1883")
    Debug             bool          // Enable debug logging
    ClientID          string        // MQTT client ID
    Username          string        // Authentication username
    Password          string        // Authentication password
    Topic             string        // Default topic for publishing
    Retain            bool          // Retain messages at broker
    ReconnectCooldown time.Duration // Minimum time between reconnection attempts
    ReconnectDelay    time.Duration // Initial reconnection delay
    ConnectTimeout    time.Duration // Connection timeout
    PublishTimeout    time.Duration // Publish operation timeout
    DisconnectTimeout time.Duration // Graceful disconnect timeout
}
```

### Default Configuration

- ReconnectCooldown: 5 seconds
- ReconnectDelay: 1 second
- ConnectTimeout: 30 seconds
- PublishTimeout: 10 seconds
- DisconnectTimeout: 250 milliseconds
- QoS Level: 1 (at least once delivery)

## Usage Examples

### Basic Usage

```go
import "github.com/tphakala/birdnet-go/internal/logger"

func setupMQTT(settings *conf.Settings, metrics *observability.Metrics) error {
    log := logger.Global().Module("mqtt")

    // Create client
    client, err := mqtt.NewClient(settings, metrics)
    if err != nil {
        return fmt.Errorf("failed to create MQTT client: %w", err)
    }

    // Connect
    ctx := context.Background()
    if err := client.Connect(ctx); err != nil {
        log.Warn("connection failed", logger.Error(err))
        return err
    }

    // Publish message
    if err := client.Publish(ctx, "birdnet/detections", jsonPayload); err != nil {
        log.Error("publish failed", logger.Error(err))
        return err
    }

    // Disconnect when done
    defer client.Disconnect()
    return nil
}
```

### Connection Testing

```go
// Run comprehensive connection test
log := logger.Global().Module("mqtt")
resultChan := make(chan mqtt.TestResult, 10)
go client.TestConnection(ctx, resultChan)

for result := range resultChan {
    if result.Success {
        log.Info("test stage completed",
            logger.String("stage", result.Stage),
            logger.String("message", result.Message))
    } else {
        log.Error("test stage failed",
            logger.String("stage", result.Stage),
            logger.String("error", result.Error))
    }
}
```

### Secure Connection Examples

#### Basic TLS Connection

```yaml
realtime:
  mqtt:
    enabled: true
    broker: "tls://mqtt.example.com:8883" # or ssl:// or mqtts://
    username: "your-username"
    password: "your-password"
```

#### Self-Signed Certificate

```yaml
realtime:
  mqtt:
    broker: "tls://mqtt.local:8883"
    tls:
      insecureSkipVerify: true # Skip certificate verification
```

#### Custom CA Certificate

```yaml
realtime:
  mqtt:
    broker: "tls://mqtt.company.com:8883"
    tls:
      caCert: "/path/to/ca-cert.pem"
```

#### Mutual TLS (mTLS)

```yaml
realtime:
  mqtt:
    broker: "tls://mqtt.secure.com:8883"
    tls:
      caCert: "/path/to/ca-cert.pem"
      clientCert: "/path/to/client-cert.pem"
      clientKey: "/path/to/client-key.pem"
```

## Testing

### Test Coverage

- Basic functionality tests
- Error handling scenarios
- Reconnection behavior
- Metrics collection validation
- Context cancellation handling
- Timeout scenarios
- DNS resolution edge cases

### Running Tests

```bash
# Run all tests
go test ./internal/mqtt/...

# Run with local MQTT broker
MQTT_TEST_BROKER=tcp://localhost:1883 go test ./internal/mqtt/...

# Run with verbose output
go test -v ./internal/mqtt/...
```

### Test Environment

Tests support multiple brokers:

1. Local broker (preferred): `localhost:1883`
2. Public test broker: `test.mosquitto.org:1883`
3. Custom broker via `MQTT_TEST_BROKER` environment variable

## Security Considerations

### Authentication

- **Username/Password**: Basic authentication support
- **Anonymous Connections**: Can connect without credentials if broker allows

### TLS/SSL Support

- **Automatic Detection**: TLS is enabled automatically for `ssl://`, `tls://`, `mqtts://` schemes
- **Port 8883**: Standard MQTTS port with TLS encryption
- **Certificate Verification**:
  - Validates server certificates by default
  - `InsecureSkipVerify` option for self-signed certificates (use with caution)
- **Custom CA Certificates**: Support for custom Certificate Authority certificates
- **Mutual TLS**: Client certificate authentication support
- **Certificate Paths**: Configure paths to:
  - CA certificate (`CACert`)
  - Client certificate (`ClientCert`)
  - Client private key (`ClientKey`)

### TLS Certificate Management

BirdNET-Go provides a secure certificate management system:

- **UI Certificate Entry**: Paste PEM-encoded certificates directly in the web interface
- **Secure Storage**: Certificates are saved to `config/tls/mqtt/` with proper permissions
- **File Permissions**: Private keys (0600), certificates (0644)
- **Automatic Path Management**: The system handles file paths internally

### Security Best Practices

1. **Always use TLS** for production deployments
2. **Verify certificates** unless using self-signed certs in trusted networks
3. **Protect private keys** with appropriate file permissions
4. **Passwords are never logged** for security
5. **Input validation** on all configuration parameters
6. **Certificate Storage**: Certificates are stored as files, not in the config YAML

## Best Practices for Developers and LLMs

### For Developers

1. Always use context for cancellation support
2. Check `IsConnected()` before publishing
3. Handle errors appropriately with logging
4. Monitor metrics for operational insights
5. Use the test utilities for debugging connection issues

### For LLMs

1. **Error Handling**: Always use the enhanced error system from `internal/errors`
2. **Logging**: Use the package-level `GetLogger()` for consistency (returns cached `logger.Logger`)
3. **Thread Safety**: Protect shared state with appropriate locking
4. **Metrics**: Update relevant metrics for all operations
5. **Context**: Respect context cancellation in all blocking operations
6. **Testing**: Include comprehensive tests for new features

## Common Issues and Troubleshooting

### Connection Failures

- Check DNS resolution using the test utilities
- Verify broker address format (e.g., `tcp://host:port`)
- Ensure network connectivity to the broker
- Check authentication credentials if required

### Publishing Failures

- Verify client is connected before publishing
- Check topic format and permissions
- Monitor publish timeout settings
- Review broker logs for additional context

### Debugging

1. Enable debug mode: `client.SetDebug(true)`
2. Check `logs/mqtt.log` for detailed information
3. Use the connection test feature for diagnostics
4. Monitor metrics for patterns

## User Interface Integration

### Settings UI

The MQTT integration settings are managed through the web interface.

- **API Handler**: `internal/api/v2/settings.go`

### UI Features

1. **Configuration Fields**:
   - Enable/Disable toggle for MQTT integration
   - Broker URL input with scheme support:
     - Standard: `mqtt://localhost:1883`
     - Secure: `mqtts://`, `ssl://`, `tls://` (auto-enables TLS)
   - Topic configuration for publishing detections
   - Authentication options:
     - Anonymous connection toggle
     - Username/Password fields (hidden when anonymous is enabled)
   - TLS/SSL Security settings:
     - Auto-enabled for secure URL schemes
     - Skip certificate verification option (for self-signed certs)
     - CA Certificate textarea (PEM format)
     - Client Certificate textarea (for mTLS)
     - Client Private Key textarea (for mTLS)
   - Message settings:
     - Retain flag toggle with Home Assistant-specific guidance

2. **Connection Testing**:
   - Multi-stage connection test button
   - Test stages displayed in real-time:
     - Service Check
     - Service Start
     - DNS Resolution
     - TCP Connection
     - MQTT Connection
     - Message Publishing
   - Visual feedback for each stage (success/failure)
   - Detailed error messages for troubleshooting

3. **Frontend State Management**:
   - Uses Alpine.js for reactive UI updates
   - Watches for setting changes to enable save button
   - Clears test results when settings change
   - Handles anonymous authentication toggle logic

### Backend Integration

1. **Settings Handler** (`handlers/settings.go`):
   - **SaveSettings**: Processes form data and updates MQTT configuration
   - **TLS Certificate Processing**: `processTLSCertificates` saves certificates securely before form processing
   - **Anonymous Connection**: Special handling in `updateSettingsFromForm`
   - **Configuration Changes**: Detected by `mqttSettingsChanged`
   - **Control Channel**: Sends `reconfigure_mqtt` command when settings change

2. **API Endpoints**:
   - `POST /api/v1/settings/save`: Save MQTT settings
   - `POST /api/v1/mqtt/test`: Test MQTT connection (returns streaming results)

3. **Settings Persistence**:
   - Settings are saved to YAML configuration file
   - Changes trigger MQTT service reconfiguration without restart
   - Password fields are handled securely (never logged)

### Home Assistant Integration Notes

The UI includes specific guidance for Home Assistant users:

- Recommends enabling the retain flag for MQTT messages
- Explains that retained messages allow Home Assistant to retrieve last known sensor states after restart
- Compares behavior to platforms like Zigbee2MQTT

## Future Enhancements

Potential improvements for consideration:

- Message queue for offline publishing
- Multiple broker support for failover
- Message compression options
- Advanced topic management
- WebSocket transport support
- Certificate-based authentication
- QoS level configuration in UI
- Topic templates with variable substitution
- MQTT discovery for Home Assistant
