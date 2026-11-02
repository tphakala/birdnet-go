// internal/api/v2/imports/prerequisites.go
package importsapi

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/telemetry"
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

// PrerequisitesResponse is the snapshot of the prerequisites run returned by
// the prerequisites endpoints. While a run is in progress Checks holds the
// checks finished so far and AllPassed and CanStartMigration are false.
type PrerequisitesResponse struct {
	AllPassed         bool                `json:"all_passed"`
	CanStartMigration bool                `json:"can_start_migration"`
	Checks            []PrerequisiteCheck `json:"checks"`
	CriticalFailures  int                 `json:"critical_failures"`
	Warnings          int                 `json:"warnings"`
	// CheckedAt is the completion time of the run; zero (omitted) until a run completes.
	CheckedAt time.Time `json:"checked_at,omitzero"`
	// Status is one of the PrereqRunStatus values.
	Status string `json:"status"`
	// RunID increases with every run started by this process; 0 before the first.
	RunID     uint64    `json:"run_id"`
	StartedAt time.Time `json:"started_at,omitzero"`
	// Progress is present only while Status is running.
	Progress *PrerequisitesProgress `json:"progress,omitempty"`
	// Error says why a run ended with Status failed.
	Error string `json:"error,omitempty"`
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

// integrityResulter is implemented by the legacy SQLite store, which caches the
// result of its startup and daily PRAGMA quick_check (the same consumer-side
// pattern as internal/api/v2/system/diagnostics.go). It is asserted only outside
// v2-only mode, where the datastore's IntegrityResult can run a scan.
type integrityResulter interface {
	IntegrityResult() (string, bool)
}

// Check IDs used by the run steps.
const (
	checkIDStateIdle          = "state_idle"
	checkIDDiskSpace          = "disk_space"
	checkIDLegacyAccessible   = "legacy_accessible"
	checkIDSQLiteIntegrity    = "sqlite_integrity"
	checkIDWritePermission    = "write_permission"
	checkIDMySQLTableHealth   = "mysql_table_health"
	checkIDMySQLPermissions   = "mysql_permissions"
	checkIDRecordCount        = "record_count"
	checkIDExistingV2Data     = "existing_v2_data"
	checkIDMemoryAvailable    = "memory_available"
	checkIDMySQLConfiguration = "mysql_configuration"
)

// Check display names, shared by the checks and the run steps that report progress.
const (
	checkNameStateIdle          = "Migration State"
	checkNameDiskSpace          = "Disk Space"
	checkNameLegacyAccessible   = "Legacy Database"
	checkNameSQLiteIntegrity    = "Database Integrity"
	checkNameWritePermission    = "Write Permission"
	checkNameMySQLTableHealth   = "MySQL Table Health"
	checkNameMySQLPermissions   = "MySQL Permissions"
	checkNameRecordCount        = "Record Count"
	checkNameExistingV2Data     = "Existing Migration Data"
	checkNameMemoryAvailable    = "Available Memory"
	checkNameMySQLServerVersion = "Database Server Version"
	checkNameMySQLCharset       = "Database Character Set"
	checkNameMySQLConfiguration = "MySQL Configuration"
)

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

// Integrity check values.
const (
	// sqliteIntegrityOK is the single row PRAGMA quick_check returns for a healthy database.
	sqliteIntegrityOK = "ok"
	// msgIntegrityLiveOK is the message when the live PRAGMA quick_check passed.
	msgIntegrityLiveOK = "Database integrity verified"
	// msgIntegrityCachedOK is the message when the store's cached result was trusted.
	msgIntegrityCachedOK = "Database integrity verified by the startup or daily integrity check"
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

// GetPrerequisites handles GET /api/v2/system/database/migration/prerequisites.
// It returns the snapshot of the background prerequisites run and never starts
// work; POST .../prerequisites/run starts or joins a run.
func (c *Handler) GetPrerequisites(ctx echo.Context) error {
	return ctx.JSON(http.StatusOK, c.prereqRunner().snapshot())
}

// summarizePrerequisites counts failures and warnings in checks and decides
// whether a migration may start. The run stamps the completion time.
func (c *Handler) summarizePrerequisites(checks []PrerequisiteCheck) PrerequisitesResponse {
	criticalFailures := 0
	warnings := 0
	for i := range checks {
		check := &checks[i]
		if (check.Status == CheckStatusFailed || check.Status == CheckStatusError) && check.Severity == CheckSeverityCritical {
			criticalFailures++
		}
		if check.Status == CheckStatusWarning {
			warnings++
		}
	}

	allPassed := criticalFailures == 0
	return PrerequisitesResponse{
		AllPassed:         allPassed,
		CanStartMigration: allPassed && !getIsV2OnlyMode(),
		Checks:            checks,
		CriticalFailures:  criticalFailures,
		Warnings:          warnings,
	}
}

// finishPrerequisites summarizes a completed run and records its outcome in the
// migration_prereq telemetry tag. The run calls it once, only when it was not cancelled.
func (c *Handler) finishPrerequisites(checks []PrerequisiteCheck) PrerequisitesResponse {
	telemetry.SetMigrationPrereqTag(prerequisiteTagValue(checks))
	return c.summarizePrerequisites(checks)
}

// prerequisiteSteps lists every step of a full run: the critical steps, then
// the warning checks. sink receives the legacy record count.
func (c *Handler) prerequisiteSteps(sink *recordCountSink) []prereqStep {
	steps := c.criticalPrerequisiteSteps(sink)
	steps = append(steps,
		singleCheckStep(checkIDExistingV2Data, checkNameExistingV2Data, false, c.checkExistingV2Data),
		singleCheckStep(checkIDMemoryAvailable, checkNameMemoryAvailable, false, func(context.Context) PrerequisiteCheck { return c.checkMemoryAvailable() }),
	)
	if c.isUsingMySQL() {
		steps = append(steps, prereqStep{
			id:   checkIDMySQLConfiguration,
			name: checkNameMySQLConfiguration,
			run:  c.checkMySQLConfiguration,
		})
	}
	return steps
}

// singleCheckStep adapts a one-check function to a prereqStep.
func singleCheckStep(id, name string, reuseAtStart bool, fn func(context.Context) PrerequisiteCheck) prereqStep {
	return prereqStep{
		id:           id,
		name:         name,
		reuseAtStart: reuseAtStart,
		run:          func(ctx context.Context) []PrerequisiteCheck { return []PrerequisiteCheck{fn(ctx)} },
	}
}

// PrerequisitesRunRequest is the optional body of POST .../prerequisites/run.
type PrerequisitesRunRequest struct {
	// Force starts a new run even when a fresh passing result exists.
	Force bool `json:"force"`
}

// RunPrerequisites handles POST /api/v2/system/database/migration/prerequisites/run.
// It starts the background prerequisites run, or joins the one in progress, and
// returns the snapshot. The run derives from the server lifecycle context, never
// from the request, so a closed tab does not cancel a run other tabs watch.
func (c *Handler) RunPrerequisites(ctx echo.Context) error {
	var req PrerequisitesRunRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleErrorWithKey(ctx, err, "Invalid request body", http.StatusBadRequest, notification.MsgErrMigrationInvalidBody, nil)
	}

	snap := c.prereqRunner().start(c.lifecycleContext(), req.Force, c.dbFingerprint(), c.prerequisiteSteps, c.finishPrerequisites)
	return ctx.JSON(http.StatusOK, snap)
}

// logPrerequisitesStart logs a prerequisites run that really starts.
func (c *Handler) logPrerequisitesStart(runID uint64, force bool) {
	c.LogInfoIfEnabled(msgPrereqRunStarting, logger.Uint64("run_id", runID), logger.Bool("force", force))
}

// logPrerequisitesEnd logs the outcome of a prerequisites run once it ended.
func (c *Handler) logPrerequisitesEnd(result *PrerequisitesResponse, elapsed time.Duration) {
	c.LogInfoIfEnabled(msgPrereqRunFinished,
		logger.Uint64("run_id", result.RunID),
		logger.String("status", result.Status),
		logger.Duration("duration", elapsed),
		logger.Bool("can_start", result.CanStartMigration),
		logger.Int("critical_failures", result.CriticalFailures),
		logger.Int("warnings", result.Warnings))
}

// lifecycleContext returns the server lifecycle context, or Background when
// none is installed (a Core built directly in tests).
func (c *Handler) lifecycleContext() context.Context {
	if ctx := c.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// dbFingerprint reads the database settings that decide what the checks look
// at, from the live settings. The MySQL credentials are included only as a hash.
func (c *Handler) dbFingerprint() dbFingerprint {
	settings := c.CurrentSettings()
	if settings == nil {
		return dbFingerprint{}
	}
	mysql := &settings.Output.MySQL
	sqlitePath := settings.Output.SQLite.Path
	if abs, err := filepath.Abs(sqlitePath); err == nil && sqlitePath != "" {
		sqlitePath = abs
	}
	return dbFingerprint{
		MySQLEnabled:         mysql.Enabled,
		SQLitePath:           sqlitePath,
		MySQLHost:            mysql.Host,
		MySQLPort:            mysql.Port,
		MySQLDatabase:        mysql.Database,
		MySQLCredentialsHash: mysqlCredentialsHash(mysql.Username, mysql.Password),
	}
}

// mysqlCredentialsHash returns a truncated digest of the MySQL user name and
// password. Each part is length-prefixed so two different pairs cannot hash alike
// by moving a separator.
func mysqlCredentialsHash(username, password string) uint32 {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d:%s%d:%s", len(username), username, len(password), password))
	return binary.BigEndian.Uint32(sum[:])
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
		ID:          checkIDStateIdle,
		Name:        checkNameStateIdle,
		Description: "Migration must be in idle state",
		Severity:    CheckSeverityCritical,
	}

	// One snapshot under migrationMu, so a concurrent swap cannot nil the manager
	// between the nil check and the call.
	sm := getStateManager()
	if sm == nil {
		if getIsV2OnlyMode() {
			check.Status = CheckStatusSkipped
			check.Message = "Running in enhanced database mode, migration already complete"
			return check
		}
		check.Status = CheckStatusError
		check.Message = "State manager not available"
		return check
	}

	state, err := sm.GetState()
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
		ID:          checkIDDiskSpace,
		Name:        checkNameDiskSpace,
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
func (c *Handler) checkLegacyAccessible(ctx context.Context) PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          checkIDLegacyAccessible,
		Name:        checkNameLegacyAccessible,
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
	// GetLastDetections takes no context, so wait for it in a way that ctx can
	// interrupt. The abandoned call finishes on its own and its result is dropped.
	done := make(chan error, 1)
	go func() {
		// Nothing else recovers a panic in this goroutine
		defer func() {
			if rec := recover(); rec != nil {
				done <- fmt.Errorf("read panicked: %v", rec)
			}
		}()
		_, err := c.DS.GetLastDetections(1)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			check.Status = CheckStatusFailed
			check.Message = fmt.Sprintf("Cannot read from legacy database: %v", err)
			return check
		}
	case <-ctx.Done():
		check.Status = CheckStatusError
		check.Message = fmt.Sprintf("Check interrupted before the legacy database answered: %v", ctx.Err())
		return check
	}

	check.Status = CheckStatusPassed
	check.Message = "Connected and readable"
	return check
}

// checkSQLiteIntegrity verifies the legacy SQLite database. It trusts the legacy
// store's cached startup or daily quick_check result when that result is "ok"
// and corruption is not latched, which avoids re-reading the whole file. Any
// other state (no cache, an error string from the daily check, latched
// corruption) runs PRAGMA quick_check live, which also confirms real corruption.
// The cached result can be up to a day old, so corruption that appeared since the
// last scan is not detected here.
// Scans all result rows since quick_check can return multiple errors.
func (c *Handler) checkSQLiteIntegrity(ctx context.Context) PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          checkIDSQLiteIntegrity,
		Name:        checkNameSQLiteIntegrity,
		Description: "SQLite quick integrity check",
		Severity:    CheckSeverityCritical,
	}

	// In v2-only mode the datastore's IntegrityResult can itself run a scan, so
	// this guard must stay ahead of the integrityResulter assertion.
	if getIsV2OnlyMode() {
		check.Status = CheckStatusSkipped
		check.Message = v2OnlySkipMessage
		return check
	}

	if r, ok := c.DS.(integrityResulter); ok {
		if result, latched := r.IntegrityResult(); result == sqliteIntegrityOK && !latched {
			check.Status = CheckStatusPassed
			check.Message = msgIntegrityCachedOK
			return check
		}
	}

	db := c.getLegacyGormDB()
	if db == nil {
		check.Status = CheckStatusError
		check.Message = errMsgDBConnectionUnavailable
		return check
	}

	// PRAGMA quick_check can return multiple rows if there are multiple errors
	var results []string
	if err := db.WithContext(ctx).Raw("PRAGMA quick_check").Scan(&results).Error; err != nil {
		check.Status = CheckStatusError
		check.Message = fmt.Sprintf("Failed to run integrity check: %v", err)
		return check
	}

	// Check all results - any non-"ok" result indicates a problem
	for _, result := range results {
		if result != sqliteIntegrityOK {
			check.Status = CheckStatusFailed
			check.Message = fmt.Sprintf("Integrity check failed: %s", result)
			return check
		}
	}

	check.Status = CheckStatusPassed
	check.Message = msgIntegrityLiveOK
	return check
}

// checkWritePermission verifies write access to the database directory.
func (c *Handler) checkWritePermission() PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          checkIDWritePermission,
		Name:        checkNameWritePermission,
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
func (c *Handler) checkMySQLTableHealth(ctx context.Context) PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          checkIDMySQLTableHealth,
		Name:        checkNameMySQLTableHealth,
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

		if err := db.WithContext(ctx).Raw("CHECK TABLE " + table).Scan(&results).Error; err != nil {
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
func (c *Handler) checkMySQLPermissions(ctx context.Context) PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          checkIDMySQLPermissions,
		Name:        checkNameMySQLPermissions,
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
	// The cleanup runs on a context detached from ctx, so cancelling ctx does not
	// skip the DROP while this step is still running. A step the run abandoned
	// at shutdown is not waited for, so the table can then remain.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mysqlServerProbeTimeout)
		defer cancel()
		_ = db.WithContext(cleanupCtx).Exec("DROP TABLE IF EXISTS " + testTable).Error
	}()

	// Test CREATE
	if err := db.WithContext(ctx).Exec("CREATE TABLE " + testTable + " (id INT PRIMARY KEY)").Error; err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("CREATE permission denied: %v", err)
		return check
	}

	// Test INSERT
	if err := db.WithContext(ctx).Exec("INSERT INTO " + testTable + " (id) VALUES (1)").Error; err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("INSERT permission denied: %v", err)
		return check
	}

	// Test UPDATE
	if err := db.WithContext(ctx).Exec("UPDATE " + testTable + " SET id = 2 WHERE id = 1").Error; err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("UPDATE permission denied: %v", err)
		return check
	}

	// Test DELETE
	if err := db.WithContext(ctx).Exec("DELETE FROM " + testTable + " WHERE id = 2").Error; err != nil {
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
func (c *Handler) checkMySQLConfiguration(ctx context.Context) []PrerequisiteCheck {
	checks := make([]PrerequisiteCheck, 0, 2)

	db := c.getLegacyGormDB()
	if db == nil {
		return checks
	}

	// Check max_allowed_packet
	var maxPacket int64
	if err := db.WithContext(ctx).Raw("SELECT @@max_allowed_packet").Scan(&maxPacket).Error; err == nil {
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
	if err := db.WithContext(ctx).Raw("SELECT @@wait_timeout").Scan(&waitTimeout).Error; err == nil {
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
func (c *Handler) checkRecordCount(ctx context.Context) PrerequisiteCheck {
	_, check := c.countLegacyRecords(ctx)
	return check
}

// countLegacyRecords counts the legacy records and returns the count with the
// record_count check. The count is meaningful only when the check passed.
func (c *Handler) countLegacyRecords(ctx context.Context) (int64, PrerequisiteCheck) {
	check := PrerequisiteCheck{
		ID:          checkIDRecordCount,
		Name:        checkNameRecordCount,
		Description: "Count legacy records to migrate",
		Severity:    CheckSeverityCritical,
	}

	if c.Repo == nil {
		if getIsV2OnlyMode() {
			check.Status = CheckStatusSkipped
			check.Message = v2OnlySkipMessage
			return 0, check
		}
		check.Status = CheckStatusError
		check.Message = "Detection repository not available"
		return 0, check
	}

	count, err := c.Repo.CountAll(ctx)
	if err != nil {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("Failed to count records: %v", err)
		return 0, check
	}

	check.Status = CheckStatusPassed
	check.Message = fmt.Sprintf("%d records to migrate", count)
	return count, check
}

// checkExistingV2Data warns if migration target tables already have data.
func (c *Handler) checkExistingV2Data(ctx context.Context) PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          checkIDExistingV2Data,
		Name:        checkNameExistingV2Data,
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

	if err := db.WithContext(ctx).Table(tableName).Count(&count).Error; err != nil {
		if ctx.Err() != nil {
			// A cancelled count says nothing about the table; do not report it as absent.
			check.Status = CheckStatusError
			check.Message = fmt.Sprintf("Check interrupted: %v", ctx.Err())
			return check
		}
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
		ID:          checkIDMemoryAvailable,
		Name:        checkNameMemoryAvailable,
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

// probeContext returns a context for the server probes, bounded by
// mysqlServerProbeTimeout and cancelled with parent.
func probeContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, mysqlServerProbeTimeout)
}

// checkMySQLServerVersion blocks the migration on a database server release
// that BirdNET-Go does not support. It follows dbserver's release table, so a
// table edit changes the verdict without code changes.
func (c *Handler) checkMySQLServerVersion(ctx context.Context) PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          checkIDMySQLServerVersion,
		Name:        checkNameMySQLServerVersion,
		Description: "The database server must be a supported release",
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

	ctx, cancel := probeContext(ctx)
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

	a := dbserver.AssessRaw(raw)
	check.Status, check.Message = describeServerVersion(&a)
	return check
}

// describeServerVersion maps an assessment to a check status and message.
func describeServerVersion(a *dbserver.Assessment) (status, message string) {
	name := a.Version.Name()
	supported := dbserver.SupportedReleases()
	switch a.Level {
	case dbserver.SupportSupported:
		return CheckStatusPassed, fmt.Sprintf("%s is a supported release", name)
	case dbserver.SupportUnsupported:
		return CheckStatusFailed, fmt.Sprintf("%s is not a supported release. Upgrade the database server to a supported release (%s) before migrating.", name, supported)
	case dbserver.SupportUntracked:
		return CheckStatusWarning, fmt.Sprintf("%s is not on the list of releases BirdNET-Go is tested with (%s).", name, supported)
	case dbserver.SupportUnknown:
		return CheckStatusWarning, fmt.Sprintf("The database server version %q was not recognized. Supported releases: %s.", a.Version.Raw, supported)
	}
	return CheckStatusWarning, fmt.Sprintf("The support level of %s is unknown.", name)
}

// checkMySQLCharset blocks the migration when the v2 target tables cannot
// store the text of the legacy tables. It only reads information_schema.
func (c *Handler) checkMySQLCharset(ctx context.Context) PrerequisiteCheck {
	check := PrerequisiteCheck{
		ID:          checkIDMySQLCharset,
		Name:        checkNameMySQLCharset,
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

	ctx, cancel := probeContext(ctx)
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
	targetTables   []dbserver.TableCharsets
	targetCharsets []string
	sourceCharsets []string
}

// classifyCharsetTables splits report into target tables (name starts with
// prefix) and legacy source tables (name in the legacy lists). A table's class
// depends only on its own name; any other table is ignored. Target tables keep
// their charsets so a message can name the ones that use a given charset. When no target
// table has text columns, the schema default stands in for the target.
func classifyCharsetTables(report *dbserver.CharsetReport, prefix string) charsetTables {
	var out charsetTables
	prefix = strings.ToLower(prefix)
	for _, t := range report.Tables {
		name := strings.ToLower(t.Name)
		switch {
		case strings.HasPrefix(name, prefix):
			out.targetTables = append(out.targetTables, t)
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
// Every target charset must hold every source charset. A failure names the
// target tables that use the offending charset; a warning names only the
// charsets other than utf8mb4.
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
				describeTableNames(tablesUsing(tables.targetTables, target), report.SchemaCharset), target, source,
				prefix, dbserver.CharsetUTF8MB4, dbserver.CharsetUTF8MB4)
		}
	}

	if len(tables.targetCharsets) == 1 && tables.targetCharsets[0] == dbserver.CharsetUTF8MB4 {
		return CheckStatusPassed, "The migration target tables use " + dbserver.CharsetUTF8MB4
	}
	limited := slices.DeleteFunc(slices.Clone(tables.targetCharsets), func(cs string) bool { return cs == dbserver.CharsetUTF8MB4 })
	used := strings.Join(limited, ", ")
	return CheckStatusWarning, fmt.Sprintf(
		"Some migration target tables use %s. After the migration, text outside %s, such as emoji or some scripts, is rejected there.",
		used, used)
}

// tablesUsing returns the names of the tables with a text column in charset.
func tablesUsing(tables []dbserver.TableCharsets, charset string) []string {
	var names []string
	for _, t := range tables {
		if slices.Contains(t.Charsets, charset) {
			names = append(names, t.Name)
		}
	}
	return names
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
