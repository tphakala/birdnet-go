# BirdWeather Package

The `birdweather` package provides functionality for integrating with the BirdWeather API service. This package handles uploading audio files and detection data to the BirdWeather platform for bird detection and soundscape sharing.

## Overview

BirdWeather is a service that allows sharing of bird detections and environmental audio data. This package enables automated submission of audio recordings and bird species detections to the BirdWeather platform.

## Features

- **Audio Processing**: Encode PCM audio data to FLAC using the native go-flac encoder (no FFmpeg dependency)
- **Loudness Normalization**: FLAC uploads are normalized to EBU R128 loudness with true-peak limiting via the native audionorm library
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

BirdWeather's soundscape API is FLAC-only. The package encodes PCM to FLAC with
the native go-flac encoder, entirely in Go:

```go
// Native FLAC encoding with audionorm EBU R128 loudness normalization.
func (b *BwClient) encodeWithNativeFLAC(pcmData []byte, timestamp string) (*audioEncodingResult, error)
```

The encoder writes directly to an in-memory buffer with no temporary file and no
FFmpeg dependency. go-flac declares `STREAMINFO.total_samples` up front from the
PCM length and verifies it against the samples written, so BirdWeather derives the
correct soundscape duration without a seekable sink. This makes FLAC uploads work
even on hosts where FFmpeg is not installed.

### Loudness Normalization

The package normalizes every FLAC upload with the native audionorm library
(EBU R128), applied as a single linear gain during encoding:

- **Target Loudness**: -23 LUFS (EBU R128 integrated-loudness target)
- **True Peak Ceiling**: -1 dBTP - prevents inter-sample clipping in different playback environments
- **Gain Limit**: clamped to +/- `audionorm.DefaultMaxGainDB`; silent or very short (<400 ms) clips are left unchanged (0 dB) rather than boosted into noise

The normalization makes bird vocalizations easier to hear across different devices and platforms while preserving audio quality through the lossless FLAC format.

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

func initBirdWeather(settings *conf.Settings) (*birdweather.BwClient, error) {
    // settings should contain:
    // - Realtime.Birdweather.ID: "your-station-id"
    // - Realtime.Birdweather.LocationAccuracy: 1000 (meters)
    // - BirdNET.Latitude: 60.1234
    // - BirdNET.Longitude: 24.5678

    client, err := birdweather.New(settings)
    if err != nil {
        return nil, fmt.Errorf("failed to create BirdWeather client: %w", err)
    }
    return client, nil
}

// Remember to call client.Close() when done
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

This package is designed to work across Linux, macOS, and Windows platforms. The Go code itself handles OS differences:

- **No FFmpeg dependency**: FLAC encoding and loudness normalization are pure Go (go-flac + audionorm), so BirdWeather uploads work whether or not FFmpeg is installed.
- Network connectivity differences between platforms are handled internally.
- Proper error handling for platform-specific network issues is implemented.
- Fallback DNS resolution for network problems is used.

## Error Handling

The package implements robust error handling, including:

- Network error detection and classification
- DNS resolution failure handling
- Request timeout management
- API response validation
- Rate limiting for testing functions
- Encode failures return a categorized error for the caller to retry; silent or very short clips are encoded without gain rather than failing

## Resource Management

Client connections are properly managed to prevent resource leaks:

- HTTP client timeouts to prevent hanging connections
- Proper cleanup of resources in the `Close()` method
- Idle connection cleanup
- **In-memory FLAC encoding**: the native go-flac encoder writes the upload buffer directly in memory, so there are no temporary files or directories to create or clean up

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

- **Audio Files**: Saves the processed FLAC upload file
  - Stored in `debug/birdweather/flac/` directory
  - Filename format: `bw_debug_*.flac`
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
│   └── flac/                     # Processed FLAC upload files
│       ├── bw_debug_*.flac       # FLAC audio files
│       └── bw_debug_*.txt        # Metadata files
```

## Loudness Normalization Technical Details

Bird call audio is normalized with the native audionorm library (EBU R128) toward:

- **Integrated Loudness (I)**: -23 LUFS - EBU R128 integrated-loudness target
- **True Peak (TP)**: -1 dBTP - prevents inter-sample clipping during playback

The normalization process involves:

1.  **Measurement**: audionorm decodes the 16-bit PCM in memory and measures the clip's integrated loudness and true peak.
2.  **Gain planning**: a single linear gain toward the target is computed under the true-peak ceiling, then clamped to +/- `audionorm.DefaultMaxGainDB`. Silent or sub-400 ms input yields 0 dB, leaving the clip unchanged.
3.  **Encoding**: the gain is applied in Go while the PCM is streamed into the go-flac encoder, producing the in-memory FLAC upload buffer.

Benefits:

- Applies a single linear gain, avoiding the "pumping" artifacts of dynamic-range compression.
- Adds true-peak limiting that the previous fixed-gain FFmpeg path lacked.
- Makes quiet bird calls more audible without clipping loud ones.
- Maintains audio quality through lossless compression.
- Runs entirely in memory with no FFmpeg process or temporary files.

## Testing

Comprehensive tests are provided for all key functionality:

- Audio conversion tests
- API connectivity tests
- Mock server tests for API interactions
- Error handling tests
- Loudness normalization validation

## Limitations

- Audio data is expected to be 16-bit PCM at 48kHz sample rate
- Location accuracy is limited to 4 decimal places
- Network connectivity is required for all API operations
- **Location coordinates** are currently ignored by BirdWeather's API. Despite the location randomization feature, BirdWeather always uses the coordinates assigned to your station ID/token rather than the coordinates submitted with detections.
- **Memory Usage**: FLAC encoding and loudness normalization run entirely in memory (no FFmpeg process, no temporary files), declaring `STREAMINFO.total_samples` up front from the PCM length.

## Dependencies

- Standard library packages (`net/http`, `encoding/binary`, etc.)
- Internal `datastore` package for detection data structures
- Internal `conf` package for configuration settings
- Internal `audiocore/flac` and `audiocore/audionorm` packages for native FLAC encoding and loudness normalization

## Thread Safety

The package is designed with concurrent usage in mind:

- Rate limiting uses mutex locks
- HTTP client is safe for concurrent use
- Configuration data is not modified after initialization
