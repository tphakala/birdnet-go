package detections

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// deduplicateIDs returns a new slice with duplicate IDs removed, preserving order.
func deduplicateIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result
}

// maxBatchSize is the maximum number of detection IDs allowed in a single batch request.
const maxBatchSize = 500

// BatchIDsRequest represents a batch request containing a list of detection IDs.
type BatchIDsRequest struct {
	IDs []string `json:"ids"`
}

// BatchReviewRequest represents a batch review request with IDs and verification status.
type BatchReviewRequest struct {
	IDs      []string `json:"ids"`
	Verified string   `json:"verified"`
}

// BatchLockRequest represents a batch lock/unlock request with IDs and lock state.
type BatchLockRequest struct {
	IDs    []string `json:"ids"`
	Locked bool     `json:"locked"`
}

// BatchResolveRequest represents a query to resolve to a list of detection IDs.
type BatchResolveRequest struct {
	QueryType string `json:"queryType"`
	Species   string `json:"species,omitempty"`
	Date      string `json:"date,omitempty"`
	Search    string `json:"search,omitempty"`
	Hour      string `json:"hour,omitempty"`
	Duration  int    `json:"duration,omitempty"`
}

// BatchResult represents the outcome of a batch operation.
type BatchResult struct {
	Processed int `json:"processed"`
	Skipped   int `json:"skipped"`
}

// BatchResolveResult represents the resolved list of detection IDs for a query.
type BatchResolveResult struct {
	IDs   []string `json:"ids"`
	Count int      `json:"count"`
}

// BatchDeleteDetections deletes multiple detections by ID, skipping locked ones.
func (c *Handler) BatchDeleteDetections(ctx echo.Context) error {
	var req BatchIDsRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	if len(req.IDs) == 0 {
		return c.HandleError(ctx, fmt.Errorf("no IDs provided"), "At least one ID is required", http.StatusBadRequest)
	}
	if len(req.IDs) > maxBatchSize {
		return c.HandleError(ctx, fmt.Errorf("batch size %d exceeds maximum %d", len(req.IDs), maxBatchSize),
			"Batch size exceeds maximum", http.StatusBadRequest)
	}

	processed, skipped := c.deleteNotesByIDs(deduplicateIDs(req.IDs))

	c.invalidateDetectionCache()

	return ctx.JSON(http.StatusOK, BatchResult{
		Processed: processed,
		Skipped:   skipped,
	})
}

// deleteNotesByIDs deletes the given detections, skipping locked ones, and removes
// any associated audio/spectrogram files. It returns the number of deletions
// performed and the number skipped (missing or locked). Callers are responsible
// for deduplicating IDs and invalidating the detection cache.
func (c *Handler) deleteNotesByIDs(ids []string) (deleted, skipped int) {
	for _, idStr := range ids {
		note, err := c.DS.Get(idStr)
		if err != nil {
			c.LogWarnIfEnabled("Delete: failed to get detection",
				logger.String("id", idStr),
				logger.Error(err))
			skipped++
			continue
		}
		if note.Locked {
			skipped++
			continue
		}

		clipName := note.ClipName
		if err := c.DS.Delete(idStr); err != nil {
			c.LogWarnIfEnabled("Delete: failed to delete detection",
				logger.String("id", idStr),
				logger.Error(err))
			skipped++
			continue
		}

		deleted++
		if clipName != "" {
			c.removeDetectionFiles(clipName)
		}
	}
	return deleted, skipped
}

// BatchReviewDetections sets the verification status on multiple detections, skipping locked ones.
func (c *Handler) BatchReviewDetections(ctx echo.Context) error {
	var req BatchReviewRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	if len(req.IDs) == 0 {
		return c.HandleError(ctx, fmt.Errorf("no IDs provided"), "At least one ID is required", http.StatusBadRequest)
	}
	if len(req.IDs) > maxBatchSize {
		return c.HandleError(ctx, fmt.Errorf("batch size %d exceeds maximum %d", len(req.IDs), maxBatchSize),
			"Batch size exceeds maximum", http.StatusBadRequest)
	}

	verification, err := parseVerificationStatus(req.Verified)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid verification status", http.StatusBadRequest)
	}
	if !verification.IsSet {
		return c.HandleError(ctx, fmt.Errorf("verified field is required"), "Verification status is required", http.StatusBadRequest)
	}

	ids := deduplicateIDs(req.IDs)
	var processed, skipped int
	for _, idStr := range ids {
		note, err := c.DS.Get(idStr)
		if err != nil {
			c.LogWarnIfEnabled("Batch review: failed to get detection",
				logger.String("id", idStr),
				logger.Error(err))
			skipped++
			continue
		}

		if note.Locked {
			skipped++
			continue
		}

		if err := c.AddReview(note.ID, verification.Verified); err != nil {
			c.LogWarnIfEnabled("Batch review: failed to set verification",
				logger.String("id", idStr),
				logger.Error(err))
			skipped++
			continue
		}
		processed++
	}

	c.invalidateDetectionCache()

	return ctx.JSON(http.StatusOK, BatchResult{
		Processed: processed,
		Skipped:   skipped,
	})
}

// BatchLockDetections locks or unlocks multiple detections. Already-locked detections
// are skipped when locking; all detections are processed when unlocking.
func (c *Handler) BatchLockDetections(ctx echo.Context) error {
	var req BatchLockRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	if len(req.IDs) == 0 {
		return c.HandleError(ctx, fmt.Errorf("no IDs provided"), "At least one ID is required", http.StatusBadRequest)
	}
	if len(req.IDs) > maxBatchSize {
		return c.HandleError(ctx, fmt.Errorf("batch size %d exceeds maximum %d", len(req.IDs), maxBatchSize),
			"Batch size exceeds maximum", http.StatusBadRequest)
	}

	ids := deduplicateIDs(req.IDs)
	var processed, skipped int
	for _, idStr := range ids {
		note, err := c.DS.Get(idStr)
		if err != nil {
			c.LogWarnIfEnabled("Batch lock: failed to get detection",
				logger.String("id", idStr),
				logger.Error(err))
			skipped++
			continue
		}

		if req.Locked && note.Locked {
			skipped++
			continue
		}

		if err := c.AddLock(note.ID, req.Locked); err != nil {
			c.LogWarnIfEnabled("Batch lock: failed to set lock state",
				logger.String("id", idStr),
				logger.Error(err))
			skipped++
			continue
		}
		processed++
	}

	c.invalidateDetectionCache()

	return ctx.JSON(http.StatusOK, BatchResult{
		Processed: processed,
		Skipped:   skipped,
	})
}

// BatchResolveDetections resolves a query to a list of detection IDs without modifying any data.
// Returns an error if the matching set exceeds maxBatchSize.
func (c *Handler) BatchResolveDetections(ctx echo.Context) error {
	var req BatchResolveRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	duration := req.Duration
	if duration == 0 && req.Hour != "" {
		duration = 1
	}

	params := &detectionQueryParams{
		QueryType:  req.QueryType,
		Species:    req.Species,
		Date:       req.Date,
		Search:     req.Search,
		Hour:       req.Hour,
		Duration:   duration,
		NumResults: maxBatchSize + 1,
		Offset:     0,
	}

	notes, totalCount, err := c.getDetectionsByQueryType(params)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to resolve detections", http.StatusInternalServerError)
	}

	if totalCount > int64(maxBatchSize) {
		return c.HandleError(ctx,
			fmt.Errorf("query matched %d detections, maximum is %d", totalCount, maxBatchSize),
			"Too many matching detections, narrow your filters", http.StatusBadRequest)
	}

	ids := make([]string, 0, len(notes))
	for i := range notes {
		ids = append(ids, strconv.FormatUint(uint64(notes[i].ID), 10))
	}

	return ctx.JSON(http.StatusOK, BatchResolveResult{
		IDs:   ids,
		Count: len(ids),
	})
}

// SpeciesDeleteRequest is the request body for deleting all detections of a species.
type SpeciesDeleteRequest struct {
	ScientificName string `json:"scientific_name"`
}

// SpeciesDeleteResult reports the outcome of a species-wide delete.
type SpeciesDeleteResult struct {
	Deleted int `json:"deleted"`
	Skipped int `json:"skipped"`
}

// speciesNoteIDsDatastore is the optional datastore capability required to resolve
// every detection ID for a species. Datastores that do not implement it cause
// DeleteSpeciesDetections to return HTTP 501.
type speciesNoteIDsDatastore interface {
	GetSpeciesNoteIDs(ctx context.Context, scientificName string) ([]string, error)
}

// DeleteSpeciesDetections deletes every (unlocked) detection for the given
// scientific name. Locked detections are skipped and counted, mirroring the
// batch delete semantics. Returns HTTP 501 when the active datastore cannot
// resolve a species' detection IDs.
func (c *Handler) DeleteSpeciesDetections(ctx echo.Context) error {
	var req SpeciesDeleteRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}
	req.ScientificName = strings.TrimSpace(req.ScientificName)
	if req.ScientificName == "" {
		return c.HandleError(ctx, fmt.Errorf("no scientific name provided"),
			"Scientific name is required", http.StatusBadRequest)
	}

	ds, ok := c.DS.(speciesNoteIDsDatastore)
	if !ok {
		return c.HandleError(ctx, fmt.Errorf("datastore does not support species note lookup"),
			"Species deletion is not supported by the active datastore", http.StatusNotImplemented)
	}

	ids, err := ds.GetSpeciesNoteIDs(ctx.Request().Context(), req.ScientificName)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to look up detections for species", http.StatusInternalServerError)
	}

	deleted, skipped := c.deleteNotesByIDs(deduplicateIDs(ids))

	c.invalidateDetectionCache()

	c.LogInfoIfEnabled("Species detections deleted",
		logger.String("scientific_name", req.ScientificName),
		logger.Int("deleted", deleted),
		logger.Int("skipped", skipped),
		logger.String("ip", ctx.RealIP()))

	return ctx.JSON(http.StatusOK, SpeciesDeleteResult{
		Deleted: deleted,
		Skipped: skipped,
	})
}
