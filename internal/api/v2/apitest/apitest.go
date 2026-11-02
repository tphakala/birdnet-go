// Package apitest provides reusable, Core-level test scaffolding for the
// internal/api/v2 packages. It exists so the per-domain test packages created by
// the api/v2 split can build a *apicore.Core (and the supporting mocks, settings,
// HTTP scaffolding, and assertions) without each one re-deriving the setup.
//
// Importability and the import-cycle rule: this is a regular (non-_test.go)
// package because a _test.go file cannot be imported by another package's tests.
// It has no production importers. apitest imports ONLY apicore and leaf or
// test-support packages (conf, conf/conftest, datastore, datastore/mocks,
// imageprovider, suncalc, observability, httpclient). It MUST NEVER import the
// api/v2 facade (package api) or any api/v2 domain package: package api's tests
// and the future domain tests import apitest, so importing the facade or a domain
// from here would close an import cycle. Consequently apitest builds and returns
// only *apicore.Core, never *api.Controller.
//
// Isolation contract: helpers here guarantee per-binary isolation so the
// now-parallel api/v2 test binaries cannot collide. Scratch space comes from
// t.TempDir() only, there are no fixed TCP ports (use echo.New() + httptest), and
// the datastore is always a mock (no shared on-disk DB). See NewCore.
package apitest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// NewValidTestSettings returns a *conf.Settings populated with minimal values
// that pass conf.ValidateSettings(). Tests that create inline controllers or
// cores should call this and then override the fields they care about.
//
// Debug defaults to false to match production behavior. Tests that specifically
// need debug mode should set settings.WebServer.Debug = true.
func NewValidTestSettings() *conf.Settings {
	return &conf.Settings{
		BirdNET: conf.BirdNETConfig{
			Sensitivity: 1.0,
			Threshold:   0.8,
			Locale:      "en",
		},
		WebServer: conf.WebServerSettings{
			Debug: false,
			LiveStream: conf.LiveStreamSettings{
				BitRate:       128,
				SegmentLength: 5,
			},
		},
		Security: conf.Security{
			SessionDuration: 168 * time.Hour,
		},
		Realtime: conf.RealtimeSettings{
			Interval: 15,
			Dashboard: conf.Dashboard{
				SummaryLimit: 100,
			},
			Weather: conf.WeatherSettings{
				PollInterval: 30,
			},
		},
	}
}

// PublishTestSettings publishes settings as the process-global atomic snapshot so
// handlers reading via apicore.Core.CurrentSettings() observe them, and restores
// the previous snapshot on cleanup so the publish does not leak into sibling
// tests.
//
// IMPORTANT: tests using this helper must NOT be parallel. It mutates the
// process-global settings snapshot, so parallel tests in the same binary would
// observe each other's settings and flake. A parallel test, or a test with a
// parallel ancestor, fails with a message naming WithoutSettingsPublish and
// publishes nothing. Cross-binary isolation is automatic: each api/v2 package
// compiles to its own test binary (its own OS process), so the global snapshot
// is never shared across domains.
//
// Not caught: t.Parallel() after publishing in the same test (it panics with
// the standard testing message, which does not name this helper); a serial
// parent that publishes and then starts a parallel subtest; benchmarks
// (*testing.B); and code that calls conftest.SetTestSettings or
// conf.StoreSettings directly.
//
// CurrentSettings() prefers the process-global snapshot over a core-cached
// Settings field, so tests that inject a per-core *conf.Settings must publish it
// here for request-time reads to observe it (pass nil to exercise a handler's
// nil-settings fallback path).
func PublishTestSettings(tb testing.TB, settings *conf.Settings) {
	tb.Helper()
	if refuseParallelPublish(tb) {
		return
	}
	prev := conf.GetSettings()
	conftest.SetTestSettings(settings)
	tb.Cleanup(func() { conftest.SetTestSettings(prev) })
}

const (
	// settingsPublishGuardEnvKey names the variable the publish guard sets. Its
	// value does not matter: tb.Setenv checks that the test and its ancestors are
	// not parallel and marks the test so a later t.Parallel() panics. The key sits
	// outside the BIRDNET_ prefix so it never reads as a configuration variable.
	settingsPublishGuardEnvKey = "APITEST_SETTINGS_PUBLISHED"

	// settingsPublishGuardEnvValue is the placeholder value the guard stores.
	settingsPublishGuardEnvValue = "1"

	// parallelPublishFatalFormat is the failure message for a parallel test that
	// publishes settings. Its one verb receives the recovered testing panic.
	parallelPublishFatalFormat = "apitest: PublishTestSettings mutates the process-global settings snapshot, " +
		"so the calling test and its ancestors must not be parallel (%v); " +
		"build the core with apitest.WithoutSettingsPublish() or remove t.Parallel()"
)

// refuseParallelPublish fails tb and reports true when tb is parallel or has a
// parallel ancestor. A real test ends in Fatalf; a recording testing.TB returns,
// so callers must stop on true before they publish.
func refuseParallelPublish(tb testing.TB) bool {
	tb.Helper()
	conflict := denyParallelPublish(tb)
	if conflict == nil {
		return false
	}
	tb.Fatalf(parallelPublishFatalFormat, conflict)
	return true
}

// denyParallelPublish returns the panic tb.Setenv raised because the test or an
// ancestor is parallel, or nil when tb may publish. Any other panic also comes
// back, so its text reaches the failure message.
func denyParallelPublish(tb testing.TB) (conflict any) {
	tb.Helper()
	defer func() { conflict = recover() }()
	tb.Setenv(settingsPublishGuardEnvKey, settingsPublishGuardEnvValue)
	return nil
}

// NewTestMetrics creates a new observability.Metrics instance for testing.
func NewTestMetrics(t *testing.T) *observability.Metrics {
	t.Helper()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "apitest: creating test metrics")
	return metrics
}
