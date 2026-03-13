package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettings_MigrateDashboardLayout(t *testing.T) {
	tests := []struct {
		name           string
		settings       Settings
		expectMigrated bool
		expectElements int
		expectLimit    int
	}{
		{
			name: "migrate empty layout with existing summaryLimit",
			settings: Settings{
				Realtime: RealtimeSettings{
					Dashboard: Dashboard{
						SummaryLimit: 50,
					},
				},
			},
			expectMigrated: true,
			expectElements: 3,
			expectLimit:    50,
		},
		{
			name: "skip migration if layout already has elements",
			settings: Settings{
				Realtime: RealtimeSettings{
					Dashboard: Dashboard{
						SummaryLimit: 50,
						Layout: DashboardLayout{
							Elements: []DashboardElement{
								{Type: "daily-summary", Enabled: true},
							},
						},
					},
				},
			},
			expectMigrated: false,
			expectElements: 1,
		},
		{
			name: "migrate with default summaryLimit when zero",
			settings: Settings{
				Realtime: RealtimeSettings{
					Dashboard: Dashboard{
						SummaryLimit: 0,
					},
				},
			},
			expectMigrated: true,
			expectElements: 3,
			expectLimit:    100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			migrated := tt.settings.MigrateDashboardLayout()
			assert.Equal(t, tt.expectMigrated, migrated)
			assert.Len(t, tt.settings.Realtime.Dashboard.Layout.Elements, tt.expectElements)

			if tt.expectMigrated {
				elements := tt.settings.Realtime.Dashboard.Layout.Elements
				assert.Equal(t, "daily-summary", elements[0].Type)
				assert.True(t, elements[0].Enabled)
				assert.Equal(t, "currently-hearing", elements[1].Type)
				assert.True(t, elements[1].Enabled)
				assert.Equal(t, "detections-grid", elements[2].Type)
				assert.True(t, elements[2].Enabled)

				require.NotNil(t, elements[0].Summary)
				assert.Equal(t, tt.expectLimit, elements[0].Summary.SummaryLimit)
			}
		})
	}
}

func TestSettings_MigrateDashboardLayout_Idempotent(t *testing.T) {
	settings := Settings{
		Realtime: RealtimeSettings{
			Dashboard: Dashboard{SummaryLimit: 75},
		},
	}

	migrated1 := settings.MigrateDashboardLayout()
	require.True(t, migrated1)
	assert.Len(t, settings.Realtime.Dashboard.Layout.Elements, 3)

	migrated2 := settings.MigrateDashboardLayout()
	require.False(t, migrated2)
	assert.Len(t, settings.Realtime.Dashboard.Layout.Elements, 3)
}
