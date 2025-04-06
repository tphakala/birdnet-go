# BirdWeather Package

The `birdweather` package provides functionality for integrating with the BirdWeather API service. This package handles uploading audio files and detection data to the BirdWeather platform for bird detection and soundscape sharing.

## Overview

BirdWeather is a service that allows sharing of bird detections and environmental audio data. This package enables automated submission of audio recordings and bird species detections to the BirdWeather platform.

## Features

- **Audio Processing**: Convert PCM audio data to WAV format
- **API Integration**: Connect to the BirdWeather API for data submission
- **Location Privacy**: Randomize location coordinates to protect precise location data
- **Error Handling**: Comprehensive network and API error handling
- **Connection Testing**: Diagnostic tools to test connectivity with the BirdWeather service
- **Debug Capabilities**: Comprehensive debugging tools for audio data capture and analysis

## Components

### Client Interface

The package provides a client interface for interacting with the BirdWeather API:

```go
type Interface interface {
    Publish(note *datastore.Note, pcmData []byte) error
    UploadSoundscape(timestamp string, pcmData []byte) (soundscapeID string, err error)
    PostDetection(soundscapeID, timestamp, commonName, scientificName string, confidence float64) error
    TestConnection(ctx context.Context, resultChan chan<- TestResult)
    Close()
}
```

### Client Implementation

`BwClient` is the main implementation that provides methods to interact with the BirdWeather API:

```go
type BwClient struct {
    Settings      *conf.Settings
    BirdweatherID string
    Accuracy      float64
    Latitude      float64
    Longitude     float64
    HTTPClient    *http.Client
}
```

### Audio Processing

The package includes functionality to convert PCM audio data to WAV format for upload:

```go
func encodePCMtoWAV(pcmData []byte) (*bytes.Buffer, error)
```

### Location Randomization

For privacy protection, the package can randomize location data:

```go
func (b *BwClient) RandomizeLocation(radiusMeters float64) (latitude, longitude float64)
```

> **Note**: Currently, BirdWeather ignores user-submitted coordinates for BirdNET-Pi/Go implementations. The service always uses coordinates assigned to the station's token/ID. The location randomization feature is implemented but unused at this time.

### Connection Testing

Comprehensive testing tools to verify connectivity with BirdWeather services:

```go
func (b *BwClient) TestConnection(ctx context.Context, resultChan chan<- TestResult)
```

## Usage Examples

### Initializing the Client

```go
import (
    "github.com/tphakala/birdnet-go/internal/conf"
    "github.com/tphakala/birdnet-go/internal/birdweather"
)

func main() {
    settings := &conf.Settings{
        Realtime: conf.Realtime{
            Birdweather: conf.Birdweather{
                ID:               "your-station-id",
                LocationAccuracy: 1000, // Accuracy in meters
            },
        },
        BirdNET: conf.BirdNET{
            Latitude:  60.1234,
            Longitude: 24.5678,
        },
    }
    
    client, err := birdweather.New(settings)
    if err != nil {
        log.Fatalf("Failed to create BirdWeather client: %v", err)
    }
    defer client.Close()
    
    // Use the client...
}
```

### Publishing a Detection

```go
import (
    "github.com/tphakala/birdnet-go/internal/datastore"
)

func publishDetection(client *birdweather.BwClient, pcmData []byte) error {
    note := &datastore.Note{
        Date:          "2023-04-10",
        Time:          "14:30:25",
        CommonName:    "Eurasian Blackbird",
        ScientificName: "Turdus merula",
        Confidence:    0.95,
    }
    
    return client.Publish(note, pcmData)
}
```

### Testing Connectivity

```go
func testConnection(client *birdweather.BwClient) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    resultChan := make(chan birdweather.TestResult)
    
    go client.TestConnection(ctx, resultChan)
    
    for result := range resultChan {
        if result.IsProgress {
            fmt.Printf("Progress: %s - %s\n", result.Stage, result.State)
        } else {
            fmt.Printf("Result: %s - Success: %v\n", result.Stage, result.Success)
            if !result.Success {
                fmt.Printf("Error: %s\n", result.Error)
            }
        }
    }
}
```

## Cross-Platform Compatibility

This package is designed to work across Linux, macOS, and Windows platforms. The package handles:

- Network connectivity differences between platforms
- Proper error handling for platform-specific network issues
- Fallback DNS resolution for network problems

## Error Handling

The package implements robust error handling, including:

- Network error detection and classification
- DNS resolution failure handling
- Request timeout management
- API response validation
- Rate limiting for testing functions

## Resource Management

Client connections are properly managed to prevent resource leaks:

- HTTP client timeouts to prevent hanging connections
- Proper cleanup of resources in the `Close()` method
- Idle connection cleanup

## Debug Capabilities

The package includes robust debugging features to help diagnose audio-related issues:

### Audio Data Debugging

When debug mode is enabled (`Settings.Realtime.Birdweather.Debug = true`), the following debug artifacts are saved:

- **Raw PCM Data**: Saves the original PCM audio data before any processing
  - Stored in `debug/birdweather/pcm/` directory
  - Filename format: `bw_pcm_debug_*.raw`
  - Includes detailed metadata in accompanying `.txt` file:
    - Timestamp information
    - Bird species details
    - Confidence score
    - Audio specifications (sample rate, bit depth, etc.)
    - Expected duration

- **WAV Audio Files**: Saves the processed WAV audio files
  - Stored in `debug/birdweather/wav/` directory
  - Filename format: `bw_debug_*.wav`
  - Includes metadata in accompanying `.txt` file:
    - Timestamp information
    - File sizes (total file, buffer, PCM data)
    - Audio duration and specifications

### Enabling Debug Mode

Enable debugging in your configuration:

```go
settings := &conf.Settings{
    Realtime: conf.Realtime{
        Birdweather: conf.Birdweather{
            ID:               "your-station-id",
            LocationAccuracy: 1000,
            Debug:            true,  // Enable debug mode
        },
        // ...
    },
    // ...
}
```

### Debug Directory Structure

```
debug/
├── birdweather/
│   ├── pcm/                      # Raw PCM audio data
│   │   ├── bw_pcm_debug_*.raw    # Raw PCM files
│   │   └── bw_pcm_debug_*.txt    # Metadata files
│   └── wav/                      # Processed WAV files
│       ├── bw_debug_*.wav        # WAV audio files
│       └── bw_debug_*.txt        # Metadata files
```

## Testing

Comprehensive tests are provided for all key functionality:

- Audio conversion tests
- API connectivity tests
- Mock server tests for API interactions
- Error handling tests

## Limitations

- Audio data is expected to be 16-bit PCM at 48kHz sample rate
- Location accuracy is limited to 4 decimal places
- Network connectivity is required for all API operations
- **Location coordinates** are currently ignored by BirdWeather's API. Despite the location randomization feature, BirdWeather always uses the coordinates assigned to your station ID/token rather than the coordinates submitted with detections.

## Dependencies

- Standard library packages (`net/http`, `encoding/binary`, etc.)
- Internal `datastore` package for detection data structures
- Internal `conf` package for configuration settings

## Thread Safety

The package is designed with concurrent usage in mind:
- Rate limiting uses mutex locks
- HTTP client is safe for concurrent use
- Configuration data is not modified after initialization 