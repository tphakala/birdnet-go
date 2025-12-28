# Backup Package (`internal/backup`)

**Version:** 1.0.0

## Overview

The `internal/backup` package provides a comprehensive framework for managing backups within the BirdNET-Go application. It orchestrates the entire backup lifecycle, including:

- **Source Management:** Defining and handling various data sources to be backed up (e.g., SQLite databases).
- **Target Management:** Defining and interacting with various storage destinations for backups (e.g., local filesystem, S3).
- **Scheduling:** Running backups automatically on daily or weekly schedules.
- **State Management:** Persistently tracking the status, history, and statistics of backups.
- **Encryption:** Optional AES-256-GCM encryption for backup archives.
- **Compression:** Optional Gzip compression for backup archives.
- **Archiving:** Packaging backup data, configuration, and metadata into TAR archives.
- **Error Handling:** Providing structured error types for robust error management.
- **Cleanup:** Implementing retention policies to manage the number and age of stored backups.
- **Metadata:** Storing detailed metadata with each backup (timestamp, size, source, type, versions, etc.).
- **Platform Compatibility:** Handling platform-specific details (like file metadata) correctly across Linux, macOS, and Windows.

This package is designed to be extensible, allowing developers to easily add new backup sources and targets.

## Key Concepts

- **Source:** Represents a data entity to be backed up (e.g., a database). Must implement the `Source` interface.
- **Target:** Represents a storage location for backups (e.g., local disk, cloud storage). Must implement the `Target` interface.
- **Manager (`Manager`):** The central orchestrator. It manages registered sources and targets, initiates backup processes, handles encryption/compression, and applies retention policies.
- **Scheduler (`Scheduler`):** Responsible for triggering backups based on configured daily or weekly schedules. It uses the `Manager` to run the actual backup operations.
- **State Manager (`StateManager`):** Manages the persistent state of the backup system, stored in `backup-state.json` within the application's configuration directory. This includes last run times, success/failure status, missed runs, and target statistics.
- **Metadata (`Metadata`):** A struct containing essential information about a specific backup archive. This metadata is stored within the archive itself (`metadata.json`) and is also used by targets to list and manage backups.
- **Backup Archive:** A `.tar.gz` file (optionally encrypted) containing:
  - The actual backup data from the source (e.g., `backup.db`).
  - A `metadata.json` file describing the backup.
  - A sanitized `config.yml` file (passwords/secrets removed) from the time of the backup.

## Core Interfaces

### `Source`

```go
type Source interface {
    // Name returns the name of the source (e.g., "main_db")
    Name() string
    // Backup performs the backup and returns a reader for the data stream.
    Backup(ctx context.Context) (io.ReadCloser, error)
    // Validate checks if the source configuration is valid.
    Validate() error
}
```

- Implementations define how to extract data from a specific source.
- See `internal/backup/sources/sqlite.go` for an example.

### `Target`

```go
type Target interface {
    // Name returns the name of the target (e.g., "local_disk", "s3_backups")
    Name() string
    // Store takes a temporary local archive file path and its metadata,
    // uploads it to the target storage.
    Store(ctx context.Context, sourcePath string, metadata *Metadata) error
    // List returns metadata information for all backups stored in the target.
    List(ctx context.Context) ([]BackupInfo, error)
    // Delete removes a backup identified by its ID from the target storage.
    Delete(ctx context.Context, id string) error
    // Validate checks if the target configuration is valid.
    Validate() error
}
```

- Implementations define how to interact with specific storage systems.
- See `internal/backup/targets/local.go` (likely) for an example.

## Main Components

### `Manager`

- **Initialization:** `NewManager(fullConfig *conf.Settings, lg logger.Logger, stateManager *StateManager, appVersion string) (*Manager, error)` - accepts a `logger.Logger` interface from the centralized logging package
- **Registration:** `RegisterSource(source Source)`, `RegisterTarget(target Target)`
- **Execution:** `RunBackup(ctx context.Context)` performs an immediate backup of all registered sources to all registered targets.
- **Listing:** `ListBackups(ctx context.Context)` lists backups across all targets.
- **Deletion:** `DeleteBackup(ctx context.Context, id string)` deletes a specific backup by ID.
- **Cleanup:** `cleanupOldBackups(ctx context.Context)` (internal) enforces retention policies based on configuration.
- **Encryption:** Handles key generation (`GenerateEncryptionKey`), validation (`ValidateEncryption`), and provides methods for decryption (`DecryptData`). Keys are stored hex-encoded in `<config_dir>/encryption.key`.
- **Configuration:** Uses `conf.BackupConfig` for settings like enabling/disabling, timeouts, retention policies, encryption, and compression.

### `Scheduler`

- **Initialization:** `NewScheduler(manager *Manager, lg logger.Logger)` - accepts a `logger.Logger` interface
- **Configuration:** `LoadFromConfig(config *conf.BackupConfig)` reads schedule settings (daily time, weekly day/time) from the main configuration.
- **Lifecycle:** `Start()`, `Stop()`, `IsRunning()`
- **Execution:** Periodically checks schedules and calls `manager.RunBackup` when a backup is due.
- **State Interaction:** Uses the `StateManager` to record missed runs and update schedule status.

### `StateManager`

- **Initialization:** `NewStateManager(lg logger.Logger)` - accepts a `logger.Logger` interface and automatically loads state from `<config_dir>/backup-state.json`.
- **Persistence:** Automatically saves state changes to the JSON file atomically.
- **State Tracking:** Provides methods to update schedule status (`UpdateScheduleState`), target status (`UpdateTargetState`), record missed runs (`AddMissedBackup`), and update overall statistics (`UpdateStats`).
- **State Retrieval:** Offers methods to get the current state for schedules (`GetScheduleState`), targets (`GetTargetState`), missed runs (`GetMissedBackups`), and stats (`GetStats`).

## Backup Workflow

1.  **Initialization:** The main application creates `Manager`, `Scheduler`, and registers concrete `Source` and `Target` implementations based on the application configuration (`conf.Settings`).
2.  **Scheduling:** The `Scheduler` is started (`scheduler.Start()`). It loads schedule details from the configuration (`scheduler.LoadFromConfig()`).
3.  **Trigger:**
    - **Scheduled:** The `Scheduler`'s internal timer triggers based on the configured time(s).
    - **Manual:** The application calls `manager.RunBackup()` directly.
4.  **Execution (`manager.RunBackup`)**:
    - Iterates through each registered `Source`.
    - Calls `source.Backup()` to get a data stream (`io.ReadCloser`).
    - Creates a unique `Metadata` struct for the backup.
    - Creates a temporary TAR archive.
    - Adds `metadata.json` to the archive.
    - Adds a sanitized `config.yml` to the archive.
    - Streams the data from `source.Backup()` into the archive (e.g., as `backup.db`).
    - If compression is enabled, compresses the TAR archive using Gzip.
    - If encryption is enabled, encrypts the (potentially compressed) archive using AES-256-GCM with the key from `encryption.key`.
    - Iterates through each registered `Target`.
    - Calls `target.Store()` to upload the final archive file (plain or encrypted) along with its `Metadata`.
    - Updates the `StateManager` with the outcome for each target.
5.  **Cleanup (`manager.cleanupOldBackups`, potentially triggered by `Scheduler` after a successful run):**
    - For each `Target`:
      - Calls `target.List()` to get all stored backups.
      - Applies retention rules (keep N daily, keep N weekly, max age) based on `Metadata`.
      - Calls `target.Delete()` for backups that exceed the retention policy.
6.  **State Update:** The `Scheduler` (if it triggered the backup) or the application updates the `StateManager` with success/failure status and statistics.

## Configuration

The backup system is primarily configured via the `Backup` section within the main `conf.Settings` struct (likely mapped to `conf.BackupConfig` internally). Key settings include:

- `Enabled`: Master switch for the backup system.
- `Schedule`: Daily and weekly backup times/days.
- `Retention`: Policies for how many daily/weekly backups to keep and the maximum age.
- `Encryption`: Enable/disable backup encryption.
- `Compression`: Enable/disable Gzip compression.
- `Timeouts`: Durations for various operations (backup, store, delete, cleanup).
- Source-specific settings (e.g., database paths).
- Target-specific settings (e.g., local directory path, S3 bucket/credentials).

## Error Handling

The package defines custom error types for better classification and handling:

- **`backup.Error`:** The main error struct, containing:
  - `Code (backup.ErrorCode)`: A specific category (e.g., `ErrConfig`, `ErrIO`, `ErrDatabase`, `ErrMedia`, `ErrEncryption`, `ErrTimeout`, `ErrCanceled`).
  - `Message`: A human-readable description.
  - `Err`: The underlying wrapped error (if any).
- **`backup.ErrorCode`:** An enum defining specific error categories.
- **Helper Functions:** `NewError()`, `IsErrorCode()`, `IsMediaError()`, `IsTimeoutError()`, etc., are provided for creating and checking specific error types.

Errors are logged using structured fields with appropriate log levels (Error, Warn, Info, Debug). `ErrMedia` is specifically used to identify potential issues with storage media like SD cards.

## Encryption

- Uses AES-256-GCM for authenticated encryption.
- A 32-byte (256-bit) encryption key is required.
- The key is generated automatically on the first run if encryption is enabled and no key exists.
- The key is stored in hex format in `<config_dir>/encryption.key`.
- Permissions for the key file are set to `0o600`.
- The `Manager` provides `GenerateEncryptionKey`, `ValidateEncryption`, `GetEncryptionKey`, `DecryptData`, `ImportEncryptionKey` methods.
- If encryption is enabled, the entire `.tar.gz` archive is encrypted _before_ being sent to the target. The target stores the encrypted blob. Metadata stored _by the target itself_ (like filename/ID) is not encrypted by this package.

## State Management

- The `StateManager` persists the backup system's state in `<config_dir>/backup-state.json`.
- This file tracks:
  - Last update time of the state file.
  - State of each schedule (last attempt, last success, next run).
  - State of each target (last backup details, total size/count).
  - A list of missed backup runs with reasons.
  - Aggregated statistics per target.
- The state file is crucial for resuming schedules correctly after restarts and for tracking backup history/health.
- Writes to the state file are atomic (write to temp file, then rename).

## Extensibility

- **Adding a New Source:**
  1.  Create a new type in the `internal/backup/sources` directory.
  2.  Implement the `backup.Source` interface for this type.
  3.  In the main application setup, instantiate this new source type based on configuration.
  4.  Register the instance with the `backup.Manager` using `manager.RegisterSource()`.
- **Adding a New Target:**
  1.  Create a new type in the `internal/backup/targets` directory.
  2.  Implement the `backup.Target` interface for this type.
  3.  In the main application setup, instantiate this new target type based on configuration.
  4.  Register the instance with the `backup.Manager` using `manager.RegisterTarget()`.

## Platform Considerations

- **File Paths:** Uses `path/filepath` for cross-platform path manipulation.
- **Temporary Files:** Uses `os.CreateTemp` which respects OS-specific temporary directories.
- **Error Handling:** The `isMediaError` function (found in `sources/sqlite.go`, but the concept might be relevant elsewhere) uses `runtime.GOOS` and platform-specific `syscall.Errno` values to detect media issues (like SD card errors) on Windows, Linux, and macOS.
- **File Metadata:** Uses `metadata_unix.go` and `metadata_windows.go` with build tags (`//go:build`) to handle platform-specific file attributes (like UID/GID on Unix) when potentially needed (though the current core `backup.go` doesn't seem to _actively_ use `FileMetadata` for archive creation, the capability exists).

## Logging

The backup package uses the centralized `logger.Logger` interface from `internal/logger`. This provides:

- **Structured logging** with typed field constructors
- **Module-scoped loggers** for hierarchical log organization
- **Consistent logging format** across all backup components

### Logger Interface

All components accept a `logger.Logger` interface. If `nil` is passed, they default to `logger.Global().Module("backup")`.

### Structured Field Constructors

Use the typed field constructors for structured logging:

```go
import "github.com/tphakala/birdnet-go/internal/logger"

log.Info("Backup completed",
    logger.String("target", "local"),
    logger.Int64("size_bytes", fileSize),
    logger.Int("file_count", count),
    logger.Bool("encrypted", true),
    logger.Error(err),
    logger.Any("metadata", metadata))
```

### Module Hierarchy

Each component creates a sub-module logger:

```go
// Manager creates: backup
// Scheduler creates: backup.scheduler
// StateManager creates: backup.state
// Targets create: backup.local, backup.sftp, backup.gdrive, backup.rsync, backup.ftp
// Sources create: backup.sqlite
```

## Usage Example (Conceptual)

```go
package main

import (
    "fmt"

    "github.com/tphakala/birdnet-go/internal/backup"
    "github.com/tphakala/birdnet-go/internal/backup/sources"
    "github.com/tphakala/birdnet-go/internal/backup/targets"
    "github.com/tphakala/birdnet-go/internal/conf"
    "github.com/tphakala/birdnet-go/internal/logger"
)

func initializeBackup(config *conf.Settings) error {
    // Get a module-scoped logger
    log := logger.Global().Module("backup")

    // Check if backups are enabled
    if !config.Backup.Enabled {
        log.Info("backups are disabled in configuration")
        return nil
    }

    // Initialize state manager with logger
    stateManager, err := backup.NewStateManager(log)
    if err != nil {
        return fmt.Errorf("failed to initialize state manager: %w", err)
    }

    // Create Backup Manager with logger
    backupManager, err := backup.NewManager(config, log, stateManager, "1.0.0")
    if err != nil {
        return fmt.Errorf("failed to create backup manager: %w", err)
    }

    // --- Register Sources (Example: SQLite) ---
    if config.Output.SQLite.Enabled {
        sqliteSource := sources.NewSQLiteSource(config, log)
        if err := backupManager.RegisterSource(sqliteSource); err != nil {
            log.Warn("failed to register SQLite source", logger.Error(err))
        } else {
            log.Info("registered SQLite backup source")
        }
    }

    // --- Register Targets (Example: Local Disk) ---
    if config.Backup.Targets.Local.Enabled {
        localTarget, err := targets.NewLocalTarget(config.Backup.Targets.Local, log)
        if err != nil {
            return fmt.Errorf("failed to create local target: %w", err)
        }
        if err := backupManager.RegisterTarget(localTarget); err != nil {
            log.Warn("failed to register Local target", logger.Error(err))
        } else {
            log.Info("registered Local backup target")
        }
    }

    // Start the manager (validates sources/targets/encryption)
    if err := backupManager.Start(); err != nil {
        return fmt.Errorf("failed to start backup manager: %w", err)
    }

    // Create and start the Scheduler with logger
    scheduler, err := backup.NewScheduler(backupManager, log)
    if err != nil {
        return fmt.Errorf("failed to create scheduler: %w", err)
    }

    if err := scheduler.LoadFromConfig(&config.Backup); err != nil {
        return fmt.Errorf("failed to load schedule from config: %w", err)
    }

    scheduler.Start()
    log.Info("backup scheduler started")

    return nil
}
```

This documentation should provide a solid foundation for understanding and working with the `internal/backup` package.
