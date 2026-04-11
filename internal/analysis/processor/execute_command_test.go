package processor

import (
	"os"
	"testing"

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
