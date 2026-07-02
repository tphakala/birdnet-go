package api

import (
	"slices"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// goldenRoutes is the complete, sorted "METHOD PATH" set registered by a fully
// initialized v2 controller (initializeRoutes=true). It is the regression gate
// for the internal/api/v2 package split: every phase that moves handlers must
// keep this set byte-identical. The entries include echo's auto-generated
// route-not-found stubs for group prefixes, and the greedy GET /api/v2/audio/:id
// route registered directly on the Echo instance (see internal/api/v2/CLAUDE.md).
//
// To regenerate after an INTENTIONAL route change: run the test, take the sorted
// list it prints on failure, and replace this slice.
var goldenRoutes = []string{
	"DELETE /api/v2/alerts/history",
	"DELETE /api/v2/alerts/rules/:id",
	"DELETE /api/v2/detections/:id",
	"DELETE /api/v2/dynamic-thresholds",
	"DELETE /api/v2/dynamic-thresholds/:species",
	"DELETE /api/v2/integrations/mqtt/tls/certificate",
	"DELETE /api/v2/models/installed/:id",
	"DELETE /api/v2/notifications/:id",
	"DELETE /api/v2/species/notes/:id",
	"DELETE /api/v2/system/database/backup/jobs/:id",
	"DELETE /api/v2/tls/certificate",
	"GET /api/v2/alerts/history",
	"GET /api/v2/alerts/rules",
	"GET /api/v2/alerts/rules/:id",
	"GET /api/v2/alerts/rules/export",
	"GET /api/v2/alerts/schema",
	"GET /api/v2/analytics/confidence/distribution",
	"GET /api/v2/analytics/sources",
	"GET /api/v2/analytics/species/accumulation",
	"GET /api/v2/analytics/species/daily",
	"GET /api/v2/analytics/species/daily/batch",
	"GET /api/v2/analytics/species/detections/new",
	"GET /api/v2/analytics/species/diversity",
	"GET /api/v2/analytics/species/phenology",
	"GET /api/v2/analytics/species/summary",
	"GET /api/v2/analytics/species/thumbnails",
	"GET /api/v2/analytics/sun",
	"GET /api/v2/analytics/time/daily",
	"GET /api/v2/analytics/time/daily/batch",
	"GET /api/v2/analytics/time/dawn-onset",
	"GET /api/v2/analytics/time/distribution/hourly",
	"GET /api/v2/analytics/time/distribution/species",
	"GET /api/v2/analytics/time/heatmap",
	"GET /api/v2/analytics/time/hourly",
	"GET /api/v2/analytics/time/hourly/batch",
	"GET /api/v2/analytics/time/succession",
	"GET /api/v2/analytics/time/year-over-year",
	"GET /api/v2/app/config",
	"GET /api/v2/audio/:id",
	"GET /api/v2/auth/callback",
	"GET /api/v2/auth/status",
	"GET /api/v2/control/actions",
	"GET /api/v2/detections",
	"GET /api/v2/detections/:id",
	"GET /api/v2/detections/:id/time-of-day",
	"GET /api/v2/detections/ignored",
	"GET /api/v2/detections/recent",
	"GET /api/v2/detections/stream",
	"GET /api/v2/dynamic-thresholds",
	"GET /api/v2/dynamic-thresholds/:species",
	"GET /api/v2/dynamic-thresholds/:species/events",
	"GET /api/v2/dynamic-thresholds/stats",
	"GET /api/v2/filesystem/browse",
	"GET /api/v2/health",
	"GET /api/v2/health/audio",
	"GET /api/v2/import/jobs/:jobId/progress",
	"GET /api/v2/import/sources",
	"GET /api/v2/import/status",
	"GET /api/v2/integrations/birdweather/status",
	"GET /api/v2/integrations/mqtt/status",
	"GET /api/v2/integrations/mqtt/tls/certificate",
	"GET /api/v2/media/audio",
	"GET /api/v2/media/audio/:filename",
	"GET /api/v2/media/bird-image/:scientific_name",
	"GET /api/v2/media/image/:scientific_name",
	"GET /api/v2/media/species-image",
	"GET /api/v2/media/species-image/info",
	"GET /api/v2/media/spectrogram/:filename",
	"GET /api/v2/models",
	"GET /api/v2/models/catalog",
	"GET /api/v2/models/install/:id/progress",
	"GET /api/v2/models/installed",
	"GET /api/v2/notifications",
	"GET /api/v2/notifications/:id",
	"GET /api/v2/notifications/check-ntfy-server",
	"GET /api/v2/notifications/stream",
	"GET /api/v2/notifications/unread/count",
	"GET /api/v2/ping",
	"GET /api/v2/range/heatmap",
	"GET /api/v2/range/species/count",
	"GET /api/v2/range/species/csv",
	"GET /api/v2/range/species/list",
	"GET /api/v2/range/species/scores",
	"GET /api/v2/range/status",
	"GET /api/v2/settings",
	"GET /api/v2/settings/:section",
	"GET /api/v2/settings/dashboard",
	"GET /api/v2/settings/imageproviders",
	"GET /api/v2/settings/locales",
	"GET /api/v2/settings/systemid",
	"GET /api/v2/soundlevels/stream",
	"GET /api/v2/species",
	"GET /api/v2/species/:code/thumbnail",
	"GET /api/v2/species/:scientific_name/guide",
	"GET /api/v2/species/:scientific_name/notes",
	"GET /api/v2/species/:scientific_name/similar",
	"GET /api/v2/species/all",
	"GET /api/v2/species/dictionary/:locale",
	"GET /api/v2/species/taxonomy",
	"GET /api/v2/spectrogram/:id",
	"GET /api/v2/spectrogram/:id/status",
	"GET /api/v2/sse/status",
	"GET /api/v2/streams/audio-level",
	"GET /api/v2/streams/health",
	"GET /api/v2/streams/health/:url",
	"GET /api/v2/streams/health/stream",
	"GET /api/v2/streams/hls/status",
	"GET /api/v2/streams/hls/t/:streamToken/*",
	"GET /api/v2/streams/hls/t/:streamToken/playlist.m3u8",
	"GET /api/v2/streams/quiet-hours/status",
	"GET /api/v2/streams/sources",
	"GET /api/v2/streams/status",
	"GET /api/v2/support/download/:id",
	"GET /api/v2/support/status",
	"GET /api/v2/system/audio/active",
	"GET /api/v2/system/audio/devices",
	"GET /api/v2/system/audio/devices/capabilities",
	"GET /api/v2/system/audio/equalizer/config",
	"GET /api/v2/system/audio/sources",
	"GET /api/v2/system/database/backup/jobs",
	"GET /api/v2/system/database/backup/jobs/:id",
	"GET /api/v2/system/database/backup/jobs/:id/download",
	"GET /api/v2/system/database/legacy/status",
	"GET /api/v2/system/database/migration/prerequisites",
	"GET /api/v2/system/database/migration/status",
	"GET /api/v2/system/database/overview",
	"GET /api/v2/system/database/stats",
	"GET /api/v2/system/database/v2/stats",
	"GET /api/v2/system/diagnostics/errors",
	"GET /api/v2/system/diagnostics/report/:id",
	"GET /api/v2/system/diagnostics/status",
	"GET /api/v2/system/disks",
	"GET /api/v2/system/events/detections",
	"GET /api/v2/system/events/operational",
	"GET /api/v2/system/external-media",
	"GET /api/v2/system/inference",
	"GET /api/v2/system/info",
	"GET /api/v2/system/jobs",
	"GET /api/v2/system/models",
	"GET /api/v2/system/network-interfaces",
	"GET /api/v2/system/processes",
	"GET /api/v2/system/resources",
	"GET /api/v2/system/restart-status",
	"GET /api/v2/system/temperature/cpu",
	"GET /api/v2/taxonomy/family/:family",
	"GET /api/v2/taxonomy/genus/:genus",
	"GET /api/v2/taxonomy/tree/:scientific_name",
	"GET /api/v2/terminal/ws",
	"GET /api/v2/tls/certificate",
	"GET /api/v2/tls/certificate/download",
	"GET /api/v2/weather/daily/:date",
	"GET /api/v2/weather/detection/:id",
	"GET /api/v2/weather/hourly/:date",
	"GET /api/v2/weather/hourly/:date/:hour",
	"GET /api/v2/weather/latest",
	"GET /api/v2/weather/moon/:date",
	"GET /api/v2/weather/sun/:date",
	"PATCH /api/v2/alerts/rules/:id/toggle",
	"PATCH /api/v2/settings/:section",
	"POST /api/v2/alerts/rules",
	"POST /api/v2/alerts/rules/:id/test",
	"POST /api/v2/alerts/rules/import",
	"POST /api/v2/alerts/rules/reset-defaults",
	"POST /api/v2/app/wizard/dismiss",
	"POST /api/v2/audio/:id/clip",
	"POST /api/v2/audio/:id/process",
	"POST /api/v2/auth/login",
	"POST /api/v2/auth/logout",
	"POST /api/v2/control/rebuild-filter",
	"POST /api/v2/control/reload",
	"POST /api/v2/control/restart",
	"POST /api/v2/control/restart-container",
	"POST /api/v2/control/restart-server",
	"POST /api/v2/control/restart-source/:id",
	"POST /api/v2/detections/:id/lock",
	"POST /api/v2/detections/:id/review",
	"POST /api/v2/detections/batch/delete",
	"POST /api/v2/detections/batch/lock",
	"POST /api/v2/detections/batch/resolve",
	"POST /api/v2/detections/batch/review",
	"POST /api/v2/detections/ignore",
	"POST /api/v2/import/birdnet-pi",
	"POST /api/v2/import/elevate",
	"POST /api/v2/import/jobs/:jobId/cancel",
	"POST /api/v2/import/validate",
	"POST /api/v2/integrations/birdweather/test",
	"POST /api/v2/integrations/ebird/test",
	"POST /api/v2/integrations/mqtt/homeassistant/discovery",
	"POST /api/v2/integrations/mqtt/test",
	"POST /api/v2/integrations/mqtt/tls/certificate",
	"POST /api/v2/integrations/weather/test",
	"POST /api/v2/models/install/:id",
	"POST /api/v2/models/reinstall/:id",
	"POST /api/v2/notifications/test/new-species",
	"POST /api/v2/range/rebuild",
	"POST /api/v2/range/species/test",
	"POST /api/v2/search",
	"POST /api/v2/species/:scientific_name/notes",
	"POST /api/v2/spectrogram/:id/generate",
	"POST /api/v2/spectrogram/:id/process",
	"POST /api/v2/streams/analyze-channels",
	"POST /api/v2/streams/hls/:sourceID/start",
	"POST /api/v2/streams/hls/:sourceID/stop",
	"POST /api/v2/streams/hls/heartbeat",
	"POST /api/v2/streams/test",
	"POST /api/v2/support/generate",
	"POST /api/v2/system/database/backup",
	"POST /api/v2/system/database/backup/jobs",
	"POST /api/v2/system/database/legacy/cleanup",
	"POST /api/v2/system/database/migration/cancel",
	"POST /api/v2/system/database/migration/pause",
	"POST /api/v2/system/database/migration/resume",
	"POST /api/v2/system/database/migration/retry-validation",
	"POST /api/v2/system/database/migration/rollback",
	"POST /api/v2/system/database/migration/start",
	"POST /api/v2/system/diagnostics/run",
	"POST /api/v2/tls/certificate",
	"POST /api/v2/tls/certificate/generate",
	"PUT /api/v2/alerts/rules/:id",
	"PUT /api/v2/notifications/:id/acknowledge",
	"PUT /api/v2/notifications/:id/read",
	"PUT /api/v2/notifications/read-all",
	"PUT /api/v2/settings",
	"PUT /api/v2/species/notes/:id",
	"echo_route_not_found /api/v2",
	"echo_route_not_found /api/v2/*",
	"echo_route_not_found /api/v2/alerts",
	"echo_route_not_found /api/v2/alerts/*",
	"echo_route_not_found /api/v2/analytics",
	"echo_route_not_found /api/v2/analytics/*",
	"echo_route_not_found /api/v2/analytics/confidence",
	"echo_route_not_found /api/v2/analytics/confidence/*",
	"echo_route_not_found /api/v2/analytics/species",
	"echo_route_not_found /api/v2/analytics/species/*",
	"echo_route_not_found /api/v2/analytics/time",
	"echo_route_not_found /api/v2/analytics/time/*",
	"echo_route_not_found /api/v2/auth",
	"echo_route_not_found /api/v2/auth/*",
	"echo_route_not_found /api/v2/control",
	"echo_route_not_found /api/v2/control/*",
	"echo_route_not_found /api/v2/detections",
	"echo_route_not_found /api/v2/detections/*",
	"echo_route_not_found /api/v2/detections/batch",
	"echo_route_not_found /api/v2/detections/batch/*",
	"echo_route_not_found /api/v2/filesystem",
	"echo_route_not_found /api/v2/filesystem/*",
	"echo_route_not_found /api/v2/import",
	"echo_route_not_found /api/v2/import/*",
	"echo_route_not_found /api/v2/integrations",
	"echo_route_not_found /api/v2/integrations/*",
	"echo_route_not_found /api/v2/integrations/birdweather",
	"echo_route_not_found /api/v2/integrations/birdweather/*",
	"echo_route_not_found /api/v2/integrations/ebird",
	"echo_route_not_found /api/v2/integrations/ebird/*",
	"echo_route_not_found /api/v2/integrations/mqtt",
	"echo_route_not_found /api/v2/integrations/mqtt/*",
	"echo_route_not_found /api/v2/integrations/mqtt/tls",
	"echo_route_not_found /api/v2/integrations/mqtt/tls/*",
	"echo_route_not_found /api/v2/integrations/weather",
	"echo_route_not_found /api/v2/integrations/weather/*",
	"echo_route_not_found /api/v2/notifications",
	"echo_route_not_found /api/v2/notifications/*",
	"echo_route_not_found /api/v2/settings",
	"echo_route_not_found /api/v2/settings/*",
	"echo_route_not_found /api/v2/streams/hls",
	"echo_route_not_found /api/v2/streams/hls/*",
	"echo_route_not_found /api/v2/streams/hls/t",
	"echo_route_not_found /api/v2/streams/hls/t/*",
	"echo_route_not_found /api/v2/support",
	"echo_route_not_found /api/v2/support/*",
	"echo_route_not_found /api/v2/system",
	"echo_route_not_found /api/v2/system/*",
	"echo_route_not_found /api/v2/system/audio",
	"echo_route_not_found /api/v2/system/audio/*",
	"echo_route_not_found /api/v2/system/database",
	"echo_route_not_found /api/v2/system/database/*",
	"echo_route_not_found /api/v2/system/database/backup/jobs",
	"echo_route_not_found /api/v2/system/database/backup/jobs/*",
	"echo_route_not_found /api/v2/system/database/legacy",
	"echo_route_not_found /api/v2/system/database/legacy/*",
	"echo_route_not_found /api/v2/system/database/migration",
	"echo_route_not_found /api/v2/system/database/migration/*",
	"echo_route_not_found /api/v2/system/diagnostics",
	"echo_route_not_found /api/v2/system/diagnostics/*",
	"echo_route_not_found /api/v2/system/events",
	"echo_route_not_found /api/v2/system/events/*",
	"echo_route_not_found /api/v2/terminal",
	"echo_route_not_found /api/v2/terminal/*",
	"echo_route_not_found /api/v2/tls",
	"echo_route_not_found /api/v2/tls/*",
	"echo_route_not_found /api/v2/weather",
	"echo_route_not_found /api/v2/weather/*",
}

// buildFullyRoutedController constructs a controller with every route registered,
// mirroring production wiring (NewWithOptions with initializeRoutes=true).
func buildFullyRoutedController(t *testing.T) *echo.Echo {
	t.Helper()
	e := echo.New()
	mockDS := mocks.NewMockInterface(t)
	// Permissive expectations for DS methods that background goroutines started
	// by initRoutes (e.g. the app-event pruning worker) may call before Shutdown.
	mockDS.EXPECT().PruneAppEvents(mock.Anything, mock.Anything).Return(int64(0), nil).Maybe()
	settings := apitest.NewValidTestSettings()
	settings.Realtime.Audio.Export.Path = t.TempDir()
	apitest.PublishTestSettings(t, settings)
	birdImageCache := &imageprovider.BirdImageCache{}
	sunCalc := suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)
	controlChan := make(chan string, testControlChannelBuf)
	metrics, err := observability.NewMetrics()
	require.NoError(t, err)
	c, err := NewWithOptions(e, mockDS, settings, birdImageCache, sunCalc, controlChan, metrics, true)
	require.NoError(t, err)
	t.Cleanup(c.Shutdown)
	return e
}

func sortedRouteSet(e *echo.Echo) []string {
	routes := e.Routes()
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, r.Method+" "+r.Path)
	}
	slices.Sort(out)
	return out
}

// TestRouteEnumerationMatchesGolden pins the full method+path route set so the
// API v2 package-split phases cannot silently add, drop, or reorder a route.
func TestRouteEnumerationMatchesGolden(t *testing.T) {
	// NOT parallel: apitest.PublishTestSettings mutates the process-global snapshot.
	e := buildFullyRoutedController(t)
	got := sortedRouteSet(e)

	assert.Equal(t, goldenRoutes, got,
		"registered route set changed; if intentional, regenerate goldenRoutes from the sorted list")
}

// TestRouteEnumerationIsDeterministic builds the controller twice and asserts the
// route set is identical, guarding against registration-order nondeterminism.
func TestRouteEnumerationIsDeterministic(t *testing.T) {
	// NOT parallel: apitest.PublishTestSettings mutates the process-global snapshot.
	first := sortedRouteSet(buildFullyRoutedController(t))
	second := sortedRouteSet(buildFullyRoutedController(t))
	assert.Equal(t, first, second)
}

// TestGreedyAudioRouteRegisteredOnEcho pins that GET /api/v2/audio/:id is
// registered directly on the Echo instance (not the group), preserving the
// documented greedy-route behavior that catches all /api/v2/audio/* paths.
func TestGreedyAudioRouteRegisteredOnEcho(t *testing.T) {
	// NOT parallel: apitest.PublishTestSettings mutates the process-global snapshot.
	e := buildFullyRoutedController(t)
	found := false
	for _, r := range e.Routes() {
		if r.Method == "GET" && r.Path == "/api/v2/audio/:id" {
			found = true
		}
	}
	assert.True(t, found, "greedy GET /api/v2/audio/:id must be registered")
}
