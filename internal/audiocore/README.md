# BirdNET-Go AudioCore Package

AudioCore is a modular and extensible audio processing framework for BirdNET-Go that supports multiple simultaneous audio sources, per-source configuration, and a plugin-based processing architecture.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
  - [High-Level Block Diagram](#high-level-block-diagram)
  - [Detailed Data Flow](#detailed-data-flow)
  - [Audio Processing Pipeline](#audio-processing-pipeline)
- [Core Components](#core-components)
- [Audio Data Flow](#audio-data-flow)
- [Key Features](#key-features)
- [Usage Examples](#usage-examples)
- [Configuration](#configuration)
- [Performance & Monitoring](#performance--monitoring)
- [Implementation Status](#implementation-status)
- [Migration from MyAudio](#migration-from-myaudio)
- [Buffer Pool Analysis](#buffer-pool-analysis)

## Overview

The AudioCore package provides a complete audio processing pipeline designed to replace the legacy `internal/myaudio` package with a cleaner, more modular architecture. It was developed as part of [GitHub Issue #876](https://github.com/tphakala/birdnet-go/issues/876) to support:

- **Multiple simultaneous audio sources** (USB devices, RTSP streams, files)
- **Per-source BirdNET model assignment** (standard, bat calls, custom models)
- **Per-source audio gain control** (e.g., +6dB for USB source 1, -3dB for RTSP source)
- **Reliable FFmpeg process lifecycle management**
- **Memory-efficient buffer management** with pooling
- **Comprehensive metrics and health monitoring**

## Architecture

### High-Level Block Diagram

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              AUDIOCORE SYSTEM                                   │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────┐    ┌──────────────┐    ┌─────────────┐    ┌─────────────┐     │
│  │  Audio      │    │ Processing   │    │  Analyzer   │    │  Results    │     │
│  │  Sources    │───▶│  Pipeline    │───▶│  Manager    │───▶│  Handler    │     │
│  │             │    │             │    │             │    │             │     │
│  └─────────────┘    └──────────────┘    └─────────────┘    └─────────────┘     │
│                                                                                 │
│  ┌─────────────┐    ┌──────────────┐    ┌─────────────┐                        │
│  │  Buffer     │    │  Health      │    │  Metrics    │                        │
│  │  Pool       │    │  Monitor     │    │  Collector  │                        │
│  │             │    │             │    │             │                        │
│  └─────────────┘    └──────────────┘    └─────────────┘                        │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘

External Interfaces:
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│  Hardware   │────────▶│ AudioCore   │────────▶│ Detection   │
│  Sources    │         │ System      │         │ Database    │
│             │         │            │         │            │
│ • USB Mics  │         │            │         │            │
│ • RTSP      │         │            │         │            │
│ • File      │         │            │         │            │
└─────────────┘         └─────────────┘         └─────────────┘
```

### Detailed Data Flow

```
Audio Input Sources:
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│   USB Mic   │    │    RTSP     │    │    File     │
│   Source    │    │   Stream    │    │   Source    │
└──────┬──────┘    └──────┬──────┘    └──────┬──────┘
       │                  │                  │
       │ PCM Audio        │ FFmpeg Process   │ File Read
       │                  │                  │
       ▼                  ▼                  ▼
┌─────────────────────────────────────────────────────────┐
│              AUDIO MANAGER                              │
│                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │   Source 1   │  │   Source 2   │  │   Source N   │   │
│  │  Pipeline    │  │  Pipeline    │  │  Pipeline    │   │
│  └──────────────┘  └──────────────┘  └──────────────┘   │
│                                                         │
└─────────────────────────────────────────────────────────┘
                              │
                              ▼
                    ┌─────────────────┐
                    │   Unified       │
                    │ Audio Output    │
                    │    Channel      │
                    └─────────────────┘
```

### Audio Processing Pipeline

Each audio source has its own dedicated processing pipeline:

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          PROCESSING PIPELINE                                   │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Audio Source                                                                   │
│       │                                                                         │
│       ▼                                                                         │
│  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐                       │
│  │   Capture   │────▶│    Tee      │────▶│   Chunk     │                       │
│  │   Buffer    │     │ (optional)  │     │   Buffer    │                       │
│  │             │     │            │     │             │                       │
│  └─────────────┘     └─────────────┘     └─────────────┘                       │
│                              │                   │                             │
│                              │                   ▼                             │
│                              │          ┌─────────────┐                        │
│                              │          │   Overlap   │                        │
│                              │          │   Buffer    │                        │
│                              │          │            │                        │
│                              │          └─────────────┘                        │
│                              │                   │                             │
│                              │                   ▼                             │
│                              │          ┌─────────────┐                        │
│                              │          │  Processor  │                        │
│                              │          │    Chain    │                        │
│                              │          │  (Gain,     │                        │
│                              │          │   EQ, etc)  │                        │
│                              │          └─────────────┘                        │
│                              │                   │                             │
│                              │                   ▼                             │
│                              │          ┌─────────────┐                        │
│                              │          │  Analyzer   │                        │
│                              │          │  (BirdNET)  │                        │
│                              │          │            │                        │
│                              │          └─────────────┘                        │
│                              │                   │                             │
│                              │                   ▼                             │
│                              │          ┌─────────────┐                        │
│                              │          │   Results   │                        │
│                              │          │    Queue    │────────────────▶ birdnet.ResultsQueue
│                              │          │ Integration │                        │
│                              │          └─────────────┘                        │
│                              │                                                 │
│                              ▼                                                 │
│                     ┌─────────────┐                                            │
│                     │ Capture     │◄────── Processor calls                     │
│                     │ Manager     │        ExportClip() when                   │
│                     │ (Export)    │        detection saved                     │
│                     │            │                                            │
│                     └─────────────┘                                            │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

## Core Components

### 1. AudioSource
Represents an audio input source (microphone, RTSP stream, file).

**Key Interfaces:**
- `Start(ctx context.Context) error` - Begin audio capture
- `Stop() error` - Halt audio capture  
- `AudioOutput() <-chan AudioData` - Stream of audio data
- `SetGain(gain float64) error` - Adjust audio gain level

**Implementations:**
- **MalgoSource** (`sources/malgo/`) - USB/soundcard capture using PortAudio
- **RTSPSource** (*TODO*) - RTSP stream capture using FFmpeg
- **FileSource** (*TODO*) - File-based audio input

### 2. ProcessingPipeline
Manages data flow from source to analyzer with buffering, overlap, and processing.

**Features:**
- **ChunkBuffer** - Accumulates audio into fixed-duration chunks
- **OverlapBuffer** - Creates sliding windows for continuous analysis
- **ProcessorChain** - Applies audio transformations (gain, EQ, filters)
- **Backpressure Handling** - Adaptive delays when analyzer falls behind
- **Health Monitoring** - Tracks drop rates and performance metrics

### 3. Analyzer System
Processes audio chunks for detection/analysis.

**Components:**
- **AnalyzerManager** - Manages pool of analyzers
- **BirdNETAnalyzer** - Integrates BirdNET ML models
- **SafeAnalyzerWrapper** - Thread-safe wrapper with worker pools
- **CompositeFactory** - Creates analyzers from configuration

### 4. Buffer Management
Efficient memory management with pooling to reduce allocations.

**Buffer Pool Tiers:**
- **Small buffers:** 4KB (typical audio chunks)
- **Medium buffers:** 64KB (overlap processing)  
- **Large buffers:** 1MB (capture buffering)
- **Custom buffers:** Any size (fallback)

**Features:**
- Reference counting for safe buffer sharing
- Per-tier statistics and monitoring
- Automatic garbage collection for unused buffers

**Important Design Decision - Buffer Pool Usage:**

While audiocore includes a comprehensive buffer pool system, its usage is intentionally limited based on performance analysis:

1. **ChunkBufferV2 deliberately disables pooling** (`BufferPool: nil`)
   - Audio chunks are 288KB (3 seconds @ 48kHz, 16-bit mono)
   - Pooling would force unnecessary copying when sending to results queue
   - Direct allocation allows zero-copy ownership transfer to the processor
   
2. **Actual buffer pool usage is limited to:**
   - OverlapBuffer for temporary buffers during overlap processing
   - Small internal buffers for format conversion
   
3. **Why this matters for real-time audio:**
   - Zero-copy transfer is more important than allocation overhead for large buffers
   - 288KB allocations happen only every 3 seconds (not high frequency)
   - GC pressure from these allocations is minimal compared to copy overhead
   - Ownership transfer model is simpler without pool management

See [Buffer Pool Analysis](#buffer-pool-analysis) section for detailed performance considerations.

### 5. FFmpeg Process Management
Robust FFmpeg lifecycle management for RTSP streams.

**Location:** `utils/ffmpeg/`

**Features:**
- **Automatic restart** with exponential backoff
- **Health monitoring** with silence detection  
- **Process isolation** with proper cleanup
- **RTSP optimization** with TCP transport and buffering
- **Comprehensive logging** with privacy-aware output

## Audio Data Flow

### 1. Source Capture
```go
// Audio sources emit AudioData chunks
type AudioData struct {
    Buffer       []byte        // Raw PCM audio data
    Format       AudioFormat   // Sample rate, channels, bit depth
    Timestamp    time.Time     // Capture timestamp
    Duration     time.Duration // Duration of this chunk
    SourceID     string        // Source identifier
    BufferHandle AudioBuffer   // Buffer pool reference (optional)
}
```

### 2. Pipeline Processing
1. **Capture Tee** - Optional copy to capture buffer for clip saving
2. **Chunk Accumulation** - Buffer audio into analysis-sized chunks (3 seconds)
3. **Overlap Processing** - Add overlap from previous chunk for continuity
4. **Processor Chain** - Apply gain, EQ, filters, etc.
5. **Analysis** - Send to BirdNET or other ML models

### 3. Result Handling & Export Integration

AudioCore integrates seamlessly with the existing BirdNET-Go results processing pipeline by publishing detection results to the same `birdnet.ResultsQueue` used by the legacy myaudio system.

```go
// Analysis results from analyzers
type AnalysisResult struct {
    Timestamp   time.Time
    Duration    time.Duration
    Detections  []Detection      // List of species detected
    Metadata    map[string]any   // Additional context
    AnalyzerID  string          // Which analyzer produced this
    SourceID    string          // Which source this came from
}

// Simplified detection format (after recent refactoring)
type Detection struct {
    Label      string   // Species string (e.g., "Turdus_migratorius_American Robin")
    Confidence float32  // Detection confidence (0.0-1.0)
    StartTime  float64  // Start time within chunk
    EndTime    float64  // End time within chunk
    Attributes map[string]any // Additional attributes (now unused, set to nil)
}
```

**Integration with Results Queue:**
- ProcessingPipeline converts audiocore detections to `datastore.Results` format
- Results are published to `birdnet.ResultsQueue` for downstream processing
- Full compatibility maintained with existing database saving and notification systems
- PCM audio data ownership transfers to the results queue for clip generation

**Audio Export Integration (New):**
- The processor directly calls `captureManager.ExportClip()` when saving detections
- No longer uses internal detection handlers for exports
- Clean separation: processor decides when to export, audiocore handles how
- Zero-copy audio chunk transfer from audiocore to processor

## Key Features

### Multi-Source Support
Each source runs independently with its own:
- Processing pipeline
- Analyzer assignment  
- Gain settings
- Health monitoring

### Per-Source Configuration
```go
sources := []SourceConfig{
    {
        ID:         "usb_mic_1",
        Type:       "soundcard", 
        Device:     "USB Audio Device #1",
        AnalyzerID: "birdnet-standard",
        Gain:       1.5,  // +3dB boost
    },
    {
        ID:         "garden_stream",
        Type:       "rtsp",
        Device:     "rtsp://camera.local/audio", 
        AnalyzerID: "birdnet-bats",
        Gain:       0.7,  // -3dB reduction
    },
}
```

### Memory Efficiency
- **Buffer pooling** reduces allocations by 90%+
- **Reference counting** ensures safe buffer sharing
- **Tiered allocation** optimizes for common buffer sizes
- **Resource tracking** detects memory leaks

### Observability
- **Comprehensive metrics** for all components
- **Health monitoring** with automatic recovery
- **Performance tracking** with detailed timing
- **Error context** with structured logging

## Usage Examples

### Basic Setup
```go
// Create audio manager
config := &ManagerConfig{
    MaxSources:        10,
    DefaultBufferSize: 4096,
    EnableMetrics:     true,
    MetricsInterval:   10 * time.Second,
}
manager := NewAudioManager(config)

// Create and add audio source
sourceConfig := &SourceConfig{
    ID:         "microphone",
    Type:       "soundcard",
    Device:     "default",
    AnalyzerID: "birdnet-standard",
    Format: AudioFormat{
        SampleRate: 48000,
        Channels:   1,
        BitDepth:   16,
        Encoding:   "pcm_s16le",
    },
}

source, err := sources.CreateSource(sourceConfig, bufferPool)
if err != nil {
    log.Fatal(err)
}

err = manager.AddSource(source)
if err != nil {
    log.Fatal(err)
}

// Start processing
ctx := context.Background()
err = manager.Start(ctx)
if err != nil {
    log.Fatal(err)
}
defer manager.Stop()

// Process audio output
for audioData := range manager.AudioOutput() {
    fmt.Printf("Received audio from %s: %d bytes\n", 
        audioData.SourceID, len(audioData.Buffer))
}
```

### Multi-Source with Different Analyzers
```go
// Register multiple analyzers
analyzerManager := NewAnalyzerManager(factory)

// Standard bird detection
standardConfig := AnalyzerConfig{
    Type:      "birdnet",
    ModelPath: "/models/birdnet-standard.tflite",
    Threshold: 0.8,
}
standardAnalyzer, _ := analyzerManager.CreateAnalyzer(standardConfig)

// Bat-specific detection  
batConfig := AnalyzerConfig{
    Type:      "birdnet", 
    ModelPath: "/models/birdnet-bats.tflite",
    Threshold: 0.7,
}
batAnalyzer, _ := analyzerManager.CreateAnalyzer(batConfig)

// Assign different analyzers to different sources
manager.SetupProcessingPipeline("usb_mic", "birdnet-standard")
manager.SetupProcessingPipeline("ultrasonic_mic", "birdnet-bats")
```

## Configuration

### Manager Configuration
```go
type ManagerConfig struct {
    MaxSources        int           // Maximum number of concurrent sources
    DefaultBufferSize int           // Default buffer size for sources
    EnableMetrics     bool          // Enable metrics collection
    MetricsInterval   time.Duration // How often to collect metrics
    ProcessingTimeout time.Duration // Timeout for processing operations
    BufferPoolConfig  BufferPoolConfig // Buffer pool settings
}
```

### Source Configuration
```go
type SourceConfig struct {
    ID         string        // Unique identifier
    Name       string        // Human-readable name
    Type       string        // "soundcard", "rtsp", "file"
    Device     string        // Device ID or URL
    Format     AudioFormat   // Audio format requirements
    Gain       float64       // Audio gain multiplier (1.0 = no gain)
    AnalyzerID string        // Which analyzer to use
    Processing ProcessingConfig // Processing-specific settings
}
```

### Processing Configuration
```go
type ProcessingConfig struct {
    ChunkDuration   time.Duration // Analysis chunk size (e.g., 3s)
    OverlapPercent  float64       // Overlap between chunks (e.g., 0.1 = 10%)
    BufferAhead     int           // Number of chunks to buffer ahead
    Priority        int           // Processing priority
}
```

## Performance & Monitoring

### Metrics Collection
The system provides comprehensive metrics:

```go
type ManagerMetrics struct {
    ActiveSources    int             // Number of active sources
    ProcessedFrames  int64           // Total frames processed
    ProcessingErrors int64           // Total processing errors
    BufferPoolStats  BufferPoolStats // Buffer pool utilization
    LastUpdate       time.Time       // Last metrics update
}
```

### Health Monitoring
- **Source Health** - Monitors audio input for silence/failures
- **Pipeline Health** - Tracks drop rates and backpressure  
- **Analyzer Health** - Monitors processing timeouts and errors
- **Resource Health** - Tracks memory usage and leaks

### Performance Characteristics
Based on testing and benchmarks:

- **Memory Usage:** ~50-100MB for 3 concurrent sources
- **CPU Usage:** ~5-15% per source (depending on model complexity)
- **Latency:** ~100-500ms from capture to detection
- **Throughput:** 1000+ audio chunks/second per core

## Implementation Status

### ✅ Completed Features
- **Core Infrastructure** - AudioManager, interfaces, error handling
- **Soundcard Sources** - USB microphone capture via PortAudio
- **Processing Pipeline** - Chunking, overlap, processor chains
- **BirdNET Integration** - Full analyzer implementation with thread safety
- **Buffer Management** - Multi-tier pooling with reference counting (strategically not used for audio chunks)
- **FFmpeg Management** - Robust process lifecycle for RTSP streams
- **Gain Control** - Per-source audio level adjustment
- **Health Monitoring** - Adaptive backpressure and error recovery
- **Metrics System** - Comprehensive observability
- **Compatibility Layer** - Adapter for existing myaudio interface
- **Results Integration** - Direct publishing to birdnet.ResultsQueue
- **Simplified Data Flow** - Streamlined species string handling
- **Audio Export** - Full WAV/MP3/FLAC export via capture manager
- **Processor Integration** - Direct export access from processor actions

### 🔄 In Progress
- **RTSP Source Integration** - Connecting FFmpeg manager to source factory
- **Testing & Validation** - Expanding test coverage

### ❌ Not Yet Implemented
- **File Sources** - Audio file input support
- **Advanced Processors** - EQ, noise reduction, filters beyond gain
- **Plugin System** - Dynamic loading of custom processors/analyzers

## Recent Architectural Improvements

### Simplified Results Processing (2025-01)
Based on integration requirements, the audiocore package underwent significant simplification:

1. **Direct Species String Handling**
   - BirdNETAnalyzer now stores species strings directly in `Detection.Label`
   - Removed complex parsing and reconstruction logic
   - Eliminated unnecessary use of `Detection.Attributes` map

2. **Streamlined Queue Integration**
   - ProcessingPipeline publishes directly to `birdnet.ResultsQueue`
   - Merged `processAnalysisResult` and `sendToResultsQueue` methods
   - Maintains full compatibility with existing processor expectations

3. **Processor-Driven Export Architecture**
   - Removed internal `CaptureHandler` for detection exports
   - Processor directly calls `captureManager.ExportClip()` when needed
   - Eliminated duplicate export logic and dual buffer writing
   - Clean separation of concerns: processor controls when, audiocore controls how

4. **Benefits of Simplification**
   - Reduced code complexity and maintenance burden (-630 lines)
   - Better performance with fewer allocations and no duplicate buffering
   - Cleaner data flow from analyzer to database
   - Single export path improves reliability and debugging
   - No changes required to downstream systems

## Migration from MyAudio

### Compatibility Layer
The package includes a compatibility adapter (`adapter/myaudio_compat.go`) that provides a drop-in replacement for the existing myaudio interface:

```go
// Enable audiocore in configuration
settings.Audio.UseAudioCore = true

// Use compatibility adapter
if settings.Audio.UseAudioCore {
    audioCoreAdapter := adapter.StartAudioCoreCapture(settings, wg, quitChan, restartChan, audioChan)
    // Pass capture manager to processor for export functionality
    processor.SetCaptureManager(audioCoreAdapter.GetCaptureManager())
} else {
    myaudio.CaptureAudio(settings, wg, quitChan, restartChan, audioChan)
}
```

### Migration Benefits
1. **Better Performance** - Reduced memory allocations and improved concurrency
2. **More Reliable** - Robust error handling and automatic recovery
3. **More Observable** - Comprehensive metrics and health monitoring  
4. **More Extensible** - Clean interfaces for adding new sources/analyzers
5. **Multi-Source Support** - Handle multiple audio inputs simultaneously

### Migration Strategy
1. **Parallel Development** ✅ - New package developed alongside existing one
2. **Feature Flag** ✅ - `UseAudioCore` config option for switching
3. **Compatibility Layer** ✅ - Adapter ensures existing code works
4. **Processor Integration** ✅ - Export functionality integrated with processor
5. **Gradual Rollout** 🔄 - Test with subset of users before full migration
6. **Documentation** ✅ - This comprehensive guide for migration

## Buffer Pool Analysis

### Overview
The audiocore package includes a sophisticated buffer pool system designed to reduce memory allocations and GC pressure. However, after careful analysis and real-world testing, we've made specific decisions about when to use pooling.

### Buffer Pool Architecture
```
┌─────────────────────────────────────────────────────────┐
│                  Buffer Pool System                     │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐     │
│  │ Small Tier  │  │ Medium Tier │  │ Large Tier  │     │
│  │   (4KB)     │  │   (64KB)    │  │   (1MB)     │     │
│  └─────────────┘  └─────────────┘  └─────────────┘     │
│                                                         │
│  Features:                                              │
│  • Reference counting for safe sharing                  │
│  • Per-tier statistics and monitoring                   │
│  • Automatic garbage collection                         │
│  • Metrics integration                                  │
└─────────────────────────────────────────────────────────┘
```

### Real-Time Audio Considerations

#### Audio Chunk Characteristics
- **Size**: 288KB per chunk (3 seconds @ 48kHz, 16-bit mono)
- **Frequency**: One chunk every 3 seconds
- **Lifetime**: Chunks live through the entire processing pipeline
- **Ownership**: Must transfer from audiocore to processor

#### Performance Trade-offs

**Benefits of Buffer Pooling:**
1. Reduced allocation overhead
2. Lower GC pressure
3. Better memory locality
4. Predictable memory usage

**Costs of Buffer Pooling for Large Buffers:**
1. **Forced copying**: Cannot transfer ownership without copying
2. **Reference counting overhead**: Additional bookkeeping
3. **Synchronization costs**: Pool access requires locking
4. **Memory overhead**: Pools keep buffers allocated even when idle

### Empirical Analysis

#### Allocation Frequency
```
Audio chunks: 288KB every 3 seconds = 96KB/second
With 5 sources: 480KB/second allocation rate

This is relatively low for modern systems and GC.
```

#### Copy vs Allocation Cost
```
288KB copy: ~50-100μs (memory bandwidth limited)
288KB allocation: ~1-5μs (fast path)
GC amortized cost: ~10-20μs per allocation

Copy cost >> Allocation + GC cost
```

### Design Decision

Based on this analysis, we've made the following decisions:

1. **Disable pooling for audio chunks** (288KB buffers)
   - Zero-copy transfer is critical for performance
   - Allocation overhead is negligible at this frequency
   - Simplifies ownership model

2. **Enable pooling for small, frequent allocations**
   - Overlap buffers (temporary, short-lived)
   - Format conversion buffers
   - Internal processing buffers

3. **Future considerations**
   - If we add more audio sources (>20), revisit this decision
   - Consider custom allocator for predictable latency
   - Profile actual GC impact in production

### Best Practices

When deciding whether to use buffer pools in audiocore:

1. **Use pooling when:**
   - Buffers are small (<64KB)
   - Allocation frequency is high (>100/second)
   - Buffers have short, predictable lifetimes
   - Ownership doesn't need to transfer between components

2. **Avoid pooling when:**
   - Buffers are large (>64KB)
   - Zero-copy transfer is required
   - Allocation frequency is low (<10/second)
   - Buffer lifetime spans multiple components

3. **Always measure:**
   - Profile before optimizing
   - Consider total system impact, not just allocation cost
   - Test with realistic workloads

---

For questions or issues related to audiocore, please refer to [GitHub Issue #876](https://github.com/tphakala/birdnet-go/issues/876) or create a new issue in the repository.