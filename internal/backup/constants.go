// Package backup provides functionality for backing up application data
package backup

import (
	"os"
	"time"
)

// Cryptographic constants
const (
	// AES256KeySize is the key size for AES-256 encryption (32 bytes = 256 bits)
	AES256KeySize = 32

	// KeyFileMinLines is the minimum number of lines expected in a key file
	KeyFileMinLines = 3

	// KeyFileHeader is the expected header in exported key files
	KeyFileHeader = "BirdNET-Go Backup Encryption Key"
)

// File system permission constants
const (
	// PermConfigDir is the permission for configuration directories (owner only)
	PermConfigDir os.FileMode = 0o700

	// PermSecureFile is the permission for sensitive files like encryption keys (owner only)
	PermSecureFile os.FileMode = 0o600

	// PermBackupDir is the permission for backup directories (owner + group read)
	PermBackupDir os.FileMode = 0o750

	// PermBackupFile is the permission for backup files (owner + group read)
	PermBackupFile os.FileMode = 0o640

	// PermArchiveFile is the permission for archive files within tarballs
	PermArchiveFile os.FileMode = 0o644
)

// Metadata constants
const (
	// MetadataVersion is the current version of the backup metadata format
	MetadataVersion = 1
)

// Byte size constants for readable calculations
const (
	KB = 1024
	MB = KB * 1024
	GB = MB * 1024
)

// SpaceBufferMultiplier is the multiplier for calculating required disk space
// (e.g., 1.1 = 10% buffer over actual file size)
const SpaceBufferMultiplier = 1.1

// Time constants for retention period calculations
const (
	// HoursPerDay is the number of hours in a day
	HoursPerDay = 24

	// DaysPerMonth is the approximate number of days in a month
	DaysPerMonth = 30

	// DaysPerYear is the approximate number of days in a year
	DaysPerYear = 365
)

// Default timeout constants for backup operations
const (
	// DefaultBackupTimeout is the default timeout for the entire backup process
	DefaultBackupTimeout = 2 * time.Hour

	// DefaultStoreTimeout is the default timeout for storing a backup to a single target
	DefaultStoreTimeout = 30 * time.Minute

	// DefaultCleanupTimeout is the default timeout for the cleanup process
	DefaultCleanupTimeout = 1 * time.Hour

	// DefaultDeleteTimeout is the default timeout for deleting a single backup
	DefaultDeleteTimeout = 5 * time.Minute

	// DefaultOperationTimeout is the default timeout for general operations
	DefaultOperationTimeout = 15 * time.Minute

	// DefaultValidateTimeout is the default timeout for validation operations
	DefaultValidateTimeout = 30 * time.Second
)
