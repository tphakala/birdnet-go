package diagnostics

import "time"

// SchemaVersion is the current journal record schema version, embedded in
// every record so future readers can interpret older journals.
const SchemaVersion = 1

// Record type constants: the value of the "type" field on each journal line.
const (
	// RecordTypeBoot is the full per-startup snapshot record.
	RecordTypeBoot = "boot"
	// RecordTypeDBFreshCreated marks creation of a fresh (empty) database.
	RecordTypeDBFreshCreated = "db_fresh_created"
	// RecordTypeMigration marks a migration state transition.
	RecordTypeMigration = "migration"
	// RecordTypeConsolidation marks a database consolidation attempt.
	RecordTypeConsolidation = "consolidation"
	// RecordTypeConfigDefaulted marks generation of a default config file.
	RecordTypeConfigDefaulted = "config_defaulted"
	// RecordTypeShutdown marks application shutdown.
	RecordTypeShutdown = "shutdown"
	// RecordTypeAnomaly marks a detected boot-over-boot anomaly.
	RecordTypeAnomaly = "anomaly"
)

// RecordHeader is embedded in every journal record.
type RecordHeader struct {
	// SchemaVersion is the journal schema version (see SchemaVersion).
	SchemaVersion int `json:"schema_version"`
	// Type discriminates the record (see RecordType* constants).
	Type string `json:"type"`
	// Timestamp is the record creation time (RFC3339 with timezone via
	// standard time.Time JSON marshaling).
	Timestamp time.Time `json:"timestamp"`
}

// NewRecordHeader returns a header for the given record type stamped now.
func NewRecordHeader(recordType string) RecordHeader {
	return RecordHeader{
		SchemaVersion: SchemaVersion,
		Type:          recordType,
		Timestamp:     time.Now(),
	}
}

// AppInfo describes the running application build.
type AppInfo struct {
	Version   string `json:"version,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
	Commit    string `json:"commit,omitempty"`
}

// RuntimeInfo describes the process runtime environment.
type RuntimeInfo struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`
	Container bool   `json:"container"`
	PID       int    `json:"pid"`
}

// ConfigInfo describes the active configuration file.
type ConfigInfo struct {
	Path      string `json:"config_path,omitempty"`
	Existed   bool   `json:"config_existed"`
	Defaulted bool   `json:"config_defaulted"`
}

// DBFileInfo describes one database-related file discovered on disk.
type DBFileInfo struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// DatastoreSnapshot captures datastore path facts at boot.
type DatastoreSnapshot struct {
	Dialect          string       `json:"dialect"`
	ConfiguredPath   string       `json:"configured_path,omitempty"`
	ResolvedAbsPath  string       `json:"resolved_abs_path,omitempty"`
	ConfiguredExists bool         `json:"configured_exists"`
	ConfiguredSize   int64        `json:"configured_size"`
	V2SidecarPath    string       `json:"v2_sidecar_path,omitempty"`
	V2SidecarExists  bool         `json:"v2_sidecar_exists"`
	V2SidecarSize    int64        `json:"v2_sidecar_size"`
	StartupDecision  string       `json:"startup_decision,omitempty"`
	MigrationState   string       `json:"migration_state,omitempty"`
	DBFilesFound     []DBFileInfo `json:"db_files_found,omitempty"`
}

// Mount describes one mount point visible to the process (raw, un-anonymized).
type Mount struct {
	Source      string `json:"source"`
	Destination string `json:"dest"`
	FSType      string `json:"fstype"`
	Options     string `json:"options,omitempty"`
}

// DirEntryInfo describes one entry of a directory listing (raw name).
type DirEntryInfo struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	IsDir    bool      `json:"is_dir"`
}

// DiskUsage describes free/total space for one path's filesystem.
type DiskUsage struct {
	Path       string `json:"path"`
	FreeBytes  uint64 `json:"free_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
}

// BootRecord is the full snapshot written once per startup.
type BootRecord struct {
	RecordHeader
	App            AppInfo           `json:"app"`
	Runtime        RuntimeInfo       `json:"runtime"`
	Config         ConfigInfo        `json:"config"`
	Datastore      DatastoreSnapshot `json:"datastore"`
	Mounts         []Mount           `json:"mounts,omitempty"`
	DataDirFiles   []DirEntryInfo    `json:"data_dir_files,omitempty"`
	ConfigDirFiles []DirEntryInfo    `json:"config_dir_files,omitempty"`
	Disk           []DiskUsage       `json:"disk,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Errors         []string          `json:"collection_errors,omitempty"`
}

// FreshDBRecord marks creation of a fresh empty database.
type FreshDBRecord struct {
	RecordHeader
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// MigrationRecord marks a migration state transition.
type MigrationRecord struct {
	RecordHeader
	Phase   string `json:"phase"`
	From    string `json:"from"`
	To      string `json:"to"`
	Records int64  `json:"records,omitempty"`
}

// ConsolidationRecord marks a database consolidation attempt.
type ConsolidationRecord struct {
	RecordHeader
	From       string `json:"from"`
	To         string `json:"to"`
	BackupPath string `json:"backup_path,omitempty"`
	Result     string `json:"result"`
}

// ConfigDefaultedRecord marks generation of a default configuration file.
type ConfigDefaultedRecord struct {
	RecordHeader
	Path string `json:"path,omitempty"`
}

// ShutdownRecord marks application shutdown.
type ShutdownRecord struct {
	RecordHeader
	Clean         bool  `json:"clean"`
	UptimeSeconds int64 `json:"uptime_seconds"`
}

// AnomalyRecord marks a boot-over-boot anomaly. Previous and Current carry
// the values that triggered the detection, formatted per anomaly kind.
type AnomalyRecord struct {
	RecordHeader
	Kind     string `json:"kind"`
	Previous string `json:"previous"`
	Current  string `json:"current"`
	Message  string `json:"message"`
}
