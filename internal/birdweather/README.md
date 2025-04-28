# BirdWeather Package

The `birdweather` package provides functionality for integrating with the BirdWeather API service. This package handles uploading audio files and detection data to the BirdWeather platform for bird detection and soundscape sharing.

## Overview

BirdWeather is a service that allows sharing of bird detections and environmental audio data. This package enables automated submission of audio recordings and bird species detections to the BirdWeather platform.

## Features

- **Audio Processing**: Convert PCM audio data to FLAC (preferred) or WAV format
- **Format Selection**: Automatic selection between FLAC and WAV based on FFmpeg availability
- **Loudness Normalization**: FLAC audio uploads are normalized to standard streaming loudness targets
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

The package includes functionality to convert PCM audio data to FLAC or WAV format for upload:

```go
// FLAC encoding using FFmpeg (preferred) with loudness normalization
func encodeFlacUsingFFmpeg(pcmData []byte, settings *conf.Settings) (*bytes.Buffer, error)

// WAV encoding as fallback
func encodePCMtoWAV(pcmData []byte) (*bytes.Buffer, error)
```

### Loudness Normalization

When FFmpeg is available, the package applies loudness normalization to FLAC audio files to ensure consistent playback quality:

- **Target Loudness**: -14 LUFS (Loudness Units Full Scale) - optimal for web streaming
- **True Peak Maximum**: -1 dB - prevents clipping in different playback environments
- **Loudness Range**: 11 LU - balanced dynamic range

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

func main() {
    settings := &conf.Settings{
        Realtime: conf.Realtime{
            Birdweather: conf.Birdweather{
                ID:               "your-station-id",
                LocationAccuracy: 1000, // Accuracy in meters
            },
            Audio: conf.AudioSettings{
                FfmpegPath: "/path/to/ffmpeg", // Path to FFmpeg binary
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

This package is designed to work across Linux, macOS, and Windows platforms. The Go code itself handles OS differences, but relies on external dependencies:

- **FFmpeg Dependency**: The primary cross-platform requirement is the **installation and proper configuration of FFmpeg** on each target system. The path to the `ffmpeg` executable must be set correctly in the application configuration or be available in the system's `PATH`.
- Network connectivity differences between platforms are handled internally.
- Proper error handling for platform-specific network issues is implemented.
- Fallback DNS resolution for network problems is used.
- FFmpeg detection and usage for FLAC encoding with loudness normalization is performed.
- WAV fallback when FFmpeg is not available ensures basic functionality.

## Error Handling

The package implements robust error handling, including:

- Network error detection and classification
- DNS resolution failure handling
- Request timeout management
- API response validation
- Rate limiting for testing functions
- Graceful fallback to WAV if FLAC encoding fails
- Fallback to unnormalized FLAC if loudness normalization fails

## Resource Management

Client connections are properly managed to prevent resource leaks:

- HTTP client timeouts to prevent hanging connections
- Proper cleanup of resources in the `Close()` method
- Idle connection cleanup
- **In-Memory Processing**: Audio export via FFmpeg now occurs entirely in memory to reduce disk I/O, especially beneficial for systems using SD cards. However, be mindful that very large audio inputs could lead to increased memory consumption during this process.
- Temporary files and directories are properly cleaned up

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

- **Audio Files**: Saves the processed FLAC or WAV audio files
  - Stored in `debug/birdweather/flac/` or `debug/birdweather/wav/` directory
  - Filename format: `bw_debug_*.flac` or `bw_debug_*.wav`
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
│   ├── flac/                     # Processed FLAC files (when FFmpeg is available)
│   │   ├── bw_debug_*.flac       # FLAC audio files
│   │   └── bw_debug_*.txt        # Metadata files
│   └── wav/                      # Processed WAV files (when FFmpeg is not available)
│       ├── bw_debug_*.wav        # WAV audio files
│       └── bw_debug_*.txt        # Metadata files
```

## Loudness Normalization Technical Details

When FFmpeg is available, bird call audio is processed using the `loudnorm` filter with a **two-pass** method to achieve these targets:

- **Integrated Loudness (I)**: -23 LUFS - standard target for EBU R128 compliance
- **True Peak (TP)**: -1 dB - prevents clipping during playback
- **Loudness Range (LRA)**: 11 LU - maintains appropriate dynamic range

The normalization process involves:
1.  **Pass 1 (Analysis)**: Reads raw PCM audio data and runs `loudnorm` in analysis mode (`print_format=json`) to measure the input audio's characteristics (I, LRA, TP, Threshold).
2.  **Pass 2 (Normalization & Encoding)**: Reads the PCM data again, applies the `loudnorm` filter using the measured values from Pass 1 (`linear=true`, `measured_*` parameters), and encodes the normalized audio directly to FLAC format in a single step.

Benefits:
- Produces higher quality, more consistent normalization compared to single-pass, avoiding audible artifacts like "pumping".
- Improves listening experience across different devices.
- Makes quiet bird calls more audible without clipping loud ones.
- Maintains audio quality through lossless compression.
- Efficiently performs analysis on raw PCM data.
- Falls back to single-pass normalization if the analysis pass fails.

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
- FLAC encoding requires **FFmpeg to be installed and configured correctly** on the host system (Linux, macOS, Windows).
- Loudness normalization requires FFmpeg with the `loudnorm` filter (available in most modern FFmpeg builds).
- **Memory Usage**: The in-memory FFmpeg processing, while reducing disk writes, might consume significant memory for very long audio inputs.

## Dependencies

- Standard library packages (`net/http`, `encoding/binary`, etc.)
- Internal `datastore` package for detection data structures
- Internal `conf` package for configuration settings
- Internal `myaudio` package for FFmpeg integration

## Thread Safety

The package is designed with concurrent usage in mind:
- Rate limiting uses mutex locks
- HTTP client is safe for concurrent use
- Configuration data is not modified after initialization 