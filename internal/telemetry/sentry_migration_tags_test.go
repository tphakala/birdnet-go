package telemetry

import (
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// migrationTagSetters lists the two migration tag setters. Their boolean result
// means the tag was applied; the legacy schema monitor uses it for the
// migration_state tag to retry an unapplied value.
var migrationTagSetters = []struct {
	name   string
	key    string
	setter func(string) bool
}{
	{"migration_state", migrationStateTagKey, SetMigrationStateTag},
	{"migration_prereq", migrationPrereqTagKey, SetMigrationPrereqTag},
}

// hubTag returns the tag value on the hub's top scope, or "" when it is not set.
func hubTag(t *testing.T, key string) string {
	t.Helper()
	event := sentry.CurrentHub().Scope().ApplyToEvent(&sentry.Event{}, nil, nil)
	require.NotNil(t, event)
	return event.Tags[key]
}

// useIsolatedHub gives the test its own top scope on the process-wide hub and
// pins the telemetry test mode, restoring both afterwards.
func useIsolatedHub(t *testing.T, mode int32) {
	t.Helper()
	prevMode := testMode.Load()
	t.Cleanup(func() { testMode.Store(prevMode) })
	testMode.Store(mode)

	hub := sentry.CurrentHub()
	hub.PushScope()
	t.Cleanup(hub.PopScope)
}

// TestMigrationTagSetters pins the contract the monitor depends on: false means
// the tag was not applied (telemetry off or empty value) and sets nothing; true
// means it is on the scope. Not parallel: it changes the telemetry test mode
// and the process-wide Sentry hub.
func TestMigrationTagSetters(t *testing.T) {
	for _, tt := range migrationTagSetters {
		t.Run(tt.name+"/applied", func(t *testing.T) {
			useIsolatedHub(t, testModeEnabled)
			assert.True(t, tt.setter("migrating"))
			assert.Equal(t, "migrating", hubTag(t, tt.key))
		})
		t.Run(tt.name+"/empty value", func(t *testing.T) {
			useIsolatedHub(t, testModeEnabled)
			assert.False(t, tt.setter(""))
			assert.Empty(t, hubTag(t, tt.key))
		})
		t.Run(tt.name+"/telemetry off", func(t *testing.T) {
			useIsolatedHub(t, testModeDisabled)
			bindTestClient(t, nil) // no bound client counts as telemetry off
			assert.False(t, tt.setter("migrating"))
			assert.Empty(t, hubTag(t, tt.key))
		})
	}
}
