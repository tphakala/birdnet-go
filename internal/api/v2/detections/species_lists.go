// internal/api/v2/detections/species_lists.go
//
// Managed species-list endpoints (always-include and confirmed) for the
// analytics "Manage" view. These mirror the ignore-list toggle but act on the
// Realtime.Species.Include / Realtime.Species.Confirmed lists. The confirmed
// list is analytics-only and does not affect detection processing, so its toggle
// does not trigger settings side-effects.
package detections

import (
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Species-list toggle actions returned to clients.
const (
	speciesActionAdded   = "added"
	speciesActionRemoved = "removed"
)

// SpeciesListRequest represents the request body for toggling a species in a
// managed species list (always-include or confirmed).
type SpeciesListRequest struct {
	CommonName string `json:"common_name"`
}

// SpeciesListToggleResponse is returned by the include/confirm toggle endpoints.
type SpeciesListToggleResponse struct {
	CommonName string `json:"common_name"`
	Action     string `json:"action"` // "added" or "removed"
	Present    bool   `json:"present"`
}

// SpeciesListResponse is returned by the included/confirmed list endpoints.
type SpeciesListResponse struct {
	Species []string `json:"species"`
	Count   int      `json:"count"`
}

// IncludeSpecies toggles a species in the always-include list (adds if absent, removes if present).
func (c *Handler) IncludeSpecies(ctx echo.Context) error {
	return c.toggleManagedSpecies(ctx,
		func(s *conf.Settings) []string { return s.Realtime.Species.Include },
		func(s *conf.Settings, list []string) { s.Realtime.Species.Include = list },
		true, "include")
}

// GetIncludedSpecies returns the always-include species list.
func (c *Handler) GetIncludedSpecies(ctx echo.Context) error {
	species := nonNilSpeciesList(c.getSettingsOrFallback().Realtime.Species.Include)

	return ctx.JSON(http.StatusOK, SpeciesListResponse{
		Species: species,
		Count:   len(species),
	})
}

// ConfirmSpecies toggles a species in the confirmed list (adds if absent, removes if present).
// The confirmed list is analytics-only and does not affect detection processing, so no
// settings side-effects are triggered.
func (c *Handler) ConfirmSpecies(ctx echo.Context) error {
	return c.toggleManagedSpecies(ctx,
		func(s *conf.Settings) []string { return s.Realtime.Species.Confirmed },
		func(s *conf.Settings, list []string) { s.Realtime.Species.Confirmed = list },
		false, "confirm")
}

// GetConfirmedSpecies returns the confirmed species list.
func (c *Handler) GetConfirmedSpecies(ctx echo.Context) error {
	species := nonNilSpeciesList(c.getSettingsOrFallback().Realtime.Species.Confirmed)

	return ctx.JSON(http.StatusOK, SpeciesListResponse{
		Species: species,
		Count:   len(species),
	})
}

// nonNilSpeciesList clones list, substituting a non-nil empty slice when list
// is nil. slices.Clone preserves nilness, which would otherwise encode an
// unset list as JSON null instead of [], inconsistent with GetExcludedSpecies
// (which builds its response via make([]string, len(...))).
func nonNilSpeciesList(list []string) []string {
	if list == nil {
		return []string{}
	}
	return slices.Clone(list)
}

// toggleManagedSpecies binds and validates the request, toggles the species in
// the list selected by listOf/setList, and returns the standard toggle response.
func (c *Handler) toggleManagedSpecies(ctx echo.Context, listOf func(*conf.Settings) []string, setList func(*conf.Settings, []string), triggerSideEffects bool, opLabel string) error {
	req := &SpeciesListRequest{}
	if err := ctx.Bind(req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}
	req.CommonName = strings.TrimSpace(req.CommonName)
	if req.CommonName == "" {
		return c.HandleError(ctx, nil, "Missing species name", http.StatusBadRequest)
	}

	action, present, err := c.toggleSpeciesInList(req.CommonName, listOf, setList, triggerSideEffects)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to update species list", http.StatusInternalServerError)
	}

	c.LogInfoIfEnabled("Species "+opLabel+" toggled",
		logger.String("species", req.CommonName),
		logger.String("action", action),
		logger.Bool("present", present),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, SpeciesListToggleResponse{
		CommonName: req.CommonName,
		Action:     action,
		Present:    present,
	})
}

// toggleSpeciesInList toggles a species in one of the realtime species lists
// (e.g. include or confirmed) under the settings mutex, persisting the change
// through the standard publish/save path. listOf reads the current slice and
// setList writes the updated slice back onto a cloned Settings. When
// triggerSideEffects is true, settings change side-effects (e.g. range filter
// rebuild) are triggered after saving. Returns the action ("added"/"removed")
// and the resulting membership state.
func (c *Handler) toggleSpeciesInList(species string, listOf func(*conf.Settings) []string, setList func(*conf.Settings, []string), triggerSideEffects bool) (action string, present bool, err error) {
	if species == "" {
		return "", false, nil
	}

	// Serialise this read-modify-write against concurrent settings saves so an
	// out-of-band StoreSettings cannot interleave between read and publish.
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	current := c.getSettingsOrFallback()

	// Resolve the incoming name to its scientific form for MATCHING purposes
	// only (mirrors the Exclude list's resolveExcludeName/excludeEntryMatches
	// convention: canonicalize the target once, then compare each stored entry
	// against it, which also resolves the entry's own alias). This catches an
	// entry stored as the scientific name (e.g. typed directly into the
	// Settings editor) against an incoming current-locale common name, or vice
	// versa - not just a case difference. It is comparison-only: the value
	// actually stored (below, on add) is still the caller's verbatim `species`
	// string, not this resolved form, so Include's range-filter override
	// (internal/classifier/range_filter.go resolveOverrideLabels) keeps
	// operating on unchanged stored strings; Confirmed has no other consumer
	// so this is purely a UX improvement there. Known residual limitation: the
	// common-to-scientific map reflects only the currently active BirdNET
	// locale (name_maps.go buildNameMaps), so an entry stored as a common name
	// under a locale that is no longer active still won't resolve - only a
	// same-locale common/scientific mismatch is fixed here.
	canonicalTarget := c.resolveExcludeName(species)
	matchesTarget := func(s string) bool { return c.excludeEntryMatches(s, canonicalTarget) }

	wasPresent := slices.ContainsFunc(listOf(current), matchesTarget)

	updated := conf.CloneSettings(current)
	if wasPresent {
		setList(updated, slices.DeleteFunc(listOf(updated), matchesTarget))
		action = speciesActionRemoved
		present = false
	} else {
		setList(updated, append(listOf(updated), species))
		action = speciesActionAdded
		present = true
	}

	if err := c.publishAndSaveSettings(current, updated); err != nil {
		return "", wasPresent, err
	}

	if triggerSideEffects {
		if handleErr := c.handleSettingsChanges(current, updated); handleErr != nil {
			apicore.GetLogger().Warn("Failed to trigger settings side-effects after species list change",
				logger.Error(handleErr),
				logger.String("species", species),
				logger.String("action", action))
		}
	}

	return action, present, nil
}
