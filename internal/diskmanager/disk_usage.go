// disk_usage.go - Common definitions for disk usage calculations

package diskmanager

// DiskSpaceInfo holds detailed disk space information.
type DiskSpaceInfo struct {
	TotalBytes uint64
	UsedBytes  uint64
}
