package backup

import (
	"os"
)

// DefaultDirectoryPermissions returns secure default permissions for directories
// Uses PermBackupDir for better security while maintaining functionality
func DefaultDirectoryPermissions() os.FileMode {
	return PermBackupDir
}

// DefaultFilePermissions returns secure default permissions for files
func DefaultFilePermissions() os.FileMode {
	return PermBackupFile
}
