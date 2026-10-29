// internal/api/v2/imports/legacy_cleanup.go
package importsapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/formatutil"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"gorm.io/gorm"
)

// LegacyStatusResponse represents legacy database status for cleanup UI.
type LegacyStatusResponse struct {
	Exists       bool              `json:"exists"`
	CanCleanup   bool              `json:"can_cleanup"`
	Reason       string            `json:"reason,omitempty"`
	ErrorKey     string            `json:"error_key,omitempty"`
	SizeBytes    int64             `json:"size_bytes"`
	TotalRecords int64             `json:"total_records"`
	LastModified *time.Time        `json:"last_modified,omitempty"`
	Location     string            `json:"location"`
	Tables       []LegacyTableInfo `json:"tables"`
}

// LegacyTableInfo provides details about a legacy table (MySQL).
type LegacyTableInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	RowCount  int64  `json:"row_count"`
}

// CleanupActionResponse represents the response for cleanup actions.
type CleanupActionResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	ErrorKey string `json:"error_key,omitempty"`
}

// Cleanup state constants.
const (
	CleanupStateIdle       = "idle"
	CleanupStateInProgress = "in_progress"
	CleanupStateCompleted  = "completed"
	CleanupStateFailed     = "failed"
)

// legacyOnlyTables are the only tables the MySQL cleanup drops: legacy-schema
// tables that no v2 schema (bare or prefixed) ever has, in dependency order
// (children first).
var legacyOnlyTables = []string{
	"note_reviews",
	"note_comments",
	"note_locks",
	"results",
	"notes",
}

// legacySharedTables are legacy-schema tables whose names are also the live
// tables of an unprefixed (bare) v2 schema, because the v2 entities have no
// TableName overrides. The cleanup never drops them: rows written to them after
// the one-time auxiliary copy are never migrated, so they may hold the only copy.
// The migration charset check also uses this list to classify source tables
// (prerequisites.go:910).
var legacySharedTables = []string{
	"hourly_weathers",
	"daily_events",
	"image_caches",
	"dynamic_thresholds",
	"threshold_events",
	"notification_histories",
}

// mysqlLegacyCandidates returns the legacy table names that may be dropped for a
// v2 schema with the given table prefix, in drop order. A bare prefix means the
// legacy-named tables are live v2 tables, so nothing is offered. The shared
// auxiliary tables are never candidates. The result is a fresh slice.
func mysqlLegacyCandidates(tablePrefix string) []string {
	if tablePrefix == "" {
		return nil
	}
	return slices.Clone(legacyOnlyTables)
}

// legacyCleanupRefusal describes why the MySQL legacy cleanup is not offered.
type legacyCleanupRefusal struct {
	exists   bool
	reason   string
	errorKey string
}

// Refusal reasons for the cleanup target checks.
const (
	reasonNoManager        = "Database manager is not available"
	reasonActiveDatabase   = "Target file is the active database, cleanup not allowed for safety"
	reasonPathChangedSince = "Database path changed since start, restart before cleanup"

	reasonStateUnreadable     = "Cannot read migration state"
	reasonMigrationIncomplete = "Migration has not completed, cleanup is not available"
	reasonRelatedDataError    = "Migration recorded an error in related data, cleanup is not allowed for safety"
	reasonAuxiliaryFailed     = "Auxiliary data migration failed, cleanup is not allowed for safety"
	reasonAuxiliaryUnknown    = "Auxiliary data migration outcome was not recorded, cleanup is not allowed for safety"
	reasonFreshInstall        = "The active database was created by a fresh install and has no legacy database"
	reasonNotLegacyDatabase   = "Target file is not a legacy database"
	reasonLegacyUnmigrated    = "Legacy records are missing from the enhanced database, cleanup is not allowed for safety"
	reasonCannotVerify        = "Cannot verify the legacy database"
	reasonNoLegacyDatabase    = "No legacy database found"

	reasonNoConnection            = "Cannot access database connection"
	reasonMySQLManagerUnavailable = "MySQL v2 database manager is not available"
	reasonNoLegacyTables          = "No legacy tables found"
	reasonListTablesFailed        = "Cannot list database tables"
	reasonCleanupCancelled        = "Cleanup was cancelled"
)

// cleanupManager returns the v2 manager the process opened, or a refusal when
// there is none. The cleanup takes its backend and target from it, not from the
// live settings: the connection settings only take effect after a restart.
func (c *Handler) cleanupManager() (datastoreV2.Manager, *legacyCleanupRefusal) {
	if c.V2Manager == nil {
		return nil, &legacyCleanupRefusal{reason: reasonNoManager, errorKey: notification.MsgErrCleanupAccessFailed}
	}
	return c.V2Manager, nil
}

// sqliteLegacyRefusal decides whether legacyPath, read from the settings, is the
// legacy companion of the SQLite database mgr opened. It refuses the active
// database itself and any path whose derived v2 path is not the opened file,
// which is a path changed in settings since the process started. It is pure and
// opens nothing.
func sqliteLegacyRefusal(mgr datastoreV2.Manager, legacyPath string) *legacyCleanupRefusal {
	activePath := filepath.Clean(mgr.Path())
	if filepath.Clean(legacyPath) == activePath {
		return &legacyCleanupRefusal{exists: true, reason: reasonActiveDatabase, errorKey: notification.MsgErrCleanupSafetyCheck}
	}
	if filepath.Clean(datastoreV2.V2MigrationPathFromConfigured(legacyPath)) != activePath {
		return &legacyCleanupRefusal{exists: true, reason: reasonPathChangedSince, errorKey: notification.MsgErrCleanupRestartRequired}
	}
	return nil
}

// migrationStateRefusal reads the migration state of the v2 database mgr opened
// and refuses unless the migration completed without a related-data error and
// the auxiliary data outcome is succeeded or not_required. Failed and unrecorded
// outcomes refuse, so a migration from a build that recorded none is never
// cleaned up. It returns the state for the callers that need the watermark.
func (c *Handler) migrationStateRefusal(ctx context.Context, mgr datastoreV2.Manager) (*entities.MigrationState, *legacyCleanupRefusal) {
	db := mgr.DB()
	if db == nil {
		return nil, &legacyCleanupRefusal{reason: reasonNoManager, errorKey: notification.MsgErrCleanupAccessFailed}
	}
	migrationState, err := datastoreV2.NewStateManager(db.WithContext(ctx)).GetState()
	if err != nil {
		c.LogWarnIfEnabled("Legacy cleanup: failed to read migration state", logger.Error(err))
		return nil, &legacyCleanupRefusal{reason: reasonStateUnreadable, errorKey: notification.MsgErrCleanupAccessFailed}
	}
	if migrationState.State != entities.MigrationStatusCompleted {
		return nil, &legacyCleanupRefusal{exists: true, reason: reasonMigrationIncomplete, errorKey: notification.MsgErrCleanupRestartRequired}
	}
	if migrationState.RelatedDataError != "" {
		return nil, &legacyCleanupRefusal{exists: true, reason: reasonRelatedDataError, errorKey: notification.MsgErrCleanupSafetyCheck}
	}
	switch migrationState.AuxiliaryDataOutcome() {
	case entities.AuxiliaryDataStatusSucceeded, entities.AuxiliaryDataStatusNotRequired:
		return migrationState, nil
	case entities.AuxiliaryDataStatusFailed:
		return nil, &legacyCleanupRefusal{exists: true, reason: reasonAuxiliaryFailed, errorKey: notification.MsgErrCleanupSafetyCheck}
	default:
		return nil, &legacyCleanupRefusal{exists: true, reason: reasonAuxiliaryUnknown, errorKey: notification.MsgErrCleanupSafetyCheck}
	}
}

// sqliteStateRefusal is migrationStateRefusal for the SQLite delete. It also
// refuses a not_required outcome: only a fresh install records it, so that
// database has no legacy companion to delete. Every refusal it returns reports
// the legacy file as existing.
func (c *Handler) sqliteStateRefusal(ctx context.Context, mgr datastoreV2.Manager) (*entities.MigrationState, *legacyCleanupRefusal) {
	migrationState, refusal := c.migrationStateRefusal(ctx, mgr)
	if refusal != nil {
		// A SQLite caller asks about a legacy file it already found, so the
		// refusal reports it as existing even when the state itself is unreadable.
		refusal.exists = true
		return nil, refusal
	}
	if migrationState.AuxiliaryDataOutcome() == entities.AuxiliaryDataStatusNotRequired {
		return nil, &legacyCleanupRefusal{exists: true, reason: reasonFreshInstall, errorKey: notification.MsgErrCleanupNoLegacyDB}
	}
	return migrationState, nil
}

// sqliteMigrationRefusal decides whether the SQLite file at legacyPath may be
// deleted: the migration state must pass sqliteStateRefusal and the file must be
// a legacy database whose tail is in v2. It reads the file, so the POST and the
// background delete call it and the status call does not. Any failure to read or
// prove refuses.
func (c *Handler) sqliteMigrationRefusal(ctx context.Context, mgr datastoreV2.Manager, legacyPath string) *legacyCleanupRefusal {
	migrationState, refusal := c.sqliteStateRefusal(ctx, mgr)
	if refusal != nil {
		return refusal
	}
	err := datastoreV2.VerifySQLiteLegacyMigrated(ctx, legacyPath, mgr.DB(), migrationState.LastMigratedID, apicore.GetLogger())
	return c.legacyProofRefusal(err)
}

// legacyProofRefusal maps the result of a legacy tail proof to a refusal, or nil
// when the proof held. Both backends share it. An unverifiable proof is logged
// here because its error may carry server or path text that must not reach the
// client.
func (c *Handler) legacyProofRefusal(err error) *legacyCleanupRefusal {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, datastoreV2.ErrNotLegacyDatabase):
		return &legacyCleanupRefusal{exists: true, reason: reasonNotLegacyDatabase, errorKey: notification.MsgErrCleanupSafetyCheck}
	case errors.Is(err, datastoreV2.ErrLegacyRecordsUnmigrated):
		return &legacyCleanupRefusal{exists: true, reason: reasonLegacyUnmigrated, errorKey: notification.MsgErrCleanupSafetyCheck}
	default:
		c.LogWarnIfEnabled("Legacy cleanup: cannot verify the legacy database", logger.Error(err))
		return &legacyCleanupRefusal{exists: true, reason: reasonCannotVerify, errorKey: notification.MsgErrCleanupAccessFailed}
	}
}

// legacyTableListing is one row of the information_schema table listing.
type legacyTableListing struct {
	Name string
	Size int64
}

// mysqlCleanupConn returns the connection the MySQL cleanup runs on: the GORM
// handle of the v2 manager the process opened, bound to ctx. The manager's
// database holds both the prefixed v2 schema and the legacy tables. It refuses
// when mgr is not a MySQL manager or has no connection.
func mysqlCleanupConn(ctx context.Context, mgr datastoreV2.Manager) (*gorm.DB, *legacyCleanupRefusal) {
	if mgr == nil || !mgr.IsMySQL() {
		return nil, &legacyCleanupRefusal{reason: reasonMySQLManagerUnavailable, errorKey: notification.MsgErrCleanupAccessFailed}
	}
	db := mgr.DB()
	if db == nil {
		return nil, &legacyCleanupRefusal{reason: reasonNoConnection, errorKey: notification.MsgErrCleanupAccessFailed}
	}
	return db.WithContext(ctx), nil
}

// mysqlResolveMode selects what the MySQL resolver does besides listing.
type mysqlResolveMode int

const (
	// mysqlResolveDrop runs the tail proof and skips row counts, because
	// COUNT(*) can scan a whole large table; the POST pre-check and the
	// background drop use it. It is the zero value, so a caller that forgets the
	// mode gets the proof.
	mysqlResolveDrop mysqlResolveMode = iota
	// mysqlResolveStatus adds an exact row count per table and skips the tail
	// proof; only the status call uses it.
	mysqlResolveStatus
)

// resolveMySQLLegacyTables decides which legacy tables the MySQL cleanup may
// list and drop, from the v2 manager's actual schema. It is the single decision
// shared by the status call, the POST pre-check and the background drop, and it
// fails closed: anything it cannot read refuses. Order: the manager's
// connection, the table prefix (a bare schema has no legacy companion), the
// migration state gate (completed, no related-data error, auxiliary outcome
// succeeded; not_required means a fresh install and refuses as "no legacy
// database"), the table listing, and in drop mode the tail proof
// (VerifyMySQLLegacyMigrated). Only the legacy-only tables are candidates. The
// returned handle is the one the caller runs its DROPs on.
func (c *Handler) resolveMySQLLegacyTables(ctx context.Context, mgr datastoreV2.Manager, mode mysqlResolveMode) (*gorm.DB, []LegacyTableInfo, *legacyCleanupRefusal) {
	db, refusal := mysqlCleanupConn(ctx, mgr)
	if refusal != nil {
		return nil, nil, refusal
	}

	prefix := mgr.TablePrefix()
	if prefix == "" {
		return nil, nil, &legacyCleanupRefusal{reason: reasonNoLegacyTables, errorKey: notification.MsgErrCleanupNoLegacyTables}
	}

	state, refusal := c.migrationStateRefusal(ctx, mgr)
	if refusal != nil {
		return nil, nil, refusal
	}
	if state.AuxiliaryDataOutcome() == entities.AuxiliaryDataStatusNotRequired {
		return nil, nil, &legacyCleanupRefusal{reason: reasonFreshInstall, errorKey: notification.MsgErrCleanupNoLegacyDB}
	}

	sizes, refusal := c.listMySQLTableSizes(db)
	if refusal != nil {
		return nil, nil, refusal
	}

	var tables []LegacyTableInfo
	for _, name := range mysqlLegacyCandidates(prefix) {
		size, ok := sizes[name]
		if !ok {
			continue
		}
		tables = append(tables, LegacyTableInfo{Name: name, SizeBytes: size})
	}
	if len(tables) == 0 {
		return nil, nil, &legacyCleanupRefusal{reason: reasonNoLegacyTables, errorKey: notification.MsgErrCleanupNoLegacyTables}
	}

	if mode != mysqlResolveStatus {
		err := datastoreV2.VerifyMySQLLegacyMigrated(ctx, db, prefix, state.LastMigratedID, apicore.GetLogger())
		if refusal := c.legacyProofRefusal(err); refusal != nil {
			return nil, nil, refusal
		}
		return db, tables, nil
	}

	for i := range tables {
		name := tables[i].Name
		if err := db.Raw("SELECT COUNT(*) FROM `" + name + "`").Scan(&tables[i].RowCount).Error; err != nil {
			if ctx.Err() != nil {
				return nil, nil, &legacyCleanupRefusal{reason: reasonCleanupCancelled, errorKey: notification.MsgErrCleanupAccessFailed}
			}
			// The count is informational and never decides what is dropped.
			c.LogWarnIfEnabled("Legacy cleanup: failed to query row count",
				logger.String("table", name),
				logger.Error(err))
			tables[i].RowCount = 0
		}
	}
	return db, tables, nil
}

// listMySQLTableSizes returns the size in bytes of each base table in the
// current database, keyed by lower-cased name (MySQL 8 and MariaDB differ in the
// case of returned names).
func (c *Handler) listMySQLTableSizes(db *gorm.DB) (map[string]int64, *legacyCleanupRefusal) {
	var listing []legacyTableListing
	err := db.Raw(`
		SELECT table_name AS name, COALESCE(data_length, 0) + COALESCE(index_length, 0) AS size
		FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'`).Scan(&listing).Error
	if err != nil {
		c.LogWarnIfEnabled("Legacy cleanup: failed to list tables", logger.Error(err))
		return nil, &legacyCleanupRefusal{reason: reasonListTablesFailed, errorKey: notification.MsgErrCleanupAccessFailed}
	}
	sizes := make(map[string]int64, len(listing))
	for _, row := range listing {
		sizes[strings.ToLower(row.Name)] = row.Size
	}
	return sizes, nil
}

// sumLegacyTableSizes returns the total size of the resolved tables.
func sumLegacyTableSizes(tables []LegacyTableInfo) int64 {
	var total int64
	for _, t := range tables {
		total += t.SizeBytes
	}
	return total
}

// CleanupStatus tracks the state of legacy database cleanup.
// Thread-safe for concurrent access.
type CleanupStatus struct {
	mu              sync.RWMutex
	state           string
	errorMsg        string
	tablesRemaining []string
	spaceReclaimed  int64
}

// NewCleanupStatus creates a new cleanup status tracker initialized to idle.
func NewCleanupStatus() *CleanupStatus {
	return &CleanupStatus{
		state: CleanupStateIdle,
	}
}

// Get returns the current cleanup state safely.
// Returns a copy of tablesRemaining to prevent callers from observing concurrent mutations.
func (cs *CleanupStatus) Get() (state, errMsg string, remaining []string, reclaimed int64) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	remainingCopy := slices.Clone(cs.tablesRemaining)
	return cs.state, cs.errorMsg, remainingCopy, cs.spaceReclaimed
}

// Set sets the cleanup state safely.
func (cs *CleanupStatus) Set(state, errMsg string, remaining []string, reclaimed int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.state = state
	cs.errorMsg = errMsg
	cs.tablesRemaining = remaining
	cs.spaceReclaimed = reclaimed
}

// Reset resets the cleanup state to idle.
func (cs *CleanupStatus) Reset() {
	cs.Set(CleanupStateIdle, "", nil, 0)
}

// TryStart atomically checks if cleanup can start and sets state to in_progress.
// Returns true if cleanup was started, false if already in progress.
// This prevents race conditions between concurrent cleanup requests.
func (cs *CleanupStatus) TryStart() bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.state == CleanupStateInProgress {
		return false
	}
	cs.state = CleanupStateInProgress
	cs.errorMsg = ""
	cs.tablesRemaining = nil
	cs.spaceReclaimed = 0
	return true
}

// RegisterLegacyCleanupRoutes registers the legacy cleanup API routes on the supplied v2 group.
func (c *Handler) RegisterLegacyCleanupRoutes(g *echo.Group) {
	c.LogInfoIfEnabled("Initializing legacy cleanup routes")

	// Initialize cleanup status tracker
	c.cleanupStatus = NewCleanupStatus()

	// Create legacy API group under system/database
	legacyGroup := g.Group("/system/database/legacy")

	// Get the appropriate auth middleware
	authMiddleware := c.AuthMiddleware

	// Create auth-protected group
	protectedGroup := legacyGroup.Group("", authMiddleware)

	// Legacy status and cleanup routes
	protectedGroup.GET("/status", c.GetLegacyStatus)
	protectedGroup.POST("/cleanup", c.StartLegacyCleanup)

	c.LogInfoIfEnabled("Legacy cleanup routes initialized successfully")
}

// GetLegacyStatus handles GET /api/v2/system/database/legacy/status
// Returns information about the legacy database for the cleanup UI.
func (c *Handler) GetLegacyStatus(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Getting legacy database status", logger.String("path", path), logger.String("ip", ip))

	response := LegacyStatusResponse{
		Tables: []LegacyTableInfo{},
	}

	mgr, refusal := c.cleanupManager()
	if refusal != nil {
		response.Location = c.CurrentSettings().Output.SQLite.Path
		return writeLegacyRefusal(ctx, &response, refusal)
	}

	// The opened database decides the backend, not the live settings.
	if mgr.IsMySQL() {
		return c.getLegacyStatusMySQL(ctx, &response, mgr)
	}

	return c.getLegacyStatusSQLite(ctx, &response, mgr)
}

// writeLegacyRefusal answers a status request with the refusal.
func writeLegacyRefusal(ctx echo.Context, response *LegacyStatusResponse, refusal *legacyCleanupRefusal) error {
	response.Exists = refusal.exists
	response.CanCleanup = false
	response.Reason = refusal.reason
	response.ErrorKey = refusal.errorKey
	return ctx.JSON(http.StatusOK, response)
}

// getLegacyStatusSQLite handles legacy status for SQLite deployments.
func (c *Handler) getLegacyStatusSQLite(ctx echo.Context, response *LegacyStatusResponse, mgr datastoreV2.Manager) error {
	legacyPath := c.CurrentSettings().Output.SQLite.Path
	response.Location = legacyPath

	// Check if legacy file exists
	fileInfo, err := os.Stat(legacyPath)
	if os.IsNotExist(err) {
		response.Exists = false
		response.CanCleanup = false
		response.Reason = reasonNoLegacyDatabase
		response.ErrorKey = notification.MsgErrCleanupNoLegacyDB
		return ctx.JSON(http.StatusOK, response)
	}
	if err != nil {
		response.Exists = false
		response.CanCleanup = false
		response.Reason = fmt.Sprintf("Cannot access legacy database: %v", err)
		response.ErrorKey = notification.MsgErrCleanupAccessFailed
		return ctx.JSON(http.StatusOK, response)
	}

	response.Exists = true
	response.SizeBytes = fileInfo.Size()
	modTime := fileInfo.ModTime()
	response.LastModified = &modTime

	// Add WAL and SHM file sizes if they exist
	if walInfo, err := os.Stat(legacyPath + "-wal"); err == nil {
		response.SizeBytes += walInfo.Size()
	}
	if shmInfo, err := os.Stat(legacyPath + "-shm"); err == nil {
		response.SizeBytes += shmInfo.Size()
	}

	// Check if we're in v2-only mode (required for cleanup)
	if !getIsV2OnlyMode() {
		response.CanCleanup = false
		response.Reason = "Application must be restarted after migration before cleanup is available"
		response.ErrorKey = notification.MsgErrCleanupRestartRequired
		return ctx.JSON(http.StatusOK, response)
	}

	// The path must be the legacy companion of the database this process
	// opened. Checked before the schema probe so an unowned file is never opened.
	if refusal := sqliteLegacyRefusal(mgr, legacyPath); refusal != nil {
		return writeLegacyRefusal(ctx, response, refusal)
	}

	// CRITICAL SAFETY CHECK: Ensure target is not a V2 database
	if datastoreV2.CheckSQLiteHasV2Schema(legacyPath) {
		response.CanCleanup = false
		response.Reason = "Target file appears to be a V2 database - cleanup not allowed for safety"
		response.ErrorKey = notification.MsgErrCleanupSafetyCheck
		return ctx.JSON(http.StatusOK, response)
	}

	// The migration state is one row read. The tail proof scans the legacy
	// file, so only the POST runs it; the card may then be offered and the POST
	// refuse with a message.
	if _, refusal := c.sqliteStateRefusal(ctx.Request().Context(), mgr); refusal != nil {
		return writeLegacyRefusal(ctx, response, refusal)
	}

	response.CanCleanup = true
	response.Tables = []LegacyTableInfo{
		{Name: "notes"},
		{Name: "results"},
		{Name: "note_reviews"},
		{Name: "note_comments"},
		{Name: "note_locks"},
		{Name: "daily_events"},
		{Name: "hourly_weathers"},
		{Name: "image_caches"},
		{Name: "dynamic_thresholds"},
		{Name: "threshold_events"},
		{Name: "notification_histories"},
	}

	return ctx.JSON(http.StatusOK, response)
}

// getLegacyStatusMySQL handles legacy status for MySQL deployments.
func (c *Handler) getLegacyStatusMySQL(ctx echo.Context, response *LegacyStatusResponse, mgr datastoreV2.Manager) error {
	response.Location = c.CurrentSettings().Output.MySQL.Database

	// Check if we're in v2-only mode (required for cleanup)
	if !getIsV2OnlyMode() {
		response.Exists = true // Assume exists for MySQL
		response.CanCleanup = false
		response.Reason = "Application must be restarted after migration before cleanup is available"
		response.ErrorKey = notification.MsgErrCleanupRestartRequired
		return ctx.JSON(http.StatusOK, response)
	}

	// Bind the request context so the schema queries below abort if the
	// client disconnects, rather than running unbounded on the request thread.
	reqCtx := ctx.Request().Context()
	_, tables, refusal := c.resolveMySQLLegacyTables(reqCtx, mgr, mysqlResolveStatus)
	if refusal == nil {
		var totalRows int64
		for _, t := range tables {
			totalRows += t.RowCount
		}
		response.Exists = true
		response.CanCleanup = true
		response.SizeBytes = sumLegacyTableSizes(tables)
		response.TotalRecords = totalRows
		response.Tables = tables
		return ctx.JSON(http.StatusOK, response)
	}

	response.Exists = refusal.exists
	response.CanCleanup = false
	response.Reason = refusal.reason
	response.ErrorKey = refusal.errorKey
	return ctx.JSON(http.StatusOK, response)
}

// StartLegacyCleanup handles POST /api/v2/system/database/legacy/cleanup
// Initiates asynchronous legacy database cleanup. For MySQL the tables are
// resolved first, including the proof that the legacy tail is in v2, and a
// refusal answers 400 without starting anything; the background drop resolves
// and proves again immediately before it drops. For SQLite the target is
// checked against the opened database, then the migration state and the legacy
// file are proven (see sqliteMigrationRefusal), and the background delete
// proves them again. The backend and the SQLite target are decided once here,
// from the v2 manager the process opened, and handed to the background job,
// which reads no settings.
func (c *Handler) StartLegacyCleanup(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Starting legacy database cleanup", logger.String("path", path), logger.String("ip", ip))

	// Check if we're in v2-only mode (check before trying to start)
	if !getIsV2OnlyMode() {
		return ctx.JSON(http.StatusBadRequest, CleanupActionResponse{
			Success:  false,
			Message:  "Cannot cleanup: Application must be restarted after migration",
			ErrorKey: notification.MsgErrCleanupRestartNeeded,
		})
	}

	mgr, refusal := c.cleanupManager()
	if refusal != nil {
		return ctx.JSON(http.StatusBadRequest, CleanupActionResponse{
			Success:  false,
			Message:  refusal.reason,
			ErrorKey: refusal.errorKey,
		})
	}

	// Resolve the target before scheduling anything, so a refusal is a
	// synchronous answer and leaves the cleanup state idle.
	usingMySQL := mgr.IsMySQL()
	var legacyPath string
	var mysqlSize int64
	if !usingMySQL {
		legacyPath = c.CurrentSettings().Output.SQLite.Path
		refusal := sqliteLegacyRefusal(mgr, legacyPath)
		if refusal == nil {
			// A missing file is a plain "nothing to delete", not a failed proof.
			if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
				refusal = &legacyCleanupRefusal{reason: reasonNoLegacyDatabase, errorKey: notification.MsgErrCleanupNoLegacyDB}
			}
		}
		if refusal == nil {
			refusal = c.sqliteMigrationRefusal(ctx.Request().Context(), mgr, legacyPath)
		}
		if refusal != nil {
			errorKey := refusal.errorKey
			if errorKey == notification.MsgErrCleanupRestartRequired {
				errorKey = notification.MsgErrCleanupRestartNeeded
			}
			return ctx.JSON(http.StatusBadRequest, CleanupActionResponse{
				Success:  false,
				Message:  refusal.reason,
				ErrorKey: errorKey,
			})
		}
	} else {
		var tables []LegacyTableInfo
		_, tables, refusal = c.resolveMySQLLegacyTables(ctx.Request().Context(), mgr, mysqlResolveDrop)
		mysqlSize = sumLegacyTableSizes(tables)
		if refusal != nil {
			return ctx.JSON(http.StatusBadRequest, CleanupActionResponse{
				Success:  false,
				Message:  refusal.reason,
				ErrorKey: refusal.errorKey,
			})
		}
	}

	// Atomically check and set cleanup state to prevent race conditions
	if !c.cleanupStatus.TryStart() {
		return ctx.JSON(http.StatusConflict, CleanupActionResponse{
			Success:  false,
			Message:  "Cleanup already in progress",
			ErrorKey: notification.MsgErrCleanupAlreadyRunning,
		})
	}

	// Get size before deletion for reporting
	sizeBytes := mysqlSize
	if !usingMySQL {
		sizeBytes = c.getSQLiteLegacySize(legacyPath)
	}

	// Start async cleanup using WaitGroup for proper lifecycle management
	c.Go(func() {

		cleanupCtx := c.Context()
		if cleanupCtx == nil {
			cleanupCtx = context.Background()
		}

		var err error
		var remaining []string

		if usingMySQL {
			remaining, err = c.cleanupMySQLLegacy(cleanupCtx, mgr)
		} else {
			err = c.cleanupSQLiteLegacy(cleanupCtx, mgr, legacyPath)
		}

		if err != nil {
			c.LogErrorIfEnabled("Legacy cleanup failed", logger.Error(err))
			c.cleanupStatus.Set(CleanupStateFailed, err.Error(), remaining, 0)
			c.sendCleanupNotification(false, 0, err.Error())
		} else {
			c.LogInfoIfEnabled("Legacy cleanup completed successfully",
				logger.Int64("space_reclaimed_bytes", sizeBytes),
				logger.String("space_reclaimed", formatutil.Bytes(sizeBytes)))
			c.cleanupStatus.Set(CleanupStateCompleted, "", nil, sizeBytes)
			c.sendCleanupNotification(true, sizeBytes, "")
		}
	})

	c.LogInfoIfEnabled("Legacy cleanup started", logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, CleanupActionResponse{
		Success: true,
		Message: "Legacy database cleanup started",
	})
}

// cleanupSQLiteLegacy deletes the legacy SQLite database files at legacyPath,
// the path the POST checked. It re-checks that the path is the legacy companion
// of the database mgr opened and that the migration is proven for it (state,
// auxiliary outcome and legacy tail), and deletes nothing otherwise.
func (c *Handler) cleanupSQLiteLegacy(ctx context.Context, mgr datastoreV2.Manager, legacyPath string) error {
	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.LogInfoIfEnabled("Starting SQLite legacy database cleanup",
		logger.String("path", legacyPath))

	if refusal := sqliteLegacyRefusal(mgr, legacyPath); refusal != nil {
		c.LogErrorIfEnabled("Safety check failed: target is not the legacy database of the opened database",
			logger.String("path", legacyPath))
		return fmt.Errorf("legacy cleanup refused: %s", refusal.reason)
	}

	// The migration state and the target may have changed since the POST.
	if refusal := c.sqliteMigrationRefusal(ctx, mgr, legacyPath); refusal != nil {
		c.LogErrorIfEnabled("Safety check failed: migration is not proven for the legacy database",
			logger.String("reason", refusal.reason))
		return fmt.Errorf("legacy cleanup refused: %s", refusal.reason)
	}

	// CRITICAL SAFETY CHECK: Ensure we're not deleting a V2 database
	if datastoreV2.CheckSQLiteHasV2Schema(legacyPath) {
		c.LogErrorIfEnabled("Safety check failed: target is a V2 database",
			logger.String("path", legacyPath))
		return fmt.Errorf("target file appears to be a V2 database - cleanup aborted for safety")
	}

	c.LogInfoIfEnabled("Safety check passed: confirmed legacy database",
		logger.String("path", legacyPath))

	// Delete main database file
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete legacy database: %w", err)
	}
	c.LogInfoIfEnabled("Deleted legacy database file",
		logger.String("file", legacyPath))

	// Delete WAL and SHM files if they exist (ignore errors)
	walDeleted := os.Remove(legacyPath+"-wal") == nil
	shmDeleted := os.Remove(legacyPath+"-shm") == nil

	c.LogInfoIfEnabled("SQLite legacy cleanup completed",
		logger.String("path", legacyPath),
		logger.Bool("wal_deleted", walDeleted),
		logger.Bool("shm_deleted", shmDeleted))

	return nil
}

// cleanupMySQLLegacy drops the legacy tables the resolver allows, on the
// connection of mgr. It resolves and proves the legacy tail again immediately
// before the first DROP, because the database may have changed since the POST was
// accepted, and drops nothing when the resolver refuses. The bound context lets
// an in-flight DROP abort on shutdown. On a failure or cancellation it returns
// the names not dropped.
func (c *Handler) cleanupMySQLLegacy(ctx context.Context, mgr datastoreV2.Manager) ([]string, error) {
	db, tables, refusal := c.resolveMySQLLegacyTables(ctx, mgr, mysqlResolveDrop)
	if refusal != nil {
		return nil, fmt.Errorf("legacy cleanup refused: %s", refusal.reason)
	}
	remaining, err := c.dropMySQLLegacyTables(ctx, db, tables)
	if err == nil {
		c.LogInfoIfEnabled("MySQL legacy cleanup kept the shared auxiliary tables, which may hold rows the migration never copied",
			logger.String("kept_tables", strings.Join(legacySharedTables, ",")))
	}
	return remaining, err
}

// dropMySQLLegacyTables drops the resolved tables in order and stops at the first
// error or cancellation, returning the names left behind.
func (c *Handler) dropMySQLLegacyTables(ctx context.Context, db *gorm.DB, tables []LegacyTableInfo) ([]string, error) {
	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = t.Name
	}
	c.LogInfoIfEnabled("Starting MySQL legacy tables cleanup",
		logger.Int("total_tables", len(names)))

	for i, tableName := range names {
		// Check for cancellation before each table
		select {
		case <-ctx.Done():
			c.LogWarnIfEnabled("MySQL cleanup cancelled",
				logger.Int("tables_dropped", i),
				logger.Int("tables_remaining", len(names)-i))
			return names[i:], ctx.Err()
		default:
		}

		// Names come only from the fixed legacy lists.
		if err := db.Exec("DROP TABLE IF EXISTS `" + tableName + "`").Error; err != nil {
			c.LogErrorIfEnabled("Failed to drop legacy table",
				logger.String("table", tableName),
				logger.Error(err))
			// Stop on first error, return remaining tables
			return names[i:], fmt.Errorf("failed to drop table %s: %w", tableName, err)
		}

		c.LogInfoIfEnabled("Dropped legacy table",
			logger.String("table", tableName))
	}

	c.LogInfoIfEnabled("MySQL legacy cleanup completed",
		logger.Int("tables_dropped", len(names)))

	return nil, nil
}

// getSQLiteLegacySize returns the total size of SQLite legacy files.
func (c *Handler) getSQLiteLegacySize(legacyPath string) int64 {
	var totalSize int64

	if info, err := os.Stat(legacyPath); err == nil {
		totalSize += info.Size()
	}
	if info, err := os.Stat(legacyPath + "-wal"); err == nil {
		totalSize += info.Size()
	}
	if info, err := os.Stat(legacyPath + "-shm"); err == nil {
		totalSize += info.Size()
	}

	return totalSize
}

// sendCleanupNotification sends a notification about cleanup status.
func (c *Handler) sendCleanupNotification(success bool, spaceReclaimed int64, errMsg string) {
	notifService := c.getNotificationService()
	if notifService == nil {
		return
	}

	var title, body, titleKey, messageKey string
	var messageParams map[string]any
	var priority notification.Priority

	if success {
		title = "Legacy Database Cleanup Complete"
		body = fmt.Sprintf("Successfully removed legacy database. %s disk space reclaimed.",
			formatutil.Bytes(spaceReclaimed))
		priority = notification.PriorityMedium
		titleKey = notification.MsgCleanupCompleteTitle
		messageKey = notification.MsgCleanupCompleteMessage
		messageParams = map[string]any{"space": formatutil.Bytes(spaceReclaimed)}
	} else {
		title = "Legacy Database Cleanup Failed"
		body = fmt.Sprintf("Failed to remove legacy database: %s", errMsg)
		priority = notification.PriorityHigh
		titleKey = notification.MsgCleanupFailedTitle
		messageKey = notification.MsgCleanupFailedMessage
		messageParams = nil
	}

	// Build notification fully before broadcast to ensure SSE subscribers see translation keys
	notif := notification.NewNotification(
		notification.TypeSystem,
		priority,
		title,
		body,
	).WithComponent("database").
		WithTitleKey(titleKey, nil).
		WithMessageKey(messageKey, messageParams)

	if err := notifService.CreateWithMetadata(notif); err != nil {
		c.LogWarnIfEnabled("Failed to send cleanup notification", logger.Error(err))
	}
}
