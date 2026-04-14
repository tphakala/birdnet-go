package telemetry

import (
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestUptimeBucket verifies the bucket boundaries documented in the package
// constants: values below uptimeStartupCutoff are "startup", values below
// uptimeWarmupCutoff are "warmup", and anything else is "running".
func TestUptimeBucket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"zero", 0, uptimeBucketStartup},
		{"one second", 1 * time.Second, uptimeBucketStartup},
		{"just below startup cutoff", 59 * time.Second, uptimeBucketStartup},
		{"exactly startup cutoff", 60 * time.Second, uptimeBucketWarmup},
		{"just below warmup cutoff", 599 * time.Second, uptimeBucketWarmup},
		{"exactly warmup cutoff", 600 * time.Second, uptimeBucketRunning},
		{"one hour", 1 * time.Hour, uptimeBucketRunning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, uptimeBucket(tt.duration))
		})
	}
}

// TestEnrichEventWithUptime_SetsTagAndContext verifies that the enrichment
// helper writes both the coarse uptime_bucket tag and the integer
// uptime_seconds context value on a fresh event.
func TestEnrichEventWithUptime_SetsTagAndContext(t *testing.T) {
	// Ensure appStartTime is initialized. The sync.Once makes this idempotent,
	// so the test does not disturb other tests in the package.
	initAppStartTime()

	event := &sentry.Event{}
	enrichEventWithUptime(event)

	require.NotNil(t, event.Tags, "Tags must be initialized by enrichEventWithUptime")
	bucket, ok := event.Tags[uptimeTagKey]
	require.True(t, ok, "event must carry %q tag", uptimeTagKey)
	assert.Contains(t,
		[]string{uptimeBucketStartup, uptimeBucketWarmup, uptimeBucketRunning},
		bucket,
		"bucket must be one of the documented values")

	require.NotNil(t, event.Contexts, "Contexts must be initialized by enrichEventWithUptime")
	runtimeCtx, ok := event.Contexts[uptimeContextKey]
	require.True(t, ok, "event must carry %q context", uptimeContextKey)

	rawSeconds, ok := runtimeCtx[uptimeContextField]
	require.True(t, ok, "context must carry %q field", uptimeContextField)

	secs, ok := rawSeconds.(int)
	require.True(t, ok, "%s must be stored as int, got %T", uptimeContextField, rawSeconds)
	assert.GreaterOrEqual(t, secs, 0, "uptime seconds must not be negative")
}

// TestEnrichEventWithUptime_NilSafe guards against panics when the event's
// Tags / Contexts maps start out nil — a common case for freshly constructed
// *sentry.Event values inside a BeforeSend hook.
func TestEnrichEventWithUptime_NilSafe(t *testing.T) {
	initAppStartTime()

	event := &sentry.Event{
		Tags:     nil,
		Contexts: nil,
	}

	require.NotPanics(t, func() {
		enrichEventWithUptime(event)
	})

	assert.NotNil(t, event.Tags, "Tags must be populated even when starting nil")
	assert.NotNil(t, event.Contexts, "Contexts must be populated even when starting nil")
}

// TestEnrichEventWithUptime_NilEvent guards against panics when a nil event
// is passed (defensive behavior so the hook never drops an unrelated event).
func TestEnrichEventWithUptime_NilEvent(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		enrichEventWithUptime(nil)
	})
}

// TestEnrichEventWithUptime_PreservesExistingTags ensures enrichment does not
// clobber tags already on the event (the privacy-filter pass may leave tags
// in place that uptime enrichment must not remove).
func TestEnrichEventWithUptime_PreservesExistingTags(t *testing.T) {
	initAppStartTime()

	event := &sentry.Event{
		Tags: map[string]string{
			"component": "test",
		},
		Contexts: map[string]sentry.Context{
			"application": {
				"name": "BirdNET-Go",
			},
		},
	}

	enrichEventWithUptime(event)

	assert.Equal(t, "test", event.Tags["component"], "existing tags must survive enrichment")
	assert.Contains(t, event.Tags, uptimeTagKey)

	appCtx, ok := event.Contexts["application"]
	require.True(t, ok, "existing contexts must survive enrichment")
	assert.Equal(t, "BirdNET-Go", appCtx["name"])
}

// TestEnrichEventWithUptime_MergesExistingRuntimeContext verifies that if the
// runtime_state context already has fields (added by another part of the
// system in the future), enrichment adds uptime_seconds without dropping
// those fields.
func TestEnrichEventWithUptime_MergesExistingRuntimeContext(t *testing.T) {
	initAppStartTime()

	event := &sentry.Event{
		Contexts: map[string]sentry.Context{
			uptimeContextKey: {
				"some_other_field": "preserved",
			},
		},
	}

	enrichEventWithUptime(event)

	runtimeCtx, ok := event.Contexts[uptimeContextKey]
	require.True(t, ok)
	assert.Equal(t, "preserved", runtimeCtx["some_other_field"],
		"existing context fields must survive uptime enrichment")

	_, hasUptime := runtimeCtx[uptimeContextField]
	assert.True(t, hasUptime, "uptime_seconds must be added alongside existing fields")
}

// TestInitAppStartTime_Idempotent verifies that calling initAppStartTime
// multiple times does not rewind the clock.
func TestInitAppStartTime_Idempotent(t *testing.T) {
	initAppStartTime()
	first := appStartTime

	// Force enough time to pass that a naive re-initialization would yield
	// a later value.
	time.Sleep(2 * time.Millisecond)

	initAppStartTime()
	assert.Equal(t, first, appStartTime, "sync.Once must prevent re-initialization")
}

// TestAppStartTimeInitializedAtPackageInit verifies that the baseline is set
// at package load time (via `init()`), not deferred until InitSentry runs. A
// zero-value appStartTime here would signal that the init was moved or lost.
func TestAppStartTimeInitializedAtPackageInit(t *testing.T) {
	t.Parallel()
	assert.False(t, appStartTime.IsZero(),
		"appStartTime must be populated by package init() before any test runs")
}

// TestCreateBeforeSendHook_NilEvent verifies that the hook returned by
// createBeforeSendHook handles a nil event safely (returns nil, no panic).
// Without this guard, applyPrivacyFilters would dereference nil before
// enrichEventWithUptime could clean up.
func TestCreateBeforeSendHook_NilEvent(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{
		Sentry: conf.SentrySettings{Enabled: true, Debug: false},
	}
	hook := createBeforeSendHook(settings)

	var got *sentry.Event
	require.NotPanics(t, func() {
		got = hook(nil, nil)
	})
	assert.Nil(t, got, "hook must drop a nil event cleanly")

	// Same guarantee in debug mode — the debug privacy filter also
	// dereferences event and would panic without the guard.
	settings.Sentry.Debug = true
	hook = createBeforeSendHook(settings)
	require.NotPanics(t, func() {
		got = hook(nil, nil)
	})
	assert.Nil(t, got, "hook must drop a nil event cleanly (debug branch)")
}

// TestCreateBeforeSendHook_EnrichesEvent verifies the hook still annotates
// non-nil events with uptime after privacy filtering, i.e. the nil guard
// did not break the happy path.
func TestCreateBeforeSendHook_EnrichesEvent(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{
		Sentry: conf.SentrySettings{Enabled: true, Debug: false},
	}
	hook := createBeforeSendHook(settings)

	event := &sentry.Event{}
	got := hook(event, nil)
	require.NotNil(t, got, "hook must not drop a non-nil event")
	require.NotNil(t, got.Tags)
	assert.Contains(t, got.Tags, uptimeTagKey, "hook must attach uptime_bucket tag")
	require.NotNil(t, got.Contexts)
	assert.Contains(t, got.Contexts, uptimeContextKey, "hook must attach runtime_state context")
}
