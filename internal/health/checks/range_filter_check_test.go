package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/health"
)

// installGuidance is the install hint the not-installed message must carry.
const installGuidance = "BirdNET Geomodel v3.0 from the models list (Settings > Analysis > Models)"

// TestRangeFilterCheck covers the range-filter health states from the #852 fix plan:
// a silent fail-open (no filter active while a location is configured) must be flagged
// critical; a fallback to the embedded TFLite filter or a geomodel that matched zero
// species must warn; and the no-location and fully-healthy cases must stay healthy.
// It also covers the missing-geomodel diagnosis: Perch or BirdNET v3.0 loaded without the
// BirdNET Geomodel is never healthy. The "active embedded filter is healthy" row documents
// that a v2.4-only install on the MData backend stays healthy.
func TestRangeFilterCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		info           RangeFilterStatusInfo
		wantStatus     health.Status
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:       "unavailable engine is skipped",
			info:       RangeFilterStatusInfo{},
			wantStatus: health.StatusSkipped,
		},
		{
			name:       "no participant loaded is not applicable",
			info:       RangeFilterStatusInfo{Available: true, ParticipantsLoaded: false, LocationConfigured: true, Active: false},
			wantStatus: health.StatusHealthy,
		},
		{
			name:       "no location configured is not applicable",
			info:       RangeFilterStatusInfo{Available: true, ParticipantsLoaded: true, LocationConfigured: false, Active: false},
			wantStatus: health.StatusHealthy,
		},
		{
			name:       "location configured but no filter active is fail-open",
			info:       RangeFilterStatusInfo{Available: true, ParticipantsLoaded: true, LocationConfigured: true, Active: false},
			wantStatus: health.StatusCritical,
		},
		{
			name:       "fell back to embedded filter warns",
			info:       RangeFilterStatusInfo{Available: true, ParticipantsLoaded: true, LocationConfigured: true, Active: true, FellBack: true},
			wantStatus: health.StatusWarning,
		},
		{
			name:       "geomodel mapped zero species warns",
			info:       RangeFilterStatusInfo{Available: true, ParticipantsLoaded: true, LocationConfigured: true, Active: true, GeomodelActive: true, MappedSpecies: 0},
			wantStatus: health.StatusWarning,
		},
		{
			name:       "active geomodel with mapped species is healthy",
			info:       RangeFilterStatusInfo{Available: true, ParticipantsLoaded: true, LocationConfigured: true, Active: true, GeomodelActive: true, MappedSpecies: 6500},
			wantStatus: health.StatusHealthy,
		},
		{
			name:       "active embedded filter is healthy",
			info:       RangeFilterStatusInfo{Available: true, ParticipantsLoaded: true, LocationConfigured: true, Active: true},
			wantStatus: health.StatusHealthy,
		},
		{
			name: "geomodel not installed with location and no backend is critical",
			info: RangeFilterStatusInfo{
				Available: true, ParticipantsLoaded: true, LocationConfigured: true,
				GeomodelIssue: GeomodelIssueNotInstalled, UncoveredModels: []string{"Google Perch v2"},
			},
			wantStatus:   health.StatusCritical,
			wantContains: []string{rangeFilterInactivePrefix, installGuidance, "Google Perch v2"},
		},
		{
			name: "geomodel installed but not selected warns without install guidance",
			info: RangeFilterStatusInfo{
				Available: true, ParticipantsLoaded: true, LocationConfigured: true, Active: true,
				GeomodelIssue: GeomodelIssueNotSelected, UncoveredModels: []string{"Google Perch v2"},
			},
			wantStatus:     health.StatusWarning,
			wantContains:   []string{"birdnet.rangefilter.model", "BIRDNET_RANGEFILTER_MODEL"},
			wantNotContain: []string{installGuidance},
		},
		{
			name: "geomodel load failure while fell back reports the load failure",
			info: RangeFilterStatusInfo{
				Available: true, ParticipantsLoaded: true, LocationConfigured: true, Active: true, FellBack: true,
				GeomodelIssue: GeomodelIssueLoadFailed, UncoveredModels: []string{"Google Perch v2"},
			},
			wantStatus:     health.StatusWarning,
			wantContains:   []string{"could not be loaded", installGuidance},
			wantNotContain: []string{"failed to load; using"},
		},
		{
			name: "geomodel issue without model names uses the fallback names",
			info: RangeFilterStatusInfo{
				Available: true, ParticipantsLoaded: true, LocationConfigured: true,
				GeomodelIssue: GeomodelIssueNotInstalled,
			},
			wantStatus:   health.StatusCritical,
			wantContains: []string{rangeFilterFallbackModels},
		},
		{
			name: "geomodel issue without location is still not healthy",
			info: RangeFilterStatusInfo{
				Available: true, ParticipantsLoaded: true, LocationConfigured: false,
				GeomodelIssue: GeomodelIssueNotInstalled, UncoveredModels: []string{"Google Perch v2"},
			},
			wantStatus:     health.StatusWarning,
			wantContains:   []string{installGuidance},
			wantNotContain: []string{rangeFilterInactivePrefix},
		},
		{
			name: "failed reload with retained backend and no location warns",
			info: RangeFilterStatusInfo{
				Available: true, ParticipantsLoaded: true, LocationConfigured: false, Active: true,
				ReloadFailed: true, UncoveredModels: []string{"Google Perch v2"},
			},
			wantStatus:   health.StatusWarning,
			wantContains: []string{"reload failed", "Google Perch v2"},
		},
		{
			name: "failed reload with no backend and a location stays critical",
			info: RangeFilterStatusInfo{
				Available: true, ParticipantsLoaded: true, LocationConfigured: true, ReloadFailed: true,
			},
			wantStatus:   health.StatusCritical,
			wantContains: []string{rangeFilterInactiveMsg},
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
			for _, want := range tt.wantContains {
				assert.Contains(t, result.Message, want)
			}
			for _, unwanted := range tt.wantNotContain {
				assert.NotContains(t, result.Message, unwanted)
			}
		})
	}
}

// TestRangeFilterCheck_GeomodelIssueIsNeverHealthy pins the maintainer's invariant over
// the whole grid: whenever a model needing the BirdNET Geomodel runs without it, the check
// is Critical when a location is set and no backend is active, and Warning otherwise.
func TestRangeFilterCheck_GeomodelIssueIsNeverHealthy(t *testing.T) {
	t.Parallel()

	issues := []GeomodelIssue{GeomodelIssueNotInstalled, GeomodelIssueNotSelected, GeomodelIssueLoadFailed}
	for _, issue := range issues {
		for _, location := range []bool{false, true} {
			for _, active := range []bool{false, true} {
				for _, fellBack := range []bool{false, true} {
					info := RangeFilterStatusInfo{
						Available: true, ParticipantsLoaded: true, LocationConfigured: location,
						Active: active, FellBack: fellBack, GeomodelIssue: issue,
					}
					want := health.StatusWarning
					if location && !active {
						want = health.StatusCritical
					}
					result := NewRangeFilterCheck(func() RangeFilterStatusInfo { return info }).Run(t.Context())
					assert.Equal(t, want, result.Status, "issue=%s location=%t active=%t fellBack=%t", issue, location, active, fellBack)
				}
			}
		}
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
