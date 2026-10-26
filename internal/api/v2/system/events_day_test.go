// events_day_test.go: the event log endpoints select the requested local calendar day.

package system

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const (
	// eventDayModule is the logger module whose output path the detection endpoint reads.
	eventDayModule = "analysis.processor"
	// eventDayLogFile is the log file the application output of the test logger points at.
	eventDayLogFile = "application.log"
	// eventDayDetectionFile is the log file of the detection module.
	eventDayDetectionFile = "actions.log"
	eventDayFilePerm      = 0o600
)

// skippedMidnightCase is a zone whose local midnight does not exist on Date: the clocks jump
// from 23:59 to 01:00, so the day starts at 01:00. LateBefore is a local time late on the
// previous day and EarlyOn a local time early on Date.
type skippedMidnightCase struct {
	zone       string
	loc        *time.Location
	date       string
	lateBefore time.Time
	earlyOn    time.Time
}

func skippedMidnightCases(t *testing.T) []skippedMidnightCase {
	t.Helper()
	zones := testutil.ToolchainZones(t)
	havana := zones["America/Havana"]
	santiago := zones["America/Santiago"]
	require.NotNil(t, havana)
	require.NotNil(t, santiago)
	return []skippedMidnightCase{
		{
			zone:       "America/Havana",
			loc:        havana,
			date:       "2026-03-08",
			lateBefore: time.Date(2026, time.March, 7, 23, 30, 0, 0, havana),
			earlyOn:    time.Date(2026, time.March, 8, 1, 30, 0, 0, havana),
		},
		{
			zone:       "America/Santiago",
			loc:        santiago,
			date:       "2026-09-06",
			lateBefore: time.Date(2026, time.September, 5, 23, 30, 0, 0, santiago),
			earlyOn:    time.Date(2026, time.September, 6, 1, 30, 0, 0, santiago),
		},
	}
}

func eventLogLine(ts time.Time, operation, extra string) string {
	line := `{"time":"` + ts.Format(time.RFC3339) + `","level":"INFO","msg":"entry","module":"` +
		eventDayModule + `","operation":"` + operation + `"`
	if extra != "" {
		line += "," + extra
	}
	return line + "}\n"
}

// useEventLogs points the global logger at log files in a temp dir and time.Local at zone. It
// swaps process-wide state, so callers must not run in parallel.
func useEventLogs(t *testing.T, zone *time.Location, appLog, detectionLog string) {
	t.Helper()
	dir := t.TempDir()
	appPath := filepath.Join(dir, eventDayLogFile)
	detectionPath := filepath.Join(dir, eventDayDetectionFile)
	require.NoError(t, os.WriteFile(appPath, []byte(appLog), eventDayFilePerm))
	require.NoError(t, os.WriteFile(detectionPath, []byte(detectionLog), eventDayFilePerm))

	cfg := &logger.LoggingConfig{
		DefaultLevel: logger.DefaultLogLevel,
		Timezone:     "Local",
		Console:      &logger.ConsoleOutput{Enabled: false},
		FileOutput:   &logger.FileOutput{Enabled: true, Path: appPath, Level: logger.DefaultLogLevel},
		ModuleOutputs: map[string]logger.ModuleOutput{
			eventDayModule: {Enabled: true, FilePath: detectionPath, Level: logger.DefaultLogLevel},
		},
	}
	previousZone := time.Local
	previousLogger := logger.Global()
	time.Local = zone
	cl, err := logger.NewCentralLogger(cfg)
	require.NoError(t, err)
	logger.SetGlobal(cl)
	t.Cleanup(func() {
		_ = cl.Close()
		logger.SetGlobal(previousLogger)
		time.Local = previousZone
	})
}

func getEvents(t *testing.T, handle func(echo.Context) error, query string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?"+query, http.NoBody)
	rec := httptest.NewRecorder()
	// A handler that already sent an error response returns a sentinel, so only the
	// recorder is checked.
	_ = handle(e.NewContext(req, rec))
	return rec
}

// TestEventLogEndpointsSelectSkippedMidnightDay swaps time.Local and the global logger, so it
// must not run in parallel. Without a local midnight, the day must still be the requested
// calendar date: the entry late on the previous day is excluded and the one early on the day
// is returned.
func TestEventLogEndpointsSelectSkippedMidnightDay(t *testing.T) {
	for _, tc := range skippedMidnightCases(t) {
		t.Run(tc.zone, func(t *testing.T) {
			appLog := eventLogLine(tc.lateBefore, "before_op", "") + eventLogLine(tc.earlyOn, "on_op", "")
			species := `"species":"Parus major"`
			detectionLog := eventLogLine(tc.lateBefore, "approve_detection", species) +
				eventLogLine(tc.earlyOn, "approve_detection", species)
			useEventLogs(t, tc.loc, appLog, detectionLog)
			c := &Handler{Core: &apicore.Core{}}

			t.Run("operational", func(t *testing.T) {
				rec := getEvents(t, c.GetOperationalEvents, "date="+tc.date)
				require.Equal(t, http.StatusOK, rec.Code)
				var resp SystemEventsResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				require.Len(t, resp.Events, 1, "only the entry on the requested day is returned")
				assert.Equal(t, tc.earlyOn.Unix(), resp.Events[0].Timestamp.Unix())
			})

			t.Run("detections", func(t *testing.T) {
				rec := getEvents(t, c.GetDetectionEvents, "date="+tc.date)
				require.Equal(t, http.StatusOK, rec.Code)
				var resp DetectionEventsResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				require.Len(t, resp.Buckets, 1, "only the entry on the requested day is bucketed")
				assert.Equal(t, wallclock.FloorToHour(tc.earlyOn).Unix(), resp.Buckets[0].Timestamp.Unix(), "bucket of the hour the entry falls in")
			})
		})
	}
}

// TestEventLogEndpointsRejectMalformedDate pins the error behaviour for a bad date.
func TestEventLogEndpointsRejectMalformedDate(t *testing.T) {
	t.Parallel()
	c := &Handler{Core: &apicore.Core{Echo: echo.New()}}
	for name, handle := range map[string]func(echo.Context) error{
		"operational": c.GetOperationalEvents,
		"detections":  c.GetDetectionEvents,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := getEvents(t, handle, "date=2026-3-8")
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), "Invalid date format")
		})
	}
}
