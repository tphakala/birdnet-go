package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestIngressPath_RaceAgainstStoreSettings exercises the request-path reader
// (ingressPath) concurrently with conf.StoreSettings publications. Must pass
// under `go test -race`.
//
// Guards against a regression where the basepath strip Pre middleware again
// starts reading fields off a pointer that can be mutated in place by a
// settings writer. The new copy-on-write path in api/v2.UpdateSettings keeps
// writers on a clone and publishes via atomic.Pointer.Store; readers always
// see one of the two valid snapshots.
func TestIngressPath_RaceAgainstStoreSettings(t *testing.T) {
	// Intentionally NOT t.Parallel(): this test mutates conf.settingsInstance
	// via StoreSettings. A parallel sibling that publishes its own snapshot
	// would make the "/alpha"/"/beta" assertion flaky even though the
	// atomic.Pointer itself is race-safe.

	const iterations = 5_000

	// Capture the pre-test global snapshot so we can restore it after the
	// test. Parallel tests in other files may depend on their own snapshot.
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })

	a := &conf.Settings{}
	a.WebServer.BasePath = "/alpha"
	b := &conf.Settings{}
	b.WebServer.BasePath = "/beta"

	conf.StoreSettings(a)

	e := echo.New()

	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range iterations {
			if i%2 == 0 {
				conf.StoreSettings(a)
			} else {
				conf.StoreSettings(b)
			}
		}
	})

	wg.Go(func() {
		for range iterations {
			req := httptest.NewRequest(http.MethodGet, "/any", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			bp := ingressPath(c, conf.GetSettings())
			if bp != "/alpha" && bp != "/beta" {
				t.Errorf("unexpected basepath: %q", bp)
				return
			}
		}
	})

	wg.Wait()
}
