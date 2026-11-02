// migration_start_test.go: tests for the prerequisite gate of POST /start.
package importsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/notification"
)

const (
	startURL          = "/api/v2/system/database/migration/start"
	startBody         = `{"total_records": 1000}`
	testSeededRecords = int64(1234)
	staleResultAge    = prereqResultMaxAge + time.Minute
	freshResultAge    = time.Minute
	prereqTagKey      = "migration_prereq"
	blockingCheckName = "Disk Space"
)

// reusedCheckIDs returns the IDs of the critical checks a start takes from a stored run.
func reusedCheckIDs(c *Handler) []string {
	var ids []string
	for _, step := range c.criticalPrerequisiteSteps(nil) {
		if step.reuseAtStart {
			ids = append(ids, step.id)
		}
	}
	return ids
}

// passingReusedChecks returns passing results for every check a start reuses.
func passingReusedChecks(c *Handler) []PrerequisiteCheck {
	ids := reusedCheckIDs(c)
	checks := make([]PrerequisiteCheck, 0, len(ids))
	for _, id := range ids {
		checks = append(checks, passCheck(id))
	}
	return checks
}

// seedPrerequisiteResult sets the runner's completed state directly: the test
// environment cannot pass every real check (CHECK TABLE needs MySQL). The result
// carries the handler's current database fingerprint.
func seedPrerequisiteResult(t *testing.T, c *Handler, checks []PrerequisiteCheck, age time.Duration, recordCount int64) {
	t.Helper()
	r := c.prereqRunner()
	result := c.summarizePrerequisites(checks)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runID++
	r.generation++
	r.status = PrereqRunStatusCompleted
	r.startedAt = time.Now().Add(-age)
	r.finishedAt = time.Now().Add(-age)
	result.CheckedAt = r.finishedAt
	r.result = &result
	r.resultRecordCount = recordCount
	r.resultRecordCountKnown = recordCount > 0
	r.resultFingerprint = c.dbFingerprint()
}

// seedPassingResult seeds a fresh passing result with a known record count.
func seedPassingResult(t *testing.T, c *Handler) {
	t.Helper()
	seedPrerequisiteResult(t, c, passingReusedChecks(c), freshResultAge, testSeededRecords)
}

// postStart sends POST /start with body and returns the recorder.
func postStart(t *testing.T, e *echo.Echo, c *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, startURL, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, c.StartMigration(e.NewContext(req, rec)))
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) apicore.ErrorResponse {
	t.Helper()
	var resp apicore.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func migrationStateOf(t *testing.T, sm interface {
	GetState() (*entities.MigrationState, error)
}) string {
	t.Helper()
	state, err := sm.GetState()
	require.NoError(t, err)
	return string(state.State)
}

// TestStartMigration_PrerequisiteGate pins what a start requires of the stored
// prerequisites run, and that a successful start forgets the result.
func TestStartMigration_PrerequisiteGate(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	tests := []struct {
		name      string
		prepare   func(t *testing.T, c *Handler)
		wantCode  int
		wantKey   string
		wantCheck string
		wantState string
	}{
		{
			name:      "no run yet",
			prepare:   func(*testing.T, *Handler) {},
			wantCode:  http.StatusConflict,
			wantKey:   notification.MsgErrMigrationPrereqNotReady,
			wantState: "idle",
		},
		{
			name: "a run in progress",
			prepare: func(_ *testing.T, c *Handler) {
				r := c.prereqRunner()
				r.mu.Lock()
				r.status = PrereqRunStatusRunning
				r.mu.Unlock()
			},
			wantCode:  http.StatusConflict,
			wantKey:   notification.MsgErrMigrationPrereqNotReady,
			wantState: "idle",
		},
		{
			name: "the stored run has a failed critical check",
			prepare: func(t *testing.T, c *Handler) {
				t.Helper()
				failed := failCheck(checkIDDiskSpace)
				failed.Name = blockingCheckName
				seedPrerequisiteResult(t, c, append(passingReusedChecks(c), failed), freshResultAge, testSeededRecords)
			},
			wantCode:  http.StatusBadRequest,
			wantKey:   notification.MsgErrMigrationPreFlight,
			wantCheck: blockingCheckName,
			wantState: "idle",
		},
		{
			name: "the stored run is older than the maximum age",
			prepare: func(t *testing.T, c *Handler) {
				t.Helper()
				seedPrerequisiteResult(t, c, passingReusedChecks(c), staleResultAge, testSeededRecords)
			},
			wantCode:  http.StatusConflict,
			wantKey:   notification.MsgErrMigrationPrereqNotReady,
			wantState: "idle",
		},
		{
			name: "the database settings changed since the run",
			prepare: func(t *testing.T, c *Handler) {
				t.Helper()
				seedPassingResult(t, c)
				c.Settings.Load().Output.SQLite.Path = filepath.Join(t.TempDir(), "other.db")
			},
			wantCode:  http.StatusConflict,
			wantKey:   notification.MsgErrMigrationPrereqNotReady,
			wantState: "idle",
		},
		{
			name: "the stored run lacks a check the start reuses",
			prepare: func(t *testing.T, c *Handler) {
				t.Helper()
				checks := passingReusedChecks(c)
				seedPrerequisiteResult(t, c, checks[:len(checks)-1], freshResultAge, testSeededRecords)
			},
			wantCode:  http.StatusBadRequest,
			wantKey:   notification.MsgErrMigrationPreFlight,
			wantCheck: "Record Count",
			wantState: "idle",
		},
		{
			name: "the stored run is marked failed without naming a check",
			prepare: func(t *testing.T, c *Handler) {
				t.Helper()
				seedPassingResult(t, c)
				c.prereqRunner().result.AllPassed = false
			},
			wantCode:  http.StatusConflict,
			wantKey:   notification.MsgErrMigrationPrereqNotReady,
			wantState: "idle",
		},
		{
			name: "the re-run checks outlast the preflight limit",
			prepare: func(t *testing.T, c *Handler) {
				t.Helper()
				seedPassingResult(t, c)
				c.startPreflightLimit = time.Nanosecond
			},
			wantCode:  http.StatusGatewayTimeout,
			wantKey:   notification.MsgErrMigrationStartFailed,
			wantState: "idle",
		},
		{
			name: "a re-run check fails at start despite a passing stored run",
			prepare: func(t *testing.T, c *Handler) {
				t.Helper()
				seedPassingResult(t, c)
				c.diskFreeFn = func(string) (uint64, error) { return 0, nil }
			},
			wantCode:  http.StatusBadRequest,
			wantKey:   notification.MsgErrMigrationPreFlight,
			wantCheck: blockingCheckName,
			wantState: "idle",
		},
		{
			name:      "a fresh passing run",
			prepare:   seedPassingResult,
			wantCode:  http.StatusOK,
			wantState: string(entities.MigrationStatusDualWrite),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller, sm := setupMigrationTestEnvironment(t)
			tt.prepare(t, controller)

			rec := postStart(t, e, controller, startBody)

			assert.Equal(t, tt.wantCode, rec.Code, rec.Body.String())
			if tt.wantKey != "" {
				resp := decodeError(t, rec)
				assert.Equal(t, tt.wantKey, resp.ErrorKey)
				if tt.wantCheck != "" {
					assert.Equal(t, tt.wantCheck, resp.ErrorParams[preFlightCheckParam])
				}
			}
			assert.Equal(t, tt.wantState, migrationStateOf(t, sm))
			if tt.wantCode == http.StatusOK {
				assert.Equal(t, PrereqRunStatusIdle, controller.prereqRunner().snapshot().Status,
					"a started migration forgets the stored checks")
			}
		})
	}
}

// TestStartMigration_ReusesExpensiveChecks pins that a start takes the slow
// checks and the record count from the stored run: any call into the legacy
// store fails the test.
func TestStartMigration_ReusesExpensiveChecks(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	tests := []struct {
		name      string
		body      string
		wantTotal int64
	}{
		{name: "total from the request", body: startBody, wantTotal: 1000},
		{name: "total from the stored run", body: `{}`, wantTotal: testSeededRecords},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller, sm := setupMigrationTestEnvironment(t)
			// No expectations: GetLastDetections and CountAll would fail the test
			controller.DS = &testDatastoreWrapper{MockInterface: mocks.NewMockInterface(t)}
			controller.Repo = mocks.NewMockDetectionRepository(t)
			seedPassingResult(t, controller)

			rec := postStart(t, e, controller, tt.body)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			state, err := sm.GetState()
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, state.TotalRecords)
		})
	}
}

// hungCheckAnswerBound is how long the hung-check test waits for the handler to answer. The
// handler answers as soon as the start's context ends; the bound only keeps a regression from
// hanging the suite.
const hungCheckAnswerBound = 10 * time.Second

// TestStartMigration_HungCheapCheckEndsWithTheStartContext pins that a re-run check that takes
// no context and never returns, a stalled mount, does not hold the start once its context
// ends: the check is abandoned and the start answers, with nothing started. The test ends the
// context only after the check has begun, so it cannot pass without the abandonment.
func TestStartMigration_HungCheapCheckEndsWithTheStartContext(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm := setupMigrationTestEnvironment(t)
	seedPassingResult(t, controller)
	lifecycle, stop := context.WithCancel(t.Context())
	controller.SetTestContext(lifecycle, stop)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	// Runs before the setup cleanups, so the abandoned check ends before they close the stores
	t.Cleanup(func() { close(release) })
	controller.diskFreeFn = func(string) (uint64, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return testDiskFreeBytes, nil
	}

	req := httptest.NewRequest(http.MethodPost, startURL, strings.NewReader(startBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	answered := make(chan error, 1)
	go func() { answered <- controller.StartMigration(e.NewContext(req, rec)) }()

	select {
	case <-entered:
	case <-time.After(hungCheckAnswerBound):
		t.Fatal("the disk space check never started")
	}
	stop()

	select {
	case err := <-answered:
		require.NoError(t, err)
	case <-time.After(hungCheckAnswerBound):
		t.Fatal("the start is still waiting for a check that ignores its context")
	}

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), notification.MsgErrMigrationStartFailed)
	assert.Equal(t, "idle", migrationStateOf(t, sm))
}

// TestStartMigration_EndedContextBeatsTheStoredRunGate pins that a request whose client has gone,
// or whose server is stopping, is answered as such even when there is no usable stored run: the
// gate must not answer a gone client with a 409 or a stopping server with a request to run again.
func TestStartMigration_EndedContextBeatsTheStoredRunGate(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	tests := []struct {
		name       string
		goneClient bool
		wantCode   int
		wantBody   bool
	}{
		{name: "the client has gone", goneClient: true, wantBody: false},
		{name: "the server is stopping", goneClient: false, wantCode: http.StatusServiceUnavailable, wantBody: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller, sm := setupMigrationTestEnvironment(t)
			// No stored run, so the gate alone would answer 409 prerequisitesNotReady
			lifecycle, stop := context.WithCancel(t.Context())
			controller.SetTestContext(lifecycle, stop)

			reqCtx, gone := context.WithCancel(t.Context())
			if tt.goneClient {
				gone()
			} else {
				stop()
				defer gone()
			}
			req := httptest.NewRequest(http.MethodPost, startURL, strings.NewReader(startBody)).WithContext(reqCtx)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			require.NoError(t, controller.StartMigration(e.NewContext(req, rec)))

			assert.Equal(t, "idle", migrationStateOf(t, sm))
			if !tt.wantBody {
				assert.Zero(t, rec.Body.Len(), "a gone client is sent nothing")
				return
			}
			assert.Equal(t, tt.wantCode, rec.Code)
			assert.Contains(t, rec.Body.String(), notification.MsgErrMigrationStartFailed)
		})
	}
}

// TestStartMigration_ClientGoneDoesNotStart pins that a request whose client has
// gone starts nothing and leaves the telemetry tag alone.
func TestStartMigration_ClientGoneDoesNotStart(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm := setupMigrationTestEnvironment(t)
	seedPassingResult(t, controller)
	controller.Settings.Load().Sentry.Enabled = true
	hub := sentry.CurrentHub()
	original := hub.Client()
	client, err := sentry.NewClient(sentry.ClientOptions{Transport: discardTransport{}})
	require.NoError(t, err)
	hub.BindClient(client)
	t.Cleanup(func() { hub.BindClient(original) })
	hub.PushScope()
	t.Cleanup(hub.PopScope)

	gone, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodPost, startURL, strings.NewReader(startBody)).WithContext(gone)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.StartMigration(e.NewContext(req, rec)))

	assert.Equal(t, "idle", migrationStateOf(t, sm))
	assert.Zero(t, rec.Body.Len(), "a gone client is sent nothing")
	event := hub.Scope().ApplyToEvent(&sentry.Event{}, nil, client)
	require.NotNil(t, event)
	assert.Empty(t, event.Tags[prereqTagKey])
}

// TestStartMigration_CountsWhenNoTotalIsKnown pins the fallback: neither the
// request nor the stored run has a total, so the start counts the legacy records.
func TestStartMigration_CountsWhenNoTotalIsKnown(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm := setupMigrationTestEnvironment(t)
	seedPrerequisiteResult(t, controller, passingReusedChecks(controller), freshResultAge, 0)

	rec := postStart(t, e, controller, `{}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, int64(100), state.TotalRecords, "the mock repository counts 100 records")
}

// TestStartMigration_AbandonedDuringCountStartsNothing pins the abandonment check
// after the fallback count: a client that goes away during the count gets no
// answer, not a 500, and no migration starts.
func TestStartMigration_AbandonedDuringCountStartsNothing(t *testing.T) {
	t.Attr("component", "migration")
	t.Attr("type", "unit")
	t.Attr("feature", "start")

	e, controller, sm := setupMigrationTestEnvironment(t)
	seedPrerequisiteResult(t, controller, passingReusedChecks(controller), freshResultAge, 0)
	gone, cancel := context.WithCancel(t.Context())
	repo := mocks.NewMockDetectionRepository(t)
	repo.EXPECT().CountAll(mock.Anything).Run(func(context.Context) { cancel() }).Return(int64(0), context.Canceled)
	controller.Repo = repo

	req := httptest.NewRequest(http.MethodPost, startURL, strings.NewReader(`{}`)).WithContext(gone)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.StartMigration(e.NewContext(req, rec)))

	assert.Equal(t, "idle", migrationStateOf(t, sm))
	assert.Zero(t, rec.Body.Len(), "a client that left during the count is sent nothing, not a 500")
}
