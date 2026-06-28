// Package discovery scans the filesystem for importable BirdNET-Pi databases
// and provides per-environment setup guidance. It is a leaf package consumed by
// the api/v2 import layer; it must not import any api package.
package discovery

// Kind classifies where a candidate database lives, so the UI can label it.
type Kind string

const (
	// KindLocal is a database on a fixed local filesystem.
	KindLocal Kind = "local"
	// KindRemovable is a database on removable media (USB stick, SD card).
	KindRemovable Kind = "removable"
	// KindNetwork is a database on a network filesystem.
	KindNetwork Kind = "network"
)

const (
	// ReasonPermissionDenied means the file exists but is not readable by the
	// BirdNET-Go process user.
	ReasonPermissionDenied = "permission_denied"
	// ReasonInvalidSchema means the file opened but is not a BirdNET-Pi database.
	ReasonInvalidSchema = "invalid_schema"
	// ReasonOpenFailed means the file could not be opened as a SQLite database.
	ReasonOpenFailed = "open_failed"
)

// SourceCandidate is one discovered BirdNET-Pi database with display metadata.
type SourceCandidate struct {
	// Path is the absolute path to the birds.db file.
	Path string `json:"path"`
	// Kind is where it lives (local, removable, network).
	Kind Kind `json:"kind"`
	// DetectionCount is the number of rows in the detections table (0 if unread).
	DetectionCount int `json:"detection_count"`
	// LatestDate is the most recent detection date as "YYYY-MM-DD", or "".
	LatestDate string `json:"latest_date"`
	// AudioDirGuess is a sibling audio tree if found, else "".
	AudioDirGuess string `json:"audio_dir_guess"`
	// Size is the database file size in bytes.
	Size int64 `json:"size"`
	// Valid is true when the database opened and matched the BirdNET-Pi schema.
	Valid bool `json:"valid"`
	// Reason explains why Valid is false: one of the Reason* constants, or "".
	Reason string `json:"reason"`
	// OwnerUID is the file owner uid, or -1 if unknown.
	OwnerUID int `json:"owner_uid"`
	// OwnerName is the file owner username, or "" if unknown.
	OwnerName string `json:"owner_name"`
}
