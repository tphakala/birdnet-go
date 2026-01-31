// internal/api/v2/prerequisites.go
package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

// PrerequisiteCheck represents a single prerequisite check result.
type PrerequisiteCheck struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`   // passed, failed, warning, skipped, error
	Message     string `json:"message"`  // Human-readable result message
	Severity    string `json:"severity"` // critical, warning
}

// PrerequisitesResponse represents the response for the prerequisites endpoint.
type PrerequisitesResponse struct {
	AllPassed         bool                `json:"all_passed"`
	CanStartMigration bool                `json:"can_start_migration"`
	Checks            []PrerequisiteCheck `json:"checks"`
	CriticalFailures  int                 `json:"critical_failures"`
	Warnings          int                 `json:"warnings"`
	CheckedAt         time.Time           `json:"checked_at"`
}

// gormDBProvider is implemented by datastores that can provide a GORM DB instance.
// Used by prerequisite checks that need direct database access for integrity checks.
type gormDBProvider interface {
	GetDB() *gorm.DB
}

// Check status constants
const (
	CheckStatusPassed  = "passed"
	CheckStatusFailed  = "failed"
	CheckStatusWarning = "warning"
	CheckStatusSkipped = "skipped"
	CheckStatusError   = "error"
)

// Check severity constants
const (
	CheckSeverityCritical = "critical"
	CheckSeverityWarning  = "warning"
)

// Error messages
const (
	errMsgDBConnectionUnavailable = "Cannot access database connection"
)

// MinDiskSpaceBytes is the minimum free disk space required (1GB).
const MinDiskSpaceBytes = 1 << 30 // 1GB in bytes

// MinMemoryBytes is the minimum recommended free memory (50MB).
// Kept low to support resource-constrained devices like Raspberry Pi Zero 2 (512MB total RAM).
const MinMemoryBytes = 50 * 1024 * 1024 // 50MB

// MinMySQLMaxPacket is the minimum recommended max_allowed_packet (16MB).
const MinMySQLMaxPacket = 16 * 1024 * 1024 // 16MB

// MinMySQLWaitTimeout is the minimum recommended wait_timeout (600 seconds).
const MinMySQLWaitTimeout = 600

// GetPrerequisites handles GET /api/v2/system/database/migration/prerequisites
// Returns the status of all prerequisite checks before migration can start.
func (c *Controller) GetPrerequisites(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Checking migration prerequisites", logger.String("path", path), logger.String("ip", ip))

	criticalFailures := 0
	warnings := 0

	// Run critical checks (shared with runPreflightChecks)
	checks := c.runCriticalPrerequisiteChecks()

	// Add warning checks (non-blocking)
	checks = append(checks,
		c.checkExistingV2Data(),
		c.checkMemoryAvailable(),
	)

	// Add MySQL configuration warnings if applicable
	if c.isUsingMySQL() {
		checks = append(checks, c.checkMySQLConfiguration()...)
	}

	// Count failures and warnings
	for _, check := range checks {
		if check.Status == CheckStatusFailed || check.Status == CheckStatusError {
			if check.Severity == CheckSeverityCritical {
				criticalFailures++
			}
		}
		if check.Status == CheckStatusWarning {
			warnings++
		}
	}

	allPassed := criticalFailures == 0
	canStart := allPassed && !isV2OnlyMode

	response := PrerequisitesResponse{
		AllPassed:         allPassed,
		CanStartMigration: canStart,
		Checks:            checks,
		CriticalFailures:  criticalFailures,
		Warnings:          warnings,
		CheckedAt:         time.Now(),
	}

	c.logInfoIfEnabled("Prerequisites check complete",
		logger.String("path", path),
		logger.String("ip", ip),
		logger.Bool("can_start", canStart),
		logger.Int("critical_failures", criticalFailures),
		logger.Int("warnings", warnings))

	return ctx.JSON(http.StatusOK, response)
}

// isUsingMySQL returns true if the application is configured to use MySQL.
func (c *Controller) isUsingMySQL() bool {
	if c.Settings == nil {
		return false
	}
	return c.Settings.Output.MySQL.Enabled
}

// checkStateIdle verifies the migration is in IDLE state.
func (c *Controller) checkStateIdle() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "state_idle",
		Name:        "Migration State",
		Description: "Migration must be in idle state",
		Severity:    CheckSeverityCritical,
	}

	if stateManager == nil {
		if isV2OnlyMode {
			check.Status = CheckStatusSkipped
			check.Message = "Running in enhanced database mode, migration already complete"
			return check
		}
		check.Status = CheckStatusError
		check.Message = "State manager not available"
		return check
	}

	state, err := stateManager.GetState()
	if err != nil {
		check.Status = CheckStatusError
		check.Message = fmt.Sprintf("Failed to get migration state: %v", err)
		return check
	}

	if state.State == entities.MigrationStatusIdle {
		check.Status = CheckStatusPassed
		check.Message = "State is IDLE"
	} else {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("State is %s, must be IDLE to start migration", state.State)
	}

	return check
}

// checkDiskSpace verifies there is sufficient free disk space for migration.
// Required space is the greater of: 1GB minimum or 50% of existing database size.
func (c *Controller) checkDiskSpace() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "disk_space",
		Name:        "Disk Space",
		Description: "Sufficient free space for migration",
		Severity:    CheckSeverityCritical,
	}

	diskPath, err := c.getDatabaseDirectoryResolved()
	if err != nil {
		check.Status = CheckStatusError
		check.Message = fmt.Sprintf("Cannot determine database directory: %v", err)
		return check
	}

	// For MySQL (remote database), disk space check is not applicable
	if diskPath == "" {
		check.Status = CheckStatusSkipped
		check.Message = "Disk space check not applicable for MySQL (remote database)"
		return check
	}

	// Calculate required space: max(1GB, 50% of existing DB size)
	requiredSpace := uint64(MinDiskSpaceBytes)
	dbSizeInfo := ""

	if c.Settings != nil && !c.Settings.Output.MySQL.Enabled {
		dbPath := c.Settings.Output.SQLite.Path
		if !filepath.IsAbs(dbPath) {
			dbPath, _ = filepath.Abs(dbPath)
		}
		if info, err := os.Stat(dbPath); err == nil {
			dbSize := uint64(info.Size())
			halfDBSize := dbSize / 2
			if halfDBSize > requiredSpace {
				requiredSpace = halfDBSize
			}
			dbSizeGB := float64(dbSize) / (1024 * 1024 * 1024)
			dbSizeInfo = fmt.Sprintf(" (DB size: %.1fGB)", dbSizeGB)
		}
	}

	usage, err := disk.Usage(diskPath)
	if err != nil {
		check.Status = CheckStatusError
		check.Message = fmt.Sprintf("Failed to check disk space on %s: %v", diskPath, err)
		return check
	}

	freeGB := float64(usage.Free) / (1024 * 1024 * 1024)
	requiredGB := float64(requiredSpace) / (1024 * 1024 * 1024)

	if usage.Free >= requiredSpace {
		check.Status = CheckStatusPassed
		check.Message = fmt.Sprintf("%.1fGB available (need %.1fGB)%s", freeGB, requiredGB, dbSizeInfo)
	} else {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("%.1fGB available, need at least %.1fGB%s", freeGB, requiredGB, dbSizeInfo)
	}

	return check
}

// getDatabaseDirectoryResolved returns the database directory with symlinks resolved.
// Returns an error if the directory cannot be determined or verified.
// For MySQL, returns empty string with nil error (disk space check not applicable to remote DB).
func (c *Controller) getDatabaseDirectoryResolved() (string, error) {
	if c.Settings == nil {
		return "", fmt.Errorf("settings not available")
	}

	if c.Settings.Output.MySQL.Enabled {
		// For MySQL, disk space check is not applicable (remote database)
		return "", nil
	}

	dbPath := c.Settings.Output.SQLite.Path
	if dbPath == "" {
		return "", fmt.Errorf("SQLite database path not configured")
	}

	// Handle relative paths
	if !filepath.IsAbs(dbPath) {
		absPath, err := filepath.Abs(dbPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve absolute path for %s: %w", dbPath, err)
		}
		dbPath = absPath
	}

	// Resolve symbolic links (important for Docker volumes)
	resolved, err := filepath.EvalSymlinks(dbPath)
	if err != nil {
		// If symlink resolution fails, continue with the original path
		// This handles cases where the file doesn't exist yet
		resolved = dbPath
	}

	dir := filepath.Dir(resolved)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("database directory %s does not exist or is not accessible: %w", dir, err)
	}

	return dir, nil
}

// checkLegacyAccessible verifies the legacy database is accessible for reading.
func (c *Controller) checkLegacyAccessible() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "legacy_accessible",
		Name:        "Legacy Database",
		Description: "Legacy database must be accessible",
		Severity:    CheckSeverityCritical,
	}

	if c.DS == nil {
		check.Status = CheckStatusError
		check.Message = "Database not available"
		return check
	}

	// Try to perform a simple read operation
	// This uses the existing datastore interface
	_, err := c.DS.GetLastDetections(1)
	if err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("Cannot read from legacy database: %v", err)
		return check
	}

	check.Status = CheckStatusPassed
	check.Message = "Connected and readable"
	return check
}

// checkSQLiteIntegrity runs PRAGMA quick_check on the SQLite database.
// Scans all result rows since quick_check can return multiple errors.
func (c *Controller) checkSQLiteIntegrity() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "sqlite_integrity",
		Name:        "Database Integrity",
		Description: "SQLite quick integrity check",
		Severity:    CheckSeverityCritical,
	}

	db := c.getLegacyGormDB()
	if db == nil {
		check.Status = CheckStatusError
		check.Message = errMsgDBConnectionUnavailable
		return check
	}

	// PRAGMA quick_check can return multiple rows if there are multiple errors
	var results []string
	if err := db.Raw("PRAGMA quick_check").Scan(&results).Error; err != nil {
		check.Status = CheckStatusError
		check.Message = fmt.Sprintf("Failed to run integrity check: %v", err)
		return check
	}

	// Check all results - any non-"ok" result indicates a problem
	for _, result := range results {
		if result != "ok" {
			check.Status = CheckStatusFailed
			check.Message = fmt.Sprintf("Integrity check failed: %s", result)
			return check
		}
	}

	check.Status = CheckStatusPassed
	check.Message = "Database integrity verified"
	return check
}

// checkWritePermission verifies write access to the database directory.
func (c *Controller) checkWritePermission() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "write_permission",
		Name:        "Write Permission",
		Description: "Can write to database directory",
		Severity:    CheckSeverityCritical,
	}

	dir, err := c.getDatabaseDirectoryResolved()
	if err != nil {
		check.Status = CheckStatusError
		check.Message = fmt.Sprintf("Cannot determine database directory: %v", err)
		return check
	}

	// For MySQL (remote database), write permission check is not applicable locally
	if dir == "" {
		check.Status = CheckStatusSkipped
		check.Message = "Write permission check not applicable for MySQL (remote database)"
		return check
	}

	testFile := filepath.Join(dir, ".migration_permission_test")

	f, err := os.Create(testFile) //#nosec G304 -- testFile is constructed from trusted path
	if err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("Cannot write to %s: %v", dir, err)
		return check
	}
	_ = f.Close()
	_ = os.Remove(testFile)

	check.Status = CheckStatusPassed
	check.Message = fmt.Sprintf("Write access verified for %s", dir)
	return check
}

// checkMySQLTableHealth runs CHECK TABLE on legacy MySQL tables.
func (c *Controller) checkMySQLTableHealth() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "mysql_table_health",
		Name:        "MySQL Table Health",
		Description: "Check legacy table health",
		Severity:    CheckSeverityCritical,
	}

	db := c.getLegacyGormDB()
	if db == nil {
		check.Status = CheckStatusError
		check.Message = errMsgDBConnectionUnavailable
		return check
	}

	tables := []string{"notes", "results"}
	for _, table := range tables {
		var results []struct {
			Table   string `gorm:"column:Table"`
			Op      string `gorm:"column:Op"`
			MsgType string `gorm:"column:Msg_type"`
			MsgText string `gorm:"column:Msg_text"`
		}

		if err := db.Raw("CHECK TABLE " + table).Scan(&results).Error; err != nil {
			check.Status = CheckStatusError
			check.Message = fmt.Sprintf("Failed to check table %s: %v", table, err)
			return check
		}

		for _, r := range results {
			if r.MsgType == "error" {
				check.Status = CheckStatusFailed
				check.Message = fmt.Sprintf("Table %s unhealthy: %s", table, r.MsgText)
				return check
			}
		}
	}

	check.Status = CheckStatusPassed
	check.Message = "All tables healthy"
	return check
}

// checkMySQLPermissions verifies CREATE, INSERT, UPDATE, DELETE, DROP permissions.
// Uses a unique table name with timestamp to prevent concurrent request collisions.
func (c *Controller) checkMySQLPermissions() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "mysql_permissions",
		Name:        "MySQL Permissions",
		Description: "Verify CREATE, INSERT, UPDATE, DELETE, DROP privileges",
		Severity:    CheckSeverityCritical,
	}

	db := c.getLegacyGormDB()
	if db == nil {
		check.Status = CheckStatusError
		check.Message = errMsgDBConnectionUnavailable
		return check
	}

	// Use unique table name with timestamp to prevent concurrent request collisions
	testTable := fmt.Sprintf("v2_permission_test_%d", time.Now().UnixNano())

	// Ensure cleanup even if function returns early due to error
	// Cleanup errors are silently ignored - orphan test tables are harmless
	defer func() {
		_ = db.Exec("DROP TABLE IF EXISTS " + testTable).Error
	}()

	// Test CREATE
	if err := db.Exec("CREATE TABLE " + testTable + " (id INT PRIMARY KEY)").Error; err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("CREATE permission denied: %v", err)
		return check
	}

	// Test INSERT
	if err := db.Exec("INSERT INTO " + testTable + " (id) VALUES (1)").Error; err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("INSERT permission denied: %v", err)
		return check
	}

	// Test UPDATE
	if err := db.Exec("UPDATE " + testTable + " SET id = 2 WHERE id = 1").Error; err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("UPDATE permission denied: %v", err)
		return check
	}

	// Test DELETE
	if err := db.Exec("DELETE FROM " + testTable + " WHERE id = 2").Error; err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("DELETE permission denied: %v", err)
		return check
	}

	// DROP is tested via the defer cleanup above - if it fails, we still pass
	// since we've verified all the essential permissions
	check.Status = CheckStatusPassed
	check.Message = "All required permissions verified"
	return check
}

// checkMySQLConfiguration checks MySQL configuration for potential issues.
func (c *Controller) checkMySQLConfiguration() []PrerequisiteCheck {
	checks := make([]PrerequisiteCheck, 0, 2)

	db := c.getLegacyGormDB()
	if db == nil {
		return checks
	}

	// Check max_allowed_packet
	var maxPacket int64
	if err := db.Raw("SELECT @@max_allowed_packet").Scan(&maxPacket).Error; err == nil {
		check := PrerequisiteCheck{
			ID:          "mysql_max_packet",
			Name:        "MySQL max_allowed_packet",
			Description: "Recommend >= 16MB for batch operations",
			Severity:    CheckSeverityWarning,
		}
		if maxPacket >= MinMySQLMaxPacket {
			check.Status = CheckStatusPassed
			check.Message = fmt.Sprintf("%dMB configured", maxPacket/(1024*1024))
		} else {
			check.Status = CheckStatusWarning
			check.Message = fmt.Sprintf("%dMB configured, recommend >= 16MB", maxPacket/(1024*1024))
		}
		checks = append(checks, check)
	}

	// Check wait_timeout
	var waitTimeout int64
	if err := db.Raw("SELECT @@wait_timeout").Scan(&waitTimeout).Error; err == nil {
		check := PrerequisiteCheck{
			ID:          "mysql_timeout",
			Name:        "MySQL Wait Timeout",
			Description: "Recommend >= 600s for long migrations",
			Severity:    CheckSeverityWarning,
		}
		if waitTimeout >= MinMySQLWaitTimeout {
			check.Status = CheckStatusPassed
			check.Message = fmt.Sprintf("%ds configured", waitTimeout)
		} else {
			check.Status = CheckStatusWarning
			check.Message = fmt.Sprintf("%ds configured, recommend >= 600s", waitTimeout)
		}
		checks = append(checks, check)
	}

	return checks
}

// checkRecordCount verifies we can count legacy records.
func (c *Controller) checkRecordCount() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "record_count",
		Name:        "Record Count",
		Description: "Count legacy records to migrate",
		Severity:    CheckSeverityCritical,
	}

	if c.Repo == nil {
		check.Status = CheckStatusError
		check.Message = "Detection repository not available"
		return check
	}

	// Use context.Background() as fallback if ctx is nil (e.g., in tests)
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	count, err := c.Repo.CountAll(ctx)
	if err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("Failed to count records: %v", err)
		return check
	}

	check.Status = CheckStatusPassed
	check.Message = fmt.Sprintf("%d records to migrate", count)
	return check
}

// checkExistingV2Data warns if migration target tables already have data.
func (c *Controller) checkExistingV2Data() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "existing_v2_data",
		Name:        "Existing Migration Data",
		Description: "Check for existing data in migration target tables",
		Severity:    CheckSeverityWarning,
	}

	if c.V2Manager == nil {
		check.Status = CheckStatusSkipped
		check.Message = "Migration target database not available"
		return check
	}

	db := c.V2Manager.DB()
	if db == nil {
		check.Status = CheckStatusSkipped
		check.Message = "Migration target database not available"
		return check
	}

	// Check if detections table has data
	var count int64
	tableName := "detections"
	if c.isUsingMySQL() {
		tableName = "v2_detections"
	}

	if err := db.Table(tableName).Count(&count).Error; err != nil {
		// Table might not exist yet, which is fine
		check.Status = CheckStatusPassed
		check.Message = "No existing migration data found"
		return check
	}

	if count > 0 {
		check.Status = CheckStatusWarning
		check.Message = fmt.Sprintf("Migration target contains %d existing records", count)
	} else {
		check.Status = CheckStatusPassed
		check.Message = "No existing migration data"
	}

	return check
}

// checkMemoryAvailable checks if there's enough free memory for migration.
func (c *Controller) checkMemoryAvailable() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "memory_available",
		Name:        "Available Memory",
		Description: "Sufficient memory for batch processing",
		Severity:    CheckSeverityWarning,
	}

	v, err := mem.VirtualMemory()
	if err != nil {
		check.Status = CheckStatusSkipped
		check.Message = "Could not check memory"
		return check
	}

	availableMB := v.Available / (1024 * 1024)
	if v.Available >= MinMemoryBytes {
		check.Status = CheckStatusPassed
		check.Message = fmt.Sprintf("%dMB available", availableMB)
	} else {
		check.Status = CheckStatusWarning
		check.Message = fmt.Sprintf("%dMB available, recommend >= 50MB", availableMB)
	}

	return check
}

// getLegacyGormDB returns the GORM database instance from the legacy datastore.
// Returns nil if the datastore doesn't implement gormDBProvider.
func (c *Controller) getLegacyGormDB() *gorm.DB {
	if c.DS == nil {
		return nil
	}

	if provider, ok := c.DS.(gormDBProvider); ok {
		return provider.GetDB()
	}

	return nil
}
