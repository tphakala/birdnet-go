package checks

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// RangeFilterStatusInfo is a minimal snapshot of range-filter state for health reporting.
// It is populated from the classifier's range-filter status so the health package does
// not depend on classifier internals.
type RangeFilterStatusInfo struct {
	// LocationConfigured reports whether a location is set; range filtering only filters
	// by location, so without one it is not applicable.
	LocationConfigured bool
	// Active reports whether a range-filter backend is loaded. False means the app runs
	// fail-open (no species filtering) which, with a location configured, is a fault.
	Active bool
	// FellBack reports that the configured ONNX geomodel could not be loaded and the
	// classifier fell back to its embedded TFLite range filter.
	FellBack bool
	// GeomodelActive reports that the active backend is the mapped geomodel.
	GeomodelActive bool
	// MappedSpecies is the number of classifier species matched to the geomodel; only
	// meaningful when GeomodelActive. Zero means the geomodel filters out everything.
	MappedSpecies int
}

// RangeFilterCheck reports whether geographic range filtering is active when expected.
// It surfaces the silent fail-open described in the range-filter robustness work: when a
// location is configured but no range filter is loaded, detections run unfiltered without
// any visible signal.
type RangeFilterCheck struct {
	getStatus func() RangeFilterStatusInfo
}

// NewRangeFilterCheck creates a RangeFilterCheck using the given status provider.
func NewRangeFilterCheck(getStatus func() RangeFilterStatusInfo) *RangeFilterCheck {
	return &RangeFilterCheck{getStatus: getStatus}
}

// Name returns the check identifier.
func (c *RangeFilterCheck) Name() string { return "range_filter" }

// Category returns the analysis category.
func (c *RangeFilterCheck) Category() health.Category { return health.CategoryAnalysis }

// Run evaluates range-filter health.
func (c *RangeFilterCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getStatus == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	s := c.getStatus()

	status := health.StatusHealthy
	message := "Range filter active"
	switch {
	case !s.LocationConfigured:
		// Range filtering only filters by location; without one it is not applicable.
		message = "Range filtering not applicable (no location configured)"
	case !s.Active:
		// A location is set but no filter loaded: detections run unfiltered with no
		// other visible signal. This is the silent fail-open the fix must surface.
		status = health.StatusCritical
		message = "Range filter inactive: detections are not filtered by location (running unfiltered)"
	case s.FellBack:
		status = health.StatusWarning
		message = "Configured geomodel range filter failed to load; using the classifier's embedded range filter"
	case s.GeomodelActive && s.MappedSpecies == 0:
		status = health.StatusWarning
		message = "Range filter geomodel matched no classifier species; all detections would be filtered out (check the labels file)"
	}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     status,
		Message:    message,
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
		Details: map[string]any{
			"location_configured": s.LocationConfigured,
			"active":              s.Active,
			"fell_back":           s.FellBack,
			"geomodel_active":     s.GeomodelActive,
			"mapped_species":      s.MappedSpecies,
		},
	}
}
