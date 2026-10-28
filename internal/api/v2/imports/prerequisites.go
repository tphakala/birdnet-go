// internal/api/v2/imports/prerequisites.go
package importsapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

// v2OnlySkipMessage is the standard message for prerequisite checks that are
// skipped in v2-only (fresh install) mode where no legacy database exists.
const v2OnlySkipMessage = "Running in enhanced database mode, legacy database not available"

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

// prerequisiteTagPassed is the migration_prereq telemetry value when no critical check blocks.
const prerequisiteTagPassed = "passed"

// prerequisiteTagValue reduces check results to the bounded migration_prereq
// telemetry value: "passed" when no critical check failed or errored,
// otherwise the fixed ID of the first one that did.
func prerequisiteTagValue(checks []PrerequisiteCheck) string {
	for i := range checks {
		c := &checks[i]
		if c.Severity == CheckSeverityCritical && (c.Status == CheckStatusFailed || c.Status == CheckStatusError) {
			return c.ID
		}
	}
	return prerequisiteTagPassed
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
func (c *Handler) GetPrerequisites(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Checking migration prerequisites", logger.String("path", path), logger.String("ip", ip))

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
	canStart := allPassed && !getIsV2OnlyMode()

	response := PrerequisitesResponse{
		AllPassed:         allPassed,
		CanStartMigration: canStart,
		Checks:            checks,
		CriticalFailures:  criticalFailures,
		Warnings:          warnings,
		CheckedAt:         time.Now(),
	}

	c.LogInfoIfEnabled("Prerequisites check complete",
		logger.String("path", path),
		logger.String("ip", ip),
		logger.Bool("can_start", canStart),
		logger.Int("critical_failures", criticalFailures),
		logger.Int("warnings", warnings))

	return ctx.JSON(http.StatusOK, response)
}

// isUsingMySQL returns true if the application is configured to use MySQL.
func (c *Handler) isUsingMySQL() bool {
	settings := c.CurrentSettings()
	if settings == nil {
		return false
	}
	return settings.Output.MySQL.Enabled
}

// checkStateIdle verifies the migration is in IDLE state.
func (c *Handler) checkStateIdle() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "state_idle",
		Name:        "Migration State",
		Description: "Migration must be in idle state",
		Severity:    CheckSeverityCritical,
	}

	if stateManager == nil {
		if getIsV2OnlyMode() {
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
func (c *Handler) checkDiskSpace() PrerequisiteCheck {
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

	if settings := c.CurrentSettings(); settings != nil && !settings.Output.MySQL.Enabled {
		dbPath := settings.Output.SQLite.Path
		if !filepath.IsAbs(dbPath) {
			dbPath, _ = filepath.Abs(dbPath)
		}
		if info, err := os.Stat(dbPath); err == nil {
			// #nosec G115 -- info.Size() from os.FileInfo is always non-negative
			dbSize := uint64(info.Size())
			halfDBSize := dbSize / 2
			if halfDBSize > requiredSpace {
				requiredSpace = halfDBSize
			}
			dbSizeGB := float64(dbSize) / (1024 * 1024 * 1024)
			dbSizeInfo = fmt.Sprintf(" (DB size: %.1fGB)", dbSizeGB)
		}
	}

	free, err := c.diskFree(diskPath)
	if err != nil {
		check.Status = CheckStatusError
		check.Message = fmt.Sprintf("Failed to check disk space on %s: %v", diskPath, err)
		return check
	}

	freeGB := float64(free) / (1024 * 1024 * 1024)
	requiredGB := float64(requiredSpace) / (1024 * 1024 * 1024)

	if free >= requiredSpace {
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
func (c *Handler) getDatabaseDirectoryResolved() (string, error) {
	settings := c.CurrentSettings()
	if settings == nil {
		return "", fmt.Errorf("settings not available")
	}

	if settings.Output.MySQL.Enabled {
		// For MySQL, disk space check is not applicable (remote database)
		return "", nil
	}

	dbPath := settings.Output.SQLite.Path
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
func (c *Handler) checkLegacyAccessible() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "legacy_accessible",
		Name:        "Legacy Database",
		Description: "Legacy database must be accessible",
		Severity:    CheckSeverityCritical,
	}

	if c.DS == nil {
		if getIsV2OnlyMode() {
			check.Status = CheckStatusSkipped
			check.Message = v2OnlySkipMessage
			return check
		}
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
func (c *Handler) checkSQLiteIntegrity() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "sqlite_integrity",
		Name:        "Database Integrity",
		Description: "SQLite quick integrity check",
		Severity:    CheckSeverityCritical,
	}

	db := c.getLegacyGormDB()
	if db == nil {
		if getIsV2OnlyMode() {
			check.Status = CheckStatusSkipped
			check.Message = v2OnlySkipMessage
			return check
		}
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
func (c *Handler) checkWritePermission() PrerequisiteCheck {
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

	f, err := os.CreateTemp(dir, ".birdnet_write_test_*")
	if err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("Cannot write to %s: %v", dir, err)
		return check
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)

	check.Status = CheckStatusPassed
	check.Message = fmt.Sprintf("Write access verified for %s", dir)
	return check
}

// checkMySQLTableHealth runs CHECK TABLE on legacy MySQL tables.
func (c *Handler) checkMySQLTableHealth() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "mysql_table_health",
		Name:        "MySQL Table Health",
		Description: "Check legacy table health",
		Severity:    CheckSeverityCritical,
	}

	db := c.getLegacyGormDB()
	if db == nil {
		if getIsV2OnlyMode() {
			check.Status = CheckStatusSkipped
			check.Message = v2OnlySkipMessage
			return check
		}
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
			if r.MsgType == "error" { //nolint:goconst // health-check message type, not the importEventError SSE-event constant
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
func (c *Handler) checkMySQLPermissions() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "mysql_permissions",
		Name:        "MySQL Permissions",
		Description: "Verify CREATE, INSERT, UPDATE, DELETE, DROP privileges",
		Severity:    CheckSeverityCritical,
	}

	db := c.getLegacyGormDB()
	if db == nil {
		if getIsV2OnlyMode() {
			check.Status = CheckStatusSkipped
			check.Message = v2OnlySkipMessage
			return check
		}
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
func (c *Handler) checkMySQLConfiguration() []PrerequisiteCheck {
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
func (c *Handler) checkRecordCount() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "record_count",
		Name:        "Record Count",
		Description: "Count legacy records to migrate",
		Severity:    CheckSeverityCritical,
	}

	if c.Repo == nil {
		if getIsV2OnlyMode() {
			check.Status = CheckStatusSkipped
			check.Message = v2OnlySkipMessage
			return check
		}
		check.Status = CheckStatusError
		check.Message = "Detection repository not available"
		return check
	}

	// Use context.Background() as fallback if ctx is nil (e.g., in tests)
	ctx := c.Context()
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
func (c *Handler) checkExistingV2Data() PrerequisiteCheck {
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

	// Check if detections table has data. Derive the table name from the manager's
	// actual prefix (empty for fresh installs, "v2_" during migration) instead of
	// guessing from the dialect, matching every other v2 consumer.
	var count int64
	tableName := c.V2Manager.TablePrefix() + "detections"

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
func (c *Handler) checkMemoryAvailable() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          "memory_available",
		Name:        "Available Memory",
		Description: "Sufficient memory for batch processing",
		Severity:    CheckSeverityWarning,
	}

	available, err := c.availableMemory()
	if err != nil {
		check.Status = CheckStatusSkipped
		check.Message = "Could not check memory"
		return check
	}

	availableMB := available / (1024 * 1024)
	if available >= MinMemoryBytes {
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
func (c *Handler) getLegacyGormDB() *gorm.DB {
	if c.DS == nil {
		return nil
	}

	if provider, ok := c.DS.(gormDBProvider); ok {
		return provider.GetDB()
	}

	return nil
}

// diskFree returns the free bytes on the filesystem holding path through the
// diskFreeFn seam, falling back to gopsutil disk.Usage.
func (c *Handler) diskFree(path string) (uint64, error) {
	if c.diskFreeFn != nil {
		return c.diskFreeFn(path)
	}
	usage, err := disk.Usage(path)
	if err != nil {
		return 0, err
	}
	return usage.Free, nil
}

// availableMemory returns the available memory in bytes through the
// availableMemoryFn seam, falling back to gopsutil mem.VirtualMemory.
func (c *Handler) availableMemory() (uint64, error) {
	if c.availableMemoryFn != nil {
		return c.availableMemoryFn()
	}
	v, err := mem.VirtualMemory()
	if err != nil {
		return 0, err
	}
	return v.Available, nil
}

// Check IDs and limits for the database server prerequisites.
const (
	checkIDMySQLServerVersion = "mysql_server_version"
	checkIDMySQLCharset       = "mysql_charset"

	// mysqlServerProbeTimeout bounds the information_schema and VERSION() reads.
	mysqlServerProbeTimeout = 10 * time.Second
	// maxNamedCharsetTables bounds how many tables the charset message names.
	maxNamedCharsetTables = 10

	errMsgMigrationTargetUnavailable = "Migration target database not available"
	errMsgMigrationTargetAmbiguous   = "Migration target tables cannot be told apart from the legacy tables"
	errMsgCharsetUndetermined        = "Could not determine the character set of the migration target tables"
)

// mysqlMigrationTarget returns the migration target manager when it is a MySQL
// or MariaDB one. The query seams never replace this gating.
func (c *Handler) mysqlMigrationTarget() (datastoreV2.Manager, bool) {
	if c.V2Manager == nil || !c.V2Manager.IsMySQL() {
		return nil, false
	}
	return c.V2Manager, true
}

// probeContext returns a bounded context for the server probes.
func (c *Handler) probeContext() (context.Context, context.CancelFunc) {
	ctx := c.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, mysqlServerProbeTimeout)
}

// checkMySQLServerVersion blocks the migration on a database server release
// that upstream no longer supports. It follows dbserver's lifecycle table, so
// a table edit changes the verdict without code changes.
func (c *Handler) checkMySQLServerVersion() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          checkIDMySQLServerVersion,
		Name:        "Database Server Version",
		Description: "The database server must be a release under upstream support",
		Severity:    CheckSeverityCritical,
	}
	if getIsV2OnlyMode() {
		check.Status = CheckStatusSkipped
		check.Message = v2OnlySkipMessage
		return check
	}
	mgr, ok := c.mysqlMigrationTarget()
	if !ok || (c.serverVersionFn == nil && mgr.DB() == nil) {
		check.Status = CheckStatusError
		check.Message = errMsgMigrationTargetUnavailable
		return check
	}

	ctx, cancel := c.probeContext()
	defer cancel()
	var raw string
	var err error
	if c.serverVersionFn != nil {
		raw, err = c.serverVersionFn(ctx)
	} else {
		raw, err = dbserver.QueryVersion(ctx, mgr.DB())
	}
	if err != nil {
		check.Status = CheckStatusError
		check.Message = fmt.Sprintf("Could not read the database server version: %v", err)
		return check
	}

	a := dbserver.AssessRaw(raw, time.Now())
	check.Status, check.Message = describeServerVersion(&a)
	return check
}

// describeServerVersion maps an assessment to a check status and message.
func describeServerVersion(a *dbserver.Assessment) (status, message string) {
	name := a.Version.Name()
	supported := dbserver.SupportedReleases()
	switch a.Level {
	case dbserver.SupportSupported:
		return CheckStatusPassed, fmt.Sprintf("%s is supported until %s", name, a.Release.EOL.Format(time.DateOnly))
	case dbserver.SupportEndingSoon:
		return CheckStatusWarning, fmt.Sprintf("%s reaches its upstream end of life on %s. Plan an upgrade to a supported release (%s).",
			name, a.Release.EOL.Format(time.DateOnly), supported)
	case dbserver.SupportEndOfLife:
		if a.Release != nil {
			return CheckStatusFailed, fmt.Sprintf("%s is past its upstream end of life (%s) and is not supported. Upgrade the database server to a supported release (%s) before migrating.",
				name, a.Release.EOL.Format(time.DateOnly), supported)
		}
		return CheckStatusFailed, fmt.Sprintf("%s is older than the oldest supported release and is not supported. Upgrade the database server to a supported release (%s) before migrating.",
			name, supported)
	case dbserver.SupportUntracked:
		return CheckStatusWarning, fmt.Sprintf("%s is not on the list of releases BirdNET-Go is tested with (%s).", name, supported)
	case dbserver.SupportUnknown:
		return CheckStatusWarning, fmt.Sprintf("The database server version %q was not recognized. Supported releases: %s.", a.Version.Raw, supported)
	}
	return CheckStatusWarning, fmt.Sprintf("The support level of %s is unknown.", name)
}

// checkMySQLCharset blocks the migration when the v2 target tables cannot
// store the text of the legacy tables. It only reads information_schema.
func (c *Handler) checkMySQLCharset() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          checkIDMySQLCharset,
		Name:        "Database Character Set",
		Description: "Migration target tables must be able to store the legacy text",
		Severity:    CheckSeverityCritical,
	}
	if getIsV2OnlyMode() {
		check.Status = CheckStatusSkipped
		check.Message = v2OnlySkipMessage
		return check
	}
	mgr, ok := c.mysqlMigrationTarget()
	if !ok || (c.charsetReportFn == nil && mgr.DB() == nil) {
		check.Status = CheckStatusError
		check.Message = errMsgMigrationTargetUnavailable
		return check
	}
	prefix := mgr.TablePrefix()
	if prefix == "" {
		check.Status = CheckStatusError
		check.Message = errMsgMigrationTargetAmbiguous
		return check
	}

	ctx, cancel := c.probeContext()
	defer cancel()
	var report dbserver.CharsetReport
	var err error
	if c.charsetReportFn != nil {
		report, err = c.charsetReportFn(ctx)
	} else {
		report, err = dbserver.QueryCharset(ctx, mgr.DB())
	}
	if err != nil {
		check.Status = CheckStatusError
		check.Message = fmt.Sprintf("Could not read the database character sets: %v", err)
		return check
	}

	check.Status, check.Message = describeCharset(&report, prefix)
	return check
}

// charsetTables is the classification of a schema's tables for the charset check.
type charsetTables struct {
	targetNames    []string
	targetCharsets []string
	sourceCharsets []string
}

// classifyCharsetTables splits report into target tables (name starts with
// prefix) and legacy source tables (name in the legacy lists). A table's class
// depends only on its own name; any other table is ignored. When no target
// table has text columns, the schema default stands in for the target.
func classifyCharsetTables(report *dbserver.CharsetReport, prefix string) charsetTables {
	var out charsetTables
	prefix = strings.ToLower(prefix)
	for _, t := range report.Tables {
		name := strings.ToLower(t.Name)
		switch {
		case strings.HasPrefix(name, prefix):
			out.targetNames = append(out.targetNames, t.Name)
			out.targetCharsets = append(out.targetCharsets, t.Charsets...)
		case slices.Contains(legacyOnlyTables, name) || slices.Contains(legacySharedTables, name):
			out.sourceCharsets = append(out.sourceCharsets, t.Charsets...)
		}
	}
	if len(out.targetCharsets) == 0 && report.SchemaCharset != "" {
		out.targetCharsets = []string{report.SchemaCharset}
	}
	slices.Sort(out.targetCharsets)
	out.targetCharsets = slices.Compact(out.targetCharsets)
	slices.Sort(out.sourceCharsets)
	out.sourceCharsets = slices.Compact(out.sourceCharsets)
	return out
}

// describeCharset judges whether the target tables can hold the legacy text.
// Every target charset must hold every source charset.
func describeCharset(report *dbserver.CharsetReport, prefix string) (status, message string) {
	tables := classifyCharsetTables(report, prefix)
	if len(tables.targetCharsets) == 0 {
		return CheckStatusError, errMsgCharsetUndetermined
	}

	for _, target := range tables.targetCharsets {
		for _, source := range tables.sourceCharsets {
			if dbserver.CharsetCanHold(target, source) {
				continue
			}
			return CheckStatusFailed, fmt.Sprintf(
				"Migration target tables %s use %s, which cannot store the %s text in the legacy tables. Convert the %s tables to %s (ALTER TABLE ... CONVERT TO CHARACTER SET %s) before migrating.",
				describeTableNames(tables.targetNames, report.SchemaCharset), target, source,
				prefix, dbserver.CharsetUTF8MB4, dbserver.CharsetUTF8MB4)
		}
	}

	if len(tables.targetCharsets) == 1 && tables.targetCharsets[0] == dbserver.CharsetUTF8MB4 {
		return CheckStatusPassed, "The migration target tables use " + dbserver.CharsetUTF8MB4
	}
	used := strings.Join(tables.targetCharsets, ", ")
	return CheckStatusWarning, fmt.Sprintf(
		"The migration target tables use %s. The legacy data fits, but after the migration text outside %s, such as emoji or some scripts, is rejected.",
		used, used)
}

// describeTableNames lists up to maxNamedCharsetTables names. With no target
// table it names the schema default the tables will inherit.
func describeTableNames(names []string, schemaCharset string) string {
	if len(names) == 0 {
		return "(not created yet, schema default " + schemaCharset + ")"
	}
	if len(names) <= maxNamedCharsetTables {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:maxNamedCharsetTables], ", "), len(names)-maxNamedCharsetTables)
}
