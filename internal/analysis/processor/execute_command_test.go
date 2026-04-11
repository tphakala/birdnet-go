package processor

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// resolveValidExecutable returns an absolute path to an executable that
// is known to exist on the test host. Used by tests that exercise the
// action-dispatch pipeline and need a path that survives the per-call
// command-path validation gate without relying on fixture scripts.
// Tests that cannot find any usable executable are skipped rather than
// failing — this keeps CI portable across containerized runners.
func resolveValidExecutable(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"/bin/sh", "/bin/true", "/usr/bin/true"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Skip("no suitable absolute executable found for action-dispatch test")
	return ""
}

// resolveTwoValidExecutables returns two distinct absolute paths to
// executables that exist on the test host. Tests that exercise
// multi-action dispatch and need to assert both commands appear in the
// produced action list use this to avoid tripping the per-call
// command-path validation gate. Skips gracefully on runners that only
// have one of the candidates (rare for any POSIX CI image).
func resolveTwoValidExecutables(t *testing.T) (first, second string) {
	t.Helper()
	found := make([]string, 0, 2)
	for _, candidate := range []string{"/bin/sh", "/bin/true", "/usr/bin/true", "/bin/ls", "/usr/bin/env"} {
		if _, err := os.Stat(candidate); err == nil {
			found = append(found, candidate)
			if len(found) == 2 {
				return found[0], found[1]
			}
		}
	}
	t.Skip("fewer than two suitable absolute executables found for multi-action dispatch test")
	return "", ""
}

// TestExecuteCommandAction_WithoutParameters tests that ExecuteCommand actions
// are executed even when no parameters are specified.
// Bug: https://github.com/tphakala/birdnet-go/discussions/1757
// Root cause: processor.go line 1116 had condition `if len(actionConfig.Parameters) > 0`
// which prevented commands without parameters from being added to the action list.
func TestExecuteCommandAction_WithoutParameters(t *testing.T) {
	t.Parallel()

	// Use a real executable so the per-dispatch command path validation
	// gate in getActionsForItem lets the action through. The original
	// bug this test guards against is unrelated to path validity.
	validCmd := resolveValidExecutable(t)

	tests := []struct {
		name            string
		speciesConfig   conf.SpeciesConfig
		expectAction    bool
		expectedCommand string
	}{
		{
			name: "command without parameters should still create action",
			speciesConfig: conf.SpeciesConfig{
				Threshold: 0.8,
				Actions: []conf.SpeciesAction{
					{
						Type:       "ExecuteCommand",
						Command:    validCmd,
						Parameters: []string{}, // Empty parameters - this was the bug!
					},
				},
			},
			expectAction:    true,
			expectedCommand: validCmd,
		},
		{
			name: "command with nil parameters should still create action",
			speciesConfig: conf.SpeciesConfig{
				Threshold: 0.8,
				Actions: []conf.SpeciesAction{
					{
						Type:       "ExecuteCommand",
						Command:    validCmd,
						Parameters: nil, // Nil parameters - should also work
					},
				},
			},
			expectAction:    true,
			expectedCommand: validCmd,
		},
		{
			name: "command with parameters should create action with params",
			speciesConfig: conf.SpeciesConfig{
				Threshold: 0.8,
				Actions: []conf.SpeciesAction{
					{
						Type:       "ExecuteCommand",
						Command:    validCmd,
						Parameters: []string{"CommonName", "Confidence"},
					},
				},
			},
			expectAction:    true,
			expectedCommand: validCmd,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a processor with the species config
			processor := &Processor{
				Settings: &conf.Settings{
					Debug: true,
					Realtime: conf.RealtimeSettings{
						Species: conf.SpeciesSettings{
							Config: map[string]conf.SpeciesConfig{
								"american robin": tt.speciesConfig,
							},
						},
					},
				},
				EventTracker: NewEventTracker(0), // Disable rate limiting for tests
			}

			// Create a detection for "American Robin"
			detection := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)

			// Get actions for the detection
			actions := processor.getActionsForItem(&detection)

			if tt.expectAction {
				// Find the ExecuteCommandAction in the list
				var foundExecuteAction *ExecuteCommandAction
				for _, action := range actions {
					if execAction, ok := action.(*ExecuteCommandAction); ok {
						foundExecuteAction = execAction
						break
					}
				}

				require.NotNil(t, foundExecuteAction, "Expected ExecuteCommandAction to be in the actions list")
				assert.Equal(t, tt.expectedCommand, foundExecuteAction.Command)
			}
		})
	}
}

// TestExecuteCommandAction_MultipleActionsWithMixedParams tests that both parameterized
// and non-parameterized commands are properly added when configured together.
func TestExecuteCommandAction_MultipleActionsWithMixedParams(t *testing.T) {
	t.Parallel()

	simpleCmd, detailedCmd := resolveTwoValidExecutables(t)

	processor := &Processor{
		Settings: &conf.Settings{
			Debug: true,
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"american robin": {
							Threshold: 0.8,
							Actions: []conf.SpeciesAction{
								{
									Type:       "ExecuteCommand",
									Command:    simpleCmd,
									Parameters: []string{}, // No parameters
								},
								{
									Type:       "ExecuteCommand",
									Command:    detailedCmd,
									Parameters: []string{"CommonName", "Confidence"},
								},
							},
						},
					},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	detection := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)
	actions := processor.getActionsForItem(&detection)

	// Count ExecuteCommandActions
	var executeActions []*ExecuteCommandAction
	for _, action := range actions {
		if execAction, ok := action.(*ExecuteCommandAction); ok {
			executeActions = append(executeActions, execAction)
		}
	}

	// Both commands should be in the actions list
	require.Len(t, executeActions, 2, "Expected both ExecuteCommand actions to be created")

	// Verify both commands are present (order may vary, so check both)
	commands := make(map[string]bool)
	for _, action := range executeActions {
		commands[action.Command] = true
	}

	assert.True(t, commands[simpleCmd], "%s should be in actions", simpleCmd)
	assert.True(t, commands[detailedCmd], "%s should be in actions", detailedCmd)
}

// TestExecuteCommandAction_ExecuteDefaultsWithNoParams tests that executeDefaults
// works correctly even when the custom command has no parameters.
func TestExecuteCommandAction_ExecuteDefaultsWithNoParams(t *testing.T) {
	t.Parallel()

	validCmd := resolveValidExecutable(t)

	processor := &Processor{
		Settings: &conf.Settings{
			Debug: true,
			Realtime: conf.RealtimeSettings{
				Log: struct {
					Enabled bool   `yaml:"enabled" json:"enabled"`
					Path    string `yaml:"path" json:"path"`
				}{
					Enabled: true,
				},
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"american robin": {
							Threshold: 0.8,
							Actions: []conf.SpeciesAction{
								{
									Type:            "ExecuteCommand",
									Command:         validCmd,
									Parameters:      []string{},
									ExecuteDefaults: true, // Should also run default actions
								},
							},
						},
					},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	detection := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)
	actions := processor.getActionsForItem(&detection)

	// Should have ExecuteCommandAction AND default actions (like LogAction)
	var hasExecuteCommand, hasLogAction bool
	for _, action := range actions {
		switch action.(type) {
		case *ExecuteCommandAction:
			hasExecuteCommand = true
		case *LogAction:
			hasLogAction = true
		}
	}

	assert.True(t, hasExecuteCommand, "Expected ExecuteCommandAction to be present")
	assert.True(t, hasLogAction, "Expected LogAction (default action) to be present when ExecuteDefaults is true")
}

// TestValidateCustomCommandActions_SkipsInvalidPath asserts that the
// startup validation gate correctly flags command paths that point at a
// non-existent file. Subsequent per-detection dispatch must not produce
// an ExecuteCommandAction for those species, so the dual-fingerprint
// Sentry event (validation re-wrap + file-io original) can no longer
// fire on every detection.
func TestValidateCustomCommandActions_SkipsInvalidPath(t *testing.T) {
	t.Parallel()

	missingPath := "/tmp/birdnet_go_does_not_exist_47f3e8a1.sh"

	processor := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"american robin": {
							Threshold: 0.8,
							Actions: []conf.SpeciesAction{
								{
									Type:    "ExecuteCommand",
									Command: missingPath,
								},
							},
						},
					},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	// Run the same startup validation New() runs.
	processor.validateCustomCommandActions(processor.Settings)

	_, flagged := processor.invalidCommandPaths.Load(missingPath)
	require.True(t, flagged,
		"missing command path must be recorded as invalid at startup")

	// At dispatch time, the ExecuteCommand action must NOT be registered.
	det := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)
	actions := processor.getActionsForItem(&det)
	for _, action := range actions {
		if _, ok := action.(*ExecuteCommandAction); ok {
			t.Fatalf("invalid command path %q must not produce an ExecuteCommandAction", missingPath)
		}
	}
}

// TestValidateCustomCommandActions_AllowsValidPath asserts that a valid
// command path is NOT flagged by the startup gate and the action is
// still dispatched as before.
func TestValidateCustomCommandActions_AllowsValidPath(t *testing.T) {
	t.Parallel()

	validCmd := resolveValidExecutable(t)

	processor := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"american robin": {
							Threshold: 0.8,
							Actions: []conf.SpeciesAction{
								{Type: "ExecuteCommand", Command: validCmd},
							},
						},
					},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	processor.validateCustomCommandActions(processor.Settings)

	var flaggedCount int
	processor.invalidCommandPaths.Range(func(_, _ any) bool {
		flaggedCount++
		return true
	})
	assert.Equal(t, 0, flaggedCount,
		"valid command path must not be flagged at startup")

	det := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)
	actions := processor.getActionsForItem(&det)

	var found *ExecuteCommandAction
	for _, a := range actions {
		if ea, ok := a.(*ExecuteCommandAction); ok {
			found = ea
			break
		}
	}
	require.NotNil(t, found, "valid command path must still produce an ExecuteCommandAction")
	assert.Equal(t, validCmd, found.Command)
}

// TestValidateCustomCommandActions_DeduplicatesPaths ensures that the
// same broken command path used across multiple species is only flagged
// once in the returned invalid set.
func TestValidateCustomCommandActions_DeduplicatesPaths(t *testing.T) {
	t.Parallel()

	missing := "/tmp/birdnet_go_does_not_exist_dedup_2a4c.sh"

	processor := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"american robin": {
							Actions: []conf.SpeciesAction{
								{Type: "ExecuteCommand", Command: missing},
							},
						},
						"house sparrow": {
							Actions: []conf.SpeciesAction{
								{Type: "ExecuteCommand", Command: missing},
							},
						},
					},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	processor.validateCustomCommandActions(processor.Settings)

	var recorded int
	processor.invalidCommandPaths.Range(func(key, _ any) bool {
		if key == missing {
			recorded++
		}
		return true
	})
	assert.Equal(t, 1, recorded, "same broken path across multiple species must be recorded once")
}

// TestValidateCustomCommandActions_HotReload_NewSpeciesValidatedOnFirstUse
// is a regression test for the hot-reload gap where a species custom
// ExecuteCommand action added *after* Processor.New() (i.e. via a UI
// settings save) was never revalidated, letting the original per-call
// Sentry spam return under a different code path. The sync.Map-backed
// per-dispatch gate (markCommandPathInvalidIfBroken) must stat the new
// path on its first dispatch and cache the skip from then on.
func TestValidateCustomCommandActions_HotReload_NewSpeciesValidatedOnFirstUse(t *testing.T) {
	t.Parallel()

	// Start with an empty species config — nothing to validate at startup.
	processor := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}
	processor.validateCustomCommandActions(processor.Settings)

	var startupFlagged int
	processor.invalidCommandPaths.Range(func(_, _ any) bool {
		startupFlagged++
		return true
	})
	require.Equal(t, 0, startupFlagged,
		"empty species config must not flag anything at startup")

	// Simulate a post-startup hot reload: the settings store is mutated
	// in place (mirroring how ControlMonitor updates settings without
	// recreating the Processor), adding a species with a broken path.
	missing := "/tmp/birdnet_go_hot_reload_missing_9e21b.sh"
	processor.Settings.Realtime.Species.Config = map[string]conf.SpeciesConfig{
		"american robin": {
			Threshold: 0.8,
			Actions: []conf.SpeciesAction{
				{Type: "ExecuteCommand", Command: missing},
			},
		},
	}

	// First dispatch after the reload must trigger per-call validation,
	// flag the path as invalid, and omit the ExecuteCommandAction.
	det := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)
	actions := processor.getActionsForItem(&det)
	for _, action := range actions {
		if _, ok := action.(*ExecuteCommandAction); ok {
			t.Fatalf("hot-reloaded invalid command %q must not produce an ExecuteCommandAction", missing)
		}
	}

	_, flagged := processor.invalidCommandPaths.Load(missing)
	assert.True(t, flagged, "first dispatch must cache the broken path")

	// Now swap the broken path for an executable that definitely exists
	// (same hot-reload shape as before). A *new* detection should now
	// produce an ExecuteCommandAction — the previous sync.Map entry
	// belongs to the old broken path, not this one, so the new path is
	// stat'd fresh on its first use.
	validCmd := resolveValidExecutable(t)

	processor.Settings.Realtime.Species.Config = map[string]conf.SpeciesConfig{
		"american robin": {
			Threshold: 0.8,
			Actions: []conf.SpeciesAction{
				{Type: "ExecuteCommand", Command: validCmd},
			},
		},
	}

	det2 := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)
	actions2 := processor.getActionsForItem(&det2)

	var found *ExecuteCommandAction
	for _, a := range actions2 {
		if ea, ok := a.(*ExecuteCommandAction); ok {
			found = ea
			break
		}
	}
	require.NotNil(t, found,
		"hot-reloaded valid command path must produce an ExecuteCommandAction on first dispatch")
	assert.Equal(t, validCmd, found.Command)
}

// TestGetActionsForItem_BrokenCommand_RespectsExecuteDefaultsFalse is a
// regression test for the case where a species explicitly opts out of
// default actions (ExecuteDefaults=false) and the only configured
// custom action is an ExecuteCommand whose path fails validation.
//
// Before the fix, the per-dispatch gate skipped the broken command but
// the function then fell through to the default-action fallback, so a
// user who had disabled default actions for a species suddenly got
// DB/SSE/MQTT/audio fallbacks back as soon as their script broke. The
// fix tracks "had at least one custom action configured" separately
// from "ended up with at least one runnable custom action" and
// short-circuits the default fallback when ExecuteDefaults is false.
func TestGetActionsForItem_BrokenCommand_RespectsExecuteDefaultsFalse(t *testing.T) {
	t.Parallel()

	missing := "/tmp/birdnet_go_broken_no_defaults_3f7a1.sh"

	processor := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				// Default-action sources enabled so that any
				// accidental fall-through would produce a non-empty
				// action list — this is what makes the assertion
				// meaningful.
				Log: struct {
					Enabled bool   `yaml:"enabled" json:"enabled"`
					Path    string `yaml:"path" json:"path"`
				}{
					Enabled: true,
				},
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"american robin": {
							Threshold: 0.8,
							Actions: []conf.SpeciesAction{
								{
									Type:            "ExecuteCommand",
									Command:         missing,
									ExecuteDefaults: false, // Explicit opt-out
								},
							},
						},
					},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	// Pre-populate the invalid path cache so the gate fires without
	// having to rely on the file system. Use the current timestamp so
	// the entry is well within the recheck window.
	processor.invalidCommandPaths.Store(missing, timeNowForTest())

	det := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)
	actions := processor.getActionsForItem(&det)

	assert.Empty(t, actions,
		"broken ExecuteCommand with ExecuteDefaults=false must yield zero actions, not silent default fallbacks")
	for _, a := range actions {
		switch a.(type) {
		case *LogAction, *DatabaseAction, *SSEAction, *MqttAction:
			t.Fatalf("unexpected default action %T leaked through when ExecuteDefaults=false", a)
		}
	}
}

// TestGetActionsForItem_BrokenCommand_AllowsExecuteDefaultsTrue is the
// inverse case: when the user opts INTO default actions for a species
// and the custom command happens to be broken, the default fallbacks
// should still run. This guards against an over-eager fix to the
// previous regression where we accidentally disable defaults for
// everyone.
func TestGetActionsForItem_BrokenCommand_AllowsExecuteDefaultsTrue(t *testing.T) {
	t.Parallel()

	missing := "/tmp/birdnet_go_broken_with_defaults_84c2e.sh"

	processor := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Log: struct {
					Enabled bool   `yaml:"enabled" json:"enabled"`
					Path    string `yaml:"path" json:"path"`
				}{
					Enabled: true,
				},
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"american robin": {
							Threshold: 0.8,
							Actions: []conf.SpeciesAction{
								{
									Type:            "ExecuteCommand",
									Command:         missing,
									ExecuteDefaults: true, // Opt INTO default fallback
								},
							},
						},
					},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	processor.invalidCommandPaths.Store(missing, timeNowForTest())

	det := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)
	actions := processor.getActionsForItem(&det)

	// LogAction is part of the default set when Log.Enabled is true,
	// and is the cheapest one to assert presence of without needing a
	// real datastore wired up. Its presence implies the default-action
	// fallback fired even though the custom command was skipped.
	var hasLog bool
	for _, a := range actions {
		if _, ok := a.(*LogAction); ok {
			hasLog = true
			break
		}
	}
	assert.True(t, hasLog,
		"broken ExecuteCommand with ExecuteDefaults=true must still produce default actions (LogAction)")
	// And the broken ExecuteCommand itself must NOT be in the list.
	for _, a := range actions {
		if _, ok := a.(*ExecuteCommandAction); ok {
			t.Fatalf("broken ExecuteCommand must not be registered as an action")
		}
	}
}

// TestMarkCommandPathInvalidIfBroken_RecheckClearsFixedPath is a
// regression test for the sticky-cache issue called out by CodeRabbit.
// A path that was once cached as invalid must be re-validated after the
// recheck TTL elapses; if it now passes, the cache entry is removed and
// the corresponding ExecuteCommand action becomes active again without
// any process restart.
func TestMarkCommandPathInvalidIfBroken_RecheckClearsFixedPath(t *testing.T) {
	t.Parallel()

	validCmd := resolveValidExecutable(t)

	processor := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	// Seed the cache with a stale failure timestamp older than the
	// recheck TTL, simulating a path that was broken at some point in
	// the past. The actual validCmd path is now (and was, throughout
	// this test) a real executable.
	stale := timeNowForTest().Add(-2 * invalidCommandPathRecheckTTL)
	processor.invalidCommandPaths.Store(validCmd, stale)

	// First call: TTL expired, path is good — gate should clear the
	// cache entry and report the path as usable.
	got := processor.markCommandPathInvalidIfBroken("turdus migratorius", validCmd)
	assert.False(t, got, "valid path with expired-TTL cache entry must re-validate cleanly")

	_, stillCached := processor.invalidCommandPaths.Load(validCmd)
	assert.False(t, stillCached, "fixed path must be removed from invalidCommandPaths after successful re-validation")
}

// TestMarkCommandPathInvalidIfBroken_RecentFailureSkipsRecheck verifies
// that a recent failure timestamp suppresses re-stating the path. This
// is the cheap-fast-path the original sync.Map design was built for —
// it must remain in place so the per-detection hot path stays a single
// map lookup for paths that legitimately stay broken.
func TestMarkCommandPathInvalidIfBroken_RecentFailureSkipsRecheck(t *testing.T) {
	t.Parallel()

	validCmd := resolveValidExecutable(t)

	processor := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	// Seed the cache with a *recent* failure timestamp even though the
	// path is actually fine. The gate must trust the cache and report
	// the path as invalid without re-stating, otherwise the hot-path
	// suppression that the original change was built for is lost.
	processor.invalidCommandPaths.Store(validCmd, timeNowForTest())

	got := processor.markCommandPathInvalidIfBroken("turdus migratorius", validCmd)
	assert.True(t, got, "recent failure must suppress action without re-stating the path")

	_, stillCached := processor.invalidCommandPaths.Load(validCmd)
	assert.True(t, stillCached, "recent-failure cache entry must not be cleared by a non-recheck call")
}

// timeNowForTest is a tiny indirection so the timestamp helper used by
// the recheck-TTL tests is named in one place. Keeping it as a local
// helper rather than time.Now() inline makes it obvious in the test
// body that the value is "now relative to test wall-clock", which is
// the same source of truth markCommandPathInvalidIfBroken uses.
func timeNowForTest() time.Time { return time.Now() }

// TestGetActionsForItem_UnimplementedAction_FallsThroughToDefaults is a
// regression test for the case where a species has ONLY unimplemented
// action types (e.g. the SendNotification placeholder) configured with
// ExecuteDefaults=false. A previous iteration of the "respect
// ExecuteDefaults=false when the custom action was dropped" fix was too
// broad: it tracked "any custom action was configured" rather than "a
// validated ExecuteCommand path was dropped", so a species with only
// unimplemented custom actions silently returned an empty action list,
// dropping DB/SSE/MQTT/audio fallbacks without any error. That meant
// users lost detections for the species until the new action type
// shipped. The fix narrows the short-circuit to broken command paths
// only, letting unimplemented types fall through to the default set.
func TestGetActionsForItem_UnimplementedAction_FallsThroughToDefaults(t *testing.T) {
	t.Parallel()

	processor := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				// Enable the Log default action so the presence of
				// the fallback is observable without wiring up a real
				// datastore / MQTT client / SSE broadcaster.
				Log: struct {
					Enabled bool   `yaml:"enabled" json:"enabled"`
					Path    string `yaml:"path" json:"path"`
				}{
					Enabled: true,
				},
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"american robin": {
							Threshold: 0.8,
							Actions: []conf.SpeciesAction{
								{
									Type:            "SendNotification",
									ExecuteDefaults: false, // Explicit opt-out
								},
							},
						},
					},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	det := testDetectionWithSpecies("American Robin", "Turdus migratorius", 0.95)
	actions := processor.getActionsForItem(&det)

	// The species had only an unimplemented custom action type and no
	// matching switch branch added anything to the action list. The
	// function must NOT short-circuit to an empty result — it must
	// fall through to the default action set so detections keep
	// flowing. Asserting LogAction is present is the cheapest way to
	// confirm the default-action branch fired.
	require.NotEmpty(t, actions,
		"unimplemented custom action with ExecuteDefaults=false must fall through to default actions, not yield an empty list")

	var hasLog bool
	for _, a := range actions {
		if _, ok := a.(*LogAction); ok {
			hasLog = true
			break
		}
	}
	assert.True(t, hasLog,
		"default LogAction must be present when the only configured custom action is unimplemented")

	// And the unimplemented action must not have silently produced an
	// ExecuteCommandAction.
	for _, a := range actions {
		if _, ok := a.(*ExecuteCommandAction); ok {
			t.Fatalf("unimplemented SendNotification must not produce an ExecuteCommandAction")
		}
	}
}

// TestMarkCommandPathInvalidIfBroken_ReNotifiesAfterTTL is a regression
// test for the sync.Map sticky-entry bug in the TTL recheck path. When
// a cached failure's TTL expires and the path is still broken, the
// previous implementation left the stale entry in place and called
// LoadOrStore, which returned loaded=true because of the stale value.
// The notification branch is gated on loaded=false, so operators only
// ever saw one notification per process lifetime instead of one per
// recheck window.
//
// The fix deletes the stale entry BEFORE the re-validation slow path
// runs, so a still-failing path goes through LoadOrStore's loaded=false
// branch and re-emits the notification. Because the notification
// helper is a no-op when the notification service is not initialized
// (the test binary does not init it), the assertion checks the
// observable sync.Map state: the entry was recreated with a fresh
// timestamp (i.e. the entry got deleted and re-added) rather than left
// in place at the original stale value.
func TestMarkCommandPathInvalidIfBroken_ReNotifiesAfterTTL(t *testing.T) {
	t.Parallel()

	// A path that does not exist and cannot be fixed mid-test.
	missing := "/tmp/birdnet_go_re_notify_after_ttl_b19e7.sh"

	processor := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{},
				},
			},
		},
		EventTracker: NewEventTracker(0),
	}

	// Seed the cache with a stale failure from well before the
	// recheck window. If the stale-entry bug resurfaces, the re-check
	// path below will see loaded=true and leave this exact timestamp
	// untouched instead of refreshing it.
	stale := timeNowForTest().Add(-2 * invalidCommandPathRecheckTTL)
	processor.invalidCommandPaths.Store(missing, stale)

	// Re-check the still-broken path. The gate must report "invalid",
	// delete the stale entry, re-stat the path, and re-store a fresh
	// timestamp. The notification branch runs inside the
	// just-deleted->LoadOrStore(loaded=false) window (verified below
	// by checking the timestamp was refreshed rather than left at the
	// stale value).
	got := processor.markCommandPathInvalidIfBroken("turdus migratorius", missing)
	assert.True(t, got, "still-broken path must be reported as invalid after TTL expiry")

	v, stillCached := processor.invalidCommandPaths.Load(missing)
	require.True(t, stillCached, "still-broken path must remain cached under a fresh timestamp after re-validation")

	stamp, ok := v.(time.Time)
	require.True(t, ok, "cached entry must be a time.Time")

	// The fresh timestamp must be strictly newer than the stale one
	// we seeded. If the sticky-entry bug returns, `stamp` will equal
	// `stale` because LoadOrStore observed the old value and left it
	// alone.
	assert.True(t, stamp.After(stale),
		"re-check of still-broken path must refresh the cached timestamp (old=%v, new=%v); same timestamp means the stale entry was not deleted before LoadOrStore and the notification branch never ran",
		stale, stamp)

	// The refreshed timestamp must also be within the current recheck
	// window — i.e. roughly "now" — so a subsequent call in the next
	// instant still hits the fast path.
	assert.Less(t, time.Since(stamp), invalidCommandPathRecheckTTL,
		"refreshed timestamp must be inside the active recheck window")
}
