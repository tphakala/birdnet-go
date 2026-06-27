// settings_race_test.go: shared settings-snapshot race-test helper for the
// api/v2 package.
//
// The GetAppConfig live-snapshot and concurrent-save race regression tests moved
// with the app domain to internal/api/v2/app. The shared helper below remains
// here because the package-api race tests (api_error_response_race_test.go,
// api_datastore_guard_test.go) still use it to snapshot and restore the
// process-global settings pointer around a test that publishes its own snapshot.
package api

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// withRestoredGlobalSettings snapshots the package-global settings pointer and
// restores it on cleanup so a test that publishes its own snapshot via
// conftest.SetTestSettings does not leak into sibling tests.
func withRestoredGlobalSettings(t *testing.T) {
	t.Helper()
	orig := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(orig) })
}
