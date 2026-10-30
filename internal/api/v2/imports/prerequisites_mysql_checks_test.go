// prerequisites_mysql_checks_test.go: unit tests for the mysql_server_version
// and mysql_charset migration prerequisite checks.
package importsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// useMySQLSettings switches the handler to MySQL output.
func useMySQLSettings(t *testing.T, c *Handler) {
	t.Helper()
	settings := getTestSettings(t)
	settings.Output.SQLite.Enabled = false
	settings.Output.MySQL.Enabled = true
	c.Settings.Store(settings)
	apitest.PublishTestSettings(t, settings)
}

func TestDescribeServerVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		raw          string
		wantStatus   string
		wantContains []string
	}{
		{name: "supported 8.4.13", raw: "8.4.13", wantStatus: CheckStatusPassed, wantContains: []string{"is a supported release"}},
		{name: "supported 9.7.4", raw: "9.7.4", wantStatus: CheckStatusPassed, wantContains: []string{"is a supported release"}},
		{name: "supported 10.11.19-MariaDB-ubu2204", raw: "10.11.19-MariaDB-ubu2204", wantStatus: CheckStatusPassed, wantContains: []string{"is a supported release"}},
		{name: "supported 11.4.13-MariaDB", raw: "11.4.13-MariaDB", wantStatus: CheckStatusPassed, wantContains: []string{"is a supported release"}},
		{name: "supported 11.8.9-MariaDB", raw: "11.8.9-MariaDB", wantStatus: CheckStatusPassed, wantContains: []string{"is a supported release"}},
		{name: "supported 12.3.3-MariaDB", raw: "12.3.3-MariaDB", wantStatus: CheckStatusPassed, wantContains: []string{"is a supported release"}},
		{name: "innovation release 9.0 fails", raw: "9.0.1", wantStatus: CheckStatusFailed, wantContains: []string{"9.0.1", "is not a supported release", "before migrating"}},
		{name: "innovation release 9.1 fails", raw: "9.1.1", wantStatus: CheckStatusFailed, wantContains: []string{"9.1.1", "is not a supported release", "before migrating"}},
		{name: "innovation release 9.2 fails", raw: "9.2.1", wantStatus: CheckStatusFailed, wantContains: []string{"9.2.1", "is not a supported release", "before migrating"}},
		{name: "innovation release 9.3 fails", raw: "9.3.1", wantStatus: CheckStatusFailed, wantContains: []string{"9.3.1", "is not a supported release", "before migrating"}},
		{name: "innovation release 9.4 fails", raw: "9.4.1", wantStatus: CheckStatusFailed, wantContains: []string{"9.4.1", "is not a supported release", "before migrating"}},
		{name: "innovation release 9.5 fails", raw: "9.5.1", wantStatus: CheckStatusFailed, wantContains: []string{"9.5.1", "is not a supported release", "before migrating"}},
		{name: "innovation release 9.6 fails", raw: "9.6.1", wantStatus: CheckStatusFailed, wantContains: []string{"9.6.1", "is not a supported release", "before migrating"}},
		{name: "below the floor 5.7.44-log fails", raw: "5.7.44-log", wantStatus: CheckStatusFailed, wantContains: []string{"is not a supported release", "before migrating"}},
		{name: "below the floor 8.0.46 fails", raw: "8.0.46", wantStatus: CheckStatusFailed, wantContains: []string{"is not a supported release", "before migrating"}},
		{name: "below the floor 10.6.28-MariaDB fails", raw: "10.6.28-MariaDB", wantStatus: CheckStatusFailed, wantContains: []string{"is not a supported release", "before migrating"}},
		{name: "below the floor 10.5.29-MariaDB fails", raw: "10.5.29-MariaDB", wantStatus: CheckStatusFailed, wantContains: []string{"is not a supported release", "before migrating"}},
		{name: "untracked 9.8.0 warns", raw: "9.8.0", wantStatus: CheckStatusWarning, wantContains: []string{"not on the list"}},
		{name: "untracked 26.10.0 warns", raw: "26.10.0", wantStatus: CheckStatusWarning, wantContains: []string{"not on the list"}},
		{name: "untracked 99.1.0 warns", raw: "99.1.0", wantStatus: CheckStatusWarning, wantContains: []string{"not on the list"}},
		{name: "unparseable warns with raw", raw: "garbage", wantStatus: CheckStatusWarning, wantContains: []string{`"garbage"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := dbserver.AssessRaw(tt.raw)
			status, msg := describeServerVersion(&a)
			assert.Equal(t, tt.wantStatus, status, msg)
			for _, want := range tt.wantContains {
				assert.Contains(t, msg, want)
			}
			assert.NotContains(t, strings.ToLower(msg), "end of life")
			assert.NotRegexp(t, `\d{4}-\d{2}-\d{2}`, msg)
			if tt.wantStatus != CheckStatusPassed {
				assert.Contains(t, msg, dbserver.SupportedReleases())
			}
		})
	}
}

func TestCheckMySQLCharset_Status(t *testing.T) {
	_, controller, _ := setupPrerequisitesTestEnvironment(t)
	useMySQLSettings(t, controller)
	controller.V2Manager = &fakeV2Manager{tablePrefix: "v2_", isMySQL: true}

	tbl := func(name string, charsets ...string) dbserver.TableCharsets {
		return dbserver.TableCharsets{Name: name, Charsets: charsets}
	}
	tests := []struct {
		name        string
		schema      string
		tables      []dbserver.TableCharsets
		queryErr    error
		want        string
		wantMessage string
		wantNot     string
	}{
		{name: "all utf8mb4 passes", schema: "utf8mb4",
			tables: []dbserver.TableCharsets{tbl("notes", "utf8mb4"), tbl("v2_detections", "utf8mb4")}, want: CheckStatusPassed},
		{name: "latin1 target holding latin1 source warns", schema: "latin1",
			tables: []dbserver.TableCharsets{tbl("notes", "latin1"), tbl("v2_detections", "latin1")}, want: CheckStatusWarning},
		{name: "latin1 target cannot hold utf8mb4 source", schema: "latin1",
			tables: []dbserver.TableCharsets{tbl("notes", "utf8mb4"), tbl("v2_detections", "latin1")},
			want:   CheckStatusFailed, wantMessage: "v2_detections"},
		{name: "one latin1 target table among utf8mb4 ones fails", schema: "utf8mb4",
			tables: []dbserver.TableCharsets{tbl("notes", "utf8mb4"), tbl("v2_a", "utf8mb4"), tbl("v2_b", "latin1")},
			want:   CheckStatusFailed, wantMessage: "v2_b", wantNot: "v2_a"},
		{name: "mixed targets warn without naming utf8mb4 as limited", schema: "utf8mb4",
			tables: []dbserver.TableCharsets{tbl("notes", "latin1"), tbl("v2_a", "utf8mb4"), tbl("v2_b", "latin1")},
			want:   CheckStatusWarning, wantMessage: "latin1", wantNot: "utf8mb4"},
		{name: "shared legacy table with utf8mb4 text fails a latin1 target", schema: "latin1",
			tables: []dbserver.TableCharsets{tbl("image_caches", "utf8mb4"), tbl("v2_detections", "latin1")},
			want:   CheckStatusFailed, wantMessage: "v2_detections"},
		{name: "foreign table is not a source", schema: "latin1",
			tables: []dbserver.TableCharsets{tbl("wp_posts", "utf8mb4"), tbl("notes", "latin1"), tbl("v2_detections", "latin1")},
			want:   CheckStatusWarning},
		{name: "no target text tables falls back to schema default", schema: "latin1",
			tables: []dbserver.TableCharsets{tbl("notes", "utf8mb4")}, want: CheckStatusFailed},
		{name: "no target tables and no schema default errors", schema: "",
			tables: []dbserver.TableCharsets{tbl("notes", "utf8mb4")}, want: CheckStatusError},
		{name: "query error errors", queryErr: errors.NewStd("boom"), want: CheckStatusError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller.charsetReportFn = func(context.Context) (dbserver.CharsetReport, error) {
				return dbserver.CharsetReport{SchemaCharset: tt.schema, Tables: tt.tables}, tt.queryErr
			}
			check := controller.checkMySQLCharset()
			assert.Equal(t, "mysql_charset", check.ID)
			assert.Equal(t, CheckSeverityCritical, check.Severity)
			assert.Equal(t, tt.want, check.Status, check.Message)
			if tt.wantMessage != "" {
				assert.Contains(t, check.Message, tt.wantMessage)
			}
			if tt.wantNot != "" {
				assert.NotContains(t, check.Message, tt.wantNot)
			}
		})
	}
}

func TestMySQLServerChecks_TargetUnavailable(t *testing.T) {
	checks := map[string]func(*Handler) PrerequisiteCheck{
		"mysql_server_version": (*Handler).checkMySQLServerVersion,
		"mysql_charset":        (*Handler).checkMySQLCharset,
	}
	for id, run := range checks {
		t.Run(id+" nil manager", func(t *testing.T) {
			_, controller, _ := setupPrerequisitesTestEnvironment(t)
			useMySQLSettings(t, controller)
			controller.V2Manager = nil
			check := run(controller)
			assert.Equal(t, id, check.ID)
			assert.Equal(t, CheckSeverityCritical, check.Severity)
			assert.Equal(t, CheckStatusError, check.Status, check.Message)
		})
		t.Run(id+" v2-only mode", func(t *testing.T) {
			_, controller, _ := setupPrerequisitesTestEnvironment(t)
			useMySQLSettings(t, controller)
			enableV2OnlyMode(t)
			check := run(controller)
			assert.Equal(t, CheckStatusSkipped, check.Status, check.Message)
			assert.Equal(t, CheckSeverityCritical, check.Severity)
		})
	}
}

func TestMySQLServerChecks_UnusableTarget(t *testing.T) {
	tests := []struct {
		name    string
		manager *fakeV2Manager
	}{
		{name: "non-MySQL manager", manager: &fakeV2Manager{tablePrefix: "v2_"}},
		{name: "MySQL manager without a connection", manager: &fakeV2Manager{tablePrefix: "v2_", isMySQL: true}},
	}
	for _, tt := range tests {
		for id, run := range map[string]func(*Handler) PrerequisiteCheck{
			"mysql_server_version": (*Handler).checkMySQLServerVersion,
			"mysql_charset":        (*Handler).checkMySQLCharset,
		} {
			t.Run(tt.name+" "+id, func(t *testing.T) {
				_, controller, _ := setupPrerequisitesTestEnvironment(t)
				useMySQLSettings(t, controller)
				controller.V2Manager = tt.manager
				check := run(controller)
				assert.Equal(t, CheckStatusError, check.Status, check.Message)
				assert.Equal(t, errMsgMigrationTargetUnavailable, check.Message)
			})
		}
	}

	t.Run("empty table prefix cannot tell target from legacy tables", func(t *testing.T) {
		_, controller, _ := setupPrerequisitesTestEnvironment(t)
		useMySQLSettings(t, controller)
		controller.V2Manager = &fakeV2Manager{isMySQL: true}
		controller.charsetReportFn = func(context.Context) (dbserver.CharsetReport, error) {
			return dbserver.CharsetReport{SchemaCharset: dbserver.CharsetUTF8MB4}, nil
		}
		check := controller.checkMySQLCharset()
		assert.Equal(t, CheckStatusError, check.Status, check.Message)
		assert.Equal(t, errMsgMigrationTargetAmbiguous, check.Message)
	})
}

func TestCheckMySQLServerVersion_ReadFailureBlocks(t *testing.T) {
	_, controller, _ := setupPrerequisitesTestEnvironment(t)
	useMySQLSettings(t, controller)
	controller.V2Manager = &fakeV2Manager{tablePrefix: "v2_", isMySQL: true}
	controller.serverVersionFn = func(context.Context) (string, error) { return "", errors.NewStd("connection refused") }

	check := controller.checkMySQLServerVersion()

	assert.Equal(t, CheckStatusError, check.Status, check.Message)
	assert.Contains(t, check.Message, "connection refused")
}

// TestRunCriticalPrerequisiteChecks_ServerChecks pins that the server checks
// gate through the shared critical list, MySQL only, version check first.
func TestRunCriticalPrerequisiteChecks_ServerChecks(t *testing.T) {
	ids := func(checks []PrerequisiteCheck) []string {
		out := make([]string, 0, len(checks))
		for i := range checks {
			out = append(out, checks[i].ID)
		}
		return out
	}

	t.Run("mysql runs the version check before the charset check", func(t *testing.T) {
		_, controller, _ := setupPrerequisitesTestEnvironment(t)
		useMySQLSettings(t, controller)
		got := ids(controller.runCriticalPrerequisiteChecks())
		version, charset := slices.Index(got, "mysql_server_version"), slices.Index(got, "mysql_charset")
		health := slices.Index(got, "mysql_table_health")
		require.GreaterOrEqual(t, version, 0, got)
		assert.Less(t, version, charset, got)
		assert.Less(t, charset, health, got)
	})

	t.Run("sqlite runs neither", func(t *testing.T) {
		_, controller, _ := setupPrerequisitesTestEnvironment(t)
		got := ids(controller.runCriticalPrerequisiteChecks())
		assert.NotContains(t, got, "mysql_server_version")
		assert.NotContains(t, got, "mysql_charset")
	})
}

func TestStartMigration_BlocksUnsupportedServer(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		wantNamed bool
	}{
		{name: "unsupported server is named in the refusal", version: "8.0.46", wantNamed: true},
		{name: "supported server is not the blocker", version: "8.4.13", wantNamed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller, sm := setupMigrationTestEnvironment(t)
			settings := getTestSettings(t)
			settings.Output.SQLite.Enabled = false
			settings.Output.MySQL.Enabled = true
			settings.WebServer.Debug = true
			controller.Settings.Store(settings)
			apitest.PublishTestSettings(t, settings)
			controller.V2Manager = &fakeV2Manager{tablePrefix: "v2_", isMySQL: true}
			controller.serverVersionFn = func(context.Context) (string, error) { return tt.version, nil }
			controller.charsetReportFn = func(context.Context) (dbserver.CharsetReport, error) {
				return dbserver.CharsetReport{SchemaCharset: dbserver.CharsetUTF8MB4}, nil
			}

			req := httptest.NewRequest(http.MethodPost, "/api/v2/system/database/migration/start",
				strings.NewReader(`{"total_records": 1000}`))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			require.NoError(t, controller.StartMigration(e.NewContext(req, rec)))

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			var resp apicore.ErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, notification.MsgErrMigrationPreFlight, resp.ErrorKey)
			if tt.wantNamed {
				assert.Contains(t, resp.Error, "'Database Server Version'")
			} else {
				assert.NotContains(t, resp.Error, "'Database Server Version'")
			}
			state, err := sm.GetState()
			require.NoError(t, err)
			assert.Equal(t, "idle", string(state.State))
		})
	}
}
