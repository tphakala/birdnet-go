// internal/api/v2/detections/species_change.go
package detections

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/classifier"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// Species change messages and log text (file-local).
const (
	speciesChangeV2RequiredMessage = "Changing the species of a detection requires the enhanced (v2) database"
	speciesChangeInvalidMessage    = "Invalid species change request"
	speciesChangeMissingMessage    = "A scientific name is required"
	speciesChangeNoSpeciesMessage  = "A scientific name with a species part is required"
	speciesChangeLockedMessage     = "Detection is locked and its species cannot be changed"
	speciesChangeUnknownMessage    = "Species is not on the species lists of the loaded models or the range filter"
	speciesChangeFailedMessage     = "Failed to change the species of the detection"
	speciesChangeV2RequiredLog     = "detection species change requested without the enhanced database"
	speciesChangeRequestEndedLog   = "detection species change request ended before the change finished"
	speciesChangeRequestedLog      = "Detection species change requested"

	// speciesChangeUnknownKey is the i18n key the dialog maps to its unknown species
	// message.
	speciesChangeUnknownKey = "detections.detail.species.unknownSpecies"
)

// speciesChanger changes the species of a detection. The enhanced (v2) detection
// repository implements it; the shared detection repository interface does not,
// because the legacy store behind it has no such operation.
type speciesChanger interface {
	ChangeSpecies(ctx context.Context, id uint, scientificName string, offer *repository.SpeciesOffer) error
}

// speciesKnowledge is the part of classifier.ModelSpecies the species change
// reads. *classifier.ModelSpecies satisfies it; tests inject a fixed fake because
// the snapshot has no exported constructor.
type speciesKnowledge interface {
	// ScientificName returns the model's own scientific name of the species with
	// the given canonical key (speciesindex.CanonicalKey).
	ScientificName(modelID, key string) (string, bool)
}

// liveSpeciesKnowledge returns the live orchestrator's per-model species snapshot,
// or nil when there is no orchestrator (no knowledge, not an error).
func (c *Handler) liveSpeciesKnowledge() speciesKnowledge {
	orch, err := c.GetBirdNETInstance()
	if err != nil {
		return nil
	}
	return orch.ModelSpecies()
}

// SpeciesChangeRequest is the body of PUT /detections/:id/species.
type SpeciesChangeRequest struct {
	// ScientificName is the species the detection is changed to.
	ScientificName string `json:"scientificName"`
}

// ChangeDetectionSpecies handles PUT /api/v2/detections/:id/species. It sets the
// species of a detection as a reviewer's correction and answers 200 with
// {"status":"success"}, also for the species the detection already has unless it is
// locked. It answers 409 unless the enhanced (v2) database is in use and for a
// locked detection (checked before the species is looked up), 400 for an id that is
// not a positive integer that fits in 32 bits, an unreadable body, a missing name
// or one without a species part (plain), and a species with no label of the
// detection's model that the loaded models and the range filter do not offer (with
// an error_key), and 404 for a missing detection.
func (c *Handler) ChangeDetectionSpecies(ctx echo.Context) error {
	if c.speciesChanger == nil || !datastoreV2.IsEnhancedDatabase() {
		return c.respondEnhancedDatabaseRequired(ctx, speciesChangeV2RequiredMessage, speciesChangeV2RequiredLog)
	}

	n, err := parseDetectionID(ctx)
	if err != nil {
		return c.HandleError(ctx, err, invalidDetectionIDMessage, http.StatusBadRequest)
	}

	var req SpeciesChangeRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, speciesChangeInvalidMessage, http.StatusBadRequest)
	}
	name := strings.TrimSpace(req.ScientificName)
	if name == "" {
		return c.HandleError(ctx, nil, speciesChangeMissingMessage, http.StatusBadRequest)
	}
	key := speciesindex.CanonicalKey(name)
	if key == "" {
		return c.HandleError(ctx, nil, speciesChangeNoSpeciesMessage, http.StatusBadRequest)
	}

	if err := c.speciesChanger.ChangeSpecies(ctx.Request().Context(), uint(n), name, c.speciesOffer(name, key)); err != nil {
		if handled, respErr := c.respondIfRequestEnded(ctx, err, speciesChangeRequestEndedLog); handled {
			return respErr
		}
		switch {
		case errors.Is(err, repository.ErrDetectionNotFound):
			return c.HandleError(ctx, err, detectionNotFoundMessage, http.StatusNotFound)
		case errors.Is(err, repository.ErrDetectionLocked):
			return c.HandleError(ctx, err, speciesChangeLockedMessage, http.StatusConflict)
		case errors.Is(err, repository.ErrLabelNotFound):
			return c.HandleErrorWithKey(ctx, err, speciesChangeUnknownMessage, http.StatusBadRequest,
				speciesChangeUnknownKey, map[string]any{"species": name})
		default:
			return c.HandleError(ctx, err, speciesChangeFailedMessage, http.StatusInternalServerError)
		}
	}

	// Every accepted request is logged until an audit trail exists, including one for
	// the species the detection already has, which writes nothing.
	c.LogInfoIfEnabled(speciesChangeRequestedLog,
		logger.Uint64("detection_id", n),
		logger.String("scientific_name", name),
	)
	c.invalidateDetectionCache()
	return ctx.JSON(http.StatusOK, map[string]string{"status": "success"})
}

// speciesOffer describes the species name (whose canonical key is key) from the
// sources the change dialog offers it from: the loaded models, the all-species name
// map, the range filter's included list and, before any of those is built, the
// BirdNET labels of the settings. It returns nil when none of them offers it. All
// sources are read per request.
func (c *Handler) speciesOffer(name, key string) *repository.SpeciesOffer {
	var know speciesKnowledge
	if c.speciesKnowledge != nil {
		know = c.speciesKnowledge()
	} else {
		know = c.liveSpeciesKnowledge()
	}

	// Registry IDs are sorted so the offer, and with it the label name when several
	// models spell the species differently, does not depend on map order.
	var models []repository.OfferedModel
	if know != nil {
		for _, id := range slices.Sorted(maps.Keys(classifier.ModelRegistry)) {
			if sci, ok := know.ScientificName(id, key); ok {
				models = append(models, repository.OfferedModel{Model: classifier.DetectionModelInfoForID(id), ScientificName: sci})
			}
		}
	}
	if len(models) > 0 || c.listsOfferSpecies(name, key) {
		return &repository.SpeciesOffer{Models: models}
	}
	return nil
}

// listsOfferSpecies reports whether a species list of the dialog offers the
// species: the all-species name map (by exact name), the range filter's included
// list (by canonical key, not IsSpeciesIncluded, whose fallback is a prefix match)
// or, mirroring the all-species endpoint's startup fallback, the settings' BirdNET
// labels while neither the name map nor the orchestrator's labels exist yet.
func (c *Handler) listsOfferSpecies(name, key string) bool {
	var names map[string]string
	if c.loadCommonNameMap != nil {
		names = c.loadCommonNameMap()
	}
	if _, ok := names[name]; ok {
		return true
	}
	if settings := c.CurrentSettings(); settings != nil && hasSpeciesKey(settings.GetIncludedSpecies(), key) {
		return true
	}
	if len(names) > 0 {
		return false
	}
	if orch, err := c.GetBirdNETInstance(); err == nil && len(orch.AllLabels()) > 0 {
		return false
	}
	settings := c.ControllerSettings()
	return settings != nil && hasSpeciesKey(settings.BirdNET.Labels, key)
}

// hasSpeciesKey reports whether a label has the canonical species key.
func hasSpeciesKey(labels []string, key string) bool {
	return slices.ContainsFunc(labels, func(label string) bool {
		return speciesindex.CanonicalKey(label) == key
	})
}
