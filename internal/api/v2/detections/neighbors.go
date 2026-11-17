// internal/api/v2/detections/neighbors.go
package detections

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Detection neighbors messages and log text (file-local).
const (
	neighborsV2RequiredMessage = "Detection neighbors require the enhanced (v2) database"
	neighborsLoadFailedMessage = "Failed to load detection neighbors"
	neighborsRequestEndedLog   = "detection neighbors request ended before the read finished"
	neighborsV2RequiredLog     = "detection neighbors requested without the enhanced database"
)

// neighborReader finds the detections next to a detection. The enhanced (v2)
// detection repository implements it; the shared detection repository interface
// does not, because the legacy store behind it has no such operation.
type neighborReader interface {
	GetNeighborIDs(ctx context.Context, id uint) (*repository.DetectionNeighbors, error)
}

// DetectionNeighborsResponse is the body of GET /detections/:id/neighbors. Every
// key is always present; a neighbor that does not exist is null.
type DetectionNeighborsResponse struct {
	// DetectionID is the detection the neighbors belong to.
	DetectionID uint `json:"detectionId"`
	// Previous is the greatest detection ID below DetectionID.
	Previous *uint `json:"previous"`
	// Next is the smallest detection ID above DetectionID.
	Next *uint `json:"next"`
	// PreviousSameSpecies is the greatest ID below DetectionID among detections of the same species.
	PreviousSameSpecies *uint `json:"previousSameSpecies"`
	// NextSameSpecies is the smallest ID above DetectionID among detections of the same species.
	NextSameSpecies *uint `json:"nextSameSpecies"`
}

// GetDetectionNeighbors handles GET /api/v2/detections/:id/neighbors. It answers
// 200 with the IDs of the detections next to the detection in record ID order,
// overall and of the same species, 409 unless the enhanced (v2) database is in
// use, 400 for an id that is not a positive integer that fits in 32 bits, 404 for
// a missing detection and 500 for a database error.
func (c *Handler) GetDetectionNeighbors(ctx echo.Context) error {
	if c.neighbors == nil || !datastoreV2.IsEnhancedDatabase() {
		return c.respondEnhancedDatabaseRequired(ctx, neighborsV2RequiredMessage, neighborsV2RequiredLog)
	}

	n, err := parseDetectionID(ctx)
	if err != nil {
		return c.HandleError(ctx, err, invalidDetectionIDMessage, http.StatusBadRequest)
	}
	id := uint(n)

	neighbors, err := c.neighbors.GetNeighborIDs(ctx.Request().Context(), id)
	if err != nil {
		if handled, respErr := c.respondIfRequestEnded(ctx, err, neighborsRequestEndedLog); handled {
			return respErr
		}
		if errors.Is(err, repository.ErrDetectionNotFound) {
			return c.HandleError(ctx, err, detectionNotFoundMessage, http.StatusNotFound)
		}
		return c.HandleError(ctx, err, neighborsLoadFailedMessage, http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, DetectionNeighborsResponse{
		DetectionID:         id,
		Previous:            idOrNil(neighbors.Previous),
		Next:                idOrNil(neighbors.Next),
		PreviousSameSpecies: idOrNil(neighbors.PreviousSameSpecies),
		NextSameSpecies:     idOrNil(neighbors.NextSameSpecies),
	})
}

// idOrNil maps the repository's 0 for "no such detection" to nil.
func idOrNil(id uint) *uint {
	if id == 0 {
		return nil
	}
	return &id
}
