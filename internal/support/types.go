// Package support provides functionality for collecting and uploading support logs
package support

import (
	"context"
	"time"
)

// SupportDump represents a collection of support data including logs, configuration,
// and system information used for troubleshooting and debugging BirdNET-Go issues.
// The data is privacy-scrubbed before collection to remove sensitive information.
type SupportDump struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	SystemID  string    `json:"system_id"`
	Version   string    `json:"version"`
	// Logs holds journal entries only. On-disk log files are counted for the
	// diagnostics summary and shipped raw in the archive, not retained here.
	Logs           []LogEntry            `json:"logs"`
	Config         map[string]any        `json:"config"`
	SystemInfo     SystemInfo            `json:"system_info"`
	Attachments    []AttachmentInfo      `json:"attachments"`
	Diagnostics    CollectionDiagnostics `json:"diagnostics"`               // Diagnostic information about collection process
	DatabaseInfo   *DatabaseInfo         `json:"database_info,omitempty"`   // Database schema and health diagnostics
	DeploymentInfo *DeploymentInfo       `json:"deployment_info,omitempty"` // Deployment context for troubleshooting
	AppEvents      []AppEventEntry       `json:"app_events,omitempty"`      // Recent application events
	RuntimeInfo    *RuntimeInfo          `json:"runtime_info,omitempty"`    // Inference runtime and loaded model state
}

// LogEntry represents a single log entry from application logs or system journals.
// It captures the timestamp, severity level, message content, and source of the log.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
}

// SystemInfo contains system information about the host environment where BirdNET-Go
// is running, including OS details, hardware specifications, and runtime information.
// This helps developers understand the deployment context when debugging issues.
type SystemInfo struct {
	OS           string      `json:"os"`
	Architecture string      `json:"architecture"`
	GoVersion    string      `json:"go_version"`
	CPUCount     int         `json:"cpu_count"`
	MemoryMB     uint64      `json:"memory_mb"`
	DiskInfo     []DiskInfo  `json:"disk_info"`
	DockerInfo   *DockerInfo `json:"docker_info,omitempty"`
	// CPUModel, CPUFlags, Board, SIMD, KernelVersion and CgroupMemory are
	// machine facts for hardware triage. All are best effort and omitted when
	// the host does not report them. None of them holds a path or a hostname.
	CPUModel      string   `json:"cpu_model,omitempty"`
	CPUFlags      []string `json:"cpu_flags,omitempty"`
	Board         string   `json:"board,omitempty"` // device-tree model, for example a Raspberry Pi revision
	SIMD          []string `json:"simd,omitempty"`  // SIMD extensions relevant to inference
	KernelVersion string   `json:"kernel_version,omitempty"`
	// CgroupMemory is the memory cgroup of the process; nil when no memory
	// controller is found.
	CgroupMemory *CgroupMemoryInfo `json:"cgroup_memory,omitempty"`
	// RuntimeEnv holds the environment variables on the collector's allowlist
	// (conf.SupportEnvAllowlist in production) that are actually set, so a dump
	// can report runtime gates that config.yaml cannot record. Only non-empty
	// values are present, so an absent key means the variable was unset OR set
	// to the empty string; the two are indistinguishable here and behave
	// identically at every gate. Empty and omitted entirely when no allowlisted
	// variable is set, which is the common case.
	RuntimeEnv map[string]string `json:"runtime_env,omitempty"`
	// USB and SoundCards are the connected USB devices and ALSA cards. Both are
	// nil only in dumps from versions that predate them; a version that has them
	// always sets both, with Available false and a Reason when they could not be read.
	USB        *USBInventory       `json:"usb,omitempty"`
	SoundCards *SoundCardInventory `json:"sound_cards,omitempty"`
}

// USBInventory lists the USB devices the host exposes. Serial numbers are never read.
type USBInventory struct {
	Available bool            `json:"available"`
	Reason    string          `json:"reason,omitempty"` // unsupported_os, not_visible, unreadable or cancelled when Available is false
	Truncated bool            `json:"truncated,omitempty"`
	Devices   []USBDeviceInfo `json:"devices,omitempty"`
}

// USBDeviceInfo is one USB device; every field except BusID is omitted when unknown.
type USBDeviceInfo struct {
	BusID        string `json:"bus_id"`
	VendorID     string `json:"vendor_id,omitempty"`
	ProductID    string `json:"product_id,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Product      string `json:"product,omitempty"`
	SpeedMbps    string `json:"speed_mbps,omitempty"`
	DeviceClass  string `json:"device_class,omitempty"`
}

// SoundCardInventory lists the ALSA sound cards the host exposes.
type SoundCardInventory struct {
	Available bool            `json:"available"`
	Reason    string          `json:"reason,omitempty"` // unsupported_os, not_visible, unreadable or cancelled when Available is false
	Truncated bool            `json:"truncated,omitempty"`
	Cards     []SoundCardInfo `json:"cards,omitempty"`
}

// SoundCardInfo is one ALSA sound card.
type SoundCardInfo struct {
	Index      int    `json:"index"`
	ID         string `json:"id,omitempty"`
	USBBusID   string `json:"usb_bus_id,omitempty"` // bus id of the backing USB device, empty for a non-USB card
	HasCapture bool   `json:"has_capture"`
}

// CgroupMemoryInfo is the memory cgroup limit and usage of the BirdNET-Go process.
type CgroupMemoryInfo struct {
	Version    string `json:"version"`               // "v2" or "v1"
	LimitBytes int64  `json:"limit_bytes"`           // 0 means no limit
	LimitExact bool   `json:"limit_exact"`           // false when the limit is only an upper bound
	UsageBytes int64  `json:"usage_bytes,omitempty"` // 0 means it could not be read
}

// DiskInfo represents information about a disk or filesystem mount point.
// It includes usage statistics that help diagnose storage-related issues.
type DiskInfo struct {
	Mountpoint string  `json:"mountpoint"`
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	UsagePerc  float64 `json:"usage_percent"`
}

// DockerInfo contains Docker-specific information when BirdNET-Go is running in a container.
// This includes the container ID and image details for container-specific debugging.
type DockerInfo struct {
	Version      string `json:"version"`
	ContainerID  string `json:"container_id"`
	ImageVersion string `json:"image_version"`
}

// AttachmentInfo contains metadata about a support dump attachment uploaded to Sentry.
// It tracks the filename, size, content type, and internal path for reference.
type AttachmentInfo struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Path        string `json:"-"` // Internal use only
}

// CollectorOptions configures what data to collect in a support dump.
// It allows users to control which types of information are included based on
// their privacy preferences and the specific issue being debugged.
type CollectorOptions struct {
	IncludeLogs           bool          `json:"include_logs"`
	IncludeConfig         bool          `json:"include_config"`
	IncludeSystemInfo     bool          `json:"include_system_info"`
	IncludeDatabaseInfo   bool          `json:"include_database_info"`
	IncludeDeploymentInfo bool          `json:"include_deployment_info"`
	IncludeAppEvents      bool          `json:"include_app_events"`
	LogDuration           time.Duration `json:"log_duration"`
	MaxLogSize            int64         `json:"max_log_size"`
	ScrubSensitive        bool          `json:"scrub_sensitive"`
	AnonymizePII          bool          `json:"anonymize_pii"`
}

// CollectionDiagnostics contains diagnostic information about the support data collection process.
// This helps troubleshoot why certain data might be missing from support dumps.
type CollectionDiagnostics struct {
	LogCollection    LogCollectionDiagnostics `json:"log_collection"`    // Diagnostics for log collection
	ConfigCollection DiagnosticInfo           `json:"config_collection"` // Diagnostics for config collection
	SystemCollection DiagnosticInfo           `json:"system_collection"` // Diagnostics for system info collection
}

// LogCollectionDiagnostics contains detailed information about log collection attempts
type LogCollectionDiagnostics struct {
	JournalLogs LogSourceDiagnostics `json:"journal_logs"` // Journal/systemd log collection results
	FileLogs    LogSourceDiagnostics `json:"file_logs"`    // File-based log collection results
	Summary     DiagnosticSummary    `json:"summary"`      // Overall log collection summary
}

// LogSourceDiagnostics contains diagnostics for a specific log source (journal or files).
// Thread-safety note: The Details map is currently only accessed from single-threaded
// contexts within the collection methods. If concurrent access is needed in the future,
// synchronization (e.g., sync.RWMutex) should be added.
type LogSourceDiagnostics struct {
	Attempted     bool           `json:"attempted"`         // Whether collection was attempted
	Successful    bool           `json:"successful"`        // Whether collection succeeded
	Error         string         `json:"error,omitempty"`   // Error message if failed
	EntriesFound  int            `json:"entries_found"`     // Number of log entries found
	PathsSearched []SearchedPath `json:"paths_searched"`    // Paths that were searched (for file logs)
	Command       string         `json:"command,omitempty"` // Command executed (for journal logs)
	Details       map[string]any `json:"details,omitempty"` // Additional diagnostic details (not thread-safe)
}

// SearchedPath represents a path that was searched during log collection
type SearchedPath struct {
	Path       string `json:"path"`            // The path that was searched
	Exists     bool   `json:"exists"`          // Whether the path exists
	Accessible bool   `json:"accessible"`      // Whether the path was readable
	FileCount  int    `json:"file_count"`      // Number of log files found
	Error      string `json:"error,omitempty"` // Error encountered if any
}

// DiagnosticInfo contains basic diagnostic information for a collection operation
type DiagnosticInfo struct {
	Attempted  bool   `json:"attempted"`       // Whether collection was attempted
	Successful bool   `json:"successful"`      // Whether collection succeeded
	Error      string `json:"error,omitempty"` // Error message if failed
}

// DiagnosticSummary provides an overall summary of the collection process
type DiagnosticSummary struct {
	TotalEntries    int       `json:"total_entries"`     // Total log entries collected across all sources
	TimeRange       TimeRange `json:"time_range"`        // Time range of collected logs
	SizeBytes       int64     `json:"size_bytes"`        // Approximate size of collected logs
	TruncatedBySize bool      `json:"truncated_by_size"` // Whether collection was truncated due to size limits
	TruncatedByTime bool      `json:"truncated_by_time"` // Whether logs were filtered by time range
}

// TimeRange represents a time range for log collection
type TimeRange struct {
	From time.Time `json:"from"` // Start time for log collection
	To   time.Time `json:"to"`   // End time for log collection
}

// DefaultCollectorOptions returns default collector options with sensible defaults:
// includes all data types, 4-week log window, 50MB max log size, sensitive data scrubbing and PII anonymization enabled.
func DefaultCollectorOptions() CollectorOptions {
	return CollectorOptions{
		IncludeLogs:           true,
		IncludeConfig:         true,
		IncludeSystemInfo:     true,
		IncludeDatabaseInfo:   true,
		IncludeDeploymentInfo: true,
		IncludeAppEvents:      true,
		LogDuration:           defaultLogDurationWeeks * 7 * 24 * time.Hour, // 4 weeks
		MaxLogSize:            defaultMaxLogSizeMB * bytesPerMB,             // 50MB to accommodate more logs
		ScrubSensitive:        true,
		AnonymizePII:          true,
	}
}

// DatabaseInfoProvider collects database diagnostic information.
// Implemented by the datastore layer, consumed by the support collector.
type DatabaseInfoProvider interface {
	CollectDatabaseInfo(ctx context.Context) (*DatabaseInfo, error)
}

// DatabaseInfo contains database schema and health information for troubleshooting.
type DatabaseInfo struct {
	Dialect           string                `json:"dialect"`
	EngineVersion     string                `json:"engine_version"`
	DatabasePath      string                `json:"database_path"`
	DatabaseSizeBytes int64                 `json:"database_size_bytes"`
	WALSizeBytes      int64                 `json:"wal_size_bytes,omitempty"`
	SHMSizeBytes      int64                 `json:"shm_size_bytes,omitempty"`
	SchemaVersion     string                `json:"schema_version"`
	Tables            []TableSchema         `json:"tables"`
	MigrationState    *MigrationStateInfo   `json:"migration_state,omitempty"`
	IntegrityCheck    string                `json:"integrity_check"`
	FKViolations      []ForeignKeyViolation `json:"foreign_key_violations"`
	AppMetadata       map[string]string     `json:"app_metadata,omitempty"`
	JournalMode       string                `json:"journal_mode,omitempty"`
	AutoVacuum        string                `json:"auto_vacuum,omitempty"`
	PageSize          int                   `json:"page_size,omitempty"`
	PageCount         int64                 `json:"page_count,omitempty"`
	FreelistCount     int64                 `json:"freelist_count,omitempty"`
	CollectionError   string                `json:"collection_error,omitempty"`
}

// TableSchema describes a single database table's structure.
type TableSchema struct {
	Name     string       `json:"name"`
	RowCount int64        `json:"row_count"`
	Columns  []ColumnInfo `json:"columns"`
	Indexes  []IndexInfo  `json:"indexes"`
}

// ColumnInfo describes a single column in a database table.
type ColumnInfo struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Nullable     bool    `json:"nullable"`
	PrimaryKey   bool    `json:"primary_key"`
	DefaultValue *string `json:"default_value"`
}

// IndexInfo describes a database index.
type IndexInfo struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// ForeignKeyViolation describes a foreign key constraint violation found during integrity checking.
type ForeignKeyViolation struct {
	Table    string `json:"table"`
	RowID    int64  `json:"row_id"`
	RefTable string `json:"referenced_table"`
	FKIndex  int    `json:"fk_index"`
}

// MigrationStateInfo captures the state of a v1-to-v2 database migration.
type MigrationStateInfo struct {
	State            string  `json:"state"`
	CurrentPhase     string  `json:"current_phase"`
	StartedAt        *string `json:"started_at,omitempty"`
	CompletedAt      *string `json:"completed_at,omitempty"`
	TotalRecords     int64   `json:"total_records"`
	MigratedRecords  int64   `json:"migrated_records"`
	ErrorMessage     string  `json:"error_message,omitempty"`
	RelatedDataError string  `json:"related_data_error,omitempty"`
	DirtyRecordCount int64   `json:"dirty_record_count,omitempty"`
}

// DeploymentInfo contains deployment context for troubleshooting path mismatches
// and configuration issues. Collected as part of support dumps.
type DeploymentInfo struct {
	WorkingDirectory   string              `json:"working_directory"`
	SystemdServiceFile string              `json:"systemd_service_file,omitempty"`
	DataDirectoryFiles []DataDirectoryFile `json:"data_directory_files,omitempty"`
	// ConfigDirectoryFiles lists the config directory contents, the
	// counterpart of DataDirectoryFiles (additive; introduced with the
	// diagnostics boot journal).
	ConfigDirectoryFiles []DataDirectoryFile `json:"config_directory_files,omitempty"`
	DockerMounts         []DockerMount       `json:"docker_mounts,omitempty"`
	CollectionErrors     []string            `json:"collection_errors,omitempty"`
}

// DataDirectoryFile describes a file in the data directory.
type DataDirectoryFile struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	IsDir    bool      `json:"is_dir"`
}

// DockerMount describes a mount point visible inside a container.
type DockerMount struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Type        string `json:"type"`
	Options     string `json:"options,omitempty"`
}

// AppEventEntry is a support-package-local representation of an application event.
// It uses plain types (no gorm dependency) for clean serialization into the support dump.
type AppEventEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Category  string         `json:"category"`
	EventType string         `json:"event_type"`
	Message   string         `json:"message"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// AppEventsProvider retrieves recent application events for inclusion in support dumps.
// Implemented by the datastore layer, consumed by the support collector.
type AppEventsProvider interface {
	GetRecentAppEvents(ctx context.Context, limit int) ([]AppEventEntry, error)
}

// RuntimeInfoProvider supplies the inference runtime state for a support dump.
// The provider lives outside this package because the state is owned by the
// inference and classifier packages. An implementation must not wait on
// inference or model loading: the dump has a deadline. It never returns nil.
type RuntimeInfoProvider interface {
	// CollectRuntimeInfo returns the current runtime state. Fields tagged
	// json:"-" carry raw values for finalizeRuntimeInfo, which converts them to
	// the shipped fields.
	CollectRuntimeInfo(ctx context.Context) *RuntimeInfo
}

// RuntimeInfo is the content of runtime_info.json: the ONNX Runtime and OpenVINO
// state, the loaded models, the range filter, and which models each audio source
// is assigned. It holds no library or model path and no hostname: raw paths live only in
// json:"-" fields and are reduced to a basename plus a fixed location class
// before the dump ships. The one path-like value is the container-side mount point of the models directory.
type RuntimeInfo struct {
	// Available is false when no running instance could be inspected, as in the
	// CLI collector.
	Available bool `json:"available"`
	// Reason says why Available is false, or which sections are missing.
	Reason string `json:"reason,omitempty"`

	ONNXRuntime *ONNXRuntimeInfo `json:"onnx_runtime,omitempty"`
	OpenVINO    *OpenVINOInfo    `json:"openvino,omitempty"`

	// OrchestratorAvailable is false when the model orchestrator could not be
	// reached, in which case Models is absent and RangeFilter holds configuration facts only.
	OrchestratorAvailable bool `json:"orchestrator_available"`
	// BirdNETThreadsSetting is the configured birdnet.threads value; 0 means auto.
	BirdNETThreadsSetting int                     `json:"birdnet_threads_setting"`
	Models                []ModelRuntimeInfo      `json:"models,omitempty"`
	RangeFilter           *RangeFilterRuntimeInfo `json:"range_filter,omitempty"`
	Sources               []SourceModelInfo       `json:"sources,omitempty"`
	ModelsDirectory       *ModelsDirectoryInfo    `json:"models_directory,omitempty"`
}

// ONNXRuntimeInfo is the ONNX Runtime state.
type ONNXRuntimeInfo struct {
	Initialized     bool   `json:"initialized"`
	Version         string `json:"version,omitempty"`
	RequiredVersion string `json:"required_version,omitempty"`
	// LibraryFile is the basename of the loaded library after symlink resolution.
	LibraryFile string `json:"library_file,omitempty"`
	// LibraryDir is a well-known system directory, "custom" for any other, and
	// empty when the library is found by the dynamic loader's search.
	LibraryDir string `json:"library_dir,omitempty"`
	// LibrarySource is "configured", "auto" or "loader_search".
	LibrarySource string `json:"library_source,omitempty"`

	// LibraryPath and LibraryConfigured are the raw values the provider fills.
	LibraryPath       string `json:"-"`
	LibraryConfigured bool   `json:"-"`
}

// OpenVINOInfo is the OpenVINO backend state.
type OpenVINOInfo struct {
	CompiledIn  bool `json:"compiled_in"`
	Initialized bool `json:"initialized"`
	Active      bool `json:"active"`
	// LibraryFile, LibraryDir and LibrarySource describe the library that is
	// really loaded, as for ONNXRuntimeInfo.
	LibraryFile   string            `json:"library_file,omitempty"`
	LibraryDir    string            `json:"library_dir,omitempty"`
	LibrarySource string            `json:"library_source,omitempty"`
	Probe         OpenVINOProbeInfo `json:"probe"`

	// LibraryPath is the raw value the provider fills; empty when no library
	// is loaded.
	LibraryPath string `json:"-"`
}

// OpenVINOProbeInfo is the cached OpenVINO device probe result.
type OpenVINOProbeInfo struct {
	// Status is "not_run", "in_flight" or "cached".
	Status  string   `json:"status"`
	Devices []string `json:"devices,omitempty"`
	// Failed is true when the cached probe failed. The error text is not shipped.
	Failed bool `json:"failed"`
}

// ModelRuntimeInfo is one loaded model.
type ModelRuntimeInfo struct {
	RegistryID string `json:"registry_id"`
	Backend    string `json:"backend"`
	Device     string `json:"device"`
	Precision  string `json:"precision,omitempty"`
	// ModelFile is the basename of the model file, or the built-in marker.
	ModelFile  string `json:"model_file"`
	LabelCount int    `json:"label_count"`
	// ConfiguredThreads is the thread count the model was built with as
	// configured, not a measured value; 0 means the backend default.
	ConfiguredThreads int       `json:"configured_threads"`
	Generation        uint64    `json:"generation"`
	LoadedAt          time.Time `json:"loaded_at"`

	// ModelPath is the raw model path the provider fills.
	ModelPath string `json:"-"`
}

// RangeFilterRuntimeInfo is the range filter state. Paths are reported only as
// whether they are set and whether the file exists.
type RangeFilterRuntimeInfo struct {
	Available            bool     `json:"available"`
	Active               bool     `json:"active"`
	FellBack             bool     `json:"fell_back"`
	Backend              string   `json:"backend,omitempty"`
	MappedSpecies        int      `json:"mapped_species"`
	Participants         []string `json:"participants,omitempty"`
	IncludedSpeciesCount int      `json:"included_species_count"`
	// Model is the configured range filter model version.
	Model                string `json:"model,omitempty"`
	ModelPathConfigured  bool   `json:"model_path_configured"`
	ModelFileExists      bool   `json:"model_file_exists"`
	LabelsPathConfigured bool   `json:"labels_path_configured"`
	LabelsFileExists     bool   `json:"labels_file_exists"`
	StockGeomodelPresent bool   `json:"stock_geomodel_present"`
}

// Kinds of SourceModelInfo.
const (
	// SourceKindAudio is an entry of audio.sources.
	SourceKindAudio = "audio_source"
	// SourceKindStream is an entry of realtime.rtsp.streams.
	SourceKindStream = "stream"
)

// SourceModelInfo is the model assignment of one configured audio source or
// stream. It is identified by kind and index in its config list; names, devices
// and URLs are never copied.
type SourceModelInfo struct {
	Kind  string `json:"kind"`
	Index int    `json:"index"`
	// Enabled is always true for audio sources, which have no enabled flag.
	Enabled bool `json:"enabled"`
	// Models are the configured model IDs that name a known model. Unknown IDs
	// are free text and are counted in UnknownModels, never shipped.
	Models []string `json:"models,omitempty"`
	// RegistryIDs are the registry IDs those resolve to.
	RegistryIDs []string `json:"registry_ids,omitempty"`
	// UnknownModels counts the configured IDs that name no known model. A source
	// whose named models are all unknown runs the first default target only.
	UnknownModels int `json:"unknown_models,omitempty"`
	// UsesDefault is true when no models are listed, so the default targets run.
	UsesDefault bool `json:"uses_default"`
}

// Sources of ModelsDirectoryInfo.Source.
const (
	// ModelsDirSourceConfigured means models.directory is set.
	ModelsDirSourceConfigured = "configured"
	// ModelsDirSourceDefault means the per-user default directory is used.
	ModelsDirSourceDefault = "default"
	// ModelsDirSourceUnavailable means no directory could be resolved.
	ModelsDirSourceUnavailable = "unavailable"
)

// PersistenceNotApplicable is reported instead of a persistence class outside
// Docker and Podman containers, where the check has no meaning.
const PersistenceNotApplicable = "not_applicable"

// ModelsDirectoryInfo describes the model directory in use. The directory is not
// named, but MountPoint can equal it.
type ModelsDirectoryInfo struct {
	Source string `json:"source"`
	Exists bool   `json:"exists"`
	// Persistence is the persistence class of the directory, "not_applicable"
	// outside containers.
	Persistence string `json:"persistence,omitempty"`
	// MountPoint and FSType describe the mount covering the directory. They are
	// container-side values; MountPoint equals the directory when it is a mount of its own.
	MountPoint string `json:"mount_point,omitempty"`
	FSType     string `json:"fs_type,omitempty"`
}
