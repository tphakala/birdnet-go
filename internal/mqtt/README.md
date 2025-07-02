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
- **Dedicated Log File**: `logs/mqtt.log`
- **Dynamic Log Levels**: Can be changed at runtime
- **Debug Mode**: Detailed logging for troubleshooting
- **Structured Logging**: Uses slog for consistent formatting

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
// Create client
client, err := mqtt.NewClient(settings, metrics)
if err != nil {
    log.Fatal(err)
}

// Connect
ctx := context.Background()
if err := client.Connect(ctx); err != nil {
    log.Printf("Connection failed: %v", err)
}

// Publish message
if err := client.Publish(ctx, "birdnet/detections", jsonPayload); err != nil {
    log.Printf("Publish failed: %v", err)
}

// Disconnect when done
defer client.Disconnect()
```

### Connection Testing
```go
// Run comprehensive connection test
resultChan := make(chan mqtt.TestResult, 10)
go client.TestConnection(ctx, resultChan)

for result := range resultChan {
    log.Printf("%s: %s", result.Stage, result.Message)
    if !result.Success {
        log.Printf("Error: %s", result.Error)
    }
}
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

1. **Authentication**: Supports username/password authentication
2. **TLS Support**: Broker URLs can use `ssl://` or `tls://` schemes
3. **Password Handling**: Passwords are never logged
4. **Input Validation**: All inputs are validated before use

## Best Practices for Developers and LLMs

### For Developers
1. Always use context for cancellation support
2. Check `IsConnected()` before publishing
3. Handle errors appropriately with logging
4. Monitor metrics for operational insights
5. Use the test utilities for debugging connection issues

### For LLMs
1. **Error Handling**: Always use the enhanced error system from `internal/errors`
2. **Logging**: Use the package-level `mqttLogger` for consistency
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
The MQTT integration settings are managed through a web interface located at:
- **Template**: `views/pages/settings/integrationSettings.html` (lines 106-287)
- **Handler**: `internal/httpcontroller/handlers/settings.go`

### UI Features

1. **Configuration Fields**:
   - Enable/Disable toggle for MQTT integration
   - Broker URL input (e.g., `mqtt://localhost:1883`)
   - Topic configuration for publishing detections
   - Authentication options:
     - Anonymous connection toggle
     - Username/Password fields (hidden when anonymous is enabled)
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
   - **Anonymous Connection**: Special handling in `updateSettingsFromForm` (lines 246-253)
   - **Configuration Changes**: Detected by `mqttSettingsChanged` (lines 738-745)
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