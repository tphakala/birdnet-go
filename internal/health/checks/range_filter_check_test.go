package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/health"
)

// TestRangeFilterCheck covers the range-filter health states from the #852 fix plan:
// a silent fail-open (no filter active while a location is configured) must be flagged
// critical; a fallback to the embedded TFLite filter or a geomodel that matched zero
// species must warn; and the no-location and fully-healthy cases must stay healthy.
func TestRangeFilterCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		info       RangeFilterStatusInfo
		wantStatus health.Status
	}{
		{
			name:       "no location configured is not applicable",
			info:       RangeFilterStatusInfo{LocationConfigured: false, Active: false},
			wantStatus: health.StatusHealthy,
		},
		{
			name:       "location configured but no filter active is fail-open",
			info:       RangeFilterStatusInfo{LocationConfigured: true, Active: false},
			wantStatus: health.StatusCritical,
		},
		{
			name:       "fell back to embedded filter warns",
			info:       RangeFilterStatusInfo{LocationConfigured: true, Active: true, FellBack: true},
			wantStatus: health.StatusWarning,
		},
		{
			name:       "geomodel mapped zero species warns",
			info:       RangeFilterStatusInfo{LocationConfigured: true, Active: true, GeomodelActive: true, MappedSpecies: 0},
			wantStatus: health.StatusWarning,
		},
		{
			name:       "active geomodel with mapped species is healthy",
			info:       RangeFilterStatusInfo{LocationConfigured: true, Active: true, GeomodelActive: true, MappedSpecies: 6500},
			wantStatus: health.StatusHealthy,
		},
		{
			name:       "active embedded filter is healthy",
			info:       RangeFilterStatusInfo{LocationConfigured: true, Active: true},
			wantStatus: health.StatusHealthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			check := NewRangeFilterCheck(func() RangeFilterStatusInfo { return tt.info })
			assert.Equal(t, "range_filter", check.Name())
			assert.Equal(t, health.CategoryAnalysis, check.Category())

			result := check.Run(t.Context())
			assert.Equal(t, tt.wantStatus, result.Status, "status for %+v", tt.info)
			assert.NotEmpty(t, result.Message)
		})
	}
}

// TestRangeFilterCheck_NoProvider verifies the check is skipped when no status provider
// is wired (e.g. before the analysis pipeline is ready).
func TestRangeFilterCheck_NoProvider(t *testing.T) {
	t.Parallel()
	check := NewRangeFilterCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}
