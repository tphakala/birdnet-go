package conf

import (
	"sync"
	"testing"
)

// TestSettings_GetStore_NoRace runs a writer and a reader goroutine
// concurrently and asserts the reader never observes a torn value. Must pass
// under `go test -race`.
//
// The previous design (settingsInstance *Settings + RWMutex) guarded only the
// pointer lookup; readers holding the pointer raced with writers mutating
// fields in place. The new design publishes a new *Settings snapshot per
// update via atomic.Pointer, so readers always see one of the two valid
// values.
func TestSettings_GetStore_NoRace(t *testing.T) {
	// Intentionally NOT t.Parallel(): this test mutates the package-global
	// settingsInstance via StoreSettings. Running in parallel with any
	// sibling test that touches the global (now or in the future) would
	// race even though the atomic.Pointer is itself safe, because the
	// sibling could publish a snapshot with WebServer.BasePath neither
	// "/a" nor "/b" and the reader goroutine would flag a torn read.

	const iterations = 10_000

	// Capture the current snapshot so we can restore it after the test.
	// Tests that set global state must not leak it to siblings.
	prev := settingsInstance.Load()
	t.Cleanup(func() { settingsInstance.Store(prev) })

	base := &Settings{}
	base.WebServer.BasePath = "/a"
	StoreSettings(base)

	var wg sync.WaitGroup

	wg.Go(func() {
		for i := range iterations {
			cp := CloneSettings(GetSettings())
			if i%2 == 0 {
				cp.WebServer.BasePath = "/a"
			} else {
				cp.WebServer.BasePath = "/b"
			}
			StoreSettings(cp)
		}
	})

	wg.Go(func() {
		for range iterations {
			bp := GetSettings().WebServer.BasePath
			if bp != "/a" && bp != "/b" {
				t.Errorf("torn read: %q", bp)
				return
			}
		}
	})

	wg.Wait()
}
