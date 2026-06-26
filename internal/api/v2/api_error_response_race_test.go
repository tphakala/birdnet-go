// api_error_response_race_test.go: regression coverage for the per-controller
// debug-gated error verbosity read in newErrorResponse.
//
// newErrorResponse decides whether to expose raw err.Error() based on this
// controller's own WebServer.Debug flag. That read must stay per-controller (the
// shared global snapshot would couple otherwise-independent parallel tests) yet
// must not race the Settings.Store UpdateSettings performs on every save, and it
// must not take c.settingsMutex (it is reached from HandleError while
// UpdateSettings already holds the write lock, so an RLock would deadlock).
package api

import (
	"net/http"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
)

// TestNewErrorResponseConcurrentSettingsPublishIsRaceFree hammers
// newErrorResponse from multiple readers while writers republish the settings
// snapshot the same way UpdateSettings does (Settings.Store under c.settingsMutex).
// Under `go test -race`, a non-atomic read of the debug flag in newErrorResponse
// would be flagged as a data race against the concurrent Settings.Store.
func TestNewErrorResponseConcurrentSettingsPublishIsRaceFree(t *testing.T) {
	withRestoredGlobalSettings(t)

	controller := &Controller{Core: &apicore.Core{}}
	controller.Settings.Store(newValidTestSettings())

	testErr := echo.NewHTTPError(http.StatusInternalServerError, "raw internal detail")

	const (
		writers        = 2
		readers        = 4
		itersPerWorker = 200
	)

	var wg sync.WaitGroup

	for w := range writers {
		wg.Go(func() {
			for i := range itersPerWorker {
				snap := newValidTestSettings()
				snap.WebServer.Debug = (w+i)%2 == 0
				// Republish the same way UpdateSettings does: Settings.Store while
				// holding settingsMutex. The mutex serialises writers; the read side
				// in newErrorResponse stays lock-free via the atomic Load.
				controller.settingsMutex.Lock()
				controller.Settings.Store(snap)
				controller.settingsMutex.Unlock()
			}
		})
	}

	for range readers {
		wg.Go(func() {
			for range itersPerWorker {
				_ = controller.NewErrorResponse(testErr, "sanitized message", http.StatusInternalServerError)
			}
		})
	}

	wg.Wait()
}
