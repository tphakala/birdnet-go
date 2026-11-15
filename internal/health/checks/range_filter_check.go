package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// rangeFilterInactivePrefix leads the geomodel issue message when the filter is also inactive.
const rangeFilterInactivePrefix = rangeFilterInactiveMsg + ". "

// Range filter check name and messages. Message texts that name the BirdNET Geomodel
// guide the user to the models list, the only place the geomodel can be installed from.
const (
	rangeFilterCheckName       = "range_filter"
	rangeFilterUnavailableMsg  = "Range filter status not available (analysis engine not running)"
	rangeFilterInactiveMsg     = "Range filter inactive: detections are not filtered by location (running unfiltered)"
	rangeFilterActiveMsg       = "Range filter active"
	rangeFilterNoParticipant   = "Range filtering not applicable (no loaded model participates in range filtering)"
	rangeFilterNoLocationMsg   = "Range filtering not applicable (no location configured)"
	rangeFilterFellBackMsg     = "Configured geomodel range filter failed to load; using the classifier's embedded range filter"
	rangeFilterMappedZeroMsg   = "Range filter geomodel matched no classifier species; all detections would be filtered out (check the labels file)"
	rangeFilterFallbackModels  = "Perch and BirdNET v3.0"
	rangeFilterNotInstalledFmt = "BirdNET Geomodel is not installed, so these models are not filtered by location: %s. Install BirdNET Geomodel v3.0 from the models list (Settings > Analysis > Models)."
	rangeFilterNotSelectedFmt  = "BirdNET Geomodel is installed but the range filter is configured to use another model, so these models are not filtered by location: %s. Remove the range filter model override (birdnet.rangefilter.model and birdnet.rangefilter.modelpath in the configuration file, or the BIRDNET_RANGEFILTER_MODEL and BIRDNET_RANGEFILTER_MODELPATH environment variables) to use the BirdNET Geomodel."
	rangeFilterLoadFailedFmt   = "BirdNET Geomodel could not be loaded, so these models are not filtered by location: %s. Check the log, and install or reinstall BirdNET Geomodel v3.0 from the models list (Settings > Analysis > Models)."
	rangeFilterReloadFailedMsg = "The last range filter reload failed; the previously loaded range filter is still in use. Check the log and restart BirdNET-Go to retry."
	rangeFilterNotCoveredFmt   = " Not covered: %s."
)

// GeomodelIssue diagnoses why a loaded model that needs the BirdNET Geomodel is not served
// by it. The zero value means no issue. It mirrors classifier.GeomodelIssue so this
// package does not import the classifier.
type GeomodelIssue string

const (
	// GeomodelIssueNotInstalled means the BirdNET Geomodel files are not installed.
	GeomodelIssueNotInstalled GeomodelIssue = "not_installed"
	// GeomodelIssueNotSelected means the geomodel is installed but the range filter
	// configuration selects another model.
	GeomodelIssueNotSelected GeomodelIssue = "not_selected"
	// GeomodelIssueLoadFailed means the geomodel is selected but not serving.
	GeomodelIssueLoadFailed GeomodelIssue = "load_failed"
)

// RangeFilterStatusInfo is a minimal snapshot of range-filter state for health reporting.
// It is populated from the classifier's range-filter status so the health package does
// not depend on classifier internals.
type RangeFilterStatusInfo struct {
	// Available reports whether the analysis engine could be queried. False (the zero
	// value) means the status is unknown and the check is skipped.
	Available bool
	// ParticipantsLoaded reports whether any loaded acoustic model participates in range
	// filtering (BirdNET v2.4, v3.0, Perch). When false there is nothing to range-filter,
	// so a missing backend is "not applicable" rather than a fault.
	ParticipantsLoaded bool
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
	// GeomodelIssue is non-empty when a loaded model that needs the BirdNET Geomodel
	// (Perch, BirdNET v3.0) is not served by it. Such a state is never healthy.
	GeomodelIssue GeomodelIssue
	// UncoveredModels lists the display names of loaded models the active backend does
	// not cover.
	UncoveredModels []string
	// ReloadFailed reports that the last range filter reload failed.
	ReloadFailed bool
	// Backend names the loaded backend kind (geomodel_v3, mdata_v2, mdata_v1, none).
	Backend string
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
func (c *RangeFilterCheck) Name() string { return rangeFilterCheckName }

// Category returns the analysis category.
func (c *RangeFilterCheck) Category() health.Category { return health.CategoryAnalysis }

// geomodelIssueResult maps a geomodel issue to a status and message. It is Critical when
// a location is set and no backend filters (fail-open), otherwise Warning.
func geomodelIssueResult(s *RangeFilterStatusInfo) (status health.Status, message string) {
	models := rangeFilterFallbackModels
	if len(s.UncoveredModels) > 0 {
		models = strings.Join(s.UncoveredModels, ", ")
	}
	var format string
	switch s.GeomodelIssue {
	case GeomodelIssueNotSelected:
		format = rangeFilterNotSelectedFmt
	case GeomodelIssueLoadFailed:
		format = rangeFilterLoadFailedFmt
	default:
		format = rangeFilterNotInstalledFmt
	}
	message = fmt.Sprintf(format, models)
	if s.LocationConfigured && !s.Active {
		return health.StatusCritical, rangeFilterInactivePrefix + message
	}
	return health.StatusWarning, message
}

// reloadFailedMessage builds the retained-backend reload failure message.
func reloadFailedMessage(uncovered []string) string {
	message := rangeFilterReloadFailedMsg
	if len(uncovered) > 0 {
		message += fmt.Sprintf(rangeFilterNotCoveredFmt, strings.Join(uncovered, ", "))
	}
	return message
}

// Run evaluates range-filter health. A loaded model that needs the BirdNET Geomodel
// without it is evaluated before the no-location case, so it is never reported healthy.
func (c *RangeFilterCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getStatus == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	s := c.getStatus()

	status := health.StatusHealthy
	message := rangeFilterActiveMsg
	switch {
	case !s.Available:
		status = health.StatusSkipped
		message = rangeFilterUnavailableMsg
	case !s.ParticipantsLoaded:
		// No loaded model participates in range filtering (e.g. a Bat-only or BSG-only
		// install): there is nothing to filter by location, so a missing backend is not
		// a fault. Checked before Active so this does not report a spurious Critical.
		message = rangeFilterNoParticipant
	case s.GeomodelIssue != "":
		status, message = geomodelIssueResult(&s)
	case s.ReloadFailed && s.Active:
		status = health.StatusWarning
		message = reloadFailedMessage(s.UncoveredModels)
	case !s.LocationConfigured:
		// Range filtering only filters by location; without one it is not applicable.
		message = rangeFilterNoLocationMsg
	case !s.Active:
		// A location is set but no filter loaded: detections run unfiltered with no
		// other visible signal. This is the silent fail-open the fix must surface.
		status = health.StatusCritical
		message = rangeFilterInactiveMsg
	case s.FellBack:
		status = health.StatusWarning
		message = rangeFilterFellBackMsg
	case s.GeomodelActive && s.MappedSpecies == 0:
		status = health.StatusWarning
		message = rangeFilterMappedZeroMsg
	}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     status,
		Message:    message,
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
		Details: map[string]any{
			"available":           s.Available,
			"participants_loaded": s.ParticipantsLoaded,
			"location_configured": s.LocationConfigured,
			"active":              s.Active,
			"fell_back":           s.FellBack,
			"geomodel_active":     s.GeomodelActive,
			"mapped_species":      s.MappedSpecies,
			"backend":             s.Backend,
			"geomodel_issue":      string(s.GeomodelIssue),
			"uncovered_models":    s.UncoveredModels,
			"reload_failed":       s.ReloadFailed,
		},
	}
}
