// Package datastore provides resource monitoring for database operations
package datastore

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Disk space requirements for different operations (in MB)
const (
	MinDiskSpaceVacuum   = 500 // VACUUM can temporarily double database size
	MinDiskSpaceBackup   = 200 // Backup operations need significant space
	MinDiskSpaceBulk     = 100 // Bulk operations need extra space
	MinDiskSpaceDefault  = 50  // Default minimum for normal operations
)

// Mount info cache for performance
type mountInfoCache struct {
	info   *MountInfo
	path   string
	expiry time.Time
}

var (
	mountCache     *mountInfoCache
	mountCacheLock sync.RWMutex
	mountCacheTTL  = 5 * time.Minute // Cache mount info for 5 minutes
)

// Operation disk requirements map
var operationDiskRequirements = map[string]uint64{
	"vacuum":      MinDiskSpaceVacuum,
	"optimize":    MinDiskSpaceVacuum,
	"backup":      MinDiskSpaceBackup,
	"migration":   MinDiskSpaceBackup,
	"bulk_insert": MinDiskSpaceBulk,
	"import":      MinDiskSpaceBulk,
}

// ResourceSnapshot captures system resources at a point in time
type ResourceSnapshot struct {
	Timestamp        time.Time         `json:"timestamp"`
	DiskSpace        DiskSpaceInfo     `json:"disk_space"`
	DatabaseFile     DatabaseFileInfo  `json:"database_file"`
	SystemMemory     MemoryInfo        `json:"system_memory"`
	ProcessInfo      ProcessInfo       `json:"process_info"`
	DatabaseMetrics  DatabaseMetrics   `json:"database_metrics,omitempty"`
}

// DiskSpaceInfo contains disk space information for the database partition
type DiskSpaceInfo struct {
	MountPoint      string  `json:"mount_point"`
	TotalBytes      uint64  `json:"total_bytes"`
	AvailableBytes  uint64  `json:"available_bytes"`
	UsedBytes       uint64  `json:"used_bytes"`
	UsedPercent     float64 `json:"used_percent"`
	InodesFree      uint64  `json:"inodes_free,omitempty"`
	InodesTotal     uint64  `json:"inodes_total,omitempty"`
	FileSystemType  string  `json:"filesystem_type,omitempty"`
}

// DatabaseFileInfo contains information about database files
type DatabaseFileInfo struct {
	Path            string    `json:"path"`
	SizeBytes       int64     `json:"size_bytes"`
	Permissions     string    `json:"permissions"`
	LastModified    time.Time `json:"last_modified"`
	JournalExists   bool      `json:"journal_exists"`
	JournalSize     int64     `json:"journal_size"`
	WALExists       bool      `json:"wal_exists"`
	WALSize         int64     `json:"wal_size"`
	SHMExists       bool      `json:"shm_exists"`
	SHMSize         int64     `json:"shm_size"`
}

// MemoryInfo contains system memory information
type MemoryInfo struct {
	TotalBytes      uint64  `json:"total_bytes"`
	AvailableBytes  uint64  `json:"available_bytes"`
	UsedBytes       uint64  `json:"used_bytes"`
	UsedPercent     float64 `json:"used_percent"`
	SwapTotalBytes  uint64  `json:"swap_total_bytes,omitempty"`
	SwapUsedBytes   uint64  `json:"swap_used_bytes,omitempty"`
}

// ProcessInfo contains current process resource usage
type ProcessInfo struct {
	PID                int     `json:"pid"`
	ResidentMemoryMB   int64   `json:"resident_memory_mb"`
	VirtualMemoryMB    int64   `json:"virtual_memory_mb"`
	CPUUsagePercent    float64 `json:"cpu_usage_percent,omitempty"`
	GoroutineCount     int     `json:"goroutine_count"`
	HeapAllocMB        int64   `json:"heap_alloc_mb"`
	HeapSysMB          int64   `json:"heap_sys_mb"`
}

// DatabaseMetrics contains database-specific metrics
type DatabaseMetrics struct {
	ConnectionsActive int     `json:"connections_active,omitempty"`
	ConnectionsIdle   int     `json:"connections_idle,omitempty"`
	QueriesPerSecond  float64 `json:"queries_per_second,omitempty"`
	TableCount        int     `json:"table_count,omitempty"`
	IndexCount        int     `json:"index_count,omitempty"`
}

// CaptureResourceSnapshot creates a complete snapshot of system resources
func CaptureResourceSnapshot(dbPath string) (*ResourceSnapshot, error) {
	snapshot := &ResourceSnapshot{
		Timestamp: time.Now(),
	}

	// Capture disk space information using diskmanager
	if diskInfo, err := captureDiskSpaceWithManager(dbPath); err == nil {
		snapshot.DiskSpace = diskInfo
	} else {
		getLogger().Warn("Failed to capture disk space info", "error", err)
	}

	// Capture database file information
	if dbInfo, err := captureDatabaseFileInfo(dbPath); err == nil {
		snapshot.DatabaseFile = dbInfo
	} else {
		getLogger().Warn("Failed to capture database file info", "error", err)
	}

	// Capture system memory information
	if memInfo, err := captureMemoryInfo(); err == nil {
		snapshot.SystemMemory = memInfo
	} else {
		getLogger().Warn("Failed to capture memory info", "error", err)
	}

	// Capture process information
	if procInfo, err := captureProcessInfo(); err == nil {
		snapshot.ProcessInfo = procInfo
	} else {
		getLogger().Warn("Failed to capture process info", "error", err)
	}

	return snapshot, nil
}

// captureDiskSpaceWithManager gathers disk space information using diskmanager package
func captureDiskSpaceWithManager(dbPath string) (DiskSpaceInfo, error) {
	info := DiskSpaceInfo{}
	
	// Get directory containing the database file
	dir := filepath.Dir(dbPath)
	
	// Use diskmanager for core disk space data
	diskData, err := diskmanager.GetDetailedDiskUsage(dir)
	if err != nil {
		return info, errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "get_disk_usage").
			Context("path", dir).
			Build()
	}
	
	// Get available space using diskmanager  
	availableBytes, err := diskmanager.GetAvailableSpace(dir)
	if err != nil {
		return info, errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "get_available_space").
			Context("path", dir).
			Build()
	}
	
	// Populate core disk data from diskmanager
	info.TotalBytes = diskData.TotalBytes
	info.UsedBytes = diskData.UsedBytes
	info.AvailableBytes = availableBytes
	
	// Calculate usage percentage
	if info.TotalBytes > 0 {
		info.UsedPercent = float64(info.UsedBytes) / float64(info.TotalBytes) * 100.0
	}
	
	// Add our value-add fields: mount point, filesystem type, inodes
	if mountInfo, err := getMountInfo(dir); err == nil {
		info.MountPoint = mountInfo.MountPoint
		info.FileSystemType = mountInfo.FileSystemType
	} else {
		// Fallback to directory if mount info not available
		info.MountPoint = dir
		info.FileSystemType = "unknown"
	}
	
	// Add inode information (Unix-specific, will be 0 on Windows)
	if inodeInfo, err := getInodeInfo(dir); err == nil {
		info.InodesFree = inodeInfo.Free
		info.InodesTotal = inodeInfo.Total
	}
	
	return info, nil
}

// captureDatabaseFileInfo gathers information about database files
func captureDatabaseFileInfo(dbPath string) (DatabaseFileInfo, error) {
	info := DatabaseFileInfo{
		Path: dbPath,
	}

	// Get main database file info
	if stat, err := os.Stat(dbPath); err == nil {
		info.SizeBytes = stat.Size()
		info.LastModified = stat.ModTime()
		info.Permissions = stat.Mode().String()
	} else if !os.IsNotExist(err) {
		return info, errors.New(err).
			Component("datastore").
			Category(errors.CategoryFileIO).
			Context("operation", "stat_database_file").
			Context("db_path", dbPath).
			Build()
	}

	// Check for SQLite auxiliary files
	info.JournalExists, info.JournalSize = checkAuxiliaryFile(dbPath + "-journal")
	info.WALExists, info.WALSize = checkAuxiliaryFile(dbPath + "-wal")
	info.SHMExists, info.SHMSize = checkAuxiliaryFile(dbPath + "-shm")

	return info, nil
}

// checkAuxiliaryFile checks if an auxiliary database file exists and returns its size
func checkAuxiliaryFile(path string) (exists bool, size int64) {
	if stat, err := os.Stat(path); err == nil {
		return true, stat.Size()
	}
	return false, 0
}

// captureProcessInfo gathers current process resource usage
func captureProcessInfo() (ProcessInfo, error) {
	info := ProcessInfo{
		PID:            os.Getpid(),
		GoroutineCount: runtime.NumGoroutine(),
	}

	// Capture Go runtime memory stats
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	info.HeapAllocMB = int64(memStats.HeapAlloc / 1024 / 1024)
	info.HeapSysMB = int64(memStats.HeapSys / 1024 / 1024)

	// Platform-specific process memory info will be implemented per OS
	if procMem, err := getProcessMemoryUsage(); err == nil {
		info.ResidentMemoryMB = procMem.ResidentMB
		info.VirtualMemoryMB = procMem.VirtualMB
	}

	return info, nil
}

// ProcessMemoryUsage represents process memory usage
type ProcessMemoryUsage struct {
	ResidentMB int64
	VirtualMB  int64
}

// ValidateResourceAvailability checks if system resources are sufficient for operations
func ValidateResourceAvailability(dbPath, operation string) error {
	snapshot, err := CaptureResourceSnapshot(dbPath)
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "resource_validation").
			Build()
	}

	// Check disk space requirements based on operation
	minFreeMB := getMinimumDiskSpaceForOperation(operation)
	freeSpaceMB := snapshot.DiskSpace.AvailableBytes / 1024 / 1024

	if freeSpaceMB < minFreeMB {
		return errors.Newf("insufficient disk space for operation '%s': %dMB free (minimum: %dMB required)", 
			operation, freeSpaceMB, minFreeMB).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", operation).
			Context("disk_free_mb", freeSpaceMB).
			Context("disk_required_mb", minFreeMB).
			Context("disk_total_mb", snapshot.DiskSpace.TotalBytes/1024/1024).
			Context("mount_point", snapshot.DiskSpace.MountPoint).
			Build()
	}

	// Check inode availability (important for databases with many files)
	if snapshot.DiskSpace.InodesFree > 0 && snapshot.DiskSpace.InodesFree < 1000 {
		return errors.Newf("insufficient inodes available: %d free (minimum: 1000)", 
			snapshot.DiskSpace.InodesFree).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", operation).
			Context("inodes_free", snapshot.DiskSpace.InodesFree).
			Context("inodes_total", snapshot.DiskSpace.InodesTotal).
			Build()
	}

	// Check memory availability for memory-intensive operations
	if isMemoryIntensiveOperation(operation) {
		availableMemoryMB := snapshot.SystemMemory.AvailableBytes / 1024 / 1024
		if availableMemoryMB < 100 { // Require at least 100MB free
			return errors.Newf("insufficient memory for operation '%s': %dMB available (minimum: 100MB)", 
				operation, availableMemoryMB).
				Component("datastore").
				Category(errors.CategorySystem).
				Context("operation", operation).
				Context("memory_available_mb", availableMemoryMB).
				Build()
		}
	}

	return nil
}

// getMinimumDiskSpaceForOperation returns minimum disk space required for different operations
func getMinimumDiskSpaceForOperation(operation string) uint64 {
	op := strings.ToLower(operation)
	if minSpace, ok := operationDiskRequirements[op]; ok {
		return minSpace
	}
	return MinDiskSpaceDefault
}

// isMemoryIntensiveOperation checks if an operation requires significant memory
func isMemoryIntensiveOperation(operation string) bool {
	memoryIntensiveOps := []string{
		"vacuum", "optimize", "bulk_insert", "migration", 
		"analytics", "search", "export",
	}
	
	for _, op := range memoryIntensiveOps {
		if strings.Contains(strings.ToLower(operation), op) {
			return true
		}
	}
	return false
}

// FormatResourceSummary creates a human-readable summary of resource usage
func (r *ResourceSnapshot) FormatResourceSummary() string {
	return fmt.Sprintf("Resources: Disk=%dMB free (%.1f%% used), Memory=%dMB free, DB=%dMB, Process=%dMB heap", 
		r.DiskSpace.AvailableBytes/1024/1024,
		r.DiskSpace.UsedPercent,
		r.SystemMemory.AvailableBytes/1024/1024,
		r.DatabaseFile.SizeBytes/1024/1024,
		r.ProcessInfo.HeapAllocMB,
	)
}

// IsCriticalResourceState checks if system resources are in a critical state
func (r *ResourceSnapshot) IsCriticalResourceState() bool {
	// Critical if less than 50MB disk space or over 95% usage
	if r.DiskSpace.AvailableBytes < 50*1024*1024 || r.DiskSpace.UsedPercent > 95.0 {
		return true
	}
	
	// Critical if less than 100MB system memory available
	if r.SystemMemory.AvailableBytes < 100*1024*1024 {
		return true
	}
	
	// Critical if process heap is over 500MB (potential memory leak)
	if r.ProcessInfo.HeapAllocMB > 500 {
		return true
	}
	
	return false
}

// GetResourceRecommendations returns actionable recommendations based on resource state
func (r *ResourceSnapshot) GetResourceRecommendations() []string {
	var recommendations []string
	
	const criticalDiskUsagePercent = 90.0
	const largeWALSizeMB = 50
	const criticalHeapAllocMB = 200
	const criticalGoroutineCount = 1000

	if r.DiskSpace.UsedPercent > criticalDiskUsagePercent {
		recommendations = append(recommendations, 
			fmt.Sprintf("Disk space critically low: %.1f%% used (%dMB free)", 
				r.DiskSpace.UsedPercent, r.DiskSpace.AvailableBytes/1024/1024))
	}
	
	if r.DatabaseFile.WALExists && r.DatabaseFile.WALSize > largeWALSizeMB*1024*1024 {
		recommendations = append(recommendations, 
			fmt.Sprintf("Large WAL file detected: %dMB - consider checkpoint", 
				r.DatabaseFile.WALSize/1024/1024))
	}
	
	if r.ProcessInfo.HeapAllocMB > criticalHeapAllocMB {
		recommendations = append(recommendations, 
			fmt.Sprintf("High process memory usage: %dMB heap allocated", 
				r.ProcessInfo.HeapAllocMB))
	}
	
	if r.ProcessInfo.GoroutineCount > criticalGoroutineCount {
		recommendations = append(recommendations, 
			fmt.Sprintf("High goroutine count: %d active", 
				r.ProcessInfo.GoroutineCount))
	}
	
	if len(recommendations) == 0 {
		recommendations = append(recommendations, "System resources are healthy")
	}
	
	return recommendations
}

// MountInfo represents mount point information
type MountInfo struct {
	MountPoint     string
	FileSystemType string
}

// InodeInfo represents inode information
type InodeInfo struct {
	Free  uint64
	Total uint64
}

// getMountInfo gets mount point information with caching
func getMountInfo(path string) (*MountInfo, error) {
	// Check cache first
	mountCacheLock.RLock()
	if mountCache != nil && mountCache.path == path && time.Now().Before(mountCache.expiry) {
		info := mountCache.info
		mountCacheLock.RUnlock()
		return info, nil
	}
	mountCacheLock.RUnlock()

	// Get fresh mount info
	info, err := getMountInfoPlatform(path)
	if err != nil {
		return nil, err
	}

	// Update cache
	mountCacheLock.Lock()
	mountCache = &mountInfoCache{
		info:   info,
		path:   path,
		expiry: time.Now().Add(mountCacheTTL),
	}
	mountCacheLock.Unlock()

	return info, nil
}

// getInodeInfo gets inode information (Unix-specific, returns zeros on Windows)
func getInodeInfo(path string) (*InodeInfo, error) {
	// Platform-specific implementation - will be implemented in platform files
	return getInodeInfoPlatform(path)
}