// api_error_response_race_test.go: regression coverage for the per-controller
// debug-gated error verbosity read in newErrorResponse.
//
// newErrorResponse decides whether to expose raw err.Error() based on this
// controller's own WebServer.Debug flag. That read must stay per-controller (the
// shared global snapshot would couple otherwise-independent parallel tests) yet
// must not race the c.Settings republish UpdateSettings performs on every save,
// and it must not take c.settingsMutex (it is reached from HandleError while
// UpdateSettings already holds the write lock, so an RLock would deadlock).
package api

import (
	"net/http"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
)

// TestNewErrorResponseConcurrentSettingsPublishIsRaceFree hammers
// newErrorResponse from multiple readers while writers republish the settings
// snapshot the same way UpdateSettings does (c.Settings reassignment plus the
// per-controller atomic mirror, under c.settingsMutex). Under `go test -race`,
// the pre-fix bare c.Settings.WebServer.Debug read in newErrorResponse is
// flagged as a data race against the c.Settings write.
func TestNewErrorResponseConcurrentSettingsPublishIsRaceFree(t *testing.T) {
	withRestoredGlobalSettings(t)

	controller := &Controller{Settings: newValidTestSettings()}
	// Mirror production construction so the per-controller debug read resolves
	// via the lock-free atomic snapshot rather than the bare c.Settings field.
	controller.settingsAtomic.Store(controller.Settings)

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
				// Republish through the production single write path so this test
				// also guards against publishSettings updating only one of the two
				// fields. publishSettings requires the settings mutex held.
				controller.settingsMutex.Lock()
				controller.publishSettings(snap)
				controller.settingsMutex.Unlock()
			}
		})
	}

	for range readers {
		wg.Go(func() {
			for range itersPerWorker {
				_ = controller.newErrorResponse(testErr, "sanitized message", http.StatusInternalServerError)
			}
		})
	}

	wg.Wait()
}
