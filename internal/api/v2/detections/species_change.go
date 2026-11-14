// internal/api/v2/detections/species_change.go
package detections

import (
	"context"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Species change messages and log text (file-local).
const (
	speciesChangeV2RequiredMessage = "Changing the species of a detection requires the enhanced (v2) database"
	speciesChangeInvalidMessage    = "Invalid species change request"
	speciesChangeMissingMessage    = "A scientific name is required"
	speciesChangeLockedMessage     = "Detection is locked and its species cannot be changed"
	speciesChangeUnknownMessage    = "Unknown species"
	speciesChangeFailedMessage     = "Failed to change the species of the detection"
	speciesChangeV2RequiredLog     = "detection species change requested without the enhanced database"
	speciesChangeRequestEndedLog   = "detection species change request ended before the change finished"
	speciesChangeRequestedLog      = "Detection species change requested"
)

// speciesChanger changes the species of a detection. The enhanced (v2) detection
// repository implements it; the shared detection repository interface does not,
// because the legacy store behind it has no such operation.
type speciesChanger interface {
	ChangeSpecies(ctx context.Context, id uint, scientificName string) error
}

// SpeciesChangeRequest is the body of PUT /detections/:id/species.
type SpeciesChangeRequest struct {
	// ScientificName is the species the detection is changed to.
	ScientificName string `json:"scientificName"`
}

// ChangeDetectionSpecies handles PUT /api/v2/detections/:id/species. It sets the
// species of a detection as a reviewer's correction and answers 200 with
// {"status":"success"}, also for the species the detection already has. It answers
// 409 unless the enhanced (v2) database is in use and for a locked detection, 400
// for an id that is not a positive integer that fits in 32 bits, an unreadable
// body, a missing name and an unknown species, and 404 for a missing detection.
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

	if err := c.speciesChanger.ChangeSpecies(ctx.Request().Context(), uint(n), name); err != nil {
		if handled, respErr := c.respondIfRequestEnded(ctx, err, speciesChangeRequestEndedLog); handled {
			return respErr
		}
		switch {
		case errors.Is(err, repository.ErrDetectionNotFound):
			return c.HandleError(ctx, err, detectionNotFoundMessage, http.StatusNotFound)
		case errors.Is(err, repository.ErrDetectionLocked):
			return c.HandleError(ctx, err, speciesChangeLockedMessage, http.StatusConflict)
		case errors.Is(err, repository.ErrLabelNotFound):
			return c.HandleError(ctx, err, speciesChangeUnknownMessage, http.StatusBadRequest)
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
