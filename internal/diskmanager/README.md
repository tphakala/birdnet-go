# Disk Manager Package

This package provides a robust solution for managing disk space in the BirdNET-Go application, with a focus on handling audio clip files in a cross-platform manner.

## Features

- Cross-platform disk usage monitoring for Linux, macOS, and Windows
- Configurable retention policies based on age and disk usage
- Protection of clips marked as "locked" in the database
- Minimum clip count protection per species
- Graceful cleanup operations with intelligent file sorting
- Structured logging integration
- Support for multiple audio file formats
- Concurrent operation with cancellation support
- Testable design with mocking capabilities

## Package Organization

The diskmanager package is organized into several files for maintainability:

- **file_utils.go** - Core utilities and shared functionality
- **policy_age.go** - Age-based retention policy implementation
- **policy_usage.go** - Disk usage-based retention policy implementation
- **disk_usage_unix.go** - Unix-specific (Linux/macOS) disk usage functionality
- **disk_usage_windows.go** - Windows-specific disk usage functionality
- **policy_age_test.go** - Tests for age-based policy
- **policy_usage_test.go** - Tests for usage-based policy

## Basic Usage

### Import the package

```go
import "github.com/tphakala/birdnet-go/internal/diskmanager"
```

### Create a DiskManager instance

```go
// Create a DiskManager with a parent logger and database interface
diskManager := diskmanager.NewDiskManager(logger, db)
```

### Apply an age-based retention policy

```go
// Create a quit channel for cancellation
quit := make(chan struct{})

// Apply the age-based retention policy
err := diskManager.AgeBasedCleanup(quit)
if err != nil {
    // handle error
}
```

### Apply a usage-based retention policy

```go
// Create a quit channel for cancellation
quit := make(chan struct{})

// Apply the usage-based retention policy
err := diskManager.UsageBasedCleanup(quit)
if err != nil {
    // handle error
}
```

### Get a list of audio files

```go
// Get all audio files from a directory, with allowed file extensions
allowedTypes := []string{".wav", ".flac", ".mp3", ".aac", ".opus"}
files, err := diskmanager.GetAudioFiles(baseDir, allowedTypes, db, debug)
if err != nil {
    // handle error
}

// Process the files
for _, file := range files {
    fmt.Printf("File: %s, Species: %s, Timestamp: %s\n", 
        file.Path, file.Species, file.Timestamp)
}
```

## Configuration

The diskmanager package relies on the application's configuration system for settings:

```go
// Example config structure (from conf package)
settings := conf.Setting()

// Age-based retention settings
debug := settings.Realtime.Audio.Export.Retention.Debug
baseDir := settings.Realtime.Audio.Export.Retention.Path
minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips
retentionPeriod := settings.Realtime.Audio.Export.Retention.MaxAge

// Usage-based retention settings
threshold := settings.Realtime.Audio.Export.Retention.MaxUsage // e.g., "80%"
policy := settings.Realtime.Audio.Export.Retention.Policy // "age" or "usage"
```

## Core Components

### DiskManager

The central struct that manages disk operations:

```go
type DiskManager struct {
    Logger *logger.Logger
    DB     Interface
}
```

### FileInfo

Represents information about an audio file:

```go
type FileInfo struct {
    Path       string      // Full path to the file
    Species    string      // Species name
    Confidence int         // Confidence level (percentage)
    Timestamp  time.Time   // Timestamp when the recording was made
    Size       int64       // File size in bytes
    Locked     bool        // Whether the file is locked (protected)
}
```

### Interface

The minimal database interface required for disk management:

```go
type Interface interface {
    GetLockedNotesClipPaths() ([]string, error)
}
```

## Retention Policies

### Age-Based Retention

Removes files based on their age, respecting minimum clip counts per species:

```go
func (dm *DiskManager) AgeBasedCleanup(quit <-chan struct{}) error
```

- Reads retention period from configuration
- Converts retention period to hours
- Gets list of locked clips from database
- Identifies clips older than the retention period
- Ensures minimum clip count per species is maintained
- Deletes eligible files while monitoring for cancellation

### Usage-Based Retention

Removes files based on disk usage percentage, prioritizing by age and other criteria:

```go
func (dm *DiskManager) UsageBasedCleanup(quit <-chan struct{}) error
```

- Reads maximum usage threshold from configuration (e.g., "80%")
- Gets current disk usage percentage
- If usage exceeds threshold, sorts files by priority
- Deletes files while respecting minimum clip counts
- Continues until usage is below threshold or no more eligible files

## Cross-Platform Support

The package provides platform-specific implementations for disk usage monitoring:

### Unix (Linux/macOS)

Uses `syscall.Statfs_t` to get disk usage information:

```go
// In disk_usage_unix.go
func GetDiskUsage(baseDir string) (float64, error)
```

### Windows

Uses the Windows API through `syscall` to get disk usage information:

```go
// In disk_usage_windows.go
func GetDiskUsage(baseDir string) (float64, error)
```

## Testing

The package includes comprehensive tests with mocking capabilities:

### Mock File Info

For testing file operations without actual files:

```go
type MockFileInfo struct {
    FileName    string
    FileSize    int64
    FileMode    os.FileMode
    FileModTime time.Time
    FileIsDir   bool
    FileSys     interface{}
}
```

### Mock Database

For testing database interactions:

```go
// In your tests
type MockDB struct {}

func (m *MockDB) GetLockedNotesClipPaths() ([]string, error) {
    return []string{"path/to/locked/file.wav"}, nil
}
```

## Best Practices

1. **Always provide a proper logger**:
   ```go
   diskManager := diskmanager.NewDiskManager(parentLogger, db)
   ```

2. **Handle cancellation gracefully**:
   ```go
   quit := make(chan struct{})
   
   // In a goroutine that might need to cancel
   close(quit)
   ```

3. **Check returned errors**:
   ```go
   if err := diskManager.AgeBasedCleanup(quit); err != nil {
       logger.Error("Failed to perform age-based cleanup", "error", err)
   }
   ```

4. **Use the appropriate policy for your needs**:
   - Age-based: When you want files to be kept for a specific duration
   - Usage-based: When you want to ensure disk usage stays below a threshold

5. **Configure appropriate minimum clip counts**:
   ```go
   // In your configuration
   settings.Realtime.Audio.Export.Retention.MinClips = 5 // Keep at least 5 clips per species
   ```

## Implementation Notes

- The package uses a structured logger for consistent logging
- File operations are performed directly using the `os` package
- File paths are handled in a cross-platform manner using `filepath`
- Goroutine scheduling is managed with `runtime.Gosched()` to prevent blocking
- Protected clips are identified through database integration

## Application Integration

To integrate disk management into your application:

```go
func main() {
    // Initialize logger
    logger := initLogger()
    
    // Initialize database
    db := initDatabase()
    
    // Create disk manager
    diskManager := diskmanager.NewDiskManager(logger, db)
    
    // Create quit channel
    quit := make(chan struct{})
    
    // Set up cleanup to run periodically
    go func() {
        ticker := time.NewTicker(1 * time.Hour)
        defer ticker.Stop()
        
        for {
            select {
            case <-ticker.C:
                // Run the appropriate policy based on configuration
                if settings.Realtime.Audio.Export.Retention.Policy == "age" {
                    if err := diskManager.AgeBasedCleanup(quit); err != nil {
                        logger.Error("Age-based cleanup failed", "error", err)
                    }
                } else if settings.Realtime.Audio.Export.Retention.Policy == "usage" {
                    if err := diskManager.UsageBasedCleanup(quit); err != nil {
                        logger.Error("Usage-based cleanup failed", "error", err)
                    }
                }
            case <-quit:
                return
            }
        }
    }()
    
    // ... rest of your application
}
```

## Security Considerations

The disk manager package handles file deletion, which requires careful consideration:

1. Always validate file paths before deletion
2. Ensure the deletion operation is constrained to the intended directory
3. Use the database's locked clips feature to protect important files
4. Consider implementing a "dry run" mode for testing cleanup operations
5. Log all deletion operations for auditing purposes 