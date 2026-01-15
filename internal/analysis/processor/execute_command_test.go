package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestExecuteCommandAction_WithoutParameters tests that ExecuteCommand actions
// are executed even when no parameters are specified.
// Bug: https://github.com/tphakala/birdnet-go/discussions/1757
// Root cause: processor.go line 1116 had condition `if len(actionConfig.Parameters) > 0`
// which prevented commands without parameters from being added to the action list.
func TestExecuteCommandAction_WithoutParameters(t *testing.T) {
	t.Parallel()

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
						Command:    "/usr/local/bin/notify.sh",
						Parameters: []string{}, // Empty parameters - this was the bug!
					},
				},
			},
			expectAction:    true,
			expectedCommand: "/usr/local/bin/notify.sh",
		},
		{
			name: "command with nil parameters should still create action",
			speciesConfig: conf.SpeciesConfig{
				Threshold: 0.8,
				Actions: []conf.SpeciesAction{
					{
						Type:       "ExecuteCommand",
						Command:    "/usr/local/bin/alert.sh",
						Parameters: nil, // Nil parameters - should also work
					},
				},
			},
			expectAction:    true,
			expectedCommand: "/usr/local/bin/alert.sh",
		},
		{
			name: "command with parameters should create action with params",
			speciesConfig: conf.SpeciesConfig{
				Threshold: 0.8,
				Actions: []conf.SpeciesAction{
					{
						Type:       "ExecuteCommand",
						Command:    "/usr/local/bin/script.sh",
						Parameters: []string{"CommonName", "Confidence"},
					},
				},
			},
			expectAction:    true,
			expectedCommand: "/usr/local/bin/script.sh",
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
									Command:    "/usr/local/bin/simple.sh",
									Parameters: []string{}, // No parameters
								},
								{
									Type:       "ExecuteCommand",
									Command:    "/usr/local/bin/detailed.sh",
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

	assert.True(t, commands["/usr/local/bin/simple.sh"], "simple.sh should be in actions")
	assert.True(t, commands["/usr/local/bin/detailed.sh"], "detailed.sh should be in actions")
}

// TestExecuteCommandAction_ExecuteDefaultsWithNoParams tests that executeDefaults
// works correctly even when the custom command has no parameters.
func TestExecuteCommandAction_ExecuteDefaultsWithNoParams(t *testing.T) {
	t.Parallel()

	processor := &Processor{
		Settings: &conf.Settings{
			Debug: true,
			Realtime: conf.RealtimeSettings{
				Log: struct {
					Enabled bool   `json:"enabled"`
					Path    string `json:"path"`
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
									Command:         "/usr/local/bin/notify.sh",
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
