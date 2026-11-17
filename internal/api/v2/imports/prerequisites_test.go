// prerequisites_test.go: tests for the prerequisites API endpoint.
package importsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	"github.com/tphakala/birdnet-go/internal/notification"

	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"gorm.io/gorm"
)

// prerequisiteCheck names a check by ID, severity and status. An empty status
// means the status is not pinned because it depends on the host.
type prerequisiteCheck struct{ id, severity, status string }

// memoryCheckID is the prerequisite whose status depends on the host's RAM.
const memoryCheckID = "memory_available"

// sqliteIntegrityCheckID is the prerequisite that is skipped in v2-only mode.
const sqliteIntegrityCheckID = "sqlite_integrity"

// sqlitePrerequisiteChecks is the exact sequence GetPrerequisites reports for a
// SQLite setup: the critical checks first, then the warnings.
var sqlitePrerequisiteChecks = []prerequisiteCheck{
	{"state_idle", CheckSeverityCritical, CheckStatusPassed},
	{"disk_space", CheckSeverityCritical, CheckStatusPassed},
	{"legacy_accessible", CheckSeverityCritical, CheckStatusPassed},
	{sqliteIntegrityCheckID, CheckSeverityCritical, CheckStatusPassed},
	{"write_permission", CheckSeverityCritical, CheckStatusPassed},
	{"record_count", CheckSeverityCritical, CheckStatusPassed},
	{"existing_v2_data", CheckSeverityWarning, CheckStatusSkipped},
	{memoryCheckID, CheckSeverityWarning, ""},
}

// TestGetPrerequisites_SQLite pins the prerequisites snapshot after a run for a healthy
// SQLite setup: every check is reported in order with a valid status, nothing
// blocks, and v2-only mode withholds the go-ahead without failing a check.
// Note: Not parallel due to global state modification.
func TestGetPrerequisites_SQLite(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	for _, v2Only := range []bool{false, true} {
		t.Run(fmt.Sprintf("v2Only=%t", v2Only), func(t *testing.T) {
			e, controller, _ := setupPrerequisitesTestEnvironment(t)
			setV2OnlyMode(t, v2Only)

			// GET only reads the snapshot: start the run, wait for it, then read
			post := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/prerequisites/run", http.NoBody)
			require.NoError(t, controller.RunPrerequisites(e.NewContext(post, httptest.NewRecorder())))
			controller.Wait()

			req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/prerequisites", http.NoBody)
			rec := httptest.NewRecorder()
			require.NoError(t, controller.GetPrerequisites(e.NewContext(req, rec)))
			require.Equal(t, http.StatusOK, rec.Code)

			var response PrerequisitesResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))

			assert.True(t, response.AllPassed, "AllPassed should be true when no critical check fails")
			assert.Equal(t, !v2Only, response.CanStartMigration, "v2-only mode withholds the go-ahead")
			assert.Equal(t, 0, response.CriticalFailures, "Should have no critical failures")
			assert.False(t, response.CheckedAt.IsZero(), "CheckedAt should be set")

			// The memory check depends on the host, so only its ID and severity are pinned.
			got := make([]prerequisiteCheck, 0, len(response.Checks))
			for _, check := range response.Checks {
				status := check.Status
				if check.ID == memoryCheckID {
					// Host-dependent, but it must still be one of the defined statuses.
					assert.Contains(t, []string{CheckStatusPassed, CheckStatusWarning, CheckStatusFailed, CheckStatusSkipped, CheckStatusError},
						status, "memory check status should be valid")
					status = ""
				}
				got = append(got, prerequisiteCheck{check.ID, check.Severity, status})
				assert.NotEmpty(t, check.Name, "Check %s should have a name", check.ID)
			}
			want := slices.Clone(sqlitePrerequisiteChecks)
			if v2Only {
				// The integrity result is never read in v2-only mode.
				i := slices.IndexFunc(want, func(c prerequisiteCheck) bool { return c.id == sqliteIntegrityCheckID })
				require.NotEqual(t, -1, i)
				want[i].status = CheckStatusSkipped
			}
			assert.Equal(t, want, got)
		})
	}
}

// TestPrerequisitesEndpoints_RunAndPoll pins the handler wiring: GET alone
// starts nothing, POST run starts the background run, and GET then returns the
// completed snapshot with the critical checks.
// Note: Not parallel due to global state modification.
func TestPrerequisitesEndpoints_RunAndPoll(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")
	e, controller, _ := setupPrerequisitesTestEnvironment(t)

	get := func() PrerequisitesResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/database/migration/prerequisites", http.NoBody)
		require.NoError(t, controller.GetPrerequisites(e.NewContext(req, rec)))
		require.Equal(t, http.StatusOK, rec.Code)
		var resp PrerequisitesResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		return resp
	}

	idle := get()
	assert.Equal(t, PrereqRunStatusIdle, idle.Status)
	assert.Empty(t, idle.Checks)
	assert.False(t, idle.CanStartMigration)
	assert.Equal(t, PrereqRunStatusIdle, get().Status, "GET must never start a run")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/prerequisites/run", http.NoBody)
	require.NoError(t, controller.RunPrerequisites(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code)
	var started PrerequisitesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &started))
	assert.Contains(t, []string{PrereqRunStatusRunning, PrereqRunStatusCompleted}, started.Status)
	assert.Positive(t, started.RunID)

	// Wait returns once the Core's tracked goroutines, the run included, have ended
	controller.Wait()
	done := get()
	require.Equal(t, PrereqRunStatusCompleted, done.Status)

	assert.Equal(t, started.RunID, done.RunID)
	assert.True(t, done.AllPassed)
	assert.True(t, done.CanStartMigration)
	assert.Equal(t, 0, done.CriticalFailures)
	assert.False(t, done.CheckedAt.IsZero())
	assert.Nil(t, done.Progress)

	checkIDs := make(map[string]bool)
	for _, check := range done.Checks {
		checkIDs[check.ID] = true
		assert.NotEmpty(t, check.Name)
		assert.Contains(t, []string{CheckStatusPassed, CheckStatusFailed, CheckStatusWarning, CheckStatusSkipped, CheckStatusError}, check.Status)
		assert.Contains(t, []string{CheckSeverityCritical, CheckSeverityWarning}, check.Severity)
	}
	for _, id := range []string{"state_idle", "disk_space", "legacy_accessible", "record_count", "existing_v2_data", "memory_available"} {
		assert.True(t, checkIDs[id], "missing check %s", id)
	}
}

// TestRunPrerequisites_ForceStartsANewRun pins that the request body reaches the
// runner: a plain POST reuses a fresh passing result and {"force": true} does not.
func TestRunPrerequisites_ForceStartsANewRun(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	e, controller, _ := setupPrerequisitesTestEnvironment(t)
	post := func(body string) PrerequisitesResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/prerequisites/run", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		require.NoError(t, controller.RunPrerequisites(e.NewContext(req, rec)))
		var resp PrerequisitesResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		return resp
	}

	first := post(`{}`)
	controller.Wait()
	reused := post(`{"force": false}`)
	forced := post(`{"force": true}`)
	controller.Wait()

	assert.Equal(t, first.RunID, reused.RunID, "a plain POST reuses the fresh passing result")
	assert.Greater(t, forced.RunID, first.RunID, "force starts a new run")
}

// TestCheckMySQLPermissions_CleanupSurvivesCancellation pins that the test table
// is dropped even when the context ends after the table was created: the DROP
// runs on a context detached from the run's.
func TestCheckMySQLPermissions_CleanupSurvivesCancellation(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)
	db := controller.getLegacyGormDB()
	require.NotNil(t, db)
	db = db.Session(&gorm.Session{NewDB: true})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Cancel as soon as the CREATE has run, before the INSERT
	const callbackName = "test:cancel_after_create"
	require.NoError(t, db.Callback().Raw().After("gorm:raw").Register(callbackName, func(tx *gorm.DB) {
		if strings.HasPrefix(tx.Statement.SQL.String(), "CREATE TABLE") {
			cancel()
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Raw().Remove(callbackName) })

	check := controller.checkMySQLPermissions(ctx)

	assert.Equal(t, CheckStatusFailed, check.Status, "the INSERT after the cancel must fail")
	var leftover int64
	require.NoError(t, db.Raw("SELECT count(*) FROM sqlite_master WHERE name LIKE 'v2_permission_test_%'").Scan(&leftover).Error)
	assert.Zero(t, leftover, "the permission test table must be dropped")
}

// TestRunPrerequisites_RunContext pins what the run is bound to: the server
// lifecycle context, not the request that started it.
func TestRunPrerequisites_RunContext(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	post := func(controller *Handler, e *echo.Echo, reqCtx context.Context) PrerequisitesResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/prerequisites/run", http.NoBody).WithContext(reqCtx)
		rec := httptest.NewRecorder()
		require.NoError(t, controller.RunPrerequisites(e.NewContext(req, rec)))
		var resp PrerequisitesResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		return resp
	}

	t.Run("a cancelled request does not cancel the run", func(t *testing.T) {
		e, controller, _ := setupPrerequisitesTestEnvironment(t)
		reqCtx, cancel := context.WithCancel(t.Context())
		cancel()

		post(controller, e, reqCtx)
		controller.Wait()

		assert.Equal(t, PrereqRunStatusCompleted, controller.prereqRunner().snapshot().Status)
	})

	t.Run("a cancelled lifecycle context starts no run", func(t *testing.T) {
		e, controller, _ := setupPrerequisitesTestEnvironment(t)
		lifecycle, cancel := context.WithCancel(t.Context())
		controller.SetTestContext(lifecycle, cancel)
		cancel()

		resp := post(controller, e, t.Context())

		assert.Equal(t, PrereqRunStatusFailed, resp.Status)
		assert.Equal(t, PrereqRunStatusIdle, controller.prereqRunner().snapshot().Status)
	})
}

// TestDBFingerprint_FollowsSettings pins that every database setting the checks
// read changes the fingerprint, so a changed setting forces a new run.
func TestDBFingerprint_FollowsSettings(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	mutations := map[string]func(s *conf.Settings){
		"mysql enabled":  func(s *conf.Settings) { s.Output.MySQL.Enabled = true },
		"mysql host":     func(s *conf.Settings) { s.Output.MySQL.Host = "db.example" },
		"mysql port":     func(s *conf.Settings) { s.Output.MySQL.Port = "3307" },
		"mysql database": func(s *conf.Settings) { s.Output.MySQL.Database = "other" },
		"mysql username": func(s *conf.Settings) { s.Output.MySQL.Username = "other_user" },
		"mysql password": func(s *conf.Settings) { s.Output.MySQL.Password = "other_secret" },
		"sqlite path":    func(s *conf.Settings) { s.Output.SQLite.Path = filepath.Join(t.TempDir(), "other.db") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			_, controller, _ := setupPrerequisitesTestEnvironment(t)
			before := controller.dbFingerprint()

			mutate(controller.Settings.Load())

			assert.NotEqual(t, before, controller.dbFingerprint())
		})
	}
}

// TestDBFingerprint_HidesCredentials pins that the fingerprint, which can end up
// in logs and failure output, never carries the MySQL password or user name.
func TestDBFingerprint_HidesCredentials(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	const secret = "hunter2-secret"
	_, controller, _ := setupPrerequisitesTestEnvironment(t)
	controller.Settings.Load().Output.MySQL.Password = secret
	controller.Settings.Load().Output.MySQL.Username = "fingerprint_user"

	rendered := fmt.Sprintf("%v %+v %#v", controller.dbFingerprint(), controller.dbFingerprint(), controller.dbFingerprint())

	assert.NotContains(t, rendered, secret)
	assert.NotContains(t, rendered, "fingerprint_user")
}

// TestRunPrerequisites_LogsOnlyRealRuns pins the log trail of the prerequisites
// run: one line when a run starts and one with its outcome when it ends; a POST
// that reuses the result logs neither.
func TestRunPrerequisites_LogsOnlyRealRuns(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	buf := logtest.CaptureBufferAt(t, slog.LevelInfo)
	e, controller, _ := setupPrerequisitesTestEnvironment(t)
	controller.APILogger = logger.Global().Module("api")
	post := func() {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/prerequisites/run", http.NoBody)
		require.NoError(t, controller.RunPrerequisites(e.NewContext(req, httptest.NewRecorder())))
	}

	post()
	controller.Wait()
	post() // reuses the fresh passing result

	out := buf.String()
	assert.Equal(t, 1, strings.Count(out, msgPrereqRunStarting), out)
	require.Equal(t, 1, strings.Count(out, msgPrereqRunFinished), out)
	for _, field := range []string{"run_id=1", "status=completed", "can_start=true", "critical_failures=0", "warnings=", "duration="} {
		assert.Contains(t, out, field)
	}
}

// TestRunPrerequisites_InvalidBody pins the 400 with its message key for a malformed body.
func TestRunPrerequisites_InvalidBody(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	e, controller, _ := setupPrerequisitesTestEnvironment(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/prerequisites/run", strings.NewReader("{not json"))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.RunPrerequisites(e.NewContext(req, rec)))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), notification.MsgErrMigrationInvalidBody)
	assert.Equal(t, PrereqRunStatusIdle, controller.prereqRunner().snapshot().Status, "a rejected request starts no run")
}

// TestCheckStateIdle_Idle tests checkStateIdle when migration is in IDLE state.
func TestCheckStateIdle_Idle(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkStateIdle()

	assert.Equal(t, "state_idle", check.ID)
	assert.Equal(t, CheckStatusPassed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "IDLE")
}

// checkStateIdleSwapChecks is how many times checkStateIdle runs in
// TestCheckStateIdle_ConcurrentStateManagerSwap. The writer swaps the state
// manager for as long as those checks run, so its swap count is not bounded.
const checkStateIdleSwapChecks = 200

// TestCheckStateIdle_ConcurrentStateManagerSwap pins that checkStateIdle reads
// the process-wide state manager once, under migrationMu: while another
// goroutine keeps swapping it between nil and a real manager, the check never
// dereferences a nil manager and never races the writer. It reports either an
// idle state or the missing-manager error, whichever manager it saw.
// Note: Not parallel due to global state modification.
func TestCheckStateIdle_ConcurrentStateManagerSwap(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, sm := setupPrerequisitesTestEnvironment(t)

	swapOnce := func() {
		SetMigrationDependencies(nil, nil)
		SetMigrationDependencies(sm, nil)
	}
	stop := make(chan struct{})
	started := make(chan struct{})
	var swapper sync.WaitGroup
	swapper.Go(func() {
		swapOnce()
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				swapOnce()
			}
		}
	})
	defer func() {
		close(stop)
		swapper.Wait()
	}()
	<-started

	for range checkStateIdleSwapChecks {
		check := controller.checkStateIdle()
		assert.Contains(t, []string{CheckStatusPassed, CheckStatusError}, check.Status)
	}
}

// TestCheckStateIdle_NotIdle tests checkStateIdle when migration is in a non-IDLE state.
func TestCheckStateIdle_NotIdle(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, sm := setupPrerequisitesTestEnvironment(t)

	// Set state to migrating by going through the state machine
	err := sm.StartMigration(100)
	require.NoError(t, err)
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)
	err = sm.TransitionToMigrating()
	require.NoError(t, err)

	check := controller.checkStateIdle()

	assert.Equal(t, "state_idle", check.ID)
	assert.Equal(t, CheckStatusFailed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "must be IDLE")
}

// TestCheckLegacyAccessible_Success tests checkLegacyAccessible when database is accessible.
func TestCheckLegacyAccessible_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkLegacyAccessible(t.Context())

	assert.Equal(t, "legacy_accessible", check.ID)
	assert.Equal(t, CheckStatusPassed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "readable")
}

// TestCheckLegacyAccessible_NilDS tests checkLegacyAccessible when datastore is nil.
func TestCheckLegacyAccessible_NilDS(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Handler{Core: &apicore.Core{DS: nil}}

	check := controller.checkLegacyAccessible(t.Context())

	assert.Equal(t, "legacy_accessible", check.ID)
	assert.Equal(t, CheckStatusError, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "not available")
}

// TestCheckRecordCount_Success tests checkRecordCount when count succeeds.
func TestCheckRecordCount_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkRecordCount(t.Context())

	assert.Equal(t, "record_count", check.ID)
	assert.Equal(t, CheckStatusPassed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "records to migrate")
}

// TestCheckRecordCount_NilRepo tests checkRecordCount when repository is nil.
func TestCheckRecordCount_NilRepo(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Handler{Core: &apicore.Core{Repo: nil}}

	check := controller.checkRecordCount(t.Context())

	assert.Equal(t, "record_count", check.ID)
	assert.Equal(t, CheckStatusError, check.Status)
	assert.Contains(t, check.Message, "not available")
}

// TestCheckSQLiteIntegrity_Success tests checkSQLiteIntegrity with a healthy database.
func TestCheckSQLiteIntegrity_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkSQLiteIntegrity(t.Context())

	assert.Equal(t, "sqlite_integrity", check.ID)
	assert.Equal(t, CheckStatusPassed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "verified")
}

// fakeIntegrityDS is a datastore whose cached integrity result is set by the
// test, standing in for the legacy SQLiteStore's IntegrityResult.
type fakeIntegrityDS struct {
	*testDatastoreWrapper
	result  string
	latched bool
	reads   int
}

// IntegrityResult implements integrityResulter.
func (f *fakeIntegrityDS) IntegrityResult() (string, bool) {
	f.reads++
	return f.result, f.latched
}

// TestCheckSQLiteIntegrity_CachedResult pins which cached store results the
// check trusts: only an "ok" result that is not latched skips the live scan.
func TestCheckSQLiteIntegrity_CachedResult(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	tests := []struct {
		name        string
		result      string
		latched     bool
		withDB      bool
		wantStatus  string
		wantMessage string
	}{
		{"ok cache skips the scan", sqliteIntegrityOK, false, false, CheckStatusPassed, msgIntegrityCachedOK},
		{"latched corruption is never passed from the cache", sqliteIntegrityOK, true, false, CheckStatusError, errMsgDBConnectionUnavailable},
		{"stale error string falls back to the live scan", "database is locked", false, true, CheckStatusPassed, msgIntegrityLiveOK},
		{"empty cache falls back to the live scan", "", false, true, CheckStatusPassed, msgIntegrityLiveOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds := &fakeIntegrityDS{
				testDatastoreWrapper: &testDatastoreWrapper{},
				result:               tt.result,
				latched:              tt.latched,
			}
			if tt.withDB {
				ds.db = setupMigrationTestDB(t)
			}
			controller := &Handler{Core: &apicore.Core{DS: ds}}

			check := controller.checkSQLiteIntegrity(t.Context())

			assert.Equal(t, tt.wantStatus, check.Status)
			// Exact match: the cached and live passed messages share a prefix, so a
			// substring check could not tell the two paths apart.
			assert.Equal(t, tt.wantMessage, check.Message)
		})
	}
}

// TestCheckSQLiteIntegrity_NilDB tests checkSQLiteIntegrity when database is nil.
func TestCheckSQLiteIntegrity_NilDB(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Handler{Core: &apicore.Core{DS: nil}}

	check := controller.checkSQLiteIntegrity(t.Context())

	assert.Equal(t, "sqlite_integrity", check.ID)
	assert.Equal(t, CheckStatusError, check.Status)
	assert.Contains(t, check.Message, errMsgDBConnectionUnavailable)
}

// TestLegacyChecks_V2OnlyModeSkipped tests that the checks that need the legacy
// database report skipped, not an error, in v2-only mode when it is absent. The
// SQLite integrity check has its own test below because it must also never read
// the integrity result.
// Note: Not parallel due to global state modification.
func TestLegacyChecks_V2OnlyModeSkipped(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	tests := []struct {
		name   string
		run    func(*Handler, context.Context) PrerequisiteCheck
		wantID string
	}{
		{name: "legacy accessible", run: (*Handler).checkLegacyAccessible, wantID: "legacy_accessible"},
		{name: "record count", run: (*Handler).checkRecordCount, wantID: "record_count"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setV2OnlyMode(t, true)

			// No datastore and no repository: the legacy database is absent.
			check := tt.run(&Handler{Core: &apicore.Core{}}, t.Context())

			assert.Equal(t, tt.wantID, check.ID)
			assert.Equal(t, CheckStatusSkipped, check.Status)
			assert.Equal(t, CheckSeverityCritical, check.Severity)
			assert.Equal(t, v2OnlySkipMessage, check.Message)
		})
	}
}

// TestCheckSQLiteIntegrity_V2OnlyMode tests checkSQLiteIntegrity returns skipped in v2-only mode.
// Note: Not parallel due to global state modification.
func TestCheckSQLiteIntegrity_V2OnlyMode(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	setV2OnlyMode(t, true)

	// The v2-only datastore's IntegrityResult can run a scan, so it must never be read.
	ds := &fakeIntegrityDS{
		testDatastoreWrapper: &testDatastoreWrapper{},
		result:               sqliteIntegrityOK,
	}
	controller := &Handler{Core: &apicore.Core{DS: ds}}

	check := controller.checkSQLiteIntegrity(t.Context())

	assert.Zero(t, ds.reads, "IntegrityResult must not be read in v2-only mode")
	assert.Equal(t, "sqlite_integrity", check.ID)
	assert.Equal(t, CheckStatusSkipped, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "enhanced database mode")
}

// TestCheckExistingV2Data_CancelledContext pins that a cancelled count is an
// error, not the "table might not exist" pass: the one check whose error branch
// otherwise reports success.
func TestCheckExistingV2Data_CancelledContext(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Handler{Core: &apicore.Core{V2Manager: newFakeV2ManagerWithTable(t, "detections", "", 2)}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	check := controller.checkExistingV2Data(ctx)

	assert.Equal(t, CheckStatusError, check.Status, check.Message)
}

// blockingDS is a datastore whose GetLastDetections blocks until released.
type blockingDS struct {
	*mocks.MockInterface
	release chan struct{}
}

func (b *blockingDS) GetLastDetections(int) ([]datastore.Note, error) {
	<-b.release
	return nil, nil
}

// TestCheckLegacyAccessible_ReturnsWhenContextEnds pins that the context-free
// GetLastDetections cannot hold a run (and so shutdown) past its context.
func TestCheckLegacyAccessible_ReturnsWhenContextEnds(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	ds := &blockingDS{MockInterface: mocks.NewMockInterface(t), release: make(chan struct{})}
	t.Cleanup(func() { close(ds.release) })
	controller := &Handler{Core: &apicore.Core{DS: ds}}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	var check PrerequisiteCheck
	var wg sync.WaitGroup
	wg.Go(func() { check = controller.checkLegacyAccessible(ctx) })
	wg.Wait()

	assert.Equal(t, CheckStatusError, check.Status, check.Message)
}

// failingDS is a datastore whose GetLastDetections returns an error.
type failingDS struct {
	*mocks.MockInterface
}

func (failingDS) GetLastDetections(int) ([]datastore.Note, error) {
	return nil, errors.NewStd("read failed")
}

// TestCheckLegacyAccessible_ReadError pins that a failing legacy read fails the check.
func TestCheckLegacyAccessible_ReadError(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Handler{Core: &apicore.Core{DS: failingDS{MockInterface: mocks.NewMockInterface(t)}}}

	check := controller.checkLegacyAccessible(t.Context())

	assert.Equal(t, CheckStatusFailed, check.Status)
	assert.Contains(t, check.Message, "read failed")
}

// panickingDS is a datastore whose GetLastDetections panics.
type panickingDS struct {
	*mocks.MockInterface
}

func (panickingDS) GetLastDetections(int) ([]datastore.Note, error) {
	panic("legacy read failed")
}

// TestCheckLegacyAccessible_PanickingRead pins that a panic in the legacy read
// becomes a failed check; the read runs in its own goroutine, where nothing
// else would recover it.
func TestCheckLegacyAccessible_PanickingRead(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Handler{Core: &apicore.Core{DS: panickingDS{MockInterface: mocks.NewMockInterface(t)}}}

	check := controller.checkLegacyAccessible(t.Context())

	assert.Equal(t, CheckStatusFailed, check.Status, check.Message)
}

// TestCheckMemoryAvailable_Threshold pins the memory_available thresholds: at
// MinMemoryBytes the check passes, one byte below it warns, and a failed probe
// is skipped rather than reported as low memory.
func TestCheckMemoryAvailable_Threshold(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	tests := []struct {
		name      string
		available uint64
		probeErr  error
		want      string
	}{
		{name: "at the minimum passes", available: MinMemoryBytes, want: CheckStatusPassed},
		{name: "one byte below the minimum warns", available: MinMemoryBytes - 1, want: CheckStatusWarning},
		{name: "probe error is skipped", probeErr: errors.NewStd("meminfo unavailable"), want: CheckStatusSkipped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &Handler{Core: &apicore.Core{}}
			controller.Settings.Store(apitest.NewValidTestSettings())
			controller.availableMemoryFn = func() (uint64, error) { return tt.available, tt.probeErr }

			check := controller.checkMemoryAvailable()

			assert.Equal(t, "memory_available", check.ID)
			assert.Equal(t, CheckSeverityWarning, check.Severity)
			assert.Equal(t, tt.want, check.Status, check.Message)
		})
	}
}

// Tolerances for TestHostProbes_NilSeams. Free disk space and available memory
// drift between two readings, so the accessors are compared with a window around
// the readings taken just before and after the call.
const (
	hostProbeDiskTolerance      = 256 * 1024 * 1024
	hostProbeMemoryToleranceDiv = 16 // a sixteenth of the total memory of the machine
)

// TestHostProbes_NilSeams verifies that without the seams the free disk space and
// available memory are close to the host's own free and available readings, so
// neither falls back to a total.
func TestHostProbes_NilSeams(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	dir := t.TempDir()
	controller := &Handler{Core: &apicore.Core{}}

	diskBefore, err := disk.Usage(dir)
	require.NoError(t, err)
	free, err := controller.diskFree(dir)
	require.NoError(t, err)
	diskAfter, err := disk.Usage(dir)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, free+hostProbeDiskTolerance, min(diskBefore.Free, diskAfter.Free))
	assert.LessOrEqual(t, free, max(diskBefore.Free, diskAfter.Free)+hostProbeDiskTolerance)
	if diskBefore.Used > 0 {
		assert.Less(t, free, diskBefore.Total, "free disk space must not be the filesystem total")
	}

	memBefore, err := mem.VirtualMemory()
	require.NoError(t, err)
	available, err := controller.availableMemory()
	require.NoError(t, err)
	memAfter, err := mem.VirtualMemory()
	require.NoError(t, err)
	memTolerance := memBefore.Total / hostProbeMemoryToleranceDiv
	assert.Positive(t, available)
	assert.GreaterOrEqual(t, available+memTolerance, min(memBefore.Available, memAfter.Available))
	assert.LessOrEqual(t, available, max(memBefore.Available, memAfter.Available)+memTolerance)
	assert.Less(t, available, memBefore.Total, "available memory must not be the total memory")
}

// TestCheckExistingV2Data_NilManager tests checkExistingV2Data when V2Manager is nil.
func TestCheckExistingV2Data_NilManager(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Handler{Core: &apicore.Core{V2Manager: nil}}

	check := controller.checkExistingV2Data(t.Context())

	assert.Equal(t, "existing_v2_data", check.ID)
	assert.Equal(t, CheckStatusSkipped, check.Status)
	assert.Equal(t, CheckSeverityWarning, check.Severity)
}

// TestCheckExistingV2Data_UsesManagerTablePrefix verifies checkExistingV2Data derives
// the detections table name from the manager's TablePrefix() (empty for fresh installs,
// "v2_" during migration) rather than guessing from the dialect. The last case is the
// regression guard: with a prefix set, a bare "detections" table must NOT be counted.
func TestCheckExistingV2Data_UsesManagerTablePrefix(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	tests := []struct {
		name        string
		tableName   string
		prefix      string
		count       int
		wantStatus  string
		wantMessage string
	}{
		{
			name:        "prefixed v2_detections with data is counted",
			tableName:   "v2_detections",
			prefix:      "v2_",
			count:       3,
			wantStatus:  CheckStatusWarning,
			wantMessage: "3",
		},
		{
			name:        "fresh-install bare detections with data is counted",
			tableName:   "detections",
			prefix:      "",
			count:       2,
			wantStatus:  CheckStatusWarning,
			wantMessage: "2",
		},
		{
			name:        "prefix set but only bare detections exists is not counted",
			tableName:   "detections",
			prefix:      "v2_",
			count:       5,
			wantStatus:  CheckStatusPassed,
			wantMessage: "No existing migration data found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &Handler{Core: &apicore.Core{V2Manager: newFakeV2ManagerWithTable(t, tt.tableName, tt.prefix, tt.count)}}

			check := controller.checkExistingV2Data(t.Context())

			assert.Equal(t, "existing_v2_data", check.ID)
			assert.Equal(t, tt.wantStatus, check.Status)
			if tt.wantMessage != "" {
				assert.Contains(t, check.Message, tt.wantMessage)
			}
		})
	}
}

// TestCheckWritePermission_Success tests checkWritePermission in a writable directory.
func TestCheckWritePermission_Success(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	check := controller.checkWritePermission()

	assert.Equal(t, "write_permission", check.ID)
	assert.Equal(t, CheckStatusPassed, check.Status)
	assert.Equal(t, CheckSeverityCritical, check.Severity)
	assert.Contains(t, check.Message, "Write access verified")
}

// TestCheckDiskSpace_Threshold pins the disk_space thresholds: exactly
// MinDiskSpaceBytes free passes, one byte less fails, a probe error is an
// error, a MySQL (remote) database is skipped, and for SQLite the free space of
// the database directory is the one asked for.
func TestCheckDiskSpace_Threshold(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	tests := []struct {
		name     string
		mysql    bool
		free     uint64
		probeErr error
		want     string
	}{
		{name: "exactly the minimum passes", free: MinDiskSpaceBytes, want: CheckStatusPassed},
		{name: "one byte short fails", free: MinDiskSpaceBytes - 1, want: CheckStatusFailed},
		{name: "probe error is an error", probeErr: errors.NewStd("statfs failed"), want: CheckStatusError},
		{name: "mysql is skipped", mysql: true, want: CheckStatusSkipped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, controller, _ := setupPrerequisitesTestEnvironment(t)
			if tt.mysql {
				settings := getTestSettings(t)
				settings.Output.SQLite.Enabled = false
				settings.Output.MySQL.Enabled = true
				controller.Settings.Store(settings)
				apitest.PublishTestSettings(t, settings)
			}
			var askedPath string
			controller.diskFreeFn = func(path string) (uint64, error) {
				askedPath = path
				return tt.free, tt.probeErr
			}

			check := controller.checkDiskSpace()

			assert.Equal(t, "disk_space", check.ID)
			assert.Equal(t, CheckSeverityCritical, check.Severity)
			assert.Equal(t, tt.want, check.Status, check.Message)
			if tt.mysql {
				assert.Empty(t, askedPath, "a remote database has no local disk to probe")
				return
			}
			// Derived from the configured path, not from the resolver under test.
			// Both sides go through EvalSymlinks because the temp dir can sit
			// behind a symlink (macOS /var).
			wantDir, err := filepath.EvalSymlinks(filepath.Dir(controller.CurrentSettings().Output.SQLite.Path))
			require.NoError(t, err)
			gotDir, err := filepath.EvalSymlinks(askedPath)
			require.NoError(t, err)
			assert.Equal(t, wantDir, gotDir, "the free space of the database directory is checked")
		})
	}
}

// TestIsUsingMySQL_NilSettings tests isUsingMySQL returns false when Settings is nil.
func TestIsUsingMySQL_NilSettings(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Handler{Core: &apicore.Core{}}
	// Publish a nil global so currentSettings() falls back to the controller's
	// nil c.Settings instead of a snapshot an earlier test left behind.
	apitest.PublishTestSettings(t, nil)

	result := controller.isUsingMySQL()

	assert.False(t, result, "Should return false when Settings is nil")
}

// TestGetDatabaseDirectoryResolved_SQLite tests path resolution for SQLite.
func TestGetDatabaseDirectoryResolved_SQLite(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	_, controller, _ := setupPrerequisitesTestEnvironment(t)

	dir, err := controller.getDatabaseDirectoryResolved()

	require.NoError(t, err, "Should not return an error for valid SQLite config")
	assert.NotEmpty(t, dir, "Should return a non-empty directory path")
}

// TestGetDatabaseDirectoryResolved_NilSettings tests path resolution when Settings is nil.
func TestGetDatabaseDirectoryResolved_NilSettings(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Handler{Core: &apicore.Core{}}
	// Publish a nil global so currentSettings() falls back to the controller's
	// nil c.Settings and exercises the "settings not available" path.
	apitest.PublishTestSettings(t, nil)

	_, err := controller.getDatabaseDirectoryResolved()

	require.Error(t, err, "Should return error when Settings is nil")
	assert.Contains(t, err.Error(), "settings not available")
}

// TestGetLegacyGormDB_NilDS tests getLegacyGormDB returns nil when DS is nil.
func TestGetLegacyGormDB_NilDS(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "prerequisites")

	controller := &Handler{Core: &apicore.Core{DS: nil}}

	db := controller.getLegacyGormDB()

	assert.Nil(t, db, "Should return nil when DS is nil")
}

// setupPrerequisitesTestEnvironment creates a test environment for prerequisites API tests.
// Note: Tests using this function must NOT be run in parallel due to global state.
func setupPrerequisitesTestEnvironment(t *testing.T) (*echo.Echo, *Handler, *datastoreV2.StateManager) {
	t.Helper()

	e := echo.New()
	db := setupMigrationTestDB(t)

	sm := datastoreV2.NewStateManager(db)

	// Set the global state manager for the handlers and make sure the tests
	// are not in v2-only mode.
	setMigrationState(t, sm, nil)
	setV2OnlyMode(t, false)

	// Register DB close cleanup
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	mockDS := mocks.NewMockInterface(t)
	mockRepo := mocks.NewMockDetectionRepository(t)

	// Set up common mock expectations for prerequisite checks
	mockDS.EXPECT().GetLastDetections(1).Return([]datastore.Note{}, nil).Maybe()
	mockRepo.EXPECT().CountAll(mock.Anything).Return(int64(100), nil).Maybe()

	// Create a wrapper that provides both the mock interface and the GORM DB
	testDS := &testDatastoreWrapper{
		MockInterface: mockDS,
		db:            db,
	}

	controller := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2"), DS: testDS, Repo: mockRepo}}
	controller.diskFreeFn = stubDiskFree
	controller.Settings.Store(getTestSettings(t))
	apitest.PublishTestSettings(t, controller.Settings.Load())

	return e, controller, sm
}

// testDiskFreeBytes is the free space the setup helpers report. It is twice the
// disk_space minimum so the critical check passes whatever the host disk holds.
const testDiskFreeBytes = 2 * MinDiskSpaceBytes

// stubDiskFree is the diskFreeFn the setup helpers install.
func stubDiskFree(string) (uint64, error) { return testDiskFreeBytes, nil }

// TestPrerequisiteTagValue pins the closed set of values reported to telemetry
// for the prerequisite checks: only a failing critical check is blocking.
func TestPrerequisiteTagValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		checks []PrerequisiteCheck
		want   string
	}{
		{
			name: "all checks passed",
			checks: []PrerequisiteCheck{
				{ID: "state_idle", Status: CheckStatusPassed, Severity: CheckSeverityCritical},
				{ID: "disk_space", Status: CheckStatusPassed, Severity: CheckSeverityCritical},
			},
			want: prerequisiteTagPassed,
		},
		{
			name: "failed warning-severity check is not blocking",
			checks: []PrerequisiteCheck{
				{ID: "state_idle", Status: CheckStatusPassed, Severity: CheckSeverityCritical},
				{ID: "memory_available", Status: CheckStatusFailed, Severity: CheckSeverityWarning},
			},
			want: prerequisiteTagPassed,
		},
		{
			name: "errored critical check blocks",
			checks: []PrerequisiteCheck{
				{ID: "state_idle", Status: CheckStatusPassed, Severity: CheckSeverityCritical},
				{ID: "sqlite_integrity", Status: CheckStatusError, Severity: CheckSeverityCritical},
			},
			want: "sqlite_integrity",
		},
		{
			name: "skipped critical check does not block",
			checks: []PrerequisiteCheck{
				{ID: "legacy_accessible", Status: CheckStatusSkipped, Severity: CheckSeverityCritical},
			},
			want: prerequisiteTagPassed,
		},
		{
			name: "first critical failure wins",
			checks: []PrerequisiteCheck{
				{ID: "state_idle", Status: CheckStatusPassed, Severity: CheckSeverityCritical},
				{ID: "disk_space", Status: CheckStatusFailed, Severity: CheckSeverityCritical},
				{ID: "sqlite_integrity", Status: CheckStatusError, Severity: CheckSeverityCritical},
			},
			want: "disk_space",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, prerequisiteTagValue(tt.checks))
		})
	}
}

// discardTransport is a sentry.Transport that drops every event; the test below
// only reads tags off the hub scope.
type discardTransport struct{}

func (discardTransport) Configure(sentry.ClientOptions)        {}
func (discardTransport) SendEvent(*sentry.Event)               {}
func (discardTransport) Flush(time.Duration) bool              { return true }
func (discardTransport) FlushWithContext(context.Context) bool { return true }
func (discardTransport) Close()                                {}

// TestRunCriticalPrerequisiteChecks_PublishesPrereqTag pins that running the
// critical checks publishes the migration_prereq telemetry tag derived from
// their results: "passed" when none blocks, otherwise the ID of the first
// failing critical check. Not parallel: the setup helper writes the process-wide
// migration globals, and the test binds a client on the process-wide Sentry hub.
func TestRunCriticalPrerequisiteChecks_PublishesPrereqTag(t *testing.T) {
	tests := []struct {
		name      string
		freeDisk  uint64
		cancelled bool
		want      string
	}{
		{name: "all critical checks pass", freeDisk: testDiskFreeBytes, want: prerequisiteTagPassed},
		{name: "disk space check fails", freeDisk: 0, want: "disk_space"},
		{name: "cancelled context leaves the tag unset", freeDisk: 0, cancelled: true, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, controller, _ := setupPrerequisitesTestEnvironment(t)
			controller.diskFreeFn = func(string) (uint64, error) { return tt.freeDisk, nil }

			// Telemetry reports only when the user opted in and a client is bound.
			settings := controller.Settings.Load()
			prevEnabled := settings.Sentry.Enabled
			settings.Sentry.Enabled = true
			t.Cleanup(func() { settings.Sentry.Enabled = prevEnabled })

			hub := sentry.CurrentHub()
			original := hub.Client()
			client, err := sentry.NewClient(sentry.ClientOptions{Transport: discardTransport{}})
			require.NoError(t, err)
			hub.BindClient(client)
			t.Cleanup(func() { hub.BindClient(original) })
			// The tag lands on the top scope; pop it afterwards so it does not leak.
			hub.PushScope()
			t.Cleanup(hub.PopScope)

			ctx := t.Context()
			if tt.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			controller.runCriticalPrerequisiteChecks(ctx, nil)

			event := hub.Scope().ApplyToEvent(&sentry.Event{}, nil, client)
			require.NotNil(t, event)
			assert.Equal(t, tt.want, event.Tags["migration_prereq"])
		})
	}
}
