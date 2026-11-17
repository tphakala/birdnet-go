package analysis

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	apicontrol "github.com/tphakala/birdnet-go/internal/api/v2/control"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

// restartQueueCapacity is the restart channel capacity of the table test. With a
// capacity of one, a second token would be dropped and the length would stay at
// one. Room for a second token lets a handler that queued the restart twice show
// a length of two.
const restartQueueCapacity = 2

// TestHandleControlSignal_RestartCapture pins that both the restart_audio_capture
// settings signal and the restart_analysis control API signal queue exactly one
// capture restart, without reconfiguring the sources or re-planning the cadence.
func TestHandleControlSignal_RestartCapture(t *testing.T) {
	t.Parallel()

	for _, signal := range []string{signalRestartAudioCapture, apicontrol.SignalRestartAnalysis} {
		t.Run(signal, func(t *testing.T) {
			t.Parallel()

			var reconfigures, replans int
			restartChan := make(chan struct{}, restartQueueCapacity)
			cm := &ControlMonitor{
				restartChan:          restartChan,
				reconfigureSourcesFn: func() { reconfigures++ },
				replanCadenceFn:      func() { replans++ },
			}
			cm.handleControlSignal(signal)

			assert.Len(t, restartChan, 1, "the signal must queue one capture restart")
			assert.Zero(t, reconfigures, "the signal must not reconfigure the sources")
			assert.Zero(t, replans, "the signal must not re-plan the cadence")
		})
	}
}

// TestRestartAnalysisRoute_RestartsCapture drives POST /api/v2/control/restart
// through the real control routes into a running ControlMonitor and expects the
// capture restart the endpoint documents. Before the monitor handled the signal
// the endpoint answered 200 and nothing happened.
func TestRestartAnalysisRoute_RestartsCapture(t *testing.T) {
	t.Parallel()

	// The restart handler needs no published settings (its debug logging only
	// reads the process-global snapshot, and tolerates it being unset), so the
	// core does not publish them.
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
	core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }

	controlChan := make(chan string, 1)
	apicontrol.New(core, controlChan).RegisterRoutes(core.Group)

	restartChan := make(chan struct{}, 1)
	quitChan := make(chan struct{})
	cm := &ControlMonitor{
		controlChan: controlChan,
		quitChan:    quitChan,
		restartChan: restartChan,
	}
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		cm.monitor()
	}()
	t.Cleanup(func() {
		close(quitChan)
		<-monitorDone
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v2/control/restart", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	testutil.WaitForChannel(t, restartChan, testutil.LongTestTimeout,
		"the monitor did not queue a capture restart for the restart_analysis signal")
}
