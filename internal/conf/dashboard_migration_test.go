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
			expectElements: 4,
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
			expectElements: 4,
			expectLimit:    30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			migrated := tt.settings.MigrateDashboardLayout()
			assert.Equal(t, tt.expectMigrated, migrated)
			assert.Len(t, tt.settings.Realtime.Dashboard.Layout.Elements, tt.expectElements)

			if tt.expectMigrated {
				elements := tt.settings.Realtime.Dashboard.Layout.Elements

				// Verify element types, ordering, and enabled state
				assert.Equal(t, "search", elements[0].Type)
				assert.True(t, elements[0].Enabled)
				assert.Equal(t, "daily-summary", elements[1].Type)
				assert.True(t, elements[1].Enabled)
				assert.Equal(t, "currently-hearing", elements[2].Type)
				assert.True(t, elements[2].Enabled)
				assert.Equal(t, "detections-grid", elements[3].Type)
				assert.True(t, elements[3].Enabled)

				// Verify stable IDs are generated
				assert.Equal(t, "search-0", elements[0].ID)
				assert.Equal(t, "daily-summary-0", elements[1].ID)
				assert.Equal(t, "currently-hearing-0", elements[2].ID)
				assert.Equal(t, "detections-grid-0", elements[3].ID)

				// Verify summaryLimit is migrated into the element config
				require.NotNil(t, elements[1].Summary)
				assert.Equal(t, tt.expectLimit, elements[1].Summary.SummaryLimit)

				// Verify deprecated root SummaryLimit is zeroed out
				assert.Equal(t, 0, tt.settings.Realtime.Dashboard.SummaryLimit)
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
	assert.Len(t, settings.Realtime.Dashboard.Layout.Elements, 4)
	assert.Equal(t, 0, settings.Realtime.Dashboard.SummaryLimit) // zeroed after migration

	migrated2 := settings.MigrateDashboardLayout()
	require.False(t, migrated2)
	assert.Len(t, settings.Realtime.Dashboard.Layout.Elements, 4)
}
