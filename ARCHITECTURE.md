# BirdNET-Go Architecture

This document provides a comprehensive overview of BirdNET-Go's architecture, tech stack, and design decisions.

## Table of Contents

- [BirdNET-Go Architecture](#birdnet-go-architecture)
  - [Table of Contents](#table-of-contents)
  - [System Overview](#system-overview)
    - [High-Level Architecture](#high-level-architecture)
  - [Backend Architecture](#backend-architecture)
    - [Core Technologies](#core-technologies)
    - [AI Model Integration](#ai-model-integration)
    - [Web Framework](#web-framework)
    - [Configuration Management](#configuration-management)
    - [Database Layer](#database-layer)
    - [Audio Processing Pipeline](#audio-processing-pipeline)
    - [Real-Time Processing](#real-time-processing)
    - [External Integrations](#external-integrations)
    - [Testing Framework](#testing-framework)
  - [Frontend Architecture](#frontend-architecture)
    - [UI Technology Stack](#ui-technology-stack)
    - [Legacy UI (Deprecated)](#legacy-ui-deprecated)
    - [Modern UI (Svelte 5)](#modern-ui-svelte-5)
    - [Real-Time Communication](#real-time-communication)
    - [State Management](#state-management)
    - [Testing Strategy](#testing-strategy)
  - [Build System](#build-system)
    - [Frontend Compilation](#frontend-compilation)
    - [Embedding in Go Binary](#embedding-in-go-binary)
    - [Cross-Platform Builds](#cross-platform-builds)
    - [Hot Reload Development](#hot-reload-development)
  - [Platform Support](#platform-support)
    - [Target Platforms](#target-platforms)
    - [Hardware Requirements](#hardware-requirements)
    - [Platform-Specific Features](#platform-specific-features)
  - [API Design](#api-design)
    - [API v1 (Deprecated)](#api-v1-deprecated)
    - [API v2 (Active Development)](#api-v2-active-development)
  - [Security Architecture](#security-architecture)
    - [Authentication](#authentication)
    - [Authorization](#authorization)
    - [Content Security Policy](#content-security-policy)
    - [Input Validation](#input-validation)
    - [Privacy by Design](#privacy-by-design)
  - [Performance Considerations](#performance-considerations)
    - [Memory Management](#memory-management)
    - [Concurrency](#concurrency)
    - [Caching](#caching)
    - [Frontend Performance](#frontend-performance)
  - [Development Tools](#development-tools)
    - [Code Quality](#code-quality)
    - [Pre-Commit Hooks](#pre-commit-hooks)
    - [Debugging](#debugging)
    - [Documentation](#documentation)
  - [Future Architecture Considerations](#future-architecture-considerations)
    - [Planned Improvements](#planned-improvements)
    - [Scalability](#scalability)
  - [Conclusion](#conclusion)

---

## System Overview

BirdNET-Go is a self-contained application for real-time bird sound identification using the BirdNET AI model. The architecture follows these key principles:

- **Single Binary Deployment**: Frontend assets are embedded into the Go binary
- **Privacy-First**: No data collection without explicit user opt-in
- **Cross-Platform**: Supports Linux, macOS, Windows on amd64 and arm64
- **Resource-Efficient**: Runs on devices from Raspberry Pi to desktop servers
- **Real-Time Processing**: Continuous audio analysis with immediate detection feedback

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        User Interface                        │
│  ┌──────────────────┐         ┌──────────────────────────┐ │
│  │  Legacy UI       │         │  Modern UI (Svelte 5)    │ │
│  │  HTMX + Alpine   │         │  TypeScript + Tailwind   │ │
│  │  (Deprecated)    │         │  (Active Development)    │ │
│  └──────────────────┘         └──────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼ (HTTP/SSE)
┌─────────────────────────────────────────────────────────────┐
│                     Echo Web Framework                       │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐ │
│  │  API v1      │  │  API v2      │  │  Static Assets   │ │
│  │ (Deprecated) │  │  (Active)    │  │  (Embedded)      │ │
│  └──────────────┘  └──────────────┘  └──────────────────┘ │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                     Application Core (Go)                    │
│  ┌───────────────────────────────────────────────────────┐ │
│  │  Real-Time Audio Processing Pipeline                  │ │
│  │  ┌──────────┐  ┌──────────┐  ┌────────┐  ┌─────────┐│ │
│  │  │ Capture  │→ │ Analyze  │→ │ Detect │→ │ Notify  ││ │
│  │  └──────────┘  └──────────┘  └────────┘  └─────────┘│ │
│  └───────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
                              │
                 ┌────────────┼────────────┐
                 ▼            ▼            ▼
┌──────────────────┐ ┌─────────────┐ ┌──────────────┐
│  BirdNET Model   │ │  Database   │ │ External     │
│  (TensorFlow     │ │  (GORM)     │ │ Services     │
│   Lite + CGO)    │ │  SQLite/    │ │ MQTT/Webhook │
│                  │ │  MySQL      │ │ BirdWeather  │
└──────────────────┘ └─────────────┘ └──────────────┘
```

---

## Backend Architecture

### Core Technologies

**Go 1.25+**

- Modern, statically-typed compiled language
- Excellent concurrency primitives (goroutines, channels)
- Cross-platform compilation support
- Fast compilation and execution
- Strong standard library for networking, HTTP, and I/O

**CGO (C Interoperability)**

- Used exclusively for interfacing with TensorFlow Lite C API
- Enables BirdNET AI model inference

**Key Dependencies:**

```go
// Web framework
github.com/labstack/echo/v4

// Configuration management
github.com/spf13/viper
github.com/spf13/cobra

// Database ORM
gorm.io/gorm
gorm.io/driver/sqlite
gorm.io/driver/mysql

// Testing
github.com/stretchr/testify

// Audio processing (external binaries)
// - FFmpeg (audio format conversion, RTSP streams)
// - SoX (spectrogram generation only)
```

### AI Model Integration

**Dual TensorFlow Lite Model Architecture**

BirdNET-Go uses **TWO** TensorFlow Lite models from the BirdNET Analyzer project:

1. **Analysis Model** - Species identification from audio
2. **Range Filter Model** - Geographic/regional filtering of species

Both models work together to provide accurate, location-aware bird identification.

**Package Structure:**

```
internal/birdnet/
├── birdnet.go              # Main BirdNET struct and initialization
├── analyze.go              # Audio analysis and species detection
├── range_filter.go         # Geographic range filtering
├── model_registry.go       # Model metadata and registry
├── models_embedded.go      # Embedded model data
├── models_external.go      # External model loading
├── label_files.go          # Label file management
├── taxonomy.go             # Taxonomy mapping
└── queue.go                # Analysis queue management
```

**TensorFlow Lite Integration via go-tflite:**

BirdNET-Go uses the `github.com/tphakala/go-tflite` library for TensorFlow Lite integration:

```go
// internal/birdnet/birdnet.go
import (
    tflite "github.com/tphakala/go-tflite"
    "github.com/tphakala/go-tflite/delegates/xnnpack"
)

type BirdNET struct {
    AnalysisInterpreter *tflite.Interpreter  // Species identification model
    RangeInterpreter    *tflite.Interpreter  // Geographic filtering model
    // ...
}
```

The `go-tflite` library handles CGO/C API bindings to TensorFlow Lite internally, providing a clean Go interface to the BirdNET package.

**Model Files:**

| Model Type             | Filename                                            | Precision | Purpose                                               |
| ---------------------- | --------------------------------------------------- | --------- | ----------------------------------------------------- |
| Analysis               | `BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite`          | FP32      | Species identification (default, embedded)            |
| Range Filter (Legacy)  | `BirdNET_GLOBAL_6K_V2.4_MData_Model_FP16.tflite`    | FP16      | Geographic filtering (embedded)                       |
| Range Filter (Updated) | `BirdNET_GLOBAL_6K_V2.4_MData_Model_V2_FP16.tflite` | FP16      | Geographic filtering (embedded)                       |
| Labels                 | `BirdNET_GLOBAL_6K_V2.4_Labels_<locale>.txt`        | N/A       | Species labels in multiple languages (6,000+ species) |

**Model Workflow:**

```
Audio PCM → Analysis Model → Species Predictions → Range Filter Model → Filtered Results
    ↓              ↓                    ↓                     ↓                 ↓
  48kHz        TFLite FP32         Confidence scores    Location-based      Final
  Mono         Inference           (all species)        probability         detections
```

**Performance Characteristics:**

- **Analysis Model Inference**: ~100-500ms per 3-second audio chunk (hardware dependent)
- **Range Filter**: Negligible overhead (<10ms)
- **Memory**: ~50-200MB total footprint (both models loaded)
- **Hardware Acceleration**: XNNPACK delegate for CPU optimization
- **Supported Platforms**: CPU inference (ARM64, AMD64)

### Web Framework

**Echo Framework (github.com/labstack/echo/v4)**

Echo was chosen for its:

- High performance and low memory footprint
- Simple, expressive API
- Built-in middleware support
- Excellent routing capabilities
- WebSocket and Server-Sent Events support

**Server Structure:**

```
internal/httpcontroller/
├── server.go           # Echo server initialization
├── handlers/           # HTTP request handlers
│   ├── dashboard.go    # Dashboard endpoints
│   ├── settings.go     # Settings management
│   ├── detections.go   # Detection history
│   └── sse.go          # Server-Sent Events
├── middleware/         # Custom middleware
│   ├── auth.go         # Authentication
│   ├── cors.go         # CORS handling
│   └── security.go     # Security headers
└── securefs/           # Embedded filesystem with security
```

**Middleware Stack:**

1. **Recovery**: Panic recovery and error handling
2. **Logger**: Request/response logging
3. **CORS**: Cross-origin resource sharing
4. **Security Headers**: CSP, X-Frame-Options, etc.
5. **Authentication**: JWT-based auth for protected routes
6. **Rate Limiting**: DoS protection

**Server-Sent Events (SSE):**

Real-time updates are delivered via SSE for efficiency:

```go
// Real-time detection stream
GET /api/v2/events/detections
→ text/event-stream

// System status updates
GET /api/v2/events/status
→ text/event-stream
```

### Configuration Management

**Viper + Cobra CLI**

Configuration is managed using Viper with Cobra for CLI commands:

```
cmd/
├── root.go             # Root command and global flags
├── realtime.go         # Real-time processing command
├── file.go             # File analysis command
├── config.go           # Configuration management
└── directory.go        # Directory analysis command
```

**Configuration Sources (Priority Order):**

1. Command-line flags
2. Environment variables
3. Configuration file (`config.yaml`)
4. Defaults

**Configuration File:**

```yaml
# config.yaml
main:
  name: BirdNET-Go
  timeAs24h: true
  log:
    level: info
    type: text

realtime:
  interval: 15
  processingtime: false
  audio:
    source: ""

webserver:
  enabled: true
  port: 8080
  autotls: false

database:
  driver: sqlite
  path: birdnet.db
```

**Settings Management:**

```
internal/conf/
├── config.go           # Configuration structures
├── settings.go         # Runtime settings management
├── validation.go       # Configuration validation
└── defaults.go         # Default values
```

### Database Layer

**GORM ORM (gorm.io/gorm)**

GORM provides a developer-friendly abstraction over database operations:

**Supported Databases:**

- **SQLite** (default): Embedded database, zero configuration
- **MySQL/MariaDB**: For use-cases where external database is needed

**Database Schema:**

```
internal/datastore/
├── datastore.go        # Database interface
├── models.go           # Data models
├── migrations.go       # Schema migrations
└── queries.go          # Common queries
```

**Core Models:**

```go
// Detection record
type Note struct {
    ID              uint      `gorm:"primaryKey"`
    Date            time.Time `gorm:"index"`
    Time            time.Time
    InputFile       string
    BeginTime       float64
    EndTime         float64
    ScientificName  string    `gorm:"index"`
    CommonName      string    `gorm:"index"`
    Confidence      float64   `gorm:"index"`
    Latitude        float64
    Longitude       float64
    Threshold       float64
    Verified        *bool
    Comment         string
}

// Settings storage
type Settings struct {
    Key         string `gorm:"primaryKey"`
    Value       string
    Description string
}
```

**Database Features:**

- **Automatic Migrations**: Schema updates on startup
- **Indexes**: Optimized queries for common searches
- **Connection Pooling**: Efficient connection management
- **Transactions**: ACID compliance for data integrity
- **Foreign Keys**: Referential integrity (MySQL)

**Database Access Pattern:**

```go
// Singleton pattern for database access
db := datastore.GetInstance()

// Query detections
detections, err := db.GetDetectionsBetweenDates(startDate, endDate)

// Create detection
err := db.Save(&detection)

// Statistics
stats, err := db.GetTopBirdsDetected(limit)
```

### Audio Processing Pipeline

**Multi-Source Audio Capture**

BirdNET-Go supports various audio input sources:

1. **Local Audio Devices** (ALSA, CoreAudio, WASAPI)
2. **RTSP Streams** (IP cameras, network audio sources)
3. **File Analysis** (WAV, MP3, FLAC, OGG, etc.)

**Audio Processing Architecture:**

```
internal/analysis/
├── realtime.go         # Real-time processing orchestrator and entry point
├── control_monitor.go  # Control signals and system restart handling
├── file.go             # File analysis mode implementation
├── directory.go        # Directory batch processing
├── buffer_manager.go   # Audio buffer management
├── processor/
│   ├── processor.go    # Main audio analysis processor
│   ├── workers.go      # Task queue management and job enqueueing
│   ├── actions.go      # Action types (Database, MQTT, BirdWeather, SSE, etc.)
│   ├── execute.go      # Action execution logic
│   ├── eventtracker.go # Event frequency tracking
│   └── jobqueue_adapter.go  # Adapter between processor and job queue
├── jobqueue/
│   ├── queue.go        # Job queue implementation with retry capabilities
│   ├── job.go          # Job lifecycle and state management
│   ├── types.go        # Job status, retry config, and interfaces
│   └── logger.go       # Job queue logging
└── species/
    └── species_tracker.go  # New species detection and tracking

internal/myaudio/
├── capture.go          # Audio capture from devices (via malgo)
├── encode.go           # Audio encoding (PCM to WAV)
├── ffmpeg_export.go    # FFmpeg-based audio export and conversion
├── ffmpeg_stream.go    # RTSP stream handling via FFmpeg
├── ffmpeg_manager.go   # FFmpeg process lifecycle management
├── resample.go         # Audio resampling (file analysis mode only)
├── buffer_pool.go      # Memory-efficient buffer pooling
├── analysis_buffer.go  # Analysis buffer management
├── capture_buffer.go   # Capture buffer management
└── source_registry.go  # Audio source registration and management
```

**Audio Device Interface (malgo/miniaudio):**

BirdNET-Go uses **malgo** (Go wrapper for miniaudio.h) for cross-platform audio device access:

- **Linux**: ALSA backend
- **macOS**: CoreAudio backend
- **Windows**: WASAPI backend

**Audio Format Requirements:**

- **Realtime Mode**: Expects 48kHz, 16-bit mono PCM from audio source
  - No explicit resampling in BirdNET-Go code
  - malgo/miniaudio may handle format conversion internally
- **File Analysis Mode**: Uses `resample.go` for format conversion to 48kHz mono

**Processing Pipeline:**

```
Audio Source → Capture → Buffer → Analyze → Detect → Store → Notify
     ↓           ↓         ↓        ↓         ↓       ↓       ↓
  RTSP/Mic    malgo/    3-sec    BirdNET  Threshold Database MQTT/
              FFmpeg    chunks   Analysis  + Range           Webhook
                                + Range   Filter
                                Filter
```

**FFmpeg Integration:**

FFmpeg is used for:

- **RTSP Stream Ingestion**: Capturing audio from IP cameras and network streams
- **Audio Format Conversion**: PCM to AAC, FLAC, Opus, and MP3 (at audio export/save stage)
- **Gain Control and Normalization**: EBU R128 loudnorm filter or simple volume adjustment
  - Normalization: `loudnorm` filter with configurable LUFS target
  - Gain adjustment: `volume` filter for dB boost/cut


**SoX Integration:**

SoX (Sound eXchange) is used exclusively for:

- Spectrogram generation (visualization)

**Spectrogram Generation:**

```
internal/spectrogram/
├── generator.go        # Spectrogram creation with SoX
├── prerenderer.go      # Pre-rendering logic and queue management
└── utils.go            # Utility functions for spectrogram operations
```

Spectrograms are generated on-demand or pre-rendered for dashboard display using SoX.

**Async Task Processing System:**

BirdNET-Go uses an asynchronous job queue system for handling detection actions (database saves, MQTT publishes, BirdWeather uploads, etc.):

```
internal/analysis/jobqueue/
├── queue.go        # JobQueue implementation with retry and backoff
├── job.go          # Job lifecycle management (pending → running → completed/failed)
├── types.go        # RetryConfig, JobStatus, Action interface
└── logger.go       # Structured logging for job execution
```

**Job Queue Features:**

- **Async Execution**: Actions execute in background goroutines, non-blocking
- **Retry with Exponential Backoff**: Configurable retry policies per action type
- **Graceful Degradation**: Failed jobs don't block new detections
- **Queue Limits**: Bounded queue prevents memory exhaustion (default: 1000 jobs)
- **Job Archival**: Completed/failed jobs archived for debugging (max 100)
- **Statistics Tracking**: Per-action success/failure rates

**Action Types** (internal/analysis/processor/actions.go):

| Action Type           | Purpose                                          | Retry | Timeout |
| --------------------- | ------------------------------------------------ | ----- | ------- |
| LogAction             | Write detection to log file                      | No    | N/A     |
| DatabaseAction        | Save detection to database + audio clip          | No    | 30s     |
| SaveAudioAction       | Export audio clip to disk (WAV/FLAC/MP3)         | No    | 30s     |
| BirdWeatherAction     | Upload detection to BirdWeather API              | Yes   | 30s     |
| MqttAction            | Publish detection to MQTT broker                 | Yes   | 10s     |
| SSEAction             | Broadcast detection via Server-Sent Events       | Yes   | 30s     |
| UpdateRangeFilterAction | Update BirdNET species filter daily            | No    | 30s     |
| CompositeAction       | Execute multiple actions sequentially            | N/A   | 30s/action |

**Task Processing Flow:**

```
Detection → ProcessDetection() → CreateActions() → EnqueueTask()
                                                      ↓
                                          JobQueue.Enqueue()
                                                      ↓
                                          [Async Execution]
                                                      ↓
                                   ┌─────────────────┴─────────────────┐
                                   ↓                                   ↓
                            Action.Execute()                    [Retry Logic]
                                   ↓                                   ↓
                            Success/Failure            Exponential Backoff
                                   ↓                                   ↓
                            Archive Job              Retry or Mark Failed
```

**Retry Configuration:**

Actions can specify retry behavior via `RetryConfig`:

```go
type RetryConfig struct {
    Enabled      bool          // Enable retries
    MaxRetries   int           // Max attempts (e.g., 3)
    InitialDelay time.Duration // First retry delay (e.g., 5s)
    MaxDelay     time.Duration // Cap on delay (e.g., 5min)
    Multiplier   float64       // Backoff multiplier (e.g., 2.0 = exponential)
}
```

**Example: BirdWeather Upload with Retry**

1. Detection arrives → BirdWeatherAction created with retry enabled
2. EnqueueTask() adds job to queue (non-blocking)
3. Job executes in background goroutine
4. If upload fails (network error): retry after 5s, then 10s, then 20s
5. If max retries exceeded: mark as failed, log error, archive job
6. Success: archive job with success status

**CompositeAction for Sequential Execution:**

Introduced to fix race condition between DatabaseAction and SSEAction (GitHub issue #1158):

```go
// SSEAction needs database ID assigned by DatabaseAction first
composite := &CompositeAction{
    Actions: []Action{databaseAction, sseAction},
    Description: "Save to database then broadcast",
    Timeout: 45 * time.Second,  // Override default timeout
}
```

**Buffer Management:**

Memory-efficient buffer pooling prevents allocation overhead:

```go
// Reusable buffer pools
var pcmBufferPool = sync.Pool{
    New: func() interface{} {
        buf := make([]float32, defaultBufferSize)
        return &buf
    },
}
```

### Real-Time Processing

**Entry Point: Realtime Command**

Real-time bird detection is initiated via the `realtime` command:

```bash
birdnet-go realtime [flags]
```

**Realtime Processor:**

```
internal/analysis/
├── realtime.go         # Real-time processing orchestrator and entry point
├── control_monitor.go  # Control signals and system restart handling
└── processor/
    ├── processor.go    # Main audio analysis processor
    ├── workers.go      # Task queue management and job enqueueing
    ├── actions.go      # Action types (Database, MQTT, BirdWeather, SSE, etc.)
    ├── execute.go      # Action execution logic
    └── jobqueue_adapter.go  # Adapter between processor and job queue
```

**Job Queue:**

```
internal/analysis/jobqueue/
├── queue.go        # JobQueue implementation with retry and backoff
├── job.go          # Job lifecycle management
├── types.go        # RetryConfig, JobStatus, Action interface
└── logger.go       # Job queue logging
```

**Processing Flow:**

1. **Initialization**
   - Load BirdNET TensorFlow Lite model
   - Initialize audio sources (devices/RTSP streams)
   - Initialize buffers:
     - Analysis buffer: 6x buffer size to avoid underruns
     - Capture buffer: 120 seconds for audio clip export
   - Initialize job queue for async task processing (max 1000 jobs)
   - Start web server (if enabled)
   - Connect to MQTT broker (if configured)
   - Initialize species tracker and event tracker

2. **Capture Loop** (continuous)
   - Capture audio continuously (48kHz, 16-bit PCM)
   - Write to both analysis buffer and capture buffer simultaneously
   - All audio analyzed - no Voice Activity Detection

3. **Analysis Loop** (continuous)
   - Buffer monitor reads 3-second audio chunks from analysis buffer
   - Chunks overlap by configurable amount (default: 0.0s, range: 0.0-2.9s)
   - Queue audio chunks to BirdNET analysis queue (default size: 5)
   - BirdNET predicts species using TensorFlow Lite model
   - Filter results by confidence threshold
   - Apply species filters (location, time-based, custom lists)
   - Apply privacy filter (if enabled):
     - BirdNET model detects "human" vocalizations
     - Not traditional VAD - uses BirdNET's species detection
     - Filters bird detections when human speech detected
     - Protects privacy by preventing audio clip export during conversations

4. **Detection Handling** (async via job queue)
   - Create actions based on configuration
   - Enqueue tasks to job queue (non-blocking)
   - Actions execute in background:
     - DatabaseAction: Save detection to database with audio clip
     - SSEAction: Broadcast detection via Server-Sent Events
     - MqttAction: Publish to MQTT broker (with retry)
     - BirdWeatherAction: Upload to BirdWeather API (with retry)
     - SaveAudioAction: Export audio clip to disk
   - Failed actions retry with exponential backoff

**Concurrency Model:**

BirdNET-Go uses a job queue system for concurrent action processing:

```go
// Initialize job queue with capacity and options
processor.JobQueue = jobqueue.NewJobQueueWithOptions(
    1000,  // maxJobs: queue capacity
    100,   // maxArchivedJobs: keep completed jobs for debugging
    false, // logAllSuccesses: only log retries by default
)

// Start background processing goroutine
processor.JobQueue.Start()

// Detection handling - non-blocking enqueue
for _, detection := range detections {
    task := &Task{
        Type:      TaskTypeAction,
        Detection: detection,
        Action:    action, // DatabaseAction, MqttAction, etc.
    }

    // Enqueue returns immediately, action executes asynchronously
    if err := processor.EnqueueTask(task); err != nil {
        // Handle queue full or stopped errors
        log.Printf("Failed to enqueue task: %v", err)
    }
}
```

**Job Queue Processing Loop:**

The job queue runs a background goroutine that:
1. Checks for jobs ready to execute (pending or retrying after backoff)
2. Executes actions in separate goroutines (concurrent execution)
3. Handles retry logic with exponential backoff on failure
4. Archives completed/failed jobs for debugging
5. Respects queue capacity limits to prevent memory exhaustion

**Graceful Shutdown:**

```go
// Signal handling for clean shutdown
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

<-sigChan
// Stop audio capture
// Flush pending detections
// Close database connections
// Shutdown web server
```

### External Integrations

**MQTT Publishing:**

```
internal/mqtt/
├── client.go           # MQTT client implementation
├── publisher.go        # Detection publishing
└── config.go           # Connection configuration
```

Publishes detections to configurable MQTT topics for home automation integration.

**Webhook Notifications:**

```
internal/notification/
├── webhook.go          # HTTP webhook delivery
├── queue.go            # Retry queue for failed deliveries
└── templates.go        # Customizable payload templates
```

**BirdWeather Integration:**

```
internal/birdweather/
├── birdweather_client.go  # BirdWeather API client and upload logic
├── audio.go               # Audio processing and normalization (LUFS)
└── testing.go             # Test helpers and mock client
```

**Features:**

- **Soundscape Upload**: Uploads 15-second audio clips to BirdWeather
- **Audio Normalization**: Uses FFmpeg loudnorm filter to achieve -23 LUFS (EBU R128 standard)
- **Detection Metadata**: Sends species, confidence, location, and timestamp
- **Error Handling**: Retry logic via job queue for network failures
- **Logging**: Dedicated file logger (`logs/birdweather.log`) for debugging uploads
- **API Compliance**: Follows BirdWeather API v2 specification

Optional integration with [BirdWeather.com](https://birdweather.com) for global bird activity tracking.

**eBird Integration:**

```
internal/ebird/
├── client.go           # eBird API v2 client
└── types.go            # Taxonomy data structures
```

Integration with Cornell Lab's eBird API for:

- Species taxonomy data (scientific names, common names, family classifications)
- Building species family trees for UI visualization (Kingdom → Phylum → Class → Order → Family → Genus → Species → Subspecies)
- Additional species metadata (banding codes, extinction status, taxonomic ordering)

**Note:** Regional species filtering is handled by BirdNET's range filter, not by the eBird module.

### Testing Framework

**Testify + Assert:**

Primary testing framework using `github.com/stretchr/testify`:

```go
func TestDetectionThreshold(t *testing.T) {
    // Arrange
    detector := NewDetector(0.8)

    // Act
    result := detector.Filter(0.9)

    // Assert
    assert.True(t, result)
}
```

**Test Organization:**

```
internal/package/
├── package.go          # Implementation
├── package_test.go     # Unit tests
└── testdata/           # Test fixtures
    ├── audio/          # Test audio files
    └── expected/       # Expected results
```

**Mock Framework:**

Using `testify/mock` for dependency mocking:

```go
type MockDatastore struct {
    mock.Mock
}

func (m *MockDatastore) Save(detection *Note) error {
    args := m.Called(detection)
    return args.Error(0)
}
```

**Test Coverage Goals:**

- Unit tests: >80% coverage for core logic
- Integration tests: API endpoints, database operations
- E2E tests: Frontend workflows (Playwright)

**Running Tests:**

```bash
# All Go tests
task test

# Specific package
go test -v ./internal/birdnet/...

# With coverage
go test -race -coverprofile=coverage.out ./...

# Race detection
go test -race ./...
```

---

## Frontend Architecture

### UI Technology Stack

BirdNET-Go has two UI implementations:

| Feature              | Legacy UI        | Modern UI             |
| -------------------- | ---------------- | --------------------- |
| **Status**           | Deprecated       | Active Development    |
| **Technology**       | HTMX + Alpine.js | Svelte 5 + TypeScript |
| **Styling**          | Tailwind CSS     | Tailwind CSS          |
| **Components**       | DaisyUI          | DaisyUI               |
| **State Management** | Alpine stores    | Svelte 5 Runes        |
| **Build Tool**       | None (CDN)       | Vite                  |
| **Testing**          | Manual           | Vitest + Playwright   |

### Legacy UI (Deprecated)

**⚠️ No new features should be added to the legacy UI.**

**Technologies:**

- **HTMX**: Dynamic HTML over the wire
- **Alpine.js**: Lightweight reactive framework
- **Tailwind CSS**: Utility-first CSS
- **DaisyUI**: Component library

**Structure:**

```
views/
├── dashboard.html      # Legacy dashboard
├── settings.html       # Legacy settings
└── partials/           # Reusable components
```

**Why Deprecated:**

- Limited interactivity for complex features
- Difficult to test
- Poor developer experience for modern features
- No type safety
- Growing maintenance burden

### Modern UI (Svelte 5)

**🚀 All new features must use the Svelte 5 UI.**

**Core Technologies:**

**Svelte 5** (NOT SvelteKit)

- Compiled, component-based framework
- Reactive by default using Runes system
- No virtual DOM (compiles to optimal vanilla JS)
- Excellent performance and small bundle size
- True reactive programming without manual subscriptions

**Why Svelte 5 (not SvelteKit)?**

- **No SSR needed**: Frontend is embedded as static assets
- **Simpler deployment**: Single binary includes everything
- **Smaller bundle**: No server-side framework overhead
- **Better integration**: Works seamlessly with Go backend

**TypeScript:**

- Strict type checking (`strict: true`)
- No `any` types allowed (enforced by linting)
- Comprehensive type definitions for all components
- Better IDE support and refactoring

**Tailwind CSS + DaisyUI:**

- Utility-first CSS framework
- DaisyUI provides pre-built component classes
- Custom theme configuration
- Dark mode support
- Responsive design utilities

**Project Structure:**

```
frontend/
├── src/
│   ├── lib/
│   │   ├── desktop/              # Desktop-specific UI
│   │   │   ├── components/       # Reusable components
│   │   │   │   ├── ui/           # Basic UI components
│   │   │   │   ├── media/        # Audio/spectrogram players
│   │   │   │   ├── forms/        # Form components
│   │   │   │   └── data/         # Data display components
│   │   │   ├── features/         # Feature-specific modules
│   │   │   │   ├── dashboard/    # Dashboard feature
│   │   │   │   ├── settings/     # Settings management
│   │   │   │   └── detections/   # Detection history
│   │   │   └── layouts/          # Page layouts
│   │   ├── utils/                # Utility functions
│   │   │   ├── api.ts            # API client
│   │   │   ├── cn.ts             # Class name utility
│   │   │   └── date.ts           # Date formatting
│   │   ├── types/                # TypeScript type definitions
│   │   └── stores/               # Global state stores
│   ├── App.svelte                # Main application component
│   └── main.js                   # Application entry point
├── static/                       # Static assets
│   ├── images/                   # Images
│   ├── icons/                    # Icons
│   └── messages/                 # i18n message files
├── tests/                        # E2E tests
│   ├── dashboard.test.ts         # Dashboard E2E tests
│   └── settings.test.ts          # Settings E2E tests
├── vite.config.js                # Vite configuration
├── tsconfig.json                 # TypeScript configuration
├── tailwind.config.js            # Tailwind configuration
├── vitest.config.ts              # Vitest configuration (in vite.config.js)
└── playwright.config.ts          # Playwright configuration
```

**Component Architecture:**

**Svelte 5 Runes (Reactivity System):**

Svelte 5 uses "runes" for reactivity - a compile-time reactive system:

```svelte
<script lang="ts">
  // $state - reactive state
  let count = $state(0);

  // $derived - computed values
  let doubled = $derived(count * 2);

  // $effect - side effects
  $effect(() => {
    console.log(`Count is ${count}`);
  });

  // $props - component props
  interface Props {
    title: string;
    onUpdate?: (value: number) => void;
  }
  let { title, onUpdate }: Props = $props();
</script>

<button onclick={() => count++}>
  {title}: {count} (doubled: {doubled})
</button>
```

**Component Pattern:**

```svelte
<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import type { Snippet } from 'svelte';

  interface Props {
    className?: string;
    disabled?: boolean;
    children?: Snippet;
  }

  let { className = '', disabled = false, children }: Props = $props();
</script>

<div class={cn('base-class', className, { 'disabled': disabled })}>
  {#if children}
    {@render children()}
  {/if}
</div>
```

**Key Features:**

- **Snippets**: Replace slots for better composition
- **$props()**: Automatic prop reactivity
- **$state()**: Fine-grained reactivity
- **$derived()**: Computed values
- **$effect()**: Side effect management

**Styling Approach:**

```svelte
<script lang="ts">
  import { cn } from '$lib/utils/cn.js';

  let { className = '' } = $props();
</script>

<!-- Tailwind + DaisyUI + conditional classes -->
<button class={cn(
  'btn btn-primary',           // DaisyUI base
  'rounded-lg shadow-md',      // Tailwind utilities
  { 'btn-disabled': disabled }, // Conditional
  className                     // User overrides
)}>
  Click me
</button>
```

**Type Safety:**

```typescript
// types/api.ts
export interface Detection {
  id: number;
  date: string;
  scientificName: string;
  commonName: string;
  confidence: number;
  latitude?: number;
  longitude?: number;
}

export interface ApiResponse<T> {
  data: T;
  error?: string;
  status: number;
}

// Component usage
interface Props {
  detections: Detection[];
  onSelect: (detection: Detection) => void;
}
```

### Real-Time Communication

**Server-Sent Events (SSE):**

The frontend uses SSE for real-time updates from the backend:

```typescript
// utils/sse.ts
export class SSEClient {
  private eventSource: EventSource | null = null;

  connect(url: string, handlers: Record<string, (data: any) => void>) {
    this.eventSource = new EventSource(url);

    for (const [event, handler] of Object.entries(handlers)) {
      this.eventSource.addEventListener(event, (e) => {
        handler(JSON.parse(e.data));
      });
    }
  }

  disconnect() {
    this.eventSource?.close();
  }
}
```

**Usage in Components:**

```svelte
<script lang="ts">
  import { SSEClient } from '$lib/utils/sse.js';
  import { onMount, onDestroy } from 'svelte';

  let detections = $state<Detection[]>([]);
  const sseClient = new SSEClient();

  onMount(() => {
    sseClient.connect('/api/v2/events/detections', {
      'detection': (detection) => {
        detections = [detection, ...detections];
      }
    });
  });

  onDestroy(() => {
    sseClient.disconnect();
  });
</script>
```

### State Management

**Global State (Stores):**

For cross-component state, use Svelte stores:

```typescript
// stores/settings.ts
import { writable } from "svelte/store";

export interface Settings {
  theme: "light" | "dark";
  language: string;
  threshold: number;
}

export const settings = writable<Settings>({
  theme: "light",
  language: "en",
  threshold: 0.7,
});

// Auto-persist to localStorage
settings.subscribe((value) => {
  localStorage.setItem("settings", JSON.stringify(value));
});
```

**Component State (Runes):**

For component-local state, use Svelte 5 runes:

```svelte
<script lang="ts">
  // Local state with $state
  let isOpen = $state(false);
  let searchQuery = $state('');

  // Derived state with $derived
  let filteredItems = $derived(
    items.filter(item =>
      item.name.toLowerCase().includes(searchQuery.toLowerCase())
    )
  );
</script>
```

### Testing Strategy

**Unit Testing (Vitest):**

Vitest is used for component and utility testing:

```typescript
// components/Button.test.ts
import { render, screen } from "@testing-library/svelte";
import { describe, it, expect } from "vitest";
import Button from "./Button.svelte";

describe("Button", () => {
  it("renders with text", () => {
    render(Button, { props: { text: "Click me" } });
    expect(screen.getByText("Click me")).toBeInTheDocument();
  });

  it("handles click events", async () => {
    let clicked = false;
    render(Button, {
      props: {
        text: "Click",
        onclick: () => {
          clicked = true;
        },
      },
    });

    await screen.getByText("Click").click();
    expect(clicked).toBe(true);
  });
});
```

**E2E Testing (Playwright):**

Playwright tests user workflows:

```typescript
// tests/dashboard.test.ts
import { test, expect } from "@playwright/test";

test("dashboard displays recent detections", async ({ page }) => {
  await page.goto("/");

  // Wait for detections to load
  await page.waitForSelector('[data-testid="detection-card"]');

  // Verify detection cards are present
  const cards = await page.locator('[data-testid="detection-card"]').count();
  expect(cards).toBeGreaterThan(0);
});

test("audio player controls work", async ({ page }) => {
  await page.goto("/");

  // Click play button
  await page.click('[data-testid="play-button"]');

  // Verify audio is playing
  const isPlaying = await page
    .locator('[data-testid="audio-player"]')
    .getAttribute("data-playing");
  expect(isPlaying).toBe("true");
});
```

**Running Tests:**

```bash
# Unit tests
task frontend-test

# Unit tests with UI
npm run test:ui

# E2E tests
task e2e-test

# E2E tests in headed mode (browser visible)
task e2e-test-headed

# E2E tests with UI (interactive)
task e2e-test-ui
```

---

## Build System

### Frontend Compilation

**Vite Build Process:**

The frontend is compiled to static JavaScript and CSS:

```bash
# Development build (with source maps)
npm run build:dev

# Production build (optimized)
npm run build
```

**Output:**

```
frontend/dist/
├── index.html           # HTML entry point
├── *.js                 # JavaScript bundles
├── *.css                # Stylesheets
├── messages/            # i18n message files
└── assets/              # Images and other static assets
```

### Embedding in Go Binary

**Go Embed Directive:**

The compiled frontend is embedded using Go's `embed` package:

```go
// frontend/embed.go
package frontend

import (
    "embed"
    "io/fs"
)

//go:embed all:dist
var distDir embed.FS

// DistFS is the embedded Svelte build output filesystem
var DistFS fs.FS

func init() {
    // Strip the "dist" prefix to serve files directly
    DistFS, _ = fs.Sub(distDir, "dist")
}
```

The `DistFS` variable is then used by the HTTP controller to serve static assets:

```go
// internal/httpcontroller/svelte_handler.go
file, err := frontend.DistFS.Open(path)
```

**Benefits:**

- **Single binary deployment**: No external file dependencies
- **Immutable assets**: Frontend version tied to binary version
- **Simplified distribution**: One file to distribute
- **Better caching**: Static assets have content-based hashes

### Cross-Platform Builds

**Taskfile Targets:**

```bash
# Build for current platform
task

# Development server with hot reload
task dev_server

# Cross-platform builds
task linux_amd64
task linux_arm64
task darwin_amd64
task darwin_arm64
task windows_amd64

# Build all platforms
task build_all
```

**CGO Considerations:**

Cross-compiling with CGO requires platform-specific toolchains:

```bash
# Linux ARM64 from Linux AMD64
CGO_ENABLED=1 \
GOOS=linux \
GOARCH=arm64 \
CC=aarch64-linux-gnu-gcc \
go build -o birdnet-go-arm64
```

**Build Tags:**

```go
//go:build !windows
// +build !windows

// Unix-specific code
```

### Hot Reload Development

**Air (Backend Hot Reload):**

Air watches Go files and rebuilds on changes:

```bash
# Start development server with hot reload
task dev_server
```

Configuration (`.air.toml`):

```toml
[build]
  cmd = "task frontend-build && go build -o ./tmp/main ."
  bin = "./tmp/main"
  include_ext = ["go", "html", "svelte", "ts"]
  exclude_dir = ["tmp", "vendor", "frontend/node_modules"]
```

**Hot Module Replacement (HMR):**

Since the frontend is embedded in the Go binary, standalone Vite dev server is **not supported**. Instead, use Air for hot reload during development:

```bash
# Development server with hot reload (rebuilds frontend + backend)
task dev_server

# Or directly with Air
air realtime
```

Air watches both Go and frontend files, rebuilding and restarting the server automatically when changes are detected.

---

## Platform Support

### Target Platforms

**Operating Systems:**

- **Linux**: Primary development platform
  - Debian/Ubuntu (systemd service support)
  - Raspberry Pi OS (ARM32/ARM64)
  - Debian Trixie (Docker base image)
- **macOS**: Desktop support
  - Intel (amd64)
  - Apple Silicon (arm64)
- **Windows**: Desktop support
  - x64 (amd64)
  - ARM64 (experimental)

**Architectures:**

- **amd64 (x86-64)**: Desktop PCs, servers
- **arm64 (aarch64)**: Raspberry Pi 3B+/4/5, Apple Silicon, ARM servers
- **arm (32-bit)**: Raspberry Pi Zero/2/3 (legacy support)

### Hardware Requirements

**Minimum (Raspberry Pi 3B+):**

- CPU: ARM Cortex-A53 (4 cores) @ 1.4 GHz
- RAM: 1GB
- Storage: 4GB (8GB+ recommended)
- Audio: USB microphone or RTSP camera

**Recommended (Raspberry Pi 4/5 or Desktop):**

- CPU: 4+ cores @ 1.5 GHz+
- RAM: 2GB+
- Storage: 16GB+
- Audio: Quality USB microphone

**Performance Characteristics:**

| Platform         | Inference Time | Max Concurrent Streams |
| ---------------- | -------------- | ---------------------- |
| Raspberry Pi 3B+ | ~800ms         | 1                      |
| Raspberry Pi 4   | ~300ms         | 2-3                    |
| Desktop (AMD64)  | ~100ms         | 5+                     |

### Platform-Specific Features

**Linux:**

- Systemd service integration
- ALSA audio capture
- FFmpeg hardware acceleration support

**macOS:**

- CoreAudio input
- Apple Silicon optimization
- Native ARM64 support

**Windows:**

- WASAPI audio capture
- Windows Service support
- PowerShell scripts for management

---

## API Design

### API v1 (Deprecated)

**⚠️ API v1 is frozen - no new endpoints will be added.**

Located in: `internal/httpcontroller/handlers/`

Legacy API used by HTMX frontend. Preserved for backwards compatibility but should not be extended.

### API v2 (Active Development)

**✅ All new API endpoints must be in API v2.**

Located in: `internal/api/v2/`

**Design Principles:**

- **RESTful**: Standard HTTP methods and status codes
- **JSON**: All requests/responses in JSON format
- **Versioned**: `/api/v2/` prefix for versioning
- **Authenticated**: JWT tokens for protected endpoints
- **Documented**: OpenAPI/Swagger documentation

**API Structure:**

```
internal/api/v2/
├── router.go           # API v2 router
├── middleware.go       # API v2 middleware
├── auth/               # Authentication endpoints
│   ├── login.go        # POST /api/v2/auth/login
│   └── logout.go       # POST /api/v2/auth/logout
├── detections/         # Detection endpoints
│   ├── list.go         # GET /api/v2/detections
│   ├── get.go          # GET /api/v2/detections/:id
│   └── delete.go       # DELETE /api/v2/detections/:id
├── settings/           # Settings endpoints
│   ├── get.go          # GET /api/v2/settings
│   └── update.go       # PUT /api/v2/settings
├── spectrogram/        # Spectrogram endpoints
│   ├── get.go          # GET /api/v2/spectrogram/:id
│   └── generate.go     # POST /api/v2/spectrogram/:id/generate
└── events/             # SSE endpoints
    ├── detections.go   # GET /api/v2/events/detections
    └── status.go       # GET /api/v2/events/status
```

**Example Endpoint:**

```go
// internal/api/v2/detections/list.go
package detections

import (
    "net/http"
    "github.com/labstack/echo/v4"
)

type ListRequest struct {
    StartDate  string  `query:"start_date"`
    EndDate    string  `query:"end_date"`
    Species    string  `query:"species"`
    MinConfidence float64 `query:"min_confidence"`
    Limit      int     `query:"limit"`
    Offset     int     `query:"offset"`
}

type ListResponse struct {
    Detections []Detection `json:"detections"`
    Total      int         `json:"total"`
    Limit      int         `json:"limit"`
    Offset     int         `json:"offset"`
}

func List(c echo.Context) error {
    var req ListRequest
    if err := c.Bind(&req); err != nil {
        return echo.NewHTTPError(http.StatusBadRequest, err.Error())
    }

    // Query database
    detections, total, err := queryDetections(req)
    if err != nil {
        return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
    }

    return c.JSON(http.StatusOK, ListResponse{
        Detections: detections,
        Total:      total,
        Limit:      req.Limit,
        Offset:     req.Offset,
    })
}
```

**Error Handling:**

Consistent error responses:

```json
{
  "error": "Resource not found",
  "code": "NOT_FOUND",
  "details": {
    "resource": "detection",
    "id": 12345
  }
}
```

---

## Security Architecture

### Authentication

**JWT-Based Authentication:**

```
internal/security/
├── jwt.go              # JWT token generation/validation
├── middleware.go       # Authentication middleware
└── password.go         # Password hashing (bcrypt)
```

**Token Flow:**

1. User logs in with username/password
2. Server validates credentials
3. Server generates JWT with claims
4. Client stores token (localStorage/sessionStorage)
5. Client includes token in `Authorization` header
6. Server validates token on protected routes

### Authorization

**Role-Based Access Control (RBAC):**

```go
// Roles
const (
    RoleAdmin = "admin"
    RoleUser  = "user"
    RoleGuest = "guest"
)

// Middleware
func RequireRole(role string) echo.MiddlewareFunc {
    return func(next echo.HandlerFunc) echo.HandlerFunc {
        return func(c echo.Context) error {
            userRole := getUserRole(c)
            if userRole != role {
                return echo.ErrForbidden
            }
            return next(c)
        }
    }
}
```

### Content Security Policy

**CSP Headers:**

```go
// Strict CSP for XSS protection
c.Response().Header().Set("Content-Security-Policy",
    "default-src 'self'; "+
    "script-src 'self' 'unsafe-inline'; "+
    "style-src 'self' 'unsafe-inline'; "+
    "img-src 'self' data: https:; "+
    "connect-src 'self' wss:; "+
    "font-src 'self'; "+
    "object-src 'none'; "+
    "base-uri 'self'; "+
    "form-action 'self';")
```

### Input Validation

**Request Validation:**

```go
// Validation tags
type CreateDetectionRequest struct {
    CommonName     string  `json:"common_name" validate:"required,min=1,max=100"`
    ScientificName string  `json:"scientific_name" validate:"required,min=1,max=200"`
    Confidence     float64 `json:"confidence" validate:"required,min=0,max=1"`
    Timestamp      string  `json:"timestamp" validate:"required,datetime"`
}

// Validator middleware
validator := validator.New()
if err := validator.Struct(req); err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, err.Error())
}
```

### Privacy by Design

**Data Minimization:**

- No telemetry by default
- Optional opt-in analytics
- No external API calls without user consent
- Local-first data storage

**Data Retention:**

- User-configurable detection history retention
- Automatic cleanup of old detections
- Audio clips not stored by default (only metadata)

---

## Performance Considerations

### Memory Management

**Buffer Pooling:**

Reuse audio buffers to minimize GC pressure:

```go
var pcmBufferPool = sync.Pool{
    New: func() interface{} {
        buf := make([]float32, 144000) // 3 seconds at 48kHz
        return &buf
    },
}

// Acquire buffer
buf := pcmBufferPool.Get().(*[]float32)
defer pcmBufferPool.Put(buf) // Return to pool
```

**Database Connection Pooling:**

```go
// GORM configuration
db.DB().SetMaxIdleConns(10)
db.DB().SetMaxOpenConns(100)
db.DB().SetConnMaxLifetime(time.Hour)
```

### Concurrency

**Worker Pool Pattern:**

```go
// Create worker pool for analysis
numWorkers := runtime.NumCPU()
for i := 0; i < numWorkers; i++ {
    go func() {
        for chunk := range audioQueue {
            // Process chunk
            analyzeAudio(chunk)
        }
    }()
}
```

**Rate Limiting:**

```go
// Limit concurrent spectrogram generation
semaphore := make(chan struct{}, 2) // Max 2 concurrent

func generateSpectrogram(id string) error {
    semaphore <- struct{}{}        // Acquire
    defer func() { <-semaphore }() // Release

    // Generate spectrogram
    return generate(id)
}
```

### Caching

**In-Memory Cache:**

```go
// Cache for species labels
type SpeciesCache struct {
    mu    sync.RWMutex
    cache map[string]string
}

func (c *SpeciesCache) Get(key string) (string, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    val, ok := c.cache[key]
    return val, ok
}
```

**Filesystem Cache:**

Pre-rendered spectrograms cached on disk:

```
data/
└── spectrograms/
    ├── abc123.png      # Cached spectrogram
    └── def456.png
```

### Frontend Performance

**Code Splitting:**

The frontend uses **server-side routing** (handled by Go backend). Components are lazy-loaded based on the current path:

```javascript
// App.svelte - Dynamic component loading
async function loadComponent(route: string): Promise<void> {
  switch (route) {
    case 'settings':
      if (!Settings) {
        const module = await import('./lib/desktop/views/Settings.svelte');
        Settings = module.default;
      }
      break;
    case 'detections':
      if (!Detections) {
        const module = await import('./lib/desktop/views/Detections.svelte');
        Detections = module.default;
      }
      break;
  }
}
```

**Note:** There is **no client-side router** (like React Router or SvelteKit routing). The Go backend serves different pages at different URLs (`/ui/dashboard`, `/ui/settings`, etc.), and the frontend determines which component to render based on `window.location.pathname`.

**Bundle Size Optimization:**

```bash
# Production build with analysis
npm run build -- --mode production

# Typical bundle sizes:
# - Main chunk: ~150KB (gzipped)
# - Vendor chunk: ~80KB (gzipped)
# - Route chunks: ~20-30KB each (gzipped)
```

**Image Optimization:**

- Lazy loading for spectrogram images
- WebP format for smaller file sizes
- Responsive images with srcset

---

## Development Tools

### Code Quality

**Linting:**

```bash
# Backend linting
golangci-lint run -v

# Frontend linting
npm run check:all
```

**Formatting:**

```bash
# Go formatting (via golangci-lint)
golangci-lint run --fix

# Markdown formatting
task format-md

# Frontend formatting (Prettier)
npm run format
```

### Pre-Commit Hooks

**Husky Hooks:**

Automatically run linting and formatting before commits:

```json
// package.json
{
  "husky": {
    "hooks": {
      "pre-commit": "lint-staged"
    }
  },
  "lint-staged": {
    "*.go": ["golangci-lint run --fix"],
    "*.{ts,svelte}": ["prettier --write", "eslint --fix"],
    "*.md": ["prettier --write"]
  }
}
```

### Debugging

**Backend Debugging:**

```bash
# Run with debug logging
LOG_LEVEL=debug birdnet-go realtime

# Run with profiling
go run -race ./cmd/birdnet/

# Profile CPU
go tool pprof http://localhost:8080/debug/pprof/profile

# Profile memory
go tool pprof http://localhost:8080/debug/pprof/heap
```

**Frontend Debugging:**

```bash
# Development mode with source maps
npm run dev

# Svelte DevTools (browser extension)
# https://github.com/sveltejs/svelte-devtools
```

### Documentation

**Code Documentation:**

```go
// Package documentation in doc.go
// Function documentation with godoc format

// Example returns an example detection.
//
// The detection includes all required fields populated
// with sample data for testing purposes.
func Example() *Detection {
    return &Detection{
        CommonName: "Northern Cardinal",
        Confidence: 0.95,
    }
}
```

**API Documentation:**

API v2 includes OpenAPI/Swagger documentation (planned):

```
http://localhost:8080/api/v2/docs
```

---

## Future Architecture Considerations

### Planned Improvements

1. **Distributed Processing**: Support for multiple nodes analyzing different audio sources
2. **Cloud Storage**: Optional S3-compatible storage for audio clips and spectrograms
3. **Advanced ML Models**: Support for custom-trained models beyond BirdNET
4. **Real-Time Dashboards**: WebSocket-based live spectrogram streaming
5. **Mobile Apps**: Native iOS/Android apps with push notifications
6. **GraphQL API**: Alternative API for complex queries and subscriptions

### Scalability

**Horizontal Scaling:**

- Multiple BirdNET-Go instances behind load balancer
- Shared MySQL database for detections
- Redis for session storage and caching
- Message queue (NATS/RabbitMQ) for detection distribution

**Vertical Scaling:**

- GPU acceleration for BirdNET inference
- Multi-threaded audio processing
- Memory-mapped file I/O for large audio files

---

## Conclusion

BirdNET-Go's architecture balances simplicity with power:

- **Go backend**: Fast, efficient, cross-platform
- **Svelte 5 frontend**: Modern, reactive, type-safe
- **Single binary**: Easy deployment and distribution
- **Privacy-first**: No telemetry without opt-in
- **Extensible**: Clean API for integrations

For questions or contributions, see [CONTRIBUTING.md](CONTRIBUTING.md) or join our [Discord](https://discord.gg/gcSCFGUtsd).
